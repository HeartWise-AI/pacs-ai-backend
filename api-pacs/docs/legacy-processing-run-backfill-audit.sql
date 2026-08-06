-- Read-only preflight for the LEGACY_IMPORT processing-run backfill.
-- This script returns aggregate operational metadata only. It intentionally
-- excludes patient identifiers, DICOM metadata, inference results, and tenant IDs.

SELECT
    COUNT(*) AS orphan_jobs,
    COUNT(DISTINCT jobs.candidate_id) AS candidates,
    COUNT(DISTINCT (jobs.tenant_id, candidates.study_instance_uid)) AS logical_studies,
    COUNT(DISTINCT jobs.tenant_id) AS tenant_count
FROM ingestion_processing_jobs jobs
JOIN ingestion_candidates candidates ON candidates.id = jobs.candidate_id
WHERE jobs.processing_run_id IS NULL;

SELECT jobs.status, COUNT(*) AS orphan_jobs
FROM ingestion_processing_jobs jobs
WHERE jobs.processing_run_id IS NULL
GROUP BY jobs.status
ORDER BY jobs.status;

SELECT
    COUNT(*) FILTER (WHERE jobs.started_at IS NULL) AS missing_started_at,
    COUNT(*) FILTER (WHERE jobs.completed_at IS NULL) AS missing_completed_at,
    COUNT(*) FILTER (WHERE jobs.model_version IS NULL) AS missing_model_version,
    COUNT(*) FILTER (WHERE jobs.modality IS NULL) AS missing_modality,
    COUNT(*) FILTER (WHERE jobs.study_service_job_id IS NULL) AS missing_study_service_job_id
FROM ingestion_processing_jobs jobs
WHERE jobs.processing_run_id IS NULL;

SELECT COUNT(*) AS tenant_mismatches
FROM ingestion_processing_jobs jobs
JOIN ingestion_candidates candidates ON candidates.id = jobs.candidate_id
WHERE jobs.processing_run_id IS NULL
  AND jobs.tenant_id IS DISTINCT FROM candidates.tenant_id;

SELECT COUNT(*) AS studies_with_multiple_candidates
FROM (
    SELECT jobs.tenant_id, candidates.study_instance_uid
    FROM ingestion_processing_jobs jobs
    JOIN ingestion_candidates candidates ON candidates.id = jobs.candidate_id
    WHERE jobs.processing_run_id IS NULL
    GROUP BY jobs.tenant_id, candidates.study_instance_uid
    HAVING COUNT(DISTINCT jobs.candidate_id) > 1
) grouped;

SELECT COUNT(*) AS duplicate_study_models
FROM (
    SELECT jobs.tenant_id, candidates.study_instance_uid, jobs.model_name
    FROM ingestion_processing_jobs jobs
    JOIN ingestion_candidates candidates ON candidates.id = jobs.candidate_id
    WHERE jobs.processing_run_id IS NULL
    GROUP BY jobs.tenant_id, candidates.study_instance_uid, jobs.model_name
    HAVING COUNT(*) > 1
) grouped;

SELECT COUNT(*) AS orphan_studies_with_existing_runs
FROM (
    SELECT DISTINCT jobs.tenant_id, candidates.study_instance_uid
    FROM ingestion_processing_jobs jobs
    JOIN ingestion_candidates candidates ON candidates.id = jobs.candidate_id
    JOIN ingestion_processing_runs runs
      ON runs.tenant_id = jobs.tenant_id
     AND runs.study_instance_uid = candidates.study_instance_uid
    WHERE jobs.processing_run_id IS NULL
) grouped;

SELECT processing_mix, COUNT(*) AS logical_studies
FROM (
    SELECT
        jobs.tenant_id,
        candidates.study_instance_uid,
        string_agg(DISTINCT jobs.status::text, ',' ORDER BY jobs.status::text) AS processing_mix
    FROM ingestion_processing_jobs jobs
    JOIN ingestion_candidates candidates ON candidates.id = jobs.candidate_id
    WHERE jobs.processing_run_id IS NULL
    GROUP BY jobs.tenant_id, candidates.study_instance_uid
) grouped
GROUP BY processing_mix
ORDER BY logical_studies DESC, processing_mix;
