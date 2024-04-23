package application

import (
	"context"
)

// IAMCommandServiceInterface holds the implementable methods for the iam command service
type IAMCommandServiceInterface interface {
	LoginTenantUser(ctx context.Context, tenantID, idToken string) (string, error)
}
