package identitytoolkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"api-pacs/infrastructures/providers/api/identitytoolkit/types"
)

const (
	defaultBaseURL       = "https://identitytoolkit.googleapis.com/v1"
	maximumResponseBytes = 64 * 1024
)

var (
	ErrCredentialsRejected = errors.New("identity toolkit credentials rejected")
	// ErrTooManyAttempts is returned when Firebase temporarily blocks password
	// sign-in for the account (TOO_MANY_ATTEMPTS_TRY_LATER). It is a throttle,
	// not a provider outage, and must not be reported as one.
	ErrTooManyAttempts     = errors.New("identity toolkit too many attempts")
	ErrProviderUnavailable = errors.New("identity toolkit provider unavailable")
)

const maximumReasonLength = 64

// ProviderError carries a log-safe reason alongside one of the sentinel errors.
// Error() deliberately returns only the sentinel text so provider messages never
// reach API responses. Reason() is either an upper-snake-case Firebase error code
// (without the free-text " : details" suffix) or a fixed local label; it never
// contains request data such as the email, password, API key or tokens.
type ProviderError struct {
	sentinel error
	reason   string
}

func (err *ProviderError) Error() string { return err.sentinel.Error() }

func (err *ProviderError) Unwrap() error { return err.sentinel }

func (err *ProviderError) Reason() string { return err.reason }

// FailureReason returns the log-safe reason attached to err, or "unknown".
func FailureReason(err error) string {
	var providerError *ProviderError
	if errors.As(err, &providerError) && providerError.reason != "" {
		return providerError.reason
	}
	return "unknown"
}

func providerFailure(sentinel error, reason string) error {
	return &ProviderError{sentinel: sentinel, reason: reason}
}

type IdentityToolkitAPI struct {
	APIKey  string
	BaseURL string
	Client  *http.Client
}

func Init(apiKey string) *IdentityToolkitAPI {
	return &IdentityToolkitAPI{
		APIKey:  strings.TrimSpace(apiKey),
		BaseURL: defaultBaseURL,
		Client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (api *IdentityToolkitAPI) SignInWithPassword(
	ctx context.Context,
	data types.SignInWithPasswordRequest,
) (types.SignInWithPasswordResponse, error) {
	if api == nil || strings.TrimSpace(api.APIKey) == "" {
		return types.SignInWithPasswordResponse{}, providerFailure(ErrProviderUnavailable, "not_configured")
	}

	baseURL := strings.TrimRight(strings.TrimSpace(api.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	endpoint, err := url.Parse(baseURL + "/accounts:signInWithPassword")
	if err != nil {
		return types.SignInWithPasswordResponse{}, providerFailure(ErrProviderUnavailable, "invalid_base_url")
	}
	query := endpoint.Query()
	query.Set("key", api.APIKey)
	endpoint.RawQuery = query.Encode()

	requestBody := struct {
		TenantID          string `json:"tenantId"`
		Email             string `json:"email"`
		Password          string `json:"password"`
		ReturnSecureToken bool   `json:"returnSecureToken"`
	}{
		TenantID:          data.TenantID,
		Email:             data.Email,
		Password:          data.Password,
		ReturnSecureToken: true,
	}

	encodedBody := new(bytes.Buffer)
	if err := json.NewEncoder(encodedBody).Encode(requestBody); err != nil {
		return types.SignInWithPasswordResponse{}, providerFailure(ErrProviderUnavailable, "request_encoding")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), encodedBody)
	if err != nil {
		return types.SignInWithPasswordResponse{}, providerFailure(ErrProviderUnavailable, "request_build")
	}
	request.Header.Set("Content-Type", "application/json")

	client := api.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return types.SignInWithPasswordResponse{}, providerFailure(ErrProviderUnavailable, "transport")
	}
	defer response.Body.Close()

	limitedBody := io.LimitReader(response.Body, maximumResponseBytes)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var providerError struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.NewDecoder(limitedBody).Decode(&providerError); err != nil {
			return types.SignInWithPasswordResponse{}, providerFailure(ErrProviderUnavailable, "error_response_http_"+strconv.Itoa(response.StatusCode))
		}
		return types.SignInWithPasswordResponse{}, classifyProviderError(providerError.Error.Message)
	}

	var result types.SignInWithPasswordResponse
	if err := json.NewDecoder(limitedBody).Decode(&result); err != nil {
		return types.SignInWithPasswordResponse{}, providerFailure(ErrProviderUnavailable, "success_response_decode")
	}
	if strings.TrimSpace(result.IDToken) == "" || strings.TrimSpace(result.LocalID) == "" || strings.TrimSpace(result.Email) == "" {
		return types.SignInWithPasswordResponse{}, providerFailure(ErrProviderUnavailable, "incomplete_success_response")
	}

	return result, nil
}

// providerErrorCode extracts the Firebase error code from an error message.
// Firebase may append free text after " : " (for example
// "TOO_MANY_ATTEMPTS_TRY_LATER : Access to this account has been temporarily
// disabled ..."), which is discarded. Only [A-Z0-9_] characters are kept so the
// result is safe to log.
func providerErrorCode(message string) string {
	code := strings.TrimSpace(strings.SplitN(message, ":", 2)[0])
	sanitized := strings.Map(func(character rune) rune {
		switch {
		case character >= 'A' && character <= 'Z', character >= '0' && character <= '9', character == '_':
			return character
		default:
			return -1
		}
	}, code)
	if len(sanitized) > maximumReasonLength {
		sanitized = sanitized[:maximumReasonLength]
	}
	if sanitized == "" {
		return "unrecognized_provider_error"
	}
	return sanitized
}

func classifyProviderError(message string) error {
	code := providerErrorCode(message)
	switch code {
	case "EMAIL_NOT_FOUND", "INVALID_PASSWORD", "INVALID_LOGIN_CREDENTIALS", "USER_DISABLED":
		return providerFailure(ErrCredentialsRejected, code)
	case "TOO_MANY_ATTEMPTS_TRY_LATER":
		return providerFailure(ErrTooManyAttempts, code)
	default:
		// Configuration or platform problems such as API_KEY_INVALID,
		// PASSWORD_LOGIN_DISABLED, OPERATION_NOT_ALLOWED, INVALID_TENANT_ID,
		// TENANT_ID_MISMATCH or QUOTA_EXCEEDED: the operator must fix them, the
		// user cannot. The code is kept as the log reason.
		return providerFailure(ErrProviderUnavailable, code)
	}
}
