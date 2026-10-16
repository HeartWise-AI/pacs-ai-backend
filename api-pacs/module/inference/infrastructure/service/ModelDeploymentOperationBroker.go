package service

import (
	"context"
	"errors"
	"strings"
	"sync"

	"api-pacs/module/inference/domain/entity"
)

var ErrInvalidModelDeploymentOperationEvent = errors.New("model deployment operation event identity is required")

type modelDeploymentOperationSubscriptionKey struct {
	tenantID     string
	modelID      string
	deploymentID string
}

// ModelDeploymentOperationBroker fans durable operation snapshots out to
// scoped subscribers without allowing a slow client to block deployment work.
type ModelDeploymentOperationBroker struct {
	mutex       sync.RWMutex
	nextID      uint64
	subscribers map[modelDeploymentOperationSubscriptionKey]map[uint64]chan entity.InferenceModelDeploymentOperation
}

func NewModelDeploymentOperationBroker() *ModelDeploymentOperationBroker {
	return &ModelDeploymentOperationBroker{
		subscribers: make(map[modelDeploymentOperationSubscriptionKey]map[uint64]chan entity.InferenceModelDeploymentOperation),
	}
}

func (broker *ModelDeploymentOperationBroker) PublishModelDeploymentOperation(
	ctx context.Context,
	operation entity.InferenceModelDeploymentOperation,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key := deploymentOperationSubscriptionKey(operation.TenantID, operation.ModelID, operation.ID)
	if key.tenantID == "" || key.modelID == "" || key.deploymentID == "" {
		return ErrInvalidModelDeploymentOperationEvent
	}

	broker.mutex.RLock()
	defer broker.mutex.RUnlock()
	for _, subscriber := range broker.subscribers[key] {
		select {
		case subscriber <- operation:
		default:
			// Keep the newest durable snapshot for a slow consumer. Firestore and
			// the status endpoint remain the replay source of truth.
			select {
			case <-subscriber:
			default:
			}
			select {
			case subscriber <- operation:
			default:
			}
		}
	}
	return nil
}

func (broker *ModelDeploymentOperationBroker) SubscribeModelDeploymentOperation(
	tenantID, modelID, deploymentID string,
	buffer int,
) (<-chan entity.InferenceModelDeploymentOperation, func()) {
	if buffer < 1 {
		buffer = 1
	}
	key := deploymentOperationSubscriptionKey(tenantID, modelID, deploymentID)

	broker.mutex.Lock()
	broker.nextID++
	subscriberID := broker.nextID
	if broker.subscribers[key] == nil {
		broker.subscribers[key] = make(map[uint64]chan entity.InferenceModelDeploymentOperation)
	}
	channel := make(chan entity.InferenceModelDeploymentOperation, buffer)
	broker.subscribers[key][subscriberID] = channel
	broker.mutex.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			broker.mutex.Lock()
			delete(broker.subscribers[key], subscriberID)
			if len(broker.subscribers[key]) == 0 {
				delete(broker.subscribers, key)
			}
			close(channel)
			broker.mutex.Unlock()
		})
	}
	return channel, cancel
}

func deploymentOperationSubscriptionKey(
	tenantID, modelID, deploymentID string,
) modelDeploymentOperationSubscriptionKey {
	return modelDeploymentOperationSubscriptionKey{
		tenantID: strings.TrimSpace(tenantID), modelID: strings.TrimSpace(modelID),
		deploymentID: strings.TrimSpace(deploymentID),
	}
}
