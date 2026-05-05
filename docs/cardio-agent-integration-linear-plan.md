# Cardio-Agent Integration Linear Plan

This document turns [cardio-agent-integration-plan.md](/home/pacs-ai/pacs-ai-backend/docs/cardio-agent-integration-plan.md:1) into a ready-to-copy Linear planning structure.

It is organized around:

- two teams: `PACS-AI` and `Cardio-Agent`
- one umbrella issue per team
- four sprints
- explicit parent and dependency relationships

## Teams

| Team | Scope |
|---|---|
| `PACS-AI` | Go ingestion orchestration, dispatch, callback intake, processing rollup |
| `Cardio-Agent` | Python study-service execution API, queueing, execution records, standalone behavior |

## Parent Issues

| Team | Parent / Umbrella |
|---|---|
| `PACS-AI` | `PACS-232 CardioAgent ↔ StudyProcessingService Integration` |
| `Cardio-Agent` | `CA: Study-service execution API for PACS-AI integration` |

Recommendation:

- create the `Cardio-Agent` umbrella under project `CardioAgent - Automated Orthanc Pipeline`
- link both umbrella issues as `related`

## Sprint 1 — Foundation and Contracts

Goal: land the environment, schema, observability, and contract prerequisites before any endpoint or callback starts writing integration rows.

| Team | Title | Parent | Depends On | Description |
|---|---|---|---|---|
| `Cardio-Agent` | `Point study-service at pacs-ai-backend Orthanc` | `CA: Study-service execution API for PACS-AI integration` |  | Update compose and settings so study-service reads from the same local Orthanc as pacs-ai-backend. Acceptance check: `curl http://orthanc:8042/system` from the study-service container. |
| `Cardio-Agent` | `Join cardio-agent to the pacs-ai docker network` | `CA: Study-service execution API for PACS-AI integration` | `Point study-service at pacs-ai-backend Orthanc` | Add the shared external docker network and remove stale network assumptions that only work in standalone mode. |
| `Cardio-Agent` | `Add execution mode flags for polling and Go callbacks` | `CA: Study-service execution API for PACS-AI integration` |  | Add `ENABLE_ORTHANC_POLLING` and `ENABLE_GO_CALLBACKS`, log resolved mode at startup, and support standalone polling, integrated explicit-dispatch, and hybrid fallback modes. |
| `Cardio-Agent` | `Add ORTHANC_POLL_START_MODE to prevent first-run floods` | `CA: Study-service execution API for PACS-AI integration` | `Add execution mode flags for polling and Go callbacks` | Add `ORTHANC_POLL_START_MODE={latest,backlog,since_timestamp}`, document the behavior, and log the resolved start mode and starting sequence at boot. |
| `Cardio-Agent` | `Add health endpoint for study-service dependencies` | `CA: Study-service execution API for PACS-AI integration` | `Point study-service at pacs-ai-backend Orthanc` | Add `/health` covering API liveness and basic DB, Redis/Celery, and Orthanc readiness. |
| `Cardio-Agent` | `Add Prometheus metrics endpoint to study-service` | `CA: Study-service execution API for PACS-AI integration` |  | Add `/metrics` support for FastAPI and worker metrics and create a shared metrics module. |
| `Cardio-Agent` | `Extend modality mapping and add UNKNOWN modality counter` | `CA: Study-service execution API for PACS-AI integration` | `Add Prometheus metrics endpoint to study-service` | Extend the modality map for the modalities Go may retrieve and increment `study_service_unknown_modality_total` with alertable visibility on unknown values. |
| `Cardio-Agent` | `Introduce Alembic and baseline study-service schema` | `CA: Study-service execution API for PACS-AI integration` |  | Add Alembic, create a baseline revision, stamp existing deployments rather than replaying table creation, and replace implicit `create_all` schema management with versioned migrations. |
| `Cardio-Agent` | `Replace hardcoded modality-to-model selection with model container registry` | `CA: Study-service execution API for PACS-AI integration` | `PACS-AI / Define requested-model configuration shape for inference ingestion jobs` | Refactor study-service config to `MODEL_CONTAINERS` lookup by model name, align on model naming/version conventions with Go, and keep only a conservative fallback allowlist for polling mode. |
| `PACS-AI` | `Define requested-model configuration shape for inference ingestion jobs` | `PACS-232 CardioAgent ↔ StudyProcessingService Integration` |  | Confirm that `inference_ingestion_jobs` stays one row = one model, using the existing model fields and `modalities`; multiple models for one study are expressed as multiple matching rows and multiple dispatches. |
| `PACS-AI` | `Define study-service ingest and callback contracts` | `PACS-232 CardioAgent ↔ StudyProcessingService Integration` |  | Finalize `POST /ingest/study` and callback payloads, including nullable standalone fields, request IDs, and idempotency rules. |

