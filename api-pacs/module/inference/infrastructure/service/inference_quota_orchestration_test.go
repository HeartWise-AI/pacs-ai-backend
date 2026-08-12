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

type quotaDockerInferenceAPI struct {
	dockerInferenceTypes.DockerInferenceAPIInterface
	response dockerInferenceTypes.PredictResponse
	err      error
	calls    int
}

func (api *quotaDockerInferenceAPI) Predict(context.Context, string, dockerInferenceTypes.PredictRequest) (dockerInferenceTypes.PredictResponse, error) {
	api.calls++
	return api.response, api.err
}

type recordingInferenceQuotaManager struct {
	mu         sync.Mutex
	reserved   []serviceTypes.InferenceQuotaReservation
	released   []serviceTypes.InferenceQuotaReservation
	refunded   []serviceTypes.InferenceQuotaReservation
	reserveErr error
	refundCall chan serviceTypes.InferenceQuotaReservation
}

func (manager *recordingInferenceQuotaManager) Reserve(_ context.Context, data serviceTypes.InferenceQuotaReservation) (serviceTypes.InferenceQuotaStatus, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.reserved = append(manager.reserved, data)
	return serviceTypes.InferenceQuotaStatus{}, manager.reserveErr
}

func (manager *recordingInferenceQuotaManager) Release(_ context.Context, data serviceTypes.InferenceQuotaReservation) (serviceTypes.InferenceQuotaStatus, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.released = append(manager.released, data)
	return serviceTypes.InferenceQuotaStatus{}, nil
}

func (manager *recordingInferenceQuotaManager) Refund(_ context.Context, data serviceTypes.InferenceQuotaReservation) (serviceTypes.InferenceQuotaStatus, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.refunded = append(manager.refunded, data)
	if manager.refundCall != nil {
		manager.refundCall <- data
	}
	return serviceTypes.InferenceQuotaStatus{}, nil
}

func (manager *recordingInferenceQuotaManager) Status(context.Context, string, string) (serviceTypes.InferenceQuotaStatus, error) {
	return serviceTypes.InferenceQuotaStatus{}, nil
}

func manualQuotaPlannerService(t *testing.T, quotaManager *recordingInferenceQuotaManager, createErr error) (*InferenceCommandService, <-chan serviceTypes.DispatchStudyRequest) {
	t.Helper()
	queryRepository := &processingRunPlannerQueryRepository{
		candidates: []entity.InferenceIngestionCandidate{{
			ID: "candidate-a", TenantID: "tenant-a", IngestionJobID: "job-a",
			StudyInstanceUID: "1.2.3", Status: entity.InferenceIngestionCandidateStatusRetrieved,
		}},
		jobs: map[string]entity.InferenceIngestionJob{
			"job-a": {ID: "job-a", TenantID: "tenant-a", ModelName: "EchoModel"},
		},
	}
	runRepository := &processingRunPlannerRepository{
		selectedExecution: entity.InferenceIngestionProcessingJob{
			ID: "execution-a", Status: entity.InferenceIngestionProcessingJobStatusPending,
		},
		create: func(data repositoryTypes.CreateInferenceIngestionProcessingRunPlan) (repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult, error) {
			if createErr != nil {
				return repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult{}, createErr
			}
			run := entity.InferenceIngestionProcessingRun{
				ID: data.Run.ID, TenantID: data.Run.TenantID, StudyInstanceUID: data.Run.StudyInstanceUID,
				RunTrigger: data.Run.RunTrigger, Phase: data.Run.Phase,
				RequestedByUserID: data.Run.RequestedByUserID,
			}
			return repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult{Run: run, Created: true}, nil
		},
	}
	dispatchCalls := make(chan serviceTypes.DispatchStudyRequest, 1)
	service := &InferenceCommandService{
		InferenceCommandRepositoryInterface:       &guardedDispatchCommandRepository{},
		InferenceQueryRepositoryInterface:         queryRepository,
		InferenceProcessingRunRepositoryInterface: runRepository,
		InferenceQuotaManagerInterface:            quotaManager,
		ProcessingDispatcherInterface: &guardedProcessingDispatcher{
			dispatchCall: dispatchCalls,
			response:     serviceTypes.DispatchStudyResponse{JobID: "study-service-job-a"},
		},
		StudyServiceDispatchSemaphore: make(chan struct{}, 1),
	}
	return service, dispatchCalls
}

