package rest

import (
	"context"
	"net/http"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	"api-pacs/module/tenant/application"
	types "api-pacs/module/tenant/interfaces/http"
)

// TenantQueryController request controller for record query
type TenantQueryController struct {
	application.TenantQueryServiceInterface
}

// GetTenants get tenants
func (controller *TenantQueryController) GetTenants(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	res, err := controller.TenantQueryServiceInterface.GetTenants(context.TODO(), tenantID)
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

	var tenants []types.GetTenantResponse

	for _, tenant := range res {
		tenants = append(tenants, types.GetTenantResponse{
			ID:        tenant.ID,
			Name:      tenant.Name,
			Address:   tenant.Address,
			CreatedAt: tenant.CreatedAt,
			UpdatedAt: tenant.UpdatedAt,
		})
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully fetched tenants.",
		Data:    tenants,
	}

	response.JSON(w)
}
