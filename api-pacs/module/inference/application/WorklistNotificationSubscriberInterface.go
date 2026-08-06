package application

import "api-pacs/module/inference/infrastructure/service/types"

// WorklistNotificationSubscriberInterface provides a tenant-scoped live feed
// of committed worklist updates. The returned cancel function releases every
// resource owned by the subscription.
type WorklistNotificationSubscriberInterface interface {
	SubscribeWorklistNotifications(tenantID string, buffer int) (<-chan types.WorklistNotification, func())
}
