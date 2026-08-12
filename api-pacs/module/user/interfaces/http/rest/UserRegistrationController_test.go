package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/middlewares/peeraddr"
	apiError "api-pacs/internal/errors"
	iamEntity "api-pacs/module/iam/domain/entity"
	"api-pacs/module/user/application"
	serviceTypes "api-pacs/module/user/infrastructure/service/types"
)

type registrationCommandService struct {
	application.UserCommandServiceInterface
	registrationInput serviceTypes.RegisterTenantUser
	registrationCalls int
	registrationErr   error
	createdInput      serviceTypes.CreateTenantUser
}

func (service *registrationCommandService) RegisterTenantUser(_ context.Context, data serviceTypes.RegisterTenantUser) error {
	service.registrationInput = data
	service.registrationCalls++
	return service.registrationErr
}

func (service *registrationCommandService) CreateTenantUser(_ context.Context, data serviceTypes.CreateTenantUser) (string, error) {
	service.createdInput = data
	return "generated-password", nil
}

func registrationRequest(t *testing.T, role *string) *http.Request {
	t.Helper()

	payload := registrationPayload(role)
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	return httptest.NewRequest(http.MethodPost, "/v1/user/register", bytes.NewReader(body))
}

func registrationPayload(role *string) map[string]interface{} {
	payload := map[string]interface{}{
		"tenantId":       "tenant-a",
		"turnstileToken": "valid-turnstile-token",
		"name":           "Public User",
		"email":          "  PUBLIC.USER@EXAMPLE.COM  ",
		"password":       "ValidPassword!",
		"licenseNo":      "demo-license",
		"specialty":      "demo-specialty",
	}
	if role != nil {
		payload["role"] = *role
	}
	return payload
}

func TestRegisterTenantUserIgnoresClientControlledRole(t *testing.T) {
	testCases := []struct {
		name string
		role *string
	}{
		{name: "role omitted"},
		{name: "user role", role: stringPointer(iamEntity.UserRole)},
		{name: "admin role", role: stringPointer(iamEntity.AdminRole)},
		{name: "owner role", role: stringPointer(iamEntity.OwnerRole)},
		{name: "unknown role", role: stringPointer("SUPER_ADMIN")},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service := &registrationCommandService{}
			controller := UserCommandController{UserCommandServiceInterface: service}
			recorder := httptest.NewRecorder()

			controller.RegisterTenantUser(recorder, registrationRequest(t, testCase.role))

			require.Equal(t, http.StatusCreated, recorder.Code)
			require.Equal(t, 1, service.registrationCalls)
			require.Equal(t, "tenant-a", service.registrationInput.TenantID)
			require.Equal(t, "valid-turnstile-token", service.registrationInput.TurnstileToken)
			require.Equal(t, "public.user@example.com", service.registrationInput.Email)
		})
	}
}

func TestRegisterTenantUserNormalizesSupportedTextFields(t *testing.T) {
	service := &registrationCommandService{}
	controller := UserCommandController{UserCommandServiceInterface: service}
	code := "  invite-code  "
	payload := registrationPayload(nil)
	payload["tenantId"] = "  tenant-a  "
	payload["turnstileToken"] = "  valid-turnstile-token  "
	payload["name"] = "  Public User  "
	payload["licenseNo"] = "  demo-license  "
	payload["specialty"] = "  demo-specialty  "
	payload["code"] = code
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()

	controller.RegisterTenantUser(recorder, httptest.NewRequest(http.MethodPost, "/v1/user/register", bytes.NewReader(body)))

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, 1, service.registrationCalls)
	require.Equal(t, "tenant-a", service.registrationInput.TenantID)
	require.Equal(t, "valid-turnstile-token", service.registrationInput.TurnstileToken)
	require.Equal(t, "Public User", service.registrationInput.Name)
	require.Equal(t, "public.user@example.com", service.registrationInput.Email)
	require.Equal(t, "demo-license", service.registrationInput.LicenseNo)
	require.Equal(t, "demo-specialty", service.registrationInput.Specialty)
	require.NotNil(t, service.registrationInput.Code)
	require.Equal(t, "invite-code", *service.registrationInput.Code)
}

