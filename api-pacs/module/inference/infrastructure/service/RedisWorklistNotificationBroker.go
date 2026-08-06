package service

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"

	"api-pacs/module/inference/infrastructure/service/types"
)

type redisWorklistNotificationEnvelope struct {
	TenantID     string                     `json:"tenantId"`
	Notification types.WorklistNotification `json:"notification"`
}

// RedisWorklistNotificationBroker publishes committed notifications through
// Redis and feeds received messages into an instance-local tenant broker.
type RedisWorklistNotificationBroker struct {
	transport WorklistNotificationTransport
	local     *WorklistNotificationBroker

	mutex  sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewRedisWorklistNotificationBroker(transport WorklistNotificationTransport) *RedisWorklistNotificationBroker {
	return &RedisWorklistNotificationBroker{
		transport: transport,
		local:     NewWorklistNotificationBroker(),
	}
}

// Start establishes this Go instance's Redis subscription before it begins
// serving SSE clients.
func (broker *RedisWorklistNotificationBroker) Start(ctx context.Context) error {
	broker.mutex.Lock()
	defer broker.mutex.Unlock()
	if broker.cancel != nil {
		return errors.New("redis worklist notification broker is already started")
	}
	if broker.transport == nil {
		return errors.New("redis worklist notification transport is not configured")
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

func (broker *RedisWorklistNotificationBroker) PublishWorklistNotification(
	ctx context.Context,
	notification types.WorklistNotification,
) error {
	if broker.transport == nil {
		return errors.New("redis worklist notification transport is not configured")
	}
	tenantID := strings.TrimSpace(notification.TenantID)
	if tenantID == "" {
		return errors.New("worklist notification tenant is required")
	}
	payload, err := json.Marshal(redisWorklistNotificationEnvelope{
		TenantID: tenantID, Notification: notification,
	})
	if err != nil {
		ObserveWorklistNotification("encode_failed")
		return err
	}
	if err := broker.transport.Publish(ctx, payload); err != nil {
		ObserveWorklistNotification("publish_failed")
		return err
	}
	ObserveWorklistNotification("published")
	return nil
}

func (broker *RedisWorklistNotificationBroker) SubscribeWorklistNotifications(
	tenantID string,
	buffer int,
) (<-chan types.WorklistNotification, func()) {
	return broker.local.SubscribeWorklistNotifications(tenantID, buffer)
}

// Close stops Redis consumption and waits until the listener releases its
// resources. It is safe to call more than once.
func (broker *RedisWorklistNotificationBroker) Close() {
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

func (broker *RedisWorklistNotificationBroker) consume(
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
			var envelope redisWorklistNotificationEnvelope
			if err := json.Unmarshal(payload, &envelope); err != nil {
				ObserveWorklistNotification("invalid_received")
				log.Printf("[Worklist SSE] event=redis_notification_invalid err=%v", err)
				continue
			}
			tenantID := strings.TrimSpace(envelope.TenantID)
			if tenantID == "" {
				ObserveWorklistNotification("invalid_received")
				log.Printf("[Worklist SSE] event=redis_notification_without_tenant")
				continue
			}
			ObserveWorklistNotification("received")
			envelope.Notification.TenantID = tenantID
			if err := broker.local.PublishWorklistNotification(ctx, envelope.Notification); err != nil && ctx.Err() == nil {
				ObserveWorklistNotification("local_fanout_failed")
				log.Printf("[Worklist SSE] event=local_fanout_failed err=%v", err)
			}
		}
	}
}
