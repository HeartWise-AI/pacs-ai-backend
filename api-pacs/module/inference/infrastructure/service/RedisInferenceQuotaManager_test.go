package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	redisdb "github.com/go-redis/redis"
	"github.com/stretchr/testify/require"

	apiError "api-pacs/internal/errors"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

func newInferenceQuotaTestManager(t *testing.T, limits InferenceQuotaLimits) *RedisInferenceQuotaManager {
	t.Helper()
	host := os.Getenv("REDIS_HOST")
	port := os.Getenv("REDIS_PORT")
	if host == "" {
		host = "localhost"
	}
	if port == "" {
		port = "6379"
	}
	client := redisdb.NewClient(&redisdb.Options{Addr: host + ":" + port, Password: os.Getenv("REDIS_PASSWORD")})
	if err := client.Ping().Err(); err != nil {
		t.Skipf("Redis is unavailable: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return &RedisInferenceQuotaManager{
		Client: client,
		Config: InferenceQuotaConfig{Default: limits, TenantOverrides: map[string]InferenceQuotaLimits{}},
	}
}

func cleanInferenceQuotaScope(t *testing.T, manager *RedisInferenceQuotaManager, tenantID, userID string) {
	t.Helper()
	keys, err := manager.keys(tenantID, userID)
	require.NoError(t, err)
	require.NoError(t, manager.Client.Del(keys...).Err())
	t.Cleanup(func() { _ = manager.Client.Del(keys...).Err() })
}

func TestRedisInferenceQuotaManagerEnforcesAllowanceConcurrencyAndRefund(t *testing.T) {
	manager := newInferenceQuotaTestManager(t, InferenceQuotaLimits{
		Window: time.Minute, Allowance: 2, MaxConcurrentExecutions: 1, ReservationTTL: time.Minute,
	})
	tenantID, userID := "quota-tenant-main", "quota-user-main"
	cleanInferenceQuotaScope(t, manager, tenantID, userID)
	ctx := context.Background()

	first := serviceTypes.InferenceQuotaReservation{TenantID: tenantID, UserID: userID, ReservationID: "first", Units: 1}
	status, err := manager.Reserve(ctx, first)
	require.NoError(t, err)
	require.Equal(t, int64(1), status.Used)
	require.Equal(t, int64(1), status.ActiveExecutions)
	status, err = manager.Reserve(ctx, first)
	require.NoError(t, err)
	require.Equal(t, int64(1), status.Used)
	require.Equal(t, int64(1), status.ActiveExecutions)

	second := serviceTypes.InferenceQuotaReservation{TenantID: tenantID, UserID: userID, ReservationID: "second", Units: 1}
	_, err = manager.Reserve(ctx, second)
	var limitError *apiError.InferenceQuotaLimitError
	require.True(t, stderrors.As(err, &limitError))
	require.Equal(t, apiError.InferenceConcurrencyExceeded, limitError.ErrorCode)

	_, err = manager.Release(ctx, first)
	require.NoError(t, err)
	status, err = manager.Reserve(ctx, second)
	require.NoError(t, err)
	require.Equal(t, int64(2), status.Used)

	_, err = manager.Refund(ctx, second)
	require.NoError(t, err)
	status, err = manager.Refund(ctx, second)
	require.NoError(t, err)
	require.Equal(t, int64(1), status.Used)
	third := serviceTypes.InferenceQuotaReservation{TenantID: tenantID, UserID: userID, ReservationID: "third", Units: 1}
	status, err = manager.Reserve(ctx, third)
	require.NoError(t, err)
	require.Equal(t, int64(2), status.Used)
	_, err = manager.Release(ctx, third)
	require.NoError(t, err)

	_, err = manager.Reserve(ctx, serviceTypes.InferenceQuotaReservation{
		TenantID: tenantID, UserID: userID, ReservationID: "fourth", Units: 1,
	})
	require.True(t, stderrors.As(err, &limitError))
	require.Equal(t, apiError.InferenceQuotaExceeded, limitError.ErrorCode)
	require.Greater(t, limitError.ResetAfterSeconds, int64(0))
}

func TestRedisInferenceQuotaManagerConcurrentReservationsCannotBypassLimit(t *testing.T) {
	manager := newInferenceQuotaTestManager(t, InferenceQuotaLimits{
		Window: time.Minute, Allowance: 100, MaxConcurrentExecutions: 1, ReservationTTL: time.Minute,
	})
	tenantID, userID := "quota-tenant-race", "quota-user-race"
	cleanInferenceQuotaScope(t, manager, tenantID, userID)

	var accepted atomic.Int64
	var waitGroup sync.WaitGroup
	rejections := make(chan error, 24)
	for index := range 24 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, err := manager.Reserve(context.Background(), serviceTypes.InferenceQuotaReservation{
				TenantID: tenantID, UserID: userID,
				ReservationID: fmt.Sprintf("reservation-%d", index), Units: 1,
			})
			if err == nil {
				accepted.Add(1)
				return
			}
			rejections <- err
		}()
	}
	waitGroup.Wait()
	close(rejections)
	require.Equal(t, int64(1), accepted.Load())
	require.Len(t, rejections, 23)
	for err := range rejections {
		var limitError *apiError.InferenceQuotaLimitError
		require.True(t, stderrors.As(err, &limitError))
		require.Equal(t, apiError.InferenceConcurrencyExceeded, limitError.ErrorCode)
	}
	status, err := manager.Status(context.Background(), tenantID, userID)
	require.NoError(t, err)
	require.Equal(t, int64(1), status.Used)
	require.Equal(t, int64(1), status.ActiveExecutions)
}

