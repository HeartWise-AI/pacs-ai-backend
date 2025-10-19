package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/segmentio/ksuid"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/module/orchestrator/domain/entity"
	"api-pacs/module/orchestrator/infrastructure/service/types"
)

// OrchestratorService implements the orchestrator service
type OrchestratorService struct {
	OrchestratorAPIURL     string
	OrchestratorClient     *http.Client
	ThreadsByID            map[string]*entity.Thread
}

// extractBearerTokenFromContext extracts the bearer token from context
func (service *OrchestratorService) extractBearerTokenFromContext(ctx context.Context) string {
	if bearerToken := ctx.Value(iamTypes.BearerTokenCtx); bearerToken != nil {
		return bearerToken.(string)
	}
	return ""
}

// CreateThread creates a new thread
func (service *OrchestratorService) CreateThread(ctx context.Context, request types.CreateThreadRequest) (types.CreateThreadResponse, error) {
	// Extract bearer token from context
	bearerToken := service.extractBearerTokenFromContext(ctx)
	
	// Create request payload including bearer token for the orchestrator service
	requestPayload := struct {
		Metadata    map[string]interface{} `json:"metadata,omitempty"`
		BearerToken string                 `json:"bearer_token,omitempty"`
		APIBaseURL  string                 `json:"api_base_url,omitempty"`
	}{
		Metadata:    request.Metadata,
		BearerToken: bearerToken,
		APIBaseURL:  os.Getenv("API_BASE_URL"), // Allow orchestrator to know where to callback
	}

	requestBytes, err := json.Marshal(requestPayload)
	if err != nil {
		return types.CreateThreadResponse{}, err
	}

	// Make HTTP request to external python orchestrator API
	resp, err := service.OrchestratorClient.Post(
		fmt.Sprintf("%s/new_thread", service.OrchestratorAPIURL),
		"application/json",
		bytes.NewBuffer(requestBytes),
	)
	if err != nil {
		return types.CreateThreadResponse{}, err
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return types.CreateThreadResponse{}, fmt.Errorf("external API returned status: %d", resp.StatusCode)
	}

	// Parse response
	var response struct {
		ThreadID string `json:"thread_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return types.CreateThreadResponse{}, err
	}

	// Initialize in local cache too for safety
	service.ThreadsByID[response.ThreadID] = &entity.Thread{
		ID:        response.ThreadID,
		Messages:  []entity.Message{},
		Metadata:  request.Metadata,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Return the thread ID from the external service
	return types.CreateThreadResponse{ThreadID: response.ThreadID}, nil
}

// CreateMessage creates a new message in a thread
func (service *OrchestratorService) CreateMessage(ctx context.Context, request types.CreateMessageRequest) (types.CreateMessageResponse, error) {
	// Extract bearer token from context
	bearerToken := service.extractBearerTokenFromContext(ctx)
	
	// Create the request payload for the Python server including bearer token
	requestPayload := struct {
		Message     string `json:"message"`
		ThreadID    string `json:"thread_id"`
		ImageData   string `json:"image_data,omitempty"`
		ImageType   string `json:"image_type,omitempty"`
		BearerToken string `json:"bearer_token,omitempty"`
		APIBaseURL  string `json:"api_base_url,omitempty"`
	}{
		Message:     request.Content,
		ThreadID:    request.ThreadID,
		ImageData:   request.ImageData,
		ImageType:   request.ImageType,
		BearerToken: bearerToken,
		APIBaseURL:  os.Getenv("API_BASE_URL"),
	}

	requestBytes, err := json.Marshal(requestPayload)
	if err != nil {
		return types.CreateMessageResponse{}, err
	}

	// Make HTTP request to external Python orchestrator API
	resp, err := service.OrchestratorClient.Post(
		fmt.Sprintf("%s/chat/%s", service.OrchestratorAPIURL, request.ThreadID),
		"application/json",
		bytes.NewBuffer(requestBytes),
	)
	if err != nil {
		return types.CreateMessageResponse{}, err
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return types.CreateMessageResponse{}, fmt.Errorf("external API returned status: %d", resp.StatusCode)
	}

	// Parse response
	var pythonResponse types.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&pythonResponse); err != nil {
		return types.CreateMessageResponse{}, err
	}

	// Check if there was an error
	if pythonResponse.Status != "success" {
		return types.CreateMessageResponse{}, fmt.Errorf("python API error: %s", pythonResponse.Error)
	}

	// Create message records in the local cache for consistency
	messageID := generateID()
	createTime := time.Now()
	completedTime := time.Now()

	// Get thread from local cache or create it if it doesn't exist
	thread, exists := service.ThreadsByID[request.ThreadID]
	if !exists {
		thread = &entity.Thread{
			ID:        request.ThreadID,
			Messages:  []entity.Message{},
			Metadata:  make(map[string]interface{}),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		service.ThreadsByID[request.ThreadID] = thread
	}

	// Content from Python response
	var responseContent string
	if pythonResponse.Message != nil {
		responseContent = pythonResponse.Message.Content
	}

	// Create user message
	userMessage := entity.Message{
		ID:        messageID,
		ThreadID:  request.ThreadID,
		Content:   request.Content,
		Role:      "user",
		Metadata:  request.Metadata,
		CreatedAt: createTime,
	}

	// Create assistant message
	responseID := generateID()

	var role string
	if pythonResponse.Message != nil {
		role = pythonResponse.Message.Role
	} else {
		role = "assistant"
	}

	assistantMessage := entity.Message{
		ID:        responseID,
		ThreadID:  request.ThreadID,
		Content:   responseContent,
		Role:      role,
		Metadata:  make(map[string]interface{}),
		CreatedAt: completedTime,
	}

	// Add messages to thread
	thread.Messages = append(thread.Messages, userMessage, assistantMessage)
	thread.UpdatedAt = completedTime

	// Prepare response in the new format
	response := types.CreateMessageResponse{
		ThreadID: pythonResponse.ThreadID,
		Message:  pythonResponse.Message,
		Status:   pythonResponse.Status,
		Error:    pythonResponse.Error,
		// Also maintain backward compatibility fields
		MessageID:   messageID,
		Content:     request.Content,
		Response:    responseContent,
		ResponseID:  responseID,
		Metadata:    request.Metadata,
		CreatedAt:   createTime,
		CompletedAt: completedTime,
	}

	return response, nil
}

// UploadDicomPayload uploads a DICOM payload to a thread
func (service *OrchestratorService) UploadDicomPayload(ctx context.Context, request types.DicomPayloadRequest) (any, error) {
	// Extract bearer token from context
	bearerToken := service.extractBearerTokenFromContext(ctx)
	
	requestPayload := struct {
		Payload     []types.StudyData `json:"payload"`
		ThreadID    *string           `json:"thread_id,omitempty"`
		BearerToken string            `json:"bearer_token"`
		APIBaseURL  string            `json:"api_base_url"`
	}{
		Payload:     request.Payload,
		ThreadID:    request.ThreadID,
		BearerToken: bearerToken,
		APIBaseURL:  os.Getenv("API_BASE_URL"),
	}

	requestBytes, err := json.Marshal(requestPayload)
	if err != nil {
		return nil, err
	}

	// Determine the thread ID for the URL
	var threadID string
	if request.ThreadID != nil {
		threadID = *request.ThreadID
	}

	// Make HTTP request to external Python orchestrator API
	resp, err := service.OrchestratorClient.Post(
		fmt.Sprintf("%s/dicom/%s", service.OrchestratorAPIURL, threadID),
		"application/json",
		bytes.NewBuffer(requestBytes),
	)
	if err != nil {
		return types.UploadDicomPayloadResponse{}, err
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return types.UploadDicomPayloadResponse{}, fmt.Errorf("external API returned status: %d", resp.StatusCode)
	}

	// Parse response
	var pythonResponse struct {
		ThreadID string `json:"thread_id"`
		Status   string `json:"status"`
		Message  string `json:"message"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&pythonResponse); err != nil {
		return types.UploadDicomPayloadResponse{}, err
	}

	// Check if there was an error
	if pythonResponse.Status != "success" {
		return types.UploadDicomPayloadResponse{}, fmt.Errorf("python API error: %s", pythonResponse.Message)
	}

	// Update local thread cache if it exists
	if request.ThreadID != nil {
		if thread, exists := service.ThreadsByID[*request.ThreadID]; exists {
			if thread.Metadata == nil {
				thread.Metadata = make(map[string]interface{})
			}

			// Update metadata with DICOM information from the first study for backward compatibility
			if len(request.Payload) > 0 {
				firstStudy := request.Payload[0]
				thread.Metadata["studyInstanceUID"] = firstStudy.StudyInstanceUID
				thread.Metadata["seriesInstanceUIDs"] = firstStudy.SeriesInstanceUIDs
				thread.Metadata["dicomPayloadTimestamp"] = time.Now().Unix()

				// Include additional metadata if provided
				if firstStudy.AdditionalMetadata != nil {
					for key, value := range firstStudy.AdditionalMetadata {
						thread.Metadata[key] = value
					}
				}
			}

			// Update thread
			thread.UpdatedAt = time.Now()
		}
	}

	// Return response with new fields
	return types.UploadDicomPayloadResponse{
		ThreadID: pythonResponse.ThreadID,
		Status:   pythonResponse.Status,
		Message:  pythonResponse.Message,
		Success:  pythonResponse.Status == "success", // For backward compatibility
	}, nil
}

