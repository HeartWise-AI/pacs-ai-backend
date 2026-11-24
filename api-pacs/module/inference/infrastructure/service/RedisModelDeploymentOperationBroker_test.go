package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRedisModelDeploymentOperationBrokerFansOutAcrossInstances(t *testing.T) {
	transport := newSharedWorklistTransport()
	instanceA := NewRedisModelDeploymentOperationBroker(transport)
	instanceB := NewRedisModelDeploymentOperationBroker(transport)
	require.NoError(t, instanceA.Start(context.Background()))
	t.Cleanup(instanceA.Close)
	require.NoError(t, instanceB.Start(context.Background()))
	t.Cleanup(instanceB.Close)
	events, unsubscribe := instanceB.SubscribeModelDeploymentOperation("tenant-a", "model-a", "deployment-a", 1)
	t.Cleanup(unsubscribe)
	operation := deploymentBrokerTestOperation("tenant-a", "model-a", "deployment-a", 7)

	require.NoError(t, instanceA.PublishModelDeploymentOperation(context.Background(), operation))
	select {
	case received := <-events:
		require.Equal(t, operation, received)
	case <-time.After(time.Second):
		t.Fatal("second Go instance did not receive deployment event")
	}
}

type failingDeploymentTransport struct{}

func (failingDeploymentTransport) Publish(context.Context, []byte) error {
	return errors.New("redis unavailable")
}

func (failingDeploymentTransport) Subscribe(ctx context.Context) (<-chan []byte, error) {
	messages := make(chan []byte)
	go func() { <-ctx.Done(); close(messages) }()
	return messages, nil
}

// Streams on the instance running the deployment keep receiving progress when
// Redis fails (Codex review on #387).
func TestRedisModelDeploymentOperationBrokerDeliversLocallyWhenRedisFails(t *testing.T) {
	broker := NewRedisModelDeploymentOperationBroker(failingDeploymentTransport{})
	require.NoError(t, broker.Start(context.Background()))
	t.Cleanup(broker.Close)
	events, unsubscribe := broker.SubscribeModelDeploymentOperation("tenant-a", "model-a", "deployment-a", 1)
	t.Cleanup(unsubscribe)
	operation := deploymentBrokerTestOperation("tenant-a", "model-a", "deployment-a", 3)

	err := broker.PublishModelDeploymentOperation(context.Background(), operation)
	require.Error(t, err, "the Redis failure is still reported")
	select {
	case received := <-events:
		require.Equal(t, operation, received)
	case <-time.After(time.Second):
		t.Fatal("local stream did not receive the event while Redis was failing")
	}
}
