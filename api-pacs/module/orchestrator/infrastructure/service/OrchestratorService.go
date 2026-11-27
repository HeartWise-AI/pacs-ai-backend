package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/segmentio/ksuid"

	iamTypes "api-pacs/interfaces/http/rest/middlewares/iam/types"
	"api-pacs/module/orchestrator/domain/entity"
	"api-pacs/module/orchestrator/infrastructure/service/types"
)

// ErrChatProgressUnsupported is returned when the chat backend has no progress
// endpoint (an older version): callers fall back to showing no live activity.
var ErrChatProgressUnsupported = errors.New("chat_progress_unsupported")

// ErrChatTurnInProgress is returned when the chat backend refuses a message
// because an answer is still being written in the same thread (its 409).
var ErrChatTurnInProgress = errors.New("chat_turn_in_progress")

// ErrChatUpstream is returned when the chat backend fails a message without an
// answer the user can see (any other non-200).
var ErrChatUpstream = errors.New("chat_upstream_error")

// chatProgressTimeout bounds one progress poll; the client's own timeout is
// sized for a whole chat request.
const chatProgressTimeout = 4 * time.Second

// threadCacheLimit and threadCacheTTL bound ThreadsByID. The chat backend is
// the source of truth, so an evicted thread only loses its local copy.
const (
	threadCacheLimit = 10000
	threadCacheTTL   = 24 * time.Hour
)

// OrchestratorService implements the orchestrator service
type OrchestratorService struct {
	OrchestratorAPIURL     string
	OrchestratorClient     *http.Client
	ThreadsByID            map[string]*entity.Thread
	// threadsMu guards ThreadsByID and every thread in it: one service
	// instance serves all requests concurrently.
	threadsMu sync.RWMutex
	// ChatFeedbackServiceToken authenticates the admin panel's reads of chat
	// feedback (CHAT_FEEDBACK_SERVICE_TOKEN, shared with the chat backend).
	ChatFeedbackServiceToken string
}

// cacheThreadLocked stores thread in ThreadsByID, evicting stale or the
// least recently updated threads when the cache is full. Callers hold threadsMu.
func (service *OrchestratorService) cacheThreadLocked(thread *entity.Thread) {
	if _, exists := service.ThreadsByID[thread.ID]; !exists && len(service.ThreadsByID) >= threadCacheLimit {
		var oldestID string
		var oldest time.Time
		for id, cached := range service.ThreadsByID {
			if time.Since(cached.UpdatedAt) > threadCacheTTL {
				delete(service.ThreadsByID, id)
				continue
			}
			if oldestID == "" || cached.UpdatedAt.Before(oldest) {
				oldestID, oldest = id, cached.UpdatedAt
			}
		}
		if len(service.ThreadsByID) >= threadCacheLimit {
			delete(service.ThreadsByID, oldestID)
		}
	}
	service.ThreadsByID[thread.ID] = thread
}

// extractBearerTokenFromContext extracts the bearer token from context
func (service *OrchestratorService) extractBearerTokenFromContext(ctx context.Context) string {
	if bearerToken := ctx.Value(iamTypes.BearerTokenCtx); bearerToken != nil {
		return bearerToken.(string)
	}
	return ""
}

// User ID from context
func (service *OrchestratorService) extractUserIDFromContext(ctx context.Context) string {
    if userID := ctx.Value(iamTypes.UserIDCtx); userID != nil {
        return userID.(string)
    }
    return ""
}

// Tenant ID from context
func (service *OrchestratorService) extractTenantIDFromContext(ctx context.Context) string {
	if tenantID := ctx.Value(iamTypes.TenantIDCtx); tenantID != nil {
		return tenantID.(string)
	}
	return ""
}

