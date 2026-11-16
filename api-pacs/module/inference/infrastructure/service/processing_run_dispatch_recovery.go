package service

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/service/types"
)

// A dispatch to study-service runs in a goroutine of this process: it waits
// for a dispatch slot, makes sure the model container answers, and retries.
// Nothing durable says "a dispatch is under way", so a gateway restart loses
// it: the candidate stays RETRIEVED and its execution stays pending for ever.
//
// liveStudyServiceDispatches is the in-process record of dispatches under way.
// The reconciliation worker uses it to tell an orphaned execution from one
// that is merely slow, and to start at most one recovery per execution.
var liveStudyServiceDispatches = &studyServiceDispatchTracker{live: map[string]int{}}

type studyServiceDispatchTracker struct {
	mu   sync.Mutex
	live map[string]int
}

func studyServiceDispatchKey(tenantID, processingRunID, candidateID, modelName string) string {
	return strings.TrimSpace(tenantID) + "\x1f" + strings.TrimSpace(processingRunID) + "\x1f" +
		strings.TrimSpace(candidateID) + "\x1f" + strings.TrimSpace(modelName)
}

func (tracker *studyServiceDispatchTracker) begin(key string) {
	tracker.mu.Lock()
	tracker.live[key]++
	tracker.mu.Unlock()
}

// beginIfIdle registers a dispatch only when none is under way for the key.
func (tracker *studyServiceDispatchTracker) beginIfIdle(key string) bool {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.live[key] > 0 {
		return false
	}
	tracker.live[key]++
	return true
}

func (tracker *studyServiceDispatchTracker) end(key string) {
	tracker.mu.Lock()
	if tracker.live[key]--; tracker.live[key] <= 0 {
		delete(tracker.live, key)
	}
	tracker.mu.Unlock()
}

func (tracker *studyServiceDispatchTracker) isLive(key string) bool {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return tracker.live[key] > 0
}

// redispatchOrphanedExecution dispatches again an automatic execution whose
// dispatch was lost, typically by a gateway restart. The caller has already
// asked study-service, by processing run and by candidate, and found no job
// for it. It returns whether a dispatch was started and, when not, why.
//
// The rule is deliberately narrow. Everything must hold:
//   - the run was started automatically and is not finished;
//   - the execution is still pending and has no study-service job;
//   - its candidate has been retrieved, so nothing else will dispatch it
//     (a candidate that is still growing or queued for retrieval is dispatched
//     by the retrieval worker when its time comes);
//   - both have been in that state longer than the pending threshold, so a
//     dispatch that is about to start is not raced;
//   - no dispatch for it is under way in this process;
//   - its ingestion job is running and names the same model.
//
// The dispatch itself is the ordinary one: it re-reads the execution and does
// nothing unless it is still pending, it uses the candidate ID as request ID,
// and study-service answers an automatic request for a study and model it is
// already processing with that existing job instead of creating a second one.
func (service *InferenceCommandService) redispatchOrphanedExecution(
	run entity.InferenceIngestionProcessingRun,
	candidate entity.InferenceIngestionCandidate,
	execution entity.InferenceIngestionProcessingJob,
	now time.Time,
	config processingReconciliationConfig,
) (bool, string) {
	switch {
	case run.RunTrigger != entity.InferenceIngestionProcessingRunTriggerAuto:
		return false, "run was not started automatically"
	case run.Phase == entity.InferenceIngestionProcessingRunPhaseTerminal:
		return false, "run is finished"
	case execution.Status != entity.InferenceIngestionProcessingJobStatusPending || execution.HasStudyServiceJob():
		return false, "execution was already dispatched"
	case candidate.Status != entity.InferenceIngestionCandidateStatusRetrieved:
		return false, "candidate is " + string(candidate.Status) + ", not retrieved"
	case strings.TrimSpace(candidate.ID) == "" || strings.TrimSpace(candidate.ID) != strings.TrimSpace(execution.CandidateID):
		return false, "candidate does not match the execution"
	}
	threshold := config.PendingStaleAfter
	pendingSince := processingExecutionStateChangedAt(execution, run)
	if pendingSince.IsZero() || now.Before(pendingSince.Add(threshold)) {
		return false, "execution has not been pending long enough"
	}
	// retrieved_at is set when the candidate becomes RETRIEVED; updated_at
	// also moves when the study is merely seen again by the poller.
	retrievedAt := candidate.UpdatedAt
	if candidate.RetrievedAt != nil {
		retrievedAt = *candidate.RetrievedAt
	}
	if retrievedAt.IsZero() || now.Before(retrievedAt.Add(threshold)) {
		return false, "candidate was retrieved a moment ago"
	}
	if err := service.requireManagedIngestionReady(); err != nil {
		return false, "model manager is not ready"
	}
	job, err := service.InferenceQueryRepositoryInterface.SelectInferenceIngestionJobByID(candidate.IngestionJobID)
	if err != nil {
		return false, "ingestion job cannot be read: " + err.Error()
	}
	switch {
	case job.Status != entity.InferenceIngestionJobStatusRunning:
		return false, "ingestion job is " + string(job.Status)
	case strings.TrimSpace(job.TenantID) != strings.TrimSpace(run.TenantID):
		return false, "ingestion job belongs to another workspace"
	case strings.TrimSpace(job.ModelName) != strings.TrimSpace(execution.ModelName):
		return false, "ingestion job names another model"
	}
	// The key is the one the ordinary dispatch registers itself under.
	key := studyServiceDispatchKey(candidate.TenantID, run.ID, candidate.ID, job.ModelName)
	if !liveStudyServiceDispatches.beginIfIdle(key) {
		return false, "a dispatch is under way"
	}
	log.Printf("[Ingestion reconciliation worker] re-dispatching orphaned execution processing_run_id=%s execution_id=%s candidate_id=%s ingestion_job_id=%s model_name=%s pending_since=%s",
		run.ID, execution.ID, candidate.ID, job.ID, execution.ModelName, formatEasternTime(pendingSince))
	service.launchStudyServiceDispatch(key, job, candidate, run.ID, strings.TrimSpace(candidate.ID), "", "")
	return true, ""
}

// launchStudyServiceDispatch runs one dispatch in the background. The caller
// has registered key in liveStudyServiceDispatches; it is released when the
// dispatch has ended, whatever its outcome.
func (service *InferenceCommandService) launchStudyServiceDispatch(
	key string,
	job entity.InferenceIngestionJob,
	candidate entity.InferenceIngestionCandidate,
	processingRunID string,
	requestID string,
	processingExecutionID string,
	dispatchIntent types.DispatchStudyIntent,
	seriesInstanceUIDs ...string,
) {
	go func() {
		defer liveStudyServiceDispatches.end(key)
		if limiter := service.StudyServiceDispatchSemaphore; limiter != nil {
			limiter <- struct{}{}
			defer func() {
				<-limiter
			}()
		}

		if err := service.dispatchRetrievedCandidateToStudyServiceWithExecutionID(
			context.Background(), job, candidate, processingRunID, requestID, processingExecutionID, dispatchIntent,
			seriesInstanceUIDs...,
		); err != nil {
			log.Printf("[Ingestion dispatch] final dispatch failure candidate_id=%s ingestion_job_id=%s request_id=%s err=%v",
				candidate.ID,
				job.ID,
				requestID,
				err,
			)
		}
	}()
}
