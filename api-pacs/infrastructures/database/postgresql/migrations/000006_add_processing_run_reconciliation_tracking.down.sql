ALTER TABLE ingestion_processing_runs
    DROP CONSTRAINT IF EXISTS ck_ingestion_processing_runs_reconciliation_failure_count,
    DROP COLUMN IF EXISTS last_reconciliation_at,
    DROP COLUMN IF EXISTS reconciliation_failure_count;
