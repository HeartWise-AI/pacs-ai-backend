-- AI Predictions page (GET /v1/inference/predictions). Additive indexes only.
--
-- "My runs" looks up the exams a user launched any run on, finished runs
-- included; 000007 only indexes the requester's active runs.
CREATE INDEX IF NOT EXISTS idx_ingestion_processing_runs_tenant_requester_study
    ON ingestion_processing_runs (tenant_id, requested_by_user_id, study_instance_uid)
    WHERE requested_by_user_id IS NOT NULL;

-- The date filter narrows the exams to those with a run updated in the range
-- before the per-model latest state is worked out.
CREATE INDEX IF NOT EXISTS idx_ingestion_processing_runs_tenant_updated
    ON ingestion_processing_runs (tenant_id, updated_at DESC);
