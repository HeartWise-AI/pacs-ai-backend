package dockerinference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	types "api-pacs/infrastructures/providers/api/dockerinference/types"
	apiError "api-pacs/internal/errors"
)

type DockerInferenceAPI struct{}

var client *http.Client = &http.Client{}

// GetModelInfo gets the model info from the docker inference API
func (d *DockerInferenceAPI) GetModelInfo(ctx context.Context, containerName string) (types.GetModelInfoResponse, error) {
	// set timeout
	client.Timeout = 2 * time.Second

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/inference/model-info", containerName), nil)
	if err != nil {
		log.Printf("Error: %v", err)
		return types.GetModelInfoResponse{}, errors.New(apiError.DockerInferenceError)
	}

	// set headers
	req.Header.Set("Content-Type", "application/json")

	// request with context
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		log.Printf("Error: %v", err)
		return types.GetModelInfoResponse{}, errors.New(apiError.DockerInferenceError)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Error: %v", err)
			return types.GetModelInfoResponse{}, errors.New(apiError.DockerInferenceError)
		}
		errorMessage := string(response)
		log.Printf("Response: %v", errorMessage)

		return types.GetModelInfoResponse{}, errors.New(apiError.DockerInferenceError)
	}

	var response types.GetModelInfoResponse
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		log.Printf("Error: %v", err)
		return types.GetModelInfoResponse{}, errors.New(apiError.DockerInferenceError)
	}

	return response, nil
}

// GetModelFacts gets the model facts from the docker inference API
func (d *DockerInferenceAPI) GetModelFacts(ctx context.Context, containerName string) (types.GetModelFactsResponse, error) {
	// set timeout
	client.Timeout = 2 * time.Second

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/api/inference/model-facts", containerName), nil)
	if err != nil {
		log.Printf("Error: %v", err)
		return types.GetModelFactsResponse{}, errors.New(apiError.DockerInferenceError)
	}

	// set headers
	req.Header.Set("Content-Type", "application/json")

	// request with context
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		log.Printf("Error: %v", err)
		return types.GetModelFactsResponse{}, errors.New(apiError.DockerInferenceError)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Error: %v", err)
			return types.GetModelFactsResponse{}, errors.New(apiError.DockerInferenceError)
		}
		errorMessage := string(response)
		log.Printf("Response: %v", errorMessage)

		return types.GetModelFactsResponse{}, errors.New(apiError.DockerInferenceError)
	}

	var response types.GetModelFactsResponse
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		log.Printf("Error: %v", err)
		return types.GetModelFactsResponse{}, errors.New(apiError.DockerInferenceError)
	}

	return response, nil
}

// Predict predicts the result from the docker inference API
func (d *DockerInferenceAPI) Predict(ctx context.Context, containerName string, request types.PredictRequest) (types.PredictResponse, error) {
	// set timeout
	client.Timeout = 5 * time.Minute

	buf := new(bytes.Buffer)
	err := json.NewEncoder(buf).Encode(request)
	if err != nil {
		log.Println("Error:", err)
		return types.PredictResponse{}, errors.New(apiError.DockerInferenceError)
	}

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://%s/api/inference/predict", containerName), buf)
	if err != nil {
		log.Println("Error:", err)
		return types.PredictResponse{}, errors.New(apiError.DockerInferenceError)
	}

	// set headers
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Println("Error:", err)
		return types.PredictResponse{}, errors.New(apiError.DockerInferenceError)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		response, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Error: %v", err)
			return types.PredictResponse{}, errors.New(apiError.DockerInferenceError)
		}
		errorMessage := string(response)
		log.Printf("Response: %v", errorMessage)

		return types.PredictResponse{}, errors.New(apiError.DockerInferenceError)
	}

	var response types.PredictResponse
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		log.Println("Error:", err)
		return types.PredictResponse{}, errors.New(apiError.DockerInferenceError)
	}

	return response, nil
}
