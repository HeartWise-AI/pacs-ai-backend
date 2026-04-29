CREATE TYPE inference_ingestion_candidate_status AS ENUM (
    'DISCOVERED',
    'GROWING',
    'STABLE',
    'RETRIEVAL_QUEUED',
    'RETRIEVED',
    'DISAPPEARED',
    'FAILED'
);

CREATE TABLE inference_ingestion_candidates (
    id varchar(50) NOT NULL,
    tenant_id varchar(50) NOT NULL,
    ingestion_job_id varchar(50) NOT NULL,
    study_instance_uid varchar(255) NOT NULL,
    study_date varchar(20) NULL,
    study_time varchar(20) NULL,
    modalities_in_study varchar(255) NULL,
    patient_id varchar(255) NULL,
    accession_number varchar(255) NULL,
    series_count int NULL,
    instance_count int NULL,
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    last_changed_at timestamptz NOT NULL,
    missing_polls int NOT NULL DEFAULT 0,
    status inference_ingestion_candidate_status NOT NULL,
    retrieval_queued_at timestamptz NULL,
    retrieved_at timestamptz NULL,
    orthanc_job_ids text[] NULL,
    last_retrieval_state varchar(50) NULL,
    last_retrieval_error text NULL,
    last_retrieval_error_details text NULL,
    last_retrieval_checked_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    CONSTRAINT uq_inference_ingestion_candidates_job_study
        UNIQUE (ingestion_job_id, study_instance_uid),
    CONSTRAINT fk_inference_ingestion_candidates_job
        FOREIGN KEY (ingestion_job_id)
        REFERENCES inference_ingestion_jobs (id)
        ON DELETE CASCADE
);

CREATE OR REPLACE FUNCTION func_ingestion_candidates_update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER trigger_update_inference_ingestion_candidates_updated_at
    BEFORE UPDATE ON inference_ingestion_candidates
    FOR EACH ROW
    EXECUTE FUNCTION func_ingestion_candidates_update_updated_at();
