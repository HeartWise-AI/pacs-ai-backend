# Cardio-Agent / Go Ingestion Integration Plan

## Purpose

The Go `api-pacs` ingestion service already:

- polls remote PACS on an operator-defined schedule
- tracks candidates through `discovered → growing → stable → retrieval_queued → retrieved/failed`
- issues C-MOVE and waits for the study to become local in pacs-ai-backend Orthanc

The `cardio-agent/study-service` was designed to:

- detect new local studies via Orthanc `/changes`
- group series by modality
- dedupe per `(study, modality, model)`
- run inference through per-model containers
- expose `pipeline_jobs` APIs for operators

The two services do not currently connect. This document defines the concrete integration work required to make `cardio-agent/study-service` the local-processing layer for studies retrieved by the Go ingestion service, aligned with `docs/ingestion-architecture-plan.md`.

Architecturally, the integrated flow is:

- Go is the orchestrator. It decides whether a retrieved study should be processed and which ingestion job rows apply, based on `inference_ingestion_jobs` configuration.
- Study-service is the execution layer. It accepts that order, validates that the requested model is locally runnable, enqueues work, executes against local Orthanc, stores detailed inference output in `pipeline_jobs.result_json`, and reports per-model status back to Go.
- Go owns workflow state; study-service owns execution details.
- `POST /ingest/study` becomes the canonical "run this model on this local Orthanc study" endpoint. In integrated mode the caller is Go; in standalone mode the caller can be a local helper script after uploading a study to Orthanc.

## Scope

In scope:

- wiring study-service to pacs-ai-backend Orthanc
- handoff mechanism from Go retrieval success to study-service processing
- back-propagation of processing status to the Go control plane
- multi-tenant correctness of processing jobs
- model registry alignment between Go and study-service

Out of scope (tracked separately):

- the PACS retrieval layer redesign described in `ingestion-architecture-plan.md` Phase 2
- cleanup / retention policy implementation (Phase 4)
- rewriting the Go ingestion runner

## Current State Snapshot

### Go side (`api-pacs`)

- `InferenceCommandService.ExecuteInferenceIngestionRunner` (line 185) is the discovery loop: PACS query → candidate bookkeeping → stability → decide which candidates move to `retrieval_queued`.
- `InferenceCommandService.ExecuteInferenceIngestionRetrievalWorker` (line 520) is the retrieval loop. It pulls queued candidates and calls `retrieveQueuedIngestionCandidate` (line 985), which issues C-MOVE and waits for local presence.
- `persistCandidateRetrievalResult` (line 1010) commits the outcome. The `candidateRetrievalOutcomeLocal, candidateRetrievalOutcomeSuccess` branch (line 1013) is the hook point for the processing dispatch.
- There is no notification sent out of this path today.
- The `inference_ingestion_candidates` table has no concept of "processed" — only retrieval state.

### Study-service side (`cardio-agent/study-service`)

- `workers/orthanc_poller.py` polls Orthanc `/changes` and returns new Orthanc study IDs.
- `workers/tasks.py::poll_orthanc` extracts per-modality metadata and enqueues `process_study` Celery tasks.
- `services/inference_runner.py` POSTs base64-encoded DICOM to a configured model container.
- `routes/jobs.py` exposes `GET /jobs`, `GET /jobs/{id}`, `GET /jobs/by-study/{uid}`, `POST /jobs/{id}/rerun`.
- Config points at `http://hospital-pacs:8042` and uses the `cardio-agent` `pacs-net` network.
- `pipeline_jobs` rows have no `tenant_id`, `ingestion_job_id`, or `retrieval_attempt_id`.
- Model set is hardcoded in `src/config/settings.py::INFERENCE_CONTAINERS`.

In the target design, the hardcoded study-service modality → model decision disappears from the happy path. Study-service still needs local knowledge of how to reach each model container, but the decision of which model to run for a retrieved study comes from Go ingestion job configuration. If multiple models should run, Go issues multiple dispatches, one per matching ingestion job row.

## Target Handoff Shape

```
          Go api-pacs                      Local Orthanc              study-service
┌─────────────────────────┐          ┌──────────────────┐       ┌────────────────────┐
│ ExecuteInference-       │          │  pacs-ai-backend │       │  FastAPI + Celery  │
│ IngestionRetrievalWorker│          │  Orthanc         │       │                    │
│                         │          │                  │       │                    │
│ retrieveQueuedIngestion-│──C-MOVE─▶│  study lands     │       │                    │
│ Candidate               │          │                  │       │                    │
│                         │          │                  │       │                    │
│ persistCandidateRetrie- │          │                  │       │                    │
│ valResult (Local|Success│─────── POST /ingest/study ──────────▶│  accept handoff    │
│ branch) + dispatch      │          │                  │       │  create job rows   │
│                         │          │                  │       │  enqueue Celery    │
│                         │          │                  │       │  run inference     │
│ UpsertProcessingJob     │◀── POST /internal/.../processing ────│  emit callback     │
│ + rollup trigger        │          │                  │       │                    │
└─────────────────────────┘          └──────────────────┘       └────────────────────┘
```

