package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/containerready"
	"api-pacs/module/inference/infrastructure/service/types"
)

const (
	defaultReportRenderTimeout      = 60 * time.Second
	defaultReportMaxBytes           = int64(32 << 20)
	viewerModelInfoProbeTimeout     = 3 * time.Second
	reportContainerReadyTimeout     = 30 * time.Second
	reportContainerReadyPoll        = 500 * time.Millisecond
	maximumViewerStudyUIDLength     = 255
	reportContentTypeHTML           = "text/html"
	studyServiceStatusCompleted     = "completed"
	viewerRenderTimeoutEnvironment  = "VIEWER_REPORT_RENDER_TIMEOUT_SECONDS"
	viewerRenderMaxBytesEnvironment = "VIEWER_REPORT_MAX_BYTES"
)

// ViewerReportLimitsFromEnvironment reads the render timeout and response cap.
func ViewerReportLimitsFromEnvironment() (time.Duration, int64) {
	timeout := defaultReportRenderTimeout
	if seconds, err := strconv.Atoi(strings.TrimSpace(os.Getenv(viewerRenderTimeoutEnvironment))); err == nil && seconds > 0 {
		timeout = time.Duration(seconds) * time.Second
	}
	maxBytes := defaultReportMaxBytes
	if value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(viewerRenderMaxBytesEnvironment)), 10, 64); err == nil && value > 0 {
		maxBytes = value
	}
	return timeout, maxBytes
}

// viewerModelEntry is one tenant registration plus model-info when its
// container is already running. Listing never starts containers.
type viewerModelEntry struct {
	model entity.InferenceModel
	info  *dockerInferenceTypes.ModelInfo
}

func (entry viewerModelEntry) version() string {
	if entry.info != nil && strings.TrimSpace(entry.info.Version) != "" {
		return strings.TrimSpace(entry.info.Version)
	}
	if deployment := currentIngestionDeployment(entry.model); deployment != nil {
		return strings.TrimSpace(deployment.ModelVersion)
	}
	return ""
}

func (entry viewerModelEntry) artifactModelID() string {
	if entry.info != nil && strings.TrimSpace(entry.info.ModelID) != "" {
		return strings.TrimSpace(entry.info.ModelID)
	}
	if deployment := currentIngestionDeployment(entry.model); deployment != nil {
		return strings.TrimSpace(deployment.ArtifactModelID)
	}
	return ""
}

// viewerModelDirectory resolves study-service model names to registrations.
type viewerModelDirectory struct {
	entries []viewerModelEntry
	jobs    []entity.InferenceIngestionJob
}

func (service *InferenceQueryService) loadViewerModelDirectory(ctx context.Context, tenantID string) viewerModelDirectory {
	directory := viewerModelDirectory{}
	if service.InferenceQueryRepositoryInterface == nil {
		return directory
	}
	models, err := service.InferenceQueryRepositoryInterface.SelectInferenceModels(ctx, tenantID)
	if err != nil && err.Error() != apiError.MissingRecord {
		log.Printf("[Viewer results] cannot list model registrations tenant_id=%s err=%v", tenantID, err)
	}
	jobs, err := service.InferenceQueryRepositoryInterface.SelectInferenceIngestionJobs(&tenantID)
	if err != nil && err.Error() != apiError.MissingRecord {
		log.Printf("[Viewer results] cannot list ingestion jobs tenant_id=%s err=%v", tenantID, err)
	}
	for _, job := range jobs {
		if job.TenantID == tenantID {
			directory.jobs = append(directory.jobs, job)
		}
	}

	entries := make([]viewerModelEntry, 0, len(models))
	for _, model := range models {
		if model.TenantID != tenantID || strings.TrimSpace(model.ContainerID) == "" {
			continue
		}
		entries = append(entries, viewerModelEntry{model: model})
	}
	if service.DockerSDKInterface != nil && service.DockerInferenceAPIInterface != nil {
		group, groupCtx := errgroup.WithContext(ctx)
		group.SetLimit(inferenceModelInspectionConcurrency)
		for index := range entries {
			index := index
			group.Go(func() error {
				containerID := strings.TrimSpace(entries[index].model.ContainerID)
				info, err := service.DockerSDKInterface.GetContainerInfo(groupCtx, containerID)
				if err != nil || info.Lifecycle != dockerTypes.LifecycleRunning {
					return nil
				}
				name := strings.TrimPrefix(strings.TrimSpace(info.Name), "/")
				if name == "" {
					return nil
				}
				probeCtx, cancel := context.WithTimeout(groupCtx, viewerModelInfoProbeTimeout)
				defer cancel()
				response, err := service.DockerInferenceAPIInterface.GetModelInfo(probeCtx, name)
				if err == nil && response.Success {
					modelInfo := response.Data
					entries[index].info = &modelInfo
				}
				return nil
			})
		}
		_ = group.Wait()
	}
	sort.SliceStable(entries, func(left, right int) bool { return entries[left].model.ID < entries[right].model.ID })
	directory.entries = entries
	return directory
}

