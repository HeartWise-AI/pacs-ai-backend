package rest

import (
	"context"
	"encoding/json"
	stdErrors "errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"

	"api-pacs/interfaces/http/rest/clientip"
	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/iam/application"
	serviceTypes "api-pacs/module/iam/infrastructure/service/types"
	types "api-pacs/module/iam/interfaces/http"
)

// IAMCommandController request controller for iam command
type IAMCommandController struct {
	application.IAMCommandServiceInterface
	TrustedProxyCIDRs []*net.IPNet
}

// ForgotTenantUserPassword forgot tenant user password
func (controller *IAMCommandController) ForgotTenantUserPassword(w http.ResponseWriter, r *http.Request) {
	var request types.ForgotTenantUserPasswordRequest

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}
	// validate request
	err := types.Validate.Struct(request)
	if err != nil {
		errors := err.(validator.ValidationErrors)
		if len(errors) > 0 {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   types.ValidationErrors[errors[0].StructNamespace()],
				ErrorCode: apiError.InvalidPayload,
			}

			response.JSON(w)
			return
		}

		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err = controller.IAMCommandServiceInterface.ForgotTenantUserPassword(context.TODO(), request.TenantID, request.Email)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.UnauthorizedAccess:
			httpCode = http.StatusUnauthorized
			errorMsg = "Unauthorized access."
		case errors.AccountSuspended:
			httpCode = http.StatusForbidden
			errorMsg = "Account is suspended."
		case errors.FirebaseAuthEmailNotVerified:
			httpCode = http.StatusUnauthorized
			errorMsg = "Email is not verified."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully sent forgot password request.",
	}

	response.JSON(w)
}

// LoginTenantUser login tenant user
func (controller *IAMCommandController) LoginTenantUser(w http.ResponseWriter, r *http.Request) {
	var request types.LoginTenantUserRequest

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
			Data:      map[string]bool{"challengeRequired": false},
		}

		response.JSON(w)
		return
	}
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.Email = strings.ToLower(strings.TrimSpace(request.Email))
	request.TurnstileToken = strings.TrimSpace(request.TurnstileToken)

	// validate request
	err := types.Validate.Struct(request)
	if err != nil {
		errors := err.(validator.ValidationErrors)
		if len(errors) > 0 {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   types.ValidationErrors[errors[0].StructNamespace()],
				ErrorCode: apiError.InvalidPayload,
				Data:      map[string]bool{"challengeRequired": false},
			}

			response.JSON(w)
			return
		}

		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
			Data:      map[string]bool{"challengeRequired": false},
		}

		response.JSON(w)
		return
	}

	sessionToken, err := controller.IAMCommandServiceInterface.LoginTenantUser(r.Context(), serviceTypes.LoginTenantUser{
		TenantID:       request.TenantID,
		Email:          request.Email,
		Password:       request.Password,
		TurnstileToken: request.TurnstileToken,
		ClientIP:       clientip.Resolve(r, controller.TrustedProxyCIDRs),
	})
	if err != nil {
		controller.writeLoginError(w, err)
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusCreated,
		Success: true,
		Message: "Successfully signed-in tenant user.",
		Data: &types.LoginTenantUserResponse{
			SessionToken: sessionToken,
		},
	}

	response.JSON(w)
}

func (controller *IAMCommandController) writeLoginError(w http.ResponseWriter, err error) {
	code := err.Error()
	challengeRequired := false
	retryAfterSeconds := 0
	var loginError *apiError.LoginError
	if stdErrors.As(err, &loginError) {
		code = loginError.Code
		challengeRequired = loginError.ChallengeRequired
		retryAfterSeconds = loginError.RetryAfterSeconds
	}

	status := http.StatusInternalServerError
	message := "Please contact technical support."
	switch code {
	case apiError.UnauthorizedAccess:
		status = http.StatusUnauthorized
		message = "Unable to sign in with the provided credentials."
	case apiError.AccountSuspended:
		status = http.StatusForbidden
		message = "Account is suspended."
	case apiError.FirebaseAuthEmailNotVerified:
		status = http.StatusUnauthorized
		message = "Email is not verified."
	case apiError.LoginChallengeRequired:
		status = http.StatusForbidden
		message = "Complete the security check to continue."
	case apiError.TurnstileInvalid:
		status = http.StatusForbidden
		message = "Security check failed. Please try again."
	case apiError.LoginRateLimited:
		status = http.StatusTooManyRequests
		message = "Too many sign-in attempts. Please try again later."
	case apiError.FirebaseAuthError, apiError.CloudflareAPIError, apiError.LoginProtectionUnavailable:
		status = http.StatusServiceUnavailable
		message = "Sign-in is temporarily unavailable. Please try again later."
	}

	if status == http.StatusTooManyRequests && retryAfterSeconds > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
	}
	response := viewmodels.HTTPResponseVM{
		Status:    status,
		Success:   false,
		Message:   message,
		ErrorCode: code,
		Data:      map[string]bool{"challengeRequired": challengeRequired},
	}
	response.JSON(w)
}

// VerifyTenantUserEmail verify tenant user email
func (controller *IAMCommandController) VerifyTenantUserEmail(w http.ResponseWriter, r *http.Request) {
	var request types.VerifyTenantUserEmailRequest

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	// validate request
	err := types.Validate.Struct(request)
	if err != nil {
		errors := err.(validator.ValidationErrors)
		if len(errors) > 0 {
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   types.ValidationErrors[errors[0].StructNamespace()],
				ErrorCode: apiError.InvalidPayload,
			}

			response.JSON(w)
			return
		}

		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid payload request.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err = controller.IAMCommandServiceInterface.VerifyTenantUserEmail(context.TODO(), request.TenantID, strings.ToLower(request.Email))
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.MailgunError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while sending verify email request."
		case errors.MaximumLimitReached:
			httpCode = http.StatusTooManyRequests
			errorMsg = "Please wait before requesting another verification email."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Please contact technical support."
		}

		response := viewmodels.HTTPResponseVM{
			Status:    httpCode,
			Success:   false,
			Message:   errorMsg,
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully sent verify email request.",
	}

	response.JSON(w)
}
