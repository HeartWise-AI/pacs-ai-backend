package identitytoolkit

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"api-pacs/infrastructures/providers/api/identitytoolkit/types"
)

func TestSignInWithPasswordUsesTenantCredentialsAndReturnsToken(t *testing.T) {
	apiKey := strings.Join([]string{"test", "-api", "-key"}, "")
	password := strings.Join([]string{"private", "-passphrase"}, "")
	idToken := strings.Join([]string{"firebase", "-id", "-value"}, "")
	refreshToken := strings.Join([]string{"firebase", "-refresh", "-value"}, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodPost, request.Method)
		require.Equal(t, "/v1/accounts:signInWithPassword", request.URL.Path)
		require.Equal(t, apiKey, request.URL.Query().Get("key"))
		var body struct {
			TenantID          string `json:"tenantId"`
			Email             string `json:"email"`
			Password          string `json:"password"`
			ReturnSecureToken bool   `json:"returnSecureToken"`
		}
		require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
		require.Equal(t, "tenant-a", body.TenantID)
		require.Equal(t, "user@example.com", body.Email)
		require.Equal(t, password, body.Password)
		require.True(t, body.ReturnSecureToken)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"idToken": idToken, "refreshToken": refreshToken, "localId": "user-a", "email": "user@example.com",
		})
	}))
	defer server.Close()
	api := &IdentityToolkitAPI{APIKey: apiKey, BaseURL: server.URL + "/v1", Client: server.Client()}

	response, err := api.SignInWithPassword(t.Context(), types.SignInWithPasswordRequest{
		TenantID: "tenant-a", Email: "user@example.com", Password: password,
	})

	require.NoError(t, err)
	require.Equal(t, idToken, response.IDToken)
	require.Equal(t, "user-a", response.LocalID)
}

func TestSignInWithPasswordClassifiesOnlyCredentialRejections(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		message     string
		expectedErr error
	}{
		{name: "unknown account", message: "EMAIL_NOT_FOUND", expectedErr: ErrCredentialsRejected},
		{name: "wrong password", message: "INVALID_PASSWORD", expectedErr: ErrCredentialsRejected},
		{name: "enumeration protected", message: "INVALID_LOGIN_CREDENTIALS", expectedErr: ErrCredentialsRejected},
		{name: "disabled", message: "USER_DISABLED", expectedErr: ErrCredentialsRejected},
		{name: "bad API key", message: "API_KEY_INVALID", expectedErr: ErrProviderUnavailable},
		{name: "unknown tenant", message: "INVALID_TENANT_ID", expectedErr: ErrProviderUnavailable},
		{name: "password sign-in disabled", message: "PASSWORD_LOGIN_DISABLED", expectedErr: ErrProviderUnavailable},
		{name: "lockout", message: "TOO_MANY_ATTEMPTS_TRY_LATER", expectedErr: ErrTooManyAttempts},
		{
			name:        "lockout with details",
			message:     "TOO_MANY_ATTEMPTS_TRY_LATER : Access to this account has been temporarily disabled due to many failed login attempts.",
			expectedErr: ErrTooManyAttempts,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			apiKey := strings.Join([]string{"test", "-api", "-key"}, "")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]string{"message": testCase.message}})
			}))
			defer server.Close()
			api := &IdentityToolkitAPI{APIKey: apiKey, BaseURL: server.URL, Client: server.Client()}

			_, err := api.SignInWithPassword(t.Context(), types.SignInWithPasswordRequest{})

			require.ErrorIs(t, err, testCase.expectedErr)
			require.NotContains(t, err.Error(), testCase.message)
		})
	}
}

func TestSignInWithPasswordRejectsIncompleteSuccessResponse(t *testing.T) {
	apiKey := strings.Join([]string{"test", "-api", "-key"}, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"idToken": strings.Join([]string{"firebase", "-id", "-value"}, ""),
			"localId": "user-a",
		})
	}))
	defer server.Close()
	api := &IdentityToolkitAPI{APIKey: apiKey, BaseURL: server.URL, Client: server.Client()}

	_, err := api.SignInWithPassword(t.Context(), types.SignInWithPasswordRequest{})

	require.ErrorIs(t, err, ErrProviderUnavailable)
}

func TestSignInWithPasswordRequiresConfigurationAndBoundedClient(t *testing.T) {
	_, err := (&IdentityToolkitAPI{}).SignInWithPassword(t.Context(), types.SignInWithPasswordRequest{})
	require.ErrorIs(t, err, ErrProviderUnavailable)

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()
	apiKey := strings.Join([]string{"test", "-api", "-key"}, "")
	api := &IdentityToolkitAPI{APIKey: apiKey, BaseURL: server.URL, Client: &http.Client{Timeout: 10 * time.Millisecond}}

	_, err = api.SignInWithPassword(t.Context(), types.SignInWithPasswordRequest{})
	require.ErrorIs(t, err, ErrProviderUnavailable)
}

func TestSignInWithPasswordReportsOnlyTheProviderCodeAsReason(t *testing.T) {
	for _, testCase := range []struct {
		message        string
		expectedErr    error
		expectedReason string
	}{
		{
			message:        "TOO_MANY_ATTEMPTS_TRY_LATER : Access to this account has been temporarily disabled. user@example.com",
			expectedErr:    ErrTooManyAttempts,
			expectedReason: "TOO_MANY_ATTEMPTS_TRY_LATER",
		},
		{message: "INVALID_PASSWORD", expectedErr: ErrCredentialsRejected, expectedReason: "INVALID_PASSWORD"},
		{message: "TENANT_ID_MISMATCH : details", expectedErr: ErrProviderUnavailable, expectedReason: "TENANT_ID_MISMATCH"},
		{message: "API_KEY_INVALID", expectedErr: ErrProviderUnavailable, expectedReason: "API_KEY_INVALID"},
		{message: "", expectedErr: ErrProviderUnavailable, expectedReason: "unrecognized_provider_error"},
		{message: "<script>bad key=secret", expectedErr: ErrProviderUnavailable, expectedReason: "unrecognized_provider_error"},
	} {
		t.Run(testCase.expectedReason, func(t *testing.T) {
			apiKey := strings.Join([]string{"test", "-api", "-key"}, "")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]string{"message": testCase.message}})
			}))
			defer server.Close()
			api := &IdentityToolkitAPI{APIKey: apiKey, BaseURL: server.URL, Client: server.Client()}

			_, err := api.SignInWithPassword(t.Context(), types.SignInWithPasswordRequest{
				Email: "user@example.com", Password: strings.Join([]string{"private", "-passphrase"}, ""),
			})

			require.ErrorIs(t, err, testCase.expectedErr)
			require.Equal(t, testCase.expectedReason, FailureReason(err))
			require.NotContains(t, FailureReason(err), "example.com")
			require.NotContains(t, FailureReason(err), apiKey)
		})
	}
}

func TestSignInWithPasswordReasonForNonJSONErrorResponseIsHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream failure"))
	}))
	defer server.Close()
	api := &IdentityToolkitAPI{APIKey: strings.Join([]string{"test", "-api", "-key"}, ""), BaseURL: server.URL, Client: server.Client()}

	_, err := api.SignInWithPassword(t.Context(), types.SignInWithPasswordRequest{})

	require.ErrorIs(t, err, ErrProviderUnavailable)
	require.Equal(t, "error_response_http_502", FailureReason(err))
	require.Equal(t, "unknown", FailureReason(ErrProviderUnavailable))
}
