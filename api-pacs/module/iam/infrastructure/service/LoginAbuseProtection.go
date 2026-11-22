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
	"sync"
	"time"

	redisTypes "api-pacs/infrastructures/database/redis/types"
	apiError "api-pacs/internal/errors"
	serviceTypes "api-pacs/module/iam/infrastructure/service/types"
)

const (
	defaultLoginFailureWindow            = 10 * time.Minute
	defaultLoginAccountChallengeFailures = 3
	defaultLoginIPChallengeFailures      = 5
	defaultLoginIPMaxFailures            = 30
	defaultLoginTenantChallengeFailures  = 50
	defaultLoginTenantMaxFailures        = 500

	// minimumLoginCooldown keeps a cooldown answer from inviting an immediate
	// retry when the triggering counter is about to expire.
	minimumLoginCooldown = 30 * time.Second
)

// LoginChallengeMode selects what the gateway does when the adaptive policy
// would ask for a human-verification challenge.
type LoginChallengeMode string

const (
	// LoginChallengeModeTurnstile requires a Cloudflare Turnstile token.
	LoginChallengeModeTurnstile LoginChallengeMode = "turnstile"
	// LoginChallengeModeCooldown answers with a temporary rate limit (429 and
	// Retry-After) until the counter that triggered the challenge expires. It is
	// meant for deployments where Turnstile cannot work (internal hostnames,
	// workstations that cannot reach challenges.cloudflare.com).
	LoginChallengeModeCooldown LoginChallengeMode = "cooldown"
)

var loginChallengeModeLogOnce sync.Once

// LoginChallengeModeFromEnvironment resolves LOGIN_CHALLENGE_MODE and logs the
// effective mode once per process.
func LoginChallengeModeFromEnvironment() LoginChallengeMode {
	mode, source := resolveLoginChallengeMode(os.Getenv)
	loginChallengeModeLogOnce.Do(func() {
		log.Printf("[security] event=login_challenge_mode mode=%s source=%s", mode, source)
	})
	return mode
}

// resolveLoginChallengeMode returns the explicit LOGIN_CHALLENGE_MODE when it is
// valid; otherwise Turnstile when both its server secret and allowed hostnames
// are configured, and cooldown when they are not.
func resolveLoginChallengeMode(getenv func(string) string) (LoginChallengeMode, string) {
	switch LoginChallengeMode(strings.ToLower(strings.TrimSpace(getenv("LOGIN_CHALLENGE_MODE")))) {
	case LoginChallengeModeTurnstile:
		return LoginChallengeModeTurnstile, "setting"
	case LoginChallengeModeCooldown:
		return LoginChallengeModeCooldown, "setting"
	case "":
	default:
		log.Printf("[security] event=login_abuse_protection_config_invalid setting=LOGIN_CHALLENGE_MODE")
	}
	turnstileConfigured := strings.TrimSpace(getenv("CLOUDFLARE_SECRET_KEY")) != "" &&
		len(LoginTurnstileAllowedHostnamesFromEnvironment(getenv("LOGIN_TURNSTILE_ALLOWED_HOSTNAMES"))) > 0
	if turnstileConfigured {
		return LoginChallengeModeTurnstile, "default"
	}
	return LoginChallengeModeCooldown, "default"
}

type LoginAbuseProtectionConfig struct {
	Enabled                  bool
	Window                   time.Duration
	AccountChallengeFailures int64
	IPChallengeFailures      int64
	IPMaxFailures            int64
	TenantChallengeFailures  int64
	TenantMaxFailures        int64
}

type RedisLoginAbuseProtection struct {
	RedisDBHandlerInterface redisTypes.RedisDBHandlerInterface
	Config                  LoginAbuseProtectionConfig
}

type loginFailureScope struct {
	Name               string
	Value              string
	ChallengeThreshold int64
	MaximumFailures    int64
}

