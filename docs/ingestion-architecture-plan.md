# Ingestion Architecture Plan

## Purpose

This document defines the target architecture for PACS ingestion in `api-pacs`.
It separates remote PACS discovery and retrieval from local study processing so
each step has a clear owner, durable state, and observable failure modes.

The companion integration plan, `docs/cardio-agent-integration-plan.md`, covers
how retrieved local studies are handed to `cardio-agent/study-service` for model
execution.

## Goals

- Track each candidate study through discovery, stability, retrieval, processing,
  and cleanup.
- Keep Go `api-pacs` authoritative for ingestion job configuration, tenant
  ownership, remote PACS access, and what models should run.
- Keep local processing services authoritative for model execution details and
  result payloads.
- Make every transition idempotent so runners can retry safely.
- Preserve enough correlation data for operators to answer why a study was
  retrieved, which job triggered it, and what happened next.

## Non-Goals

- Rewriting the existing ingestion runner in one large change.
- Moving all processing result JSON into the Go database immediately.
- Implementing retention and deletion before the retrieval and processing state
  machines are stable.

## Current State

`api-pacs` currently has:

- `inference_ingestion_jobs`: operator-defined ingestion schedules and model
  configuration.
- `inference_ingestion_candidates`: discovered remote studies and retrieval
  status.
- `inference_ingestion_processing_jobs`: per-model processing lifecycle storage.
- A discovery runner that polls remote PACS and marks candidates stable.
- A retrieval worker that issues C-MOVE, waits for local Orthanc presence, and
  marks candidates `RETRIEVED` or `FAILED`.

The candidate lifecycle stops at retrieval. There is no first-class processing
state or cleanup state in the Go control plane.

## Target Lifecycle

Each study should move through these states:

1. `DISCOVERED`
   A remote C-FIND result matched an active ingestion job.

2. `GROWING`
   The same study was seen again with changed series or instance counts.

3. `STABLE`
   The study has not changed for the configured stability window.

4. `RETRIEVAL_QUEUED`
   The study is ready for local retrieval and has been queued for the retrieval
   worker.

5. `RETRIEVING`
   A retrieval attempt is actively issuing or waiting on Orthanc jobs.

6. `RETRIEVED`
   The study is present in local pacs-ai-backend Orthanc.

7. `PROCESSING_QUEUED`
   The local study has been handed to the processing layer and expected model
   jobs have been recorded.

8. `PROCESSING`
   At least one expected model job is running.

9. `PROCESSED`
   Every expected model job completed successfully.

10. `PARTIAL`
    At least one expected model job completed and at least one failed, with no
    model jobs still queued or running.

11. `FAILED`
    Retrieval failed, or every expected processing job failed.

12. `CLEANUP_QUEUED`
    The study is eligible for local retention cleanup.

13. `CLEANED`
    Retention cleanup completed and the local study artifacts were removed or
    archived according to policy.

Existing candidate status can remain focused on discovery and retrieval while
processing and cleanup are represented by sibling tables and rollup columns.

## Phase 1 - Stabilize Current Retrieval State

Keep the current discovery runner and retrieval worker, but make state
transitions explicit and observable.

Work:

- Continue writing discovery and retrieval state to
  `inference_ingestion_candidates`.
- Preserve `orthanc_job_ids`, `last_retrieval_state`, `last_retrieval_error`,
  `last_retrieval_error_details`, and `last_retrieval_checked_at`.
- Add metrics for candidate transitions and retrieval outcomes.
- Ensure retrieval worker retries are idempotent:
  - if a study is already local, mark it retrieved;
  - if Orthanc job IDs already exist, resume waiting on those jobs;
  - if C-MOVE returns duplicate/already-local behavior, re-check local presence.

Acceptance:

- A stable study becomes `RETRIEVAL_QUEUED`.
- A successful C-MOVE or already-local study becomes `RETRIEVED`.
- Failed or timed-out Orthanc jobs preserve enough error context for operators.

## Phase 2 - Retrieval Attempt Redesign

Move retrieval attempts out of the candidate row into their own durable table.
This avoids overloading the candidate record when a study is retried, resumed,
or retrieved by multiple mechanisms.

New table: `inference_ingestion_retrieval_attempts`

Suggested columns:

- `id`
- `candidate_id`
- `tenant_id`
- `ingestion_job_id`
- `study_instance_uid`
- `status`:
  `queued | running | succeeded | failed | timed_out | cancelled`
- `trigger`:
  `worker | manual_retry | reconciliation`
- `orthanc_job_ids`
- `started_at`
- `completed_at`
- `last_checked_at`
- `error_message`
- `error_details`
- `created_at`
- `updated_at`

Candidate relationship:

- `inference_ingestion_candidates.current_retrieval_attempt_id` points at the
  latest active or terminal attempt.
- Candidate `status` remains the high-level retrieval rollup.

Worker behavior:

- When a stable candidate is ready, create or reuse a queued retrieval attempt.
- The retrieval worker claims queued attempts with row-level locking.
- Attempt completion updates both the attempt row and the candidate rollup in one
  transaction.
