package service

import (
	"strings"
	"time"

	"api-pacs/module/inference/domain/entity"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	"api-pacs/module/inference/infrastructure/service/types"
)

// processingRunIsStaleWaiting reports an active run that computes nothing and
// that the reconciler flagged for executions that were never started.
func processingRunIsStaleWaiting(
	run entity.InferenceIngestionProcessingRun,
	counts entity.InferenceIngestionProcessingRunExecutionCounts,
) bool {
	if run.Phase == entity.InferenceIngestionProcessingRunPhaseTerminal || counts.Running > 0 || counts.Active == 0 {
		return false
	}
	return entity.ProcessingRunHasAttentionReason(run.AttentionReasons, entity.InferenceIngestionProcessingRunAttentionPendingStale) ||
		entity.ProcessingRunHasAttentionReason(run.AttentionReasons, entity.InferenceIngestionProcessingRunAttentionExpectedJobMissing)
}

// describeStudyGrowingWait sets WaitingReason "study_growing" and StableAfter
// when an active run computes nothing and at least one execution that was
// never dispatched belongs to an ingestion candidate still receiving images.
// It takes precedence over "stale": the reconciler also flags executions that
// are only waiting for the exam to finish. Lookups are best effort; the detail
// is returned unchanged when they fail.
func (service *InferenceQueryService) describeStudyGrowingWait(
	run entity.InferenceIngestionProcessingRun,
	executions []entity.InferenceIngestionProcessingJob,
	detail *types.ProcessingRunDetail,
) {
	if run.Phase == entity.InferenceIngestionProcessingRunPhaseTerminal || detail.Running > 0 ||
		service.InferenceQueryRepositoryInterface == nil {
		return
	}
	waitingCandidateIDs := map[string]struct{}{}
	for index := range executions {
		execution := &executions[index]
		if execution.Status == entity.InferenceIngestionProcessingJobStatusPending && !execution.HasStudyServiceJob() {
			waitingCandidateIDs[strings.TrimSpace(execution.CandidateID)] = struct{}{}
		}
	}
	if len(waitingCandidateIDs) == 0 {
		return
	}

	studyInstanceUID := run.StudyInstanceUID
	candidates, err := service.InferenceQueryRepositoryInterface.ListInferenceIngestionCandidates(repositoryTypes.ListInferenceIngestionCandidates{
		TenantID: run.TenantID, StudyInstanceUID: &studyInstanceUID,
	})
	if err != nil {
		return
	}

	growing := false
	var stableAfter *time.Time
	for _, candidate := range candidates {
		if _, waiting := waitingCandidateIDs[strings.TrimSpace(candidate.ID)]; !waiting {
			continue
		}
		if candidate.Status != entity.InferenceIngestionCandidateStatusGrowing &&
			candidate.Status != entity.InferenceIngestionCandidateStatusDiscovered {
			continue
		}
		growing = true
		job, jobErr := service.InferenceQueryRepositoryInterface.SelectInferenceIngestionJobByID(candidate.IngestionJobID)
		if jobErr != nil || candidate.LastChangedAt.IsZero() {
			continue
		}
		candidateStableAfter := candidate.LastChangedAt.Add(time.Duration(resolvedStabilityMinutes(job.StabilityMinutes)) * time.Minute)
		if stableAfter == nil || candidateStableAfter.After(*stableAfter) {
			stableAfter = &candidateStableAfter
		}
	}
	if !growing {
		return
	}
	detail.WaitingReason = types.ProcessingRunWaitingReasonStudyGrowing
	detail.StableAfter = stableAfter
}
