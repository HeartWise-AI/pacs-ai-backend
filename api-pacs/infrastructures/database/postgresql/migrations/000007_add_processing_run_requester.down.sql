DROP INDEX IF EXISTS idx_ingestion_processing_runs_requester_active;

ALTER TABLE ingestion_processing_runs
    DROP COLUMN IF EXISTS requested_by_user_id;
