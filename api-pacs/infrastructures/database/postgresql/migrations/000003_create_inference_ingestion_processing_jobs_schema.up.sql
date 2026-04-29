CREATE TYPE inference_ingestion_processing_job_status AS ENUM (
    'queued',
    'running',
    'completed',
    'failed'
);

CREATE TABLE inference_ingestion_processing_jobs (
    id varchar(50) NOT NULL,
    candidate_id varchar(50) NOT NULL,
    tenant_id varchar(50) NOT NULL,
    model_name varchar(255) NOT NULL,
    model_version varchar(255) NULL,
    modality varchar(255) NULL,
    status inference_ingestion_processing_job_status NOT NULL,
    study_service_job_id varchar(50) NULL,
    error_message text NULL,
    started_at timestamptz NULL,
    completed_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    CONSTRAINT uq_inference_ingestion_processing_jobs_candidate_model
        UNIQUE (candidate_id, model_name),
    CONSTRAINT fk_inference_ingestion_processing_jobs_candidate
        FOREIGN KEY (candidate_id)
        REFERENCES inference_ingestion_candidates (id)
        ON DELETE CASCADE
);

CREATE INDEX idx_inference_ingestion_processing_jobs_tenant_id
    ON inference_ingestion_processing_jobs (tenant_id);

CREATE INDEX idx_inference_ingestion_processing_jobs_candidate_id
    ON inference_ingestion_processing_jobs (candidate_id);

CREATE INDEX idx_inference_ingestion_processing_jobs_status
    ON inference_ingestion_processing_jobs (status);

CREATE OR REPLACE FUNCTION func_ingestion_processing_jobs_update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER trigger_update_inference_ingestion_processing_jobs_updated_at
    BEFORE UPDATE ON inference_ingestion_processing_jobs
    FOR EACH ROW
    EXECUTE FUNCTION func_ingestion_processing_jobs_update_updated_at();
