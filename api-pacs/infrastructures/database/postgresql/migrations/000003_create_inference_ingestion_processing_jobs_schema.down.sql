DROP TRIGGER IF EXISTS trigger_refresh_inference_ingestion_candidate_processing_rollup
    ON ingestion_processing_jobs;

DROP FUNCTION IF EXISTS trigger_refresh_inference_ingestion_candidate_processing_rollup();
DROP FUNCTION IF EXISTS func_refresh_inference_ingestion_candidate_processing_rollup(varchar);

DROP TRIGGER IF EXISTS trigger_update_inference_ingestion_processing_jobs_updated_at
    ON ingestion_processing_jobs;

DROP FUNCTION IF EXISTS func_ingestion_processing_jobs_update_updated_at();

DROP TABLE IF EXISTS ingestion_processing_jobs;

DROP TYPE IF EXISTS inference_ingestion_processing_job_status;
