package dockerinference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	types "api-pacs/infrastructures/providers/api/dockerinference/types"
)

// renderReportClient has no default timeout: callers bound every call with a
// context deadline. Redirects are refused so a model cannot bounce the request.
var renderReportClient = &http.Client{
	Timeout: 5 * time.Minute,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// RenderReport posts a stored JSON-mode result to POST /api/inference/render-report.
// Only Content-Type is sent: no Authorization or cookie from the user request.
func (d *DockerInferenceAPI) RenderReport(
	ctx context.Context,
	containerName string,
	request types.RenderReportRequest,
	maxResponseBytes int64,
) (types.RenderReportResponse, error) {
	if !lifecycleHost.MatchString(containerName) {
		return types.RenderReportResponse{}, fmt.Errorf("invalid inference container hostname")
	}
	if maxResponseBytes <= 0 {
		return types.RenderReportResponse{}, fmt.Errorf("render-report response limit must be positive")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return types.RenderReportResponse{}, err
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, "http://"+containerName+"/api/inference/render-report", bytes.NewReader(body),
	)
	if err != nil {
		return types.RenderReportResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := renderReportClient.Do(req)
	if err != nil {
		return types.RenderReportResponse{}, fmt.Errorf("render-report: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed:
		return types.RenderReportResponse{}, types.ErrRenderReportUnsupported
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return types.RenderReportResponse{}, types.ErrRenderReportRejected
	case resp.StatusCode != http.StatusOK:
		return types.RenderReportResponse{}, fmt.Errorf("render-report HTTP status %d", resp.StatusCode)
	}

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return types.RenderReportResponse{}, fmt.Errorf("render-report response: %w", err)
	}
	if int64(len(payload)) > maxResponseBytes {
		return types.RenderReportResponse{}, types.ErrRenderReportTooLarge
	}
	var response types.RenderReportResponse
	if err = json.Unmarshal(payload, &response); err != nil {
		return types.RenderReportResponse{}, fmt.Errorf("render-report response: %w", err)
	}
	if !response.Success {
		return types.RenderReportResponse{}, types.ErrRenderReportRejected
	}
	return response, nil
}
