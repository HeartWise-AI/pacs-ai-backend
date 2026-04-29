DROP TRIGGER IF EXISTS trigger_refresh_inference_ingestion_candidate_processing_rollup
    ON inference_ingestion_processing_jobs;

DROP FUNCTION IF EXISTS trigger_refresh_inference_ingestion_candidate_processing_rollup();
DROP FUNCTION IF EXISTS func_refresh_inference_ingestion_candidate_processing_rollup(varchar);

DROP INDEX IF EXISTS idx_inference_ingestion_candidates_processing_status;

ALTER TABLE inference_ingestion_candidates
    DROP COLUMN IF EXISTS last_dispatch_attempted_at,
    DROP COLUMN IF EXISTS last_dispatch_error,
    DROP COLUMN IF EXISTS processing_status_at,
    DROP COLUMN IF EXISTS processing_status;

DROP TYPE IF EXISTS inference_ingestion_candidate_processing_status;
