package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"api-pacs/module/inference/domain/entity"
)

func TestWorklistNotificationShouldNotifyAppliesAudiencePolicy(t *testing.T) {
	const owner, otherOwner, requester, bystander = "user-owner", "user-owner-2", "user-requester", "user-bystander"
	enabledOwners := WorklistNotificationAudience{
		Resolved: true, AnyJobNotificationsEnabled: true, NotifyJobOwnerUserIDs: []string{owner, otherOwner},
	}
	allDisabled := WorklistNotificationAudience{Resolved: true}
	// A job with notifications on but no recorded creator (legacy job).
	legacyEnabled := WorklistNotificationAudience{Resolved: true, AnyJobNotificationsEnabled: true}
	unresolved := WorklistNotificationAudience{}
	requested := WorklistNotificationAudience{Resolved: true, RequestedByUserID: requester}

	user := func(id string) WorklistNotificationViewer { return WorklistNotificationViewer{UserID: id} }
	admin := func(id string) WorklistNotificationViewer {
		return WorklistNotificationViewer{UserID: id, IsAdmin: true}
	}
	muted := func(viewer WorklistNotificationViewer) WorklistNotificationViewer {
		viewer.AutomaticRunsMuted = true
		return viewer
	}

	automaticTriggers := []entity.InferenceIngestionProcessingRunTrigger{
		entity.InferenceIngestionProcessingRunTriggerAuto,
		entity.InferenceIngestionProcessingRunTriggerLegacyImport,
	}
	userTriggers := []entity.InferenceIngestionProcessingRunTrigger{
		entity.InferenceIngestionProcessingRunTriggerManualReprocess,
		entity.InferenceIngestionProcessingRunTriggerViewerRequest,
	}

	type testCase struct {
		name     string
		audience WorklistNotificationAudience
		viewer   WorklistNotificationViewer
		want     bool
	}
	automaticCases := []testCase{
		{"job owner of an enabled job", enabledOwners, user(owner), true},
		{"second job owner of an enabled job", enabledOwners, user(otherOwner), true},
		{"plain user who owns no job", enabledOwners, user(bystander), false},
		{"admin", enabledOwners, admin(bystander), true},
		{"admin who muted", enabledOwners, muted(admin(bystander)), false},
		{"owner who muted", enabledOwners, muted(user(owner)), false},
		{"admin when every job is off", allDisabled, admin(bystander), false},
		{"owner when every job is off", allDisabled, user(owner), false},
		{"admin for a legacy job without creator", legacyEnabled, admin(bystander), true},
		{"user for a legacy job without creator", legacyEnabled, user(owner), false},
		{"admin when audience is unresolved", unresolved, admin(bystander), true},
		{"owner when audience is unresolved", unresolved, user(owner), false},
		{"muted admin when audience is unresolved", unresolved, muted(admin(bystander)), false},
		{"requester field is ignored for automatic runs", requested, user(requester), false},
		{"anonymous connection", enabledOwners, user(""), false},
	}
	for _, trigger := range automaticTriggers {
		for _, tc := range automaticCases {
			notification := WorklistNotification{Trigger: trigger, Audience: tc.audience}
			require.Equal(t, tc.want, notification.ShouldNotify(tc.viewer), "%s %s", trigger, tc.name)
		}
	}

	userCases := []testCase{
		{"requester", requested, user(requester), true},
		{"requester who muted automatic runs", requested, muted(user(requester)), true},
		{"admin who did not request", requested, admin(bystander), false},
		{"job owner who did not request", WorklistNotificationAudience{
			Resolved: true, RequestedByUserID: requester, AnyJobNotificationsEnabled: true, NotifyJobOwnerUserIDs: []string{owner},
		}, user(owner), false},
		{"run without requester", WorklistNotificationAudience{Resolved: true}, admin(bystander), false},
		{"anonymous connection", WorklistNotificationAudience{Resolved: true}, user(""), false},
	}
	for _, trigger := range userTriggers {
		for _, tc := range userCases {
			notification := WorklistNotification{Trigger: trigger, Audience: tc.audience}
			require.Equal(t, tc.want, notification.ShouldNotify(tc.viewer), "%s %s", trigger, tc.name)
		}
	}
}

func TestWorklistNotificationCancelledByViewer(t *testing.T) {
	notification := WorklistNotification{Audience: WorklistNotificationAudience{CancelledByUserID: "user-a"}}
	require.True(t, notification.CancelledByViewer(WorklistNotificationViewer{UserID: "user-a"}))
	require.False(t, notification.CancelledByViewer(WorklistNotificationViewer{UserID: "user-b"}))
	require.False(t, WorklistNotification{}.CancelledByViewer(WorklistNotificationViewer{}))
}

func TestWorklistNotificationJSONNeverCarriesAudience(t *testing.T) {
	payload, err := json.Marshal(WorklistNotification{
		TenantID: "tenant-secret",
		Trigger:  entity.InferenceIngestionProcessingRunTriggerAuto,
		Audience: WorklistNotificationAudience{
			Resolved: true, RequestedByUserID: "user-requester", CancelledByUserID: "user-canceller",
			AnyJobNotificationsEnabled: true, NotifyJobOwnerUserIDs: []string{"user-owner"},
		},
	})
	require.NoError(t, err)
	for _, secret := range []string{"tenant-secret", "user-requester", "user-canceller", "user-owner", "audience", "notifyJobOwner"} {
		require.NotContains(t, string(payload), secret)
	}
}
