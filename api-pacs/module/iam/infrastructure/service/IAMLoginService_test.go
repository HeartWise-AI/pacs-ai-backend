package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	cloudflareTypes "api-pacs/infrastructures/providers/api/cloudflare/types"
	identitytoolkit "api-pacs/infrastructures/providers/api/identitytoolkit"
	identitytoolkitTypes "api-pacs/infrastructures/providers/api/identitytoolkit/types"
	apiError "api-pacs/internal/errors"
	iamApplication "api-pacs/module/iam/application"
	iamEntity "api-pacs/module/iam/domain/entity"
	iamRepository "api-pacs/module/iam/domain/repository"
	iamRepositoryTypes "api-pacs/module/iam/infrastructure/repository/types"
	serviceTypes "api-pacs/module/iam/infrastructure/service/types"
	userApplication "api-pacs/module/user/application"
	userServiceTypes "api-pacs/module/user/infrastructure/service/types"
)

type loginTestProtection struct {
	iamApplication.LoginAbuseProtectionInterface
	evaluateDecision serviceTypes.LoginProtectionDecision
	recordDecision   serviceTypes.LoginProtectionDecision
	evaluateErr      error
	recordErr        error
	resetErr         error
	evaluateCalls    int
	recordCalls      int
	resetCalls       int
	lastSignals      serviceTypes.LoginAbuseSignals
}

func (protection *loginTestProtection) EvaluateLoginAttempt(_ context.Context, data serviceTypes.LoginAbuseSignals) (serviceTypes.LoginProtectionDecision, error) {
	protection.evaluateCalls++
	protection.lastSignals = data
	return protection.evaluateDecision, protection.evaluateErr
}

func (protection *loginTestProtection) RecordLoginFailure(_ context.Context, data serviceTypes.LoginAbuseSignals) (serviceTypes.LoginProtectionDecision, error) {
	protection.recordCalls++
	protection.lastSignals = data
	return protection.recordDecision, protection.recordErr
}

func (protection *loginTestProtection) ResetAccountFailures(_ context.Context, data serviceTypes.LoginAbuseSignals) error {
	protection.resetCalls++
	protection.lastSignals = data
	return protection.resetErr
}

type loginTestIdentityToolkit struct {
	identitytoolkitTypes.IdentityToolkitAPIInterface
	response identitytoolkitTypes.SignInWithPasswordResponse
	err      error
	calls    int
	request  identitytoolkitTypes.SignInWithPasswordRequest
}

func (api *loginTestIdentityToolkit) SignInWithPassword(_ context.Context, data identitytoolkitTypes.SignInWithPasswordRequest) (identitytoolkitTypes.SignInWithPasswordResponse, error) {
	api.calls++
	api.request = data
	return api.response, api.err
}

type loginTestTokenVerifier struct {
	userID string
	err    error
	calls  int
	tenant string
	token  string
}

func (verifier *loginTestTokenVerifier) VerifyTenantIDToken(_ context.Context, tenantID, idToken string) (string, error) {
	verifier.calls++
	verifier.tenant = tenantID
	verifier.token = idToken
	return verifier.userID, verifier.err
}

type loginTestTurnstile struct {
	cloudflareTypes.LoginTurnstileAPIInterface
	response cloudflareTypes.ValidateTurnstileTokenResponse
	err      error
	calls    int
	token    string
	remoteIP string
}

func (api *loginTestTurnstile) ValidateTurnstileTokenWithRemoteIP(_ context.Context, token, remoteIP string) (cloudflareTypes.ValidateTurnstileTokenResponse, error) {
	api.calls++
	api.token = token
	api.remoteIP = remoteIP
	return api.response, api.err
}

type loginTestUserQuery struct {
	userApplication.UserQueryServiceInterface
	user userServiceTypes.GetTenantUser
	err  error
}

func (query *loginTestUserQuery) GetTenantUserByID(context.Context, string, string) (userServiceTypes.GetTenantUser, error) {
	return query.user, query.err
}

