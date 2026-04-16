DROP TRIGGER IF EXISTS trigger_update_inference_ingestion_candidates_updated_at
    ON inference_ingestion_candidates;

DROP FUNCTION IF EXISTS func_ingestion_candidates_update_updated_at();

DROP TABLE IF EXISTS inference_ingestion_candidates;

DROP TYPE IF EXISTS inference_ingestion_candidate_status;
