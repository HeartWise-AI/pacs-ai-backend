package identitytoolkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
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
	ErrProviderUnavailable = errors.New("identity toolkit provider unavailable")
)

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
		return types.SignInWithPasswordResponse{}, ErrProviderUnavailable
	}

	baseURL := strings.TrimRight(strings.TrimSpace(api.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	endpoint, err := url.Parse(baseURL + "/accounts:signInWithPassword")
	if err != nil {
		return types.SignInWithPasswordResponse{}, ErrProviderUnavailable
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
		return types.SignInWithPasswordResponse{}, ErrProviderUnavailable
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), encodedBody)
	if err != nil {
		return types.SignInWithPasswordResponse{}, ErrProviderUnavailable
	}
	request.Header.Set("Content-Type", "application/json")

	client := api.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return types.SignInWithPasswordResponse{}, ErrProviderUnavailable
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
			return types.SignInWithPasswordResponse{}, ErrProviderUnavailable
		}
		if isCredentialRejection(providerError.Error.Message) {
			return types.SignInWithPasswordResponse{}, ErrCredentialsRejected
		}
		return types.SignInWithPasswordResponse{}, ErrProviderUnavailable
	}

	var result types.SignInWithPasswordResponse
	if err := json.NewDecoder(limitedBody).Decode(&result); err != nil {
		return types.SignInWithPasswordResponse{}, ErrProviderUnavailable
	}
	if strings.TrimSpace(result.IDToken) == "" || strings.TrimSpace(result.LocalID) == "" || strings.TrimSpace(result.Email) == "" {
		return types.SignInWithPasswordResponse{}, ErrProviderUnavailable
	}

	return result, nil
}

func isCredentialRejection(message string) bool {
	code := strings.TrimSpace(strings.SplitN(message, " : ", 2)[0])
	switch code {
	case "EMAIL_NOT_FOUND", "INVALID_PASSWORD", "INVALID_LOGIN_CREDENTIALS", "USER_DISABLED":
		return true
	default:
		return false
	}
}
