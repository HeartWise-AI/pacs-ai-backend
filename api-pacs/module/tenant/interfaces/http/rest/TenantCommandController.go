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
	"api-pacs/module/tenant/application"
	"api-pacs/module/tenant/domain/entity"
	serviceTypes "api-pacs/module/tenant/infrastructure/service/types"
	types "api-pacs/module/tenant/interfaces/http"
)

// TenantCommandController request controller for record command
type TenantCommandController struct {
	application.TenantCommandServiceInterface
}

// AddOnboardingQuestionnaireAnswers adds onboarding questionnaire answers
func (controller *TenantCommandController) AddOnboardingQuestionnaireAnswers(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	var request types.AddOnboardingQuestionnaireAnswerRequest
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

	// validate questionnaire type
	if request.QuestionnaireType != entity.PreSurveyQuestionnaireType && request.QuestionnaireType != entity.PostSurveyQuestionnaireType {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid questionnaire type.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	var onboardingQuestionnaireAnswers []serviceTypes.OnboardingQuestionnaireAnswer

	if len(request.OnboardingQuestionnaireAnswers) > 0 {
		for _, answer := range request.OnboardingQuestionnaireAnswers {
			onboardingQuestionnaireAnswer := serviceTypes.OnboardingQuestionnaireAnswer{
				QuestionnaireID:        answer.QuestionnaireID,
				QuestionnaireQuestion:  answer.QuestionnaireQuestion,
				QuestionnaireAnswerIDs: answer.QuestionnaireAnswerIDs,
				QuestionnaireAnswers:   answer.QuestionnaireAnswers,
			}

			onboardingQuestionnaireAnswers = append(onboardingQuestionnaireAnswers, onboardingQuestionnaireAnswer)
		}
	}

	err = controller.TenantCommandServiceInterface.AddOnboardingQuestionnaireAnswers(context.TODO(), serviceTypes.AddOnboardingQuestionnaireAnswer{
		TenantID:                       tenantID,
		UserID:                         userID,
		QuestionnaireType:              request.QuestionnaireType,
		OnboardingQuestionnaireAnswers: onboardingQuestionnaireAnswers,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while saving onboarding questionnaire answer."
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
		Message: "Successfully saved onboarding questionnaire answer.",
	}

	response.JSON(w)
}

// UpdateOnboardingConsentConfig updates the onboarding consent configuration
func (controller *TenantCommandController) UpdateOnboardingConsentConfig(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	var request types.UpdateOnboardingConsentConfigRequest
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

	err = controller.TenantCommandServiceInterface.UpdateOnboardingConsentConfig(context.TODO(), serviceTypes.UpdateOnboardingConsentConfig{
		TenantID:                tenantID,
		OnboardingEnableConsent: request.OnboardingEnableConsent,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while updating onboarding consent configuration."
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
		Message: "Successfully updated onboarding consent configuration.",
	}

	response.JSON(w)
}

// UpdateOnboardingRegistrationConfig updates the onboarding registration configuration
func (controller *TenantCommandController) UpdateOnboardingRegistrationConfig(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)

	var request types.UpdateOnboardingRegistrationConfigRequest
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

	err = controller.TenantCommandServiceInterface.UpdateOnboardingRegistrationConfig(context.TODO(), serviceTypes.UpdateOnboardingRegistrationConfig{
		TenantID:                     tenantID,
		OnboardingEnableRegistration: request.OnboardingEnableRegistration,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while updating onboarding registration configuration."
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
		Message: "Successfully updated onboarding registration configuration.",
	}

	response.JSON(w)
}
