package rest

import (
	"context"
	"net/http"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/internal/errors"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/tenant/application"
	"api-pacs/module/tenant/domain/entity"
	serviceTypes "api-pacs/module/tenant/infrastructure/service/types"
	types "api-pacs/module/tenant/interfaces/http"
)

// TenantQueryController request controller for record query
type TenantQueryController struct {
	application.TenantQueryServiceInterface
}

// GetOnboardingQuestionnaireAnswers gets the onboarding questionnaire answers
func (controller *TenantQueryController) GetOnboardingQuestionnaireAnswers(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	// required
	questionnaireTypeStr := r.URL.Query().Get("questionnaireType")
	if len(questionnaireTypeStr) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid questionnaire type.",
			ErrorCode: errors.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	questionnaireType := entity.QuestionnaireType(questionnaireTypeStr)

	res, err := controller.TenantQueryServiceInterface.GetOnboardingQuestionnaireAnswers(r.Context(), serviceTypes.GetOnboardingQuestionnaireAnswer{
		TenantID:          tenantID,
		UserID:            userID,
		QuestionnaireType: &questionnaireType,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.FirestoreError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Firestore service encountered an error."
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

	onboardingQuestionnaireAnswers := []types.GetOnboardingQuestionnaireAnswerResponse{}

	for _, answer := range res {
		onboardingQuestionnaireAnswers = append(onboardingQuestionnaireAnswers, types.GetOnboardingQuestionnaireAnswerResponse{
			ID:                     answer.ID,
			TenantID:               answer.TenantID,
			UserID:                 answer.UserID,
			QuestionnaireType:      answer.QuestionnaireType,
			QuestionnaireID:        answer.QuestionnaireID,
			QuestionnaireQuestion:  answer.QuestionnaireQuestion,
			QuestionnaireAnswerIDs: answer.QuestionnaireAnswerIDs,
			QuestionnaireAnswers:   answer.QuestionnaireAnswers,
			CreatedAt:              uint64(answer.CreatedAt),
			UpdatedAt:              uint64(answer.UpdatedAt),
		})
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully retrieved onboarding questionnaire answers.",
		Data:    onboardingQuestionnaireAnswers,
	}

	response.JSON(w)
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

	onboardingQuestionnaires := map[string][]types.OnboardingQuestionnaire{}
	for questionnaireType, questionnaires := range res.OnboardingQuestionnaires {
		var questionnairesRes []types.OnboardingQuestionnaire

		for _, questionnaire := range questionnaires {
			var answerOptionsEn []types.AnswerOption
			for _, answerOption := range questionnaire.AnswerOptionsEn {
				answerOptionsEn = append(answerOptionsEn, types.AnswerOption{
					ID:     answerOption.ID,
					Answer: answerOption.Answer,
				})
			}

			var answerOptionsFr []types.AnswerOption
			for _, answerOption := range questionnaire.AnswerOptionsFr {
				answerOptionsFr = append(answerOptionsFr, types.AnswerOption{
					ID:     answerOption.ID,
					Answer: answerOption.Answer,
				})
			}

			questionnairesRes = append(questionnairesRes, types.OnboardingQuestionnaire{
				ID:              questionnaire.ID,
				Type:            questionnaire.Type,
				QuestionEn:      questionnaire.QuestionEn,
				QuestionFr:      questionnaire.QuestionFr,
				AnswerOptionsEn: answerOptionsEn,
				AnswerOptionsFr: answerOptionsFr,
			})
		}

		onboardingQuestionnaires[questionnaireType] = questionnairesRes
	}

	response := viewmodels.HTTPResponseVM{
		Status:  http.StatusOK,
		Success: true,
		Message: "Successfully fetched tenant by id.",
		Data: &types.GetTenantResponse{
			ID:                       res.ID,
			Name:                     res.Name,
			Address:                  res.Address,
			OnboardingQuestionnaires: onboardingQuestionnaires,
			CreatedAt:                res.CreatedAt,
			UpdatedAt:                res.UpdatedAt,
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
