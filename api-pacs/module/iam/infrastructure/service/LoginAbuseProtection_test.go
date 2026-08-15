package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	redisTypes "api-pacs/infrastructures/database/redis/types"
	serviceTypes "api-pacs/module/iam/infrastructure/service/types"
)

type loginProtectionRedis struct {
	redisTypes.RedisDBHandlerInterface
	counts      map[string]int64
	ttls        map[string]time.Duration
	deletedKeys []string
	err         error
}

func (redis *loginProtectionRedis) GetCounterWithExpiry(key string) (int64, time.Duration, error) {
	if redis.err != nil {
		return 0, 0, redis.err
	}
	return redis.counts[key], redis.ttls[key], nil
}

func (redis *loginProtectionRedis) IncrementWithExpiry(key string, expiry time.Duration) (int64, time.Duration, error) {
	if redis.err != nil {
		return 0, 0, redis.err
	}
	if redis.counts == nil {
		redis.counts = make(map[string]int64)
	}
	if redis.ttls == nil {
		redis.ttls = make(map[string]time.Duration)
	}
	redis.counts[key]++
	if redis.ttls[key] == 0 {
		redis.ttls[key] = expiry
	}
	return redis.counts[key], redis.ttls[key], nil
}

func (redis *loginProtectionRedis) Delete(key string) error {
	if redis.err != nil {
		return redis.err
	}
	redis.deletedKeys = append(redis.deletedKeys, key)
	delete(redis.counts, key)
	delete(redis.ttls, key)
	return nil
}

func testLoginProtectionConfig() LoginAbuseProtectionConfig {
	return LoginAbuseProtectionConfig{
		Enabled:                  true,
		Window:                   10 * time.Minute,
		AccountChallengeFailures: 3,
		IPChallengeFailures:      5,
		IPMaxFailures:            30,
		TenantChallengeFailures:  50,
		TenantMaxFailures:        500,
	}
}

func TestLoginProtectionEscalatesAccountWithoutHardLock(t *testing.T) {
	redis := &loginProtectionRedis{}
	protection := RedisLoginAbuseProtection{RedisDBHandlerInterface: redis, Config: testLoginProtectionConfig()}
	signals := serviceTypes.LoginAbuseSignals{TenantID: "tenant-a", Email: " User@Example.com ", ClientIP: "203.0.113.1"}

	for attempt := 1; attempt <= 40; attempt++ {
		signals.ClientIP = fmt.Sprintf("203.0.113.%d", attempt)
		decision, err := protection.RecordLoginFailure(t.Context(), signals)
		require.NoError(t, err)
		if attempt < 3 {
			require.False(t, decision.ChallengeRequired)
		} else {
			require.True(t, decision.ChallengeRequired)
		}
		require.Zero(t, decision.RetryAfter)
	}

	accountKey := loginFailureKey("account", "tenant-a", "user@example.com")
	require.EqualValues(t, 40, redis.counts[accountKey])
	// The account scope deliberately has no hard limit, even after forty failures.
	scopes := loginFailureScopes(testLoginProtectionConfig(), signals)
	require.Zero(t, scopes[1].MaximumFailures)
}

func TestLoginProtectionHardLimitUsesLongestBreachedTTL(t *testing.T) {
	config := testLoginProtectionConfig()
	signals := serviceTypes.LoginAbuseSignals{TenantID: "tenant-a", Email: "user@example.com", ClientIP: "203.0.113.1"}
	redis := &loginProtectionRedis{
		counts: map[string]int64{
			loginFailureKey("tenant", signals.TenantID, signals.TenantID): config.TenantMaxFailures,
			loginFailureKey("ip", signals.TenantID, signals.ClientIP):     config.IPMaxFailures,
		},
		ttls: map[string]time.Duration{
			loginFailureKey("tenant", signals.TenantID, signals.TenantID): 41 * time.Second,
			loginFailureKey("ip", signals.TenantID, signals.ClientIP):     73 * time.Second,
		},
	}
	protection := RedisLoginAbuseProtection{RedisDBHandlerInterface: redis, Config: config}

	decision, err := protection.EvaluateLoginAttempt(t.Context(), signals)

	require.NoError(t, err)
	require.True(t, decision.ChallengeRequired)
	require.Equal(t, 73*time.Second, decision.RetryAfter)
}

func TestLoginProtectionResetClearsOnlyTenantAccountKey(t *testing.T) {
	config := testLoginProtectionConfig()
	signals := serviceTypes.LoginAbuseSignals{TenantID: "tenant-a", Email: "User@Example.com", ClientIP: "203.0.113.1"}
	accountKey := loginFailureKey("account", signals.TenantID, "user@example.com")
	tenantKey := loginFailureKey("tenant", signals.TenantID, signals.TenantID)
	ipKey := loginFailureKey("ip", signals.TenantID, signals.ClientIP)
	redis := &loginProtectionRedis{counts: map[string]int64{accountKey: 3, tenantKey: 3, ipKey: 3}, ttls: map[string]time.Duration{}}
	protection := RedisLoginAbuseProtection{RedisDBHandlerInterface: redis, Config: config}

	err := protection.ResetAccountFailures(t.Context(), signals)

	require.NoError(t, err)
	require.Equal(t, []string{accountKey}, redis.deletedKeys)
	require.NotContains(t, redis.counts, accountKey)
	require.EqualValues(t, 3, redis.counts[tenantKey])
	require.EqualValues(t, 3, redis.counts[ipKey])
}

