package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	apiError "api-pacs/internal/errors"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
	types "api-pacs/module/inference/interfaces/http"
)

// AddOnboardingModelQuestionnaireAnswers adds onboarding model questionnaire answers
func (controller *InferenceCommandController) AddOnboardingModelQuestionnaireAnswers(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	var request types.AddOnboardingModelQuestionnaireAnswerRequest
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

	onboardingModelQuestionnaires := []serviceTypes.OnboardingModelQuestionnaireAnswer{}
	for _, answer := range request.OnboardingModelQuestionnaireAnswers {
		onboardingModelQuestionnaire := serviceTypes.OnboardingModelQuestionnaireAnswer{
			QuestionnaireID:        answer.QuestionnaireID,
			QuestionnaireQuestion:  answer.QuestionnaireQuestion,
			QuestionnaireAnswerIDs: answer.QuestionnaireAnswerIDs,
			QuestionnaireAnswers:   answer.QuestionnaireAnswers,
		}

		onboardingModelQuestionnaires = append(onboardingModelQuestionnaires, onboardingModelQuestionnaire)
	}

	err = controller.InferenceCommandServiceInterface.AddOnboardingModelQuestionnaireAnswers(context.TODO(), serviceTypes.AddOnboardingModelQuestionnaireAnswer{
		TenantID:                            tenantID,
		UserID:                              userID,
		ModelID:                             request.ModelID,
		OnboardingModelQuestionnaireAnswers: onboardingModelQuestionnaires,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while adding onboarding model questionnaire answers."
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
		Message: "Successfully added onboarding model questionnaire answers.",
	}

	response.JSON(w)
}

// maximumFeedbackExecutionIDLength bounds the optional executionId query
// parameter, like the executionId of the update payload.
const maximumFeedbackExecutionIDLength = 128

// feedbackExecutionID reads the optional ?executionId= that scopes a vote to
// one report. It is trimmed; ok is false when it is too long.
func feedbackExecutionID(r *http.Request) (string, bool) {
	executionID := strings.TrimSpace(r.URL.Query().Get("executionId"))
	if utf8.RuneCountInString(executionID) > maximumFeedbackExecutionIDLength {
		return "", false
	}
	return executionID, true
}

// RemoveModelFeedback removes the vote of the user on a model version. The
// optional ?executionId= scopes it to one report; without it only the vote
// stored without a report is removed.
func (controller *InferenceCommandController) RemoveModelFeedback(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	modelID := chi.URLParam(r, "modelID")
	if len(modelID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Model id is required.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	executionID, ok := feedbackExecutionID(r)
	if !ok {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Execution ID must be at most 128 characters.",
			ErrorCode: apiError.InvalidPayload,
		}

		response.JSON(w)
		return
	}

	err := controller.InferenceCommandServiceInterface.RemoveModelFeedback(context.TODO(), serviceTypes.RemoveModelFeedback{
		TenantID:    tenantID,
		UserID:      userID,
		ModelID:     modelID,
		ExecutionID: executionID,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while removing model feedback."
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
		Message: "Successfully removed model feedback.",
	}

	response.JSON(w)
}

// UpdateModelFeedback updates model feedback
func (controller *InferenceCommandController) UpdateModelFeedback(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	var request types.UpdateModelFeedbackRequest

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

	// check if there are model feedbacks
	var modelFeedbackAnswers []serviceTypes.ModelFeedbackAnswer

	if len(request.ModelFeedbackAnswers) > 0 {
		for _, answer := range request.ModelFeedbackAnswers {
			modelFeedbackAnswers = append(modelFeedbackAnswers, serviceTypes.ModelFeedbackAnswer{
				ModelFeedbackID:        request.ID,
				QuestionnaireID:        answer.QuestionnaireID,
				QuestionnaireQuestion:  answer.QuestionnaireQuestion,
				QuestionnaireAnswerIDs: answer.QuestionnaireAnswerIDs,
				QuestionnaireAnswers:   answer.QuestionnaireAnswers,
			})
		}
	}

	err = controller.InferenceCommandServiceInterface.UpdateModelFeedback(r.Context(), serviceTypes.UpdateModelFeedback{
		ID:                   request.ID,
		TenantID:             tenantID,
		UserID:               userID,
		InferenceModelID:     request.InferenceModelID,
		ModelID:              request.ModelID,
		FeedbackType:         request.FeedbackType,
		ModelFeedbackAnswers: modelFeedbackAnswers,
		RunID:                strings.TrimSpace(request.RunID),
		ExecutionID:          strings.TrimSpace(request.ExecutionID),
		StudyServiceJobID:    strings.TrimSpace(request.StudyServiceJobID),
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.ForbiddenAccess:
			httpCode = http.StatusForbidden
			errorMsg = "Access to this model feedback is forbidden."
		case errors.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Error occurred while updating model feedback."
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
		Message: "Successfully updated model feedback.",
	}

	response.JSON(w)
}

// ListModelFeedback handles GET /v1/inference/model/feedback/list for
// workspace administrators. The tenant and role always come from the
// authenticated request context, never from the query.
func (controller *InferenceQueryController) ListModelFeedback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	tenantID, ok := worklistTenantID(r)
	if !ok {
		writeModelFeedbackListError(w, apiError.UnauthorizedAccess)
		return
	}
	role, _ := r.Context().Value(iamTypes.RoleCtx).(string)

	values := r.URL.Query()
	from, err := parsePredictionsTime(values.Get("from"))
	if err != nil {
		writeModelFeedbackListError(w, apiError.InvalidPayload)
		return
	}
	to, err := parsePredictionsTime(values.Get("to"))
	if err != nil {
		writeModelFeedbackListError(w, apiError.InvalidPayload)
		return
	}

	list, err := controller.InferenceQueryServiceInterface.ListModelFeedback(r.Context(), serviceTypes.ListModelFeedback{
		TenantID:     tenantID,
		Role:         strings.TrimSpace(role),
		Model:        values.Get("model"),
		FeedbackType: values.Get("feedbackType"),
		From:         from,
		To:           to,
	})
	if err != nil {
		writeModelFeedbackListError(w, err.Error())
		return
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully retrieved model feedback.",
		Data:    list,
	}
	response.JSON(w)
}

func writeModelFeedbackListError(w http.ResponseWriter, errorCode string) {
	status, message := http.StatusInternalServerError, "Please contact technical support."
	switch errorCode {
	case apiError.InvalidPayload, apiError.InvalidRequestPayload:
		status, message = http.StatusBadRequest, "Invalid model feedback query."
	case apiError.UnauthorizedAccess:
		status, message = http.StatusUnauthorized, "Authentication is required."
	case apiError.ForbiddenAccess:
		status, message = http.StatusForbidden, "Only workspace administrators can see model feedback."
	case apiError.FirestoreError:
		message = "Error occurred while retrieving model feedback."
	case apiError.HystrixTimeout:
		status, message = http.StatusServiceUnavailable, "Model feedback is temporarily unavailable."
	default:
		errorCode = apiError.ServerError
	}
	response := viewmodels.HTTPResponseVM{Status: status, Success: false, Message: message, ErrorCode: errorCode}
	response.JSON(w)
}
