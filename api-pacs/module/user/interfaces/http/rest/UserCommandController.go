package rest

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/middlewares/requestbody"
	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/iam/domain/entity"
	"api-pacs/module/user/application"
	userEntity "api-pacs/module/user/domain/entity"
	serviceTypes "api-pacs/module/user/infrastructure/service/types"
	types "api-pacs/module/user/interfaces/http"
)

// UserCommandController request controller for user command
type UserCommandController struct {
	application.UserCommandServiceInterface
	TrustedProxyCIDRs []*net.IPNet
}

const maxPublicRegistrationBodyBytes = 16 << 10

// AcceptPolicies records acceptance of every current required policy version.
func (controller *UserCommandController) AcceptPolicies(w http.ResponseWriter, r *http.Request) {
	var request types.AcceptPoliciesRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxPublicRegistrationBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeInvalidPolicyAcceptanceRequest(w)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeInvalidPolicyAcceptanceRequest(w)
		return
	}
	for index := range request.Acceptances {
		request.Acceptances[index].PolicyKey = strings.TrimSpace(request.Acceptances[index].PolicyKey)
		request.Acceptances[index].Version = strings.TrimSpace(request.Acceptances[index].Version)
	}
	if err := types.Validate.Struct(request); err != nil {
		writeInvalidPolicyAcceptanceRequest(w)
		return
	}

	acceptances := make([]serviceTypes.PolicyAcceptanceInput, 0, len(request.Acceptances))
	for _, acceptance := range request.Acceptances {
		acceptances = append(acceptances, serviceTypes.PolicyAcceptanceInput{PolicyKey: acceptance.PolicyKey, Version: acceptance.Version})
	}
	err := controller.UserCommandServiceInterface.AcceptPolicies(r.Context(), serviceTypes.AcceptPolicies{
		TenantID:    r.Context().Value(iamTypes.TenantIDCtx).(string),
		UserID:      r.Context().Value(iamTypes.UserIDCtx).(string),
		Source:      userEntity.PolicyAcceptanceSourceAuthenticated,
		Acceptances: acceptances,
	})
	if err != nil {
		status := http.StatusInternalServerError
		message := "Unable to record policy acceptance."
		switch err.Error() {
		case apiError.PolicyAcceptanceRequired:
			status, message = http.StatusPreconditionRequired, "Acceptance of all current policies is required."
		case apiError.PolicyVersionStale:
			status, message = http.StatusConflict, "The policies have changed. Please review the current versions."
		case apiError.InvalidPayload:
			status, message = http.StatusBadRequest, "Invalid policy acceptance payload."
		case apiError.PolicyConfigurationUnavailable:
			status, message = http.StatusServiceUnavailable, "Policy information is temporarily unavailable."
		}
		response := viewmodels.HTTPResponseVM{Status: status, Success: false, Message: message, ErrorCode: err.Error()}
		response.JSON(w)
		return
	}

	response := viewmodels.HTTPResponseVM{Status: http.StatusOK, Success: true, Message: "Successfully recorded policy acceptance."}
	response.JSON(w)
}

func writeInvalidPolicyAcceptanceRequest(w http.ResponseWriter) {
	response := viewmodels.HTTPResponseVM{Status: http.StatusBadRequest, Success: false, Message: "Invalid policy acceptance payload.", ErrorCode: apiError.InvalidRequestPayload}
	response.JSON(w)
}

