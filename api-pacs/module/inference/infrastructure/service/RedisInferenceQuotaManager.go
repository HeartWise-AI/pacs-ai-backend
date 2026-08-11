package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	redisdb "github.com/go-redis/redis"

	apiError "api-pacs/internal/errors"
	"api-pacs/module/inference/infrastructure/service/types"
)

const (
	defaultInferenceQuotaWindow          = 24 * time.Hour
	defaultInferenceQuotaAllowance int64 = 50
	defaultInferenceMaxConcurrent  int64 = 2
	defaultInferenceReservationTTL       = 2 * time.Hour
)

// InferenceQuotaLimits are resolved independently for every tenant.
type InferenceQuotaLimits struct {
	Window                  time.Duration
	Allowance               int64
	MaxConcurrentExecutions int64
	ReservationTTL          time.Duration
}

// InferenceQuotaConfig contains safe defaults and optional tenant overrides.
// Overrides are loaded at process startup so operators can tune limits without
// changing or rebuilding application code.
type InferenceQuotaConfig struct {
	Default         InferenceQuotaLimits
	TenantOverrides map[string]InferenceQuotaLimits
}

type inferenceQuotaEnvironmentOverride struct {
	WindowSeconds           int64 `json:"windowSeconds"`
	Allowance               int64 `json:"allowance"`
	MaxConcurrentExecutions int64 `json:"maxConcurrentExecutions"`
	ReservationTTLSeconds   int64 `json:"reservationTtlSeconds"`
}

// InferenceQuotaConfigFromEnvironment resolves safe defaults plus optional
// per-tenant JSON overrides from INFERENCE_USER_QUOTA_TENANT_OVERRIDES_JSON.
func InferenceQuotaConfigFromEnvironment() InferenceQuotaConfig {
	defaults := InferenceQuotaLimits{
		Window:                  positiveDurationFromEnvironment("INFERENCE_USER_QUOTA_WINDOW_SECONDS", defaultInferenceQuotaWindow),
		Allowance:               positiveInt64FromEnvironment("INFERENCE_USER_QUOTA_ALLOWANCE", defaultInferenceQuotaAllowance),
		MaxConcurrentExecutions: positiveInt64FromEnvironment("INFERENCE_USER_MAX_CONCURRENT_EXECUTIONS", defaultInferenceMaxConcurrent),
		ReservationTTL:          positiveDurationFromEnvironment("INFERENCE_USER_RESERVATION_TTL_SECONDS", defaultInferenceReservationTTL),
	}

	config := InferenceQuotaConfig{Default: defaults, TenantOverrides: map[string]InferenceQuotaLimits{}}
	rawOverrides := strings.TrimSpace(os.Getenv("INFERENCE_USER_QUOTA_TENANT_OVERRIDES_JSON"))
	if rawOverrides == "" {
		return config
	}

	parsed := map[string]inferenceQuotaEnvironmentOverride{}
	if err := json.Unmarshal([]byte(rawOverrides), &parsed); err != nil {
		return config
	}
	for tenantID, override := range parsed {
		tenantID = strings.TrimSpace(tenantID)
		if tenantID == "" {
			continue
		}
		limits := defaults
		if override.WindowSeconds > 0 {
			limits.Window = time.Duration(override.WindowSeconds) * time.Second
		}
		if override.Allowance > 0 {
			limits.Allowance = override.Allowance
		}
		if override.MaxConcurrentExecutions > 0 {
			limits.MaxConcurrentExecutions = override.MaxConcurrentExecutions
		}
		if override.ReservationTTLSeconds > 0 {
			limits.ReservationTTL = time.Duration(override.ReservationTTLSeconds) * time.Second
		}
		config.TenantOverrides[tenantID] = limits
	}
	return config
}

func positiveDurationFromEnvironment(name string, fallback time.Duration) time.Duration {
	seconds, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64)
	if err != nil || seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func positiveInt64FromEnvironment(name string, fallback int64) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(os.Getenv(name)), 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func (config InferenceQuotaConfig) limitsForTenant(tenantID string) InferenceQuotaLimits {
	if override, ok := config.TenantOverrides[strings.TrimSpace(tenantID)]; ok {
		return override
	}
	return config.Default
}

