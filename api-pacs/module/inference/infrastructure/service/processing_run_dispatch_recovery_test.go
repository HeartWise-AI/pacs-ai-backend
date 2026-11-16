package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"api-pacs/module/inference/domain/entity"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

// recoveryRunRepository serializes the worker fake: the recovered dispatch
// runs in the background and reads and writes the same rows as the worker.
type recoveryRunRepository struct {
	mu sync.Mutex
	*reconciliationWorkerRunRepository
}

func (repository *recoveryRunRepository) SelectProcessingRun(ctx context.Context, tenantID, id string) (entity.InferenceIngestionProcessingRun, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.reconciliationWorkerRunRepository.SelectProcessingRun(ctx, tenantID, id)
}
func (repository *recoveryRunRepository) SelectProcessingRunExecution(ctx context.Context, tenantID, runID, candidateID, modelName string) (entity.InferenceIngestionProcessingJob, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.reconciliationWorkerRunRepository.SelectProcessingRunExecution(ctx, tenantID, runID, candidateID, modelName)
}
func (repository *recoveryRunRepository) UpdateProcessingRunAggregate(ctx context.Context, data repositoryTypes.UpdateInferenceIngestionProcessingRunAggregate) (entity.InferenceIngestionProcessingRun, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.reconciliationWorkerRunRepository.UpdateProcessingRunAggregate(ctx, data)
}
func (repository *recoveryRunRepository) RecordProcessingRunReconciliationAttempt(ctx context.Context, data repositoryTypes.RecordInferenceIngestionProcessingRunReconciliationAttempt) (entity.InferenceIngestionProcessingRun, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.reconciliationWorkerRunRepository.RecordProcessingRunReconciliationAttempt(ctx, data)
}
func (repository *recoveryRunRepository) ListProcessingRunsForReconciliation(ctx context.Context, data repositoryTypes.ListInferenceIngestionProcessingRunsForReconciliation) ([]entity.InferenceIngestionProcessingRun, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return append([]entity.InferenceIngestionProcessingRun(nil), repository.runs...), nil
}
func (repository *recoveryRunRepository) ListProcessingRunExecutions(_ context.Context, _, runID string) ([]entity.InferenceIngestionProcessingJob, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return append([]entity.InferenceIngestionProcessingJob(nil), repository.executions[runID]...), nil
}
func (repository *recoveryRunRepository) execution(runID string) entity.InferenceIngestionProcessingJob {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.executions[runID][0]
}

// recoveryCommandRepository persists the dispatch outcome into the run fake,
// as the real repository does in the database.
type recoveryCommandRepository struct {
	*guardedDispatchCommandRepository
	runs *recoveryRunRepository
}

func (repository *recoveryCommandRepository) UpdateInferenceIngestionProcessingJob(data repositoryTypes.UpdateInferenceIngestionProcessingJob) error {
	repository.runs.mu.Lock()
	defer repository.runs.mu.Unlock()
	for runID, executions := range repository.runs.executions {
		for index := range executions {
			if executions[index].ID == data.ID {
				executions[index].Status = data.Status
				executions[index].StudyServiceJobID = data.StudyServiceJobID
				executions[index].UpdatedAt = time.Now()
			}
		}
		repository.runs.executions[runID] = executions
	}
	return nil
}

type recoveryQueryRepository struct {
	*reconciliationWorkerQueryRepository
	job entity.InferenceIngestionJob
}

func (repository *recoveryQueryRepository) SelectInferenceIngestionCandidateByID(id string) (entity.InferenceIngestionCandidate, error) {
	return repository.candidates[id], nil
}
func (repository *recoveryQueryRepository) SelectInferenceIngestionJobByID(string) (entity.InferenceIngestionJob, error) {
	return repository.job, nil
}

// recoveryDispatcher is study-service: it can already know a job of the run.
type recoveryDispatcher struct {
	*guardedProcessingDispatcher
	jobsByRun []serviceTypes.StudyServiceJob
}

func (dispatcher *recoveryDispatcher) GetJobsByProcessingRun(context.Context, string, string) ([]serviceTypes.StudyServiceJob, error) {
	return dispatcher.jobsByRun, nil
}

