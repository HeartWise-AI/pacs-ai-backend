package repository

import (
	"context"
	"log"

	"github.com/lib/pq"

	postgresqlTypes "api-pacs/infrastructures/database/postgresql/types"
	"api-pacs/module/orthanc/domain/repository"
)

// OrthancTenantStudyRepository keeps the studies each tenant retrieved into the
// shared Orthanc in orthanc_tenant_studies (migration 000017).
type OrthancTenantStudyRepository struct {
	PostgresSQLDBHandlerInterface postgresqlTypes.PostgresSQLDBHandlerInterface
}

const insertTenantStudySQL = `
INSERT INTO orthanc_tenant_studies (tenant_id, study_instance_uid)
VALUES (:tenant_id, :study_instance_uid)
ON CONFLICT (tenant_id, study_instance_uid) DO NOTHING`

const selectTenantStudiesSQL = `
SELECT study_instance_uid
FROM orthanc_tenant_studies
WHERE tenant_id = :tenant_id AND study_instance_uid = ANY(:study_instance_uids)`

// InsertTenantStudy records that the tenant retrieved the study
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

// SelectTenantStudies returns the studies among studyInstanceUIDs that the tenant retrieved
func (repository *OrthancTenantStudyRepository) SelectTenantStudies(ctx context.Context, tenantID string, studyInstanceUIDs []string) ([]string, error) {
	studies := []string{}
	if tenantID == "" || len(studyInstanceUIDs) == 0 {
		return studies, nil
	}

	err := repository.PostgresSQLDBHandlerInterface.Query(selectTenantStudiesSQL, map[string]interface{}{
		"tenant_id":           tenantID,
		"study_instance_uids": pq.Array(studyInstanceUIDs),
	}, &studies)
	if err != nil {
		log.Println(err)
		return nil, err
	}

	return studies, nil
}

var _ repository.OrthancTenantStudyRepositoryInterface = (*OrthancTenantStudyRepository)(nil)