Core principles:

- Go remains the owner of "what should be processed" (ingestion job, tenant, selected model per dispatch, workflow status).
- Study-service remains the owner of "how processing runs" (container execution, deduping, queueing, detailed result JSON).
- Orthanc is the **data** boundary: studies cross from Go to study-service only via local Orthanc; nothing in study-service touches remote PACS.
- Direct HTTP is the **control** boundary: `POST /ingest/study` carries the execution order, `POST /internal/.../processing` (study-service → Go) reports completion. In integrated mode the caller is Go; in standalone mode it can be a local script or test harness. Polling remains available both as a safety net and as a standalone operating mode.
- Correlation IDs flow end-to-end. A request ID (`X-Request-ID` header) is minted by the caller, echoed by study-service on the callback, and logged at every hop so operators can answer "why did this study get processed" and "what happened to that candidate".

## Work Plan

The plan is grouped by where the code lives and ordered so each step produces a working increment.

### Phase A — Wiring only (end-to-end polling path works)

Goal: make study-service see studies that Go places in local Orthanc, using the existing polling loop. No schema or API changes yet.

#### A.1 Point study-service at pacs-ai-backend Orthanc

File: `cardio-agent/study-service/docker-compose.yaml`
- Change `ORTHANC_URL` on both `study-service` and `study-celery-worker` from `http://hospital-pacs:8042` to the pacs-ai-backend Orthanc container (`http://orthanc:8042` if on the same compose network).

File: `cardio-agent/study-service/src/config/settings.py`
- Keep env-driven default so local dev without the backend still works, but document that the production default is the pacs-ai-backend Orthanc.
- Add two explicit mode switches:
  - `ENABLE_ORTHANC_POLLING=true|false`
  - `ENABLE_GO_CALLBACKS=true|false`
- Recommended defaults:
  - standalone cardio-agent: `ENABLE_ORTHANC_POLLING=true`, `ENABLE_GO_CALLBACKS=false`
  - Go-integrated deployment: `ENABLE_ORTHANC_POLLING=false` for explicit-dispatch mode, `ENABLE_GO_CALLBACKS=true`
- Optional integrated fallback mode: `ENABLE_ORTHANC_POLLING=true`, `ENABLE_GO_CALLBACKS=true` if you intentionally want polling left on as a recovery path.
- Startup logs should print the resolved mode flags so operators can see whether the process is running in standalone polling mode, explicit-dispatch mode, or hybrid mode.

#### A.2 Join the same docker network

- Declare an `external` network (e.g. `pacs-ai-net`) in the pacs-ai-backend root compose.
- Reference the same external network from `cardio-agent/study-service/docker-compose.yaml`.
- Remove `pacs-net` references from the study-service compose unless cardio-agent standalone still needs it.

Acceptance:

- `docker exec study-service curl http://orthanc:8042/system` returns Orthanc's identity banner.

#### A.3 Fix the first-run sequence gap

File: `cardio-agent/study-service/src/workers/orthanc_poller.py`
- On first run, initializing `last_seq` to the current Orthanc seq silently drops any preexisting study.
- Expose `ORTHANC_POLL_START_MODE={latest, backlog, since_timestamp}` (default: `latest`).
  - `latest` — current behavior kept, but made explicit. Safe for fresh deploys against long-lived Orthanc installations.
  - `backlog` — process from seq 0. Opt-in only; a production Orthanc with months of history will flood inference containers if this is the default.
  - `since_timestamp` — process from a specific ISO-8601 timestamp. The operator-triggered backfill path.
- Log the chosen mode + resolved starting seq at boot.

Acceptance:

- Stop study-service.
- Have Go retrieve a study into Orthanc.
- Start study-service with `ORTHANC_POLL_START_MODE=latest`.
- The next Go retrieval appears in `pipeline_jobs` after one poll interval; the preexisting study does not (confirming `latest` is not silently dropping under the new flag).
- Set `ORTHANC_POLL_START_MODE=since_timestamp` with a past cutoff and confirm the preexisting study is enqueued exactly once.

#### A.4 Fill DICOM modality gaps

File: `cardio-agent/study-service/src/models/modality.py`
- `DICOM_MODALITY_MAP` currently only covers US, XA, ECG, CR, DX.
- Extend with the modalities Go may deliver (CT, MR, PT, NM, OT, SR) either by mapping to explicit enums or to `Modality.UNKNOWN` with clear logging.
- Emit a Prometheus counter `study_service_unknown_modality_total{modality_code}` on every UNKNOWN mapping (depends on A.5 — metrics infrastructure). Silent drops are the dominant failure mode for this class of system.
- Add an alert in the observability config (Grafana / Alertmanager, wherever pacs-ai-backend alerts live) that fires if `rate(study_service_unknown_modality_total[15m]) > 0` for more than one poll interval. Tune threshold after a week of real data.

Acceptance:

- A CT study retrieved by Go produces either a processing job or a clearly logged "unsupported modality" entry, not a silent drop.
- Scraping `/metrics` returns the counter with a known modality code after a manual test.

