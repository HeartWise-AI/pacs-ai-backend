package iam

import (
	"context"
	"crypto/subtle"
	"net/http"
	"os"
	"strings"

	"api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/access/accessctx"
	accessEntity "api-pacs/module/access/domain/entity"
	"api-pacs/module/iam/application"
	"api-pacs/module/iam/domain/entity"
	serviceTypes "api-pacs/module/iam/infrastructure/service/types"
	userApplication "api-pacs/module/user/application"
)

type IAMMiddleware struct {
	application.IAMCommandServiceInterface
	application.IAMQueryServiceInterface
	userApplication.UserQueryServiceInterface
	// AccessResolver resolves custom-role permissions and model access. When
	// nil, access falls back to the session role alone (no custom roles).
	AccessResolver AccessResolver
}

// AccessResolver resolves the effective access of a signed-in user.
type AccessResolver interface {
	Resolve(ctx context.Context, tenantID, userID, role string) (accessEntity.Access, error)
}

// withAccess attaches a lazy access resolver for the authenticated session.
func (middleware *IAMMiddleware) withAccess(ctx context.Context, tenantID, userID, role string) context.Context {
	if middleware.AccessResolver == nil {
		return ctx
	}
	resolver := middleware.AccessResolver
	return accessctx.WithResolver(ctx, func() (accessEntity.Access, error) {
		return resolver.Resolve(ctx, tenantID, userID, role)
	})
}

// RequirePermission allows the request only when the authenticated user holds
// permission. OWNER and ADMIN hold every permission. Access that cannot be
// resolved is denied with 503 (fail closed). Mount after a token-session guard.
func (middleware *IAMMiddleware) RequirePermission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			access, err := accessctx.Get(r.Context())
			if err != nil {
				response := viewmodels.HTTPResponseVM{
					Status:    http.StatusServiceUnavailable,
					Success:   false,
					Message:   "Access verification is temporarily unavailable.",
					ErrorCode: apiError.AccessUnavailable,
				}
				response.JSON(w)
				return
			}
			if !access.Has(permission) {
				response := viewmodels.HTTPResponseVM{
					Status:    http.StatusForbidden,
					Success:   false,
					Message:   "Forbidden access.",
					ErrorCode: apiError.ForbiddenAccess,
				}
				response.JSON(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
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
		expected := strings.TrimSpace(os.Getenv("FIREBASE_SUPERUSER_KEY"))
		provided := r.Header.Get("X-FB-SUDO-KEY")
		if expected == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
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
				Status:    http.StatusForbidden,
				Success:   false,
				Message:   "Forbidden access.",
				ErrorCode: apiError.ForbiddenAccess,
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
				Status:    http.StatusForbidden,
				Success:   false,
				Message:   "Forbidden access.",
				ErrorCode: apiError.ForbiddenAccess,
			}

			response.JSON(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAllModels allows the request only when the authenticated user may
// use every model. Workspace-wide processing (reprocessing, ingestion jobs)
// dispatches every ingestion model, so a role restricted to some models
// cannot use it. Fails closed with 503 when access cannot be read.
func (middleware *IAMMiddleware) RequireAllModels(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		access, err := accessctx.Get(r.Context())
		if err != nil {
			response := viewmodels.HTTPResponseVM{
				Status: http.StatusServiceUnavailable, Success: false,
				Message: "Access verification is temporarily unavailable.", ErrorCode: apiError.AccessUnavailable,
			}
			response.JSON(w)
			return
		}
		if !access.AllModels {
			response := viewmodels.HTTPResponseVM{
				Status: http.StatusForbidden, Success: false,
				Message: "This action runs every model and needs a role with access to all models.", ErrorCode: apiError.ForbiddenAccess,
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
		ctx = middleware.withAccess(ctx, tokenSession.TenantID, tokenSession.UserID, tokenSession.Role)

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
		ctx = middleware.withAccess(ctx, tokenSession.TenantID, tokenSession.UserID, tokenSession.Role)

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
