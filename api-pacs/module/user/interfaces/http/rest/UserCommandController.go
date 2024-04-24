package rest

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-playground/validator/v10"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/iam/domain/entity"
	"api-pacs/module/user/application"
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

	// check user role and role they want to add
	userRole := r.Context().Value(iamTypes.RoleCtx)
	if userRole == entity.AdminRole && request.Role == entity.AdminRole {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusUnauthorized,
			Success:   false,
			Message:   "Unauthorized access.",
			ErrorCode: apiError.UnauthorizedAccess,
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

// DeleteTenantUser delete a tenant user. Only callable by admin or owner.
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

	// TODO: check user role and target user role to be deleted

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

// UpdateTenantUserPassword update a tenant user password. Only callable by admin or owner.
func (controller *UserCommandController) UpdateTenantUserPassword(w http.ResponseWriter, r *http.Request) {
	var request types.UpdateTenantUserPasswordRequest

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

	err = controller.UserCommandServiceInterface.UpdateTenantUserPassword(context.TODO(), serviceTypes.UpdateTenantUserPassword{
		ID:          request.ID,
		TenantID:    request.TenantID,
		NewPassword: request.NewPassword,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while updating user password."
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
		Message: "Successfully updated tenant user password.",
	}

	response.JSON(w)
}

// UpdateTenantUser update a tenant user. Only callable by admin or owner.
func (controller *UserCommandController) UpdateTenantUser(w http.ResponseWriter, r *http.Request) {
	var request types.UpdateTenantUserRequest

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

	// check user role and role they want to add
	userRole := r.Context().Value(iamTypes.RoleCtx)
	if userRole == entity.AdminRole && request.Role == entity.AdminRole {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusUnauthorized,
			Success:   false,
			Message:   "Unauthorized access.",
			ErrorCode: apiError.UnauthorizedAccess,
		}

		response.JSON(w)
		return
	}

	err = controller.UserCommandServiceInterface.UpdateTenantUser(context.TODO(), serviceTypes.UpdateTenantUser{
		ID:        request.ID,
		TenantID:  request.TenantID,
		Role:      request.Role,
		Name:      request.Name,
		LicenseNo: request.LicenseNo,
		Specialty: request.Specialty,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while updating user."
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
		Message: "Successfully updated tenant user.",
	}

	response.JSON(w)
}
