ALTER TABLE ingestion_processing_runs
    ADD COLUMN requested_by_user_id varchar(128) NULL;

CREATE INDEX idx_ingestion_processing_runs_requester_active
    ON ingestion_processing_runs (tenant_id, requested_by_user_id, phase)
    WHERE requested_by_user_id IS NOT NULL AND phase <> 'TERMINAL';