type loginTestRepository struct {
	iamRepository.IAMCommandRepositoryInterface
	setErr       error
	deleteErr    error
	setCalls     int
	deleteCalls  int
	session      iamRepositoryTypes.SetTokenSession
	deletedToken string
}

func (repository *loginTestRepository) SetTokenSession(data iamRepositoryTypes.SetTokenSession) error {
	repository.setCalls++
	repository.session = data
	return repository.setErr
}

func (repository *loginTestRepository) DeleteTokenSession(token string) error {
	repository.deleteCalls++
	repository.deletedToken = token
	return repository.deleteErr
}

func newLoginTestService() (*IAMCommandService, *loginTestProtection, *loginTestIdentityToolkit, *loginTestTokenVerifier, *loginTestRepository) {
	protection := &loginTestProtection{}
	identityAPI := &loginTestIdentityToolkit{response: identitytoolkitTypes.SignInWithPasswordResponse{
		IDToken: loginTestValue("firebase", "-id", "-value"), LocalID: "user-a", Email: "user@example.com",
	}}
	verifier := &loginTestTokenVerifier{userID: "user-a"}
	repository := &loginTestRepository{}
	service := &IAMCommandService{
		IAMCommandRepositoryInterface: repository,
		UserQueryServiceInterface: &loginTestUserQuery{user: userServiceTypes.GetTenantUser{
			ID: "user-a", TenantID: "tenant-a", Email: "user@example.com", Role: iamEntity.UserRole, AccessState: "ACTIVE", IsEmailVerified: true,
		}},
		TenantIDTokenVerifierInterface: verifier,
		IdentityToolkitAPIInterface:    identityAPI,
		LoginTurnstileAPIInterface: &loginTestTurnstile{response: cloudflareTypes.ValidateTurnstileTokenResponse{
			Success: true, Action: "login", Hostname: "app.example.com",
		}},
		LoginAbuseProtectionInterface:  protection,
		LoginTurnstileAllowedHostnames: map[string]struct{}{"app.example.com": {}},
	}
	return service, protection, identityAPI, verifier, repository
}

func loginTestValue(parts ...string) string {
	return strings.Join(parts, "")
}

func testLoginInput() serviceTypes.LoginTenantUser {
	return serviceTypes.LoginTenantUser{
		TenantID: " tenant-a ", Email: " User@Example.com ", Password: loginTestValue(" private", "-passphrase "), ClientIP: "203.0.113.10",
	}
}

func requireLoginError(t *testing.T, err error, code string, challengeRequired bool) *apiError.LoginError {
	t.Helper()
	var loginError *apiError.LoginError
	require.ErrorAs(t, err, &loginError)
	require.Equal(t, code, loginError.Code)
	require.Equal(t, challengeRequired, loginError.ChallengeRequired)
	return loginError
}

func TestLoginTenantUserAuthenticatesServerSideAndCreatesOnlyPACSSession(t *testing.T) {
	service, protection, identityAPI, verifier, repository := newLoginTestService()
	turnstile := service.LoginTurnstileAPIInterface.(*loginTestTurnstile)
	input := testLoginInput()

	sessionToken, err := service.LoginTenantUser(t.Context(), input)

	require.NoError(t, err)
	require.NotEmpty(t, sessionToken)
	require.Equal(t, "tenant-a", identityAPI.request.TenantID)
	require.Equal(t, "user@example.com", identityAPI.request.Email)
	require.Equal(t, input.Password, identityAPI.request.Password, "password must not be trimmed")
	require.Equal(t, loginTestValue("firebase", "-id", "-value"), verifier.token)
	require.Equal(t, sessionToken, repository.session.SessionID)
	require.Equal(t, "user-a", repository.session.UserID)
	require.Equal(t, 1, protection.resetCalls)
	require.Equal(t, "user@example.com", protection.lastSignals.Email)
	require.Zero(t, repository.deleteCalls)
	require.Zero(t, turnstile.calls, "a normal successful login must not be challenged")
}

