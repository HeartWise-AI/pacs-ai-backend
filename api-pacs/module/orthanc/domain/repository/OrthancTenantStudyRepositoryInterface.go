package repository

import (
	"context"
)

// OrthancTenantStudyRepositoryInterface is the interface for the studies each
// tenant may read from the Orthanc that all tenants share.
type OrthancTenantStudyRepositoryInterface interface {
	// InsertTenantStudy records that the study belongs to the tenant
	InsertTenantStudy(ctx context.Context, tenantID, studyInstanceUID string) error
	// SelectTenantStudies returns the studies among studyInstanceUIDs that the
	// tenant may read: the tenant's own studies and, for the install's default
	// tenant only, the studies no tenant owns yet.
	SelectTenantStudies(ctx context.Context, tenantID string, studyInstanceUIDs []string) ([]string, error)
}

// OrthancTenantStudyOwnershipInterface answers the questions the DICOMweb
// proxy and the ownership sync ask about every tenant at once.
type OrthancTenantStudyOwnershipInterface interface {
	OrthancTenantStudyRepositoryInterface
	// SelectOwnedStudies returns the studies among studyInstanceUIDs that at
	// least one tenant owns.
	SelectOwnedStudies(ctx context.Context, studyInstanceUIDs []string) ([]string, error)
	// SeesEveryStudy reports whether the tenant may read every study in the
	// shared Orthanc: it is the default tenant and no other tenant owns one.
	SeesEveryStudy(ctx context.Context, tenantID string) (bool, error)
	// SelectTenantStudyEvidence returns, per study, the tenants that the
	// gateway's own records tie to it (ingestion candidates and processing
	// runs of that tenant).
	SelectTenantStudyEvidence(ctx context.Context, studyInstanceUIDs []string) (map[string][]string, error)
}
