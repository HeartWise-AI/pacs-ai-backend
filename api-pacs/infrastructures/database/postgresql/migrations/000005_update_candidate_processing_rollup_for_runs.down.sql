-- Restore the candidate-wide roll-up that existed before processing runs.
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

SELECT func_refresh_inference_ingestion_candidate_processing_rollup(candidate_id)
FROM (
    SELECT DISTINCT candidate_id
    FROM ingestion_processing_jobs
) candidates;