#### A.5 Add metrics infrastructure to study-service

Study-service currently exposes no `/metrics` endpoint. Subsequent phases require Prometheus counters (`study_service_unknown_modality_total` in A.4; `dispatch_attempts_total` and `polling_fallback_enqueue_total` etc. later), so the plumbing must exist before A.4 or any later metric lands.

Files: `cardio-agent/study-service/pyproject.toml`, `src/main.py` (or wherever the FastAPI app is constructed), new `src/observability/metrics.py`.

- Add `prometheus-client` to `pyproject.toml`.
- Expose `GET /metrics` using `prometheus_client.make_asgi_app()` mounted on the FastAPI app.
- Create the metrics module as the single place where counters / histograms / gauges are declared. Later phases import from here.
- The Celery worker process must also expose `/metrics` — or push to a pushgateway — since several counters (unknown modality, dispatch adoption) increment on the worker side, not the API side. Simplest: run a lightweight HTTP server in the Celery worker that exposes the shared registry.
- Document the scrape config in `nginx/` or wherever pacs-ai-backend's Prometheus setup already scrapes targets.

Acceptance:

- `curl http://study-service:PORT/metrics` returns Prometheus exposition format.
- `curl http://study-celery-worker:PORT/metrics` returns the same format from the worker process.
- A manual counter increment in code shows up in the scraped output.

### Phase B — Correlation and multi-tenancy (operator-visible integration)

Goal: processing jobs can be tied back to ingestion jobs, candidates, and tenants. No behavioral change yet — pure data model alignment.

#### B.1 Extend `pipeline_jobs` schema

File: `cardio-agent/study-service/src/database/models.py` (or the migration file)
- Add nullable columns:
  - `tenant_id` (uuid / string, indexed)
  - `ingestion_job_id` (uuid, nullable, indexed)
  - `candidate_id` (uuid, nullable, indexed)
  - `retrieval_attempt_id` (uuid, nullable — populated later, see `ingestion-architecture-plan.md`)
- Keep them nullable so existing jobs and standalone cardio-agent usage continue to work.

#### B.2 Accept correlation fields in study-service APIs

Files: `cardio-agent/study-service/src/routes/jobs.py`, `src/workers/tasks.py`, `src/database/study_db.py`
- Pass `tenant_id`, `ingestion_job_id`, `candidate_id` from task kwargs into `create_job`.
- Return them in the job response schemas so Go can display them.
- **Extend the dedupe key.** `_should_enqueue` currently keys on `(study_instance_uid, modality, model_name)`. Add `tenant_id` to the key so the same study UID ingested for two tenants produces two jobs. If `tenant_id` is null (cardio-agent standalone dev path or the polling fallback path in E.3), the key must still enforce uniqueness — Postgres default `NULL != NULL` semantics would let duplicates through.
- Add a unique index backing the dedupe key on `pipeline_jobs`. Two options depending on Postgres version:
  - **Postgres 15+:** `CREATE UNIQUE INDEX pipeline_jobs_dedupe_idx ON pipeline_jobs (tenant_id, study_instance_uid, modality, model_name) NULLS NOT DISTINCT WHERE status NOT IN ('failed', 'cancelled');`
  - **Postgres 14 or earlier:** use an expression index: `CREATE UNIQUE INDEX pipeline_jobs_dedupe_idx ON pipeline_jobs (COALESCE(tenant_id::text, '__standalone__'), study_instance_uid, modality, model_name) WHERE status NOT IN ('failed', 'cancelled');`
- The in-memory `_should_enqueue` check alone races under concurrent dispatch + poll. The DB-side unique index is the authoritative dedupe.

**Migration tooling.** Study-service currently uses `Base.metadata.create_all(self.engine)` in `src/database/study_db.py` and has no migration system. Before B.1 lands, introduce Alembic:

