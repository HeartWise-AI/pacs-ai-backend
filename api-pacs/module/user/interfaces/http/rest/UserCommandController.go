package rest

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-playground/validator/v10"

	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/user/application"
	"api-pacs/module/user/domain/entity"
	serviceTypes "api-pacs/module/user/infrastructure/service/types"
	types "api-pacs/module/user/interfaces/http"
)

// UserCommandController request controller for user command
type UserCommandController struct {
	application.UserCommandServiceInterface
}

// CreateTenantOwner create a tenant owner. Only callable by superuser.
func (controller *UserCommandController) CreateTenantOwner(w http.ResponseWriter, r *http.Request) {
	var request types.CreateTenantUserRequest

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

	// check if role is set to owner
	if request.Role != entity.OwnerRole {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusForbidden,
			Success:   false,
			Message:   "Role must be owner.",
			ErrorCode: apiError.InvalidPayload,
		}

		response.JSON(w)
		return
	}

	generatedPassword, err := controller.UserCommandServiceInterface.CreateTenantUser(context.TODO(), serviceTypes.CreateTenantUser{
		TenantID:  request.TenantID,
		Role:      request.Role,
		Name:      request.Name,
		Email:     request.Email,
		LicenseNo: request.LicenseNo,
		Specialty: request.Specialty,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while saving user."
		case errors.DuplicateRecord:
			httpCode = http.StatusConflict
			errorMsg = "User already exist."
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
		Message: "Successfully created tenant user.",
		Data: &types.CreateTenantUserResponse{
			Password: generatedPassword,
		},
	}

	response.JSON(w)
}

// CreateTenantUser create a tenant user. Only callable by admin or owner.
// TODO: middleware to check rbac. Pass user id, tenant id, role in ctx
func (controller *UserCommandController) CreateTenantUser(w http.ResponseWriter, r *http.Request) {
	var request types.CreateTenantUserRequest

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

	// check if role to be added is owner (only callable via CreateTenantOwner)
	if request.Role == entity.OwnerRole {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusUnauthorized,
			Success:   false,
			Message:   "Unauthorized access.",
			ErrorCode: apiError.UnauthorizedAccess,
		}

		response.JSON(w)
		return
	}

	// TODO check user role and role they want to add

	generatedPassword, err := controller.UserCommandServiceInterface.CreateTenantUser(context.TODO(), serviceTypes.CreateTenantUser{
		TenantID:  request.TenantID,
		Role:      request.Role,
		Name:      request.Name,
		Email:     request.Email,
		LicenseNo: request.LicenseNo,
		Specialty: request.Specialty,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while saving user."
		case errors.DuplicateRecord:
			httpCode = http.StatusConflict
			errorMsg = "User already exist."
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
		Message: "Successfully created tenant user.",
		Data: &types.CreateTenantUserResponse{
			Password: generatedPassword,
		},
	}

	response.JSON(w)
}

// DeleteTenantUser delete a tenant user. Only callable by admin or owner.
// TODO: middleware to check rbac. Pass user id, tenant id, role in ctx
func (controller *UserCommandController) DeleteTenantUser(w http.ResponseWriter, r *http.Request) {
	var request types.DeleteTenantUserRequest

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

	err = controller.UserCommandServiceInterface.DeleteTenantUser(context.TODO(), request.TenantID, request.UserID)
	if err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusInternalServerError,
			Success:   false,
			Message:   "Database error.",
			ErrorCode: err.Error(),
		}

		response.JSON(w)
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully deleted tenant user.",
	}

	response.JSON(w)
}