## Sprint 2 — Endpoint and Data Model Alignment

Goal: make `POST /ingest/study` safe to ship by landing the correlation columns, dedupe, auth, and adoption logic before the endpoint is live.

| Team | Title | Parent | Depends On | Description |
|---|---|---|---|---|
| `Cardio-Agent` | `Add Go correlation fields to pipeline_jobs` | `CA: Study-service execution API for PACS-AI integration` | `Introduce Alembic and baseline study-service schema` | Add nullable `tenant_id`, `ingestion_job_id`, `candidate_id`, and `retrieval_attempt_id` to `pipeline_jobs` and return them in APIs. |
| `Cardio-Agent` | `Add tenant-aware dedupe index for pipeline jobs` | `CA: Study-service execution API for PACS-AI integration` | `Introduce Alembic and baseline study-service schema`, `Add Go correlation fields to pipeline_jobs` | Add DB-backed dedupe on `(tenant_id, study_instance_uid, modality, model_name)` including NULL-safe standalone behavior. |
| `Cardio-Agent` | `Implement polling fallback adoption logic for tenant-attributed dispatch` | `CA: Study-service execution API for PACS-AI integration` | `Add Go correlation fields to pipeline_jobs`, `Add tenant-aware dedupe index for pipeline jobs` | When a later integrated dispatch arrives for a NULL-tenant fallback job, adopt the existing row instead of creating a second one. |
| `Cardio-Agent` | `Refactor polling enqueue path into reusable ingest helper` | `CA: Study-service execution API for PACS-AI integration` | `Add Go correlation fields to pipeline_jobs`, `Add tenant-aware dedupe index for pipeline jobs`, `Implement polling fallback adoption logic for tenant-attributed dispatch` | Extract the minimal shared create/enqueue path from the current poller logic first, so both polling and the future ingest endpoint use the same dedupe and job-creation path from day one. |
| `Cardio-Agent` | `Implement POST /ingest/study as canonical execution endpoint` | `CA: Study-service execution API for PACS-AI integration` | `PACS-AI / Define study-service ingest and callback contracts`, `Add Go correlation fields to pipeline_jobs`, `Add tenant-aware dedupe index for pipeline jobs`, `Implement polling fallback adoption logic for tenant-attributed dispatch`, `Refactor polling enqueue path into reusable ingest helper` | Add `POST /ingest/study`, validate payload and `orthanc_study_id`, accept one model per request, and return the created or deduped job ID. Support nullable Go correlation IDs for standalone/manual dispatch. |
| `Cardio-Agent` | `Protect ingest endpoint with internal auth` | `CA: Study-service execution API for PACS-AI integration` | `Implement POST /ingest/study as canonical execution endpoint` | Require internal bearer token auth for `POST /ingest/study`, while allowing explicitly configured local-dev behavior. This ships with the endpoint, not after it. |
| `Cardio-Agent` | `Validate requested model availability at ingest time` | `CA: Study-service execution API for PACS-AI integration` | `Implement POST /ingest/study as canonical execution endpoint` | Return `422` for unknown models; treat unreachable-but-configured containers as runtime execution failures rather than request validation errors. |
| `Cardio-Agent` | `Add standalone helper support for upload plus ingest dispatch` | `CA: Study-service execution API for PACS-AI integration` | `Implement POST /ingest/study as canonical execution endpoint` | Extend or document the existing helper script so it can upload a study to Orthanc and call `POST /ingest/study` with `study_instance_uid`, `orthanc_study_id`, `modality`, `model_name`, and `model_version`. |
| `PACS-AI` | `Add study-service dispatcher interface and request builder` | `PACS-232 CardioAgent ↔ StudyProcessingService Integration` | `Define requested-model configuration shape for inference ingestion jobs`, `Define study-service ingest and callback contracts` | Add the Go-side dispatcher abstraction and request struct for `POST /ingest/study`, using the agreed single-model payload and emitting one request per matching ingestion job row. |
| `PACS-AI` | `Add inference ingestion processing jobs table` | `PACS-232 CardioAgent ↔ StudyProcessingService Integration` |  | Create `inference_ingestion_processing_jobs` for per-model processing state keyed by candidate and model, with `tenant_id NOT NULL` from the first migration. Pull this forward so PACS-AI starts the storage work before callback and rollup wiring begins. |

## Sprint 3 — Go-Driven Integration

