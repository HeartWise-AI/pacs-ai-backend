package service

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/domain/entity"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
)

// processingRunCancelledByUserMessage is stored on each execution a user
// cancellation ended, next to the run's cancelled_by_user_id/cancelled_at.
const processingRunCancelledByUserMessage = "Cancelled by user"

// errAutomaticProcessingSuppressedByCancellation stops automatic ingestion
// from planning a new run for a study whose latest study-wide run a user
// cancelled. A manual reprocess or a viewer run is not affected, and a later
// manual reprocess lifts the suppression because it becomes the latest run.
var errAutomaticProcessingSuppressedByCancellation = errors.New(
	"automatic processing suppressed: the latest study run was cancelled by a user",
)

// CancelProcessingRun cancels every execution of the run that has not started
// computing and lets the run reach its terminal phase through the ordinary
// execution transition. Executions already handed to study-service are left to
// finish and record their result. Cancelling a terminal or already-cancelled
// run returns its current state.
func (service *InferenceCommandService) CancelProcessingRun(ctx context.Context, data types.CancelProcessingRun) (types.ProcessingRunDetail, error) {
	tenantID := strings.TrimSpace(data.TenantID)
	runID := strings.TrimSpace(data.RunID)
	userID := strings.TrimSpace(data.UserID)
	if tenantID == "" || runID == "" || userID == "" {
		return types.ProcessingRunDetail{}, errors.New(apiError.InvalidPayload)
	}

	run, executions, err := service.loadProcessingRunWithExecutions(ctx, tenantID, runID)
	if err != nil {
		return types.ProcessingRunDetail{}, err
	}
	targets := entity.CancellableProcessingRunExecutions(run, executions)
	if run.Phase == entity.InferenceIngestionProcessingRunPhaseTerminal || (len(targets) == 0 && run.IsUserCancelled()) {
		return buildProcessingRunDetail(run, executions), nil
	}
	if len(targets) == 0 {
		return types.ProcessingRunDetail{}, errors.New(apiError.ProcessingRunNotCancellable)
	}

	now := time.Now().UTC()
	if _, err := service.InferenceProcessingRunRepositoryInterface.MarkProcessingRunCancelled(ctx, repositoryTypes.MarkInferenceIngestionProcessingRunCancelled{
		ID: runID, TenantID: tenantID, CancelledByUserID: userID, CancelledAt: now,
	}); err != nil {
		if err.Error() != apiError.MissingRecord {
			return types.ProcessingRunDetail{}, err
		}
		// The run ended between the read and the mark.
		run, executions, err = service.loadProcessingRunWithExecutions(ctx, tenantID, runID)
		if err != nil {
			return types.ProcessingRunDetail{}, err
		}
		return buildProcessingRunDetail(run, executions), nil
	}

	service.clearAttentionResolvedByCancellation(ctx, run, executions, targets)

	cancelled := 0
	message := processingRunCancelledByUserMessage
	for _, target := range targets {
		// A dispatch that is handing this execution to study-service right now
		// owns it: study-service may already be computing, and the dispatch
		// records the accepted job. Leave it to finish instead of cancelling.
		release, claimed := studyServiceDispatchHandoffs.tryAcquire(
			studyServiceDispatchKey(tenantID, runID, target.CandidateID, target.ModelName),
		)
		if !claimed {
			log.Printf("[Processing run cancel] execution left to finish: dispatch under way run_id=%s execution_id=%s model_name=%s",
				runID, target.ID, target.ModelName)
			continue
		}
		transition, transitionErr := service.InferenceProcessingRunRepositoryInterface.ApplyProcessingRunExecutionTransition(
			ctx,
			repositoryTypes.ApplyInferenceIngestionProcessingTransition{
				TenantID:                       tenantID,
				ProcessingRunID:                runID,
				ExecutionID:                    target.ID,
				CandidateID:                    target.CandidateID,
				ModelName:                      target.ModelName,
				Status:                         entity.InferenceIngestionProcessingJobStatusCancelled,
				ErrorMessage:                   &message,
				CompletedAt:                    &now,
				OnlyIfCancellableBeforeCompute: true,
				StudyServiceJobMissing:         entity.ProcessingRunExecutionStudyServiceJobMissing(run.AttentionReasons, target),
			},
		)
		release()
		if transitionErr != nil {
			// Executions cancelled so far stay cancelled; repeating the request resumes.
			return types.ProcessingRunDetail{}, transitionErr
		}
		if transition.Changed {
			cancelled++
			service.publishCommittedWorklistNotification(ctx, transition)
		}
	}

	run, executions, err = service.loadProcessingRunWithExecutions(ctx, tenantID, runID)
	if err != nil {
		return types.ProcessingRunDetail{}, err
	}
	detail := buildProcessingRunDetail(run, executions)
	log.Printf(
		"[Processing run cancel] run_id=%s tenant_id=%s user_id=%s trigger=%s cancelled_executions=%d left_to_finish=%d phase=%s",
		runID, tenantID, userID, run.RunTrigger, cancelled, detail.Active, run.Phase,
	)
	return detail, nil
}

