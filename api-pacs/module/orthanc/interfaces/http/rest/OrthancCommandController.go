package rest

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi"

	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/orthanc/application"
	types "api-pacs/module/orthanc/interfaces/http"
)

// OrthancCommandController request controller for orthanc command
type OrthancCommandController struct {
	application.OrthancCommandServiceInterface
}

// RetrieveModalityStudy retrieve modality study
func (controller *OrthancCommandController) RetrieveModalityStudy(w http.ResponseWriter, r *http.Request) {
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

	res, err := controller.OrthancCommandServiceInterface.RetrieveModalityStudy(context.TODO(), queryID, uint(answerIndex))
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
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