// GetThread gets thread information
func (service *OrchestratorService) GetThread(ctx context.Context, threadID string) (types.GetThreadResponse, error) {
	// Extract bearer token from context
	bearerToken := service.extractBearerTokenFromContext(ctx)
	
	// Create request with bearer token
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/threads/%s", service.OrchestratorAPIURL, threadID), nil)
	if err != nil {
		return types.GetThreadResponse{}, err
	}

	// Add query parameters for authentication
	q := req.URL.Query()
	q.Add("bearer_token", bearerToken)
	if apiBaseURL := os.Getenv("API_BASE_URL"); apiBaseURL != "" {
		q.Add("api_base_url", apiBaseURL)
	}
	req.URL.RawQuery = q.Encode()

	// Make HTTP request to external Python orchestrator API
	resp, err := service.OrchestratorClient.Do(req)
	if err != nil {
		return types.GetThreadResponse{}, err
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return types.GetThreadResponse{}, fmt.Errorf("external API returned status: %d", resp.StatusCode)
	}

	// Parse response
	var pythonResponse struct {
		ThreadID        string `json:"thread_id"`
		HasDicomPayload bool   `json:"has_dicom_payload"`
		Status          string `json:"status,omitempty"`
		Error           string `json:"error,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&pythonResponse); err != nil {
		return types.GetThreadResponse{}, err
	}

	// Check if there was an error
	if pythonResponse.Status == "error" {
		return types.GetThreadResponse{}, fmt.Errorf("python API error: %s", pythonResponse.Error)
	}

	// Attempt to get information from local cache if available
	var messages []types.MessageResponse
	var metadata map[string]interface{}
	var createdAt, updatedAt time.Time

	if thread, exists := service.ThreadsByID[threadID]; exists {
		// Prepare messages for response
		messages = make([]types.MessageResponse, len(thread.Messages))
		for i, msg := range thread.Messages {
			messages[i] = types.MessageResponse{
				ID:        msg.ID,
				Content:   msg.Content,
				Role:      msg.Role,
				Metadata:  msg.Metadata,
				CreatedAt: msg.CreatedAt,
			}
		}

		metadata = thread.Metadata
		createdAt = thread.CreatedAt
		updatedAt = thread.UpdatedAt
	} else {
		// If not in local cache, create minimal response with what we know
		metadata = map[string]interface{}{
			"hasDicomPayload": pythonResponse.HasDicomPayload,
		}

		now := time.Now()
		createdAt = now
		updatedAt = now
	}

	// Prepare response with new fields
	response := types.GetThreadResponse{
		ID:              threadID, // Keep ID for backward compatibility
		ThreadID:        threadID,
		Messages:        messages,
		Metadata:        metadata,
		HasDicomPayload: pythonResponse.HasDicomPayload,
		Status:          pythonResponse.Status,
		Error:           pythonResponse.Error,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}

	return response, nil
}

// generateID generates a unique ID
func generateID() string {
	return ksuid.New().String()
}