- Add `alembic` to `cardio-agent/study-service/pyproject.toml`.
- Run `alembic init migrations` and wire it to the existing SQLAlchemy metadata.
- Create a baseline revision that reflects the current schema (stamp, don't migrate) so existing deployments don't double-create tables.
- Replace the `create_all` call with `alembic upgrade head` at service startup (or run it explicitly from the deploy step, not in-process).

All B.1 and subsequent schema changes ship as Alembic revisions. This work is a prerequisite for B — call it **task B.0**.

#### B.3 Expose a query endpoint keyed by Go IDs

New endpoint: `GET /jobs/by-candidate/{candidate_id}` returning the list of processing jobs for that candidate (one per model).

This is what Go will poll or what a summary view will use.

Acceptance:

- Manual insert of a job row with a fake `candidate_id` returns correctly from the new endpoint.

### Phase C — Canonical execution request endpoint

Goal: make `POST /ingest/study` the canonical way to ask study-service to run a specific model against a study already present in local Orthanc. In integrated mode, Go calls it when retrieval succeeds. In standalone mode, a local helper script can call the same endpoint after uploading a study to Orthanc. Polling remains available as a passive fallback / standalone mode.

Mode assumptions:

- `ENABLE_ORTHANC_POLLING=false` means study-service processes studies only when `POST /ingest/study` is called.
- `ENABLE_ORTHANC_POLLING=true` means the poller may also discover new local Orthanc studies and enqueue fallback jobs.
- `ENABLE_GO_CALLBACKS=true` means study-service sends processing-state callbacks to Go for jobs that have Go correlation fields.
- `ENABLE_GO_CALLBACKS=false` means local processing still runs and stores results, but no callback is attempted.

#### C.1 Define the handoff contract

Endpoint on study-service: `POST /ingest/study`

The payload is an execution order, not a generic "new study exists" signal. In integrated mode, Go evaluates `inference_ingestion_jobs` and sends one dispatch per matching ingestion job row after retrieval succeeds. In standalone mode, a local script may construct the same payload directly after uploading a study to Orthanc. Study-service must execute only the model named in the payload, subject to local validation that the model is installed/configured.

Headers:

- `X-Request-ID: <uuid>` — minted by the caller, echoed by study-service in logs and on the callback. Required.
- `Authorization: Bearer <token>` — shared internal token (`STUDY_SERVICE_INGEST_TOKEN`) in integrated mode, distinct from the operator-facing auth in Phase F. In standalone local-dev mode this can be disabled by config, but production defaults should require it.

Request body:

```json
{
  "tenant_id": "uuid or null",
  "ingestion_job_id": "uuid or null",
  "candidate_id": "uuid or null",
  "study_instance_uid": "2.16...",
  "orthanc_study_id": "abc-123",
  "modality": "XA",
  "model_name": "CardioSyntax",
  "model_version": "1.0.0"
}
```

Notes:

- `tenant_id`, `ingestion_job_id`, and `candidate_id` are required for the Go-integrated path and nullable for standalone/manual dispatch.
- The standalone/manual path assumes the study has already been uploaded to the same local Orthanc instance study-service reads from.

Response:

- `202 Accepted` with `{ "job_id": "uuid", "already_present": false }`.
- `200 OK` with `{ "job_id": "uuid", "already_present": true }` when the dispatch hits the dedupe key (study-service returns the existing job_id). Not a conflict — retries are expected to land here.
- `404 Not Found` when `orthanc_study_id` is not present in local Orthanc (catches wrong-Orthanc misconfiguration loudly).
- `422 Unprocessable Entity` on schema validation failure.

Idempotency:

- Caller must be safe to retry. Study-service dedupes on the key defined in B.2 (`tenant_id, study_instance_uid, modality, model_name`).
- The same `X-Request-ID` replayed within 5 minutes returns the prior response verbatim (cached on study-service); this avoids race-on-retry where two in-flight dispatches both create rows.

#### C.2 Implement the endpoint

File: new `cardio-agent/study-service/src/routes/ingest.py`
- Validate payload, resolve `orthanc_study_id` against local Orthanc (fail if not present — this is how we catch wrong-Orthanc misconfiguration).
- Treat `model_name`, `model_version`, and `modality` as authoritative for this dispatch. Study-service must not substitute a different model based on local modality rules.
- Validate that the requested model has a local container mapping; reject the request with `422` if the model is unknown/unrunnable.
- Call the same deduped enqueue path `poll_orthanc` uses (refactor `_should_enqueue` + the create/enqueue block into a shared helper).
- Return the job ID.
- Keep this endpoint usable without Go-specific identifiers so local scripts/tests can exercise the same execution path in standalone mode.

#### C.3 Emit the handoff from Go

File: `api-pacs/module/inference/infrastructure/service/InferenceCommandService.go`
- Hook point: `persistCandidateRetrievalResult` (line 1010), inside the `case candidateRetrievalOutcomeLocal, candidateRetrievalOutcomeSuccess` branch (line 1013), after the candidate row commit succeeds. This is the only point where retrieval has definitively succeeded and the study is guaranteed present locally. The caller is `ExecuteInferenceIngestionRetrievalWorker` (line 520) via `retrieveQueuedIngestionCandidate` (line 985) — dispatching from the retrieval worker path, not the discovery runner `ExecuteInferenceIngestionRunner`.
- Introduce a new interface (e.g. `ProcessingDispatcherInterface`) with `DispatchStudy(ctx, DispatchStudyRequest) error`.
- Implementation lives in `api-pacs/module/inference/infrastructure/service/` as `StudyServiceDispatcher.go`, wrapping an HTTP client with the study-service base URL (env var `STUDY_SERVICE_BASE_URL`) and bearer token (`STUDY_SERVICE_INGEST_TOKEN`).

Execution rules — the dispatch must never block the ingestion runner:

- **Async.** The call runs in a detached goroutine spawned after `MarkCandidateRetrievedWithContext` commits. The main loop returns immediately.
- **Per-attempt timeout.** 2s HTTP timeout per attempt via `context.WithTimeout`.
- **Retry.** 3 attempts with exponential backoff (0s, 2s, 8s) on network errors and 5xx. No retry on 4xx (except 429, which uses the `Retry-After` header if present, capped at 30s).
- **Request ID.** Mint `X-Request-ID` as the candidate's UUID (or a fresh UUID if the candidate schema doesn't expose one) and log it on every attempt.
- **Concurrency cap.** A semaphore bounded at e.g. 16 in-flight dispatches prevents a study-service outage from fanning out into thousands of stuck goroutines.
- **On final failure:** log with `X-Request-ID` and candidate ID, persist the error on the candidate (new column `last_dispatch_error TEXT` and `last_dispatch_attempted_at TIMESTAMPTZ`), leave the polling fallback as recovery.
- **Metrics.** Counters for `dispatch_attempts_total{outcome}` (`success`, `already_present`, `transient_error`, `permanent_error`), histogram for dispatch latency.

