-- PostgreSQL cannot drop one enum value; rebuild the type without it.
-- Viewer runs are preserved in history as manual runs.
ALTER TYPE inference_ingestion_processing_run_trigger
    RENAME TO inference_ingestion_processing_run_trigger_with_viewer;

CREATE TYPE inference_ingestion_processing_run_trigger AS ENUM (
    'AUTO',
    'MANUAL_REPROCESS',
    'LEGACY_IMPORT'
);

ALTER TABLE ingestion_processing_runs
    ALTER COLUMN run_trigger TYPE inference_ingestion_processing_run_trigger
    USING (
        CASE run_trigger::text
            WHEN 'VIEWER_REQUEST' THEN 'MANUAL_REPROCESS'
            ELSE run_trigger::text
        END
    )::inference_ingestion_processing_run_trigger;

DROP TYPE inference_ingestion_processing_run_trigger_with_viewer;