func TestRegisterTenantUserRejectsInvalidPublicInputs(t *testing.T) {
	testCases := []struct {
		name   string
		field  string
		value  interface{}
		remove bool
	}{
		{name: "blank tenant", field: "tenantId", value: "   "},
		{name: "tenant too long", field: "tenantId", value: string(make([]byte, 129))},
		{name: "invalid email", field: "email", value: "not-an-email"},
		{name: "password too short", field: "password", value: string([]byte{'A', 'a', '!', '1', '2', '3'})},
		{name: "password without uppercase", field: "password", value: "lowercase!"},
		{name: "password without lowercase", field: "password", value: "UPPERCASE!"},
		{name: "password without special character", field: "password", value: "MixedCase123"},
		{name: "name too long", field: "name", value: string(make([]byte, 101))},
		{name: "blank license", field: "licenseNo", value: "   "},
		{name: "blank specialty", field: "specialty", value: "   "},
		{name: "invite code too long", field: "code", value: string(make([]byte, 257))},
		{name: "legacy role too long", field: "role", value: string(make([]byte, 65))},
		{name: "missing email", field: "email", remove: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service := &registrationCommandService{}
			controller := UserCommandController{UserCommandServiceInterface: service}
			payload := registrationPayload(nil)
			if testCase.remove {
				delete(payload, testCase.field)
			} else {
				payload[testCase.field] = testCase.value
			}
			body, err := json.Marshal(payload)
			require.NoError(t, err)
			recorder := httptest.NewRecorder()

			controller.RegisterTenantUser(recorder, httptest.NewRequest(http.MethodPost, "/v1/user/register", bytes.NewReader(body)))

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Equal(t, 0, service.registrationCalls)
			var response struct {
				ErrorCode string `json:"errorCode"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.Equal(t, apiError.InvalidPayload, response.ErrorCode)
		})
	}
}

func TestRegisterTenantUserRejectsUnsupportedOrTrailingJSON(t *testing.T) {
	testCases := []struct {
		name string
		body []byte
	}{
		{
			name: "unsupported field",
			body: func() []byte {
				payload := registrationPayload(nil)
				payload["isAdmin"] = true
				body, err := json.Marshal(payload)
				require.NoError(t, err)
				return body
			}(),
		},
		{name: "trailing JSON", body: append(mustMarshalRegistrationPayload(t), []byte(` {}`)...)},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service := &registrationCommandService{}
			controller := UserCommandController{UserCommandServiceInterface: service}
			recorder := httptest.NewRecorder()

			controller.RegisterTenantUser(recorder, httptest.NewRequest(http.MethodPost, "/v1/user/register", bytes.NewReader(testCase.body)))

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Equal(t, 0, service.registrationCalls)
			var response struct {
				ErrorCode string `json:"errorCode"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.Equal(t, apiError.InvalidRequestPayload, response.ErrorCode)
		})
	}
}

func mustMarshalRegistrationPayload(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(registrationPayload(nil))
	require.NoError(t, err)
	return body
}

func TestRegisterTenantUserRequiresTurnstileToken(t *testing.T) {
	service := &registrationCommandService{}
	controller := UserCommandController{UserCommandServiceInterface: service}
	payload := registrationPayload(nil)
	delete(payload, "turnstileToken")
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()

	controller.RegisterTenantUser(recorder, httptest.NewRequest(http.MethodPost, "/v1/user/register", bytes.NewReader(body)))

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, 0, service.registrationCalls)
	var response struct {
		ErrorCode string `json:"errorCode"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, apiError.InvalidPayload, response.ErrorCode)
}

func TestRegisterTenantUserMapsTurnstileFailures(t *testing.T) {
	testCases := []struct {
		name       string
		serviceErr error
		status     int
	}{
		{name: "invalid response", serviceErr: errors.New(apiError.TurnstileInvalid), status: http.StatusBadRequest},
		{name: "provider unavailable", serviceErr: errors.New(apiError.CloudflareAPIError), status: http.StatusServiceUnavailable},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service := &registrationCommandService{registrationErr: testCase.serviceErr}
			controller := UserCommandController{UserCommandServiceInterface: service}
			recorder := httptest.NewRecorder()

			controller.RegisterTenantUser(recorder, registrationRequest(t, nil))

			require.Equal(t, testCase.status, recorder.Code)
			var response struct {
				ErrorCode string `json:"errorCode"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.Equal(t, testCase.serviceErr.Error(), response.ErrorCode)
		})
	}
}

