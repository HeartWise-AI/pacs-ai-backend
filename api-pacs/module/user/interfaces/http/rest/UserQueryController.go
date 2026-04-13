package rest

import (
	"context"
	"encoding/json"
	"net/http"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
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
func (controller *UserQueryController) GetCurrentTenantUser(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	res, err := controller.UserQueryServiceInterface.GetTenantUserByID(context.TODO(), tenantID, userID)
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
			IsConsentSigned:   res.IsConsentSigned,
			CreatedAt:         res.CreatedAt,
			UpdatedAt:         res.UpdatedAt,
		},
	}

	response.JSON(w)
}

// GetDoctorSpecialties get doctor specialties
func (controller *UserQueryController) GetDoctorSpecialties(w http.ResponseWriter, r *http.Request) {
	res, err := controller.UserQueryServiceInterface.GetDoctorSpecialties(context.TODO())
	if err != nil && err.Error() != errors.MissingRecord {
		var httpCode int
		var errorMsg string

		switch err.Error() {
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
		Message: "Successfully fetched doctor specialties.",
		Data:    res,
	}

	response.JSON(w)
}

// GetTenantUsers get tenant users
func (controller *UserQueryController) GetTenantUsers(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	res, err := controller.UserQueryServiceInterface.GetTenantUsers(context.TODO(), tenantID)
	if err != nil && err.Error() != errors.MissingRecord {
		var httpCode int
		var errorMsg string

		switch err.Error() {
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

	users := []types.GetTenantUserResponse{}

	for _, user := range res {
		users = append(users, types.GetTenantUserResponse{
			ID:                user.ID,
			TenantID:          user.TenantID,
			Role:              user.Role,
			Name:              user.Name,
			Email:             user.Email,
			LicenseNo:         user.LicenseNo,
			Specialty:         user.Specialty,
			IsEmailVerified:   user.IsEmailVerified,
			IsAccountDisabled: user.IsAccountDisabled,
			CreatedAt:         user.CreatedAt,
			UpdatedAt:         user.UpdatedAt,
		})
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully fetched tenant users.",
		Data:    users,
	}

	response.JSON(w)
}

// GetUserMetadata get user metadata
func (controller *UserQueryController) GetUserMetadata(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	res, err := controller.UserQueryServiceInterface.GetUserMetadata(context.TODO(), userID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.FirestoreError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while fetching user metadata."
		case errors.MissingRecord:
			httpCode = http.StatusNotFound
			errorMsg = "User metadata not found."
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

	var metadata map[string]interface{}
	err = json.Unmarshal([]byte(res.Metadata), &metadata)
	if err != nil {
		metadata = map[string]interface{}{}
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully fetched user metadata.",
		Data: &types.GetUserMetadataResponse{
			UserID:    res.UserID,
			Metadata:  metadata,
			CreatedAt: uint64(res.CreatedAt),
			UpdatedAt: uint64(res.UpdatedAt),
		},
	}

	response.JSON(w)
}
