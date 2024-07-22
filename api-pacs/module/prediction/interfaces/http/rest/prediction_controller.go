package rest

import (
	"api-pacs/module/prediction/infrastructure/service"
	"api-pacs/module/prediction/types"
	"encoding/json"
	"log"
	"net/http"
)

type PredictionController struct {
	PredictionCommandServiceInterface service.PredictionCommandServiceInterface
	PredictionQueryServiceInterface   service.PredictionQueryServiceInterface
}

func (c *PredictionController) HandlePrediction(w http.ResponseWriter, r *http.Request) {
	// For now, we'll just pass an empty DicomInputData
	// You can add the Age field here if needed
	inputData := types.DicomInputData{
		Age: 0, // Set this to a default value or get it from the request
	}

	// Call the service to create the prediction
	prediction, err := c.PredictionCommandServiceInterface.CreatePrediction(r.Context(), inputData)
	if err != nil {
		log.Printf("Error creating prediction: %v", err)
		http.Error(w, "Failed to create prediction", http.StatusInternalServerError)
		return
	}

	// Return the prediction
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(prediction)
}

func (c *PredictionController) GetPrediction(w http.ResponseWriter, r *http.Request) {
	// Get ID from request (you might use a router that provides this functionality)
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
