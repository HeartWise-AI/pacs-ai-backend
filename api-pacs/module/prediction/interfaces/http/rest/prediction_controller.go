package rest

import (
	"api-pacs/module/prediction/infrastructure/service"
	"api-pacs/module/prediction/types"
	"encoding/json"
	"io/ioutil"
	"log"
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
	// Read the request body
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		http.Error(w, "Failed to read request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse the JSON request
	var predReq PredictionRequest
	err = json.Unmarshal(body, &predReq)
	if err != nil {
		log.Printf("Error parsing JSON: %v", err)
		http.Error(w, "Failed to parse request", http.StatusBadRequest)
		return
	}

	// Create DicomInputData with the UID
	inputData := types.DicomInputData{
		UID: predReq.DicomUID,
	}

	// Call the service to create the prediction
	prediction, err := c.PredictionCommandServiceInterface.CreatePrediction(inputData)
	if err != nil {
		log.Printf("Error creating prediction: %v", err)
		http.Error(w, "Failed to create prediction", http.StatusInternalServerError)
		return
	}

	log.Printf("Prediction created: %v", prediction)

	// Return the prediction
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
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