func TestLoginProtectionIsTenantIsolatedAndKeysContainNoRawSignals(t *testing.T) {
	keyA := loginFailureKey("account", "tenant-a", "user@example.com")
	keyB := loginFailureKey("account", "tenant-b", "user@example.com")

	require.NotEqual(t, keyA, keyB)
	require.True(t, strings.HasPrefix(keyA, "pacs-ai:login-failures:account:"))
	require.NotContains(t, keyA, "tenant-a")
	require.NotContains(t, keyA, "user@example.com")
}

func TestLoginProtectionFailsClosedWhenRedisUnavailable(t *testing.T) {
	protection := RedisLoginAbuseProtection{
		RedisDBHandlerInterface: &loginProtectionRedis{err: errors.New("redis unavailable")},
		Config:                  testLoginProtectionConfig(),
	}

	_, err := protection.EvaluateLoginAttempt(t.Context(), serviceTypes.LoginAbuseSignals{
		TenantID: "tenant-a", Email: "user@example.com", ClientIP: "203.0.113.1",
	})

	require.Error(t, err)
}

func TestDisabledLoginProtectionSkipsRedis(t *testing.T) {
	config := testLoginProtectionConfig()
	config.Enabled = false
	protection := RedisLoginAbuseProtection{RedisDBHandlerInterface: &loginProtectionRedis{err: errors.New("must not be called")}, Config: config}
	signals := serviceTypes.LoginAbuseSignals{TenantID: "tenant-a", Email: "user@example.com", ClientIP: "203.0.113.1"}

	decision, err := protection.EvaluateLoginAttempt(t.Context(), signals)
	require.NoError(t, err)
	require.False(t, decision.ChallengeRequired)
	require.NoError(t, protection.ResetAccountFailures(t.Context(), signals))
}

func TestLoginProtectionConfigUsesSafeDefaults(t *testing.T) {
	t.Setenv("LOGIN_ABUSE_PROTECTION_ENABLED", "invalid")
	t.Setenv("LOGIN_FAILURE_WINDOW_SECONDS", "0")
	t.Setenv("LOGIN_ACCOUNT_CHALLENGE_FAILURES", "invalid")
	t.Setenv("LOGIN_IP_CHALLENGE_FAILURES", "-1")
	t.Setenv("LOGIN_IP_MAX_FAILURES", "0")
	t.Setenv("LOGIN_TENANT_CHALLENGE_FAILURES", "invalid")
	t.Setenv("LOGIN_TENANT_MAX_FAILURES", "-1")

	config := LoginAbuseProtectionConfigFromEnvironment()

	require.True(t, config.Enabled)
	require.Equal(t, defaultLoginFailureWindow, config.Window)
	require.EqualValues(t, defaultLoginAccountChallengeFailures, config.AccountChallengeFailures)
	require.EqualValues(t, defaultLoginIPChallengeFailures, config.IPChallengeFailures)
	require.EqualValues(t, defaultLoginIPMaxFailures, config.IPMaxFailures)
	require.EqualValues(t, defaultLoginTenantChallengeFailures, config.TenantChallengeFailures)
	require.EqualValues(t, defaultLoginTenantMaxFailures, config.TenantMaxFailures)
}

func TestLoginProtectionConfigRejectsChallengeThresholdAboveHardLimit(t *testing.T) {
	t.Setenv("LOGIN_IP_CHALLENGE_FAILURES", "100")
	t.Setenv("LOGIN_IP_MAX_FAILURES", "5")
	t.Setenv("LOGIN_TENANT_CHALLENGE_FAILURES", "1000")
	t.Setenv("LOGIN_TENANT_MAX_FAILURES", "50")

	config := LoginAbuseProtectionConfigFromEnvironment()

	require.EqualValues(t, defaultLoginIPChallengeFailures, config.IPChallengeFailures)
	require.EqualValues(t, defaultLoginIPMaxFailures, config.IPMaxFailures)
	require.EqualValues(t, defaultLoginTenantChallengeFailures, config.TenantChallengeFailures)
	require.EqualValues(t, defaultLoginTenantMaxFailures, config.TenantMaxFailures)
}

func TestLoginProtectionConfigRejectsWindowThatOverflowsDuration(t *testing.T) {
	t.Setenv("LOGIN_FAILURE_WINDOW_SECONDS", "9223372036854775807")

	config := LoginAbuseProtectionConfigFromEnvironment()

	require.Equal(t, defaultLoginFailureWindow, config.Window)
}
