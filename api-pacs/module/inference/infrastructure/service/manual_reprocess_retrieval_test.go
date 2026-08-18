package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	"api-pacs/module/inference/domain/entity"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
	orthancApplication "api-pacs/module/orthanc/application"
	orthancServiceTypes "api-pacs/module/orthanc/infrastructure/service/types"
)

type manualRetrievalOrthancCommand struct {
	orthancApplication.OrthancCommandServiceInterface
	calls int
}

func (command *manualRetrievalOrthancCommand) RetrieveModalityStudyBySeries(
	context.Context,
	orthancServiceTypes.RetrieveModalityStudyBySeries,
) ([]orthancAPITypes.QueryModalityResponse, error) {
	command.calls++
	return []orthancAPITypes.QueryModalityResponse{{ID: "new-cmove-job"}}, nil
}

type manualRetrievalOrthancQuery struct {
	orthancApplication.OrthancQueryServiceInterface
	jobs []orthancAPITypes.GetJobResponse
	calls int
}

func (query *manualRetrievalOrthancQuery) GetJobsInfo(context.Context, []string) ([]orthancAPITypes.GetJobResponse, error) {
	query.calls++
	return query.jobs, nil
}

func manualReprocessRetrievalFixture() (
	*realtimeWorklistE2EState,
	entity.InferenceIngestionJob,
	entity.InferenceIngestionCandidate,
) {
	job := entity.InferenceIngestionJob{
		ID: "job-a", TenantID: "tenant-a", ModelName: "EchoModel", ModelVersion: "v1",
		DICOMModality: "US", Status: entity.InferenceIngestionJobStatusRunning,
	}
	lastRetrievalState := "local"
	candidate := entity.InferenceIngestionCandidate{
		ID: "candidate-a", TenantID: "tenant-a", IngestionJobID: job.ID,
		StudyInstanceUID: "1.2.3", Status: entity.InferenceIngestionCandidateStatusRetrieved,
		OrthancJobIDs: []string{"old-successful-cmove"}, LastRetrievalState: &lastRetrievalState,
	}
	state := &realtimeWorklistE2EState{
		candidates: map[string]entity.InferenceIngestionCandidate{candidate.ID: candidate},
		jobs:       map[string]entity.InferenceIngestionJob{job.ID: job},
		runs:       map[string]entity.InferenceIngestionProcessingRun{},
		executions: map[string]entity.InferenceIngestionProcessingJob{},
	}
	return state, job, candidate
}

func TestManualReprocessMissingFromOrthancQueuesFreshPACSRetrieval(t *testing.T) {
	state, _, candidate := manualReprocessRetrievalFixture()
	quotaManager := &recordingInferenceQuotaManager{}
	dispatcher := &guardedProcessingDispatcher{}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface:         state,
		InferenceCommandRepositoryInterface:       state,
		InferenceProcessingRunRepositoryInterface: state,
		InferenceQuotaManagerInterface:            quotaManager,
		OrthancAPIInterface:                       &manualReprocessOrthancAPI{local: false},
		ProcessingDispatcherInterface:             dispatcher,
	}
	userID := "user-a"

	result, err := service.CreateManualStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: candidate.TenantID, StudyInstanceUID: candidate.StudyInstanceUID, UserID: &userID,
	})

	require.NoError(t, err)
	require.Len(t, result.Executions, 1)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusPending, state.executions[result.Executions[0].ID].Status)
	reconciled := state.candidates[candidate.ID]
	require.Equal(t, entity.InferenceIngestionCandidateStatusRetrievalQueued, reconciled.Status)
	require.Empty(t, reconciled.OrthancJobIDs, "a fresh C-MOVE must not reuse the old successful Orthanc job")
	require.Equal(t, "queued", *reconciled.LastRetrievalState)
	require.Zero(t, dispatcher.buildCalls)
	require.Zero(t, dispatcher.dispatchCalls)
}

func TestManualReprocessRetrievalFailureTerminatesRunWithActionableReason(t *testing.T) {
	state, job, candidate := manualReprocessRetrievalFixture()
	quotaManager := &recordingInferenceQuotaManager{}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface:         state,
		InferenceCommandRepositoryInterface:       state,
		InferenceProcessingRunRepositoryInterface: state,
		InferenceQuotaManagerInterface:            quotaManager,
		OrthancAPIInterface:                       &manualReprocessOrthancAPI{local: false},
	}
	userID := "user-a"
	result, err := service.CreateManualStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: candidate.TenantID, StudyInstanceUID: candidate.StudyInstanceUID, UserID: &userID,
	})
	require.NoError(t, err)

	err = service.persistCandidateRetrievalResult(context.Background(), job, state.candidates[candidate.ID], candidateRetrievalResult{
		Outcome:            candidateRetrievalOutcomeFailure,
		LastRetrievalState: stringPointer("failure"),
		LastRetrievalError: stringPointer("remote PACS rejected C-MOVE"),
	})

	require.NoError(t, err)
	execution := state.executions[result.Executions[0].ID]
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusFailed, execution.Status)
	require.NotNil(t, execution.ErrorMessage)
	require.Contains(t, *execution.ErrorMessage, "PACS retrieval failure")
	require.Contains(t, *execution.ErrorMessage, "remote PACS rejected C-MOVE")
	run := state.runs[result.Run.ID]
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, run.Phase)
	require.True(t, run.AttentionRequired)
	require.True(t, hasProcessingRunAttentionReason(
		run.AttentionReasons, entity.InferenceIngestionProcessingRunAttentionRetrievalFailed,
	))
	require.Len(t, quotaManager.refunded, 1)
}