- Manual retries create a new attempt instead of overwriting old error context.

Acceptance:

- Retrying a failed candidate creates a new attempt while preserving the previous
  failure details.
- Operators can inspect every retrieval attempt for a candidate.
- The existing candidate list still shows the latest high-level status.

## Phase 3 - Processing State

Processing is the handoff from local retrieval to model execution. Go remains the
source of truth for tenant, ingestion job, candidate, and the selected model
for each dispatch. The processing service owns execution and result JSON.

New Go table: `inference_ingestion_processing_jobs`

Suggested columns:

- `id`
- `candidate_id`
- `tenant_id`
- `ingestion_job_id`
- `retrieval_attempt_id`
- `study_instance_uid`
- `study_service_job_id`
- `model_name`
- `model_version`
- `modality`
- `status`:
  `queued | running | completed | failed`
- `error_message`
- `started_at`
- `completed_at`
- `created_at`
- `updated_at`

Candidate rollup:

- Add `processing_status`:
  `queued | running | completed | partial | failed`
- Add `processing_status_at`

Rollup rules:

- `completed` iff every expected processing row is `completed`.
- `failed` iff every expected processing row is `failed`.
- `partial` iff at least one row is `completed` and at least one row is `failed`,
  with no queued or running rows.
- `running` iff any row is `running`.
- `queued` otherwise.

Expected jobs:

- Expected processing rows are created at dispatch time, one per dispatch
  request / matching ingestion job row.
- Callbacks from the processing service upsert by `(candidate_id, model_name)`.
- Out-of-order transitions are ignored and logged.

Acceptance:

- Operators can see processing status without joining across service databases.
- A multi-model candidate can represent `partial` accurately.
- Callback replay is safe.

## Phase 4 - Cleanup and Retention

Cleanup should only run after retrieval and processing have reached a terminal
state or after an explicit operator policy allows early deletion.

Retention policy inputs:

- Tenant-level retention configuration.
- Ingestion-job-level override.
- Study age in local Orthanc.
- Processing terminal state.
- Manual hold flags.
- Legal or audit hold flags, if introduced later.

New table: `inference_ingestion_cleanup_jobs`

Suggested columns:

- `id`
- `candidate_id`
- `tenant_id`
- `study_instance_uid`
- `orthanc_study_id`
- `status`:
  `queued | running | completed | failed | skipped | held`
- `policy_name`
- `eligible_at`
- `started_at`
- `completed_at`
- `error_message`
- `error_details`
- `created_at`
- `updated_at`

Cleanup worker behavior:

- Periodically identify candidates eligible for cleanup.
- Skip candidates with active retrieval or processing jobs.
- Resolve `study_instance_uid` to local Orthanc study IDs immediately before
  deletion.
- Delete or archive according to policy.
- Record terminal cleanup state.

Safety rules:

- Never delete studies that are still processing.
- Never delete studies with a manual hold.
- Prefer idempotent deletion: missing local Orthanc resources should mark cleanup
  complete with a note, not fail forever.
- Emit metrics for cleanup attempts, failures, skipped held studies, and deleted
  bytes if available.

Acceptance:

- A processed study becomes cleanup-eligible after the configured retention
  period.
- A held study is skipped and visible to operators.
- Re-running cleanup for a missing Orthanc resource is safe.

## Phase 5 - Operator API and UI

Expose a coherent ingestion view through Go APIs.

Recommended API surfaces:

- List ingestion jobs.
- List candidates by job with discovery, retrieval, processing, and cleanup
  rollups.
- Detail endpoint for one candidate with:
  - candidate metadata;
  - retrieval attempts;
  - processing jobs;
  - cleanup jobs;
  - relevant errors and timestamps.
- Manual retry endpoints for retrieval, processing, and cleanup.

Tenant scoping:

- Every query must be scoped to the authenticated tenant.
- Cross-tenant lookup by `study_instance_uid` must return 404 unless the caller
  has an explicit superuser role.

## Observability

Minimum metrics:

- `ingestion_candidates_discovered_total`
- `ingestion_candidate_transitions_total{from,to}`
- `ingestion_retrieval_attempts_total{outcome}`
- `ingestion_retrieval_latency_seconds`
- `ingestion_processing_callbacks_total{status,outcome}`
- `ingestion_processing_rollups_total{status}`
- `ingestion_cleanup_jobs_total{outcome}`

Minimum logs:

- Include `tenant_id`, `ingestion_job_id`, `candidate_id`, `study_instance_uid`,
  and `request_id` on every lifecycle transition.
- Include Orthanc job IDs on retrieval attempt logs.
- Include processing service job IDs on processing callback logs.

## Open Questions

- Should `RETRIEVING`, processing rollups, and cleanup rollups become first-class
  candidate enum values, or remain sibling-table rollups exposed by query APIs?
- Should processing result JSON remain only in study-service, or should Go store
  a lightweight result reference?
- Should cleanup delete from Orthanc directly, call an Orthanc service wrapper,
  or enqueue work into a separate storage-retention service?
- How long should retrieval attempt and callback dead-letter records be retained?