Goal: have Go dispatch processing after retrieval success and receive per-model completion state back from Python, with tenant-aware storage and observability from day one.

| Team | Title | Parent | Depends On | Description |
|---|---|---|---|---|
| `PACS-AI` | `Implement study-service processing callback endpoint in Go` | `PACS-232 CardioAgent ↔ StudyProcessingService Integration` | `Add inference ingestion processing jobs table`, `Define study-service ingest and callback contracts` | Add an internal callback endpoint with token auth, idempotent upsert behavior, and state-transition validation. |
| `PACS-AI` | `Add candidate processing rollup from per-model job state` | `PACS-232 CardioAgent ↔ StudyProcessingService Integration` | `Add inference ingestion processing jobs table`, `Implement study-service processing callback endpoint in Go` | Compute candidate-level `processing_status` and timestamp from the per-model processing rows. |
| `PACS-AI` | `Dispatch study-service ingest request after retrieval success` | `PACS-232 CardioAgent ↔ StudyProcessingService Integration` | `Add study-service dispatcher interface and request builder`, `Cardio-Agent / Implement POST /ingest/study as canonical execution endpoint` | Hook dispatch into `persistCandidateRetrievalResult` on retrieval success, with async execution, timeout, retry, request ID propagation, and concurrency cap. |
| `PACS-AI` | `Persist study-service dispatch failures on ingestion candidates` | `PACS-232 CardioAgent ↔ StudyProcessingService Integration` | `Dispatch study-service ingest request after retrieval success` | Add candidate fields for `last_dispatch_error` and `last_dispatch_attempted_at` and write them on final dispatch failure. |
| `PACS-AI` | `Add dispatch and callback metrics for ingestion integration` | `PACS-232 CardioAgent ↔ StudyProcessingService Integration` | `Dispatch study-service ingest request after retrieval success`, `Implement study-service processing callback endpoint in Go` | Add metrics for dispatch attempts, outcomes, latency, and callback processing results as part of the definition of done for the integration path. |
| `PACS-AI` | `Enforce tenant-aware processing records in Go` | `PACS-232 CardioAgent ↔ StudyProcessingService Integration` | `Add inference ingestion processing jobs table`, `Add candidate processing rollup from per-model job state` | Ensure processing rows and candidate rollups are stored and queried with correct tenant context from the first release of the processing table. |
| `Cardio-Agent` | `Add GET /jobs/by-candidate/{candidate_id}` | `CA: Study-service execution API for PACS-AI integration` | `Add Go correlation fields to pipeline_jobs` | Expose processing jobs by Go candidate ID for reconciliation and operator views. |
| `Cardio-Agent` | `Emit Go processing callbacks from study-service jobs` | `CA: Study-service execution API for PACS-AI integration` | `PACS-AI / Implement study-service processing callback endpoint in Go`, `Protect internal callback emitter and callback auth config` | When `ENABLE_GO_CALLBACKS=true`, emit processing callbacks on terminal job states with retry, request ID propagation, and no effect on local job truth if callback fails. |
| `Cardio-Agent` | `Protect internal callback emitter and callback auth config` | `CA: Study-service execution API for PACS-AI integration` | `PACS-AI / Implement study-service processing callback endpoint in Go` | Add study-service-side config and auth wiring for callback emission so Go callback protection ships with the first callback-capable release. |
| `Cardio-Agent` | `Add callback dead-letter persistence for failed Go updates` | `CA: Study-service execution API for PACS-AI integration` | `Emit Go processing callbacks from study-service jobs` | Persist callback payload and error after final retry failure so missing status updates can be replayed later. |

## Sprint 4 — Reconciliation and Rollout Hardening

Goal: finish the recovery story, operator-facing auth decisions, and post-cutover cleanup once the core integration path is already tenant-correct and observable.

