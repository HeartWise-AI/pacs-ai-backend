CREATE TYPE inference_ingestion_processing_job_status AS ENUM (
    'queued',
    'running',
    'completed',
    'failed'
);

CREATE TABLE ingestion_processing_jobs (
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
        REFERENCES ingestion_candidates (id)
        ON DELETE CASCADE
);

CREATE INDEX idx_inference_ingestion_processing_jobs_tenant_id
    ON ingestion_processing_jobs (tenant_id);

CREATE INDEX idx_inference_ingestion_processing_jobs_candidate_id
    ON ingestion_processing_jobs (candidate_id);

CREATE INDEX idx_inference_ingestion_processing_jobs_status
    ON ingestion_processing_jobs (status);

CREATE OR REPLACE FUNCTION func_ingestion_processing_jobs_update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER trigger_update_inference_ingestion_processing_jobs_updated_at
    BEFORE UPDATE ON ingestion_processing_jobs
    FOR EACH ROW
    EXECUTE FUNCTION func_ingestion_processing_jobs_update_updated_at();

CREATE OR REPLACE FUNCTION func_refresh_inference_ingestion_candidate_processing_rollup(p_candidate_id varchar)
RETURNS void AS $$
DECLARE
    total_count integer;
    queued_count integer;
    running_count integer;
    completed_count integer;
    failed_count integer;
    next_status inference_ingestion_candidate_processing_status;
BEGIN
    SELECT
        COUNT(*),
        COUNT(*) FILTER (WHERE status = 'queued'),
        COUNT(*) FILTER (WHERE status = 'running'),
        COUNT(*) FILTER (WHERE status = 'completed'),
        COUNT(*) FILTER (WHERE status = 'failed')
    INTO
        total_count,
        queued_count,
        running_count,
        completed_count,
        failed_count
    FROM ingestion_processing_jobs
    WHERE candidate_id = p_candidate_id;

    IF total_count = 0 THEN
        UPDATE ingestion_candidates
        SET processing_status = NULL,
            processing_status_at = NULL
        WHERE id = p_candidate_id;
        RETURN;
    END IF;

    IF running_count > 0 THEN
        next_status = 'running';
    ELSIF queued_count > 0 THEN
        next_status = 'queued';
    ELSIF completed_count = total_count THEN
        next_status = 'completed';
    ELSIF failed_count = total_count THEN
        next_status = 'failed';
    ELSIF completed_count > 0 AND failed_count > 0 THEN
        next_status = 'partial';
    ELSE
        next_status = 'queued';
    END IF;

    UPDATE ingestion_candidates
    SET processing_status = next_status,
        processing_status_at = CASE
            WHEN processing_status IS DISTINCT FROM next_status OR processing_status_at IS NULL
                THEN CURRENT_TIMESTAMP
            ELSE processing_status_at
        END
    WHERE id = p_candidate_id;
END;
$$ language 'plpgsql';

CREATE OR REPLACE FUNCTION trigger_refresh_inference_ingestion_candidate_processing_rollup()
RETURNS TRIGGER AS $$
BEGIN
    PERFORM func_refresh_inference_ingestion_candidate_processing_rollup(COALESCE(NEW.candidate_id, OLD.candidate_id));
    RETURN COALESCE(NEW, OLD);
END;
$$ language 'plpgsql';

CREATE TRIGGER trigger_refresh_inference_ingestion_candidate_processing_rollup
    AFTER INSERT OR UPDATE OR DELETE ON ingestion_processing_jobs
    FOR EACH ROW
    EXECUTE FUNCTION trigger_refresh_inference_ingestion_candidate_processing_rollup();
