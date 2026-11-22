-- Kept in its own migration: a new enum value cannot be referenced in the
-- same transaction that adds it (000009 uses it in an index predicate).
ALTER TYPE inference_ingestion_processing_run_trigger
    ADD VALUE IF NOT EXISTS 'VIEWER_REQUEST';
