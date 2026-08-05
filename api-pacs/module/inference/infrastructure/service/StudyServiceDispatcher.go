package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	orthancAPITypes "api-pacs/infrastructures/providers/api/orthanc/types"
	"api-pacs/module/inference/domain/entity"
	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

var (
	errStudyServiceBaseURLMissing   = errors.New("study-service base URL is not configured")
	errLocalOrthancStudyNotFound    = errors.New("study not found in local Orthanc")
	errDispatchModalityUndetermined = errors.New("cannot determine dispatch modality for ingestion candidate")
)

// DispatchStudyHTTPError represents a non-2xx response from study-service ingest.
type DispatchStudyHTTPError struct {
	StatusCode int
	Body       string
	RetryAfter time.Duration
}

func (err *DispatchStudyHTTPError) Error() string {
	body := strings.TrimSpace(err.Body)
	if body == "" {
		return fmt.Sprintf("study-service dispatch failed with status %d", err.StatusCode)
	}

	return fmt.Sprintf("study-service dispatch failed with status %d: %s", err.StatusCode, body)
}

// StudyServiceDispatcher builds and emits explicit study-service ingest requests.
type StudyServiceDispatcher struct {
	StudyServiceBaseURL       string
	StudyServiceIngestToken   string
	StudyServiceOperatorToken string
	StudyServiceClient        *http.Client
	orthancAPITypes.OrthancAPIInterface
}

// BuildDispatchStudyRequest resolves the explicit study-service ingest payload for one candidate/job pair.
func (service *StudyServiceDispatcher) BuildDispatchStudyRequest(ctx context.Context, data serviceTypes.BuildStudyServiceDispatchRequestInput) (serviceTypes.DispatchStudyRequest, error) {
	orthancStudyID := trimmedPointerValue(data.OrthancStudyID)
	if orthancStudyID == "" {
		var err error
		orthancStudyID, err = service.resolveLocalOrthancStudyID(ctx, data.Candidate.StudyInstanceUID)
		if err != nil {
			return serviceTypes.DispatchStudyRequest{}, err
		}
	}

	modality, err := resolveDispatchModality(data.IngestionJob, data.Candidate)
	if err != nil {
		return serviceTypes.DispatchStudyRequest{}, err
	}

	requestID := trimmedPointerValue(data.RequestID)
	if requestID == "" {
		requestID = strings.TrimSpace(data.Candidate.ID)
	}
	if requestID == "" {
		requestID = generateID()
	}

	tenantID := nonEmptyStringPointer(
		strings.TrimSpace(data.Candidate.TenantID),
		strings.TrimSpace(data.IngestionJob.TenantID),
	)
	ingestionJobID := nonEmptyStringPointer(
		strings.TrimSpace(data.Candidate.IngestionJobID),
		strings.TrimSpace(data.IngestionJob.ID),
	)

	return serviceTypes.DispatchStudyRequest{
		XRequestID:         requestID,
		TenantID:           tenantID,
		IngestionJobID:     ingestionJobID,
		CandidateID:        nonEmptyStringPointer(strings.TrimSpace(data.Candidate.ID)),
		RetrievalAttemptID: trimmedPointer(data.RetrievalAttemptID),
		ProcessingRunID:    trimmedPointer(data.ProcessingRunID),
		StudyInstanceUID:   strings.TrimSpace(data.Candidate.StudyInstanceUID),
		OrthancStudyID:     orthancStudyID,
		Modality:           modality,
		ModelName:          strings.TrimSpace(data.IngestionJob.ModelName),
		ModelVersion:       strings.TrimSpace(data.IngestionJob.ModelVersion),
	}, nil
}