| Team | Title | Parent | Depends On | Description |
|---|---|---|---|---|
| `PACS-AI` | `Add reconciliation worker for stale processing candidates` | `PACS-232 CardioAgent ↔ StudyProcessingService Integration` | `Add candidate processing rollup from per-model job state`, `Cardio-Agent / Add GET /jobs/by-candidate/{candidate_id}` | Periodically query stale queued or running candidates and reconcile them against study-service state. |
| `Cardio-Agent` | `Replay dead-lettered processing callbacks` | `CA: Study-service execution API for PACS-AI integration` | `Add callback dead-letter persistence for failed Go updates`, `Protect internal callback emitter and callback auth config` | Add a small replay worker or operator-triggered command that drains `processing_callback_deadletter` and retries delivery when Go becomes reachable again. This complements the Go-side reconciliation worker instead of replacing it. |
| `Cardio-Agent` | `Finalize operator-facing auth for study-service endpoints` | `CA: Study-service execution API for PACS-AI integration` | `Protect ingest endpoint with internal auth` | Decide and implement the user-facing auth model for `/processing/*` routes: shared JWT/JWKS validation vs proxy-to-Go auth. |
| `Cardio-Agent` | `Enforce tenant scoping on study-service operator endpoints` | `CA: Study-service execution API for PACS-AI integration` | `Add Go correlation fields to pipeline_jobs`, `Finalize operator-facing auth for study-service endpoints` | Ensure `GET /jobs`, `GET /jobs/by-study/{uid}`, and `GET /jobs/by-candidate/{candidate_id}` are tenant-scoped. |
| `PACS-AI` | `Disable hybrid polling in integrated deployments after stabilization` | `PACS-232 CardioAgent ↔ StudyProcessingService Integration` | `Dispatch study-service ingest request after retrieval success`, `Add reconciliation worker for stale processing candidates` | After the explicit-dispatch path is stable, turn off `ENABLE_ORTHANC_POLLING` in integrated deployments so fallback does not remain the permanent default. Acceptance trigger: `polling_fallback_enqueue_total` stays at `0` for 7 consecutive days in integrated environments. |

## Cross-Team Dependency Summary

These are the most important blocker relationships to add in Linear.

| Blocking Issue | Blocked Issue |
|---|---|
| `PACS-AI / Define study-service ingest and callback contracts` | `Cardio-Agent / Implement POST /ingest/study as canonical execution endpoint` |
| `PACS-AI / Define requested-model configuration shape for inference ingestion jobs` | `PACS-AI / Add study-service dispatcher interface and request builder` |
| `PACS-AI / Define requested-model configuration shape for inference ingestion jobs` | `Cardio-Agent / Replace hardcoded modality-to-model selection with model container registry` |
| `Cardio-Agent / Introduce Alembic and baseline study-service schema` | `Cardio-Agent / Add Go correlation fields to pipeline_jobs` |
| `Cardio-Agent / Add Go correlation fields to pipeline_jobs` | `Cardio-Agent / Implement POST /ingest/study as canonical execution endpoint` |
| `Cardio-Agent / Add tenant-aware dedupe index for pipeline jobs` | `Cardio-Agent / Implement POST /ingest/study as canonical execution endpoint` |
| `Cardio-Agent / Implement polling fallback adoption logic for tenant-attributed dispatch` | `Cardio-Agent / Implement POST /ingest/study as canonical execution endpoint` |
| `Cardio-Agent / Refactor polling enqueue path into reusable ingest helper` | `Cardio-Agent / Implement POST /ingest/study as canonical execution endpoint` |
| `Cardio-Agent / Implement POST /ingest/study as canonical execution endpoint` | `PACS-AI / Dispatch study-service ingest request after retrieval success` |
| `PACS-AI / Implement study-service processing callback endpoint in Go` | `Cardio-Agent / Emit Go processing callbacks from study-service jobs` |
| `Cardio-Agent / Add GET /jobs/by-candidate/{candidate_id}` | `PACS-AI / Add reconciliation worker for stale processing candidates` |
| `Cardio-Agent / Add callback dead-letter persistence for failed Go updates` | `Cardio-Agent / Replay dead-lettered processing callbacks` |

## Recommended Sprint Summary

| Sprint | Main Outcome |
|---|---|
| `Sprint 1` | Environment wiring, schema tooling, observability, and contracts are locked before integration rows exist |
| `Sprint 2` | `POST /ingest/study` ships on top of tenant-aware schema, dedupe, auth, and shared enqueue logic foundations |
| `Sprint 3` | Go dispatches processing and receives callback state with tenant-aware storage and metrics from day one |
| `Sprint 4` | Reconciliation, dead-letter replay, operator-facing auth, tenant-scoped read APIs, and post-cutover cleanup land |

## Notes

- In steady-state integrated mode, use:
  - `ENABLE_ORTHANC_POLLING=false`
  - `ENABLE_GO_CALLBACKS=true`
- In standalone mode, use:
  - `ENABLE_ORTHANC_POLLING=true`
  - `ENABLE_GO_CALLBACKS=false`
- Hybrid mode is useful during rollout, but should not be the permanent default unless you intentionally want polling left on as a recovery path.
- Once the issues are created in Linear, rewrite the dependency tables to use actual issue IDs instead of long titles. The titles in this document are planning placeholders, not durable identifiers.
