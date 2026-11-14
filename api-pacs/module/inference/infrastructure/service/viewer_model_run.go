package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/containerready"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
)

// viewerIngestionJobSource marks the stopped ingestion job created to own
// candidates for studies that no scheduled job has ingested. It is never
// polled: the runner only executes RUNNING jobs and its schedule window is empty.
const viewerIngestionJobSource = "VIEWER_REQUEST"

const maximumViewerSeriesInstanceUIDs = 1000

// CreateViewerModelRun creates one VIEWER_REQUEST run with a single execution
// for one tenant model and dispatches it to study-service like a manual
// reprocess. It reserves one quota unit for the requesting user, works for
// studies never ingested by a job, and rejects a second active execution of
// the same model for the same study with RUN_ALREADY_ACTIVE.
func (service *InferenceCommandService) CreateViewerModelRun(ctx context.Context, data types.CreateViewerModelRun) (types.CreateViewerModelRunResult, error) {
	tenantID := strings.TrimSpace(data.TenantID)
	userID := strings.TrimSpace(data.UserID)
	studyInstanceUID := strings.TrimSpace(data.StudyInstanceUID)
	modelRef := strings.TrimSpace(data.ModelRef)
	if tenantID == "" || userID == "" || studyInstanceUID == "" || modelRef == "" ||
		len(studyInstanceUID) > maximumViewerStudyUIDLength || len(data.SeriesInstanceUIDs) > maximumViewerSeriesInstanceUIDs {
		return types.CreateViewerModelRunResult{}, errors.New(apiError.InvalidPayload)
	}
	for _, uid := range data.SeriesInstanceUIDs {
		if trimmed := strings.TrimSpace(uid); trimmed == "" || len(trimmed) > 64 {
			return types.CreateViewerModelRunResult{}, errors.New(apiError.InvalidPayload)
		}
	}

	model, err := service.resolveViewerModelRegistration(ctx, tenantID, modelRef)
	if err != nil {
		return types.CreateViewerModelRunResult{}, err
	}
	modelInfo, err := service.viewerModelInfo(ctx, model)
	if err != nil {
		return types.CreateViewerModelRunResult{}, err
	}
	modelName := strings.TrimSpace(modelInfo.ModelName)
	modelVersion := strings.TrimSpace(modelInfo.Version)
	if deployment := currentIngestionDeployment(model); deployment != nil && strings.TrimSpace(deployment.ModelVersion) != "" {
		modelVersion = strings.TrimSpace(deployment.ModelVersion)
	}
	if modelName == "" || modelVersion == "" {
		return types.CreateViewerModelRunResult{}, errors.New(apiError.InferenceModelUnavailable)
	}

	local, err := service.isStudyPresentLocally(ctx, studyInstanceUID)
	if err != nil {
		return types.CreateViewerModelRunResult{}, fmt.Errorf("cannot verify local study before viewer run: %w", err)
	}
	if !local {
		return types.CreateViewerModelRunResult{}, errors.New(apiError.StudyNotAvailableLocally)
	}

	job, candidate, err := service.ensureViewerCandidate(ctx, tenantID, studyInstanceUID, model, modelInfo, modelName, modelVersion, data.Modality)
	if err != nil {
		return types.CreateViewerModelRunResult{}, err
	}

	var executionModality *string
	if modality, modalityErr := resolveDispatchModality(job, candidate); modalityErr == nil {
		executionModality = nonEmptyStringPointer(modality)
	}

	if service.InferenceQuotaManagerInterface == nil {
		ObserveInferenceQuotaEvent("unavailable")
		return types.CreateViewerModelRunResult{}, errors.New(apiError.InferenceQuotaUnavailable)
	}
	runID := generateID()
	executionID := generateID()
	quotaReservation := &types.InferenceQuotaReservation{
		TenantID: tenantID, UserID: userID, ReservationID: runID, Units: 1,
	}
	if _, err := service.InferenceQuotaManagerInterface.Reserve(ctx, *quotaReservation); err != nil {
		return types.CreateViewerModelRunResult{}, err
	}

	plan, err := service.InferenceProcessingRunRepositoryInterface.CreateProcessingRunPlan(ctx, repositoryTypes.CreateInferenceIngestionProcessingRunPlan{
		Run: repositoryTypes.CreateInferenceIngestionProcessingRun{
			ID:                runID,
			TenantID:          tenantID,
			StudyInstanceUID:  studyInstanceUID,
			RunTrigger:        entity.InferenceIngestionProcessingRunTriggerViewerRequest,
			Phase:             entity.InferenceIngestionProcessingRunPhaseQueued,
			RequestedByUserID: &userID,
		},
		Executions: []repositoryTypes.CreateInferenceIngestionProcessingExecution{{
			ID:           executionID,
			CandidateID:  candidate.ID,
			ModelName:    job.ModelName,
			ModelVersion: nonEmptyStringPointer(job.ModelVersion),
			Modality:     executionModality,
		}},
	})
	if err != nil {
		service.finishInferenceQuotaReservation(quotaReservation, true)
		if err.Error() == apiError.DuplicateRecord {
			return types.CreateViewerModelRunResult{}, errors.New(apiError.RunAlreadyActive)
		}
		return types.CreateViewerModelRunResult{}, err
	}
	if len(plan.Executions) != 1 {
		service.finishInferenceQuotaReservation(quotaReservation, true)
		return types.CreateViewerModelRunResult{}, errors.New(apiError.DatabaseError)
	}

	execution := plan.Executions[0]
	log.Printf("[Viewer run] created processing_run_id=%s execution_id=%s candidate_id=%s model_name=%s series_hint=%d",
		plan.Run.ID, execution.ID, candidate.ID, job.ModelName, len(data.SeriesInstanceUIDs))
	service.scheduleStudyServiceDispatchWithRequestID(
		job, candidate, plan.Run.ID, execution.ID, execution.ID, types.DispatchStudyIntentManualReprocess,
	)

	return types.CreateViewerModelRunResult{
		Run: plan.Run, Execution: execution, ModelID: model.ID, ModelName: job.ModelName, ModelVersion: job.ModelVersion,
	}, nil
}