#### C.4 Keep polling as the safety net and standalone mode

No code removal. `poll_orthanc` remains available behind `ENABLE_ORTHANC_POLLING`. If the Go dispatch is lost (network, study-service down, redeploy), the next poll can still enqueue the job when polling is enabled. Dedupe prevents double-processing. In standalone deployments, polling remains a first-class way to discover and process new studies without any Go dependency.

#### C.5 Standalone/manual dispatch helper

Out of scope for the first integration slice, but recommended: keep or extend the existing local helper script that uploads DICOM to Orthanc so it can also call `POST /ingest/study` with `study_instance_uid`, `orthanc_study_id`, `modality`, `model_name`, and `model_version`.

This gives cardio-agent a unified active execution path in standalone mode:

1. upload study to local Orthanc
2. call `POST /ingest/study`
3. study-service validates, enqueues, runs, and stores results

That avoids a three-way split between poll-only standalone behavior, Go-triggered integration behavior, and ad hoc manual test code.

Acceptance:

- Retrieve a study end-to-end, observe in study-service logs that the dispatch arrived before the next poll cycle.
- Kill study-service during retrieval, restart it, observe that the poll cycle catches up.

### Phase D — Back-propagation of processing state to Go

Goal: the Go `inference_ingestion_candidates` (or a sibling table) reflects whether processing completed, so operators see one coherent picture per study.

#### D.1 Decide where processing state lives in Go

Go gets a new table `inference_ingestion_processing_jobs`:

- rows keyed by `(candidate_id, model_name)` with columns:
  - `id uuid primary key`
  - `candidate_id uuid not null references inference_ingestion_candidates`
  - `tenant_id uuid not null`
  - `model_name text not null`
  - `model_version text`
  - `modality text`
  - `status text not null` — one of `queued`, `running`, `completed`, `failed`
  - `study_service_job_id uuid` — the `pipeline_jobs.id` on the other side, for cross-reference
  - `error_message text`
  - `started_at timestamptz`
  - `completed_at timestamptz`
  - `created_at timestamptz not null default now()`
  - `updated_at timestamptz not null`
- unique constraint on `(candidate_id, model_name)`.
- indexed on `tenant_id`, `candidate_id`, `status`.

On the candidate itself, add two rollup columns that are computed from this table rather than written directly:

- `processing_status` — `queued | running | completed | partial | failed`
- `processing_status_at timestamptz`

Rollup rule (computed in a single view or maintained by a trigger — prefer a trigger to avoid per-query cost):

- `completed` iff every expected row is `completed`
- `failed` iff every expected row is `failed`
- `partial` iff at least one `completed` and at least one `failed`, and no `queued`/`running`
- `running` iff any row is `running`
- `queued` otherwise

"Expected rows" = the rows created at dispatch time (one per matching ingestion job row / dispatch request). This makes `partial` unambiguous — something option 1 cannot express.

Rejected: putting only a `processing_status` enum on the candidate. It cannot represent `partial` without knowing the expected-model set, and retrofitting per-model granularity later means two migrations and a dual-write window.

#### D.2 Define the callback contract

Endpoint on Go: `POST /internal/inference/ingestion/candidates/{candidate_id}/processing`

Headers:

- `X-Request-ID: <uuid>` — the ID Go sent on the dispatch, echoed here so both sides' logs can be joined.
- `Authorization: Bearer <token>` — shared internal token (env `STUDY_SERVICE_CALLBACK_TOKEN`), not the user-facing auth layer.

Body:

```json
{
  "study_instance_uid": "2.16...",
  "model_name": "CardioSyntax",
  "model_version": "1.0.0",
  "modality": "angiogram",
  "status": "completed",
  "error_message": null,
  "study_service_job_id": "uuid",
  "started_at": "2026-04-21T14:01:55Z",
  "completed_at": "2026-04-21T14:02:03Z"
}
```

Response:

- `200 OK` on every successful write (including replays — see idempotency).
- `404 Not Found` if the `candidate_id` doesn't exist in Go. Study-service must NOT retry on 404.
- `401 Unauthorized` on bad token. Study-service must NOT retry.
- `5xx` / network errors trigger retry on the study-service side.

Idempotency:

- Go MUST upsert keyed by `(candidate_id, model_name)`. Same-state replays are no-ops. State transitions follow the rule below — late-arriving `running` after `completed` is dropped, not accepted.
- Allowed transitions: `queued → running → completed | failed`. Any out-of-order callback is logged at WARN and ignored.

