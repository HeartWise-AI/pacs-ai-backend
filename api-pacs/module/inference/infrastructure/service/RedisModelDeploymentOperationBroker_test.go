package service

import (
	"context"
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
