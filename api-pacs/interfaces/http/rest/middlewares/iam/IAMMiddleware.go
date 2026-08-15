package iam

import (
	"context"
	"net/http"
	"os"
	"strings"

	"api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/iam/application"
	"api-pacs/module/iam/domain/entity"
	serviceTypes "api-pacs/module/iam/infrastructure/service/types"
	userApplication "api-pacs/module/user/application"
)

type IAMMiddleware struct {
	application.IAMCommandServiceInterface
	application.IAMQueryServiceInterface
	userApplication.UserQueryServiceInterface
}

// PolicyAcceptanceGuard blocks protected demo functionality until the signed-in
// user has accepted every current required policy. Policy recovery endpoints are
// deliberately mounted outside this middleware.
func (middleware *IAMMiddleware) PolicyAcceptanceGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID, tenantOK := r.Context().Value(types.TenantIDCtx).(string)
		userID, userOK := r.Context().Value(types.UserIDCtx).(string)
		if !tenantOK || !userOK || tenantID == "" || userID == "" || middleware.UserQueryServiceInterface == nil {
			writePolicyUnavailable(w, apiError.PolicyConfigurationUnavailable)
			return
		}
		status, err := middleware.UserQueryServiceInterface.GetPolicyStatus(r.Context(), tenantID, userID)
		if err != nil {
			writePolicyUnavailable(w, err.Error())
			return
		}
		if status.EnforcementActive && status.AcceptanceRequired {
			response := viewmodels.HTTPResponseVM{
				Status: http.StatusPreconditionRequired, Success: false,
				Message:   "Acceptance of the current Terms and Privacy Policy is required.",
				ErrorCode: apiError.PolicyAcceptanceRequired,
			}
			response.JSON(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writePolicyUnavailable(w http.ResponseWriter, errorCode string) {
	response := viewmodels.HTTPResponseVM{
		Status: http.StatusServiceUnavailable, Success: false,
		Message: "Policy acceptance verification is temporarily unavailable.", ErrorCode: errorCode,
	}
	response.JSON(w)
}

// FirebaseSuperUserGuard firebase superuser guard middleware
func (middleware *IAMMiddleware) FirebaseSuperUserGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// check if superuser key is provided and correct
		if r.Header.Get("X-FB-SUDO-KEY") != os.Getenv("FIREBASE_SUPERUSER_KEY") {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusUnauthorized,
				Success:   false,
				Message:   "Unauthorized access.",
				ErrorCode: apiError.UnauthorizedAccess,
			}

			response.JSON(w)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// RBACOwnerGuard rbac for owner
func (middleware *IAMMiddleware) RBACOwnerGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userRole := r.Context().Value(types.RoleCtx)

		if userRole != entity.OwnerRole {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusUnauthorized,
				Success:   false,
				Message:   "Unauthorized access.",
				ErrorCode: apiError.UnauthorizedAccess,
			}

			response.JSON(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RBACOwnerOrAdminGuard rbac for owner and admin
func (middleware *IAMMiddleware) RBACOwnerOrAdminGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userRole := r.Context().Value(types.RoleCtx)

		if userRole != entity.OwnerRole && userRole != entity.AdminRole {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusUnauthorized,
				Success:   false,
				Message:   "Unauthorized access.",
				ErrorCode: apiError.UnauthorizedAccess,
			}

			response.JSON(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// TokenSessionAuthGuard token session authenticator guard
func (middleware *IAMMiddleware) TokenSessionAuthGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Get("Authorization")
		sessionToken := strings.TrimSpace(strings.Replace(authorization, "Bearer", "", 1))

		tokenSession, err := middleware.IAMQueryServiceInterface.GetSessionToken(r.Context(), sessionToken)
		if err != nil {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusUnauthorized,
				Success:   false,
				Message:   "Unauthorized access.",
				ErrorCode: apiError.UnauthorizedAccess,
			}

			response.JSON(w)
			return
		}

		suspended, err := middleware.IAMQueryServiceInterface.IsUserSuspended(r.Context(), tokenSession.TenantID, tokenSession.UserID)
		if err != nil || suspended {
			writeUnauthorized(w)
			return
		}

		// reset token session duration
		err = middleware.IAMCommandServiceInterface.SetTokenSession(r.Context(), serviceTypes.SetTokenSession{
			SessionID:           sessionToken,
			TenantID:            tokenSession.TenantID,
			UserID:              tokenSession.UserID,
			Role:                tokenSession.Role,
			ExpireTimeInSeconds: entity.ExpireTimeInSeconds,
		})
		if err != nil {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusUnauthorized,
				Success:   false,
				Message:   "Unauthorized access.",
				ErrorCode: apiError.UnauthorizedAccess,
			}

			response.JSON(w)
			return

		}

		// set context and pass
		ctx := context.WithValue(r.Context(), types.TenantIDCtx, tokenSession.TenantID)
		ctx = context.WithValue(ctx, types.UserIDCtx, tokenSession.UserID)
		ctx = context.WithValue(ctx, types.RoleCtx, tokenSession.Role)
		ctx = context.WithValue(ctx, types.BearerTokenCtx, sessionToken)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// TokenSessionOrthancProxyAuthGuard token session orthanc proxy authenticator guard
func (middleware *IAMMiddleware) TokenSessionOrthancProxyAuthGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Get("Authorization")
		sessionToken := strings.TrimSpace(strings.Replace(authorization, "Bearer", "", 1))

		tokenSession, err := middleware.IAMQueryServiceInterface.GetSessionToken(r.Context(), sessionToken)
		if err != nil {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusUnauthorized,
				Success:   false,
				Message:   "Unauthorized access.",
				ErrorCode: apiError.UnauthorizedAccess,
			}

			response.JSON(w)
			return
		}

		suspended, err := middleware.IAMQueryServiceInterface.IsUserSuspended(r.Context(), tokenSession.TenantID, tokenSession.UserID)
		if err != nil || suspended {
			writeUnauthorized(w)
			return
		}

		// set context and pass
		ctx := context.WithValue(r.Context(), types.TenantIDCtx, tokenSession.TenantID)
		ctx = context.WithValue(ctx, types.UserIDCtx, tokenSession.UserID)
		ctx = context.WithValue(ctx, types.RoleCtx, tokenSession.Role)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeUnauthorized(w http.ResponseWriter) {
	response := viewmodels.HTTPResponseVM{
		Status:    http.StatusUnauthorized,
		Success:   false,
		Message:   "Unauthorized access.",
		ErrorCode: apiError.UnauthorizedAccess,
	}
	response.JSON(w)
}
