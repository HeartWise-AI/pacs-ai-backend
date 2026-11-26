package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	dockerTypes "api-pacs/infrastructures/providers/sdk/docker/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type viewerStudyResultsProvider struct {
	results  serviceTypes.StudyServiceLatestResults
	found    bool
	err      error
	tenantID string
	modality string
	label    string
}

func (provider *viewerStudyResultsProvider) GetLatestStudyResults(_ context.Context, tenantID, _ string, modality, label string) (serviceTypes.StudyServiceLatestResults, bool, error) {
	provider.tenantID = tenantID
	provider.modality = modality
	provider.label = label
	return provider.results, provider.found, provider.err
}

type viewerResultsRunRepository struct {
	domainRepository.InferenceProcessingRunRepositoryInterface
	runs       map[string]entity.InferenceIngestionProcessingRun
	executions map[string][]entity.InferenceIngestionProcessingJob
	tenantIDs  []string
}

func (repository *viewerResultsRunRepository) SelectProcessingRun(_ context.Context, tenantID, runID string) (entity.InferenceIngestionProcessingRun, error) {
	repository.tenantIDs = append(repository.tenantIDs, tenantID)
	run, ok := repository.runs[runID]
	if !ok || run.TenantID != tenantID {
		return entity.InferenceIngestionProcessingRun{}, errors.New(apiError.MissingRecord)
	}
	return run, nil
}

func (repository *viewerResultsRunRepository) ListProcessingRunExecutions(_ context.Context, tenantID, runID string) ([]entity.InferenceIngestionProcessingJob, error) {
	repository.tenantIDs = append(repository.tenantIDs, tenantID)
	return repository.executions[runID], nil
}

type viewerResultsDocker struct {
	dockerTypes.DockerSDKInterface
	lifecycle dockerTypes.Lifecycle
	starts    int
}

func (docker *viewerResultsDocker) GetContainerInfo(_ context.Context, containerID string) (dockerTypes.GetContainerInfoResult, error) {
	lifecycle := docker.lifecycle
	if lifecycle == "" {
		lifecycle = dockerTypes.LifecycleRunning
	}
	return dockerTypes.GetContainerInfoResult{ID: containerID, Name: "/jepa", Lifecycle: lifecycle}, nil
}

func (docker *viewerResultsDocker) StartContainer(context.Context, string) error {
	docker.starts++
	docker.lifecycle = dockerTypes.LifecycleRunning
	return nil
}

type viewerReportRenderer struct {
	response      dockerInferenceTypes.RenderReportResponse
	err           error
	containerName string
	request       dockerInferenceTypes.RenderReportRequest
	maxBytes      int64
	deadline      bool
	calls         int
}

func (renderer *viewerReportRenderer) RenderReport(ctx context.Context, containerName string, request dockerInferenceTypes.RenderReportRequest, maxBytes int64) (dockerInferenceTypes.RenderReportResponse, error) {
	renderer.calls++
	renderer.containerName = containerName
	renderer.request = request
	renderer.maxBytes = maxBytes
	_, renderer.deadline = ctx.Deadline()
	return renderer.response, renderer.err
}

func renderingModelInfo(renderReport bool) dockerInferenceTypes.ModelInfo {
	return dockerInferenceTypes.ModelInfo{
		ModelID: "deepcoro-jepa", ModelName: "DeepCORO-JEPA", Version: "1.3.0",
		Capabilities: &dockerInferenceTypes.ModelCapabilities{RenderReport: renderReport},
	}
}

func viewerResultsService() (*InferenceQueryService, *viewerStudyResultsProvider, *viewerResultsRunRepository) {
	runID := "run-1"
	jobID := "job-1"
	completedAt := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	provider := &viewerStudyResultsProvider{found: true}
	runs := &viewerResultsRunRepository{
		runs: map[string]entity.InferenceIngestionProcessingRun{
			runID:         {ID: runID, TenantID: "tenant-a", StudyInstanceUID: "1.2.3", RunTrigger: entity.InferenceIngestionProcessingRunTriggerViewerRequest},
			"run-foreign": {ID: "run-foreign", TenantID: "tenant-b", StudyInstanceUID: "1.2.3"},
		},
		executions: map[string][]entity.InferenceIngestionProcessingJob{
			runID: {{
				ID: "execution-1", ProcessingRunID: &runID, ModelName: "DeepCORO-JEPA",
				Status: entity.InferenceIngestionProcessingJobStatusCompleted, StudyServiceJobID: &jobID, CompletedAt: &completedAt,
			}},
		},
	}
	service := &InferenceQueryService{
		InferenceQueryRepositoryInterface: &viewerRunQueryRepository{models: []entity.InferenceModel{{
			ID: "registration-1", TenantID: "tenant-a", ContainerID: "container-1", Name: "JEPA",
		}}},
		InferenceProcessingRunRepositoryInterface: runs,
		DockerSDKInterface:                        &viewerResultsDocker{},
		DockerInferenceAPIInterface:               &viewerModelInfoAPI{info: renderingModelInfo(true)},
		StudyResultsProvider:                      provider,
	}
	return service, provider, runs
}

