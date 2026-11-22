ALTER TABLE ingestion_processing_runs
    DROP COLUMN IF EXISTS cancelled_at,
    DROP COLUMN IF EXISTS cancelled_by_user_id;
