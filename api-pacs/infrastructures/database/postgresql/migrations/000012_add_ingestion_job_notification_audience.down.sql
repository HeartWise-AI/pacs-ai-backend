ALTER TABLE ingestion_jobs
    DROP COLUMN IF EXISTS notifications_enabled,
    DROP COLUMN IF EXISTS created_by_user_id;
