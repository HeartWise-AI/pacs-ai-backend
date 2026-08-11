package application

import (
	"context"
	"time"

	"api-pacs/module/user/infrastructure/service/types"
)

// RegistrationRateLimiterInterface enforces public-registration anti-abuse limits.
type RegistrationRateLimiterInterface interface {
	CheckRegistrationAttempt(ctx context.Context, data types.RegistrationRateLimit) (time.Duration, error)
}