Rollup rule: as defined in D.1 — the candidate-level `processing_status` is derived from the per-model rows by trigger, not written by the callback handler.

#### D.3 Implement the callback

Go side:

- New HTTP handler in `api-pacs/module/inference/interfaces/` calling a new repository method `UpsertProcessingJob(ctx, ProcessingJobUpdate) error`.
- Guard with token middleware validating `STUDY_SERVICE_CALLBACK_TOKEN`.
- The handler writes to `inference_ingestion_processing_jobs`; the candidate rollup is updated by the trigger defined in D.1.
- Emit a metric `processing_callback_total{status, outcome}` for observability.

Study-service side:

- In `workers/tasks.py`, emit the callback on each terminal job transition (`running` → `completed|failed`) when `ENABLE_GO_CALLBACKS=true`. Optionally emit on `queued → running` to give operators live visibility.
- Retry: 5 attempts, exponential backoff (0s, 2s, 8s, 32s, 120s), per-attempt timeout 5s. Network errors and 5xx retry; 4xx except 429 do not.
- Callback failure MUST NOT flip the `pipeline_jobs` status — the local job truth is independent of whether Go heard about it.
- If `ENABLE_GO_CALLBACKS=false`, skip callback emission entirely; standalone mode should not require Go connectivity.
- After final retry failure, write the callback payload + last error to a dead-letter table (`processing_callback_deadletter`) so a reconciliation worker can replay them later.
- Include `X-Request-ID` from the dispatch; if the local row has no `request_id` (poller-enqueued job), mint one and log it.

Reconciliation worker (Go side):

- Periodically (every 5 min) query candidates with `processing_status IN ('queued', 'running')` older than 15 min.
- For each, call study-service `GET /jobs/by-candidate/{id}` and reconcile the per-model rows.
- This closes the loop when both the primary callback and the dead-letter replay have failed.

Acceptance:

- Successful inference run updates the Go candidate row to `completed` within seconds.
- Forced inference failure updates the candidate row to `failed` with the model's error message.
- Simulate Go being down during the callback window; when Go returns, the reconciliation worker fills in the missing rows within one cycle.

### Phase E — Model registry alignment

Goal: Go and study-service agree on which models exist and apply to which modality.

#### E.1 Decide source of truth

Recommendation: Go remains authoritative (`inference_models` table plus `inference_ingestion_jobs` configuration) for the integrated path. Study-service removes the hardcoded modality-driven model selection from the push/manual dispatch path.

Explicitly:

- `inference_ingestion_jobs` remains one row = one model.
- The existing `model_id`, `model_name`, `model_version`, and `modalities` fields are the source of truth for what a given job row means.
- If multiple models should run for a retrieved study, Go sends multiple dispatches, one per matching ingestion job row.
- Study-service should validate "can I run model X" but should not decide "for XA I will run model Y" on the normal push/manual dispatch path.
- In standalone polling mode only, study-service may keep a conservative local fallback allowlist because there is no Go job configuration to consult.

#### E.2 Pass models through the handoff

- Phase C already includes the model identity in the handoff payload (`model_name`, `model_version`, `modality`). Study-service treats those fields as definitive for the dispatch.
- The `container_url` used to reach the model must still be resolvable. Options:
  1. Go also sends `container_url` in the payload — simplest, couples Go to deployment topology.
  2. Study-service keeps a `models` config mapping `model_name → container_url` but no longer decides *which* model to run — only how to reach it.

Recommendation: option 2. Go decides what runs; study-service configures how to reach containers. This keeps deployment-specific URLs out of the Go DB.

#### E.3 Remove hardcoded modality → models mapping

File: `cardio-agent/study-service/src/config/settings.py`
- Replace `INFERENCE_CONTAINERS` with a flat map `MODEL_CONTAINERS = { "CardioSyntax": "http://cardio-syntax:8000", ... }`.
- `services/model_registry.py` becomes: "given a model_name, return the container URL".
- On the push path, study-service executes exactly the model named by Go in `POST /ingest/study`. It does not apply a local modality → model rule there.

Polling fallback behavior:

- Polling fallback only runs when `ENABLE_ORTHANC_POLLING=true`.
- The poller keeps a minimal local allowlist: `POLLING_FALLBACK_MODELS = { "XA": ["CardioSyntax"], "US": ["EchoPrime"], ... }`. Conservative — only the known-good model per modality.
- The poller does NOT call back into Go to check "is this study one of ours". The safety net must not depend on the service it's backing up; if Go is degraded, dispatches fail AND a Go-dependent poller would fail too, leaving nothing working.
- Fallback jobs are inserted with `tenant_id = NULL`. The B.2 unique index (with `NULLS NOT DISTINCT` or the `COALESCE` expression) still enforces one NULL-tenant row per `(study_instance_uid, modality, model_name)`.
- **Adoption rule.** A later tenant-attributed dispatch will key on `(tenant_id != NULL, study_instance_uid, modality, model_name)` and would otherwise create a second row. On `POST /ingest/study`, study-service must:
  1. Check for an existing active/completed row matching `(tenant_id IS NULL, study_instance_uid, modality, model_name)`.
  2. If found, **adopt** it: update that row's `tenant_id`, `ingestion_job_id`, `candidate_id`, `request_id` to the values from the dispatch payload in a single UPDATE guarded by the row's current `tenant_id IS NULL`.
  3. Return that row's `job_id` with `already_present: true`.
  4. Only if no adoptable row exists, insert a new row.
