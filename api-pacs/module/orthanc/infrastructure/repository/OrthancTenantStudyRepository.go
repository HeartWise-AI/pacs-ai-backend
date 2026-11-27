package repository

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/lib/pq"

	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
	"api-pacs/module/orthanc/domain/repository"
)

// DefaultTenantEnv names the tenant that owns the studies arriving in the
// shared Orthanc with no tenant evidence (C-STORE pushes, direct uploads).
// Set it on a single-tenant install. Left empty, such studies stay unassigned
// and no tenant reads them until a retrieve, an ingestion record or an upload
// through the gateway assigns them (fail closed).
const DefaultTenantEnv = "ORTHANC_DEFAULT_TENANT_ID"

// DefaultTenantID returns the configured default tenant, or "".
func DefaultTenantID() string {
	return strings.TrimSpace(os.Getenv(DefaultTenantEnv))
}

// OrthancTenantStudyRepository keeps the owners of the studies in the shared
// Orthanc in orthanc_tenant_studies (migration 000017).
type OrthancTenantStudyRepository struct {
	PostgresSQLDBHandlerInterface postgresqlTypes.PostgresSQLDBHandlerInterface
	// DefaultTenantID also reads the studies no tenant owns. Empty: nobody does.
	DefaultTenantID string
}

const insertTenantStudySQL = `
INSERT INTO orthanc_tenant_studies (tenant_id, study_instance_uid)
VALUES (:tenant_id, :study_instance_uid)
ON CONFLICT (tenant_id, study_instance_uid) DO NOTHING`

// A study is the tenant's when the tenant owns it, or when the tenant is the
// default tenant and no tenant owns it.
const selectTenantStudiesSQL = `
SELECT requested.study_instance_uid
FROM unnest(CAST(:study_instance_uids AS text[])) AS requested(study_instance_uid)
WHERE EXISTS (
    SELECT 1 FROM orthanc_tenant_studies owned
    WHERE owned.tenant_id = :tenant_id AND owned.study_instance_uid = requested.study_instance_uid
) OR (
    CAST(:default_tenant_id AS text) <> '' AND CAST(:tenant_id AS text) = CAST(:default_tenant_id AS text) AND NOT EXISTS (
        SELECT 1 FROM orthanc_tenant_studies owned
        WHERE owned.study_instance_uid = requested.study_instance_uid
    )
)`

const selectOwnedStudiesSQL = `
SELECT DISTINCT study_instance_uid
FROM orthanc_tenant_studies
WHERE study_instance_uid = ANY(CAST(:study_instance_uids AS text[]))`

const selectForeignOwnerSQL = `
SELECT EXISTS (SELECT 1 FROM orthanc_tenant_studies WHERE tenant_id <> :tenant_id)`

const selectTenantStudyEvidenceSQL = `
SELECT DISTINCT tenant_id, study_instance_uid FROM (
    SELECT tenant_id, study_instance_uid FROM ingestion_candidates
    WHERE study_instance_uid = ANY(CAST(:study_instance_uids AS text[]))
    UNION
    SELECT tenant_id, study_instance_uid FROM ingestion_processing_runs
    WHERE study_instance_uid = ANY(CAST(:study_instance_uids AS text[]))
) evidence
WHERE tenant_id <> ''`

// InsertTenantStudy records that the study belongs to the tenant
func (repository *OrthancTenantStudyRepository) InsertTenantStudy(ctx context.Context, tenantID, studyInstanceUID string) error {
	_, err := repository.PostgresSQLDBHandlerInterface.Execute(insertTenantStudySQL, map[string]interface{}{
		"tenant_id":          tenantID,
		"study_instance_uid": studyInstanceUID,
	})
	if err != nil {
		log.Println(err)
		return err
	}

	return nil
}

// SelectTenantStudies returns the studies among studyInstanceUIDs that the tenant may read
func (repository *OrthancTenantStudyRepository) SelectTenantStudies(ctx context.Context, tenantID string, studyInstanceUIDs []string) ([]string, error) {
	studies := []string{}
	if tenantID == "" || len(studyInstanceUIDs) == 0 {
		return studies, nil
	}

	err := repository.PostgresSQLDBHandlerInterface.Query(selectTenantStudiesSQL, map[string]interface{}{
		"tenant_id":           tenantID,
		"default_tenant_id":   repository.DefaultTenantID,
		"study_instance_uids": pq.Array(studyInstanceUIDs),
	}, &studies)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	return studies, nil
}

// SelectOwnedStudies returns the studies among studyInstanceUIDs that a tenant owns
func (repository *OrthancTenantStudyRepository) SelectOwnedStudies(ctx context.Context, studyInstanceUIDs []string) ([]string, error) {
	studies := []string{}
	if len(studyInstanceUIDs) == 0 {
		return studies, nil
	}
	err := repository.PostgresSQLDBHandlerInterface.Query(selectOwnedStudiesSQL, map[string]interface{}{
		"study_instance_uids": pq.Array(studyInstanceUIDs),
	}, &studies)
	if err != nil {
		log.Println(err)
		return nil, err
	}
	return studies, nil
}

// SeesEveryStudy reports whether the tenant is the default tenant and no other
// tenant owns a study, so every study in the shared Orthanc is the tenant's.
func (repository *OrthancTenantStudyRepository) SeesEveryStudy(ctx context.Context, tenantID string) (bool, error) {
	if tenantID == "" || repository.DefaultTenantID == "" || tenantID != repository.DefaultTenantID {
		return false, nil
	}
	var foreign []bool
	err := repository.PostgresSQLDBHandlerInterface.Query(selectForeignOwnerSQL, map[string]interface{}{
		"tenant_id": tenantID,
	}, &foreign)
	if err != nil {
		log.Println(err)
		return false, err
	}
	return len(foreign) == 1 && !foreign[0], nil
}

type tenantStudyEvidenceRow struct {
	TenantID         string `db:"tenant_id"`
	StudyInstanceUID string `db:"study_instance_uid"`
}

// SelectTenantStudyEvidence returns the tenants the gateway's ingestion
// records tie to each study.
func (repository *OrthancTenantStudyRepository) SelectTenantStudyEvidence(ctx context.Context, studyInstanceUIDs []string) (map[string][]string, error) {
	evidence := map[string][]string{}
	if len(studyInstanceUIDs) == 0 {
		return evidence, nil
	}
	var rows []tenantStudyEvidenceRow
	err := repository.PostgresSQLDBHandlerInterface.Query(selectTenantStudyEvidenceSQL, map[string]interface{}{
		"study_instance_uids": pq.Array(studyInstanceUIDs),
	}, &rows)
	if err != nil {
		log.Println(err)
		return nil, err
	}
	for _, row := range rows {
		evidence[row.StudyInstanceUID] = append(evidence[row.StudyInstanceUID], row.TenantID)
	}
	return evidence, nil
}

var _ repository.OrthancTenantStudyOwnershipInterface = (*OrthancTenantStudyRepository)(nil)
