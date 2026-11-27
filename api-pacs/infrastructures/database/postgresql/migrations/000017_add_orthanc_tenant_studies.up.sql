-- Studies each tenant retrieved into the Orthanc that all tenants share. A row
-- is written when a retrieve through a modality the tenant moves from finds the
-- study on that PACS. The DICOMweb proxy and the endpoints that read or write a
-- local study by UID only serve a tenant the studies listed here.
CREATE TABLE IF NOT EXISTS orthanc_tenant_studies (
    tenant_id varchar(50) NOT NULL,
    study_instance_uid varchar(64) NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, study_instance_uid)
);
