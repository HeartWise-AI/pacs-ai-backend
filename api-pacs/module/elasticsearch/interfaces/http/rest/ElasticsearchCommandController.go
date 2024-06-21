package rest

import (
	"api-pacs/interfaces/http/rest/viewmodels"
	"api-pacs/module/elasticsearch/application"
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-playground/validator/v10"
)

// ElasticsearchCommandController request controller for elasticsearch command
type ElasticsearchCommandController struct {
	application.ElasticsearchCommandServiceInterface
}

// RetrieveModalityStudy retrieve modality study
func (controller *ElasticsearchCommandController) CreateDataView(w http.ResponseWriter, r *http.Request) {
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
		case apiError.OrthancError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Orthanc service encountered an error or timeout."
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
