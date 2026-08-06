package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

type sharedWorklistTransport struct {
	mutex       sync.Mutex
	subscribers map[uint64]chan []byte
	nextID      uint64
}

func newSharedWorklistTransport() *sharedWorklistTransport {
	return &sharedWorklistTransport{subscribers: make(map[uint64]chan []byte)}
}

func (transport *sharedWorklistTransport) Publish(ctx context.Context, payload []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	transport.mutex.Lock()
	defer transport.mutex.Unlock()
	for _, subscriber := range transport.subscribers {
		copyOfPayload := append([]byte(nil), payload...)
		subscriber <- copyOfPayload
	}
	return nil
}

func (transport *sharedWorklistTransport) Subscribe(ctx context.Context) (<-chan []byte, error) {
	transport.mutex.Lock()
	transport.nextID++
	id := transport.nextID
	messages := make(chan []byte, 4)
	transport.subscribers[id] = messages
	transport.mutex.Unlock()
	go func() {
		<-ctx.Done()
		transport.mutex.Lock()
		delete(transport.subscribers, id)
		close(messages)
		transport.mutex.Unlock()
	}()
	return messages, nil
}

func TestRedisWorklistNotificationBrokerFansOutBetweenGoInstancesByTenant(t *testing.T) {
	transport := newSharedWorklistTransport()
	instanceA := NewRedisWorklistNotificationBroker(transport)
	instanceB := NewRedisWorklistNotificationBroker(transport)
	require.NoError(t, instanceA.Start(context.Background()))
	t.Cleanup(instanceA.Close)
	require.NoError(t, instanceB.Start(context.Background()))
	t.Cleanup(instanceB.Close)

	tenantAEvents, unsubscribeA := instanceB.SubscribeWorklistNotifications("tenant-a", 1)
	t.Cleanup(unsubscribeA)
	tenantBEvents, unsubscribeB := instanceB.SubscribeWorklistNotifications("tenant-b", 1)
	t.Cleanup(unsubscribeB)
	notification := serviceTypes.WorklistNotification{
		Type:     serviceTypes.WorklistNotificationTypeStudyStatusUpdated,
		TenantID: "tenant-a", StudyInstanceUID: "study-1", RunID: "run-1", Version: 4,
	}

	require.NoError(t, instanceA.PublishWorklistNotification(context.Background(), notification))
	select {
	case received := <-tenantAEvents:
		require.Equal(t, notification, received)
	case <-time.After(time.Second):
		t.Fatal("second Go instance did not receive the Redis notification")
	}
	select {
	case unexpected := <-tenantBEvents:
		t.Fatalf("tenant-b received tenant-a notification: %+v", unexpected)
	default:
	}
}

func TestRedisWorklistNotificationBrokerRejectsNotificationWithoutTenant(t *testing.T) {
	broker := NewRedisWorklistNotificationBroker(newSharedWorklistTransport())

	err := broker.PublishWorklistNotification(context.Background(), serviceTypes.WorklistNotification{})

	require.EqualError(t, err, "worklist notification tenant is required")
}

func TestRedisWorklistNotificationBrokerCloseReleasesSubscription(t *testing.T) {
	transport := newSharedWorklistTransport()
	broker := NewRedisWorklistNotificationBroker(transport)
	require.NoError(t, broker.Start(context.Background()))

	broker.Close()
	broker.Close()

	require.Eventually(t, func() bool {
		transport.mutex.Lock()
		defer transport.mutex.Unlock()
		return len(transport.subscribers) == 0
	}, time.Second, time.Millisecond)
}