// match resolves a study-service model name (and version) to a registration:
// running model-info name first, then ingestion jobs for that model, then the
// registration display name. A version match wins within one tier.
func (directory viewerModelDirectory) match(modelName, modelVersion string) *viewerModelEntry {
	modelName = strings.TrimSpace(modelName)
	modelVersion = strings.TrimSpace(modelVersion)
	if modelName == "" {
		return nil
	}

	jobRefs := map[string]struct{}{}
	for _, job := range directory.jobs {
		if strings.EqualFold(strings.TrimSpace(job.ModelName), modelName) {
			for _, ref := range []string{job.ModelID, job.ContainerID} {
				if ref = strings.TrimSpace(ref); ref != "" {
					jobRefs[ref] = struct{}{}
				}
			}
		}
	}
	tiers := []func(viewerModelEntry) bool{
		func(entry viewerModelEntry) bool {
			return entry.info != nil && strings.EqualFold(strings.TrimSpace(entry.info.ModelName), modelName)
		},
		func(entry viewerModelEntry) bool {
			for _, ref := range []string{entry.model.ID, entry.model.ContainerID, entry.artifactModelID()} {
				if _, ok := jobRefs[strings.TrimSpace(ref)]; ok && strings.TrimSpace(ref) != "" {
					return true
				}
			}
			return false
		},
		func(entry viewerModelEntry) bool {
			return strings.EqualFold(strings.TrimSpace(entry.model.Name), modelName)
		},
	}
	for _, tier := range tiers {
		var first *viewerModelEntry
		for index := range directory.entries {
			entry := &directory.entries[index]
			if !tier(*entry) {
				continue
			}
			if modelVersion != "" && entry.version() == modelVersion {
				return entry
			}
			if first == nil {
				first = entry
			}
		}
		if first != nil {
			return first
		}
	}
	return nil
}

// GetStudyLatestModelResults proxies study-service latest results and joins
// tenant registrations, processing runs, and report capability.
func (service *InferenceQueryService) GetStudyLatestModelResults(ctx context.Context, data types.GetStudyLatestModelResults) (types.StudyLatestModelResults, error) {
	tenantID := strings.TrimSpace(data.TenantID)
	studyInstanceUID := strings.TrimSpace(data.StudyInstanceUID)
	if tenantID == "" || studyInstanceUID == "" || len(studyInstanceUID) > maximumViewerStudyUIDLength {
		return types.StudyLatestModelResults{}, errors.New(apiError.InvalidPayload)
	}
	response := types.StudyLatestModelResults{StudyInstanceUID: studyInstanceUID, Results: []types.StudyLatestModelResult{}}
	if service.StudyResultsProvider == nil {
		return types.StudyLatestModelResults{}, errors.New(apiError.InferenceResultServiceUnavailable)
	}

	latest, found, err := service.StudyResultsProvider.GetLatestStudyResults(ctx, tenantID, studyInstanceUID, data.Modality)
	if err != nil {
		log.Printf("[Viewer results] study-service latest-results failed tenant_id=%s err=%v", tenantID, err)
		return types.StudyLatestModelResults{}, errors.New(apiError.InferenceResultServiceUnavailable)
	}
	if !found || len(latest.Results) == 0 {
		return response, nil
	}

	directory := service.loadViewerModelDirectory(ctx, tenantID)
	runs := map[string]*entity.InferenceIngestionProcessingRun{}
	runExecutions := map[string][]entity.InferenceIngestionProcessingJob{}

	for _, result := range latest.Results {
		status := strings.TrimSpace(result.Status)
		if strings.TrimSpace(result.JobID) == "" || strings.TrimSpace(result.ModelName) == "" ||
			(status != "" && !strings.EqualFold(status, studyServiceStatusCompleted)) {
			continue
		}
		item := types.StudyLatestModelResult{
			ModelName:         strings.TrimSpace(result.ModelName),
			ModelVersion:      trimmedPointer(result.ModelVersion),
			Modality:          result.Modality,
			StudyServiceJobID: strings.TrimSpace(result.JobID),
			CompletedAt:       trimmedPointer(result.CompletedAt),
			SelectedViews:     normalizeSelectedViews(result.SelectedViews),
		}
		if entry := directory.match(item.ModelName, trimmedPointerValue(result.ModelVersion)); entry != nil {
			item.ModelID = nonEmptyStringPointer(entry.model.ID)
			item.ContainerID = nonEmptyStringPointer(entry.model.ContainerID)
			item.ArtifactModelID = nonEmptyStringPointer(entry.artifactModelID())
			item.ReportAvailable = entry.info != nil && entry.info.SupportsReportRendering() &&
				strings.EqualFold(strings.TrimSpace(entry.info.ModelName), item.ModelName)
		}

		if runID := trimmedPointerValue(result.ProcessingRunID); runID != "" {
			run, cached := runs[runID]
			if !cached {
				if loaded, runErr := service.InferenceProcessingRunRepositoryInterface.SelectProcessingRun(ctx, tenantID, runID); runErr == nil &&
					strings.TrimSpace(loaded.StudyInstanceUID) == studyInstanceUID {
					run = &loaded
				}
				runs[runID] = run
			}
			if run != nil {
				item.RunID = &run.ID
				trigger := run.RunTrigger
				item.Trigger = &trigger
				executions, listed := runExecutions[runID]
				if !listed {
					executions, _ = service.InferenceProcessingRunRepositoryInterface.ListProcessingRunExecutions(ctx, tenantID, runID)
					runExecutions[runID] = executions
				}
				item.ExecutionID = matchLatestResultExecution(executions, result)
			}
		}
		response.Results = append(response.Results, item)
	}
	return response, nil
}

