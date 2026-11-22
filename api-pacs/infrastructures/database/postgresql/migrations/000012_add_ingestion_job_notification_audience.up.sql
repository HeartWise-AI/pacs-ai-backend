-- Who created an automatic ingestion job, and whether the runs it starts
-- notify anyone. Both are additive: existing jobs keep a NULL creator (their
-- runs notify workspace admins only) and notifications stay on.
ALTER TABLE ingestion_jobs
    ADD COLUMN IF NOT EXISTS created_by_user_id varchar(128) NULL,
    ADD COLUMN IF NOT EXISTS notifications_enabled boolean NOT NULL DEFAULT true;