// resolveViewerModelRegistration accepts the Firestore registration ID and,
// for viewers that only know /model/proxy/available, the container ID or the
// model-info modelId. Only the tenant's current registrations are considered.
func (service *InferenceCommandService) resolveViewerModelRegistration(ctx context.Context, tenantID, ref string) (entity.InferenceModel, error) {
	models, err := service.InferenceQueryRepositoryInterface.SelectInferenceModels(ctx, tenantID)
	if err != nil {
		if err.Error() == apiError.MissingRecord {
			return entity.InferenceModel{}, err
		}
		return entity.InferenceModel{}, errors.New(apiError.FirestoreError)
	}
	var byContainer, byArtifact []entity.InferenceModel
	for _, model := range models {
		if model.TenantID != tenantID || strings.TrimSpace(model.ContainerID) == "" {
			continue
		}
		if model.ID == ref {
			return model, nil
		}
		if model.ContainerID == ref {
			byContainer = append(byContainer, model)
		}
		if deployment := currentIngestionDeployment(model); deployment != nil && strings.TrimSpace(deployment.ArtifactModelID) == ref {
			byArtifact = append(byArtifact, model)
		}
	}
	for _, matches := range [][]entity.InferenceModel{byContainer, byArtifact} {
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return entity.InferenceModel{}, errors.New(apiError.InvalidPayload)
		}
	}
	return entity.InferenceModel{}, errors.New(apiError.MissingRecord)
}

func (service *InferenceCommandService) viewerModelInfo(ctx context.Context, model entity.InferenceModel) (dockerInferenceTypes.ModelInfo, error) {
	if service.DockerSDKInterface == nil || service.DockerInferenceAPIInterface == nil {
		return dockerInferenceTypes.ModelInfo{}, errors.New(apiError.InferenceModelUnavailable)
	}
	timeout := service.inferenceContainerReadinessTimeout
	if timeout <= 0 {
		timeout = defaultInferenceContainerReadinessTimeout
	}
	poll := service.inferenceContainerReadinessPollInterval
	if poll <= 0 {
		poll = defaultInferenceContainerReadinessPollInterval
	}
	_, info, err := containerready.Ensure(ctx, service.DockerSDKInterface, service.DockerInferenceAPIInterface, model.ContainerID, timeout, poll)
	if err != nil {
		log.Printf("[Viewer run] model container not ready model_id=%s err=%v", model.ID, err)
		return dockerInferenceTypes.ModelInfo{}, errors.New(apiError.InferenceModelUnavailable)
	}
	return info, nil
}

