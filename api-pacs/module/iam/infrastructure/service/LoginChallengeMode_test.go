package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	identitytoolkit "api-pacs/infrastructures/providers/api/identitytoolkit"
	apiError "api-pacs/internal/errors"
	serviceTypes "api-pacs/module/iam/infrastructure/service/types"
)

func TestResolveLoginChallengeModeFromEnvironment(t *testing.T) {
	secret := loginTestValue("configured", "-secret")
	for _, testCase := range []struct {
		name   string
		env    map[string]string
		mode   LoginChallengeMode
		source string
	}{
		{name: "turnstile fully configured", env: map[string]string{"CLOUDFLARE_SECRET_KEY": secret, "LOGIN_TURNSTILE_ALLOWED_HOSTNAMES": "app.example.com"}, mode: LoginChallengeModeTurnstile, source: "default"},
		{name: "no secret", env: map[string]string{"LOGIN_TURNSTILE_ALLOWED_HOSTNAMES": "app.example.com"}, mode: LoginChallengeModeCooldown, source: "default"},
		{name: "no hostnames", env: map[string]string{"CLOUDFLARE_SECRET_KEY": secret, "LOGIN_TURNSTILE_ALLOWED_HOSTNAMES": " , "}, mode: LoginChallengeModeCooldown, source: "default"},
		{name: "nothing configured", env: map[string]string{}, mode: LoginChallengeModeCooldown, source: "default"},
		{name: "explicit cooldown overrides configured turnstile", env: map[string]string{"LOGIN_CHALLENGE_MODE": " Cooldown ", "CLOUDFLARE_SECRET_KEY": secret, "LOGIN_TURNSTILE_ALLOWED_HOSTNAMES": "app.example.com"}, mode: LoginChallengeModeCooldown, source: "setting"},
		{name: "explicit turnstile", env: map[string]string{"LOGIN_CHALLENGE_MODE": "turnstile"}, mode: LoginChallengeModeTurnstile, source: "setting"},
		{name: "invalid value falls back to default", env: map[string]string{"LOGIN_CHALLENGE_MODE": "captcha", "CLOUDFLARE_SECRET_KEY": secret, "LOGIN_TURNSTILE_ALLOWED_HOSTNAMES": "app.example.com"}, mode: LoginChallengeModeTurnstile, source: "default"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mode, source := resolveLoginChallengeMode(func(name string) string { return testCase.env[name] })
			require.Equal(t, testCase.mode, mode)
			require.Equal(t, testCase.source, source)
		})
	}
}

func TestLoginChallengeModeFromEnvironmentReadsProcessEnvironment(t *testing.T) {
	t.Setenv("LOGIN_CHALLENGE_MODE", "")
	t.Setenv("CLOUDFLARE_SECRET_KEY", "")
	t.Setenv("LOGIN_TURNSTILE_ALLOWED_HOSTNAMES", "app.example.com")
	require.Equal(t, LoginChallengeModeCooldown, LoginChallengeModeFromEnvironment())

	t.Setenv("LOGIN_CHALLENGE_MODE", "turnstile")
	require.Equal(t, LoginChallengeModeTurnstile, LoginChallengeModeFromEnvironment())
}

func TestLoginCooldownFromTTLIsBoundedByMinimumAndWindow(t *testing.T) {
	window := 10 * time.Minute
	require.Equal(t, 580*time.Second, loginCooldownFromTTL(580*time.Second, window))
	require.Equal(t, 30*time.Second, loginCooldownFromTTL(4*time.Second, window))
	require.Equal(t, window, loginCooldownFromTTL(0, window), "a counter without a TTL waits the full window")
	require.Equal(t, window, loginCooldownFromTTL(time.Hour, window))
	require.Equal(t, 20*time.Second, loginCooldownFromTTL(5*time.Second, 20*time.Second), "a window shorter than the minimum wins")
}

