package types

import (
	"context"
	"errors"
)

// ModelReportRendererInterface re-renders a stored model result as a report.
// It is separate from DockerInferenceAPIInterface so existing fakes and the
// model manager are unaffected. Implementations must never forward end-user
// credentials to the model container.
type ModelReportRendererInterface interface {
	RenderReport(ctx context.Context, containerName string, request RenderReportRequest, maxResponseBytes int64) (RenderReportResponse, error)
}

// ErrRenderReportRejected means the model refused the stored result (HTTP 4xx,
// typically 422 for an unusable result).
var ErrRenderReportRejected = errors.New("model rejected the stored result for report rendering")

// ErrRenderReportUnsupported means the container does not implement the endpoint.
var ErrRenderReportUnsupported = errors.New("model does not implement report rendering")

// ErrRenderReportTooLarge means the rendered report exceeded the response cap.
var ErrRenderReportTooLarge = errors.New("rendered report exceeds the configured size limit")
