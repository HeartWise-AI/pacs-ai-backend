package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"api-pacs/module/inference/domain/entity"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

func TestWorklistNotificationJSONKeepsTenantInternal(t *testing.T) {
	notification := serviceTypes.WorklistNotification{
		Type:             serviceTypes.WorklistNotificationTypeStudyStatusUpdated,
		TenantID:         "tenant-a",
		StudyInstanceUID: "study-1",
		RunID:            "run-1",
		AttentionReasons: entity.InferenceIngestionProcessingRunAttentionReasons{},
		Version:          2,
	}

	payload, err := json.Marshal(notification)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"type":"study_status.updated",
		"studyInstanceUID":"study-1",
		"runId":"run-1",
		"runNumber":0,
		"trigger":"",
		"phase":"",
		"outcome":null,
		"attentionRequired":false,
		"attentionReasons":[],
		"expectedModels":0,
		"pendingModels":0,
		"queuedModels":0,
		"runningModels":0,
		"completedModels":0,
		"failedModels":0,
		"skippedModels":0,
		"cancelledModels":0,
		"activeModels":0,
		"version":2,
		"startedAt":null,
		"completedAt":null,
		"updatedAt":"0001-01-01T00:00:00Z"
	}`, string(payload))
}

func TestWorklistNotificationBrokerIsTenantScoped(t *testing.T) {
	broker := NewWorklistNotificationBroker()
	tenantA, cancelA := broker.SubscribeWorklistNotifications("tenant-a", 1)
	t.Cleanup(cancelA)
	tenantB, cancelB := broker.SubscribeWorklistNotifications("tenant-b", 1)
	t.Cleanup(cancelB)
	notification := serviceTypes.WorklistNotification{
		Type: "study_status.updated", TenantID: "tenant-a", RunID: "run-1", Version: 2,
	}

	require.NoError(t, broker.PublishWorklistNotification(context.Background(), notification))

	require.Equal(t, notification, <-tenantA)
	select {
	case unexpected := <-tenantB:
		t.Fatalf("tenant-b received tenant-a notification: %+v", unexpected)
	default:
	}
}

func TestWorklistNotificationBrokerDoesNotBlockOnSlowSubscriber(t *testing.T) {
	broker := NewWorklistNotificationBroker()
	_, cancel := broker.SubscribeWorklistNotifications("tenant-a", 1)
	t.Cleanup(cancel)
	droppedBefore := expvarMapIntValue(worklistNotificationsTotal, "local_subscriber_dropped")

	require.NoError(t, broker.PublishWorklistNotification(context.Background(), serviceTypes.WorklistNotification{
		TenantID: "tenant-a", Version: 1,
	}))
	require.NoError(t, broker.PublishWorklistNotification(context.Background(), serviceTypes.WorklistNotification{
		TenantID: "tenant-a", Version: 2,
	}))
	require.Equal(t, droppedBefore+1, expvarMapIntValue(worklistNotificationsTotal, "local_subscriber_dropped"))
}
