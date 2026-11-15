-- Study-wide runs (AUTO, MANUAL_REPROCESS, LEGACY_IMPORT) stay limited to one
-- active run per study. Single-model VIEWER_REQUEST runs may coexist with
-- them; the API serializes them per study+model under the study advisory lock
-- and rejects a request while that model already has a pending/queued/running
-- execution for the study.
DROP INDEX IF EXISTS uq_inference_ingestion_processing_runs_active_study;

CREATE UNIQUE INDEX uq_inference_ingestion_processing_runs_active_study
    ON ingestion_processing_runs (tenant_id, study_instance_uid)
    WHERE phase <> 'TERMINAL' AND run_trigger <> 'VIEWER_REQUEST';

CREATE INDEX IF NOT EXISTS idx_ingestion_processing_jobs_active_model
    ON ingestion_processing_jobs (tenant_id, model_name)
    WHERE status IN ('pending', 'queued', 'running');