func LoginAbuseProtectionConfigFromEnvironment() LoginAbuseProtectionConfig {
	config := LoginAbuseProtectionConfig{
		Enabled: loginProtectionEnabledFromEnvironment(),
		Window:  loginFailureWindowFromEnvironment(),
		AccountChallengeFailures: positiveLoginEnvironmentInteger(
			"LOGIN_ACCOUNT_CHALLENGE_FAILURES",
			defaultLoginAccountChallengeFailures,
		),
		IPChallengeFailures: positiveLoginEnvironmentInteger(
			"LOGIN_IP_CHALLENGE_FAILURES",
			defaultLoginIPChallengeFailures,
		),
		IPMaxFailures: positiveLoginEnvironmentInteger(
			"LOGIN_IP_MAX_FAILURES",
			defaultLoginIPMaxFailures,
		),
		TenantChallengeFailures: positiveLoginEnvironmentInteger(
			"LOGIN_TENANT_CHALLENGE_FAILURES",
			defaultLoginTenantChallengeFailures,
		),
		TenantMaxFailures: positiveLoginEnvironmentInteger(
			"LOGIN_TENANT_MAX_FAILURES",
			defaultLoginTenantMaxFailures,
		),
	}
	if config.IPChallengeFailures > config.IPMaxFailures {
		log.Printf("[security] event=login_abuse_protection_config_invalid settings=LOGIN_IP_CHALLENGE_FAILURES,LOGIN_IP_MAX_FAILURES")
		config.IPChallengeFailures = defaultLoginIPChallengeFailures
		config.IPMaxFailures = defaultLoginIPMaxFailures
	}
	if config.TenantChallengeFailures > config.TenantMaxFailures {
		log.Printf("[security] event=login_abuse_protection_config_invalid settings=LOGIN_TENANT_CHALLENGE_FAILURES,LOGIN_TENANT_MAX_FAILURES")
		config.TenantChallengeFailures = defaultLoginTenantChallengeFailures
		config.TenantMaxFailures = defaultLoginTenantMaxFailures
	}
	if !config.Enabled {
		log.Printf("[security] event=login_abuse_protection_disabled")
	}
	return config
}

func loginFailureWindowFromEnvironment() time.Duration {
	seconds := positiveLoginEnvironmentInteger(
		"LOGIN_FAILURE_WINDOW_SECONDS",
		int64(defaultLoginFailureWindow/time.Second),
	)
	const maximumDurationSeconds = int64((1<<63 - 1) / time.Second)
	if seconds > maximumDurationSeconds {
		log.Printf("[security] event=login_abuse_protection_config_invalid setting=LOGIN_FAILURE_WINDOW_SECONDS")
		return defaultLoginFailureWindow
	}
	return time.Duration(seconds) * time.Second
}

func loginProtectionEnabledFromEnvironment() bool {
	value := strings.TrimSpace(os.Getenv("LOGIN_ABUSE_PROTECTION_ENABLED"))
	if value == "" {
		return true
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		log.Printf("[security] event=login_abuse_protection_config_invalid setting=LOGIN_ABUSE_PROTECTION_ENABLED")
		return true
	}
	return enabled
}

func positiveLoginEnvironmentInteger(name string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		log.Printf("[security] event=login_abuse_protection_config_invalid setting=%s", name)
		return fallback
	}
	return parsed
}

func (protection *RedisLoginAbuseProtection) EvaluateLoginAttempt(
	ctx context.Context,
	data serviceTypes.LoginAbuseSignals,
) (serviceTypes.LoginProtectionDecision, error) {
	if err := ctx.Err(); err != nil {
		return serviceTypes.LoginProtectionDecision{}, err
	}
	config := protection.resolvedConfig()
	if !config.Enabled {
		return serviceTypes.LoginProtectionDecision{}, nil
	}
	if protection.RedisDBHandlerInterface == nil {
		return serviceTypes.LoginProtectionDecision{}, fmt.Errorf("%s: login protection is not configured", apiError.LoginProtectionUnavailable)
	}

	decision := serviceTypes.LoginProtectionDecision{}
	for _, scope := range loginFailureScopes(config, data) {
		count, ttl, err := protection.RedisDBHandlerInterface.GetCounterWithExpiry(
			loginFailureKey(scope.Name, data.TenantID, scope.Value),
		)
		if err != nil {
			log.Printf("[security] event=login_protection_unavailable operation=evaluate scope=%s", scope.Name)
			return serviceTypes.LoginProtectionDecision{}, fmt.Errorf("%s: %w", apiError.LoginProtectionUnavailable, err)
		}
		applyLoginFailureScope(&decision, scope, count, ttl, config.Window)
	}
	return decision, nil
}