// CreateThread creates a new thread
func (service *OrchestratorService) CreateThread(ctx context.Context, request types.CreateThreadRequest) (types.CreateThreadResponse, error) {
	// Extract bearer token from context
	bearerToken := service.extractBearerTokenFromContext(ctx)
	
	// Extract user ID from context
	userID := service.extractUserIDFromContext(ctx)

	// Extract tenant ID from context
	tenantID := service.extractTenantIDFromContext(ctx)

	// Create request payload including bearer token for the orchestrator service
	requestPayload := struct {
		Metadata    map[string]interface{} `json:"metadata,omitempty"`
		BearerToken string                 `json:"bearer_token,omitempty"`
		APIBaseURL  string                 `json:"api_base_url,omitempty"`
		UserID      string                 `json:"user_id,omitempty"`
		TenantID    string                 `json:"tenant_id,omitempty"`
	}{
		Metadata:    request.Metadata,
		BearerToken: bearerToken,
		APIBaseURL:  os.Getenv("API_BASE_URL"), // Allow orchestrator to know where to callback
		UserID:      userID,
		TenantID:    tenantID,
	}

	requestBytes, err := json.Marshal(requestPayload)
	if err != nil {
		return types.CreateThreadResponse{}, err
	}

	// Make HTTP request to external python orchestrator API
	resp, err := service.OrchestratorClient.Post(
		fmt.Sprintf("%s/chat/init_chat", service.OrchestratorAPIURL),
		"application/json",
		bytes.NewBuffer(requestBytes),
	)
	if err != nil {
		return types.CreateThreadResponse{}, err
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return types.CreateThreadResponse{}, fmt.Errorf("external API returned status: %d", resp.StatusCode)
	}

	// Parse response
	var response struct {
		ThreadID string `json:"conversation_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return types.CreateThreadResponse{}, err
	}

	// Initialize in local cache too for safety
	service.threadsMu.Lock()
	service.cacheThreadLocked(&entity.Thread{
		ID:        response.ThreadID,
		Messages:  []entity.Message{},
		Metadata:  request.Metadata,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})
	service.threadsMu.Unlock()

	// Return the thread ID from the external service
	return types.CreateThreadResponse{ThreadID: response.ThreadID}, nil
}

// CreateMessage creates a new message in a thread
func (service *OrchestratorService) CreateMessage(ctx context.Context, request types.CreateMessageRequest) (types.CreateMessageResponse, error) {
	// Extract bearer token from context
	bearerToken := service.extractBearerTokenFromContext(ctx)
	
	// Extract user ID from context
	userID := service.extractUserIDFromContext(ctx)

	// Extract tenant ID from context
	tenantID := service.extractTenantIDFromContext(ctx)

	// Create the request payload for the Python server including bearer token
	requestPayload := struct {
		Message           string `json:"message"`
		ConversationID    string `json:"conversation_id"`
		ImageData         string `json:"image_data,omitempty"`
		ImageType         string `json:"image_type,omitempty"`
		BearerToken       string `json:"bearer_token,omitempty"`
		APIBaseURL        string `json:"api_base_url,omitempty"`
		UserID            string `json:"user_id,omitempty"`
		TenantID          string `json:"tenant_id,omitempty"`
		// Viewer context (open study: studyInstanceUID, modality, selectedSeries).
		Metadata map[string]interface{} `json:"metadata,omitempty"`
	}{
		Message:           request.Content,
		ConversationID:    request.ThreadID,
		ImageData:         request.ImageData,
		ImageType:         request.ImageType,
		Metadata:          request.Metadata,
		BearerToken:       bearerToken,
		APIBaseURL:        os.Getenv("API_BASE_URL"),
		UserID:      	   userID,
		TenantID:          tenantID,
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
	if resp.StatusCode == http.StatusConflict {
		// One answer at a time per thread: the previous one is still running.
		log.Printf("orchestrator chat: thread busy (409 from chat backend), status=409")
		return types.CreateMessageResponse{}, ErrChatTurnInProgress
	}
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		log.Printf("orchestrator chat: chat backend returned status %d: %s", resp.StatusCode, sanitizeLogText(string(detail)))
		return types.CreateMessageResponse{}, fmt.Errorf("%w: status %d", ErrChatUpstream, resp.StatusCode)
	}

	// Parse response
	var pythonResponse types.ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&pythonResponse); err != nil {
		return types.CreateMessageResponse{}, err
	}

	// A turn that timed out or failed is answered by the chat backend with a
	// stored, retryable message (and its id, so it can be voted on): pass it on.
	// Only a non-success status without any answer is an error here.
	if pythonResponse.Status != "success" {
		if pythonResponse.Message == nil || pythonResponse.Message.Content == "" {
			log.Printf("orchestrator chat: chat backend status %q without an answer", sanitizeLogText(pythonResponse.Status))
			return types.CreateMessageResponse{}, fmt.Errorf("%w: status %s", ErrChatUpstream, pythonResponse.Status)
		}
		log.Printf("orchestrator chat: turn answered with status %q", sanitizeLogText(pythonResponse.Status))
	}

	// Create message records in the local cache for consistency
	messageID := generateID()
	createTime := time.Now()
	completedTime := time.Now()

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

	service.threadsMu.Lock()
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
		service.cacheThreadLocked(thread)
	}

	// Add messages to thread
	thread.Messages = append(thread.Messages, userMessage, assistantMessage)
	thread.UpdatedAt = completedTime
	service.threadsMu.Unlock()

	// Prepare response in the new format
	response := types.CreateMessageResponse{
		ThreadID: pythonResponse.ThreadID,
		Message:  pythonResponse.Message,
		Status:   pythonResponse.Status,
		Error:    pythonResponse.Error,
		Activity: pythonResponse.Activity,
		Citations: pythonResponse.Citations,
		// The stored id of the answer: chat feedback targets it.
		AssistantMessageID: pythonResponse.MessageID,
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
	
	// Extract user ID from context
	userID := service.extractUserIDFromContext(ctx)

	// Extract tenant ID from context
	tenantID := service.extractTenantIDFromContext(ctx)

	// Create request payload
	requestPayload := struct {
		Payload     []types.StudyData `json:"payload"`
		ThreadID    *string           `json:"conversation_id,omitempty"`
		BearerToken string            `json:"bearer_token"`
		UserID      string            `json:"user_id"`
		TenantID    string            `json:"tenant_id,omitempty"`
	}{
		Payload:     request.Payload,
		ThreadID:    request.ThreadID,
		BearerToken: bearerToken,
		UserID:      userID,
		TenantID:    tenantID,
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
		ThreadID string `json:"conversation_id"`
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
		service.threadsMu.Lock()
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
		service.threadsMu.Unlock()
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

	// Extract tenant ID from context
	tenantID := service.extractTenantIDFromContext(ctx)
	
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
	if tenantID != "" {
		q.Add("tenant_id", tenantID)
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

	service.threadsMu.RLock()
	thread, exists := service.ThreadsByID[threadID]
	if exists {
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

		// Copied: UploadDicomPayload keeps writing the cached map after we unlock.
		if thread.Metadata != nil {
			metadata = make(map[string]interface{}, len(thread.Metadata))
			for key, value := range thread.Metadata {
				metadata[key] = value
			}
		}
		createdAt = thread.CreatedAt
		updatedAt = thread.UpdatedAt
	}
	service.threadsMu.RUnlock()

	if !exists {
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

// GetChatProgress returns the steps so far of the chat request in flight for a
// thread. The chat backend only answers for the user and tenant that started
// the request, so both are forwarded from the authenticated context.
func (service *OrchestratorService) GetChatProgress(ctx context.Context, threadID string) (types.ChatProgressResponse, error) {
	bearerToken := service.extractBearerTokenFromContext(ctx)
	userID := service.extractUserIDFromContext(ctx)
	tenantID := service.extractTenantIDFromContext(ctx)

	ctx, cancel := context.WithTimeout(ctx, chatProgressTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/chat/%s/progress", service.OrchestratorAPIURL, url.PathEscape(threadID)),
		nil,
	)
	if err != nil {
		return types.ChatProgressResponse{}, err
	}
	q := req.URL.Query()
	q.Add("user_id", userID)
	if tenantID != "" {
		q.Add("tenant_id", tenantID)
	}
	req.URL.RawQuery = q.Encode()
	// The session token travels in a header, not in the URL, so it stays out of access logs.
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	resp, err := service.OrchestratorClient.Do(req)
	if err != nil {
		return types.ChatProgressResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return types.ChatProgressResponse{}, ErrChatProgressUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return types.ChatProgressResponse{}, fmt.Errorf("external API returned status: %d", resp.StatusCode)
	}

	var progress types.ChatProgressResponse
	if err := json.NewDecoder(resp.Body).Decode(&progress); err != nil {
		return types.ChatProgressResponse{}, err
	}
	progress.ThreadID = threadID
	if progress.Status == "" {
		progress.Status = "idle"
	}
	if progress.Steps == nil {
		progress.Steps = []types.ChatActivityStep{}
	}
	return progress, nil
}

// generateID generates a unique ID
func generateID() string {
	return ksuid.New().String()
}

// sanitizeLogText keeps a log line on one line and short.
func sanitizeLogText(value string) string {
	out := make([]rune, 0, len(value))
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			r = ' '
		}
		out = append(out, r)
		if len(out) >= 200 {
			break
		}
	}
	return string(out)
}
