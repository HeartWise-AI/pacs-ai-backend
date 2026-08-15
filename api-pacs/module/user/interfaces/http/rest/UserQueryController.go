package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	"api-pacs/module/user/application"
	serviceTypes "api-pacs/module/user/infrastructure/service/types"
	types "api-pacs/module/user/interfaces/http"
)

// UserQueryController request controller for record query
type UserQueryController struct {
	application.UserQueryServiceInterface
}

// GetRegistrationPolicies returns current deployment-owned policy metadata.
func (controller *UserQueryController) GetRegistrationPolicies(w http.ResponseWriter, r *http.Request) {
	tenantID := strings.TrimSpace(r.URL.Query().Get("tenantId"))
	if tenantID == "" || len(tenantID) > 128 {
		response := viewmodels.HTTPResponseVM{Status: http.StatusBadRequest, Success: false, Message: "A valid tenant ID is required.", ErrorCode: errors.InvalidPayload}
		response.JSON(w)
		return
	}

	policies, err := controller.UserQueryServiceInterface.GetRegistrationPolicies(r.Context(), tenantID)
	if err != nil {
		writePolicyQueryError(w, err)
		return
	}
	response := viewmodels.HTTPResponseVM{Status: http.StatusOK, Success: true, Message: "Successfully fetched registration policies.", Data: policyDefinitionResponses(policies)}
	response.JSON(w)
}

// GetCurrentUserPolicyStatus returns the signed-in user's current policy state.
func (controller *UserQueryController) GetCurrentUserPolicyStatus(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)
	controller.getPolicyStatus(w, r, tenantID, userID)
}

// GetTenantUserPolicyStatus gives tenant administrators audit visibility into
// current-version acceptance without exposing request metadata.
func (controller *UserQueryController) GetTenantUserPolicyStatus(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	controller.getPolicyStatus(w, r, tenantID, strings.TrimSpace(chi.URLParam(r, "ID")))
}

func (controller *UserQueryController) getPolicyStatus(w http.ResponseWriter, r *http.Request, tenantID, userID string) {
	if userID == "" || len(userID) > 128 {
		response := viewmodels.HTTPResponseVM{Status: http.StatusBadRequest, Success: false, Message: "A valid user ID is required.", ErrorCode: errors.InvalidPayload}
		response.JSON(w)
		return
	}
	status, err := controller.UserQueryServiceInterface.GetPolicyStatus(r.Context(), tenantID, userID)
	if err != nil {
		writePolicyQueryError(w, err)
		return
	}
	items := make([]types.PolicyStatusItemResponse, 0, len(status.Policies))
	for _, item := range status.Policies {
		items = append(items, types.PolicyStatusItemResponse{
			PolicyDefinitionResponse: policyDefinitionResponse(item.PolicyDefinition),
			Accepted:                 item.Accepted, AcceptedAt: item.AcceptedAt,
		})
	}
	response := viewmodels.HTTPResponseVM{Status: http.StatusOK, Success: true, Message: "Successfully fetched policy status.", Data: types.PolicyStatusResponse{Policies: items, AcceptanceRequired: status.AcceptanceRequired, EnforcementActive: status.EnforcementActive}}
	response.JSON(w)
}

func policyDefinitionResponses(policies []serviceTypes.PolicyDefinition) []types.PolicyDefinitionResponse {
	responses := make([]types.PolicyDefinitionResponse, 0, len(policies))
	for _, policy := range policies {
		responses = append(responses, policyDefinitionResponse(policy))
	}
	return responses
}

func policyDefinitionResponse(policy serviceTypes.PolicyDefinition) types.PolicyDefinitionResponse {
	return types.PolicyDefinitionResponse{
		PolicyKey: policy.PolicyKey, Version: policy.Version, Title: policy.Title, URL: policy.URL,
		EffectiveAt: policy.EffectiveAt, AcceptanceAction: policy.AcceptanceAction, Required: policy.Required,
	}
}

func writePolicyQueryError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "Unable to retrieve policy status."
	if err.Error() == errors.PolicyConfigurationUnavailable {
		status = http.StatusServiceUnavailable
		message = "Policy information is temporarily unavailable."
	}
	response := viewmodels.HTTPResponseVM{Status: status, Success: false, Message: message, ErrorCode: err.Error()}
	response.JSON(w)
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
			AccessState:       res.AccessState,
			Name:              res.Name,
			Email:             res.Email,
			LicenseNo:         res.LicenseNo,
			Specialty:         res.Specialty,
			IsEmailVerified:   res.IsEmailVerified,
			IsAccountDisabled: res.IsAccountDisabled,
			IsConsentSigned:   res.IsConsentSigned,
			IsAdminCreated:    res.IsAdminCreated,
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
			AccessState:       user.AccessState,
			Name:              user.Name,
			Email:             user.Email,
			LicenseNo:         user.LicenseNo,
			Specialty:         user.Specialty,
			IsEmailVerified:   user.IsEmailVerified,
			IsAccountDisabled: user.IsAccountDisabled,
			IsConsentSigned:   user.IsConsentSigned,
			IsAdminCreated:    user.IsAdminCreated,
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

// GetTenantUserEmailInvites get tenant user email invites
func (controller *UserQueryController) GetTenantUserEmailInvites(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	res, err := controller.UserQueryServiceInterface.GetTenantUserEmailInvites(context.TODO(), tenantID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while fetching tenant user email invites."
		case errors.FirestoreError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while fetching tenant user email invites."
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

	var userEmailInvites []types.GetTenantUserEmailInviteResponse

	for _, invite := range res {
		var verifiedAt *uint64
		if invite.VerifiedAt != nil {
			verifiedAtVal := uint64(*invite.VerifiedAt)
			verifiedAt = &verifiedAtVal
		}

		userEmailInvites = append(userEmailInvites, types.GetTenantUserEmailInviteResponse{
			ID:         invite.ID,
			TenantID:   invite.TenantID,
			Code:       invite.Code,
			Email:      invite.Email,
			ExpiresAt:  uint64(invite.ExpiresAt),
			VerifiedAt: verifiedAt,
			CreatedAt:  uint64(invite.CreatedAt),
			UpdatedAt:  uint64(invite.UpdatedAt),
		})
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully fetched tenant user email invites.",
		Data:    userEmailInvites,
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
