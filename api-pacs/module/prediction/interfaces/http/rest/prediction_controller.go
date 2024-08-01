package rest

import (
	"api-pacs/module/prediction/infrastructure/service"
	"encoding/json"
	"net/http"
)

type PredictionController struct {
	PredictionCommandServiceInterface service.PredictionCommandServiceInterface
	PredictionQueryServiceInterface   service.PredictionQueryServiceInterface
}

type PredictionRequest struct {
	DicomUID string `json:"dicomUID"`
}

func (c *PredictionController) HandlePrediction(w http.ResponseWriter, r *http.Request) {
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

func (c *PredictionController) GetPrediction(w http.ResponseWriter, r *http.Request) {
	// Get ID from request
	id := r.URL.Query().Get("id")
	// Get prediction
	prediction, err := c.PredictionQueryServiceInterface.GetPrediction(r.Context(), id)
	if err != nil {
		http.Error(w, "Failed to get prediction", http.StatusInternalServerError)
		return
	}
	// Return prediction
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(prediction)
}
