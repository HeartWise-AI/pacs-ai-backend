package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
)

const (
	// DefaultAutoRunExpireAfter is how long after its creation an automatic run
	// whose executions never ran is closed (30 days).
	DefaultAutoRunExpireAfter = 720 * time.Hour
	// AutoRunExpireAfterEnv sets that age in hours. The study images are
	// removed from the local image store about 24 hours after they arrive, so
	// an execution that has not started by then can never run: 48 is the
	// meaningful value with that retention. The default keeps 30 days.
	AutoRunExpireAfterEnv = "INFERENCE_AUTO_RUN_EXPIRE_AFTER_HOURS"
	// autoRunExpiryBatchLimit bounds the runs closed in one reconciliation cycle.
	autoRunExpiryBatchLimit = 200
)

// ConfiguredAutoRunExpireAfter reads INFERENCE_AUTO_RUN_EXPIRE_AFTER_HOURS (a
// positive whole number of hours, default 720).
func ConfiguredAutoRunExpireAfter() time.Duration {
	value := strings.TrimSpace(os.Getenv(AutoRunExpireAfterEnv))
	if value == "" {
		return DefaultAutoRunExpireAfter
	}
	hours, err := strconv.ParseUint(value, 10, 32)
	if err != nil || hours == 0 {
		log.Printf("[Automatic run expiry] invalid %s=%q, using hours=%d", AutoRunExpireAfterEnv, value, int(DefaultAutoRunExpireAfter.Hours()))
		return DefaultAutoRunExpireAfter
	}
	return time.Duration(hours) * time.Hour
}

// autoRunExpiryMessage is stored as the skip reason message of each closed
// execution, e.g. "Expired: never ran within 30 days (automatic)".
func autoRunExpiryMessage(expireAfter time.Duration) string {
	hours := int(expireAfter.Hours())
	if hours >= 48 && hours%24 == 0 {
		return fmt.Sprintf("Expired: never ran within %d days (automatic)", hours/24)
	}
	return fmt.Sprintf("Expired: never ran within %d hours (automatic)", hours)
}

// AutoRunExpiryCycle summarises one expiry pass.
type AutoRunExpiryCycle struct {
	Selected   int
	Expired    int
	Executions int
	Unchanged  int
	Failed     int
}

// expireNeverRanAutomaticRuns closes the automatic runs (AUTO, LEGACY_IMPORT)
// older than AutoRunExpireAfter whose executions are all still pending or
// queued without a study-service job: each execution is marked skipped with
// the EXPIRED_NEVER_RAN reason and the run becomes terminal through the
// ordinary aggregation. Manual and viewer runs, and runs with any execution
// that was dispatched, started or finished, are never touched; no row is
// deleted. At most autoRunExpiryBatchLimit runs are closed per cycle; the
// rest follow in the next cycles. A zero AutoRunExpireAfter disables it.
func (service *InferenceCommandService) expireNeverRanAutomaticRuns(ctx context.Context, now time.Time) AutoRunExpiryCycle {
	cycle := AutoRunExpiryCycle{}
	if service.AutoRunExpireAfter <= 0 {
		return cycle
	}
	createdBefore := now.Add(-service.AutoRunExpireAfter)
	runs, err := service.InferenceProcessingRunRepositoryInterface.ListNeverRanAutomaticProcessingRuns(ctx,
		repositoryTypes.ListNeverRanAutomaticProcessingRuns{CreatedBefore: createdBefore, Limit: autoRunExpiryBatchLimit})
	if err != nil {
		cycle.Failed++
		log.Printf("[Automatic run expiry] cannot list runs created_before=%s err=%v", formatEasternTime(createdBefore), err)
		return cycle
	}
	cycle.Selected = len(runs)
	message := autoRunExpiryMessage(service.AutoRunExpireAfter)
	for _, run := range runs {
		if ctx.Err() != nil {
			break
		}
		result, expireErr := service.InferenceProcessingRunRepositoryInterface.ExpireNeverRanProcessingRun(ctx,
			repositoryTypes.ExpireNeverRanProcessingRun{
				ID: run.ID, TenantID: run.TenantID, CreatedBefore: createdBefore, Message: message, ExpiredAt: now.UTC(),
			})
		switch {
		case expireErr != nil:
			cycle.Failed++
			log.Printf("[Automatic run expiry] cannot expire processing_run_id=%s tenant_id=%s err=%v",
				run.ID, run.TenantID, expireErr)
		case !result.Expired:
			cycle.Unchanged++
			log.Printf("[Automatic run expiry] left processing_run_id=%s tenant_id=%s reason=%q",
				run.ID, run.TenantID, result.NotExpiredReason)
		default:
			cycle.Expired++
			cycle.Executions += result.Executions
			outcome := ""
			if result.Run.Outcome != nil {
				outcome = string(*result.Run.Outcome)
			}
			log.Printf("[Automatic run expiry] expired processing_run_id=%s tenant_id=%s study_instance_uid=%s trigger=%s run_created_at=%s executions=%d phase=%s outcome=%s",
				run.ID, run.TenantID, run.StudyInstanceUID, run.RunTrigger, formatEasternTime(run.CreatedAt),
				result.Executions, result.Run.Phase, outcome)
		}
	}
	log.Printf("[Automatic run expiry] cycle selected=%d expired=%d executions=%d unchanged=%d failed=%d batch_limit=%d expire_after_hours=%d created_before=%s",
		cycle.Selected, cycle.Expired, cycle.Executions, cycle.Unchanged, cycle.Failed, autoRunExpiryBatchLimit,
		int(service.AutoRunExpireAfter.Hours()), formatEasternTime(createdBefore))
	return cycle
}