func TestGetStudyLatestModelResultsJoinsRegistrationRunAndCapability(t *testing.T) {
	service, provider, runs := viewerResultsService()
	version := "1.3.0"
	runID := "run-1"
	provider.results = serviceTypes.StudyServiceLatestResults{StudyInstanceUID: "1.2.3", Results: []serviceTypes.StudyServiceLatestResult{{
		JobID: "job-1", ModelName: "DeepCORO-JEPA", ModelVersion: &version, Modality: "angiogram", Status: "completed",
		ProcessingRunID: &runID,
		SelectedViews:   json.RawMessage(`[{"sopInstanceUID":"1","seriesInstanceUID":null,"viewLabel":null,"order":0}]`),
	}, {
		JobID: "job-legacy", ModelName: "UnknownModel", Modality: "angiogram", Status: "completed",
		SelectedViews: json.RawMessage(`null`),
	}}}

	response, err := service.GetStudyLatestModelResults(context.Background(), serviceTypes.GetStudyLatestModelResults{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", Modality: "angiogram",
	})

	require.NoError(t, err)
	require.Equal(t, "tenant-a", provider.tenantID)
	require.Equal(t, "angiogram", provider.modality)
	require.Len(t, response.Results, 2)
	first := response.Results[0]
	require.Equal(t, "registration-1", *first.ModelID)
	require.Equal(t, "container-1", *first.ContainerID)
	require.Equal(t, "deepcoro-jepa", *first.ArtifactModelID)
	require.Equal(t, "run-1", *first.RunID)
	require.Equal(t, "execution-1", *first.ExecutionID)
	require.Equal(t, entity.InferenceIngestionProcessingRunTriggerViewerRequest, *first.Trigger)
	require.Equal(t, "angiogram", first.Modality)
	require.True(t, first.ReportAvailable)
	require.JSONEq(t, `[{"sopInstanceUID":"1","seriesInstanceUID":null,"viewLabel":null,"order":0}]`, string(first.SelectedViews))

	second := response.Results[1]
	require.Nil(t, second.ModelID)
	require.Nil(t, second.RunID)
	require.Nil(t, second.Trigger)
	require.False(t, second.ReportAvailable)
	payload, err := json.Marshal(second)
	require.NoError(t, err)
	require.Contains(t, string(payload), `"selectedViews":null`)
	for _, tenantID := range runs.tenantIDs {
		require.Equal(t, "tenant-a", tenantID)
	}
}

func TestGetStudyLatestModelResultsPassesDispatchSeriesThrough(t *testing.T) {
	service, provider, _ := viewerResultsService()
	result := func(jobID, model string, series json.RawMessage) serviceTypes.StudyServiceLatestResult {
		return serviceTypes.StudyServiceLatestResult{
			JobID: jobID, ModelName: model, Modality: "angiogram", Status: "completed",
			DispatchSeriesInstanceUIDs: series,
		}
	}
	provider.results = serviceTypes.StudyServiceLatestResults{StudyInstanceUID: "1.2.3", Results: []serviceTypes.StudyServiceLatestResult{
		result("job-subset", "Subset", json.RawMessage(` ["1.2.3.4","1.2.3.6"] `)),
		result("job-full", "Full", json.RawMessage(`null`)),
		result("job-old", "OldStudyService", nil),
		result("job-odd", "Malformed", json.RawMessage(`[{"uid":"1.2.3.4"}]`)),
	}}

	response, err := service.GetStudyLatestModelResults(context.Background(), serviceTypes.GetStudyLatestModelResults{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})

	require.NoError(t, err)
	require.Len(t, response.Results, 4)
	payloads := make([]string, len(response.Results))
	for i, item := range response.Results {
		payload, err := json.Marshal(item)
		require.NoError(t, err)
		payloads[i] = string(payload)
	}
	require.Contains(t, payloads[0], `"dispatchSeriesInstanceUIDs":["1.2.3.4","1.2.3.6"]`)
	require.Contains(t, payloads[1], `"dispatchSeriesInstanceUIDs":null`)
	require.NotContains(t, payloads[2], "dispatchSeriesInstanceUIDs")
	require.NotContains(t, payloads[3], "dispatchSeriesInstanceUIDs")
}

