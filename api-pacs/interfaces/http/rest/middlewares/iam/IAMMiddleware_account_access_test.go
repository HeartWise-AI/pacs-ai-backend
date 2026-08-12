package iam

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"api-pacs/interfaces/http/rest/middlewares/iam/types"
	apiError "api-pacs/internal/errors"
	iamApplication "api-pacs/module/iam/application"
	iamEntity "api-pacs/module/iam/domain/entity"
	serviceTypes "api-pacs/module/iam/infrastructure/service/types"
)

type accessGuardQueryService struct {
	iamApplication.IAMQueryServiceInterface
	suspended bool
}

func (service *accessGuardQueryService) GetSessionToken(context.Context, string) (iamEntity.TokenSession, error) {
	return iamEntity.TokenSession{TenantID: "tenant-a", UserID: "user-a", Role: iamEntity.UserRole}, nil
}

func (service *accessGuardQueryService) IsUserSuspended(context.Context, string, string) (bool, error) {
	return service.suspended, nil
}

type accessGuardCommandService struct {
	iamApplication.IAMCommandServiceInterface
	setCalls int
	setErr   error
}

func (service *accessGuardCommandService) SetTokenSession(context.Context, serviceTypes.SetTokenSession) error {
	service.setCalls++
	return service.setErr
}

func TestSuspendedSessionCannotReachProtectedAPIOrOrthancProxy(t *testing.T) {
	for name, guard := range map[string]func(*IAMMiddleware, http.Handler) http.Handler{
		"api": func(middleware *IAMMiddleware, next http.Handler) http.Handler {
			return middleware.TokenSessionAuthGuard(next)
		},
		"orthanc": func(middleware *IAMMiddleware, next http.Handler) http.Handler {
			return middleware.TokenSessionOrthancProxyAuthGuard(next)
		},
	} {
		t.Run(name, func(t *testing.T) {
			command := &accessGuardCommandService{}
			middleware := &IAMMiddleware{
				IAMCommandServiceInterface: command,
				IAMQueryServiceInterface:   &accessGuardQueryService{suspended: true},
			}
			reached := false
			handler := guard(middleware, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.Header.Set("Authorization", "Bearer session")
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusUnauthorized, recorder.Code)
			require.False(t, reached)
			require.Zero(t, command.setCalls)
		})
	}
}

func TestActiveSessionReachesProtectedAPIAndRefreshesTTL(t *testing.T) {
	command := &accessGuardCommandService{}
	middleware := &IAMMiddleware{
		IAMCommandServiceInterface: command,
		IAMQueryServiceInterface:   &accessGuardQueryService{suspended: false},
	}
	reached := false
	handler := middleware.TokenSessionAuthGuard(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		reached = true
		require.Equal(t, "tenant-a", request.Context().Value(types.TenantIDCtx))
		require.Equal(t, "user-a", request.Context().Value(types.UserIDCtx))
	}))
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer session")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	require.True(t, reached)
	require.Equal(t, 1, command.setCalls)
}

func TestConcurrentSuspensionCannotResurrectSession(t *testing.T) {
	command := &accessGuardCommandService{setErr: errors.New(apiError.AccountSuspended)}
	middleware := &IAMMiddleware{
		IAMCommandServiceInterface: command,
		IAMQueryServiceInterface:   &accessGuardQueryService{suspended: false},
	}
	reached := false
	handler := middleware.TokenSessionAuthGuard(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer session")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.False(t, reached)
	require.Equal(t, 1, command.setCalls)
}
