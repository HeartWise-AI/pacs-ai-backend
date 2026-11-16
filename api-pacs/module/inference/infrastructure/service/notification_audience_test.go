package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"api-pacs/module/inference/domain/entity"
	repositoryTypes "api-pacs/module/inference/infrastructure/repository/types"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type audienceRepositoryStub struct {
	jobs  []repositoryTypes.ProcessingRunNotificationJob
	err   error
	calls []string
}

func (stub *audienceRepositoryStub) SelectProcessingRunNotificationJobs(_ context.Context, tenantID, processingRunID string) ([]repositoryTypes.ProcessingRunNotificationJob, error) {
	stub.calls = append(stub.calls, tenantID+"/"+processingRunID)
	return stub.jobs, stub.err
}


func TestResolveWorklistNotificationAudienceForAutomaticRunSpanningSeveralJobs(t *testing.T) {
	repository := &audienceRepositoryStub{jobs: []repositoryTypes.ProcessingRunNotificationJob{
		{IngestionJobID: "job-1", CreatedByUserID: stringPointer("user-a"), NotificationsEnabled: true},
		{IngestionJobID: "job-2", CreatedByUserID: stringPointer("user-b"), NotificationsEnabled: false},
		{IngestionJobID: "job-3", CreatedByUserID: nil, NotificationsEnabled: true},
		{IngestionJobID: "job-4", CreatedByUserID: stringPointer(" user-a "), NotificationsEnabled: true},
		{IngestionJobID: "job-5", CreatedByUserID: stringPointer("user-c"), NotificationsEnabled: true},
	}}
	service := &InferenceCommandService{WorklistNotificationAudienceRepository: repository}

	audience := service.resolveWorklistNotificationAudience(context.Background(), entity.InferenceIngestionProcessingRun{
		ID: "run-1", TenantID: "tenant-a", RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto,
	})

	require.Equal(t, []string{"tenant-a/run-1"}, repository.calls)
	require.True(t, audience.Resolved)
	require.True(t, audience.AnyJobNotificationsEnabled)
	require.Equal(t, []string{"user-a", "user-c"}, audience.NotifyJobOwnerUserIDs)
	require.Empty(t, audience.RequestedByUserID)
}

func TestResolveWorklistNotificationAudienceWhenEveryJobIsSilenced(t *testing.T) {
	repository := &audienceRepositoryStub{jobs: []repositoryTypes.ProcessingRunNotificationJob{
		{IngestionJobID: "job-1", CreatedByUserID: stringPointer("user-a"), NotificationsEnabled: false},
		{IngestionJobID: "job-2", NotificationsEnabled: false},
	}}
	service := &InferenceCommandService{WorklistNotificationAudienceRepository: repository}

	audience := service.resolveWorklistNotificationAudience(context.Background(), entity.InferenceIngestionProcessingRun{
		ID: "run-1", TenantID: "tenant-a", RunTrigger: entity.InferenceIngestionProcessingRunTriggerLegacyImport,
	})

	require.True(t, audience.Resolved)
	require.False(t, audience.AnyJobNotificationsEnabled)
	require.Empty(t, audience.NotifyJobOwnerUserIDs)
	notification := serviceTypes.WorklistNotification{Trigger: entity.InferenceIngestionProcessingRunTriggerLegacyImport, Audience: audience}
	require.False(t, notification.ShouldNotify(serviceTypes.WorklistNotificationViewer{UserID: "admin", IsAdmin: true}))
}

func TestResolveWorklistNotificationAudienceFallsBackToAdminsWhenLookupFails(t *testing.T) {
	repository := &audienceRepositoryStub{err: errors.New("DATABASE_ERROR")}
	service := &InferenceCommandService{WorklistNotificationAudienceRepository: repository}

	audience := service.resolveWorklistNotificationAudience(context.Background(), entity.InferenceIngestionProcessingRun{
		ID: "run-1", TenantID: "tenant-a", RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto,
	})

	require.False(t, audience.Resolved)
	notification := serviceTypes.WorklistNotification{Trigger: entity.InferenceIngestionProcessingRunTriggerAuto, Audience: audience}
	require.True(t, notification.ShouldNotify(serviceTypes.WorklistNotificationViewer{UserID: "admin", IsAdmin: true}))
	require.False(t, notification.ShouldNotify(serviceTypes.WorklistNotificationViewer{UserID: "user-a"}))

	unwired := (&InferenceCommandService{}).resolveWorklistNotificationAudience(context.Background(), entity.InferenceIngestionProcessingRun{
		RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto,
	})
	require.False(t, unwired.Resolved)
}

func TestResolveWorklistNotificationAudienceForUserRunsSkipsTheQuery(t *testing.T) {
	repository := &audienceRepositoryStub{}
	service := &InferenceCommandService{WorklistNotificationAudienceRepository: repository}
	for _, trigger := range []entity.InferenceIngestionProcessingRunTrigger{
		entity.InferenceIngestionProcessingRunTriggerManualReprocess,
		entity.InferenceIngestionProcessingRunTriggerViewerRequest,
	} {
		audience := service.resolveWorklistNotificationAudience(context.Background(), entity.InferenceIngestionProcessingRun{
			ID: "run-1", TenantID: "tenant-a", RunTrigger: trigger,
			RequestedByUserID: stringPointer("user-r"), CancelledByUserID: stringPointer("user-c"),
		})
		require.True(t, audience.Resolved)
		require.Equal(t, "user-r", audience.RequestedByUserID)
		require.Equal(t, "user-c", audience.CancelledByUserID)
	}
	require.Empty(t, repository.calls)
}

func TestPublishCommittedWorklistNotificationAttachesAudience(t *testing.T) {
	publisher := &recordingWorklistNotificationPublisher{}
	service := &InferenceCommandService{
		WorklistNotificationPublisherInterface: publisher,
		WorklistNotificationAudienceRepository: &audienceRepositoryStub{jobs: []repositoryTypes.ProcessingRunNotificationJob{
			{IngestionJobID: "job-1", CreatedByUserID: stringPointer("user-a"), NotificationsEnabled: true},
		}},
	}

	service.publishCommittedWorklistNotification(context.Background(), repositoryTypes.ApplyInferenceIngestionProcessingTransitionResult{
		Run: entity.InferenceIngestionProcessingRun{
			ID: "run-1", TenantID: "tenant-a", StudyInstanceUID: "1.2.3",
			RunTrigger: entity.InferenceIngestionProcessingRunTriggerAuto,
		},
	})

	require.Len(t, publisher.notifications, 1)
	require.Equal(t, serviceTypes.WorklistNotificationAudience{
		Resolved: true, AnyJobNotificationsEnabled: true, NotifyJobOwnerUserIDs: []string{"user-a"},
	}, publisher.notifications[0].Audience)
}