var reserveInferenceQuotaScript = redisdb.NewScript(`
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local reservation_ttl = tonumber(ARGV[3])
local allowance = tonumber(ARGV[4])
local concurrent_limit = tonumber(ARGV[5])
local units = tonumber(ARGV[6])
local reservation_id = ARGV[7]

local expired = redis.call("ZRANGEBYSCORE", KEYS[2], "-inf", now)
for _, member in ipairs(expired) do
  redis.call("HDEL", KEYS[3], member)
end
redis.call("ZREMRANGEBYSCORE", KEYS[2], "-inf", now)

local function active_units()
  local total = 0
  local members = redis.call("ZRANGE", KEYS[2], 0, -1)
  for _, member in ipairs(members) do
    total = total + (tonumber(redis.call("HGET", KEYS[3], member)) or 0)
  end
  return total
end

local function earliest_retry()
  local first = redis.call("ZRANGE", KEYS[2], 0, 0, "WITHSCORES")
  if #first < 2 then return 0 end
  return math.max(0, tonumber(first[2]) - now)
end

local used = tonumber(redis.call("GET", KEYS[1])) or 0
local usage_ttl = redis.call("PTTL", KEYS[1])
if usage_ttl < 0 and used > 0 then
  redis.call("PEXPIRE", KEYS[1], window)
  usage_ttl = window
end
local active = active_units()

if redis.call("ZSCORE", KEYS[2], reservation_id) then
  return {0, used, usage_ttl, active, earliest_retry()}
end
if used + units > allowance then
  if usage_ttl < 0 then usage_ttl = window end
  return {1, used, usage_ttl, active, earliest_retry()}
end
if active + units > concurrent_limit then
  return {2, used, usage_ttl, active, earliest_retry()}
end

used = redis.call("INCRBY", KEYS[1], units)
if used == units then
  redis.call("PEXPIRE", KEYS[1], window)
end
usage_ttl = redis.call("PTTL", KEYS[1])
redis.call("HSET", KEYS[3], reservation_id, units)
redis.call("ZADD", KEYS[2], now + reservation_ttl, reservation_id)
redis.call("PEXPIRE", KEYS[2], reservation_ttl + 60000)
redis.call("PEXPIRE", KEYS[3], reservation_ttl + 60000)
return {0, used, usage_ttl, active + units, earliest_retry()}
`)

var releaseInferenceQuotaScript = redisdb.NewScript(`
local now = tonumber(ARGV[1])
local reservation_id = ARGV[2]
local expired = redis.call("ZRANGEBYSCORE", KEYS[2], "-inf", now)
for _, member in ipairs(expired) do redis.call("HDEL", KEYS[3], member) end
redis.call("ZREMRANGEBYSCORE", KEYS[2], "-inf", now)

local units = tonumber(redis.call("HGET", KEYS[3], reservation_id)) or 0
if units > 0 then
  redis.call("HDEL", KEYS[3], reservation_id)
  redis.call("ZREM", KEYS[2], reservation_id)
end

local active = 0
local members = redis.call("ZRANGE", KEYS[2], 0, -1)
for _, member in ipairs(members) do active = active + (tonumber(redis.call("HGET", KEYS[3], member)) or 0) end
local first = redis.call("ZRANGE", KEYS[2], 0, 0, "WITHSCORES")
local retry = 0
if #first >= 2 then retry = math.max(0, tonumber(first[2]) - now) end
return {units, tonumber(redis.call("GET", KEYS[1])) or 0, redis.call("PTTL", KEYS[1]), active, retry}
`)

var refundInferenceQuotaScript = redisdb.NewScript(`
local now = tonumber(ARGV[1])
local reservation_id = ARGV[2]
local expired = redis.call("ZRANGEBYSCORE", KEYS[2], "-inf", now)
for _, member in ipairs(expired) do redis.call("HDEL", KEYS[3], member) end
redis.call("ZREMRANGEBYSCORE", KEYS[2], "-inf", now)

local units = tonumber(redis.call("HGET", KEYS[3], reservation_id)) or 0
if units > 0 then
  redis.call("HDEL", KEYS[3], reservation_id)
  redis.call("ZREM", KEYS[2], reservation_id)
  local used = tonumber(redis.call("GET", KEYS[1])) or 0
  local next_used = math.max(0, used - units)
  if next_used == 0 then redis.call("DEL", KEYS[1]) else redis.call("SET", KEYS[1], next_used, "KEEPTTL") end
end

local active = 0
local members = redis.call("ZRANGE", KEYS[2], 0, -1)
for _, member in ipairs(members) do active = active + (tonumber(redis.call("HGET", KEYS[3], member)) or 0) end
local first = redis.call("ZRANGE", KEYS[2], 0, 0, "WITHSCORES")
local retry = 0
if #first >= 2 then retry = math.max(0, tonumber(first[2]) - now) end
return {units, tonumber(redis.call("GET", KEYS[1])) or 0, redis.call("PTTL", KEYS[1]), active, retry}
`)

