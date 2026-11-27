-- Owners of the studies in the Orthanc that all tenants share. A row is
-- written when a tenant retrieves the study through a modality it moves from,
-- uploads it with STOW-RS through the gateway, or when api-pacs's ownership
-- sync assigns a cached or newly arrived study (ingestion records of the
-- tenant, a "pacs-ai-tenant-<id>" label, or ORTHANC_DEFAULT_TENANT_ID).
-- The DICOMweb proxy and the endpoints that read a local study by UID only
-- serve a tenant its studies; the default tenant also reads unowned studies.
CREATE TABLE IF NOT EXISTS orthanc_tenant_studies (
    tenant_id varchar(50) NOT NULL,
    study_instance_uid varchar(64) NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, study_instance_uid)
);

CREATE INDEX IF NOT EXISTS orthanc_tenant_studies_study_idx
    ON orthanc_tenant_studies (study_instance_uid);
