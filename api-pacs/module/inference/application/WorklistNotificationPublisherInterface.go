package application

import (
	"context"

	"api-pacs/module/inference/infrastructure/service/types"
)

// WorklistNotificationPublisherInterface publishes committed processing-run changes.
type WorklistNotificationPublisherInterface interface {
	PublishWorklistNotification(ctx context.Context, notification types.WorklistNotification) error
}