func TestLoginTenantUserRequiresChallengeBeforeIdentityProvider(t *testing.T) {
	service, protection, identityAPI, _, _ := newLoginTestService()
	protection.evaluateDecision.ChallengeRequired = true

	_, err := service.LoginTenantUser(t.Context(), testLoginInput())

	requireLoginError(t, err, apiError.LoginChallengeRequired, true)
	require.Zero(t, identityAPI.calls)
	require.Equal(t, 1, protection.recordCalls, "missing challenged submissions advance hard-limit counters")
}

func TestLoginTenantUserValidatesChallengeActionHostnameAndRemoteIP(t *testing.T) {
	service, protection, identityAPI, _, _ := newLoginTestService()
	protection.evaluateDecision.ChallengeRequired = true
	turnstile := service.LoginTurnstileAPIInterface.(*loginTestTurnstile)
	input := testLoginInput()
	input.TurnstileToken = loginTestValue(" turnstile", "-value ")

	_, err := service.LoginTenantUser(t.Context(), input)

	require.NoError(t, err)
	require.Equal(t, loginTestValue("turnstile", "-value"), turnstile.token)
	require.Equal(t, input.ClientIP, turnstile.remoteIP)
	require.Equal(t, 1, identityAPI.calls)
}

func TestLoginTenantUserRejectsInvalidChallengeAndCountsDeniedAttempt(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		response cloudflareTypes.ValidateTurnstileTokenResponse
	}{
		{name: "invalid or replayed", response: cloudflareTypes.ValidateTurnstileTokenResponse{Success: false, ErrorCodes: []string{"timeout-or-duplicate"}}},
		{name: "wrong action", response: cloudflareTypes.ValidateTurnstileTokenResponse{Success: true, Action: "register", Hostname: "app.example.com"}},
		{name: "wrong hostname", response: cloudflareTypes.ValidateTurnstileTokenResponse{Success: true, Action: "login", Hostname: "attacker.example"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service, protection, identityAPI, _, _ := newLoginTestService()
			protection.evaluateDecision.ChallengeRequired = true
			service.LoginTurnstileAPIInterface.(*loginTestTurnstile).response = testCase.response
			input := testLoginInput()
			input.TurnstileToken = loginTestValue("turnstile", "-value")

			_, err := service.LoginTenantUser(t.Context(), input)

			requireLoginError(t, err, apiError.TurnstileInvalid, true)
			require.Equal(t, 1, protection.recordCalls)
			require.Zero(t, identityAPI.calls)
		})
	}
}

func TestLoginTenantUserDoesNotCountTurnstileProviderFailure(t *testing.T) {
	service, protection, identityAPI, _, _ := newLoginTestService()
	protection.evaluateDecision.ChallengeRequired = true
	turnstile := service.LoginTurnstileAPIInterface.(*loginTestTurnstile)
	turnstile.response = cloudflareTypes.ValidateTurnstileTokenResponse{Success: false, ErrorCodes: []string{"internal-error"}}
	input := testLoginInput()
	input.TurnstileToken = loginTestValue("turnstile", "-value")

	_, err := service.LoginTenantUser(t.Context(), input)

	requireLoginError(t, err, apiError.CloudflareAPIError, true)
	require.Zero(t, protection.recordCalls)
	require.Zero(t, identityAPI.calls)
}

func TestLoginTenantUserReturnsRateLimitAfterDeniedChallengeCrossesHardLimit(t *testing.T) {
	service, protection, _, _, _ := newLoginTestService()
	protection.evaluateDecision.ChallengeRequired = true
	protection.recordDecision = serviceTypes.LoginProtectionDecision{ChallengeRequired: true, RetryAfter: 73 * time.Second}

	_, err := service.LoginTenantUser(t.Context(), testLoginInput())

	loginError := requireLoginError(t, err, apiError.LoginRateLimited, true)
	require.Equal(t, 73, loginError.RetryAfterSeconds)
}