- Adoption happens in the same transaction as the dedupe check so two concurrent dispatches race on the UPDATE, not on the INSERT.
- The counter `polling_fallback_enqueue_total` and a companion `polling_fallback_adopted_total` make this observable. Alert if `polling_fallback_enqueue_total` is non-zero for more than an hour in steady state — the dispatch path is supposed to cover the happy case.

Rejected: polling path looking up candidate in Go via a new internal endpoint. Creates a circular dependency where the fallback cannot fire when its primary is down — which is exactly when you need it.

### Phase F — Authentication and multi-tenant hardening

#### F.1 Authenticate operator-facing study-service endpoints

- Put `cardio-agent/study-service` behind the same nginx as pacs-ai-backend with a subpath (`/processing/...`).
- Either require the same JWT the Go API uses (shared secret / JWKS) or validate via a small middleware that proxies to the Go auth endpoint.
- `/internal/...` and `/ingest/...` use a separate shared internal token.

#### F.2 Enforce tenant scoping on list/detail endpoints

- Once `tenant_id` is populated (Phase B), every job query must filter on the caller's tenant.
- `by-study/{uid}` and `by-candidate/{id}` must 404 for studies/candidates in another tenant.

### Phase G — Cleanup (optional, tracked separately)

Out of scope for first release. See `ingestion-architecture-plan.md` Phase 4 for the retention design.

## Ownership Summary

| Concern | Owner after integration |
|---|---|
| Ingestion job config | Go |
| Selected model per dispatch | Go (`inference_ingestion_jobs`) |
| Candidate discovery & C-MOVE | Go |
| Local Orthanc | pacs-ai-backend |
| Processing dispatch trigger | `POST /ingest/study` caller (Go in integrated mode, local script in standalone/manual mode), plus study-service polling as passive fallback |
| Processing dedupe | study-service |
| Processing execution | study-service |
| Detailed processing result JSON | study-service DB (`pipeline_jobs`) |
| Processing status rollup per candidate | Go (via callback) |
| Model definition | Go |
| Container URL per model | study-service config |
| Operator UI | pacs-ai-backend frontend, reading both Go and study-service APIs |

## Rollout Order

1. Phase A — wiring. Unblocks the basic end-to-end flow.
2. Phase B — correlation columns. Prerequisite for the dispatch payload and the per-model job table.
3. Phase C — push handoff. Drops latency and makes Go the scheduler of processing. **Depends on B** — the dispatch payload writes into the columns B adds.
4. Phase D — callback. Gives operators a single "is this study done" view. **Depends on B**; parallelizable with C once B is merged.
5. Phase E — model registry alignment. Removes divergence risk. **Depends on C** (the dispatch is where Go passes the authoritative model identity).
6. Phase F — auth & tenant. Required before the integrated surface is user-facing. **Depends on B** for tenant columns; must ship before any UI consumes study-service endpoints.

Dependency graph:

```
A.1 ──▶ A.2 ──▶ A.3 ──▶ A.5 ──▶ A.4
                                 │
                                 ▼
                      B.0 (Alembic bootstrap)
                                 │
                                 ▼
                      B.1 ──▶ B.2 ──▶ B.3
                                         │
                                ┌────────┼────────┐
                                ▼        ▼        ▼
                                C ──▶ E  D        F
```

Key prerequisites:
- **A.5 before A.4** — the UNKNOWN modality counter has no exposition endpoint to land on otherwise.
- **B.0 before B.1** — study-service has no migration system today (`create_all` only); introducing Alembic must land before the first schema change.
- **B before C** — the dispatch payload writes into the columns B adds.
- **B before D** — the per-model job table and callback handler both reference tenant/correlation columns.

Corrected from an earlier claim that A–C could ship independently.

## Effort & Sequencing

Estimates assume one engineer working daily, pair-programming with an AI assistant for code drafting but still owning real-infrastructure testing, migrations, deploys, and review. Solo human estimates are roughly double.

