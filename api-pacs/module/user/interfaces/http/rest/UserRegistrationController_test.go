package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	iamEntity "api-pacs/module/iam/domain/entity"
	"api-pacs/module/user/application"
	serviceTypes "api-pacs/module/user/infrastructure/service/types"
)

type registrationCommandService struct {
	application.UserCommandServiceInterface
	registrationInput serviceTypes.RegisterTenantUser
	registrationCalls int
	createdInput      serviceTypes.CreateTenantUser
}

func (service *registrationCommandService) RegisterTenantUser(_ context.Context, data serviceTypes.RegisterTenantUser) error {
	service.registrationInput = data
	service.registrationCalls++
	return nil
}

func (service *registrationCommandService) CreateTenantUser(_ context.Context, data serviceTypes.CreateTenantUser) (string, error) {
	service.createdInput = data
	return "generated-password", nil
}

func registrationRequest(t *testing.T, role *string) *http.Request {
	t.Helper()

	payload := map[string]interface{}{
		"tenantId":  "tenant-a",
		"name":      "Public User",
		"email":     "PUBLIC.USER@EXAMPLE.COM",
		"password":  "ValidPassword!",
		"licenseNo": "demo-license",
		"specialty": "demo-specialty",
	}
	if role != nil {
		payload["role"] = *role
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)
	return httptest.NewRequest(http.MethodPost, "/v1/user/register", bytes.NewReader(body))
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
			require.Equal(t, "public.user@example.com", service.registrationInput.Email)
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
}

func stringPointer(value string) *string {
	return &value
}
