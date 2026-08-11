package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	domainRepository "api-pacs/module/inference/domain/repository"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

// realtimeWorklistE2EState is an in-memory transactional boundary used to
// exercise the complete Go orchestration contract without writing a live DB.
type realtimeWorklistE2EState struct {
	domainRepository.InferenceQueryRepositoryInterface
	domainRepository.InferenceCommandRepositoryInterface
	domainRepository.InferenceProcessingRunRepositoryInterface
	candidates      map[string]entity.InferenceIngestionCandidate
	jobs            map[string]entity.InferenceIngestionJob
	runs            map[string]entity.InferenceIngestionProcessingRun
	executions      map[string]entity.InferenceIngestionProcessingJob
	dispatchUpdates chan string
}

func (state *realtimeWorklistE2EState) ListInferenceIngestionCandidates(data repositoryTypes.ListInferenceIngestionCandidates) ([]entity.InferenceIngestionCandidate, error) {
	result := make([]entity.InferenceIngestionCandidate, 0)
	for _, candidate := range state.candidates {
		if candidate.TenantID != data.TenantID || data.StudyInstanceUID == nil || candidate.StudyInstanceUID != *data.StudyInstanceUID {
			continue
		}
		result = append(result, candidate)
	}
	return result, nil
}

func (state *realtimeWorklistE2EState) SelectInferenceIngestionJobByID(id string) (entity.InferenceIngestionJob, error) {
	job, exists := state.jobs[id]
	if !exists {
		return entity.InferenceIngestionJob{}, errors.New(apiError.MissingRecord)
	}
	return job, nil
}

func (state *realtimeWorklistE2EState) SelectInferenceIngestionCandidateByID(id string) (entity.InferenceIngestionCandidate, error) {
	candidate, exists := state.candidates[id]
	if !exists {
		return entity.InferenceIngestionCandidate{}, errors.New(apiError.MissingRecord)
	}
	return candidate, nil
}

func (state *realtimeWorklistE2EState) SelectActiveProcessingRun(_ context.Context, tenantID, studyUID string) (entity.InferenceIngestionProcessingRun, error) {
	for _, run := range state.runs {
		if run.TenantID == tenantID && run.StudyInstanceUID == studyUID && run.Phase != entity.InferenceIngestionProcessingRunPhaseTerminal {
			return run, nil
		}
	}
	return entity.InferenceIngestionProcessingRun{}, errors.New(apiError.MissingRecord)
}

func (state *realtimeWorklistE2EState) CreateProcessingRunPlan(_ context.Context, data repositoryTypes.CreateInferenceIngestionProcessingRunPlan) (repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult, error) {
	runNumber := 1
	for _, existing := range state.runs {
		if existing.TenantID == data.Run.TenantID && existing.StudyInstanceUID == data.Run.StudyInstanceUID && existing.RunNumber >= runNumber {
			runNumber = existing.RunNumber + 1
		}
	}
	now := time.Now().UTC()
	run := entity.InferenceIngestionProcessingRun{
		ID: data.Run.ID, TenantID: data.Run.TenantID, StudyInstanceUID: data.Run.StudyInstanceUID,
		RunNumber: runNumber, RunTrigger: data.Run.RunTrigger, Phase: data.Run.Phase,
		RequestedByUserID: data.Run.RequestedByUserID,
		Version:           1, CreatedAt: now, UpdatedAt: now,
	}
	state.runs[run.ID] = run
	executions := make([]entity.InferenceIngestionProcessingJob, 0, len(data.Executions))
	for _, expected := range data.Executions {
		runID := run.ID
		execution := entity.InferenceIngestionProcessingJob{
			ID: expected.ID, ProcessingRunID: &runID, CandidateID: expected.CandidateID,
			TenantID: run.TenantID, ModelName: expected.ModelName, ModelVersion: expected.ModelVersion,
			Modality: expected.Modality, Status: entity.InferenceIngestionProcessingJobStatusPending,
			CreatedAt: now, UpdatedAt: now,
		}
		state.executions[execution.ID] = execution
		executions = append(executions, execution)
	}
	return repositoryTypes.CreateInferenceIngestionProcessingRunPlanResult{Run: run, Executions: executions, Created: true}, nil
}

func (state *realtimeWorklistE2EState) SelectProcessingRun(_ context.Context, tenantID, runID string) (entity.InferenceIngestionProcessingRun, error) {
	run, exists := state.runs[runID]
	if !exists || run.TenantID != tenantID {
		return entity.InferenceIngestionProcessingRun{}, errors.New(apiError.MissingRecord)
	}
	return run, nil
}