func TestLoginProtectionReportsLongestChallengeTTL(t *testing.T) {
	config := testLoginProtectionConfig()
	signals := serviceTypes.LoginAbuseSignals{TenantID: "tenant-a", Email: "user@example.com", ClientIP: "203.0.113.1"}
	redis := &loginProtectionRedis{
		counts: map[string]int64{
			loginFailureKey("account", signals.TenantID, signals.Email):   config.AccountChallengeFailures,
			loginFailureKey("ip", signals.TenantID, signals.ClientIP):     config.IPChallengeFailures,
			loginFailureKey("tenant", signals.TenantID, signals.TenantID): 1,
		},
		ttls: map[string]time.Duration{
			loginFailureKey("account", signals.TenantID, signals.Email):   580 * time.Second,
			loginFailureKey("ip", signals.TenantID, signals.ClientIP):     120 * time.Second,
			loginFailureKey("tenant", signals.TenantID, signals.TenantID): 599 * time.Second,
		},
	}
	protection := RedisLoginAbuseProtection{RedisDBHandlerInterface: redis, Config: config}

	decision, err := protection.EvaluateLoginAttempt(t.Context(), signals)

	require.NoError(t, err)
	require.True(t, decision.ChallengeRequired)
	require.Zero(t, decision.RetryAfter, "challenge thresholds are not hard limits")
	require.Equal(t, 580*time.Second, decision.ChallengeRetryAfter, "the tenant counter is below its threshold and must not count")
}

func newCooldownLoginTestService() (*IAMCommandService, *loginTestProtection, *loginTestIdentityToolkit) {
	service, protection, identityAPI, _, _ := newLoginTestService()
	service.LoginChallengeMode = LoginChallengeModeCooldown
	return service, protection, identityAPI
}

func TestCooldownModeAnswersChallengeWithRateLimitBeforeProviders(t *testing.T) {
	service, protection, identityAPI := newCooldownLoginTestService()
	turnstile := service.LoginTurnstileAPIInterface.(*loginTestTurnstile)
	protection.evaluateDecision = serviceTypes.LoginProtectionDecision{ChallengeRequired: true, ChallengeRetryAfter: 300 * time.Second}
	protection.recordDecision = serviceTypes.LoginProtectionDecision{ChallengeRequired: true, ChallengeRetryAfter: 299500 * time.Millisecond}
	input := testLoginInput()
	input.TurnstileToken = loginTestValue("turnstile", "-value")

	_, err := service.LoginTenantUser(t.Context(), input)

	loginError := requireLoginError(t, err, apiError.LoginRateLimited, true)
	require.Equal(t, 300, loginError.RetryAfterSeconds, "Retry-After comes from the recorded counter TTL, rounded up")
	require.Zero(t, identityAPI.calls, "a cooled-down account must not reach the identity provider")
	require.Zero(t, turnstile.calls, "a token never replaces the cooldown")
	require.Equal(t, 1, protection.recordCalls, "denied attempts still advance the hard-limit counters")
}

func TestCooldownModeTurnsTheThresholdFailureIntoRateLimit(t *testing.T) {
	service, protection, identityAPI := newCooldownLoginTestService()
	identityAPI.err = identitytoolkit.ErrCredentialsRejected
	protection.recordDecision = serviceTypes.LoginProtectionDecision{ChallengeRequired: true, ChallengeRetryAfter: 580 * time.Second}

	_, err := service.LoginTenantUser(t.Context(), testLoginInput())

	loginError := requireLoginError(t, err, apiError.LoginRateLimited, true)
	require.Equal(t, 580, loginError.RetryAfterSeconds)
}

func TestCooldownModeKeepsGenericRejectionBelowThreshold(t *testing.T) {
	service, protection, identityAPI := newCooldownLoginTestService()
	identityAPI.err = identitytoolkit.ErrCredentialsRejected

	_, err := service.LoginTenantUser(t.Context(), testLoginInput())

	requireLoginError(t, err, apiError.UnauthorizedAccess, false)
	require.Equal(t, 1, protection.recordCalls)
}