func TestSuccessfulReretrievalDispatchesCommittedManualExecution(t *testing.T) {
	state, job, candidate := manualReprocessRetrievalFixture()
	dispatchCalls := make(chan serviceTypes.DispatchStudyRequest, 1)
	dispatcher := &guardedProcessingDispatcher{
		dispatchCall: dispatchCalls, response: serviceTypes.DispatchStudyResponse{JobID: "study-job-a"},
		echoManualCorrelation: true,
	}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface:         state,
		InferenceCommandRepositoryInterface:       state,
		InferenceProcessingRunRepositoryInterface: state,
		InferenceQuotaManagerInterface:            &recordingInferenceQuotaManager{},
		OrthancAPIInterface:                       &manualReprocessOrthancAPI{local: false},
		ProcessingDispatcherInterface:             dispatcher,
		StudyServiceDispatchSemaphore:             make(chan struct{}, 1),
	}
	userID := "user-a"
	result, err := service.CreateManualStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: candidate.TenantID, StudyInstanceUID: candidate.StudyInstanceUID, UserID: &userID,
	})
	require.NoError(t, err)

	err = service.persistCandidateRetrievalResult(context.Background(), job, state.candidates[candidate.ID], candidateRetrievalResult{
		Outcome:            candidateRetrievalOutcomeSuccess,
		OrthancJobIDs:      []string{"new-cmove-job"},
		LastRetrievalState: stringPointer("success"),
	})
	require.NoError(t, err)

	select {
	case request := <-dispatchCalls:
		require.Equal(t, serviceTypes.DispatchStudyIntentManualReprocess, request.DispatchIntent)
		require.Equal(t, result.Run.ID, trimmedPointerValue(request.ProcessingRunID))
		require.Equal(t, result.Executions[0].ID, trimmedPointerValue(request.ProcessingExecutionID))
		require.Equal(t, result.Executions[0].ID, request.XRequestID)
	case <-time.After(time.Second):
		t.Fatal("retrieved manual execution was not dispatched")
	}
}

func TestFreshManualRetrievalTriggersCMoveAndConfirmsLocalStudy(t *testing.T) {
	state, job, candidate := manualReprocessRetrievalFixture()
	require.NoError(t, state.MarkCandidateRetrievalQueued(candidate.ID))
	command := &manualRetrievalOrthancCommand{}
	service := &InferenceCommandService{
		InferenceCommandRepositoryInterface: state,
		OrthancAPIInterface: &manualReprocessOrthancAPI{
			localResponses: []bool{false, true},
		},
		OrthancCommandServiceInterface: command,
		OrthancQueryServiceInterface: &manualRetrievalOrthancQuery{jobs: []orthancAPITypes.GetJobResponse{{
			ID: "new-cmove-job", State: string(orthancAPITypes.JobSuccess),
		}}},
	}

	result, err := service.retrieveStableCandidate(context.Background(), job, state.candidates[candidate.ID])

	require.NoError(t, err)
	require.Equal(t, 1, command.calls)
	require.Equal(t, candidateRetrievalOutcomeLocal, result.Outcome)
	require.Equal(t, []string{"new-cmove-job"}, result.OrthancJobIDs)
	require.Equal(t, []string{"new-cmove-job"}, []string(state.candidates[candidate.ID].OrthancJobIDs))
}

func TestSuccessfulCMoveWithoutLocalStudyIsRetrievalFailure(t *testing.T) {
	service := &InferenceCommandService{
		OrthancAPIInterface: &manualReprocessOrthancAPI{local: false},
		OrthancQueryServiceInterface: &manualRetrievalOrthancQuery{jobs: []orthancAPITypes.GetJobResponse{{
			ID: "cmove-job-a", State: string(orthancAPITypes.JobSuccess),
		}}},
	}

	result, err := service.waitForCandidateRetrieval(context.Background(), "1.2.3", []string{"cmove-job-a"})

	require.NoError(t, err)
	require.Equal(t, candidateRetrievalOutcomeFailure, result.Outcome)
	require.NotNil(t, result.LastRetrievalError)
	require.Contains(t, *result.LastRetrievalError, "still missing locally")
}

func TestPartialLocalStudyDoesNotCompleteRetrievalWhileCMoveIsPending(t *testing.T) {
	query := &manualRetrievalOrthancQuery{jobs: []orthancAPITypes.GetJobResponse{
		{ID: "cmove-job-a", State: string(orthancAPITypes.JobSuccess)},
		{ID: "cmove-job-b", State: string(orthancAPITypes.JobRunning)},
	}}
	service := &InferenceCommandService{
		OrthancAPIInterface:          &manualReprocessOrthancAPI{local: true},
		OrthancQueryServiceInterface: query,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := service.waitForCandidateRetrieval(ctx, "1.2.3", []string{"cmove-job-a", "cmove-job-b"})

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, query.calls, "retrieval must inspect every C-MOVE job before accepting a locally visible study")
}

func TestManualReprocessLocalCheckFailureDoesNotReserveQuotaOrCreateRun(t *testing.T) {
	state, _, candidate := manualReprocessRetrievalFixture()
	quotaManager := &recordingInferenceQuotaManager{}
	service := &InferenceCommandService{
		InferenceQueryRepositoryInterface:         state,
		InferenceCommandRepositoryInterface:       state,
		InferenceProcessingRunRepositoryInterface: state,
		InferenceQuotaManagerInterface:            quotaManager,
		OrthancAPIInterface:                       &manualReprocessOrthancAPI{err: errors.New("Orthanc unavailable")},
	}
	userID := "user-a"

	_, err := service.CreateManualStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: candidate.TenantID, StudyInstanceUID: candidate.StudyInstanceUID, UserID: &userID,
	})

	require.ErrorContains(t, err, "cannot verify local study before manual reprocess")
	require.Empty(t, state.runs)
	require.Empty(t, quotaManager.reserved)
}