// DispatchStudy sends a POST /ingest/study request to study-service.
func (service *StudyServiceDispatcher) DispatchStudy(ctx context.Context, data serviceTypes.DispatchStudyRequest) (serviceTypes.DispatchStudyResponse, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(service.StudyServiceBaseURL), "/")
	if baseURL == "" {
		return serviceTypes.DispatchStudyResponse{}, errStudyServiceBaseURLMissing
	}

	var requestBuffer bytes.Buffer
	if err := json.NewEncoder(&requestBuffer).Encode(data); err != nil {
		return serviceTypes.DispatchStudyResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/ingest/study", baseURL), &requestBuffer)
	if err != nil {
		return serviceTypes.DispatchStudyResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", data.XRequestID)
	service.applyBearerToken(req, service.StudyServiceIngestToken)

	resp, err := service.StudyServiceClient.Do(req)
	if err != nil {
		return serviceTypes.DispatchStudyResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return serviceTypes.DispatchStudyResponse{}, readErr
		}
		return serviceTypes.DispatchStudyResponse{}, &DispatchStudyHTTPError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
			RetryAfter: parseRetryAfterHeader(resp.Header.Get("Retry-After")),
		}
	}

	var response serviceTypes.DispatchStudyResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return serviceTypes.DispatchStudyResponse{}, err
	}
	response.StatusCode = resp.StatusCode

	return response, nil
}

// GetJobByID fetches one exact tenant-scoped job. A missing job is an expected
// reconciliation result rather than a transport failure.
func (service *StudyServiceDispatcher) GetJobByID(
	ctx context.Context,
	tenantID, jobID string,
) (serviceTypes.StudyServiceJob, bool, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(service.StudyServiceBaseURL), "/")
	if baseURL == "" {
		return serviceTypes.StudyServiceJob{}, false, errStudyServiceBaseURLMissing
	}

	requestID := generateID()
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/jobs/%s", baseURL, url.PathEscape(strings.TrimSpace(jobID))),
		nil,
	)
	if err != nil {
		return serviceTypes.StudyServiceJob{}, false, err
	}
	req.Header.Set("X-Request-ID", requestID)
	req.Header.Set("X-Tenant-ID", strings.TrimSpace(tenantID))
	service.applyBearerToken(req, service.StudyServiceOperatorToken)

	resp, err := service.StudyServiceClient.Do(req)
	if err != nil {
		return serviceTypes.StudyServiceJob{}, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return serviceTypes.StudyServiceJob{}, false, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return serviceTypes.StudyServiceJob{}, false, readErr
		}
		return serviceTypes.StudyServiceJob{}, false, fmt.Errorf(
			"study-service job lookup failed with status %d: %s",
			resp.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}

	var job serviceTypes.StudyServiceJob
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return serviceTypes.StudyServiceJob{}, false, err
	}
	return job, true, nil
}

// GetJobsByProcessingRun fetches all tenant-scoped jobs correlated to one Go
// run. A 404 is treated as endpoint/data absence so mixed-version deployments
// can continue with candidate fallback.
func (service *StudyServiceDispatcher) GetJobsByProcessingRun(
	ctx context.Context,
	tenantID, processingRunID string,
) ([]serviceTypes.StudyServiceJob, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(service.StudyServiceBaseURL), "/")
	if baseURL == "" {
		return nil, errStudyServiceBaseURLMissing
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/jobs/by-processing-run/%s?page=1&page_size=250", baseURL, url.PathEscape(strings.TrimSpace(processingRunID))),
		nil,
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Request-ID", generateID())
	req.Header.Set("X-Tenant-ID", strings.TrimSpace(tenantID))
	service.applyBearerToken(req, service.StudyServiceOperatorToken)

	resp, err := service.StudyServiceClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return []serviceTypes.StudyServiceJob{}, nil
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, readErr
		}
		return nil, fmt.Errorf(
			"study-service processing-run lookup failed with status %d: %s",
			resp.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}

	var response serviceTypes.StudyServiceJobsResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}
	return response.Jobs, nil
}

