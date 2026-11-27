package repository

import (
	"context"
)

// OrthancTenantStudyRepositoryInterface is the interface for the studies each tenant retrieved into the shared Orthanc
type OrthancTenantStudyRepositoryInterface interface {
	// InsertTenantStudy records that the tenant retrieved the study
	InsertTenantStudy(ctx context.Context, tenantID, studyInstanceUID string) error
	// SelectTenantStudies returns the studies among studyInstanceUIDs that the tenant retrieved
	SelectTenantStudies(ctx context.Context, tenantID string, studyInstanceUIDs []string) ([]string, error)
}
