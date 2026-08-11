package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	redisTypes "api-pacs/infrastructures/database/redis/types"
	apiError "api-pacs/internal/errors"
	serviceTypes "api-pacs/module/user/infrastructure/service/types"
)

const (
	defaultRegistrationRateLimitWindow         = 10 * time.Minute
	defaultRegistrationRateLimitTenantAttempts = 100
	defaultRegistrationRateLimitEmailAttempts  = 5
	defaultRegistrationRateLimitIPAttempts     = 10
)

type RegistrationRateLimitConfig struct {
	Window         time.Duration
	TenantAttempts int64
	EmailAttempts  int64
	IPAttempts     int64
}

type RedisRegistrationRateLimiter struct {
	RedisDBHandlerInterface redisTypes.RedisDBHandlerInterface
	Config                  RegistrationRateLimitConfig
}

type registrationRateLimitScope struct {
	name  string
	value string
	limit int64
}

func RegistrationRateLimitConfigFromEnvironment() RegistrationRateLimitConfig {
	return RegistrationRateLimitConfig{
		Window: time.Duration(positiveEnvironmentInteger(
			"REGISTRATION_RATE_LIMIT_WINDOW_SECONDS",
			int64(defaultRegistrationRateLimitWindow/time.Second),
		)) * time.Second,
		TenantAttempts: positiveEnvironmentInteger(
			"REGISTRATION_RATE_LIMIT_TENANT_ATTEMPTS",
			defaultRegistrationRateLimitTenantAttempts,
		),
		EmailAttempts: positiveEnvironmentInteger(
			"REGISTRATION_RATE_LIMIT_EMAIL_ATTEMPTS",
			defaultRegistrationRateLimitEmailAttempts,
		),
		IPAttempts: positiveEnvironmentInteger(
			"REGISTRATION_RATE_LIMIT_IP_ATTEMPTS",
			defaultRegistrationRateLimitIPAttempts,
		),
	}
}

func positiveEnvironmentInteger(name string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		log.Printf("[security] event=registration_rate_limit_config_invalid setting=%s", name)
		return fallback
	}

	return parsed
}

func (limiter *RedisRegistrationRateLimiter) CheckRegistrationAttempt(
	ctx context.Context,
	data serviceTypes.RegistrationRateLimit,
) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if limiter.RedisDBHandlerInterface == nil {
		return 0, fmt.Errorf("%s: registration rate limiter is not configured", apiError.DatabaseError)
	}

	config := limiter.Config
	if config.Window <= 0 || config.TenantAttempts <= 0 || config.EmailAttempts <= 0 || config.IPAttempts <= 0 {
		config = RegistrationRateLimitConfigFromEnvironment()
	}

	tenantID := strings.TrimSpace(data.TenantID)
	scopes := []registrationRateLimitScope{
		{name: "tenant", value: tenantID, limit: config.TenantAttempts},
		{name: "email", value: strings.ToLower(strings.TrimSpace(data.Email)), limit: config.EmailAttempts},
		{name: "ip", value: data.ClientIP, limit: config.IPAttempts},
	}

	var retryAfter time.Duration
	for _, scope := range scopes {
		count, ttl, err := limiter.RedisDBHandlerInterface.IncrementWithExpiry(
			registrationRateLimitKey(scope.name, tenantID, scope.value),
			config.Window,
		)
		if err != nil {
			log.Printf("[security] event=registration_rate_limit_unavailable scope=%s", scope.name)
			return 0, fmt.Errorf("%s: %w", apiError.DatabaseError, err)
		}
		if ttl <= 0 {
			ttl = config.Window
		}
		if count > scope.limit && ttl > retryAfter {
			retryAfter = ttl
		}
	}

	return retryAfter, nil
}

func registrationRateLimitKey(scope, tenantID, value string) string {
	digest := sha256.Sum256([]byte(tenantID + "\x00" + value))
	return "pacs-ai:registration-rate-limit:" + scope + ":" + hex.EncodeToString(digest[:])
}
