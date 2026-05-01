package application

import (
	"context"

	serviceTypes "api-pacs/module/inference/infrastructure/service/types"
)

// ProcessingDispatcherInterface defines the outbound study-service dispatch contract.
type ProcessingDispatcherInterface interface {
	// BuildDispatchStudyRequest resolves the explicit study-service ingest payload for one candidate/job pair.
	BuildDispatchStudyRequest(ctx context.Context, data serviceTypes.BuildStudyServiceDispatchRequestInput) (serviceTypes.DispatchStudyRequest, error)
	// DispatchStudy sends a POST /ingest/study request to study-service.
	DispatchStudy(ctx context.Context, data serviceTypes.DispatchStudyRequest) (serviceTypes.DispatchStudyResponse, error)
}