// matchLatestResultExecution returns the local execution that produced the
// study-service job; nil means callers must use the legacy job report route.
func matchLatestResultExecution(executions []entity.InferenceIngestionProcessingJob, result types.StudyServiceLatestResult) *string {
	jobID := strings.TrimSpace(result.JobID)
	executionID := trimmedPointerValue(result.ProcessingExecutionID)
	for index := range executions {
		execution := &executions[index]
		if execution.Status != entity.InferenceIngestionProcessingJobStatusCompleted ||
			trimmedPointerValue(execution.StudyServiceJobID) != jobID ||
			strings.TrimSpace(execution.ModelName) != strings.TrimSpace(result.ModelName) {
			continue
		}
		if executionID != "" && strings.TrimSpace(execution.ID) != executionID {
			continue
		}
		id := execution.ID
		return &id
	}
	return nil
}

// normalizeSelectedViews keeps a JSON array as-is and maps anything else
// (absent, null, malformed) to null: "selection not recorded".
func normalizeSelectedViews(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' || !json.Valid(trimmed) {
		return nil
	}
	return append(json.RawMessage(nil), trimmed...)
}

// GetProcessingRunExecutionReport re-renders the stored result of one
// tenant-owned completed execution through its model container.
func (service *InferenceQueryService) GetProcessingRunExecutionReport(ctx context.Context, data types.GetProcessingRunExecutionReport) (types.ModelReport, error) {
	result, err := service.GetProcessingRunExecutionResult(ctx, types.GetProcessingRunExecutionResult{
		TenantID: data.TenantID, RunID: data.RunID, ExecutionID: data.ExecutionID,
	})
	if err != nil {
		return types.ModelReport{}, err
	}
	return service.renderStoredResult(ctx, strings.TrimSpace(data.TenantID), result.ModelName, trimmedPointerValue(result.ModelVersion), result.Result)
}

