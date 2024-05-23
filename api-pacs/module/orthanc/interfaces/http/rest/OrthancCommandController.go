package rest

import (
	"api-pacs/module/orthanc/application"
)

// OrthancCommandController request controller for orthanc command
type OrthancCommandController struct {
	application.OrthancCommandServiceInterface
}

// // CreateRecord request handler to create record
// func (controller *RecordCommandController) CreateRecord(w http.ResponseWriter, r *http.Request) {
// 	var request types.CreateRecordRequest

// 	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
// 		response := viewmodels.HTTPResponseVM{
// 			Status:    http.StatusBadRequest,
// 			Success:   false,
// 			Message:   "Invalid payload request.",
// 			ErrorCode: apiError.InvalidRequestPayload,
// 		}

// 		response.JSON(w)
// 		return
// 	}

// 	// validate request
// 	err := types.Validate.Struct(request)
// 	if err != nil {
// 		errors := err.(validator.ValidationErrors)
// 		if len(errors) > 0 {
// 			response := viewmodels.HTTPResponseVM{
// 				Status:    http.StatusBadRequest,
// 				Success:   false,
// 				Message:   types.ValidationErrors[errors[0].StructNamespace()],
// 				ErrorCode: apiError.InvalidPayload,
// 			}

// 			response.JSON(w)
// 			return
// 		}

// 		response := viewmodels.HTTPResponseVM{
// 			Status:    http.StatusBadRequest,
// 			Success:   false,
// 			Message:   "Invalid payload request.",
// 			ErrorCode: apiError.InvalidRequestPayload,
// 		}

// 		response.JSON(w)
// 		return
// 	}

// 	record := serviceTypes.CreateRecord{
// 		ID:   request.ID,
// 		Data: request.Data,
// 	}

// 	res, err := controller.RecordCommandServiceInterface.CreateRecord(context.TODO(), record)
// 	if err != nil {
// 		var httpCode int
// 		var errorMsg string

// 		switch err.Error() {
// 		case errors.DatabaseError:
// 			httpCode = http.StatusInternalServerError
// 			errorMsg = "Error occurred while saving record."
// 		case errors.DuplicateRecord:
// 			httpCode = http.StatusConflict
// 			errorMsg = "Record ID already exist."
// 		default:
// 			httpCode = http.StatusInternalServerError
// 			errorMsg = "Please contact technical support."
// 		}

// 		response := viewmodels.HTTPResponseVM{
// 			Status:    httpCode,
// 			Success:   false,
// 			Message:   errorMsg,
// 			ErrorCode: err.Error(),
// 		}

// 		response.JSON(w)
// 		return
// 	}

// 	response := viewmodels.HTTPResponseVM{
// 		Status:  http.StatusOK,
// 		Success: true,
// 		Message: "Successfully created record.",
// 		Data: &types.RecordResponse{
// 			ID:        res.ID,
// 			Data:      res.Data,
// 			CreatedAt: time.Now().Unix(),
// 		},
// 	}

// 	response.JSON(w)
// }