// ensureViewerCandidate reuses the study's candidate under an ingestion job
// for this model and version, or creates one (and, when the tenant has no such
// job, a stopped viewer-owned job). The candidate is marked RETRIEVED/local,
// which the ingestion runner neither polls nor retrieves again.
func (service *InferenceCommandService) ensureViewerCandidate(
	ctx context.Context,
	tenantID, studyInstanceUID string,
	model entity.InferenceModel,
	modelInfo dockerInferenceTypes.ModelInfo,
	modelName, modelVersion, requestedModality string,
) (entity.InferenceIngestionJob, entity.InferenceIngestionCandidate, error) {
	jobs, err := service.InferenceQueryRepositoryInterface.SelectInferenceIngestionJobs(&tenantID)
	if err != nil && err.Error() != apiError.MissingRecord {
		return entity.InferenceIngestionJob{}, entity.InferenceIngestionCandidate{}, err
	}
	matching := map[string]entity.InferenceIngestionJob{}
	var preferred *entity.InferenceIngestionJob
	for index := range jobs {
		job := jobs[index]
		if job.TenantID != tenantID || strings.TrimSpace(job.ModelName) != modelName || strings.TrimSpace(job.ModelVersion) != modelVersion {
			continue
		}
		matching[job.ID] = job
		if preferred == nil || (preferred.Status != entity.InferenceIngestionJobStatusRunning && job.Status == entity.InferenceIngestionJobStatusRunning) {
			preferred = &jobs[index]
		}
	}

	candidates, err := service.InferenceQueryRepositoryInterface.ListInferenceIngestionCandidates(repositoryTypes.ListInferenceIngestionCandidates{
		TenantID: tenantID, StudyInstanceUID: &studyInstanceUID,
	})
	if err != nil && err.Error() != apiError.MissingRecord {
		return entity.InferenceIngestionJob{}, entity.InferenceIngestionCandidate{}, err
	}
	var reuse *entity.InferenceIngestionCandidate
	for index := range candidates {
		candidate := &candidates[index]
		if candidate.TenantID != tenantID || strings.TrimSpace(candidate.StudyInstanceUID) != studyInstanceUID {
			continue
		}
		if _, ok := matching[candidate.IngestionJobID]; !ok {
			continue
		}
		if reuse == nil || (reuse.Status == entity.InferenceIngestionCandidateStatusDisappeared &&
			candidate.Status != entity.InferenceIngestionCandidateStatusDisappeared) {
			reuse = candidate
		}
	}
	if reuse != nil {
		if reuse.Status != entity.InferenceIngestionCandidateStatusRetrieved {
			if err := service.InferenceCommandRepositoryInterface.MarkCandidateRetrievedWithContext(repositoryTypes.UpdateCandidateRetrievalState{
				ID: reuse.ID, LastRetrievalState: stringPointer(string(candidateRetrievalOutcomeLocal)),
			}); err != nil {
				return entity.InferenceIngestionJob{}, entity.InferenceIngestionCandidate{}, err
			}
			reuse.Status = entity.InferenceIngestionCandidateStatusRetrieved
		}
		return matching[reuse.IngestionJobID], *reuse, nil
	}

	var job entity.InferenceIngestionJob
	if preferred != nil {
		job = *preferred
	} else {
		job, err = service.createViewerIngestionJob(tenantID, model, modelInfo, modelName, modelVersion, requestedModality)
		if err != nil {
			return entity.InferenceIngestionJob{}, entity.InferenceIngestionCandidate{}, err
		}
	}

	candidateID := generateID()
	if err := service.InferenceCommandRepositoryInterface.UpsertIngestionCandidate(repositoryTypes.UpsertIngestionCandidate{
		ID: candidateID, TenantID: tenantID, IngestionJobID: job.ID, StudyInstanceUID: studyInstanceUID,
	}); err != nil {
		return entity.InferenceIngestionJob{}, entity.InferenceIngestionCandidate{}, err
	}
	jobID := job.ID
	created, err := service.InferenceQueryRepositoryInterface.ListInferenceIngestionCandidates(repositoryTypes.ListInferenceIngestionCandidates{
		TenantID: tenantID, IngestionJobID: &jobID, StudyInstanceUID: &studyInstanceUID,
	})
	if err != nil {
		return entity.InferenceIngestionJob{}, entity.InferenceIngestionCandidate{}, err
	}
	if len(created) != 1 {
		return entity.InferenceIngestionJob{}, entity.InferenceIngestionCandidate{}, errors.New(apiError.DatabaseError)
	}
	candidate := created[0]
	if err := service.InferenceCommandRepositoryInterface.MarkCandidateRetrievedWithContext(repositoryTypes.UpdateCandidateRetrievalState{
		ID: candidate.ID, LastRetrievalState: stringPointer(string(candidateRetrievalOutcomeLocal)),
	}); err != nil {
		return entity.InferenceIngestionJob{}, entity.InferenceIngestionCandidate{}, err
	}
	candidate.Status = entity.InferenceIngestionCandidateStatusRetrieved
	return job, candidate, nil
}