// GetStudyJobReport re-renders a legacy completed study-service job that has
// no run/execution IDs, after checking tenant and study ownership.
func (service *InferenceQueryService) GetStudyJobReport(ctx context.Context, data types.GetStudyJobReport) (types.ModelReport, error) {
	tenantID := strings.TrimSpace(data.TenantID)
	studyInstanceUID := strings.TrimSpace(data.StudyInstanceUID)
	jobID := strings.TrimSpace(data.StudyServiceJobID)
	if tenantID == "" || studyInstanceUID == "" || jobID == "" || len(studyInstanceUID) > maximumViewerStudyUIDLength {
		return types.ModelReport{}, errors.New(apiError.InvalidPayload)
	}
	if service.ProcessingResultProviderInterface == nil {
		return types.ModelReport{}, errors.New(apiError.InferenceResultServiceUnavailable)
	}
	job, found, err := service.ProcessingResultProviderInterface.GetJobResultByID(ctx, tenantID, jobID)
	if err != nil {
		return types.ModelReport{}, errors.New(apiError.InferenceResultServiceUnavailable)
	}
	if !found || strings.TrimSpace(job.JobID) != jobID ||
		strings.TrimSpace(job.StudyInstanceUID) != studyInstanceUID ||
		trimmedPointerValue(job.TenantID) != tenantID {
		return types.ModelReport{}, errors.New(apiError.MissingRecord)
	}
	if !strings.EqualFold(strings.TrimSpace(job.Status), studyServiceStatusCompleted) {
		return types.ModelReport{}, errors.New(apiError.InferenceExecutionResultNotAvailable)
	}
	if !validExecutionResultJSON(job.ResultJSON) {
		return types.ModelReport{}, errors.New(apiError.InferenceExecutionResultInvalid)
	}
	return service.renderStoredResult(ctx, tenantID, job.ModelName, trimmedPointerValue(job.ModelVersion), job.ResultJSON)
}

func (service *InferenceQueryService) renderStoredResult(ctx context.Context, tenantID, modelName, modelVersion string, result json.RawMessage) (types.ModelReport, error) {
	if service.ModelReportRenderer == nil || service.DockerSDKInterface == nil || service.DockerInferenceAPIInterface == nil {
		return types.ModelReport{}, errors.New(apiError.ReportNotAvailable)
	}
	entry := service.loadViewerModelDirectory(ctx, tenantID).match(modelName, modelVersion)
	if entry == nil {
		return types.ModelReport{}, errors.New(apiError.ReportNotAvailable)
	}
	// Same addressing as managed predict: the tenant's current registration,
	// started if needed and reached by its Docker container name.
	containerName, info, err := containerready.Ensure(
		ctx, service.DockerSDKInterface, service.DockerInferenceAPIInterface,
		strings.TrimSpace(entry.model.ContainerID), reportContainerReadyTimeout, reportContainerReadyPoll,
	)
	if err != nil {
		log.Printf("[Viewer results] report container not ready tenant_id=%s model_id=%s err=%v", tenantID, entry.model.ID, err)
		return types.ModelReport{}, errors.New(apiError.InferenceModelUnavailable)
	}
	if !strings.EqualFold(strings.TrimSpace(info.ModelName), strings.TrimSpace(modelName)) || !info.SupportsReportRendering() {
		return types.ModelReport{}, errors.New(apiError.ReportNotAvailable)
	}

	timeout := service.ReportRenderTimeout
	if timeout <= 0 {
		timeout = defaultReportRenderTimeout
	}
	maxBytes := service.ReportMaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultReportMaxBytes
	}
	renderCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Only the stored result is sent. The caller's token never reaches the model.
	rendered, err := service.ModelReportRenderer.RenderReport(renderCtx, containerName, dockerInferenceTypes.RenderReportRequest{
		Result: result,
	}, maxBytes)
	if err != nil {
		switch {
		case errors.Is(err, dockerInferenceTypes.ErrRenderReportUnsupported):
			return types.ModelReport{}, errors.New(apiError.ReportNotAvailable)
		case errors.Is(err, dockerInferenceTypes.ErrRenderReportRejected):
			return types.ModelReport{}, errors.New(apiError.ReportRenderRejected)
		}
		log.Printf("[Viewer results] report render failed tenant_id=%s model_id=%s err=%v", tenantID, entry.model.ID, err)
		return types.ModelReport{}, errors.New(apiError.ReportRenderFailed)
	}
	htmlBase64 := strings.TrimSpace(rendered.Data.HTMLBase64)
	if htmlBase64 == "" {
		return types.ModelReport{}, errors.New(apiError.ReportRenderFailed)
	}
	if _, err := base64.StdEncoding.DecodeString(htmlBase64); err != nil {
		return types.ModelReport{}, errors.New(apiError.ReportRenderFailed)
	}
	return types.ModelReport{
		HTMLBase64: htmlBase64, ContentType: reportContentTypeHTML, ModelVersion: strings.TrimSpace(rendered.Data.ModelVersion),
	}, nil
}
