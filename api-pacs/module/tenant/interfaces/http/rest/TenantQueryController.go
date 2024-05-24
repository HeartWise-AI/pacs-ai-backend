package rest

import (
	"context"
	"net/http"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/tenant/application"
	types "api-pacs/module/tenant/interfaces/http"
)

// TenantQueryController request controller for record query
type TenantQueryController struct {
	application.TenantQueryServiceInterface
}

// GetTenantByID get current tenant by id
func (controller *TenantQueryController) GetTenantByID(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	res, err := controller.TenantQueryServiceInterface.GetTenantByID(context.TODO(), tenantID)
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
		Message: "Successfully fetched tenant by id.",
		Data: &types.GetTenantResponse{
			ID:              res.ID,
			Name:            res.Name,
			Address:         res.Address,
			AvailableModels: res.AvailableModels,
			AET:             res.AET,
			CreatedAt:       res.CreatedAt,
			UpdatedAt:       res.UpdatedAt,
		},
	}

	response.JSON(w)
}

// GetPublicTenantByID get public tenant by id
func (controller *TenantQueryController) GetPublicTenantByID(w http.ResponseWriter, r *http.Request) {
	tenantID := r.URL.Query().Get("tenantId")

	if len(tenantID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Tenant ID is required.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	res, err := controller.TenantQueryServiceInterface.GetTenantByID(context.TODO(), tenantID)
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
		Message: "Successfully fetched public tenant.",
		Data: &types.GetPublicTenantResponse{
			ID:      res.ID,
			Name:    res.Name,
			Address: res.Address,
		},
	}

	response.JSON(w)
}
