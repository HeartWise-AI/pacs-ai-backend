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
	defaultInferenceMaxQueued      int64 = 50
	defaultInferenceReservationTTL       = 2 * time.Hour
)

// InferenceQuotaLimits are resolved independently for every tenant.
type InferenceQuotaLimits struct {
	Window                  time.Duration
	Allowance               int64
	MaxConcurrentExecutions int64
	// MaxQueuedExecutions is a runaway-queueing safety net for queued
	// (managed) reservations; it is independent of MaxConcurrentExecutions.
	MaxQueuedExecutions int64
	ReservationTTL      time.Duration
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
	MaxQueuedExecutions     int64 `json:"maxQueuedExecutions"`
	ReservationTTLSeconds   int64 `json:"reservationTtlSeconds"`
}

// InferenceQuotaConfigFromEnvironment resolves safe defaults plus optional
// per-tenant JSON overrides from INFERENCE_USER_QUOTA_TENANT_OVERRIDES_JSON.
func InferenceQuotaConfigFromEnvironment() InferenceQuotaConfig {
	defaults := InferenceQuotaLimits{
		Window:                  positiveDurationFromEnvironment("INFERENCE_USER_QUOTA_WINDOW_SECONDS", defaultInferenceQuotaWindow),
		Allowance:               positiveInt64FromEnvironment("INFERENCE_USER_QUOTA_ALLOWANCE", defaultInferenceQuotaAllowance),
		MaxConcurrentExecutions: positiveInt64FromEnvironment("INFERENCE_USER_MAX_CONCURRENT_EXECUTIONS", defaultInferenceMaxConcurrent),
		MaxQueuedExecutions:     positiveInt64FromEnvironment("INFERENCE_USER_MAX_QUEUED_EXECUTIONS", defaultInferenceMaxQueued),
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
		if override.MaxQueuedExecutions > 0 {
			limits.MaxQueuedExecutions = override.MaxQueuedExecutions
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
	limits := config.Default
	if override, ok := config.TenantOverrides[strings.TrimSpace(tenantID)]; ok {
		limits = override
	}
	if limits.MaxQueuedExecutions <= 0 {
		limits.MaxQueuedExecutions = defaultInferenceMaxQueued
	}
	return limits
}

// Keys (all share one hash tag so they live in the same cluster slot):
//
//	KEYS[1] usage counter for the rolling allowance window
//	KEYS[2] interactive active reservations (zset, score = expiry ms)
//	KEYS[3] interactive reservation units (hash)
//	KEYS[4] queued active reservations (zset, score = expiry ms)
//	KEYS[5] queued reservation units (hash)
//
// Every script returns {decision_or_units, used, usage_ttl, active,
// active_retry, queued, queued_retry}.
const inferenceQuotaScriptPrelude = `
local now = tonumber(ARGV[1])

local function purge(zkey, hkey)
  local expired = redis.call("ZRANGEBYSCORE", zkey, "-inf", now)
  for _, member in ipairs(expired) do redis.call("HDEL", hkey, member) end
  redis.call("ZREMRANGEBYSCORE", zkey, "-inf", now)
end

local function active_units(zkey, hkey)
  local total = 0
  local members = redis.call("ZRANGE", zkey, 0, -1)
  for _, member in ipairs(members) do
    total = total + (tonumber(redis.call("HGET", hkey, member)) or 0)
  end
  return total
end

local function earliest_retry(zkey)
  local first = redis.call("ZRANGE", zkey, 0, 0, "WITHSCORES")
  if #first < 2 then return 0 end
  return math.max(0, tonumber(first[2]) - now)
end

local function state(code)
  return {code, tonumber(redis.call("GET", KEYS[1])) or 0, redis.call("PTTL", KEYS[1]),
    active_units(KEYS[2], KEYS[3]), earliest_retry(KEYS[2]),
    active_units(KEYS[4], KEYS[5]), earliest_retry(KEYS[4])}
end

-- Removes the reservation from whichever kind holds it and returns its units.
local function take(reservation_id)
  local pairs_to_check = {{KEYS[2], KEYS[3]}, {KEYS[4], KEYS[5]}}
  for _, pair in ipairs(pairs_to_check) do
    local units = tonumber(redis.call("HGET", pair[2], reservation_id)) or 0
    if units > 0 then
      redis.call("HDEL", pair[2], reservation_id)
      redis.call("ZREM", pair[1], reservation_id)
      return units
    end
  end
  return 0
end

purge(KEYS[2], KEYS[3])
purge(KEYS[4], KEYS[5])
`

var reserveInferenceQuotaScript = redisdb.NewScript(inferenceQuotaScriptPrelude + `
local window = tonumber(ARGV[2])
local reservation_ttl = tonumber(ARGV[3])
local allowance = tonumber(ARGV[4])
local concurrent_limit = tonumber(ARGV[5])
local units = tonumber(ARGV[6])
local reservation_id = ARGV[7]
local queued_limit = tonumber(ARGV[8])
local queued = ARGV[9] == "queued"

local used = tonumber(redis.call("GET", KEYS[1])) or 0
if redis.call("PTTL", KEYS[1]) < 0 and used > 0 then
  redis.call("PEXPIRE", KEYS[1], window)
end

if redis.call("ZSCORE", KEYS[2], reservation_id) or redis.call("ZSCORE", KEYS[4], reservation_id) then
  return state(0)
end
if used + units > allowance then
  local result = state(1)
  if result[3] < 0 then result[3] = window end
  return result
end

local zkey, hkey = KEYS[2], KEYS[3]
if queued then
  zkey, hkey = KEYS[4], KEYS[5]
  if active_units(zkey, hkey) + units > queued_limit then return state(3) end
else
  if active_units(zkey, hkey) + units > concurrent_limit then return state(2) end
end

used = redis.call("INCRBY", KEYS[1], units)
if used == units then
  redis.call("PEXPIRE", KEYS[1], window)
end
redis.call("HSET", hkey, reservation_id, units)
redis.call("ZADD", zkey, now + reservation_ttl, reservation_id)
redis.call("PEXPIRE", zkey, reservation_ttl + 60000)
redis.call("PEXPIRE", hkey, reservation_ttl + 60000)
return state(0)
`)

var releaseInferenceQuotaScript = redisdb.NewScript(inferenceQuotaScriptPrelude + `
return state(take(ARGV[2]))
`)

var refundInferenceQuotaScript = redisdb.NewScript(inferenceQuotaScriptPrelude + `
local units = take(ARGV[2])
if units > 0 then
  local used = tonumber(redis.call("GET", KEYS[1])) or 0
  local next_used = math.max(0, used - units)
  if next_used == 0 then redis.call("DEL", KEYS[1]) else redis.call("SET", KEYS[1], next_used, "KEEPTTL") end
end
return state(units)
`)

var inferenceQuotaStatusScript = redisdb.NewScript(inferenceQuotaScriptPrelude + `
return state(0)
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
		limits.MaxQueuedExecutions, reservationKindArgument(data),
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
	if decision == 3 {
		ObserveInferenceQuotaEvent("rejected_queue_limit")
		return status, inferenceQuotaLimitError(apiError.InferenceQueueLimitExceeded, status, status.QueuedRetryAfter)
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
	switch data.Kind {
	case "", types.InferenceQuotaReservationInteractive, types.InferenceQuotaReservationQueued:
		return nil
	default:
		return errors.New(apiError.InvalidPayload)
	}
}

func reservationKindArgument(data types.InferenceQuotaReservation) string {
	if data.IsQueued() {
		return string(types.InferenceQuotaReservationQueued)
	}
	return string(types.InferenceQuotaReservationInteractive)
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
	return []string{
		prefix + "usage", prefix + "active", prefix + "reservation-units",
		prefix + "queued-active", prefix + "queued-reservation-units",
	}, nil
}

func parseInferenceQuotaResult(result interface{}, limits InferenceQuotaLimits) (types.InferenceQuotaStatus, int64, error) {
	values, ok := result.([]interface{})
	if !ok || len(values) != 7 {
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
	queuedRetryAfter := time.Duration(maxInt64(parsed[6], 0)) * time.Millisecond
	return types.InferenceQuotaStatus{
		Allowance: limits.Allowance, Used: used, Remaining: remaining,
		Window: limits.Window, ResetAfter: resetAfter,
		MaxConcurrentExecutions: limits.MaxConcurrentExecutions,
		ActiveExecutions:        parsed[3], ConcurrentRetryAfter: concurrentRetryAfter,
		MaxQueuedExecutions: limits.MaxQueuedExecutions,
		QueuedExecutions:    parsed[5], QueuedRetryAfter: queuedRetryAfter,
	}, parsed[0], nil
}

func inferenceQuotaLimitError(code string, status types.InferenceQuotaStatus, retryAfter time.Duration) error {
	return &apiError.InferenceQuotaLimitError{
		ErrorCode: code, Allowance: status.Allowance, Used: status.Used, Remaining: status.Remaining,
		ResetAfterSeconds:       durationSecondsCeiling(status.ResetAfter),
		MaxConcurrentExecutions: status.MaxConcurrentExecutions,
		ActiveExecutions:        status.ActiveExecutions,
		RetryAfterSeconds:       durationSecondsCeiling(retryAfter),
		MaxQueuedExecutions:     status.MaxQueuedExecutions,
		QueuedExecutions:        status.QueuedExecutions,
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