func TestManualReprocessReservesOneUserScopedUnitAndPersistsRequester(t *testing.T) {
	quotaManager := &recordingInferenceQuotaManager{}
	service, dispatchCalls := manualQuotaPlannerService(t, quotaManager, nil)
	userID := "user-a"

	result, err := service.CreateManualStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", UserID: &userID,
	})

	require.NoError(t, err)
	require.NotNil(t, result.Run.RequestedByUserID)
	require.Equal(t, userID, *result.Run.RequestedByUserID)
	require.Len(t, quotaManager.reserved, 1)
	require.Equal(t, result.Run.ID, quotaManager.reserved[0].ReservationID)
	require.Equal(t, int64(1), quotaManager.reserved[0].Units)
	select {
	case request := <-dispatchCalls:
		require.Equal(t, result.Run.ID, trimmedPointerValue(request.ProcessingRunID))
		require.NotEmpty(t, request.XRequestID)
		require.NotEqual(t, "candidate-a", request.XRequestID)
	case <-time.After(time.Second):
		t.Fatal("manual processing run was not dispatched")
	}
}

func TestAutomaticIngestionRunIsExplicitlyExemptFromUserQuota(t *testing.T) {
	quotaManager := &recordingInferenceQuotaManager{}
	service, _ := manualQuotaPlannerService(t, quotaManager, nil)

	result, err := service.CreateAutomaticStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
	})

	require.NoError(t, err)
	require.Nil(t, result.Run.RequestedByUserID)
	require.Empty(t, quotaManager.reserved)
}

func TestManualReprocessRefundsReservationWhenPlanCreationFails(t *testing.T) {
	quotaManager := &recordingInferenceQuotaManager{}
	service, dispatchCalls := manualQuotaPlannerService(t, quotaManager, errors.New(apiError.DatabaseError))
	userID := "user-a"

	_, err := service.CreateManualStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", UserID: &userID,
	})

	require.EqualError(t, err, apiError.DatabaseError)
	require.Len(t, quotaManager.reserved, 1)
	require.Len(t, quotaManager.refunded, 1)
	require.Equal(t, quotaManager.reserved[0].ReservationID, quotaManager.refunded[0].ReservationID)
	require.Empty(t, dispatchCalls)
}

func TestManualReprocessPropagatesQuotaRejectionBeforeCreatingPlan(t *testing.T) {
	quotaManager := &recordingInferenceQuotaManager{reserveErr: &apiError.InferenceQuotaLimitError{
		ErrorCode: apiError.InferenceQuotaExceeded,
	}}
	service, dispatchCalls := manualQuotaPlannerService(t, quotaManager, nil)
	userID := "user-a"

	_, err := service.CreateManualStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: "tenant-a", StudyInstanceUID: "1.2.3", UserID: &userID,
	})

	require.EqualError(t, err, apiError.InferenceQuotaExceeded)
	require.Empty(t, quotaManager.refunded)
	require.Empty(t, dispatchCalls)
}

func TestManualReprocessRefundsQuotaWhenPreDispatchRequestBuildFails(t *testing.T) {
	job := entity.InferenceIngestionJob{
		ID: "job-a", TenantID: "tenant-a", ModelName: "EchoModel", DICOMModality: "US",
	}
	candidate := entity.InferenceIngestionCandidate{
		ID: "candidate-a", TenantID: "tenant-a", IngestionJobID: job.ID,
		StudyInstanceUID: "1.2.3", Status: entity.InferenceIngestionCandidateStatusRetrieved,
	}
	state := &realtimeWorklistE2EState{
		candidates: map[string]entity.InferenceIngestionCandidate{candidate.ID: candidate},
		jobs:       map[string]entity.InferenceIngestionJob{job.ID: job},
		runs:       map[string]entity.InferenceIngestionProcessingRun{},
		executions: map[string]entity.InferenceIngestionProcessingJob{},
	}
	refundCalls := make(chan serviceTypes.InferenceQuotaReservation, 1)
	quotaManager := &recordingInferenceQuotaManager{refundCall: refundCalls}
	dispatcher := &guardedProcessingDispatcher{buildErr: errors.New("orthanc study is unavailable")}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface:         state,
		InferenceCommandRepositoryInterface:       state,
		InferenceProcessingRunRepositoryInterface: state,
		InferenceQuotaManagerInterface:            quotaManager,
		ProcessingDispatcherInterface:             dispatcher,
		StudyServiceDispatchSemaphore:             make(chan struct{}, 1),
	}
	userID := "user-a"

	result, err := service.CreateManualStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: "tenant-a", StudyInstanceUID: candidate.StudyInstanceUID, UserID: &userID,
	})
	require.NoError(t, err)

	select {
	case refunded := <-refundCalls:
		require.Equal(t, result.Run.ID, refunded.ReservationID)
	case <-time.After(time.Second):
		t.Fatal("pre-dispatch failure did not refund the manual run quota")
	}

	terminalRun := state.runs[result.Run.ID]
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, terminalRun.Phase)
	require.True(t, terminalRun.AttentionRequired)
	require.True(t, hasProcessingRunAttentionReason(
		terminalRun.AttentionReasons, entity.InferenceIngestionProcessingRunAttentionDispatchFailed,
	))
	require.Len(t, result.Executions, 1)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusFailed, state.executions[result.Executions[0].ID].Status)
	require.Zero(t, dispatcher.dispatchCalls)
}