type recoveryFixture struct {
	service    *InferenceCommandService
	runs       *recoveryRunRepository
	queries    *recoveryQueryRepository
	dispatcher *recoveryDispatcher
	metrics    *reconciliationMetricsRecorderTestDouble
	dispatched chan serviceTypes.DispatchStudyRequest
	release    chan struct{}
	runID      string
	key        string
}

// newRecoveryFixture is the state a gateway restart leaves behind: a retrieved
// candidate and a pending execution of an automatic run, both ten minutes old,
// and no job in study-service.
func newRecoveryFixture(t *testing.T) *recoveryFixture {
	t.Helper()
	t.Setenv(reconciliationPendingStaleMinutesEnv, "2")
	t.Setenv(reconciliationQueuedStaleMinutesEnv, "10")
	t.Setenv(reconciliationRunningStaleMinutesEnv, "65")
	t.Setenv(reconciliationModelRunningStaleMinutesEnv, "")
	t.Setenv("INFERENCE_INGESTION_RECONCILIATION_STALE_MINUTES", "")
	retries := studyServiceDispatchRetrySchedule
	studyServiceDispatchRetrySchedule = []time.Duration{0}
	t.Cleanup(func() { studyServiceDispatchRetrySchedule = retries })

	old := time.Now().Add(-10 * time.Minute)
	runID, candidateID, tenantID := "run-"+t.Name(), "candidate-"+t.Name(), "tenant-a"
	runs := &recoveryRunRepository{reconciliationWorkerRunRepository: &reconciliationWorkerRunRepository{
		runs: []entity.InferenceIngestionProcessingRun{{
			ID: runID, TenantID: tenantID, StudyInstanceUID: "study-1", Version: 1,
			RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto, Phase: entity.InferenceIngestionProcessingRunPhaseQueued,
		}},
		executions: map[string][]entity.InferenceIngestionProcessingJob{runID: {{
			ID: "execution-1", ProcessingRunID: &runID, CandidateID: candidateID, TenantID: tenantID, ModelName: "EchoPrime",
			Status: entity.InferenceIngestionProcessingJobStatusPending, CreatedAt: old, UpdatedAt: old,
		}}},
	}}
	queries := &recoveryQueryRepository{
		reconciliationWorkerQueryRepository: &reconciliationWorkerQueryRepository{candidates: map[string]entity.InferenceIngestionCandidate{candidateID: {
			ID: candidateID, TenantID: tenantID, StudyInstanceUID: "study-1", IngestionJobID: "job-1",
			Status: entity.InferenceIngestionCandidateStatusRetrieved, RetrievedAt: &old, UpdatedAt: time.Now(),
		}}},
		job: entity.InferenceIngestionJob{
			ID: "job-1", TenantID: tenantID, ContainerID: "container-1", ModelName: "EchoPrime", ModelVersion: "1.0",
			Status: entity.InferenceIngestionJobStatusRunning,
		},
	}
	fixture := &recoveryFixture{
		runs: runs, queries: queries, metrics: &reconciliationMetricsRecorderTestDouble{},
		dispatched: make(chan serviceTypes.DispatchStudyRequest, 8), release: make(chan struct{}),
		runID: runID, key: studyServiceDispatchKey(tenantID, runID, candidateID, "EchoPrime"),
	}
	fixture.dispatcher = &recoveryDispatcher{guardedProcessingDispatcher: &guardedProcessingDispatcher{
		response: serviceTypes.DispatchStudyResponse{JobID: "study-job-1"}, dispatchCall: fixture.dispatched, dispatchRelease: fixture.release,
	}}
	fixture.service = &InferenceCommandService{
		InferenceCommandRepositoryInterface:       &recoveryCommandRepository{guardedDispatchCommandRepository: &guardedDispatchCommandRepository{}, runs: runs},
		InferenceProcessingRunRepositoryInterface: runs,
		InferenceQueryRepositoryInterface:         queries,
		ProcessingDispatcherInterface:             fixture.dispatcher,
		ProcessingReconciliationMetricsRecorder:   fixture.metrics,
		StudyServiceDispatchSemaphore:             make(chan struct{}, 1),
	}
	configureReadyInferenceContainer(fixture.service)
	t.Cleanup(func() {
		// Let a dispatch still held open finish before the next test.
		select {
		case <-fixture.release:
		default:
			close(fixture.release)
		}
		require.Eventually(t, func() bool { return !liveStudyServiceDispatches.isLive(fixture.key) }, 5*time.Second, time.Millisecond)
	})
	return fixture
}