func TestGetStudyLatestModelResultsHidesForeignRunIdentifiers(t *testing.T) {
	service, provider, _ := viewerResultsService()
	runID := "run-foreign"
	provider.results = serviceTypes.StudyServiceLatestResults{Results: []serviceTypes.StudyServiceLatestResult{{
		JobID: "job-9", ModelName: "DeepCORO-JEPA", Status: "completed", ProcessingRunID: &runID,
	}}}

	response, err := service.GetStudyLatestModelResults(context.Background(), serviceTypes.GetStudyLatestModelResults{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})

	require.NoError(t, err)
	require.Nil(t, response.Results[0].RunID)
	require.Nil(t, response.Results[0].ExecutionID)
}

func TestGetStudyLatestModelResultsMapsStudyServiceFailureTo503AndAbsenceToEmpty(t *testing.T) {
	service, provider, _ := viewerResultsService()
	provider.err = errors.New("connection refused")
	_, err := service.GetStudyLatestModelResults(context.Background(), serviceTypes.GetStudyLatestModelResults{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})
	require.EqualError(t, err, apiError.InferenceResultServiceUnavailable)

	provider.err = nil
	provider.found = false
	response, err := service.GetStudyLatestModelResults(context.Background(), serviceTypes.GetStudyLatestModelResults{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})
	require.NoError(t, err)
	require.NotNil(t, response.Results)
	require.Empty(t, response.Results)
}

func TestGetStudyLatestModelResultsDoesNotStartStoppedContainers(t *testing.T) {
	service, provider, _ := viewerResultsService()
	docker := &viewerResultsDocker{lifecycle: dockerTypes.LifecycleStopped}
	service.DockerSDKInterface = docker
	provider.results = serviceTypes.StudyServiceLatestResults{Results: []serviceTypes.StudyServiceLatestResult{{
		JobID: "job-1", ModelName: "DeepCORO-JEPA", Status: "completed",
	}}}

	response, err := service.GetStudyLatestModelResults(context.Background(), serviceTypes.GetStudyLatestModelResults{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})

	require.NoError(t, err)
	require.Zero(t, docker.starts)
	require.False(t, response.Results[0].ReportAvailable)
}

func TestGetStudyJobReportRendersOwnedCompletedJobWithBoundedCall(t *testing.T) {
	service, _, _ := viewerResultsService()
	tenantID := "tenant-a"
	version := "1.2.0"
	service.ProcessingResultProviderInterface = &executionResultDispatcher{found: true, job: serviceTypes.StudyServiceJobResult{
		StudyServiceJob: serviceTypes.StudyServiceJob{
			JobID: "job-legacy", StudyInstanceUID: "1.2.3", TenantID: &tenantID,
			ModelName: "DeepCORO-JEPA", ModelVersion: &version, Status: "completed",
		},
		ResultJSON: json.RawMessage(`{"predictions":{}}`),
	}}
	renderer := &viewerReportRenderer{}
	renderer.response.Success = true
	renderer.response.Data.HTMLBase64 = "PGh0bWw+PC9odG1sPg=="
	renderer.response.Data.ModelVersion = "1.3.0"
	service.ModelReportRenderer = renderer
	service.ReportMaxBytes = 1024

	report, err := service.GetStudyJobReport(context.Background(), serviceTypes.GetStudyJobReport{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", StudyServiceJobID: "job-legacy",
	})

	require.NoError(t, err)
	require.Equal(t, "PGh0bWw+PC9odG1sPg==", report.HTMLBase64)
	require.Equal(t, "text/html", report.ContentType)
	require.Equal(t, "jepa", renderer.containerName)
	require.JSONEq(t, `{"predictions":{}}`, string(renderer.request.Result))
	require.Equal(t, int64(1024), renderer.maxBytes)
	require.True(t, renderer.deadline)
}