func TestTerminalManualRunReleasesOrRefundsReservationByDispatchPolicy(t *testing.T) {
	userID := "user-a"
	tests := []struct {
		name     string
		run      entity.InferenceIngestionProcessingRun
		refunded int
		released int
	}{
		{
			name: "accepted work remains charged",
			run: entity.InferenceIngestionProcessingRun{
				ID: "run-complete", TenantID: "tenant-a", RequestedByUserID: &userID,
				Phase: entity.InferenceIngestionProcessingRunPhaseTerminal,
			},
			released: 1,
		},
		{
			name: "pre-dispatch failure is refunded",
			run: entity.InferenceIngestionProcessingRun{
				ID: "run-dispatch-failed", TenantID: "tenant-a", RequestedByUserID: &userID,
				Phase: entity.InferenceIngestionProcessingRunPhaseTerminal,
				AttentionReasons: entity.InferenceIngestionProcessingRunAttentionReasons{{
					Code: entity.InferenceIngestionProcessingRunAttentionDispatchFailed,
				}},
			},
			refunded: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			quotaManager := &recordingInferenceQuotaManager{}
			service := &InferenceCommandService{InferenceQuotaManagerInterface: quotaManager}
			service.finalizeProcessingRunQuota(test.run)
			require.Len(t, quotaManager.released, test.released)
			require.Len(t, quotaManager.refunded, test.refunded)
		})
	}
}

func TestDirectPredictionChargesAcceptedWorkAndRefundsDispatchFailure(t *testing.T) {
	userID := "user-a"
	tests := []struct {
		name       string
		predictErr error
		released   int
		refunded   int
	}{
		{name: "successful prediction stays charged", released: 1},
		{name: "failed model dispatch is refunded", predictErr: errors.New(apiError.DockerInferenceError), refunded: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			quotaManager := &recordingInferenceQuotaManager{}
			dockerAPI := &quotaDockerInferenceAPI{response: dockerInferenceTypes.PredictResponse{Success: true}, err: test.predictErr}
			service := &InferenceCommandService{
				InferenceQuotaManagerInterface: quotaManager,
				DockerInferenceAPIInterface:    dockerAPI,
			}

			response, err := service.predictInferenceModelWithQuota(
				context.Background(), "tenant-a", "model-container", &userID, dockerInferenceTypes.PredictRequest{},
			)

			if test.predictErr == nil {
				require.NoError(t, err)
				require.True(t, response.Success)
			} else {
				require.EqualError(t, err, test.predictErr.Error())
			}
			require.Equal(t, 1, dockerAPI.calls)
			require.Len(t, quotaManager.reserved, 1)
			require.Len(t, quotaManager.released, test.released)
			require.Len(t, quotaManager.refunded, test.refunded)
		})
	}
}

func TestDirectPredictionDoesNotDispatchWhenQuotaRejectsReservation(t *testing.T) {
	userID := "user-a"
	quotaManager := &recordingInferenceQuotaManager{reserveErr: &apiError.InferenceQuotaLimitError{
		ErrorCode: apiError.InferenceConcurrencyExceeded,
	}}
	dockerAPI := &quotaDockerInferenceAPI{}
	service := &InferenceCommandService{
		InferenceQuotaManagerInterface: quotaManager,
		DockerInferenceAPIInterface:    dockerAPI,
	}

	_, err := service.predictInferenceModelWithQuota(
		context.Background(), "tenant-a", "model-container", &userID, dockerInferenceTypes.PredictRequest{},
	)

	require.EqualError(t, err, apiError.InferenceConcurrencyExceeded)
	require.Zero(t, dockerAPI.calls)
	require.Empty(t, quotaManager.released)
	require.Empty(t, quotaManager.refunded)
}

var _ domainRepository.InferenceQueryRepositoryInterface = (*processingRunPlannerQueryRepository)(nil)
