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
	"strings"

	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

// studyServiceRunLabelsMaxBytes bounds a label response (a few labels per run,
// a catalog of at most 500 entries).
const studyServiceRunLabelsMaxBytes = 1 << 20

// errStudyServiceRunLabelNotFound is study-service's 404 on a label route: the
// job is not the tenant's, or the run does not carry the label.
var errStudyServiceRunLabelNotFound = errors.New("study-service run label not found")

// errStudyServiceRunLabelInvalid is study-service's 422: the label is not valid
// text (empty, longer than 40 characters, control characters) or the run has
// too many labels.
var errStudyServiceRunLabelInvalid = errors.New("study-service run label invalid")

func (service *StudyServiceDispatcher) runLabelRequest(
	ctx context.Context,
	method, tenantID, path string,
	query url.Values,
	body any,
	out any,
) (int, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(service.StudyServiceBaseURL), "/")
	if baseURL == "" {
		return 0, errStudyServiceBaseURLMissing
	}
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return 0, errors.New("tenant is required for study-service run labels")
	}
	endpoint := baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Request-ID", generateID())
	req.Header.Set("X-Tenant-ID", tenantID)
	service.applyBearerToken(req, service.StudyServiceOperatorToken)

	resp, err := service.StudyServiceClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return resp.StatusCode, errStudyServiceRunLabelNotFound
	case resp.StatusCode == http.StatusUnprocessableEntity:
		return resp.StatusCode, errStudyServiceRunLabelInvalid
	case resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices:
		return resp.StatusCode, &StudyServiceLookupHTTPError{StatusCode: resp.StatusCode}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, studyServiceRunLabelsMaxBytes)).Decode(out); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

func jobLabelsPath(jobID string) string {
	return fmt.Sprintf("/jobs/%s/labels", url.PathEscape(strings.TrimSpace(jobID)))
}

// GetJobLabels reads the labels of one tenant job.
func (service *StudyServiceDispatcher) GetJobLabels(ctx context.Context, tenantID, jobID string) (serviceTypes.StudyServiceJobLabels, bool, error) {
	var labels serviceTypes.StudyServiceJobLabels
	_, err := service.runLabelRequest(ctx, http.MethodGet, tenantID, jobLabelsPath(jobID), nil, nil, &labels)
	if errors.Is(err, errStudyServiceRunLabelNotFound) {
		return serviceTypes.StudyServiceJobLabels{}, false, nil
	}
	if err != nil {
		return serviceTypes.StudyServiceJobLabels{}, false, err
	}
	return labels, true, nil
}

// AddJobLabel puts a label on a tenant job; added is false when the run
// already had it in any case.
func (service *StudyServiceDispatcher) AddJobLabel(ctx context.Context, tenantID, jobID, label, createdBy string) (serviceTypes.StudyServiceJobLabels, bool, error) {
	var labels serviceTypes.StudyServiceJobLabels
	body := map[string]string{"label": label}
	if createdBy = strings.TrimSpace(createdBy); createdBy != "" {
		body["createdBy"] = createdBy
	}
	status, err := service.runLabelRequest(ctx, http.MethodPost, tenantID, jobLabelsPath(jobID), nil, body, &labels)
	if err != nil {
		return serviceTypes.StudyServiceJobLabels{}, false, err
	}
	return labels, status == http.StatusCreated, nil
}

// RemoveJobLabel takes a label off a tenant job (matched without case).
func (service *StudyServiceDispatcher) RemoveJobLabel(ctx context.Context, tenantID, jobID, label string) (serviceTypes.StudyServiceJobLabels, error) {
	var labels serviceTypes.StudyServiceJobLabels
	_, err := service.runLabelRequest(ctx, http.MethodDelete, tenantID, jobLabelsPath(jobID), url.Values{"label": []string{label}}, nil, &labels)
	return labels, err
}

// GetRunLabelCatalog lists the labels the tenant has used.
func (service *StudyServiceDispatcher) GetRunLabelCatalog(ctx context.Context, tenantID string) (serviceTypes.RunLabelCatalog, error) {
	var catalog serviceTypes.RunLabelCatalog
	_, err := service.runLabelRequest(ctx, http.MethodGet, tenantID, "/run-labels", nil, nil, &catalog)
	if catalog.Labels == nil {
		catalog.Labels = []serviceTypes.RunLabelCatalogEntry{}
	}
	return catalog, err
}
