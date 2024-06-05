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
	repositoryTypes "api-pacs/module/iam/infrastructure/repository/types"
)

type IAMMiddleware struct {
	application.IAMQueryServiceInterface
	application.IAMCommandServiceInterface
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

		// reset token session duration
		err = middleware.IAMCommandServiceInterface.SetTokenSession(r.Context(), repositoryTypes.SetTokenSession{
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

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