// GetJobsByCandidate fetches tenant-scoped operator-visible jobs for one candidate.
func (service *StudyServiceDispatcher) GetJobsByCandidate(ctx context.Context, tenantID, candidateID string) ([]serviceTypes.StudyServiceJob, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(service.StudyServiceBaseURL), "/")
	if baseURL == "" {
		return nil, errStudyServiceBaseURLMissing
	}

	requestID := generateID()
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf("%s/jobs/by-candidate/%s?page=1&page_size=250", baseURL, candidateID),
		nil,
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Request-ID", requestID)
	req.Header.Set("X-Tenant-ID", strings.TrimSpace(tenantID))
	service.applyBearerToken(req, service.StudyServiceOperatorToken)

	resp, err := service.StudyServiceClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, readErr
		}
		return nil, fmt.Errorf("study-service job lookup failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var response serviceTypes.StudyServiceJobsResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	return response.Jobs, nil
}

func (service *StudyServiceDispatcher) applyBearerToken(req *http.Request, token string) {
	if trimmed := strings.TrimSpace(token); trimmed != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", trimmed))
	}
}

func (service *StudyServiceDispatcher) resolveLocalOrthancStudyID(ctx context.Context, studyInstanceUID string) (string, error) {
	localResources, err := service.OrthancAPIInterface.FindLocalResources(ctx, orthancAPITypes.QueryLocalResourceRequest{
		Level: "Study",
		Query: orthancAPITypes.QueryLocalResource{
			StudyInstanceUID: studyInstanceUID,
		},
		Expand: true,
	})
	if err != nil {
		return "", err
	}
	if len(localResources) == 0 {
		return "", errLocalOrthancStudyNotFound
	}

	return strings.TrimSpace(localResources[0].ID), nil
}

func resolveDispatchModality(job entity.InferenceIngestionJob, candidate entity.InferenceIngestionCandidate) (string, error) {
	jobModalities := normalizeDispatchModalities([]string(job.Modalities))
	if len(jobModalities) == 0 {
		return "", errDispatchModalityUndetermined
	}

	candidateModalities := parseCandidateModalities(candidate.ModalitiesInStudy)
	if len(candidateModalities) == 0 {
		if len(jobModalities) == 1 {
			return jobModalities[0], nil
		}
		return "", errDispatchModalityUndetermined
	}

	jobModalitySet := make(map[string]struct{}, len(jobModalities))
	for _, modality := range jobModalities {
		jobModalitySet[modality] = struct{}{}
	}

	for _, modality := range candidateModalities {
		if _, ok := jobModalitySet[modality]; ok {
			return modality, nil
		}
	}

	if len(jobModalities) == 1 {
		return jobModalities[0], nil
	}

	return "", errDispatchModalityUndetermined
}

func normalizeDispatchModalities(modalities []string) []string {
	normalized := make([]string, 0, len(modalities))
	seen := make(map[string]struct{}, len(modalities))
	for _, modality := range modalities {
		trimmed := strings.ToUpper(strings.TrimSpace(modality))
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}

	return normalized
}

func parseCandidateModalities(modalitiesInStudy *string) []string {
	if modalitiesInStudy == nil {
		return nil
	}

	parts := strings.Split(strings.TrimSpace(*modalitiesInStudy), `\`)
	return normalizeDispatchModalities(parts)
}

func nonEmptyStringPointer(values ...string) *string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		return &trimmed
	}

	return nil
}

func trimmedPointer(value *string) *string {
	if value == nil {
		return nil
	}

	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}

	return &trimmed
}

func trimmedPointerValue(value *string) string {
	if value == nil {
		return ""
	}

	return strings.TrimSpace(*value)
}

func parseRetryAfterHeader(value string) time.Duration {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(trimmed); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}

	retryAt, err := http.ParseTime(trimmed)
	if err != nil {
		return 0
	}

	delay := time.Until(retryAt)
	if delay < 0 {
		return 0
	}

	return delay
}