func (protection *RedisLoginAbuseProtection) RecordLoginFailure(
	ctx context.Context,
	data serviceTypes.LoginAbuseSignals,
) (serviceTypes.LoginProtectionDecision, error) {
	if err := ctx.Err(); err != nil {
		return serviceTypes.LoginProtectionDecision{}, err
	}
	config := protection.resolvedConfig()
	if !config.Enabled {
		return serviceTypes.LoginProtectionDecision{}, nil
	}
	if protection.RedisDBHandlerInterface == nil {
		return serviceTypes.LoginProtectionDecision{}, fmt.Errorf("%s: login protection is not configured", apiError.LoginProtectionUnavailable)
	}

	decision := serviceTypes.LoginProtectionDecision{}
	for _, scope := range loginFailureScopes(config, data) {
		count, ttl, err := protection.RedisDBHandlerInterface.IncrementWithExpiry(
			loginFailureKey(scope.Name, data.TenantID, scope.Value),
			config.Window,
		)
		if err != nil {
			log.Printf("[security] event=login_protection_unavailable operation=record scope=%s", scope.Name)
			return serviceTypes.LoginProtectionDecision{}, fmt.Errorf("%s: %w", apiError.LoginProtectionUnavailable, err)
		}
		applyLoginFailureScope(&decision, scope, count, ttl, config.Window)
	}
	return decision, nil
}

func (protection *RedisLoginAbuseProtection) ResetAccountFailures(
	ctx context.Context,
	data serviceTypes.LoginAbuseSignals,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	config := protection.resolvedConfig()
	if !config.Enabled {
		return nil
	}
	if protection.RedisDBHandlerInterface == nil {
		return fmt.Errorf("%s: login protection is not configured", apiError.LoginProtectionUnavailable)
	}
	if err := protection.RedisDBHandlerInterface.Delete(loginFailureKey("account", data.TenantID, normalizedLoginEmail(data.Email))); err != nil {
		log.Printf("[security] event=login_protection_unavailable operation=reset scope=account")
		return fmt.Errorf("%s: %w", apiError.LoginProtectionUnavailable, err)
	}
	return nil
}

func (protection *RedisLoginAbuseProtection) resolvedConfig() LoginAbuseProtectionConfig {
	config := protection.Config
	if config.Window <= 0 || config.AccountChallengeFailures <= 0 || config.IPChallengeFailures <= 0 ||
		config.IPMaxFailures <= 0 || config.TenantChallengeFailures <= 0 || config.TenantMaxFailures <= 0 {
		return LoginAbuseProtectionConfigFromEnvironment()
	}
	return config
}

func loginFailureScopes(config LoginAbuseProtectionConfig, data serviceTypes.LoginAbuseSignals) []loginFailureScope {
	return []loginFailureScope{
		{
			Name:               "tenant",
			Value:              strings.TrimSpace(data.TenantID),
			ChallengeThreshold: config.TenantChallengeFailures,
			MaximumFailures:    config.TenantMaxFailures,
		},
		{
			Name:               "account",
			Value:              normalizedLoginEmail(data.Email),
			ChallengeThreshold: config.AccountChallengeFailures,
			MaximumFailures:    0,
		},
		{
			Name:               "ip",
			Value:              strings.TrimSpace(data.ClientIP),
			ChallengeThreshold: config.IPChallengeFailures,
			MaximumFailures:    config.IPMaxFailures,
		},
	}
}

func applyLoginFailureScope(
	decision *serviceTypes.LoginProtectionDecision,
	scope loginFailureScope,
	count int64,
	ttl time.Duration,
	fallbackTTL time.Duration,
) {
	if count >= scope.ChallengeThreshold {
		decision.ChallengeRequired = true
		if cooldown := loginCooldownFromTTL(ttl, fallbackTTL); cooldown > decision.ChallengeRetryAfter {
			decision.ChallengeRetryAfter = cooldown
		}
	}
	if scope.MaximumFailures > 0 && count >= scope.MaximumFailures {
		if ttl <= 0 {
			ttl = fallbackTTL
		}
		if ttl > decision.RetryAfter {
			decision.RetryAfter = ttl
		}
	}
}

// loginCooldownFromTTL turns the remaining TTL of a counter that requires a
// challenge into the wait advertised in cooldown mode: at least
// minimumLoginCooldown and at most the failure window.
func loginCooldownFromTTL(ttl, window time.Duration) time.Duration {
	if ttl <= 0 || ttl > window {
		ttl = window
	}
	if ttl < minimumLoginCooldown {
		ttl = minimumLoginCooldown
	}
	if window > 0 && ttl > window {
		ttl = window
	}
	return ttl
}

func loginFailureKey(scope, tenantID, value string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(tenantID) + "\x00" + strings.TrimSpace(value)))
	return "pacs-ai:login-failures:" + scope + ":" + hex.EncodeToString(digest[:])
}

func normalizedLoginEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
