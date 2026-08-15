package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"api-pacs/interfaces/http/rest/clientip"
	apiError "api-pacs/internal/errors"
	iamApplication "api-pacs/module/iam/application"
	serviceTypes "api-pacs/module/iam/infrastructure/service/types"
)

type loginControllerService struct {
	iamApplication.IAMCommandServiceInterface
	sessionToken string
	err          error
	calls        int
	request      serviceTypes.LoginTenantUser
}

func (service *loginControllerService) LoginTenantUser(_ context.Context, data serviceTypes.LoginTenantUser) (string, error) {
	service.calls++
	service.request = data
	return service.sessionToken, service.err
}

type loginControllerResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	ErrorCode string `json:"errorCode"`
	Data      struct {
		SessionToken      string `json:"sessionToken"`
		ChallengeRequired bool   `json:"challengeRequired"`
	} `json:"data"`
}

func performLoginRequest(t *testing.T, controller *IAMCommandController, body string) (*httptest.ResponseRecorder, loginControllerResponse) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/iam/login", strings.NewReader(body))
	request.RemoteAddr = "172.20.0.5:4321"
	request.Header.Set("X-Real-IP", "203.0.113.25")
	recorder := httptest.NewRecorder()
	controller.LoginTenantUser(recorder, request)
	var response loginControllerResponse
	require.NoError(t, json.NewDecoder(recorder.Body).Decode(&response))
	return recorder, response
}

func loginRequestBody(t *testing.T, tenantID, email, password, turnstileToken string) string {
	t.Helper()
	payload, err := json.Marshal(struct {
		TenantID       string `json:"tenantId"`
		Email          string `json:"email"`
		Password       string `json:"password"`
		TurnstileToken string `json:"turnstileToken,omitempty"`
	}{
		TenantID: tenantID, Email: email, Password: password, TurnstileToken: turnstileToken,
	})
	require.NoError(t, err)
	return string(payload)
}

func loginControllerValue(parts ...string) string {
	return strings.Join(parts, "")
}

func TestLoginControllerNormalizesInputAndUsesTrustedClientIP(t *testing.T) {
	trustedNetworks, err := clientip.ParseTrustedProxyCIDRs("172.16.0.0/12")
	require.NoError(t, err)
	sessionToken := loginControllerValue("pacs", "-session", "-value")
	password := loginControllerValue(" private", "-passphrase ")
	turnstileToken := loginControllerValue(" turnstile", "-value ")
	service := &loginControllerService{sessionToken: sessionToken}
	controller := &IAMCommandController{IAMCommandServiceInterface: service, TrustedProxyCIDRs: trustedNetworks}

	recorder, response := performLoginRequest(t, controller, loginRequestBody(t, " tenant-a ", " User@Example.com ", password, turnstileToken))

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.True(t, response.Success)
	require.Equal(t, sessionToken, response.Data.SessionToken)
	require.Equal(t, "tenant-a", service.request.TenantID)
	require.Equal(t, "user@example.com", service.request.Email)
	require.Equal(t, password, service.request.Password)
	require.Equal(t, loginControllerValue("turnstile", "-value"), service.request.TurnstileToken)
	require.Equal(t, "203.0.113.25", service.request.ClientIP)
}

func TestLoginControllerIgnoresSpoofedClientIPFromUntrustedPeer(t *testing.T) {
	service := &loginControllerService{sessionToken: loginControllerValue("pacs", "-session", "-value")}
	controller := &IAMCommandController{IAMCommandServiceInterface: service}

	_, _ = performLoginRequest(t, controller, loginRequestBody(t, "tenant-a", "user@example.com", loginControllerValue("valid", "-passphrase"), ""))

	require.Equal(t, "172.20.0.5", service.request.ClientIP)
}

func TestLoginControllerReturnsStableAdaptiveErrors(t *testing.T) {
	for _, testCase := range []struct {
		name              string
		err               error
		status            int
		code              string
		challengeRequired bool
		retryAfter        string
	}{
		{name: "generic authentication", err: &apiError.LoginError{Code: apiError.UnauthorizedAccess}, status: http.StatusUnauthorized, code: apiError.UnauthorizedAccess},
		{name: "challenge required", err: &apiError.LoginError{Code: apiError.LoginChallengeRequired, ChallengeRequired: true}, status: http.StatusForbidden, code: apiError.LoginChallengeRequired, challengeRequired: true},
		{name: "invalid challenge", err: &apiError.LoginError{Code: apiError.TurnstileInvalid, ChallengeRequired: true}, status: http.StatusForbidden, code: apiError.TurnstileInvalid, challengeRequired: true},
		{name: "hard throttle", err: &apiError.LoginError{Code: apiError.LoginRateLimited, ChallengeRequired: true, RetryAfterSeconds: 73}, status: http.StatusTooManyRequests, code: apiError.LoginRateLimited, challengeRequired: true, retryAfter: "73"},
		{name: "provider unavailable", err: &apiError.LoginError{Code: apiError.FirebaseAuthError, ChallengeRequired: true}, status: http.StatusServiceUnavailable, code: apiError.FirebaseAuthError, challengeRequired: true},
		{name: "protection unavailable", err: &apiError.LoginError{Code: apiError.LoginProtectionUnavailable}, status: http.StatusServiceUnavailable, code: apiError.LoginProtectionUnavailable},
		{name: "verified suspended account", err: &apiError.LoginError{Code: apiError.AccountSuspended}, status: http.StatusForbidden, code: apiError.AccountSuspended},
		{name: "verified unconfirmed email", err: &apiError.LoginError{Code: apiError.FirebaseAuthEmailNotVerified}, status: http.StatusUnauthorized, code: apiError.FirebaseAuthEmailNotVerified},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := &loginControllerService{err: testCase.err}
			controller := &IAMCommandController{IAMCommandServiceInterface: service}

			recorder, response := performLoginRequest(t, controller, loginRequestBody(t, "tenant-a", "user@example.com", loginControllerValue("valid", "-passphrase"), ""))

			require.Equal(t, testCase.status, recorder.Code)
			require.Equal(t, testCase.code, response.ErrorCode)
			require.Equal(t, testCase.challengeRequired, response.Data.ChallengeRequired)
			require.Equal(t, testCase.retryAfter, recorder.Header().Get("Retry-After"))
		})
	}
}

func TestLoginControllerValidationErrorsIncludeChallengeState(t *testing.T) {
	service := &loginControllerService{err: errors.New("must not be called")}
	controller := &IAMCommandController{IAMCommandServiceInterface: service}

	recorder, response := performLoginRequest(t, controller, loginRequestBody(t, "   ", "not-an-email", "", ""))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.False(t, response.Data.ChallengeRequired)
	require.Zero(t, service.calls)
}
