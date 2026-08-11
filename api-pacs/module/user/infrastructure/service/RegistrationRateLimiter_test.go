package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	redisTypes "api-pacs/infrastructures/database/redis/types"
	serviceTypes "api-pacs/module/user/infrastructure/service/types"
)

type registrationRateLimitRedis struct {
	redisTypes.RedisDBHandlerInterface
	counts map[string]int64
	keys   []string
	ttl    time.Duration
	err    error
}

func (redis *registrationRateLimitRedis) IncrementWithExpiry(key string, _ time.Duration) (int64, time.Duration, error) {
	if redis.err != nil {
		return 0, 0, redis.err
	}
	if redis.counts == nil {
		redis.counts = make(map[string]int64)
	}
	redis.counts[key]++
	redis.keys = append(redis.keys, key)
	return redis.counts[key], redis.ttl, nil
}

func TestRegistrationRateLimiterEnforcesAllTenantScopedDimensions(t *testing.T) {
	counter := &registrationRateLimitRedis{ttl: 90 * time.Second}
	limiter := RedisRegistrationRateLimiter{
		RedisDBHandlerInterface: counter,
		Config: RegistrationRateLimitConfig{
			Window:         10 * time.Minute,
			TenantAttempts: 100,
			EmailAttempts:  1,
			IPAttempts:     100,
		},
	}
	input := serviceTypes.RegistrationRateLimit{
		TenantID: "tenant-a",
		Email:    "public.user@example.com",
		ClientIP: "203.0.113.10",
	}

	retryAfter, err := limiter.CheckRegistrationAttempt(t.Context(), input)
	require.NoError(t, err)
	require.Zero(t, retryAfter)
	retryAfter, err = limiter.CheckRegistrationAttempt(t.Context(), input)
	require.NoError(t, err)
	require.Equal(t, 90*time.Second, retryAfter)
	require.Len(t, counter.counts, 3)

	for _, key := range counter.keys {
		require.NotContains(t, key, input.TenantID)
		require.NotContains(t, key, input.Email)
		require.NotContains(t, key, input.ClientIP)
	}

	input.TenantID = "tenant-b"
	retryAfter, err = limiter.CheckRegistrationAttempt(t.Context(), input)
	require.NoError(t, err)
	require.Zero(t, retryAfter, "the same email in another tenant must use a separate counter")
}

func TestRegistrationRateLimiterFailsClosedWhenRedisIsUnavailable(t *testing.T) {
	limiter := RedisRegistrationRateLimiter{
		RedisDBHandlerInterface: &registrationRateLimitRedis{err: errors.New("redis unavailable")},
		Config: RegistrationRateLimitConfig{
			Window:         time.Minute,
			TenantAttempts: 1,
			EmailAttempts:  1,
			IPAttempts:     1,
		},
	}

	_, err := limiter.CheckRegistrationAttempt(t.Context(), serviceTypes.RegistrationRateLimit{
		TenantID: "tenant-a",
		Email:    "public.user@example.com",
		ClientIP: "203.0.113.10",
	})

	require.Error(t, err)
}

func TestRegistrationRateLimitConfigUsesSafeDefaultsForInvalidValues(t *testing.T) {
	t.Setenv("REGISTRATION_RATE_LIMIT_WINDOW_SECONDS", "invalid")
	t.Setenv("REGISTRATION_RATE_LIMIT_TENANT_ATTEMPTS", "0")
	t.Setenv("REGISTRATION_RATE_LIMIT_EMAIL_ATTEMPTS", "-1")
	t.Setenv("REGISTRATION_RATE_LIMIT_IP_ATTEMPTS", "")

	config := RegistrationRateLimitConfigFromEnvironment()

	require.Equal(t, defaultRegistrationRateLimitWindow, config.Window)
	require.EqualValues(t, defaultRegistrationRateLimitTenantAttempts, config.TenantAttempts)
	require.EqualValues(t, defaultRegistrationRateLimitEmailAttempts, config.EmailAttempts)
	require.EqualValues(t, defaultRegistrationRateLimitIPAttempts, config.IPAttempts)
}

func TestRegistrationRateLimitKeysDoNotContainRawIdentifiers(t *testing.T) {
	key := registrationRateLimitKey("email", "tenant-a", "public.user@example.com")

	require.True(t, strings.HasPrefix(key, "pacs-ai:registration-rate-limit:email:"))
	require.NotContains(t, key, "tenant-a")
	require.NotContains(t, key, "public.user@example.com")
}