// CreateTenantOwner create a tenant owner. Only callable by superuser.
func (controller *UserCommandController) CreateTenantOwner(w http.ResponseWriter, r *http.Request) {
	var request types.CreateTenantOwnerRequest

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
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

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
		TenantID:  tenantID,
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
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	actorUserID := r.Context().Value(iamTypes.UserIDCtx).(string)
	actorRole := r.Context().Value(iamTypes.RoleCtx).(string)

	userID := chi.URLParam(r, "ID")
	if len(userID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid user ID.",
			ErrorCode: errors.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err := controller.UserCommandServiceInterface.DeleteTenantUser(r.Context(), serviceTypes.DeleteTenantUser{
		TenantID: tenantID, ActorUserID: actorUserID, ActorRole: actorRole, TargetUserID: userID,
	})
	if err != nil {
		status := http.StatusInternalServerError
		message := "Database error."
		if err.Error() == apiError.ForbiddenAccess || err.Error() == apiError.UnauthorizedAccess {
			status = http.StatusForbidden
			message = "Forbidden access."
		} else if err.Error() == apiError.AccountAccessTransitionInProgress {
			status = http.StatusConflict
			message = "Another account access change is already in progress."
		}
		response := viewmodels.HTTPResponseVM{
			Status:    status,
			Success:   false,
			Message:   message,
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

// SuspendTenantUser immediately blocks new and existing platform sessions.
func (controller *UserCommandController) SuspendTenantUser(w http.ResponseWriter, r *http.Request) {
	controller.changeTenantUserAccess(w, r, userEntity.AccountAccessSuspended)
}

// ReactivateTenantUser restores login without changing the user's profile or role.
func (controller *UserCommandController) ReactivateTenantUser(w http.ResponseWriter, r *http.Request) {
	controller.changeTenantUserAccess(w, r, userEntity.AccountAccessActive)
}

func (controller *UserCommandController) changeTenantUserAccess(w http.ResponseWriter, r *http.Request, accessState string) {
	targetUserID := chi.URLParam(r, "ID")
	if targetUserID == "" {
		writeUserAccessError(w, http.StatusBadRequest, "Invalid user ID.", apiError.InvalidRequestPayload)
		return
	}

	var request types.ChangeTenantUserAccessRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil && err != io.EOF {
		writeUserAccessError(w, http.StatusBadRequest, "Invalid payload request.", apiError.InvalidRequestPayload)
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if err := types.Validate.Struct(request); err != nil {
		writeUserAccessError(w, http.StatusBadRequest, "Invalid payload request.", apiError.InvalidPayload)
		return
	}

	err := controller.UserCommandServiceInterface.ChangeTenantUserAccess(r.Context(), serviceTypes.ChangeTenantUserAccess{
		TenantID:     r.Context().Value(iamTypes.TenantIDCtx).(string),
		ActorUserID:  r.Context().Value(iamTypes.UserIDCtx).(string),
		ActorRole:    r.Context().Value(iamTypes.RoleCtx).(string),
		TargetUserID: targetUserID,
		AccessState:  accessState,
		Reason:       request.Reason,
	})
	if err != nil {
		switch err.Error() {
		case apiError.ForbiddenAccess, apiError.UnauthorizedAccess:
			writeUserAccessError(w, http.StatusForbidden, "Forbidden access.", err.Error())
		case apiError.MissingRecord:
			writeUserAccessError(w, http.StatusNotFound, "User not found.", err.Error())
		case apiError.AccountAccessTransitionInProgress:
			writeUserAccessError(w, http.StatusConflict, "Another account access change is already in progress.", err.Error())
		default:
			writeUserAccessError(w, http.StatusInternalServerError, "Unable to update account access.", err.Error())
		}
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status: http.StatusOK, Success: true, Message: "Successfully updated tenant user access.",
		Data: map[string]string{"userId": targetUserID, "accessState": accessState},
	}
	response.JSON(w)
}

func writeUserAccessError(w http.ResponseWriter, status int, message, code string) {
	response := viewmodels.HTTPResponseVM{Status: status, Success: false, Message: message, ErrorCode: code}
	response.JSON(w)
}

// RegisterTenantUser registers a tenant user
func (controller *UserCommandController) RegisterTenantUser(w http.ResponseWriter, r *http.Request) {
	var request types.RegisterTenantUserRequest

	r.Body = http.MaxBytesReader(w, r.Body, maxPublicRegistrationBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		if requestbody.IsTooLarge(err) {
			requestbody.ObserveRejection(r, maxPublicRegistrationBodyBytes, "registration")
			requestbody.WriteTooLarge(w)
			return
		}
		writeInvalidRegistrationRequest(w)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if requestbody.IsTooLarge(err) {
			requestbody.ObserveRejection(r, maxPublicRegistrationBodyBytes, "registration")
			requestbody.WriteTooLarge(w)
			return
		}
		writeInvalidRegistrationRequest(w)
		return
	}

	request.TenantID = strings.TrimSpace(request.TenantID)
	request.TurnstileToken = strings.TrimSpace(request.TurnstileToken)
	request.Name = strings.TrimSpace(request.Name)
	request.Email = strings.ToLower(strings.TrimSpace(request.Email))
	request.LicenseNo = strings.TrimSpace(request.LicenseNo)
	request.Specialty = strings.TrimSpace(request.Specialty)
	if request.Code != nil {
		code := strings.TrimSpace(*request.Code)
		request.Code = &code
	}
	for index := range request.PolicyAcceptances {
		request.PolicyAcceptances[index].PolicyKey = strings.TrimSpace(request.PolicyAcceptances[index].PolicyKey)
		request.PolicyAcceptances[index].Version = strings.TrimSpace(request.PolicyAcceptances[index].Version)
	}

	// validate request
	err := types.Validate.Struct(request)
	if err != nil {
		validationErrors, ok := err.(validator.ValidationErrors)
		if ok && len(validationErrors) > 0 {
			message := types.ValidationErrors[validationErrors[0].StructNamespace()]
			if message == "" {
				message = "Invalid payload request."
			}
			response := viewmodels.HTTPResponseVM{
				Status:    http.StatusBadRequest,
				Success:   false,
				Message:   message,
				ErrorCode: apiError.InvalidPayload,
			}

			response.JSON(w)
			return
		}

		writeInvalidRegistrationRequest(w)
		return
	}

	policyAcceptances := make([]serviceTypes.PolicyAcceptanceInput, 0, len(request.PolicyAcceptances))
	for _, acceptance := range request.PolicyAcceptances {
		policyAcceptances = append(policyAcceptances, serviceTypes.PolicyAcceptanceInput{PolicyKey: acceptance.PolicyKey, Version: acceptance.Version})
	}
	err = controller.UserCommandServiceInterface.RegisterTenantUser(r.Context(), serviceTypes.RegisterTenantUser{
		TenantID:          request.TenantID,
		TurnstileToken:    request.TurnstileToken,
		ClientIP:          controller.registrationClientIP(r),
		Name:              request.Name,
		Email:             request.Email,
		Password:          request.Password,
		LicenseNo:         request.LicenseNo,
		Specialty:         request.Specialty,
		Code:              request.Code,
		PolicyAcceptances: policyAcceptances,
	})
	if err != nil {
		var httpCode int
		var errorMsg string
		var rateLimitError *apiError.RegistrationRateLimitError

		switch {
		case stderrors.As(err, &rateLimitError):
			httpCode = http.StatusTooManyRequests
			errorMsg = "Too many registration attempts. Please try again later."
			w.Header().Set("Retry-After", strconv.Itoa(rateLimitError.RetryAfterSeconds))
		case err.Error() == errors.InvalidPayload:
			httpCode = http.StatusBadRequest
			errorMsg = "Invalid registration payload."
		case err.Error() == errors.TurnstileInvalid:
			httpCode = http.StatusBadRequest
			errorMsg = "Registration verification failed."
		case err.Error() == errors.CloudflareAPIError:
			httpCode = http.StatusServiceUnavailable
			errorMsg = "Registration verification is temporarily unavailable."
		case err.Error() == errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while registering tenant user."
		case err.Error() == errors.DuplicateRecord:
			httpCode = http.StatusConflict
			errorMsg = "User already exist."
		case err.Error() == errors.UnauthorizedAccess:
			httpCode = http.StatusUnauthorized
			errorMsg = "Unauthorized access."
		case err.Error() == errors.PolicyAcceptanceRequired:
			httpCode = http.StatusPreconditionRequired
			errorMsg = "Acceptance of all current policies is required."
		case err.Error() == errors.PolicyVersionStale:
			httpCode = http.StatusConflict
			errorMsg = "The policies have changed. Please review the current versions."
		case err.Error() == errors.PolicyConfigurationUnavailable:
			httpCode = http.StatusServiceUnavailable
			errorMsg = "Policy information is temporarily unavailable."
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
		Message: "Successfully registered tenant user.",
	}

	response.JSON(w)
}

func writeInvalidRegistrationRequest(w http.ResponseWriter) {
	response := viewmodels.HTTPResponseVM{
		Status:    http.StatusBadRequest,
		Success:   false,
		Message:   "Invalid payload request.",
		ErrorCode: apiError.InvalidRequestPayload,
	}

	response.JSON(w)
}

// ResetTutorial resets the tutorial onboarding questionnaires by user
func (controller *UserCommandController) ResetTutorial(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	err := controller.UserCommandServiceInterface.ResetTutorial(context.TODO(), serviceTypes.ResetTutorial{
		TenantID: tenantID,
		UserID:   userID,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.FirestoreError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while resetting tutorial."
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
		Message: "Successfully reset tutorial.",
	}

	response.JSON(w)
}

// RemoveTenantUserEmailInvite removes a tenant user email invite
func (controller *UserCommandController) RemoveTenantUserEmailInvite(w http.ResponseWriter, r *http.Request) {
	emailInviteID := chi.URLParam(r, "ID")
	if len(emailInviteID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid email invite ID.",
			ErrorCode: errors.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	err := controller.UserCommandServiceInterface.DeleteTenantUserEmailInvite(context.TODO(), emailInviteID)
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.FirestoreError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while removing tenant user email invite."
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
		Message: "Successfully removed tenant user email invite.",
	}

	response.JSON(w)
}

// ResendTenantEmailInvite resends a tenant email invite
func (controller *UserCommandController) ResendTenantEmailInvite(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	var request types.ResendTenantEmailInviteRequest

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

	err = controller.UserCommandServiceInterface.ResendTenantUserEmailInvite(context.TODO(), serviceTypes.ResendTenantUserEmailInvite{
		ID:       request.ID,
		TenantID: tenantID,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.MissingRecord:
			httpCode = http.StatusUnauthorized
			errorMsg = "Unauthorized access."
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while resending tenant user invite."
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
		Message: "Successfully resent tenant user invite.",
	}

	response.JSON(w)
}

// SendTenantEmailInvite sends a tenant email invite
func (controller *UserCommandController) SendTenantEmailInvite(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	var request types.SendTenantEmailInviteRequest

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

	err = controller.UserCommandServiceInterface.SendTenantUserEmailInvite(context.TODO(), serviceTypes.SendTenantUserEmailInvite{
		TenantID: tenantID,
		Email:    strings.ToLower(request.Email),
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while sending tenant email invite."
		case errors.DuplicateRecord:
			httpCode = http.StatusConflict
			errorMsg = "Email already invited or joined."
		default:
			httpCode = http.StatusUnauthorized
			errorMsg = "Unauthorized access."
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
		Message: "Successfully sent tenant email invite.",
	}

	response.JSON(w)
}

// UpdateTenantUser update a tenant user. Only callable by admin or owner.
func (controller *UserCommandController) UpdateTenantUser(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

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
		TenantID:  tenantID,
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

// UpdateTenantUserPassword update a tenant user password. Only callable by admin or owner.
func (controller *UserCommandController) UpdateTenantUserPassword(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

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
		ID:          userID,
		TenantID:    tenantID,
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

// UpdateUserMetadata updates user metadata
func (controller *UserCommandController) UpdateUserMetadata(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	var request types.UpdateUserMetadataRequest

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

	err = controller.UserCommandServiceInterface.UpdateUserMetadata(context.TODO(), serviceTypes.UpdateUserMetadata{
		UserID:   userID,
		Metadata: request.Metadata,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.FirestoreError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while updating user metadata."
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

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully updated user metadata.",
	}

	response.JSON(w)
}
