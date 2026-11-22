-- AI Predictions page (GET /v1/inference/predictions). Additive indexes only.
--
-- "My runs" looks up the exams a user launched any run on, finished runs
-- included; 000007 only indexes the requester's active runs.
CREATE INDEX IF NOT EXISTS idx_ingestion_processing_runs_tenant_requester_study
    ON ingestion_processing_runs (tenant_id, requested_by_user_id, study_instance_uid)
    WHERE requested_by_user_id IS NOT NULL;

-- The date filter narrows the exams to those with activity in the range
-- before the per-model latest state is worked out. Activity is a real event,
-- never updated_at: a run created, or an execution completed or started (the
-- expression must stay exactly COALESCE(completed_at, started_at)).
CREATE INDEX IF NOT EXISTS idx_ingestion_processing_runs_tenant_created
    ON ingestion_processing_runs (tenant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ingestion_processing_jobs_tenant_activity
    ON ingestion_processing_jobs (tenant_id, (COALESCE(completed_at, started_at)) DESC);
