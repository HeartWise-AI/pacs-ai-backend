ALTER TABLE ingestion_processing_jobs
    DROP CONSTRAINT IF EXISTS uq_inference_ingestion_processing_jobs_run_candidate_model,
    DROP CONSTRAINT IF EXISTS fk_inference_ingestion_processing_jobs_processing_run,
    DROP COLUMN IF EXISTS processing_run_id,
    DROP COLUMN IF EXISTS skip_reason_code,
    DROP COLUMN IF EXISTS skip_reason_message,
    DROP COLUMN IF EXISTS last_event_id,
    DROP COLUMN IF EXISTS last_event_sequence;

ALTER TYPE inference_ingestion_processing_job_status
    RENAME TO inference_ingestion_processing_job_status_with_run_states;

CREATE TYPE inference_ingestion_processing_job_status AS ENUM (
    'queued',
    'running',
    'completed',
    'failed'
);

ALTER TABLE ingestion_processing_jobs
    ALTER COLUMN status TYPE inference_ingestion_processing_job_status
    USING (
        CASE status::text
            WHEN 'pending' THEN 'queued'
            WHEN 'skipped' THEN 'completed'
            WHEN 'cancelled' THEN 'failed'
            ELSE status::text
        END
    )::inference_ingestion_processing_job_status;

DROP TYPE inference_ingestion_processing_job_status_with_run_states;

-- The legacy schema can store only one processing row per candidate/model.
-- Preserve the most recently updated row when multiple processing runs exist.
WITH ranked_processing_jobs AS (
    SELECT
        id,
        ROW_NUMBER() OVER (
            PARTITION BY candidate_id, model_name
            ORDER BY updated_at DESC, created_at DESC, id DESC
        ) AS row_number
    FROM ingestion_processing_jobs
)
DELETE FROM ingestion_processing_jobs
WHERE id IN (
    SELECT id
    FROM ranked_processing_jobs
    WHERE row_number > 1
);

ALTER TABLE ingestion_processing_jobs
    ADD CONSTRAINT uq_inference_ingestion_processing_jobs_candidate_model
        UNIQUE (candidate_id, model_name);

DROP TRIGGER IF EXISTS trigger_update_inference_ingestion_processing_runs_updated_at
    ON ingestion_processing_runs;

DROP TABLE IF EXISTS ingestion_processing_runs;

DROP TYPE IF EXISTS inference_ingestion_processing_run_outcome;
DROP TYPE IF EXISTS inference_ingestion_processing_run_phase;
DROP TYPE IF EXISTS inference_ingestion_processing_run_trigger;
