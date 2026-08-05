package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

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

	require.NoError(t, broker.PublishWorklistNotification(context.Background(), serviceTypes.WorklistNotification{
		TenantID: "tenant-a", Version: 1,
	}))
	require.NoError(t, broker.PublishWorklistNotification(context.Background(), serviceTypes.WorklistNotification{
		TenantID: "tenant-a", Version: 2,
	}))
}
