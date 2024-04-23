package rest

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-playground/validator/v10"

	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/iam/application"
	types "api-pacs/module/iam/interfaces/http"
)

// IAMCommandController request controller for iam command
type IAMCommandController struct {
	application.IAMCommandServiceInterface
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

	sessionToken, err := controller.IAMCommandServiceInterface.LoginTenantUser(context.TODO(), request.TenantID, request.IDToken)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.UnauthorizedAccess:
			httpCode = http.StatusUnauthorized
			errorMsg = "Unauthorized access."
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
		Status:  http.StatusCreated,
		Success: true,
		Message: "Successfully signed-in tenant user.",
		Data: &types.LoginTenantUserResponse{
			SessionToken: sessionToken,
		},
	}

	response.JSON(w)
}
