package service

import (
	"context"
	"log"
	"strings"

	"api-pacs/module/inference/domain/entity"
	"api-pacs/module/inference/infrastructure/service/types"
)

// resolveWorklistNotificationAudience works out who the published event
// should notify. Manual and viewer runs need nothing beyond the run; automatic
// runs cost one query for the ingestion jobs behind the run. A failed lookup
// leaves the audience unresolved, which notifies workspace admins only.
func (service *InferenceCommandService) resolveWorklistNotificationAudience(
	ctx context.Context,
	run entity.InferenceIngestionProcessingRun,
) types.WorklistNotificationAudience {
	audience := types.WorklistNotificationAudience{
		RequestedByUserID: trimmedPointerValue(run.RequestedByUserID),
		CancelledByUserID: trimmedPointerValue(run.CancelledByUserID),
	}
	if !types.IsAutomaticWorklistTrigger(run.RunTrigger) {
		audience.Resolved = true
		return audience
	}
	if service.WorklistNotificationAudienceRepository == nil {
		return audience
	}
	jobs, err := service.WorklistNotificationAudienceRepository.SelectProcessingRunNotificationJobs(ctx, run.TenantID, run.ID)
	if err != nil {
		log.Printf("[Processing run transition] notification audience lookup failed run_id=%s err=%v", run.ID, err)
		return audience
	}
	audience.Resolved = true
	seen := map[string]bool{}
	for _, job := range jobs {
		if !job.NotificationsEnabled {
			continue
		}
		audience.AnyJobNotificationsEnabled = true
		ownerID := trimmedPointerValue(job.CreatedByUserID)
		if ownerID == "" || seen[ownerID] {
			continue
		}
		seen[ownerID] = true
		audience.NotifyJobOwnerUserIDs = append(audience.NotifyJobOwnerUserIDs, ownerID)
	}
	return audience
}

// optionalTrimmedString returns nil for a blank value.
func optionalTrimmedString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