func TestCooldownModeKeepsHardLimitRetryAfter(t *testing.T) {
	service, protection, _ := newCooldownLoginTestService()
	protection.evaluateDecision = serviceTypes.LoginProtectionDecision{ChallengeRequired: true, RetryAfter: 41 * time.Second, ChallengeRetryAfter: 500 * time.Second}

	_, err := service.LoginTenantUser(t.Context(), testLoginInput())

	loginError := requireLoginError(t, err, apiError.LoginRateLimited, true)
	require.Equal(t, 41, loginError.RetryAfterSeconds)
	require.Zero(t, protection.recordCalls)
}

func TestCooldownModeUsesMinimumWhenDecisionHasNoTTL(t *testing.T) {
	service, protection, _ := newCooldownLoginTestService()
	protection.evaluateDecision.ChallengeRequired = true
	protection.recordDecision.ChallengeRequired = true

	_, err := service.LoginTenantUser(t.Context(), testLoginInput())

	loginError := requireLoginError(t, err, apiError.LoginRateLimited, true)
	require.Equal(t, 30, loginError.RetryAfterSeconds)
}

func TestTurnstileModeIsUnchangedByCooldownTTL(t *testing.T) {
	for _, mode := range []LoginChallengeMode{"", LoginChallengeModeTurnstile} {
		t.Run(string(mode)+"-mode", func(t *testing.T) {
			service, protection, identityAPI, _, _ := newLoginTestService()
			service.LoginChallengeMode = mode
			protection.evaluateDecision = serviceTypes.LoginProtectionDecision{ChallengeRequired: true, ChallengeRetryAfter: 300 * time.Second}
			protection.recordDecision = serviceTypes.LoginProtectionDecision{ChallengeRequired: true, ChallengeRetryAfter: 300 * time.Second}

			_, err := service.LoginTenantUser(t.Context(), testLoginInput())
			loginError := requireLoginError(t, err, apiError.LoginChallengeRequired, true)
			require.Zero(t, loginError.RetryAfterSeconds)
			require.Zero(t, identityAPI.calls)

			input := testLoginInput()
			input.TurnstileToken = loginTestValue("turnstile", "-value")
			identityAPI.err = identitytoolkit.ErrCredentialsRejected
			_, err = service.LoginTenantUser(t.Context(), input)
			requireLoginError(t, err, apiError.UnauthorizedAccess, true)
			require.Equal(t, 1, identityAPI.calls, "a valid token still reaches the identity provider")
		})
	}
}

// The account counter is keyed by the normalized e-mail whether or not the
// account exists, and Identity Platform rejects unknown e-mails and wrong
// passwords alike, so an unknown address must walk the exact same sequence of
// answers as a real one.
func TestCooldownModeGivesUnknownAccountTheSameAnswers(t *testing.T) {
	run := func(email string) []apiError.LoginError {
		service, _, identityAPI, _, _ := newLoginTestService()
		service.LoginChallengeMode = LoginChallengeModeCooldown
		service.LoginAbuseProtectionInterface = &RedisLoginAbuseProtection{
			RedisDBHandlerInterface: &loginProtectionRedis{},
			Config:                  testLoginProtectionConfig(),
		}
		identityAPI.err = identitytoolkit.ErrCredentialsRejected
		answers := make([]apiError.LoginError, 0, 5)
		for attempt := 0; attempt < 5; attempt++ {
			input := testLoginInput()
			input.Email = email
			input.ClientIP = "203.0.113.10"
			_, err := service.LoginTenantUser(t.Context(), input)
			var loginError *apiError.LoginError
			require.ErrorAs(t, err, &loginError)
			answers = append(answers, *loginError)
		}
		return answers
	}

	known := run("user@example.com")
	unknown := run("nobody@example.com")

	require.Equal(t, known, unknown)
	require.Equal(t, apiError.UnauthorizedAccess, known[0].Code)
	require.Equal(t, apiError.UnauthorizedAccess, known[1].Code)
	require.Equal(t, apiError.LoginRateLimited, known[2].Code, "the third failure starts the cooldown")
	require.Equal(t, 600, known[2].RetryAfterSeconds, "fresh counter: the whole window remains")
	require.Equal(t, apiError.LoginRateLimited, known[3].Code)
	require.Equal(t, apiError.LoginRateLimited, known[4].Code)
}
