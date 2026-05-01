DROP TRIGGER IF EXISTS trigger_update_inference_ingestion_jobs_updated_at ON ingestion_jobs;
DROP FUNCTION IF EXISTS func_update_updated_at();
DROP TABLE IF EXISTS ingestion_jobs;
DROP TYPE IF EXISTS inference_ingestion_job_status;