func TestRegisterTenantUserReturnsRateLimitContract(t *testing.T) {
	service := &registrationCommandService{registrationErr: &apiError.RegistrationRateLimitError{RetryAfterSeconds: 73}}
	controller := UserCommandController{UserCommandServiceInterface: service}
	recorder := httptest.NewRecorder()

	controller.RegisterTenantUser(recorder, registrationRequest(t, nil))

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, "73", recorder.Header().Get("Retry-After"))
	var response struct {
		ErrorCode string `json:"errorCode"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, apiError.RegistrationRateLimited, response.ErrorCode)
}

func TestRegisterTenantUserUsesOnlyTrustedProxyClientIP(t *testing.T) {
	trustedNetworks, err := ParseTrustedProxyCIDRs("172.16.0.0/12")
	require.NoError(t, err)

	testCases := []struct {
		name         string
		remoteAddr   string
		realIP       string
		trueClientIP string
		forwardedFor string
		expectedIP   string
	}{
		{name: "trusted proxy", remoteAddr: "172.20.0.5:4321", realIP: "203.0.113.25", expectedIP: "203.0.113.25"},
		{name: "untrusted direct peer cannot spoof real IP", remoteAddr: "198.51.100.7:4321", realIP: "203.0.113.25", expectedIP: "198.51.100.7"},
		{name: "untrusted direct peer cannot spoof true client IP", remoteAddr: "198.51.100.7:4321", realIP: "203.0.113.25", trueClientIP: "172.20.0.5", expectedIP: "198.51.100.7"},
		{name: "untrusted direct peer cannot spoof forwarded for", remoteAddr: "198.51.100.7:4321", forwardedFor: "172.20.0.5", expectedIP: "198.51.100.7"},
		{name: "invalid trusted header falls back to peer", remoteAddr: "172.20.0.5:4321", realIP: "not-an-ip", expectedIP: "172.20.0.5"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service := &registrationCommandService{}
			controller := UserCommandController{
				UserCommandServiceInterface: service,
				TrustedProxyCIDRs:           trustedNetworks,
			}
			request := registrationRequest(t, nil)
			request.RemoteAddr = testCase.remoteAddr
			request.Header.Set("X-Real-IP", testCase.realIP)
			request.Header.Set("True-Client-IP", testCase.trueClientIP)
			request.Header.Set("X-Forwarded-For", testCase.forwardedFor)
			recorder := httptest.NewRecorder()

			handler := peeraddr.Capture(middleware.RealIP(http.HandlerFunc(controller.RegisterTenantUser)))
			handler.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusCreated, recorder.Code)
			require.Equal(t, testCase.expectedIP, service.registrationInput.ClientIP)
		})
	}
}

func TestParseTrustedProxyCIDRsRejectsInvalidConfiguration(t *testing.T) {
	_, err := ParseTrustedProxyCIDRs("172.16.0.0/12,not-a-cidr")
	require.Error(t, err)
}

func TestCreateTenantUserPreservesAuthorizedAdminRole(t *testing.T) {
	service := &registrationCommandService{}
	controller := UserCommandController{UserCommandServiceInterface: service}
	payload := []byte(`{
		"role":"ADMIN",
		"name":"Trusted Admin",
		"email":"trusted.admin@example.com",
		"licenseNo":"admin-license",
		"specialty":"admin-specialty"
	}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/user/add", bytes.NewReader(payload))
	ctx := context.WithValue(request.Context(), iamTypes.TenantIDCtx, "tenant-a")
	ctx = context.WithValue(ctx, iamTypes.RoleCtx, iamEntity.OwnerRole)
	recorder := httptest.NewRecorder()

	controller.CreateTenantUser(recorder, request.WithContext(ctx))

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, iamEntity.AdminRole, service.createdInput.Role)
	require.Equal(t, "tenant-a", service.createdInput.TenantID)
}

func TestRegisterTenantUserOpenAPIExcludesClientControlledRole(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../../../docs/openapi.json"))
	payload, err := os.ReadFile(path)
	require.NoError(t, err)

	var document struct {
		Paths map[string]struct {
			Post struct {
				Responses map[string]json.RawMessage `json:"responses"`
			} `json:"post"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Required             []string                   `json:"required"`
				Properties           map[string]json.RawMessage `json:"properties"`
				AdditionalProperties *bool                      `json:"additionalProperties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	require.NoError(t, json.Unmarshal(payload, &document))
	schema, ok := document.Components.Schemas["RegisterTenantUserRequest"]
	require.True(t, ok)
	require.NotContains(t, schema.Required, "role")
	require.NotContains(t, schema.Properties, "role")
	require.Contains(t, schema.Required, "turnstileToken")
	require.Contains(t, schema.Properties, "turnstileToken")
	require.NotNil(t, schema.AdditionalProperties)
	require.False(t, *schema.AdditionalProperties)

	var emailSchema struct {
		Format    string `json:"format"`
		MaxLength int    `json:"maxLength"`
	}
	require.NoError(t, json.Unmarshal(schema.Properties["email"], &emailSchema))
	require.Equal(t, "email", emailSchema.Format)
	require.Equal(t, 254, emailSchema.MaxLength)

	var passwordSchema struct {
		MinLength int `json:"minLength"`
		MaxLength int `json:"maxLength"`
	}
	require.NoError(t, json.Unmarshal(schema.Properties["password"], &passwordSchema))
	require.Equal(t, 8, passwordSchema.MinLength)
	require.Equal(t, 128, passwordSchema.MaxLength)

	registerPath, ok := document.Paths["/user/register"]
	require.True(t, ok)
	require.Contains(t, registerPath.Post.Responses, "429")
}

func stringPointer(value string) *string {
	return &value
}
