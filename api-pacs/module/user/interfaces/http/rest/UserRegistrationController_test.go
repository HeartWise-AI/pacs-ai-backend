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

	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
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
		"email":          "PUBLIC.USER@EXAMPLE.COM",
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
		Components struct {
			Schemas map[string]struct {
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
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
}

func stringPointer(value string) *string {
	return &value
}
