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
	t.Setenv("INFERENCE_USER_MAX_QUEUED_EXECUTIONS", "40")
	t.Setenv("INFERENCE_USER_QUOTA_TENANT_OVERRIDES_JSON", `{"tenant-special":{"allowance":80,"maxConcurrentExecutions":5,"maxQueuedExecutions":120},"tenant-inherit":{"allowance":10}}`)

	config := InferenceQuotaConfigFromEnvironment()
	require.Equal(t, 10*time.Minute, config.Default.Window)
	require.Equal(t, int64(20), config.Default.Allowance)
	require.Equal(t, int64(3), config.Default.MaxConcurrentExecutions)
	require.Equal(t, int64(80), config.limitsForTenant("tenant-special").Allowance)
	require.Equal(t, int64(5), config.limitsForTenant("tenant-special").MaxConcurrentExecutions)
	require.Equal(t, int64(40), config.Default.MaxQueuedExecutions)
	require.Equal(t, int64(120), config.limitsForTenant("tenant-special").MaxQueuedExecutions)
	require.Equal(t, int64(40), config.limitsForTenant("tenant-inherit").MaxQueuedExecutions)

	t.Setenv("INFERENCE_USER_QUOTA_ALLOWANCE", "invalid")
	t.Setenv("INFERENCE_USER_QUOTA_TENANT_OVERRIDES_JSON", "not-json")
	t.Setenv("INFERENCE_USER_MAX_QUEUED_EXECUTIONS", "")
	config = InferenceQuotaConfigFromEnvironment()
	require.Equal(t, defaultInferenceQuotaAllowance, config.Default.Allowance)
	require.Equal(t, defaultInferenceMaxQueued, config.Default.MaxQueuedExecutions)
	require.Empty(t, config.TenantOverrides)
}

func queuedReservation(tenantID, userID, id string) serviceTypes.InferenceQuotaReservation {
	return serviceTypes.InferenceQuotaReservation{
		TenantID: tenantID, UserID: userID, ReservationID: id, Units: 1,
		Kind: serviceTypes.InferenceQuotaReservationQueued,
	}
}

func TestRedisInferenceQuotaManagerQueuedReservationsBypassInteractiveCap(t *testing.T) {
	manager := newInferenceQuotaTestManager(t, InferenceQuotaLimits{
		Window: time.Minute, Allowance: 100, MaxConcurrentExecutions: 2, MaxQueuedExecutions: 50, ReservationTTL: time.Minute,
	})
	tenantID, userID := "quota-tenant-queued", "quota-user-queued"
	cleanInferenceQuotaScope(t, manager, tenantID, userID)
	ctx := context.Background()

	// Six viewer reruns on one study: all admitted despite the interactive cap of 2.
	for index := range 6 {
		status, err := manager.Reserve(ctx, queuedReservation(tenantID, userID, fmt.Sprintf("viewer-run-%d", index)))
		require.NoError(t, err)
		require.Equal(t, int64(index+1), status.QueuedExecutions)
		require.Equal(t, int64(0), status.ActiveExecutions)
	}
	// Idempotent re-reserve of a queued run does not double-count.
	status, err := manager.Reserve(ctx, queuedReservation(tenantID, userID, "viewer-run-0"))
	require.NoError(t, err)
	require.Equal(t, int64(6), status.Used)
	require.Equal(t, int64(6), status.QueuedExecutions)

	// Interactive predicts still have both slots available, and the cap still holds for them.
	for _, id := range []string{"predict-1", "predict-2"} {
		_, err = manager.Reserve(ctx, serviceTypes.InferenceQuotaReservation{TenantID: tenantID, UserID: userID, ReservationID: id, Units: 1})
		require.NoError(t, err)
	}
	_, err = manager.Reserve(ctx, serviceTypes.InferenceQuotaReservation{TenantID: tenantID, UserID: userID, ReservationID: "predict-3", Units: 1})
	var limitError *apiError.InferenceQuotaLimitError
	require.True(t, stderrors.As(err, &limitError))
	require.Equal(t, apiError.InferenceConcurrencyExceeded, limitError.ErrorCode)

	// A full interactive cap does not block queued work either.
	status, err = manager.Reserve(ctx, queuedReservation(tenantID, userID, "viewer-run-6"))
	require.NoError(t, err)
	require.Equal(t, int64(7), status.QueuedExecutions)
	require.Equal(t, int64(2), status.ActiveExecutions)

	// Release and refund find queued reservations by ID even without Kind.
	status, err = manager.Release(ctx, serviceTypes.InferenceQuotaReservation{TenantID: tenantID, UserID: userID, ReservationID: "viewer-run-0", Units: 1})
	require.NoError(t, err)
	require.Equal(t, int64(6), status.QueuedExecutions)
	require.Equal(t, int64(9), status.Used)
	status, err = manager.Refund(ctx, queuedReservation(tenantID, userID, "viewer-run-1"))
	require.NoError(t, err)
	require.Equal(t, int64(5), status.QueuedExecutions)
	require.Equal(t, int64(8), status.Used)
	status, err = manager.Refund(ctx, queuedReservation(tenantID, userID, "viewer-run-1"))
	require.NoError(t, err)
	require.Equal(t, int64(8), status.Used)
	require.Equal(t, int64(2), status.ActiveExecutions)

	status, err = manager.Status(ctx, tenantID, userID)
	require.NoError(t, err)
	require.Equal(t, int64(2), status.ActiveExecutions)
	require.Equal(t, int64(5), status.QueuedExecutions)
	require.Equal(t, int64(50), status.MaxQueuedExecutions)
	require.Greater(t, status.QueuedRetryAfter, time.Duration(0))
}

