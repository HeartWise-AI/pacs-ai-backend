DROP INDEX IF EXISTS idx_ingestion_processing_jobs_active_model;

-- The study-wide uniqueness cannot be restored while a viewer run is active
-- next to another active run. Cancel such viewer runs first.
UPDATE ingestion_processing_runs viewer
SET phase = 'TERMINAL',
    outcome = 'CANCELLED',
    completed_at = COALESCE(viewer.completed_at, CURRENT_TIMESTAMP),
    version = viewer.version + 1
WHERE viewer.run_trigger = 'VIEWER_REQUEST'
  AND viewer.phase <> 'TERMINAL'
  AND EXISTS (
      SELECT 1 FROM ingestion_processing_runs other
      WHERE other.tenant_id = viewer.tenant_id
        AND other.study_instance_uid = viewer.study_instance_uid
        AND other.id <> viewer.id
        AND other.phase <> 'TERMINAL'
  );

DROP INDEX IF EXISTS uq_inference_ingestion_processing_runs_active_study;

CREATE UNIQUE INDEX uq_inference_ingestion_processing_runs_active_study
    ON ingestion_processing_runs (tenant_id, study_instance_uid)
    WHERE phase <> 'TERMINAL';
