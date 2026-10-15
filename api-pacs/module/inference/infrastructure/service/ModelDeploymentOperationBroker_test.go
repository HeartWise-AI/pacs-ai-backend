package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"api-pacs/module/inference/domain/entity"
)

func deploymentBrokerTestOperation(tenantID, modelID, deploymentID string, sequence int64) entity.InferenceModelDeploymentOperation {
	return entity.InferenceModelDeploymentOperation{
		ID: deploymentID, TenantID: tenantID, ModelID: modelID, Sequence: sequence,
		Status: entity.InferenceModelDeploymentOperationRunning,
	}
}

func TestModelDeploymentOperationBrokerScopesByTenantModelAndDeployment(t *testing.T) {
	broker := NewModelDeploymentOperationBroker()
	matching, cancelMatching := broker.SubscribeModelDeploymentOperation("tenant-a", "model-a", "deployment-a", 1)
	t.Cleanup(cancelMatching)
	otherTenant, cancelOtherTenant := broker.SubscribeModelDeploymentOperation("tenant-b", "model-a", "deployment-a", 1)
	t.Cleanup(cancelOtherTenant)
	otherModel, cancelOtherModel := broker.SubscribeModelDeploymentOperation("tenant-a", "model-b", "deployment-a", 1)
	t.Cleanup(cancelOtherModel)
	operation := deploymentBrokerTestOperation("tenant-a", "model-a", "deployment-a", 2)

	require.NoError(t, broker.PublishModelDeploymentOperation(context.Background(), operation))
	require.Equal(t, operation, <-matching)
	for name, channel := range map[string]<-chan entity.InferenceModelDeploymentOperation{
		"other tenant": otherTenant, "other model": otherModel,
	} {
		select {
		case unexpected := <-channel:
			t.Fatalf("%s received scoped event: %+v", name, unexpected)
		default:
		}
	}
}

func TestModelDeploymentOperationBrokerCoalescesSlowConsumerToNewestSnapshot(t *testing.T) {
	broker := NewModelDeploymentOperationBroker()
	events, cancel := broker.SubscribeModelDeploymentOperation("tenant-a", "model-a", "deployment-a", 1)
	t.Cleanup(cancel)

	for sequence := int64(1); sequence <= 5; sequence++ {
		require.NoError(t, broker.PublishModelDeploymentOperation(
			context.Background(), deploymentBrokerTestOperation("tenant-a", "model-a", "deployment-a", sequence),
		))
	}

	require.Equal(t, int64(5), (<-events).Sequence)
}

func TestModelDeploymentOperationBrokerRejectsUnscopedEvent(t *testing.T) {
	broker := NewModelDeploymentOperationBroker()
	require.ErrorIs(t, broker.PublishModelDeploymentOperation(
		context.Background(), entity.InferenceModelDeploymentOperation{},
	), ErrInvalidModelDeploymentOperationEvent)
}