func TestRedisInferenceQuotaManagerEnforcesQueuedSafetyCapAndAllowance(t *testing.T) {
	manager := newInferenceQuotaTestManager(t, InferenceQuotaLimits{
		Window: time.Minute, Allowance: 5, MaxConcurrentExecutions: 1, MaxQueuedExecutions: 3, ReservationTTL: time.Minute,
	})
	tenantID, userID := "quota-tenant-queue-cap", "quota-user-queue-cap"
	cleanInferenceQuotaScope(t, manager, tenantID, userID)
	ctx := context.Background()

	for index := range 3 {
		_, err := manager.Reserve(ctx, queuedReservation(tenantID, userID, fmt.Sprintf("queued-%d", index)))
		require.NoError(t, err)
	}
	_, err := manager.Reserve(ctx, queuedReservation(tenantID, userID, "queued-3"))
	var limitError *apiError.InferenceQuotaLimitError
	require.True(t, stderrors.As(err, &limitError))
	require.Equal(t, apiError.InferenceQueueLimitExceeded, limitError.ErrorCode)
	require.Equal(t, int64(3), limitError.QueuedExecutions)
	require.Equal(t, int64(3), limitError.MaxQueuedExecutions)
	require.Greater(t, limitError.RetryAfterSeconds, int64(0))

	// A rejected queued reservation consumed no allowance; interactive still works.
	status, err := manager.Reserve(ctx, serviceTypes.InferenceQuotaReservation{TenantID: tenantID, UserID: userID, ReservationID: "predict", Units: 1})
	require.NoError(t, err)
	require.Equal(t, int64(4), status.Used)

	_, err = manager.Release(ctx, queuedReservation(tenantID, userID, "queued-0"))
	require.NoError(t, err)
	status, err = manager.Reserve(ctx, queuedReservation(tenantID, userID, "queued-3"))
	require.NoError(t, err)
	require.Equal(t, int64(5), status.Used)

	// The rolling allowance applies to queued work too.
	_, err = manager.Release(ctx, queuedReservation(tenantID, userID, "queued-1"))
	require.NoError(t, err)
	_, err = manager.Reserve(ctx, queuedReservation(tenantID, userID, "queued-4"))
	require.True(t, stderrors.As(err, &limitError))
	require.Equal(t, apiError.InferenceQuotaExceeded, limitError.ErrorCode)
}

func TestRedisInferenceQuotaManagerQueuedReservationsExpireWithoutCallback(t *testing.T) {
	now := time.Now()
	manager := newInferenceQuotaTestManager(t, InferenceQuotaLimits{
		Window: time.Hour, Allowance: 10, MaxConcurrentExecutions: 1, MaxQueuedExecutions: 1, ReservationTTL: time.Minute,
	})
	manager.Now = func() time.Time { return now }
	tenantID, userID := "quota-tenant-queue-ttl", "quota-user-queue-ttl"
	cleanInferenceQuotaScope(t, manager, tenantID, userID)
	ctx := context.Background()

	_, err := manager.Reserve(ctx, queuedReservation(tenantID, userID, "lost-callback"))
	require.NoError(t, err)
	_, err = manager.Reserve(ctx, queuedReservation(tenantID, userID, "blocked"))
	require.Error(t, err)

	now = now.Add(2 * time.Minute)
	status, err := manager.Status(ctx, tenantID, userID)
	require.NoError(t, err)
	require.Equal(t, int64(0), status.QueuedExecutions)
	status, err = manager.Reserve(ctx, queuedReservation(tenantID, userID, "after-expiry"))
	require.NoError(t, err)
	require.Equal(t, int64(1), status.QueuedExecutions)
	require.Equal(t, int64(2), status.Used)

	// A late callback for the expired reservation is a no-op and refunds nothing.
	status, err = manager.Refund(ctx, queuedReservation(tenantID, userID, "lost-callback"))
	require.NoError(t, err)
	require.Equal(t, int64(2), status.Used)
	require.Equal(t, int64(1), status.QueuedExecutions)
}

func TestRedisInferenceQuotaManagerRejectsUnknownReservationKind(t *testing.T) {
	manager := &RedisInferenceQuotaManager{}
	_, err := manager.Reserve(context.Background(), serviceTypes.InferenceQuotaReservation{
		TenantID: "t", UserID: "u", ReservationID: "r", Units: 1, Kind: "bulk",
	})
	require.EqualError(t, err, apiError.InvalidPayload)
}