func TestGetStudyJobReportRejectsOtherStudyOrTenant(t *testing.T) {
	service, _, _ := viewerResultsService()
	otherTenant := "tenant-b"
	service.ProcessingResultProviderInterface = &executionResultDispatcher{found: true, job: serviceTypes.StudyServiceJobResult{
		StudyServiceJob: serviceTypes.StudyServiceJob{
			JobID: "job-legacy", StudyInstanceUID: "1.2.3", TenantID: &otherTenant, ModelName: "DeepCORO-JEPA", Status: "completed",
		},
		ResultJSON: json.RawMessage(`{}`),
	}}
	renderer := &viewerReportRenderer{}
	service.ModelReportRenderer = renderer

	_, err := service.GetStudyJobReport(context.Background(), serviceTypes.GetStudyJobReport{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", StudyServiceJobID: "job-legacy",
	})
	require.EqualError(t, err, apiError.MissingRecord)

	_, err = service.GetStudyJobReport(context.Background(), serviceTypes.GetStudyJobReport{
		TenantID: "tenant-a", StudyInstanceUID: "9.9.9", StudyServiceJobID: "job-legacy",
	})
	require.EqualError(t, err, apiError.MissingRecord)
	require.Zero(t, renderer.calls)
}

func TestGetProcessingRunExecutionReportRequiresRenderCapability(t *testing.T) {
	service, _, _ := viewerResultsService()
	repository, dispatcher, input := validExecutionResultFixture()
	dispatcher.job.ModelName = "DeepCORO-JEPA"
	repository.execution.ModelName = "DeepCORO-JEPA"
	service.InferenceProcessingRunRepositoryInterface = repository
	service.ProcessingResultProviderInterface = dispatcher
	service.DockerInferenceAPIInterface = &viewerModelInfoAPI{info: renderingModelInfo(false)}
	renderer := &viewerReportRenderer{}
	service.ModelReportRenderer = renderer

	_, err := service.GetProcessingRunExecutionReport(context.Background(), serviceTypes.GetProcessingRunExecutionReport{
		TenantID: input.TenantID, RunID: input.RunID, ExecutionID: input.ExecutionID,
	})

	require.EqualError(t, err, apiError.ReportNotAvailable)
	require.Zero(t, renderer.calls)
}

func TestGetProcessingRunExecutionReportRejectsIncompleteExecution(t *testing.T) {
	service, _, _ := viewerResultsService()
	repository, dispatcher, input := validExecutionResultFixture()
	repository.execution.Status = entity.InferenceIngestionProcessingJobStatusRunning
	service.InferenceProcessingRunRepositoryInterface = repository
	service.ProcessingResultProviderInterface = dispatcher
	service.ModelReportRenderer = &viewerReportRenderer{}

	_, err := service.GetProcessingRunExecutionReport(context.Background(), serviceTypes.GetProcessingRunExecutionReport{
		TenantID: input.TenantID, RunID: input.RunID, ExecutionID: input.ExecutionID,
	})

	require.EqualError(t, err, apiError.InferenceExecutionResultNotAvailable)
}

func TestRenderStoredResultMapsRendererErrors(t *testing.T) {
	for _, testCase := range []struct {
		err      error
		expected string
	}{
		{dockerInferenceTypes.ErrRenderReportRejected, apiError.ReportRenderRejected},
		{dockerInferenceTypes.ErrRenderReportUnsupported, apiError.ReportNotAvailable},
		{dockerInferenceTypes.ErrRenderReportTooLarge, apiError.ReportRenderFailed},
		{context.DeadlineExceeded, apiError.ReportRenderFailed},
	} {
		service, _, _ := viewerResultsService()
		service.ModelReportRenderer = &viewerReportRenderer{err: testCase.err}
		_, err := service.renderStoredResult(context.Background(), "tenant-a", "DeepCORO-JEPA", "", json.RawMessage(`{}`))
		require.EqualError(t, err, testCase.expected)
	}
}

func TestExecutionResultIncludesSelectedViews(t *testing.T) {
	repository, dispatcher, input := validExecutionResultFixture()
	dispatcher.job.SelectedViews = json.RawMessage(`[{"sopInstanceUID":"1"}]`)
	service := InferenceQueryService{
		InferenceProcessingRunRepositoryInterface: repository,
		ProcessingResultProviderInterface:         dispatcher,
	}

	result, err := service.GetProcessingRunExecutionResult(context.Background(), input)

	require.NoError(t, err)
	require.JSONEq(t, `[{"sopInstanceUID":"1"}]`, string(result.SelectedViews))
}

