package dockerinference

import (
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

var client *http.Client = &http.Client{Timeout: 3 * time.Minute}

// GetModelInfo gets the model info from the docker inference API
func (d *DockerInferenceAPI) GetModelInfo(ctx context.Context, containerName string) (types.GetModelInfoResponse, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/inference/model-info", containerName), nil)
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
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://%s/inference/model-facts", containerName), nil)
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

// TODO: Implement the predict function
