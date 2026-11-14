package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dockerInferenceTypes "api-pacs/infrastructures/providers/api/dockerinference/types"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type viewerRunQueryRepository struct {
	domainRepository.InferenceQueryRepositoryInterface
	mu         sync.Mutex
	models     []entity.InferenceModel
	jobs       []entity.InferenceIngestionJob
	candidates []entity.InferenceIngestionCandidate
}

func (repository *viewerRunQueryRepository) SelectInferenceModels(_ context.Context, tenantID string) ([]entity.InferenceModel, error) {
	models := []entity.InferenceModel{}
	for _, model := range repository.models {
		if model.TenantID == tenantID {
			models = append(models, model)
		}
	}
	if len(models) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}
	return models, nil
}

func (repository *viewerRunQueryRepository) SelectInferenceIngestionJobs(tenantID *string) ([]entity.InferenceIngestionJob, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	jobs := []entity.InferenceIngestionJob{}
	for _, job := range repository.jobs {
		if tenantID == nil || job.TenantID == *tenantID {
			jobs = append(jobs, job)
		}
	}
	if len(jobs) == 0 {
		return nil, errors.New(apiError.MissingRecord)
	}
	return jobs, nil
}

func (repository *viewerRunQueryRepository) ListInferenceIngestionCandidates(data repositoryTypes.ListInferenceIngestionCandidates) ([]entity.InferenceIngestionCandidate, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	candidates := []entity.InferenceIngestionCandidate{}
	for _, candidate := range repository.candidates {
		if candidate.TenantID != data.TenantID ||
			(data.StudyInstanceUID != nil && candidate.StudyInstanceUID != *data.StudyInstanceUID) ||
			(data.IngestionJobID != nil && candidate.IngestionJobID != *data.IngestionJobID) {
			continue
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

type viewerRunCommandRepository struct {
	guardedDispatchCommandRepository
	query       *viewerRunQueryRepository
	insertedJob []repositoryTypes.CreateInferenceIngestionJob
	upserted    []repositoryTypes.UpsertIngestionCandidate
	retrieved   []string
}

func (repository *viewerRunCommandRepository) InsertInferenceIngestionJob(data repositoryTypes.CreateInferenceIngestionJob) error {
	repository.insertedJob = append(repository.insertedJob, data)
	repository.query.mu.Lock()
	defer repository.query.mu.Unlock()
	repository.query.jobs = append(repository.query.jobs, entity.InferenceIngestionJob{
		ID: data.ID, TenantID: data.TenantID, ModelID: data.ModelID, ModelName: data.ModelName,
		ModelVersion: data.ModelVersion, ContainerID: data.ContainerID, Modalities: data.Modalities,
		DICOMModality: data.DICOMModality, Status: data.Status,
	})
	return nil
}

func (repository *viewerRunCommandRepository) UpsertIngestionCandidate(data repositoryTypes.UpsertIngestionCandidate) error {
	repository.upserted = append(repository.upserted, data)
	repository.query.mu.Lock()
	defer repository.query.mu.Unlock()
	repository.query.candidates = append(repository.query.candidates, entity.InferenceIngestionCandidate{
		ID: data.ID, TenantID: data.TenantID, IngestionJobID: data.IngestionJobID,
		StudyInstanceUID: data.StudyInstanceUID, Status: entity.InferenceIngestionCandidateStatusDiscovered,
	})
	return nil
}

func (repository *viewerRunCommandRepository) MarkCandidateRetrievedWithContext(data repositoryTypes.UpdateCandidateRetrievalState) error {
	repository.retrieved = append(repository.retrieved, data.ID)
	return nil
}

type viewerModelInfoAPI struct {
	dockerInferenceTypes.DockerInferenceAPIInterface
	info dockerInferenceTypes.ModelInfo
	err  error
}

func (api *viewerModelInfoAPI) GetModelInfo(context.Context, string) (dockerInferenceTypes.GetModelInfoResponse, error) {
	if api.err != nil {
		return dockerInferenceTypes.GetModelInfoResponse{}, api.err
	}
	return dockerInferenceTypes.GetModelInfoResponse{Success: true, Data: api.info}, nil
}

type viewerRunFixture struct {
	service       *InferenceCommandService
	query         *viewerRunQueryRepository
	command       *viewerRunCommandRepository
	runs          *processingRunPlannerRepository
	quota         *recordingInferenceQuotaManager
	dispatchCalls chan serviceTypes.DispatchStudyRequest
	plans         []repositoryTypes.CreateInferenceIngestionProcessingRunPlan
}

func newViewerRunFixture(t *testing.T) *viewerRunFixture {
	t.Helper()
	fixture := &viewerRunFixture{
		query: &viewerRunQueryRepository{
			models: []entity.InferenceModel{{
				ID: "registration-1", TenantID: "tenant-a", ContainerID: "container-1", Name: "DeepCORO JEPA",
			}, {
				ID: "registration-other", TenantID: "tenant-b", ContainerID: "container-2", Name: "DeepCORO JEPA",
			}},
		},
		runs:          &processingRunPlannerRepository{},
		quota:         &recordingInferenceQuotaManager{},
		dispatchCalls: make(chan serviceTypes.DispatchStudyRequest, 1),
	}
	fixture.command = &viewerRunCommandRepository{query: fixture.query}
	fixture.runs.create = func(data repositoryTypes.CreateInferenceIngestionProcessingRunPlan) (repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult, error) {
		fixture.plans = append(fixture.plans, data)
		run := entity.InferenceIngestionProcessingRun{
			ID: data.Run.ID, TenantID: data.Run.TenantID, StudyInstanceUID: data.Run.StudyInstanceUID,
			RunNumber: 4, RunTrigger: data.Run.RunTrigger, Phase: data.Run.Phase, RequestedByUserID: data.Run.RequestedByUserID,
		}
		execution := entity.InferenceIngestionProcessingJob{
			ID: data.Executions[0].ID, ProcessingRunID: &run.ID, CandidateID: data.Executions[0].CandidateID,
			ModelName: data.Executions[0].ModelName, Status: entity.InferenceIngestionProcessingJobStatusPending,
		}
		fixture.runs.selectedExecution = execution
		return repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult{
			Run: run, Executions: []entity.InferenceIngestionProcessingJob{execution}, Created: true,
		}, nil
	}
	fixture.service = &InferenceCommandService{
		InferenceCommandRepositoryInterface:       fixture.command,
		InferenceQueryRepositoryInterface:         fixture.query,
		InferenceProcessingRunRepositoryInterface: fixture.runs,
		InferenceQuotaManagerInterface:            fixture.quota,
		OrthancAPIInterface:                       &manualReprocessOrthancAPI{local: true},
		ProcessingDispatcherInterface: &guardedProcessingDispatcher{
			dispatchCall:          fixture.dispatchCalls,
			response:              serviceTypes.DispatchStudyResponse{JobID: "study-service-job-1"},
			echoManualCorrelation: true,
		},
		StudyServiceDispatchSemaphore: make(chan struct{}, 1),
		DockerSDKInterface:            &guardedDispatchDockerSDK{},
		DockerInferenceAPIInterface: &viewerModelInfoAPI{info: dockerInferenceTypes.ModelInfo{
			ModelID: "deepcoro-jepa", ModelName: "DeepCORO-JEPA", Version: "1.3.0", SupportedDicomModalities: []string{"XA"},
		}},
		inferenceContainerReadinessPollInterval: time.Millisecond,
	}
	return fixture
}

func viewerRunInput() serviceTypes.CreateViewerModelRun {
	return serviceTypes.CreateViewerModelRun{
		TenantID: "tenant-a", UserID: "user-a", StudyInstanceUID: "1.2.3", ModelRef: "registration-1",
	}
}

func TestCreateViewerModelRunCreatesJobCandidateAndSingleExecutionForNeverIngestedStudy(t *testing.T) {
	fixture := newViewerRunFixture(t)

	result, err := fixture.service.CreateViewerModelRun(context.Background(), viewerRunInput())

	require.NoError(t, err)
	require.Equal(t, entity.InferenceIngestionProcessingRunTriggerViewerRequest, result.Run.RunTrigger)
	require.Equal(t, "registration-1", result.ModelID)
	require.Equal(t, "DeepCORO-JEPA", result.ModelName)
	require.Equal(t, "1.3.0", result.ModelVersion)

	require.Len(t, fixture.command.insertedJob, 1)
	job := fixture.command.insertedJob[0]
	require.Equal(t, entity.InferenceIngestionJobStatusStopped, job.Status)
	require.Equal(t, "tenant-a", job.TenantID)
	require.Equal(t, "registration-1", job.ModelID)
	require.Equal(t, []string{"XA"}, job.Modalities)
	require.Equal(t, viewerIngestionJobSource, job.DICOMModality)
	require.Equal(t, job.ScheduleStartTimestamp, job.ScheduleEndTimestamp)

	require.Len(t, fixture.command.upserted, 1)
	require.Equal(t, []string{fixture.command.upserted[0].ID}, fixture.command.retrieved)

	require.Len(t, fixture.plans, 1)
	plan := fixture.plans[0]
	require.Equal(t, "tenant-a", plan.Run.TenantID)
	require.Equal(t, "user-a", *plan.Run.RequestedByUserID)
	require.Len(t, plan.Executions, 1)
	require.Equal(t, "DeepCORO-JEPA", plan.Executions[0].ModelName)
	require.Equal(t, "angiogram", *plan.Executions[0].Modality)

	require.Len(t, fixture.quota.reserved, 1)
	require.Equal(t, serviceTypes.InferenceQuotaReservation{
		TenantID: "tenant-a", UserID: "user-a", ReservationID: plan.Run.ID, Units: 1,
	}, fixture.quota.reserved[0])

	select {
	case dispatched := <-fixture.dispatchCalls:
		require.Equal(t, serviceTypes.DispatchStudyIntentManualReprocess, dispatched.DispatchIntent)
		require.Equal(t, plan.Run.ID, *dispatched.ProcessingRunID)
		require.Equal(t, result.Execution.ID, *dispatched.ProcessingExecutionID)
	case <-time.After(5 * time.Second):
		t.Fatal("viewer run was not dispatched")
	}
}

func TestCreateViewerModelRunReusesExistingCandidateForMatchingJob(t *testing.T) {
	fixture := newViewerRunFixture(t)
	fixture.query.jobs = []entity.InferenceIngestionJob{{
		ID: "job-1", TenantID: "tenant-a", ModelID: "registration-1", ContainerID: "container-1",
		ModelName: "DeepCORO-JEPA", ModelVersion: "1.3.0", Modalities: []string{"XA"},
		Status: entity.InferenceIngestionJobStatusRunning,
	}}
	fixture.query.candidates = []entity.InferenceIngestionCandidate{{
		ID: "candidate-1", TenantID: "tenant-a", IngestionJobID: "job-1", StudyInstanceUID: "1.2.3",
		Status: entity.InferenceIngestionCandidateStatusDisappeared,
	}}

	_, err := fixture.service.CreateViewerModelRun(context.Background(), viewerRunInput())

	require.NoError(t, err)
	require.Empty(t, fixture.command.insertedJob)
	require.Empty(t, fixture.command.upserted)
	require.Equal(t, []string{"candidate-1"}, fixture.command.retrieved)
	require.Equal(t, "candidate-1", fixture.plans[0].Executions[0].CandidateID)
	<-fixture.dispatchCalls
}

func TestCreateViewerModelRunMapsActiveModelConflictAndRefundsQuota(t *testing.T) {
	fixture := newViewerRunFixture(t)
	fixture.runs.create = func(repositoryTypes.CreateInferenceIngestionProcessingRunPlan) (repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult, error) {
		return repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult{}, errors.New(apiError.DuplicateRecord)
	}

	_, err := fixture.service.CreateViewerModelRun(context.Background(), viewerRunInput())

	require.EqualError(t, err, apiError.RunAlreadyActive)
	require.Len(t, fixture.quota.reserved, 1)
	require.Len(t, fixture.quota.refunded, 1)
	require.Empty(t, fixture.dispatchCalls)
}

func TestCreateViewerModelRunNeverResolvesAnotherTenantsModel(t *testing.T) {
	fixture := newViewerRunFixture(t)
	input := viewerRunInput()
	input.ModelRef = "registration-other"

	_, err := fixture.service.CreateViewerModelRun(context.Background(), input)

	require.EqualError(t, err, apiError.MissingRecord)
	require.Empty(t, fixture.quota.reserved)
}

func TestCreateViewerModelRunAcceptsContainerIDAsModelReference(t *testing.T) {
	fixture := newViewerRunFixture(t)
	input := viewerRunInput()
	input.ModelRef = "container-1"

	result, err := fixture.service.CreateViewerModelRun(context.Background(), input)

	require.NoError(t, err)
	require.Equal(t, "registration-1", result.ModelID)
	<-fixture.dispatchCalls
}

func TestCreateViewerModelRunRejectsStudyThatIsNotLocal(t *testing.T) {
	fixture := newViewerRunFixture(t)
	fixture.service.OrthancAPIInterface = &manualReprocessOrthancAPI{local: false}

	_, err := fixture.service.CreateViewerModelRun(context.Background(), viewerRunInput())

	require.EqualError(t, err, apiError.StudyNotAvailableLocally)
	require.Empty(t, fixture.quota.reserved)
}

func TestCreateViewerModelRunReportsUnavailableModel(t *testing.T) {
	fixture := newViewerRunFixture(t)
	fixture.service.DockerInferenceAPIInterface = &viewerModelInfoAPI{err: errors.New("down")}
	fixture.service.inferenceContainerReadinessTimeout = 10 * time.Millisecond

	_, err := fixture.service.CreateViewerModelRun(context.Background(), viewerRunInput())

	require.EqualError(t, err, apiError.InferenceModelUnavailable)
}

func TestViewerJobModalityRequiresHintForMultiModalityModels(t *testing.T) {
	modality, err := viewerJobModality([]string{"xa"}, "")
	require.NoError(t, err)
	require.Equal(t, "XA", modality)

	_, err = viewerJobModality([]string{"XA", "US"}, "")
	require.EqualError(t, err, apiError.InvalidPayload)

	modality, err = viewerJobModality([]string{"XA", "US"}, "us")
	require.NoError(t, err)
	require.Equal(t, "US", modality)

	_, err = viewerJobModality([]string{"XA"}, "CT")
	require.EqualError(t, err, apiError.InvalidPayload)
}
