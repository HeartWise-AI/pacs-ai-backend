package service

import (
	"context"
	"sync"

	"api-pacs/module/inference/infrastructure/service/types"
)

// WorklistNotificationBroker fans committed updates out to tenant-scoped subscribers.
type WorklistNotificationBroker struct {
	mutex       sync.RWMutex
	nextID      uint64
	subscribers map[string]map[uint64]chan types.WorklistNotification
}

func NewWorklistNotificationBroker() *WorklistNotificationBroker {
	return &WorklistNotificationBroker{
		subscribers: make(map[string]map[uint64]chan types.WorklistNotification),
	}
}

// PublishWorklistNotification sends without blocking callback persistence.
func (broker *WorklistNotificationBroker) PublishWorklistNotification(
	ctx context.Context,
	notification types.WorklistNotification,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	broker.mutex.RLock()
	defer broker.mutex.RUnlock()
	for _, subscriber := range broker.subscribers[notification.TenantID] {
		select {
		case subscriber <- notification:
		default:
		}
	}
	return nil
}

// SubscribeWorklistNotifications registers a buffered tenant-scoped consumer.
func (broker *WorklistNotificationBroker) SubscribeWorklistNotifications(
	tenantID string,
	buffer int,
) (<-chan types.WorklistNotification, func()) {
	if buffer < 1 {
		buffer = 1
	}

	broker.mutex.Lock()
	broker.nextID++
	subscriberID := broker.nextID
	if broker.subscribers[tenantID] == nil {
		broker.subscribers[tenantID] = make(map[uint64]chan types.WorklistNotification)
	}
	channel := make(chan types.WorklistNotification, buffer)
	broker.subscribers[tenantID][subscriberID] = channel
	broker.mutex.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			broker.mutex.Lock()
			delete(broker.subscribers[tenantID], subscriberID)
			if len(broker.subscribers[tenantID]) == 0 {
				delete(broker.subscribers, tenantID)
			}
			close(channel)
			broker.mutex.Unlock()
		})
	}
	return channel, cancel
}
