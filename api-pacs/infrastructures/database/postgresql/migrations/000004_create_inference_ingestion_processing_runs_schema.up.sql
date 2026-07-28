CREATE TYPE inference_ingestion_processing_run_trigger AS ENUM (
    'AUTO',
    'MANUAL_REPROCESS',
    'LEGACY_IMPORT'
);

CREATE TYPE inference_ingestion_processing_run_phase AS ENUM (
    'QUEUED',
    'PROCESSING',
    'TERMINAL'
);

CREATE TYPE inference_ingestion_processing_run_outcome AS ENUM (
    'SUCCESS',
    'SUCCESS_WITH_SKIPS',
    'PARTIAL_SUCCESS',
    'NO_RESULT',
    'FAILED',
    'CANCELLED'
);

CREATE TABLE ingestion_processing_runs (
    id varchar(50) NOT NULL,
    tenant_id varchar(50) NOT NULL,
    study_instance_uid varchar(255) NOT NULL,
    run_number integer NOT NULL,
    run_trigger inference_ingestion_processing_run_trigger NOT NULL,
    phase inference_ingestion_processing_run_phase NOT NULL,
    outcome inference_ingestion_processing_run_outcome NULL,
    attention_required boolean NOT NULL DEFAULT false,
    attention_reasons jsonb NOT NULL DEFAULT '[]'::jsonb,
    version bigint NOT NULL DEFAULT 1,
    started_at timestamptz NULL,
    completed_at timestamptz NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    CONSTRAINT uq_inference_ingestion_processing_runs_study_run_number
        UNIQUE (tenant_id, study_instance_uid, run_number)
);

CREATE UNIQUE INDEX uq_inference_ingestion_processing_runs_active_study
    ON ingestion_processing_runs (tenant_id, study_instance_uid)
    WHERE phase <> 'TERMINAL';

CREATE INDEX idx_inference_ingestion_processing_runs_tenant_study_created
    ON ingestion_processing_runs (tenant_id, study_instance_uid, created_at DESC);

CREATE INDEX idx_inference_ingestion_processing_runs_tenant_phase_updated
    ON ingestion_processing_runs (tenant_id, phase, updated_at);

CREATE TRIGGER trigger_update_inference_ingestion_processing_runs_updated_at
    BEFORE UPDATE ON ingestion_processing_runs
    FOR EACH ROW
    EXECUTE FUNCTION func_update_updated_at();

ALTER TYPE inference_ingestion_processing_job_status
    ADD VALUE 'pending' BEFORE 'queued';

ALTER TYPE inference_ingestion_processing_job_status
    ADD VALUE 'skipped';

ALTER TYPE inference_ingestion_processing_job_status
    ADD VALUE 'cancelled';

ALTER TABLE ingestion_processing_jobs
    DROP CONSTRAINT uq_inference_ingestion_processing_jobs_candidate_model,
    ADD COLUMN processing_run_id varchar(50) NULL,
    ADD COLUMN skip_reason_code varchar(100) NULL,
    ADD COLUMN skip_reason_message text NULL,
    ADD COLUMN last_event_id varchar(100) NULL,
    ADD COLUMN last_event_sequence bigint NULL,
    ADD CONSTRAINT fk_inference_ingestion_processing_jobs_processing_run
        FOREIGN KEY (processing_run_id)
        REFERENCES ingestion_processing_runs (id)
        ON DELETE CASCADE,
    ADD CONSTRAINT uq_inference_ingestion_processing_jobs_run_candidate_model
        UNIQUE NULLS NOT DISTINCT (processing_run_id, candidate_id, model_name);

CREATE INDEX idx_inference_ingestion_processing_jobs_processing_run_id
    ON ingestion_processing_jobs (processing_run_id);