func (service *InferenceCommandService) loadProcessingRunWithExecutions(
	ctx context.Context,
	tenantID, runID string,
) (entity.InferenceIngestionProcessingRun, []entity.InferenceIngestionProcessingJob, error) {
	run, err := service.InferenceProcessingRunRepositoryInterface.SelectProcessingRun(ctx, tenantID, runID)
	if err != nil {
		return entity.InferenceIngestionProcessingRun{}, nil, err
	}
	executions, err := service.InferenceProcessingRunRepositoryInterface.ListProcessingRunExecutions(ctx, tenantID, runID)
	if err != nil {
		return entity.InferenceIngestionProcessingRun{}, nil, err
	}
	return run, executions, nil
}

// clearAttentionResolvedByCancellation removes, before the executions are
// cancelled, the stale-execution reasons that only concerned them, so the
// transitions publish a run whose attention flags are already consistent. It
// is best effort: the reconciler re-adds a reason that still applies.
func (service *InferenceCommandService) clearAttentionResolvedByCancellation(
	ctx context.Context,
	run entity.InferenceIngestionProcessingRun,
	executions []entity.InferenceIngestionProcessingJob,
	targets []entity.InferenceIngestionProcessingJob,
) {
	cancelledIDs := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		cancelledIDs[target.ID] = struct{}{}
	}
	pendingLeft, undispatchedLeft, queuedLeft, activeLeft := false, false, false, false
	for index := range executions {
		execution := &executions[index]
		if _, cancelled := cancelledIDs[execution.ID]; cancelled || execution.Status.IsTerminal() {
			continue
		}
		activeLeft = true
		pendingLeft = pendingLeft || execution.Status == entity.InferenceIngestionProcessingJobStatusPending
		queuedLeft = queuedLeft || execution.Status == entity.InferenceIngestionProcessingJobStatusQueued
		undispatchedLeft = undispatchedLeft || !execution.HasStudyServiceJob()
	}

	resolved := make([]string, 0, 4)
	addResolved := func(stillApplies bool, code string) {
		if !stillApplies && entity.ProcessingRunHasAttentionReason(run.AttentionReasons, code) {
			resolved = append(resolved, code)
		}
	}
	addResolved(pendingLeft, entity.InferenceIngestionProcessingRunAttentionPendingStale)
	addResolved(undispatchedLeft, entity.InferenceIngestionProcessingRunAttentionExpectedJobMissing)
	addResolved(queuedLeft, entity.InferenceIngestionProcessingRunAttentionQueueStale)
	addResolved(activeLeft, entity.InferenceIngestionProcessingRunAttentionStudyServiceJobMissing)
	if len(resolved) == 0 {
		return
	}

	if _, err := service.RecalculateStudyProcessingRun(ctx, types.RecalculateStudyProcessingRun{
		TenantID: run.TenantID, ProcessingRunID: run.ID, AttentionReasonCodesToRemove: resolved,
	}); err != nil {
		log.Printf("[Processing run cancel] cannot clear resolved attention run_id=%s tenant_id=%s err=%v",
			run.ID, run.TenantID, err)
	}
}
