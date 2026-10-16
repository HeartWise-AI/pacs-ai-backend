package service

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"

	"api-pacs/module/inference/domain/entity"
)

const DefaultRedisModelDeploymentOperationChannel = "pacs-ai:inference:model-deployments:events"

type redisModelDeploymentOperationEnvelope struct {
	TenantID string                                   `json:"tenantId"`
	ModelID  string                                   `json:"modelId"`
	Event    entity.InferenceModelDeploymentOperation `json:"event"`
}

// RedisModelDeploymentOperationBroker provides ephemeral cross-instance
// delivery. Durable state and reconnect snapshots remain in Firestore.
type RedisModelDeploymentOperationBroker struct {
	transport WorklistNotificationTransport
	local     *ModelDeploymentOperationBroker

	mutex  sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewRedisModelDeploymentOperationBroker(
	transport WorklistNotificationTransport,
) *RedisModelDeploymentOperationBroker {
	return &RedisModelDeploymentOperationBroker{
		transport: transport,
		local:     NewModelDeploymentOperationBroker(),
	}
}

func (broker *RedisModelDeploymentOperationBroker) Start(ctx context.Context) error {
	broker.mutex.Lock()
	defer broker.mutex.Unlock()
	if broker.cancel != nil {
		return errors.New("redis model deployment operation broker is already started")
	}
	if broker.transport == nil {
		return errors.New("redis model deployment operation transport is not configured")
	}

	listenerContext, cancel := context.WithCancel(ctx)
	messages, err := broker.transport.Subscribe(listenerContext)
	if err != nil {
		cancel()
		return err
	}
	done := make(chan struct{})
	broker.cancel = cancel
	broker.done = done
	go broker.consume(listenerContext, messages, done)
	return nil
}

func (broker *RedisModelDeploymentOperationBroker) PublishModelDeploymentOperation(
	ctx context.Context,
	operation entity.InferenceModelDeploymentOperation,
) error {
	if broker.transport == nil {
		return errors.New("redis model deployment operation transport is not configured")
	}
	tenantID := strings.TrimSpace(operation.TenantID)
	modelID := strings.TrimSpace(operation.ModelID)
	deploymentID := strings.TrimSpace(operation.ID)
	if tenantID == "" || modelID == "" || deploymentID == "" {
		return ErrInvalidModelDeploymentOperationEvent
	}
	payload, err := json.Marshal(redisModelDeploymentOperationEnvelope{
		TenantID: tenantID, ModelID: modelID, Event: operation,
	})
	if err != nil {
		return err
	}
	return broker.transport.Publish(ctx, payload)
}

func (broker *RedisModelDeploymentOperationBroker) SubscribeModelDeploymentOperation(
	tenantID, modelID, deploymentID string,
	buffer int,
) (<-chan entity.InferenceModelDeploymentOperation, func()) {
	return broker.local.SubscribeModelDeploymentOperation(tenantID, modelID, deploymentID, buffer)
}

func (broker *RedisModelDeploymentOperationBroker) Close() {
	broker.mutex.Lock()
	cancel := broker.cancel
	done := broker.done
	broker.cancel = nil
	broker.done = nil
	broker.mutex.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (broker *RedisModelDeploymentOperationBroker) consume(
	ctx context.Context,
	messages <-chan []byte,
	done chan<- struct{},
) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case payload, open := <-messages:
			if !open {
				return
			}
			var envelope redisModelDeploymentOperationEnvelope
			if err := json.Unmarshal(payload, &envelope); err != nil {
				log.Printf("[model-deployment-sse] event=redis_event_invalid err=%v", err)
				continue
			}
			envelope.Event.TenantID = strings.TrimSpace(envelope.TenantID)
			envelope.Event.ModelID = strings.TrimSpace(envelope.ModelID)
			if err := broker.local.PublishModelDeploymentOperation(ctx, envelope.Event); err != nil && ctx.Err() == nil {
				log.Printf("[model-deployment-sse] event=local_fanout_failed err=%v", err)
			}
		}
	}
}