func TestLoginTenantUserEnforcesExistingHardLimitBeforeProviders(t *testing.T) {
	service, protection, identityAPI, _, _ := newLoginTestService()
	protection.evaluateDecision = serviceTypes.LoginProtectionDecision{
		ChallengeRequired: true,
		RetryAfter:        41 * time.Second,
	}

	_, err := service.LoginTenantUser(t.Context(), testLoginInput())

	loginError := requireLoginError(t, err, apiError.LoginRateLimited, true)
	require.Equal(t, 41, loginError.RetryAfterSeconds)
	require.Zero(t, identityAPI.calls)
	require.Zero(t, protection.recordCalls)
}

func TestLoginRateLimitRoundsUpWithoutDurationOverflow(t *testing.T) {
	errorAtPartialSecond := newLoginRateLimitError(1501 * time.Millisecond).(*apiError.LoginError)
	errorAtMaximumDuration := newLoginRateLimitError(time.Duration(1<<63 - 1)).(*apiError.LoginError)

	require.Equal(t, 2, errorAtPartialSecond.RetryAfterSeconds)
	require.Greater(t, errorAtMaximumDuration.RetryAfterSeconds, 0)
}

func TestLoginTenantUserMapsAllCredentialRejectionsToGenericUnauthorized(t *testing.T) {
	service, protection, identityAPI, _, _ := newLoginTestService()
	identityAPI.err = identitytoolkit.ErrCredentialsRejected
	protection.recordDecision.ChallengeRequired = true

	_, err := service.LoginTenantUser(t.Context(), testLoginInput())

	requireLoginError(t, err, apiError.UnauthorizedAccess, true)
	require.Equal(t, 1, protection.recordCalls)
	require.Zero(t, protection.resetCalls)
}

func TestLoginTenantUserDoesNotCountIdentityProviderFailure(t *testing.T) {
	service, protection, identityAPI, _, _ := newLoginTestService()
	identityAPI.err = identitytoolkit.ErrProviderUnavailable

	_, err := service.LoginTenantUser(t.Context(), testLoginInput())

	requireLoginError(t, err, apiError.FirebaseAuthError, false)
	require.Zero(t, protection.recordCalls)
}

func TestLoginTenantUserRejectsProviderIdentityMismatchWithoutCreatingSession(t *testing.T) {
	service, protection, identityAPI, _, repository := newLoginTestService()
	identityAPI.response.Email = "different@example.com"

	_, err := service.LoginTenantUser(t.Context(), testLoginInput())

	requireLoginError(t, err, apiError.FirebaseAuthError, false)
	require.Zero(t, protection.recordCalls)
	require.Zero(t, repository.setCalls)
}

func TestLoginTenantUserPreservesVerifiedUserStateErrors(t *testing.T) {
	for _, testCase := range []struct {
		name string
		user userServiceTypes.GetTenantUser
		code string
	}{
		{name: "email not verified", user: userServiceTypes.GetTenantUser{ID: "user-a", AccessState: "ACTIVE"}, code: apiError.FirebaseAuthEmailNotVerified},
		{name: "suspended", user: userServiceTypes.GetTenantUser{ID: "user-a", AccessState: "SUSPENDED", IsEmailVerified: true}, code: apiError.AccountSuspended},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service, protection, _, _, repository := newLoginTestService()
			service.UserQueryServiceInterface = &loginTestUserQuery{user: testCase.user}

			_, err := service.LoginTenantUser(t.Context(), testLoginInput())

			requireLoginError(t, err, testCase.code, false)
			require.Zero(t, repository.setCalls)
			require.Zero(t, protection.resetCalls)
		})
	}
}

func TestLoginTenantUserRollsBackSessionWhenAccountResetFails(t *testing.T) {
	service, protection, _, _, repository := newLoginTestService()
	protection.resetErr = errors.New("redis unavailable")

	_, err := service.LoginTenantUser(t.Context(), testLoginInput())

	requireLoginError(t, err, apiError.LoginProtectionUnavailable, false)
	require.Equal(t, 1, repository.setCalls)
	require.Equal(t, 1, repository.deleteCalls)
	require.Equal(t, repository.session.SessionID, repository.deletedToken)
}