func (state *realtimeWorklistE2EState) ListProcessingRunExecutions(_ context.Context, tenantID, runID string) ([]entity.InferenceIngestionProcessingJob, error) {
	result := make([]entity.InferenceIngestionProcessingJob, 0)
	for _, execution := range state.executions {
		if execution.TenantID == tenantID && execution.ProcessingRunID != nil && *execution.ProcessingRunID == runID {
			result = append(result, execution)
		}
	}
	return result, nil
}

func (state *realtimeWorklistE2EState) SelectProcessingRunExecution(_ context.Context, tenantID, runID, candidateID, modelName string) (entity.InferenceIngestionProcessingJob, error) {
	for _, execution := range state.executions {
		if execution.TenantID == tenantID && execution.ProcessingRunID != nil && *execution.ProcessingRunID == runID &&
			execution.CandidateID == candidateID && execution.ModelName == modelName {
			return execution, nil
		}
	}
	return entity.InferenceIngestionProcessingJob{}, errors.New(apiError.MissingRecord)
}

func (state *realtimeWorklistE2EState) UpdateInferenceIngestionProcessingJob(data repositoryTypes.UpdateInferenceIngestionProcessingJob) error {
	execution, exists := state.executions[data.ID]
	if !exists {
		return errors.New(apiError.MissingRecord)
	}
	execution.Status = data.Status
	execution.StudyServiceJobID = data.StudyServiceJobID
	execution.ModelVersion = data.ModelVersion
	execution.Modality = data.Modality
	execution.UpdatedAt = time.Now().UTC()
	state.executions[data.ID] = execution
	if state.dispatchUpdates != nil {
		state.dispatchUpdates <- data.ID
	}
	return nil
}

func (state *realtimeWorklistE2EState) UpdateCandidateDispatchState(repositoryTypes.UpdateCandidateDispatchState) error {
	return nil
}

func (state *realtimeWorklistE2EState) ApplyProcessingRunExecutionTransition(_ context.Context, data repositoryTypes.ApplyInferenceIngestionProcessingTransition) (repositoryTypes.ApplyInferenceIngestionProcessingTransitionResult, error) {
	execution, exists := state.executions[data.ExecutionID]
	if !exists || execution.ProcessingRunID == nil || *execution.ProcessingRunID != data.ProcessingRunID {
		return repositoryTypes.ApplyInferenceIngestionProcessingTransitionResult{}, errors.New(apiError.MissingRecord)
	}
	execution.Status = data.Status
	execution.LastEventID = data.EventID
	execution.LastEventSequence = data.EventSequence
	execution.SkipReasonCode = nil
	execution.SkipReasonMessage = nil
	if data.SkipReason != nil {
		execution.SkipReasonCode = &data.SkipReason.Code
		execution.SkipReasonMessage = data.SkipReason.Message
	}
	execution.ErrorMessage = data.ErrorMessage
	execution.StartedAt = data.StartedAt
	execution.CompletedAt = data.CompletedAt
	execution.UpdatedAt = time.Now().UTC()
	state.executions[execution.ID] = execution

	run := state.runs[data.ProcessingRunID]
	executions, _ := state.ListProcessingRunExecutions(context.Background(), run.TenantID, run.ID)
	aggregate := entity.AggregateInferenceIngestionProcessingRun(entity.InferenceIngestionProcessingRunAggregationInput{
		Run: run, Executions: executions, WholeRunCancelled: verificationExecutionsAllCancelled(executions),
	})
	run.Phase = aggregate.Phase
	run.Outcome = aggregate.Outcome
	run.AttentionRequired = aggregate.AttentionRequired
	run.AttentionReasons = aggregate.AttentionReasons
	run.StartedAt = aggregate.StartedAt
	run.CompletedAt = aggregate.CompletedAt
	run.Version = aggregate.NextVersion
	run.UpdatedAt = time.Now().UTC()
	state.runs[run.ID] = run
	return repositoryTypes.ApplyInferenceIngestionProcessingTransitionResult{
		Outcome: "applied", Changed: true, Execution: execution, Run: run, Counts: aggregate.Counts,
	}, nil
}

