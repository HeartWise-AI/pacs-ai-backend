DROP TRIGGER IF EXISTS trigger_update_inference_ingestion_candidates_updated_at
    ON ingestion_candidates;

DROP FUNCTION IF EXISTS func_ingestion_candidates_update_updated_at();

DROP INDEX IF EXISTS idx_inference_ingestion_candidates_processing_status;

DROP TABLE IF EXISTS ingestion_candidates;

DROP TYPE IF EXISTS inference_ingestion_candidate_processing_status;
DROP TYPE IF EXISTS inference_ingestion_candidate_status;
