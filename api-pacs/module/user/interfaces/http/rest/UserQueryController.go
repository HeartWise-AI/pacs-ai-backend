package rest

import (
	"context"
	"net/http"

	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	"api-pacs/module/user/application"
	types "api-pacs/module/user/interfaces/http"
)

// UserQueryController request controller for record query
type UserQueryController struct {
	application.UserQueryServiceInterface
}

// GetCurrentTenantUser get current tenant user
// TODO: middleware to check rbac. Pass user id, tenant id, role in ctx
func (controller *UserQueryController) GetCurrentTenantUser(w http.ResponseWriter, r *http.Request) {
	// TODO: get ctx with
	res, err := controller.UserQueryServiceInterface.GetTenantUserByID(context.TODO(), "", "")
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.MissingRecord:
			httpCode = http.StatusNotFound
			errorMsg = "No records found."

		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Database error."
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
		Message: "Successfully fetched current tenant user.",
		Data: &types.GetTenantUserResponse{
			ID:                res.ID,
			TenantID:          res.TenantID,
			Role:              res.Role,
			Name:              res.Name,
			Email:             res.Email,
			LicenseNo:         res.LicenseNo,
			Specialty:         res.Specialty,
			IsEmailVerified:   res.IsEmailVerified,
			IsAccountDisabled: res.IsAccountDisabled,
			CreatedAt:         res.CreatedAt,
			UpdatedAt:         res.UpdatedAt,
		},
	}

	response.JSON(w)
}