func TestNormalizeSelectedViewsKeepsArraysOnly(t *testing.T) {
	require.Nil(t, normalizeSelectedViews(nil))
	require.Nil(t, normalizeSelectedViews(json.RawMessage(`null`)))
	require.Nil(t, normalizeSelectedViews(json.RawMessage(`{"a":1}`)))
	require.JSONEq(t, `[]`, string(normalizeSelectedViews(json.RawMessage(` [] `))))
}

func TestGetStudyLatestModelResultsUsesCachedIdentityWhenContainerIsDown(t *testing.T) {
	service, provider, _ := viewerResultsService()
	service.DockerSDKInterface, service.DockerInferenceAPIInterface = nil, nil // no live model info
	service.rememberModelInfo(context.Background(), serviceTypes.GetInferenceAvailableModelResult{
		ContainerID: "container-1", ModelID: "deepcoro-jepa", ModelName: "DeepCORO-JEPA", Version: "1.3.0",
	})
	provider.results = serviceTypes.StudyServiceLatestResults{StudyInstanceUID: "1.2.3", Results: []serviceTypes.StudyServiceLatestResult{{
		JobID: "job-1", ModelName: "DeepCORO-JEPA", Modality: "angiogram", Status: "completed",
	}}}

	response, err := service.GetStudyLatestModelResults(context.Background(), serviceTypes.GetStudyLatestModelResults{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})

	require.NoError(t, err)
	require.Len(t, response.Results, 1)
	require.Equal(t, "registration-1", *response.Results[0].ModelID)
	require.Equal(t, "deepcoro-jepa", *response.Results[0].ArtifactModelID, "custom-role filtering keys on this")
	require.False(t, response.Results[0].ReportAvailable, "report capability still needs a live container")
}

// Live case (2026-11-26): an automatic run used 4 videos, then the user reran
// the model from the viewer on one series. The automatic run stays the
// reported result (best-run rule) and the rerun must be listed with its own
// run, execution and trigger, or the viewer can never open it.
func TestGetStudyLatestModelResultsListsTheOtherRunsOfTheModel(t *testing.T) {
	service, provider, runs := viewerResultsService()
	autoRunID, viewerRunID := "run-auto", "run-1"
	autoJobID, viewerJobID := "job-auto", "job-1"
	runs.runs[autoRunID] = entity.InferenceIngestionProcessingRun{
		ID: autoRunID, TenantID: "tenant-a", StudyInstanceUID: "1.2.3", RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto,
	}
	runs.executions[autoRunID] = []entity.InferenceIngestionProcessingJob{{
		ID: "execution-auto", ProcessingRunID: &autoRunID, ModelName: "DeepCORO-JEPA",
		Status: entity.InferenceIngestionProcessingJobStatusCompleted, StudyServiceJobID: &autoJobID,
	}}
	four, five, one := 4, 5, 1
	autoCompleted, viewerCompleted := "2026-11-26T08:41:20-05:00", "2026-11-26T09:16:15-05:00"
	auto := serviceTypes.StudyServiceLatestResult{
		JobID: autoJobID, CompletedAt: &autoCompleted, ProcessingRunID: &autoRunID,
		InputVideoCount: &five, EligibleVideoCount: &four, DispatchSeriesInstanceUIDs: json.RawMessage(`null`),
	}
	viewer := serviceTypes.StudyServiceLatestResult{
		JobID: viewerJobID, CompletedAt: &viewerCompleted, ProcessingRunID: &viewerRunID,
		InputVideoCount: &one, EligibleVideoCount: &one, DispatchSeriesInstanceUIDs: json.RawMessage(`["1.2.3.4"]`),
	}
	best := auto
	best.ModelName, best.Modality, best.Status = "DeepCORO-JEPA", "angiogram", "completed"
	autoListed := auto
	autoListed.Best = true
	best.Runs = []serviceTypes.StudyServiceLatestResult{viewer, autoListed, {JobID: " "}}
	provider.results = serviceTypes.StudyServiceLatestResults{StudyInstanceUID: "1.2.3", Results: []serviceTypes.StudyServiceLatestResult{best}}

	response, err := service.GetStudyLatestModelResults(context.Background(), serviceTypes.GetStudyLatestModelResults{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})

	require.NoError(t, err)
	require.Len(t, response.Results, 1)
	result := response.Results[0]
	require.Equal(t, autoJobID, result.StudyServiceJobID)
	require.Equal(t, "execution-auto", *result.ExecutionID)
	require.Equal(t, 4, *result.EligibleVideoCount)
	require.Nil(t, result.BestRun, "only listed runs are flagged")
	require.Len(t, result.Runs, 2, "a listed run without a job ID is dropped")

	rerun := result.Runs[0]
	require.Equal(t, viewerJobID, rerun.StudyServiceJobID)
	require.Equal(t, "DeepCORO-JEPA", rerun.ModelName)
	require.Equal(t, "angiogram", rerun.Modality)
	require.Equal(t, viewerRunID, *rerun.RunID)
	require.Equal(t, "execution-1", *rerun.ExecutionID)
	require.Equal(t, entity.InferenceIngestionProcessingRunTriggerViewerRequest, *rerun.Trigger)
	require.Equal(t, viewerCompleted, *rerun.CompletedAt)
	require.Equal(t, 1, *rerun.EligibleVideoCount)
	require.Equal(t, 1, *rerun.InputVideoCount)
	require.False(t, *rerun.BestRun)
	require.True(t, rerun.ReportAvailable)
	require.Equal(t, "container-1", *rerun.ContainerID)
	require.JSONEq(t, `["1.2.3.4"]`, string(rerun.DispatchSeriesInstanceUIDs))
	require.Empty(t, rerun.Runs)

	listedBest := result.Runs[1]
	require.Equal(t, autoJobID, listedBest.StudyServiceJobID)
	require.True(t, *listedBest.BestRun)
	require.Equal(t, entity.InferenceIngestionProcessingRunTriggerAuto, *listedBest.Trigger)

	payload, err := json.Marshal(result)
	require.NoError(t, err)
	require.Contains(t, string(payload), `"runs":[`)
	require.Contains(t, string(payload), `"bestRun":false`)
}

