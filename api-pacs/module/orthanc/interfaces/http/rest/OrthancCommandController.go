package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi"
	"github.com/go-playground/validator/v10"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/orthanc/application"
	serviceTypes "api-pacs/module/orthanc/infrastructure/service/types"
	types "api-pacs/module/orthanc/interfaces/http"
)

// OrthancCommandController request controller for orthanc command
type OrthancCommandController struct {
	application.OrthancCommandServiceInterface
}

// RetrieveModalityStudy retrieve modality study
func (controller *OrthancCommandController) RetrieveModalityStudy(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value(iamTypes.TenantIDCtx).(string)
	userID := r.Context().Value(iamTypes.UserIDCtx).(string)

	queryID := chi.URLParam(r, "queryID")
	if len(queryID) == 0 {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid query ID.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	answerIndex, err := strconv.Atoi(chi.URLParam(r, "answerIndex"))
	if err != nil {
		response := viewmodels.HTTPResponseVM{
			Status:    http.StatusBadRequest,
			Success:   false,
			Message:   "Invalid answer index.",
			ErrorCode: apiError.InvalidRequestPayload,
		}

		response.JSON(w)
		return
	}

	var request types.RetrieveModalityStudyRequest

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
	err = types.Validate.Struct(request)
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

	res, err := controller.OrthancCommandServiceInterface.RetrieveModalityStudy(context.TODO(), serviceTypes.RetrieveModalityStudy{
		TenantID:         tenantID,
		UserID:           userID,
		QueryID:          queryID,
		AnswerIndex:      uint(answerIndex),
		StudyInstanceUID: request.StudyInstanceUID,
	})
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DuplicateRecord:
			httpCode = http.StatusConflict
			errorMsg = "Skipping retrieve."
		default:
			httpCode = http.StatusInternalServerError
			errorMsg = "Server error."
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
		Message: "Successfully retrieved modality study.",
		Data: &types.RetrieveQueryModalityAnswerResponse{
			ID:   res.ID,
			Path: res.Path,
		},
	}

	response.JSON(w)
}