var inferenceQuotaStatusScript = redisdb.NewScript(`
local now = tonumber(ARGV[1])
local expired = redis.call("ZRANGEBYSCORE", KEYS[2], "-inf", now)
for _, member in ipairs(expired) do redis.call("HDEL", KEYS[3], member) end
redis.call("ZREMRANGEBYSCORE", KEYS[2], "-inf", now)
local active = 0
local members = redis.call("ZRANGE", KEYS[2], 0, -1)
for _, member in ipairs(members) do active = active + (tonumber(redis.call("HGET", KEYS[3], member)) or 0) end
local first = redis.call("ZRANGE", KEYS[2], 0, 0, "WITHSCORES")
local retry = 0
if #first >= 2 then retry = math.max(0, tonumber(first[2]) - now) end
return {0, tonumber(redis.call("GET", KEYS[1])) or 0, redis.call("PTTL", KEYS[1]), active, retry}
`)

// RedisInferenceQuotaManager uses Redis scripts so concurrent API instances
// cannot independently admit work beyond the same tenant/user limits.
type RedisInferenceQuotaManager struct {
	Client *redisdb.Client
	Config InferenceQuotaConfig
	Now    func() time.Time
}

func (manager *RedisInferenceQuotaManager) Reserve(ctx context.Context, data types.InferenceQuotaReservation) (types.InferenceQuotaStatus, error) {
	if err := validateInferenceQuotaReservation(data); err != nil {
		return types.InferenceQuotaStatus{}, err
	}
	if err := ctx.Err(); err != nil {
		return types.InferenceQuotaStatus{}, err
	}
	limits := manager.Config.limitsForTenant(data.TenantID)
	keys, err := manager.keys(data.TenantID, data.UserID)
	if err != nil {
		return types.InferenceQuotaStatus{}, err
	}
	now := manager.now()
	result, err := reserveInferenceQuotaScript.Run(manager.Client, keys,
		now.UnixMilli(), limits.Window.Milliseconds(), limits.ReservationTTL.Milliseconds(),
		limits.Allowance, limits.MaxConcurrentExecutions, data.Units, data.ReservationID,
	).Result()
	if err != nil {
		ObserveInferenceQuotaEvent("unavailable")
		return types.InferenceQuotaStatus{}, errors.New(apiError.InferenceQuotaUnavailable)
	}
	status, decision, err := parseInferenceQuotaResult(result, limits)
	if err != nil {
		ObserveInferenceQuotaEvent("unavailable")
		return types.InferenceQuotaStatus{}, errors.New(apiError.InferenceQuotaUnavailable)
	}
	if decision == 1 {
		ObserveInferenceQuotaEvent("rejected_allowance")
		return status, inferenceQuotaLimitError(apiError.InferenceQuotaExceeded, status, status.ResetAfter)
	}
	if decision == 2 {
		ObserveInferenceQuotaEvent("rejected_concurrency")
		return status, inferenceQuotaLimitError(apiError.InferenceConcurrencyExceeded, status, status.ConcurrentRetryAfter)
	}
	ObserveInferenceQuotaEvent("accepted")
	return status, nil
}

func (manager *RedisInferenceQuotaManager) Release(ctx context.Context, data types.InferenceQuotaReservation) (types.InferenceQuotaStatus, error) {
	return manager.finish(ctx, data, false)
}

func (manager *RedisInferenceQuotaManager) Refund(ctx context.Context, data types.InferenceQuotaReservation) (types.InferenceQuotaStatus, error) {
	return manager.finish(ctx, data, true)
}

func (manager *RedisInferenceQuotaManager) finish(ctx context.Context, data types.InferenceQuotaReservation, refund bool) (types.InferenceQuotaStatus, error) {
	if err := validateInferenceQuotaReservation(data); err != nil {
		return types.InferenceQuotaStatus{}, err
	}
	if err := ctx.Err(); err != nil {
		return types.InferenceQuotaStatus{}, err
	}
	limits := manager.Config.limitsForTenant(data.TenantID)
	keys, err := manager.keys(data.TenantID, data.UserID)
	if err != nil {
		return types.InferenceQuotaStatus{}, err
	}
	script := releaseInferenceQuotaScript
	if refund {
		script = refundInferenceQuotaScript
	}
	result, err := script.Run(manager.Client, keys, manager.now().UnixMilli(), data.ReservationID).Result()
	if err != nil {
		ObserveInferenceQuotaEvent("unavailable")
		return types.InferenceQuotaStatus{}, errors.New(apiError.InferenceQuotaUnavailable)
	}
	status, affectedUnits, err := parseInferenceQuotaResult(result, limits)
	if err != nil {
		ObserveInferenceQuotaEvent("unavailable")
		return types.InferenceQuotaStatus{}, errors.New(apiError.InferenceQuotaUnavailable)
	}
	if affectedUnits > 0 {
		if refund {
			ObserveInferenceQuotaEvent("refunded")
		} else {
			ObserveInferenceQuotaEvent("completed")
			ObserveInferenceQuotaEvent("released")
		}
	}
	return status, nil
}