// cycle runs the reconciliation worker once and returns how many executions
// it dispatched again.
func (fixture *recoveryFixture) cycle(t *testing.T) int {
	t.Helper()
	require.NoError(t, fixture.service.ExecuteInferenceIngestionReconciliationWorker(context.Background()))
	return fixture.metrics.cycles[len(fixture.metrics.cycles)-1].Redispatched
}

func TestOrphanedPendingExecutionIsDispatchedAgainExactlyOnce(t *testing.T) {
	fixture := newRecoveryFixture(t)

	require.Equal(t, 1, fixture.cycle(t))
	request := <-fixture.dispatched
	require.Equal(t, "candidate-"+t.Name(), request.XRequestID, "the request ID study-service deduplicates on is the original one")
	require.Equal(t, fixture.runID, *request.ProcessingRunID)
	require.Equal(t, "EchoPrime", request.ModelName)
	require.Empty(t, request.DispatchIntent)

	// The dispatch is still under way (study-service has not answered): the
	// next cycles must not start another one.
	require.True(t, liveStudyServiceDispatches.isLive(fixture.key))
	require.Zero(t, fixture.cycle(t))
	require.Zero(t, fixture.cycle(t))
	require.Empty(t, fixture.dispatched)

	close(fixture.release)
	require.Eventually(t, func() bool { return !liveStudyServiceDispatches.isLive(fixture.key) }, 5*time.Second, time.Millisecond)
	execution := fixture.runs.execution(fixture.runID)
	require.Equal(t, entity.InferenceIngestionProcessingJobStatusQueued, execution.Status)
	require.Equal(t, "study-job-1", *execution.StudyServiceJobID)

	// Dispatched and recorded: the execution is no longer pending, so nothing
	// is sent again, however often the worker runs.
	require.Zero(t, fixture.cycle(t))
	require.Zero(t, fixture.cycle(t))
	require.Empty(t, fixture.dispatched)
	require.Equal(t, 1, fixture.dispatcher.dispatchCalls)
}

func TestExecutionStudyServiceAlreadyKnowsIsNeverDispatchedAgain(t *testing.T) {
	// The gateway restarted after study-service accepted the job but before
	// the gateway recorded it. Whatever study-service is doing with the job,
	// the gateway must not send the study a second time.
	for _, status := range []string{"queued", "running"} {
		t.Run(status, func(t *testing.T) {
			fixture := newRecoveryFixture(t)
			tenantID, candidateID := "tenant-a", "candidate-"+t.Name()
			startedAt := time.Now().Add(-time.Minute)
			job := serviceTypes.StudyServiceJob{
				JobID: "study-job-1", StudyInstanceUID: "study-1", TenantID: &tenantID, CandidateID: &candidateID,
				ProcessingRunID: &fixture.runID, ModelName: "EchoPrime", Status: status,
			}
			if status == "running" {
				job.StartedAt = &startedAt
			}
			fixture.dispatcher.jobsByRun = []serviceTypes.StudyServiceJob{job}
			require.Zero(t, fixture.cycle(t))
			require.Zero(t, fixture.cycle(t))
			require.False(t, liveStudyServiceDispatches.isLive(fixture.key))
			require.Empty(t, fixture.dispatched)
			require.Zero(t, fixture.dispatcher.dispatchCalls)
			if status == "queued" {
				execution := fixture.runs.execution(fixture.runID)
				require.Equal(t, entity.InferenceIngestionProcessingJobStatusQueued, execution.Status, "the existing job is adopted")
				require.Equal(t, "study-job-1", *execution.StudyServiceJobID)
			}
		})
	}
}

