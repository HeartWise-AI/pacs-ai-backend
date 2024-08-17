package rest

import (
	"api-pacs/module/prediction/application"
	"encoding/json"
	"net/http"
)

type PredictionCommandController struct {
	application.PredictionCommandServiceInterface
}

func (c *PredictionCommandController) HandlePrediction(w http.ResponseWriter, r *http.Request) {
	// Define type somewhere else
	var predReq struct {
		DicomUID string `json:"dicomUID"`
		QueryID  string `json:"queryID"`
	}

	// Parse the request
	err := json.NewDecoder(r.Body).Decode(&predReq)
	if err != nil {
		http.Error(w, "Failed to parse request", http.StatusBadRequest)
		return
	}

	// Call the service to create the prediction
	prediction, err := c.PredictionCommandServiceInterface.CreatePrediction(predReq.QueryID)
	if err != nil {
		http.Error(w, "Failed to create prediction", http.StatusInternalServerError)
		return
	}

	// Return the prediction
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(prediction)
}