func (manager *RedisInferenceQuotaManager) Status(ctx context.Context, tenantID, userID string) (types.InferenceQuotaStatus, error) {
	if strings.TrimSpace(tenantID) == "" || strings.TrimSpace(userID) == "" || manager.Client == nil {
		return types.InferenceQuotaStatus{}, errors.New(apiError.InferenceQuotaUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return types.InferenceQuotaStatus{}, err
	}
	limits := manager.Config.limitsForTenant(tenantID)
	keys, err := manager.keys(tenantID, userID)
	if err != nil {
		return types.InferenceQuotaStatus{}, err
	}
	result, err := inferenceQuotaStatusScript.Run(manager.Client, keys, manager.now().UnixMilli()).Result()
	if err != nil {
		ObserveInferenceQuotaEvent("unavailable")
		return types.InferenceQuotaStatus{}, errors.New(apiError.InferenceQuotaUnavailable)
	}
	status, _, err := parseInferenceQuotaResult(result, limits)
	if err != nil {
		ObserveInferenceQuotaEvent("unavailable")
		return types.InferenceQuotaStatus{}, errors.New(apiError.InferenceQuotaUnavailable)
	}
	return status, nil
}

func validateInferenceQuotaReservation(data types.InferenceQuotaReservation) error {
	if strings.TrimSpace(data.TenantID) == "" || strings.TrimSpace(data.UserID) == "" ||
		strings.TrimSpace(data.ReservationID) == "" || data.Units <= 0 {
		return errors.New(apiError.InvalidPayload)
	}
	return nil
}

func (manager *RedisInferenceQuotaManager) now() time.Time {
	if manager.Now != nil {
		return manager.Now()
	}
	return time.Now()
}

func (manager *RedisInferenceQuotaManager) keys(tenantID, userID string) ([]string, error) {
	if manager.Client == nil {
		return nil, errors.New(apiError.InferenceQuotaUnavailable)
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(tenantID) + "\x00" + strings.TrimSpace(userID)))
	scope := hex.EncodeToString(digest[:])
	prefix := "pacs-ai:inference-quota:{" + scope + "}:"
	return []string{prefix + "usage", prefix + "active", prefix + "reservation-units"}, nil
}

func parseInferenceQuotaResult(result interface{}, limits InferenceQuotaLimits) (types.InferenceQuotaStatus, int64, error) {
	values, ok := result.([]interface{})
	if !ok || len(values) != 5 {
		return types.InferenceQuotaStatus{}, 0, fmt.Errorf("unexpected quota result: %T", result)
	}
	parsed := make([]int64, len(values))
	for index, value := range values {
		parsedValue, ok := value.(int64)
		if !ok {
			return types.InferenceQuotaStatus{}, 0, fmt.Errorf("unexpected quota result value %d: %T", index, value)
		}
		parsed[index] = parsedValue
	}
	used := parsed[1]
	remaining := limits.Allowance - used
	if remaining < 0 {
		remaining = 0
	}
	resetAfter := time.Duration(maxInt64(parsed[2], 0)) * time.Millisecond
	concurrentRetryAfter := time.Duration(maxInt64(parsed[4], 0)) * time.Millisecond
	return types.InferenceQuotaStatus{
		Allowance: limits.Allowance, Used: used, Remaining: remaining,
		Window: limits.Window, ResetAfter: resetAfter,
		MaxConcurrentExecutions: limits.MaxConcurrentExecutions,
		ActiveExecutions:        parsed[3], ConcurrentRetryAfter: concurrentRetryAfter,
	}, parsed[0], nil
}

func inferenceQuotaLimitError(code string, status types.InferenceQuotaStatus, retryAfter time.Duration) error {
	return &apiError.InferenceQuotaLimitError{
		ErrorCode: code, Allowance: status.Allowance, Used: status.Used, Remaining: status.Remaining,
		ResetAfterSeconds:       durationSecondsCeiling(status.ResetAfter),
		MaxConcurrentExecutions: status.MaxConcurrentExecutions,
		ActiveExecutions:        status.ActiveExecutions,
		RetryAfterSeconds:       durationSecondsCeiling(retryAfter),
	}
}

func durationSecondsCeiling(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return int64((duration + time.Second - 1) / time.Second)
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