| Phase | Elapsed | Notes |
|---|---|---|
| A.1–A.3 | 1–2 days | Docker-compose, network wiring, poller start-mode fix. |
| A.5 | 1 day | Metrics infrastructure (`/metrics` endpoints, `prometheus-client`, shared registry). Prerequisite for A.4. |
| A.4 | 0.5–1 day | Modality map extension + UNKNOWN counter. Small because A.5 did the plumbing. |
| B.0 | 1–2 days | Introduce Alembic + baseline revision + deploy wiring. Risk: must stamp existing deploys to avoid double-creating tables. |
| B.1–B.3 | 1–2 days | Schema change + API plumbing + adoption-aware dedupe + unique index. |
| C | 2–4 days | Canonical study-service `/ingest/study` endpoint + Go dispatcher (async, retry, semaphore) + adoption logic. |
| D | 2–4 days | New Go table + trigger-based rollup + callback handler + study-service emit + reconciliation worker. |
| E | 1–2 days | Config flattening and polling-fallback allowlist. Low risk. |
| F | 3–6 days | nginx subpath + JWT validation + tenant scoping on every query path. Slower because review must be paranoid. |

**Minimum viable integration (A → D shipped):** ~2–3 weeks elapsed at daily cadence, 4–5 weeks alongside other work.

**Full integration including F before any UI:** add 1 week.

Ratholes that can stretch estimates:

- Docker network mismatch between the two composes (Phase A): +2–3 days if the environments are less aligned than expected.
- DICOM edge cases in A.4 if real studies surface exotic encodings: uncapped, mitigated by the UNKNOWN counter catching them fast.
- No existing HTTP client pattern in `api-pacs/module/inference/infrastructure/service/` to copy for `StudyServiceDispatcher.go`: +1–2 days of design.
- Real SSO/JWKS integration in Phase F (vs. shared secret): +1 week.

## Risks and Mitigations

- **Dispatch delivered twice.** Study-service dedupe on `(tenant_id, study_instance_uid, modality, model_name)` + the unique index from B.2 handles it. The response distinguishes `already_present: true` so the caller can treat retries as success.
- **Dispatch lost.** Poller is the safety net; dedupe prevents duplicate jobs when the lost dispatch eventually arrives.
- **Callback lost.** Study-service dead-letter table retains the payload; reconciliation worker (D.3) queries `/jobs/by-candidate/{id}` for stale candidates and backfills.
- **Study arrives in Orthanc but not in Go's candidate table.** E.3 polling-fallback allowlist enqueues with `tenant_id = NULL` and the `polling_fallback_enqueue_total` counter alerts if non-zero in steady state. Operators see what's bypassing the happy path.
- **Wrong Orthanc pointed at.** C.2 requires study-service to resolve `orthanc_study_id` against local Orthanc and return 404 if missing; a misconfigured study-service fails loudly on the first dispatch instead of silently.
- **Network partition between Go and study-service.** Dispatch retries (3 attempts); polling fallback; callback retries (5 attempts) + dead-letter + reconciliation. No single-failure path corrupts state.
- **Same study ingested for two tenants.** Dedupe key includes `tenant_id` (B.2) so each tenant gets its own job. Tested explicitly in Phase B acceptance.
- **`partial` state ambiguity.** The per-model table (D.1) makes the expected-model set explicit, so `partial` has a precise definition. Not possible with a single status enum on the candidate.
- **Study-service slow/hung during dispatch.** Per-attempt 2s timeout + detached goroutine + semaphore-bounded concurrency (C.3) prevent the ingestion runner from being affected.
- **Replay ordering (late `running` after `completed`).** D.2 enforces state-machine transitions on upsert; out-of-order callbacks are logged and ignored, not applied.

## Open Questions

- Do we keep the cardio-agent standalone dev path (pointing at `hospital-pacs`) or consolidate everything onto pacs-ai-backend Orthanc? If the former, keep env-driven configuration and document both topologies.
- Does study-service keep its own Postgres or move `pipeline_jobs` into the pacs-ai-backend DB? The plan above assumes separate DBs. Revisit after Phase D ships and operational debugging patterns are known — the right answer depends on how often "why is this study stuck" requires crossing the boundary.
- Who owns the cleanup worker when it lands (Phase G)? Not blocking.
- **Token rotation.** `STUDY_SERVICE_INGEST_TOKEN` and `STUDY_SERVICE_CALLBACK_TOKEN` are introduced as shared secrets. How are they rotated without downtime? Minimum viable approach: support two accepted tokens simultaneously (`*_TOKEN` and `*_TOKEN_PREVIOUS`) on the receiving side, flipped via env var rollouts. Decide before Phase F ships since it's where the tokens go live.
- **Auth style in Phase F.** Shared secret vs. JWT (JWKS-validated) vs. proxied-to-Go-auth-endpoint. Shared secret for `/ingest/*` and `/internal/*` is non-negotiable (machine-to-machine), but the operator-facing endpoints need a decision. Default assumption: the same JWT the Go API accepts, validated via shared JWKS.
- **Reconciliation worker placement.** The D.3 reconciliation worker runs on the Go side. Open: is it a new Celery-equivalent in Go (background goroutine, cron) or a CronJob in the deployment? Defer until rollout.

## References

- `docs/ingestion-architecture-plan.md` — architectural target and lifecycle definition
- `docs/go-ingestion-stability-issue.md` — rationale for separating retrieval from processing
- `api-pacs/module/inference/infrastructure/service/InferenceCommandService.go` — current Go ingestion runner
- `cardio-agent/study-service/src/workers/tasks.py` — current study-service polling path
