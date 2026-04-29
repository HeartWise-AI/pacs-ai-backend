CREATE TYPE inference_ingestion_candidate_processing_status AS ENUM (
    'queued',
    'running',
    'completed',
    'partial',
    'failed'
);

ALTER TABLE inference_ingestion_candidates
    ADD COLUMN processing_status inference_ingestion_candidate_processing_status NULL,
    ADD COLUMN processing_status_at timestamptz NULL,
    ADD COLUMN last_dispatch_error text NULL,
    ADD COLUMN last_dispatch_attempted_at timestamptz NULL;

CREATE INDEX idx_inference_ingestion_candidates_processing_status
    ON inference_ingestion_candidates (processing_status);

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
    FROM inference_ingestion_processing_jobs
    WHERE candidate_id = p_candidate_id;

    IF total_count = 0 THEN
        UPDATE inference_ingestion_candidates
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

    UPDATE inference_ingestion_candidates
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
    AFTER INSERT OR UPDATE OR DELETE ON inference_ingestion_processing_jobs
    FOR EACH ROW
    EXECUTE FUNCTION trigger_refresh_inference_ingestion_candidate_processing_rollup();