func TestPendingExecutionIsLeftAloneUnlessItsDispatchWasProvablyLost(t *testing.T) {
	recent := time.Now().Add(-30 * time.Second)
	tests := map[string]func(*recoveryFixture, string){
		"study still growing": func(f *recoveryFixture, candidateID string) {
			candidate := f.queries.candidates[candidateID]
			candidate.Status = entity.InferenceIngestionCandidateStatusGrowing
			f.queries.candidates[candidateID] = candidate
		},
		"queued for retrieval": func(f *recoveryFixture, candidateID string) {
			candidate := f.queries.candidates[candidateID]
			candidate.Status = entity.InferenceIngestionCandidateStatusRetrievalQueued
			f.queries.candidates[candidateID] = candidate
		},
		"retrieved a moment ago": func(f *recoveryFixture, candidateID string) {
			candidate := f.queries.candidates[candidateID]
			candidate.RetrievedAt = &recent
			f.queries.candidates[candidateID] = candidate
		},
		"execution created a moment ago": func(f *recoveryFixture, _ string) {
			f.runs.executions[f.runID][0].UpdatedAt = recent
			f.runs.runs[0].AttentionRequired = true // the run is examined all the same
		},
		"manual run": func(f *recoveryFixture, _ string) {
			f.runs.runs[0].RunTrigger = entity.InferenceIngestionProcessingRunTriggerManualReprocess
		},
		"viewer run": func(f *recoveryFixture, _ string) {
			f.runs.runs[0].RunTrigger = entity.InferenceIngestionProcessingRunTriggerViewerRequest
		},
		"ingestion job stopped": func(f *recoveryFixture, _ string) {
			f.queries.job.Status = entity.InferenceIngestionJobStatusStopped
		},
		"ingestion job of another model": func(f *recoveryFixture, _ string) {
			f.queries.job.ModelName = "PanEcho"
		},
		"ingestion job of another workspace": func(f *recoveryFixture, _ string) {
			f.queries.job.TenantID = "tenant-b"
		},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newRecoveryFixture(t)
			change(fixture, "candidate-"+t.Name())
			require.Zero(t, fixture.cycle(t))
			require.False(t, liveStudyServiceDispatches.isLive(fixture.key))
			require.Empty(t, fixture.dispatched)
			require.Equal(t, entity.InferenceIngestionProcessingJobStatusPending, fixture.runs.execution(fixture.runID).Status)
		})
	}

	t.Run("dispatch under way in this process", func(t *testing.T) {
		fixture := newRecoveryFixture(t)
		// The ordinary dispatch is waiting for a slot or retrying.
		liveStudyServiceDispatches.begin(fixture.key)
		defer liveStudyServiceDispatches.end(fixture.key)
		require.Zero(t, fixture.cycle(t))
		require.Empty(t, fixture.dispatched)
	})
}

func TestOrdinaryDispatchIsRecordedAsLiveUntilItEnds(t *testing.T) {
	fixture := newRecoveryFixture(t)
	candidate := fixture.queries.candidates["candidate-"+t.Name()]
	fixture.service.scheduleStudyServiceDispatch(fixture.queries.job, candidate, fixture.runID)
	require.True(t, liveStudyServiceDispatches.isLive(fixture.key), "registered before the goroutine starts")
	<-fixture.dispatched
	// While it is in flight the recovery never doubles it.
	require.Zero(t, fixture.cycle(t))
	require.Empty(t, fixture.dispatched)
	close(fixture.release)
	require.Eventually(t, func() bool { return !liveStudyServiceDispatches.isLive(fixture.key) }, 5*time.Second, time.Millisecond)
	require.Equal(t, 1, fixture.dispatcher.dispatchCalls)
}

func TestDispatchTrackerCountsConcurrentDispatchesOfOneExecution(t *testing.T) {
	tracker := &studyServiceDispatchTracker{live: map[string]int{}}
	require.True(t, tracker.beginIfIdle("k"))
	require.False(t, tracker.beginIfIdle("k"))
	tracker.begin("k")
	tracker.end("k")
	require.True(t, tracker.isLive("k"))
	tracker.end("k")
	require.False(t, tracker.isLive("k"))
	require.True(t, tracker.beginIfIdle("k"))
}