func (state *realtimeWorklistE2EState) ListWorklistStudyStatuses(data repositoryTypes.ListWorklistStudyStatuses) (repositoryTypes.WorklistStudyStatusPage, error) {
	var latest *entity.InferenceIngestionProcessingRun
	for _, run := range state.runs {
		if run.TenantID != data.TenantID {
			continue
		}
		if len(data.StudyInstanceUIDs) > 0 && run.StudyInstanceUID != data.StudyInstanceUIDs[0] {
			continue
		}
		if latest == nil || run.RunNumber > latest.RunNumber {
			copy := run
			latest = &copy
		}
	}
	if latest == nil {
		return repositoryTypes.WorklistStudyStatusPage{Studies: []repositoryTypes.WorklistStudyStatus{}}, nil
	}
	executions, _ := state.ListProcessingRunExecutions(context.Background(), latest.TenantID, latest.ID)
	counts := entity.AggregateInferenceIngestionProcessingRun(entity.InferenceIngestionProcessingRunAggregationInput{
		Run: *latest, Executions: executions, WholeRunCancelled: verificationExecutionsAllCancelled(executions),
	}).Counts
	runID, runNumber, trigger, phase, version := latest.ID, latest.RunNumber, latest.RunTrigger, latest.Phase, latest.Version
	return repositoryTypes.WorklistStudyStatusPage{Studies: []repositoryTypes.WorklistStudyStatus{{
		StudyInstanceUID: latest.StudyInstanceUID, IngestionStatus: entity.InferenceIngestionCandidateStatusRetrieved,
		RunID: &runID, RunNumber: &runNumber, RunTrigger: &trigger, Phase: &phase, Outcome: latest.Outcome,
		AttentionRequired: latest.AttentionRequired, AttentionReasons: latest.AttentionReasons,
		ExpectedModels: counts.Expected, PendingModels: counts.Pending, QueuedModels: counts.Queued,
		RunningModels: counts.Running, CompletedModels: counts.Completed, FailedModels: counts.Failed,
		SkippedModels: counts.Skipped, CancelledModels: counts.Cancelled, ActiveModels: counts.Active,
		Version: &version, StartedAt: latest.StartedAt, CompletedAt: latest.CompletedAt, UpdatedAt: latest.UpdatedAt,
	}}}, nil
}

func (state *realtimeWorklistE2EState) ListProcessingRunHistoryPage(_ context.Context, data repositoryTypes.ListInferenceIngestionProcessingRuns) (repositoryTypes.InferenceIngestionProcessingRunHistoryPage, error) {
	runs := make([]entity.InferenceIngestionProcessingRun, 0)
	for _, run := range state.runs {
		if run.TenantID == data.TenantID && run.StudyInstanceUID == data.StudyInstanceUID {
			runs = append(runs, run)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].RunNumber > runs[j].RunNumber })
	return repositoryTypes.InferenceIngestionProcessingRunHistoryPage{Runs: runs}, nil
}

func (state *realtimeWorklistE2EState) ListProcessingRunExecutionsByRunIDs(_ context.Context, data repositoryTypes.ListInferenceIngestionProcessingRunExecutions) ([]entity.InferenceIngestionProcessingJob, error) {
	wanted := make(map[string]struct{}, len(data.ProcessingRunIDs))
	for _, runID := range data.ProcessingRunIDs {
		wanted[runID] = struct{}{}
	}
	result := make([]entity.InferenceIngestionProcessingJob, 0)
	for _, execution := range state.executions {
		if execution.TenantID == data.TenantID && execution.ProcessingRunID != nil {
			if _, exists := wanted[*execution.ProcessingRunID]; exists {
				result = append(result, execution)
			}
		}
	}
	return result, nil
}

type realtimeWorklistE2EDispatcher struct{ http *StudyServiceDispatcher }

func (dispatcher *realtimeWorklistE2EDispatcher) BuildDispatchStudyRequest(_ context.Context, data serviceTypes.BuildStudyServiceDispatchRequestInput) (serviceTypes.DispatchStudyRequest, error) {
	requestID := trimmedPointerValue(data.RequestID)
	if requestID == "" {
		requestID = data.Candidate.ID
	}
	return serviceTypes.DispatchStudyRequest{
		XRequestID: requestID, TenantID: &data.Candidate.TenantID,
		IngestionJobID: &data.IngestionJob.ID, CandidateID: &data.Candidate.ID,
		ProcessingRunID: data.ProcessingRunID, StudyInstanceUID: data.Candidate.StudyInstanceUID,
		OrthancStudyID: "orthanc-study", Modality: "US", ModelName: data.IngestionJob.ModelName,
		ModelVersion: data.IngestionJob.ModelVersion,
	}, nil
}

