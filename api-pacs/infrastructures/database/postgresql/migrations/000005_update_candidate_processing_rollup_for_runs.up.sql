-- Keep the legacy candidate status compatible with run-aware execution states.
-- The richer, authoritative state remains on ingestion_processing_runs.
CREATE OR REPLACE FUNCTION func_refresh_inference_ingestion_candidate_processing_rollup(p_candidate_id varchar)
RETURNS void AS $$
DECLARE
    latest_processing_run_id varchar(50);
    total_count integer;
    pending_count integer;
    queued_count integer;
    running_count integer;
    completed_count integer;
    failed_count integer;
    skipped_count integer;
    cancelled_count integer;
    next_status inference_ingestion_candidate_processing_status;
BEGIN
    SELECT runs.id
    INTO latest_processing_run_id
    FROM ingestion_processing_runs runs
    JOIN ingestion_candidates candidates
      ON candidates.tenant_id = runs.tenant_id
     AND candidates.study_instance_uid = runs.study_instance_uid
    WHERE candidates.id = p_candidate_id
    ORDER BY runs.run_number DESC, runs.created_at DESC, runs.id DESC
    LIMIT 1;

    SELECT
        COUNT(*),
        COUNT(*) FILTER (WHERE status = 'pending'),
        COUNT(*) FILTER (WHERE status = 'queued'),
        COUNT(*) FILTER (WHERE status = 'running'),
        COUNT(*) FILTER (WHERE status = 'completed'),
        COUNT(*) FILTER (WHERE status = 'failed'),
        COUNT(*) FILTER (WHERE status = 'skipped'),
        COUNT(*) FILTER (WHERE status = 'cancelled')
    INTO
        total_count,
        pending_count,
        queued_count,
        running_count,
        completed_count,
        failed_count,
        skipped_count,
        cancelled_count
    FROM ingestion_processing_jobs
    WHERE candidate_id = p_candidate_id
      AND (
          processing_run_id = latest_processing_run_id
          OR (latest_processing_run_id IS NULL AND processing_run_id IS NULL)
      );

    IF total_count = 0 THEN
        UPDATE ingestion_candidates
        SET processing_status = NULL,
            processing_status_at = NULL
        WHERE id = p_candidate_id;
        RETURN;
    END IF;

    IF running_count > 0 THEN
        next_status = 'running';
    ELSIF pending_count + queued_count > 0 THEN
        next_status = 'queued';
    ELSIF completed_count > 0 AND failed_count + cancelled_count > 0 THEN
        next_status = 'partial';
    ELSIF completed_count > 0 THEN
        next_status = 'completed';
    ELSIF failed_count + cancelled_count > 0 THEN
        next_status = 'failed';
    ELSIF skipped_count = total_count THEN
        -- The legacy enum has no NO_RESULT state; skipped work is terminal and non-failing.
        next_status = 'completed';
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

-- Recalculate existing candidates because replacing a function does not fire row triggers.
SELECT func_refresh_inference_ingestion_candidate_processing_rollup(candidate_id)
FROM (
    SELECT DISTINCT candidate_id
    FROM ingestion_processing_jobs
) candidates;
