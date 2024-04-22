package iam

import (
	"net/http"
	"os"

	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
)

type IAMMiddleware struct{}

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