func (dispatcher *realtimeWorklistE2EDispatcher) DispatchStudy(ctx context.Context, data serviceTypes.DispatchStudyRequest) (serviceTypes.DispatchStudyResponse, error) {
	return dispatcher.http.DispatchStudy(ctx, data)
}
func (*realtimeWorklistE2EDispatcher) GetJobByID(context.Context, string, string) (serviceTypes.StudyServiceJob, bool, error) {
	return serviceTypes.StudyServiceJob{}, false, nil
}
func (*realtimeWorklistE2EDispatcher) GetJobsByProcessingRun(context.Context, string, string) ([]serviceTypes.StudyServiceJob, error) {
	return nil, nil
}
func (*realtimeWorklistE2EDispatcher) GetJobsByCandidate(context.Context, string, string) ([]serviceTypes.StudyServiceJob, error) {
	return nil, nil
}
func (*realtimeWorklistE2EDispatcher) GetCallbackDeadLetters(context.Context, string) ([]serviceTypes.StudyServiceCallbackDeadLetter, error) {
	return nil, nil
}

func TestRealtimeWorklistAutomaticMixedOutcomeAndManualHistoryEndToEnd(t *testing.T) {
	state := &realtimeWorklistE2EState{
		candidates: map[string]entity.InferenceIngestionCandidate{}, jobs: map[string]entity.InferenceIngestionJob{},
		runs: map[string]entity.InferenceIngestionProcessingRun{}, executions: map[string]entity.InferenceIngestionProcessingJob{},
	}
	for _, fixture := range []struct{ suffix, model string }{{"a", "EchoModel"}, {"b", "ECGModel"}} {
		jobID, candidateID := "job-"+fixture.suffix, "candidate-"+fixture.suffix
		modalities := pq.StringArray{"US"}
		state.jobs[jobID] = entity.InferenceIngestionJob{
			ID: jobID, TenantID: "tenant-a", ModelName: fixture.model, ModelVersion: "v1", DICOMModality: "US", Modalities: modalities,
		}
		state.candidates[candidateID] = entity.InferenceIngestionCandidate{
			ID: candidateID, TenantID: "tenant-a", IngestionJobID: jobID, StudyInstanceUID: "study-1",
			Status: entity.InferenceIngestionCandidateStatusRetrieved,
		}
	}

	type dispatchRecord struct {
		request   serviceTypes.DispatchStudyRequest
		requestID string
	}
	dispatched := make([]dispatchRecord, 0, 4)
	python := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		require.Equal(t, "/ingest/study", request.URL.Path)
		var payload serviceTypes.DispatchStudyRequest
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		require.NotNil(t, payload.ProcessingRunID)
		dispatched = append(dispatched, dispatchRecord{
			request: payload, requestID: request.Header.Get("X-Request-ID"),
		})
		w.WriteHeader(http.StatusAccepted)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"job_id": "python-" + strings.ToLower(payload.ModelName)}))
	}))
	defer python.Close()
	dispatcher := &realtimeWorklistE2EDispatcher{http: &StudyServiceDispatcher{
		StudyServiceBaseURL: python.URL, StudyServiceClient: python.Client(),
	}}
	broker := NewWorklistNotificationBroker()
	command := &InferenceCommandService{
		InferenceQueryRepositoryInterface: state, InferenceCommandRepositoryInterface: state,
		InferenceProcessingRunRepositoryInterface: state, ProcessingDispatcherInterface: dispatcher,
		WorklistNotificationPublisherInterface: broker,
		InferenceQuotaManagerInterface:         &allowAllInferenceQuotaManager{},
		StudyServiceDispatchSemaphore:          make(chan struct{}, 1),
	}
	query := &InferenceQueryService{InferenceQueryRepositoryInterface: state, InferenceProcessingRunRepositoryInterface: state}

	automatic, err := command.CreateAutomaticStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: "tenant-a", StudyInstanceUID: "study-1",
	})
	require.NoError(t, err)
	require.True(t, automatic.Created)
	require.Len(t, automatic.Executions, 2)
	for _, execution := range automatic.Executions {
		candidate := state.candidates[execution.CandidateID]
		require.NoError(t, command.dispatchRetrievedCandidateToStudyService(
			context.Background(), state.jobs[candidate.IngestionJobID], candidate, automatic.Run.ID, candidate.ID,
		))
	}
	require.Len(t, dispatched, 2)
	for _, dispatch := range dispatched {
		require.Equal(t, automatic.Run.ID, *dispatch.request.ProcessingRunID)
	}

	tenantAEvents, unsubscribeA := broker.SubscribeWorklistNotifications("tenant-a", 4)
	defer unsubscribeA()
	tenantBEvents, unsubscribeB := broker.SubscribeWorklistNotifications("tenant-b", 4)
	defer unsubscribeB()
	terminal := []entity.InferenceIngestionProcessingJobStatus{
		entity.InferenceIngestionProcessingJobStatusCompleted,
		entity.InferenceIngestionProcessingJobStatusFailed,
	}
	for index, execution := range automatic.Executions {
		candidate := state.candidates[execution.CandidateID]
		persisted, selectErr := state.SelectProcessingRunExecution(context.Background(), "tenant-a", automatic.Run.ID, candidate.ID, execution.ModelName)
		require.NoError(t, selectErr)
		sequence := int64(3)
		now := time.Now().UTC()
		callback, callbackErr := command.HandleStudyServiceProcessingCallback(context.Background(), serviceTypes.HandleStudyServiceProcessingCallback{
			CandidateID: candidate.ID, PayloadCandidateID: candidate.ID, TenantID: "tenant-a",
			IngestionJobID: candidate.IngestionJobID, ProcessingRunID: automatic.Run.ID,
			StudyInstanceUID: "study-1", ModelName: execution.ModelName, ModelVersion: "v1", Modality: "US",
			StudyServiceJobID: *persisted.StudyServiceJobID, Status: string(terminal[index]),
			EventID: "event-" + execution.ID, Sequence: &sequence, CompletedAt: &now,
		})
		require.NoError(t, callbackErr)
		require.Equal(t, "applied", callback.Outcome)
	}
	for range 2 {
		select {
		case event := <-tenantAEvents:
			require.Equal(t, automatic.Run.ID, event.RunID)
		case <-time.After(time.Second):
			t.Fatal("tenant A did not receive committed worklist event")
		}
	}
	select {
	case <-tenantBEvents:
		t.Fatal("tenant B received tenant A worklist event")
	default:
	}

	snapshot, err := query.GetWorklistStudyStatuses(context.Background(), serviceTypes.GetWorklistStudyStatuses{
		TenantID: "tenant-a", StudyInstanceUIDs: []string{"study-1"}, Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, snapshot.Studies, 1)
	require.Equal(t, entity.InferenceIngestionProcessingRunPhaseTerminal, *snapshot.Studies[0].Phase)
	require.Equal(t, entity.InferenceIngestionProcessingRunOutcomePartialSuccess, *snapshot.Studies[0].Outcome)
	require.Equal(t, 1, snapshot.Studies[0].Completed)
	require.Equal(t, 1, snapshot.Studies[0].Failed)

	manualUserID := "admin-a"
	state.dispatchUpdates = make(chan string, 2)
	manual, err := command.CreateManualStudyProcessingRun(context.Background(), serviceTypes.CreateStudyProcessingRun{
		TenantID: "tenant-a", StudyInstanceUID: "study-1", UserID: &manualUserID,
	})
	require.NoError(t, err)
	require.Equal(t, 2, manual.Run.RunNumber)
	require.Equal(t, entity.InferenceIngestionProcessingRunTriggerManualReprocess, manual.Run.RunTrigger)
	for range manual.Executions {
		select {
		case <-state.dispatchUpdates:
		case <-time.After(time.Second):
			t.Fatal("manual processing execution was not dispatched")
		}
	}
	require.Len(t, dispatched, 4)
	for _, dispatch := range dispatched[2:] {
		require.Equal(t, manual.Run.ID, trimmedPointerValue(dispatch.request.ProcessingRunID))
		require.NotEmpty(t, dispatch.requestID)
		require.NotEqual(t, trimmedPointerValue(dispatch.request.CandidateID), dispatch.requestID)
	}
	history, err := query.GetStudyProcessingRunHistory(context.Background(), serviceTypes.GetStudyProcessingRunHistory{
		TenantID: "tenant-a", StudyInstanceUID: "study-1", Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, history.Runs, 2)
	require.Equal(t, manual.Run.ID, history.Runs[0].RunID)
	require.Equal(t, automatic.Run.ID, history.Runs[1].RunID)
	require.Equal(t, entity.InferenceIngestionProcessingRunOutcomePartialSuccess, *history.Runs[1].Outcome)

	_, err = query.GetProcessingRunDetail(context.Background(), serviceTypes.GetProcessingRunDetail{
		TenantID: "tenant-b", RunID: automatic.Run.ID,
	})
	require.EqualError(t, err, apiError.MissingRecord)
}
