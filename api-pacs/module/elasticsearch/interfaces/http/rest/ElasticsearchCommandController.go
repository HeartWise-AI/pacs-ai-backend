package rest

import (
	"context"
	"net/http"

	"api-pacs/interfaces/http/rest/viewmodels"
	apiError "api-pacs/internal/errors"
	"api-pacs/module/elasticsearch/application"
)

// ElasticsearchCommandController request controller for elasticsearch command
type ElasticsearchCommandController struct {
	application.ElasticsearchCommandServiceInterface
}

// RetrieveModalityStudy retrieve modality study
func (controller *ElasticsearchCommandController) CreateDataView(w http.ResponseWriter, r *http.Request) {
	err := controller.ElasticsearchCommandServiceInterface.CreateDataView(context.TODO())
	if err != nil {
		var httpCode int
		var errorMsg string

		switch err.Error() {
		case apiError.DatabaseError:
			httpCode = http.StatusInternalServerError
			errorMsg = "Server Error."
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
		Message: "Successfully created data views.",
	}

	response.JSON(w)
}
