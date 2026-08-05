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
	// GetJobByID fetches one exact tenant-scoped study-service job.
	GetJobByID(ctx context.Context, tenantID, jobID string) (serviceTypes.StudyServiceJob, bool, error)
	// GetJobsByProcessingRun fetches tenant-scoped study-service jobs for one processing run.
	GetJobsByProcessingRun(ctx context.Context, tenantID, processingRunID string) ([]serviceTypes.StudyServiceJob, error)
	// GetJobsByCandidate fetches tenant-scoped study-service jobs for one ingestion candidate.
	GetJobsByCandidate(ctx context.Context, tenantID, candidateID string) ([]serviceTypes.StudyServiceJob, error)
}
