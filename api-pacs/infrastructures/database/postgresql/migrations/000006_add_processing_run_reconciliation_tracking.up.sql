ALTER TABLE ingestion_processing_runs
    ADD COLUMN reconciliation_failure_count integer NOT NULL DEFAULT 0,
    ADD COLUMN last_reconciliation_at timestamptz NULL,
    ADD CONSTRAINT ck_ingestion_processing_runs_reconciliation_failure_count
        CHECK (reconciliation_failure_count >= 0);
