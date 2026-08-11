package application

import (
	"context"

	"api-pacs/module/inference/infrastructure/service/types"
)

// InferenceQuotaManagerInterface owns per-tenant, per-user inference usage and
// active reservations. Implementations must make reserve decisions atomically.
type InferenceQuotaManagerInterface interface {
	Reserve(ctx context.Context, data types.InferenceQuotaReservation) (types.InferenceQuotaStatus, error)
	Release(ctx context.Context, data types.InferenceQuotaReservation) (types.InferenceQuotaStatus, error)
	Refund(ctx context.Context, data types.InferenceQuotaReservation) (types.InferenceQuotaStatus, error)
	Status(ctx context.Context, tenantID, userID string) (types.InferenceQuotaStatus, error)
}