func (service *InferenceCommandService) createViewerIngestionJob(
	tenantID string,
	model entity.InferenceModel,
	modelInfo dockerInferenceTypes.ModelInfo,
	modelName, modelVersion, requestedModality string,
) (entity.InferenceIngestionJob, error) {
	modality, err := viewerJobModality(modelInfo.SupportedDicomModalities, requestedModality)
	if err != nil {
		return entity.InferenceIngestionJob{}, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	job := entity.InferenceIngestionJob{
		ID:                     generateID(),
		TenantID:               tenantID,
		DICOMModality:          viewerIngestionJobSource,
		ContainerID:            model.ContainerID,
		ModelID:                model.ID,
		ModelName:              modelName,
		ModelVersion:           modelVersion,
		Modalities:             []string{modality},
		StabilityMinutes:       defaultStabilityMinutes,
		RecentWindowMinutes:    defaultRecentWindowMinutes,
		MissingPollsThreshold:  defaultMissingPollsThreshold,
		ScheduleStartTimestamp: now,
		ScheduleEndTimestamp:   now,
		Status:                 entity.InferenceIngestionJobStatusStopped,
	}
	if err := service.InferenceCommandRepositoryInterface.InsertInferenceIngestionJob(repositoryTypes.CreateInferenceIngestionJob{
		ID:                     job.ID,
		TenantID:               job.TenantID,
		DICOMModality:          job.DICOMModality,
		ContainerID:            job.ContainerID,
		ModelID:                job.ModelID,
		ModelName:              job.ModelName,
		ModelVersion:           job.ModelVersion,
		Modalities:             job.Modalities,
		StabilityMinutes:       job.StabilityMinutes,
		RecentWindowMinutes:    job.RecentWindowMinutes,
		MissingPollsThreshold:  job.MissingPollsThreshold,
		ScheduleStartTimestamp: job.ScheduleStartTimestamp,
		ScheduleEndTimestamp:   job.ScheduleEndTimestamp,
		Status:                 job.Status,
	}); err != nil {
		return entity.InferenceIngestionJob{}, err
	}
	log.Printf("[Viewer run] created stopped viewer ingestion job job_id=%s model_name=%s model_version=%s", job.ID, modelName, modelVersion)
	return job, nil
}

// viewerJobModality picks the DICOM modality the dispatch will use. A model
// with one supported modality needs no hint; otherwise the request must name one.
func viewerJobModality(supported []string, requested string) (string, error) {
	normalized := normalizeDispatchModalities(supported)
	requested = strings.ToUpper(strings.TrimSpace(requested))
	if requested != "" {
		for _, modality := range normalized {
			if modality == requested {
				return modality, nil
			}
		}
		return "", errors.New(apiError.InvalidPayload)
	}
	if len(normalized) == 1 {
		return normalized[0], nil
	}
	return "", errors.New(apiError.InvalidPayload)
}
