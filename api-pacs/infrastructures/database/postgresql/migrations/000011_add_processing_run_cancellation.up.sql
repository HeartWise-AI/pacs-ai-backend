-- A user can cancel the executions of a run that have not started computing.
-- The run keeps who asked and when: the aggregate outcome and the automatic
-- ingestion planner both read it.
ALTER TABLE ingestion_processing_runs
    ADD COLUMN cancelled_by_user_id varchar(128) NULL,
    ADD COLUMN cancelled_at timestamptz NULL;