func TestRedisInferenceQuotaManagerIsolatesTenantAndUserScopes(t *testing.T) {
	manager := newInferenceQuotaTestManager(t, InferenceQuotaLimits{
		Window: time.Minute, Allowance: 1, MaxConcurrentExecutions: 1, ReservationTTL: time.Minute,
	})
	scopes := [][2]string{{"tenant-a", "user-a"}, {"tenant-a", "user-b"}, {"tenant-b", "user-a"}}
	for index, scope := range scopes {
		cleanInferenceQuotaScope(t, manager, scope[0], scope[1])
		status, err := manager.Reserve(context.Background(), serviceTypes.InferenceQuotaReservation{
			TenantID: scope[0], UserID: scope[1], ReservationID: fmt.Sprintf("isolated-%d", index), Units: 1,
		})
		require.NoError(t, err)
		require.Equal(t, int64(1), status.Used)
		require.Equal(t, int64(1), status.ActiveExecutions)
	}
}

func TestInferenceQuotaConfigSupportsTenantOverridesAndSafeFallbacks(t *testing.T) {
	t.Setenv("INFERENCE_USER_QUOTA_WINDOW_SECONDS", "600")
	t.Setenv("INFERENCE_USER_QUOTA_ALLOWANCE", "20")
	t.Setenv("INFERENCE_USER_MAX_CONCURRENT_EXECUTIONS", "3")
	t.Setenv("INFERENCE_USER_RESERVATION_TTL_SECONDS", "900")
	t.Setenv("INFERENCE_USER_QUOTA_TENANT_OVERRIDES_JSON", `{"tenant-special":{"allowance":80,"maxConcurrentExecutions":5}}`)

	config := InferenceQuotaConfigFromEnvironment()
	require.Equal(t, 10*time.Minute, config.Default.Window)
	require.Equal(t, int64(20), config.Default.Allowance)
	require.Equal(t, int64(3), config.Default.MaxConcurrentExecutions)
	require.Equal(t, int64(80), config.limitsForTenant("tenant-special").Allowance)
	require.Equal(t, int64(5), config.limitsForTenant("tenant-special").MaxConcurrentExecutions)

	t.Setenv("INFERENCE_USER_QUOTA_ALLOWANCE", "invalid")
	t.Setenv("INFERENCE_USER_QUOTA_TENANT_OVERRIDES_JSON", "not-json")
	config = InferenceQuotaConfigFromEnvironment()
	require.Equal(t, defaultInferenceQuotaAllowance, config.Default.Allowance)
	require.Empty(t, config.TenantOverrides)
}