// A study-service that predates the run list keeps today's response shape.
func TestGetStudyLatestModelResultsOmitsRunsWhenStudyServiceListsNone(t *testing.T) {
	service, provider, _ := viewerResultsService()
	provider.results = serviceTypes.StudyServiceLatestResults{StudyInstanceUID: "1.2.3", Results: []serviceTypes.StudyServiceLatestResult{{
		JobID: "job-1", ModelName: "DeepCORO-JEPA", Modality: "angiogram", Status: "completed",
	}}}

	response, err := service.GetStudyLatestModelResults(context.Background(), serviceTypes.GetStudyLatestModelResults{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})

	require.NoError(t, err)
	payload, err := json.Marshal(response.Results[0])
	require.NoError(t, err)
	for _, field := range []string{"runs", "bestRun", "eligibleVideoCount", "inputVideoCount"} {
		require.NotContains(t, string(payload), field)
	}
}

func TestBoundedListedRunsNeverCutsTheBestRun(t *testing.T) {
	runs := func(jobIDs ...string) []serviceTypes.StudyServiceLatestResult {
		listed := make([]serviceTypes.StudyServiceLatestResult, 0, len(jobIDs))
		for _, jobID := range jobIDs {
			listed = append(listed, serviceTypes.StudyServiceLatestResult{JobID: jobID})
		}
		return listed
	}
	jobIDs := func(listed []serviceTypes.StudyServiceLatestResult) []string {
		ids := make([]string, 0, len(listed))
		for _, run := range listed {
			ids = append(ids, run.JobID)
		}
		return ids
	}

	// The best run is older than the newest runs that fit: it takes the last place.
	require.Equal(t, []string{"new-1", "new-2", "best"},
		jobIDs(boundedListedRuns(runs("new-1", "new-2", "new-3", " ", "best", "old"), "best", 3)))
	// Within the limit nothing moves, and runs without a job ID are dropped.
	require.Equal(t, []string{"new-1", "best", "new-3"},
		jobIDs(boundedListedRuns(runs("new-1", "", "best", "new-3", "old"), "best", 3)))
	require.Equal(t, []string{"a", "b"}, jobIDs(boundedListedRuns(runs("a", "b"), "missing", 20)))
	require.Empty(t, boundedListedRuns(nil, "best", 20))
}
