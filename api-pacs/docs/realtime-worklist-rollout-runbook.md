# Real-time worklist backend rollout runbook

## Safety boundary

This runbook covers the Go inference database and the Python study-service
database. The one-time write step is the command containing `--apply`; it must
not be run unattended. All counts retained in rollout evidence are aggregate
counts only. Do not copy patient, DICOM, inference-result, tenant, study,
candidate, execution, or Python job identifiers into deployment logs.

The rollout owner must record:

- Go and cardio-agent commit SHAs;
- database backup identifiers and restore test result;
- dry-run JSON, apply JSON, and verify JSON;
- deployment timestamps and compatibility-flag values;
- rollback decision owner and observation window.

## Required versions

Before the write window:

- Go migrations `000004` through `000006` are applied;
- study-service Alembic migrations `0008` and `0009` are applied;
- cardio-agent contains processing-run job lookup and ordered callbacks;
- Go contains processing-run planning, guarded dispatch, ordered callback
  application, reconciliation, REST worklist APIs, and SSE events;
- callback, ingest, and operator tokens match across services;
- `/debug/vars` is restricted to internal operators;
- study-service `/metrics` is restricted to internal operators.

## Phase 1: backup and restore proof

1. Take consistent backups of both PostgreSQL databases before changing flags
   or applying the backfill.
2. Restore those backups into isolated, non-production database instances.
3. Run schema checks and representative read-only queries against the restored
   copies.
4. Record backup identifiers, checksums, restore duration, and verification
   result. A backup that has not been restored successfully is not a rollout
   gate.

Do not use a production database name or broad filesystem path as a restore
target. The database operator owns the exact backup/restore commands because
credentials and storage differ by environment.

## Phase 2: compatible deployment

Deploy in this order:

1. Deploy the current study-service schema and code with its existing execution
   mode unchanged.
2. Deploy Go with `INFERENCE_REQUIRE_PROCESSING_RUN_ID=false`.
3. Confirm Go health, study-service health, callback authentication, operator
   job lookup, Redis, reconciliation, REST snapshot, and SSE heartbeat.
4. For integrated execution, switch study-service atomically to
   `ENABLE_ORTHANC_POLLING=false` and `ENABLE_GO_CALLBACKS=true`. These flags
   must never both be true or both be false.
5. Keep Go enforcement disabled throughout the compatibility and backfill
   window. Older queued callbacks may still be run-less.

Rollback in this phase is code/flag rollback only. Keep the nullable database
columns and additive migrations; do not run down migrations while either
version may still be active.

## Phase 3: read-only preflight

Run and retain:

```bash
go run ./cmd/legacy-processing-run-backfill --dry-run
```

Also run `docs/legacy-processing-run-backfill-audit.sql`. Stop if:

- any skipped study or skip reason is reported;
- SQL and Go eligible study/execution counts differ;
- tenant mismatches, duplicate study/model plans, or existing-run conflicts
  are non-zero;
- ingestion or callbacks are still creating run-less work;
- the fresh counts differ from the approved change record.

The expected clean baseline observed on 2026-08-06 was 669 studies and 1,179
executions. Those numbers are historical evidence, not constants: the fresh
dry run is authoritative.

## Phase 4: supervised write window

Pause new Go ingestion dispatch. Allow in-flight database transactions to
finish, take one final dry run, then execute exactly once with its fresh counts:

```bash
go run ./cmd/legacy-processing-run-backfill \
  --apply \
  --confirm=LEGACY_IMPORT \
  --expected-studies=<fresh-eligible-studies> \
  --expected-executions=<fresh-eligible-executions>
```

If the command stops, do not reuse the original remaining counts. Run a new
dry run, investigate the bounded failure outcome, and obtain operator approval
for the new remaining counts. Each completed study is already atomic and safe.

## Phase 5: point-in-time proof

Using the original total approved for the complete rollout, run:

```bash
go run ./cmd/legacy-processing-run-backfill \
  --verify \
  --expected-studies=<original-approved-studies> \
  --expected-executions=<original-approved-executions>
```

The report must contain `"passed": true`, zero issues, zero invalid runs, and
zero remaining orphan executions. The command reads runs, executions, and
orphans in one repeatable-read transaction.

Then verify:

- automatic two-model processing reaches a terminal aggregate;
- a completed+failed model mix becomes `PARTIAL_SUCCESS`;
- a no-usable-DICOM model becomes a structured skip;
- manual reprocessing creates run number N+1 and preserves prior history;
- reconciliation repairs a deliberately withheld callback in staging;
- tenant B cannot read tenant A run detail, history, snapshot, or SSE events;
- reconnecting SSE clients recover newer state from the REST snapshot.

## Phase 6: compatibility cutoff

Only after the verification proof passes, all study-service API/worker replicas
run the correlated callback version, and the observation window shows no
run-less traffic:

1. set `INFERENCE_REQUIRE_PROCESSING_RUN_ID=true` on every Go replica;
2. perform a rolling restart;
3. confirm run-less test dispatches/callbacks are rejected with no mutation;
4. confirm normal correlated processing still completes;
5. retain nullable columns for at least one full rollback window.

This flag applies to Go-orchestrated work. Standalone Orthanc ingestion in
study-service remains compatible with its own execution mode.

## Manual-reprocess contract rollout

The manual-reprocess contract is additive, but deployment order still matters:

1. Deploy the study-service schema and code that accept `dispatch_intent` and
   `processing_execution_id`. Confirm a manual request returns the same
   `processing_run_id` and `processing_execution_id` that it received.
2. Deploy Go. Confirm automatic ingestion still omits the two manual-only
   fields.
3. In staging, manually reprocess a study with an existing terminal result.
   Confirm new Python job IDs are created for the new run, their response
   correlation matches, and `rerun_of` identifies the prior job when present.
4. Replay one dispatch with the same processing execution ID. Confirm the
   response returns the same Python job with `already_present=true`.
5. Reuse that execution ID with different immutable correlation and confirm a
   `409` response with no new job.
6. Confirm a different processing execution creates a different Python job and
   leaves the prior result and run history unchanged.

Old Go remains compatible with the additive study-service response. New Go
fails closed against an old study-service response: it does not persist an
uncorrelated job ID, marks the execution as a dispatch failure, and refunds a
manual run when no downstream work was accepted. Roll back Go before rolling
back study-service.

## Recovery of pre-contract `STATE_CONFLICT` runs

This recovery applies only to manual runs created before the run-scoped
contract was deployed. Treat it as coordinated Go database, study-service
database, and quota maintenance. Never relink an old Python job to the newer Go
run, and never change or delete the prior terminal job or its result.

Keep tenant, user, run, execution, candidate, study, and Python job identifiers
inside the restricted operator session. Shared evidence must contain aggregate
counts and outcomes only.

An affected run is eligible only when all of these statements are true:

- its trigger is `MANUAL_REPROCESS`, its phase is not `TERMINAL`, and its
  structured attention includes `STATE_CONFLICT`;
- every persisted Python job reference resolves to a job owned by a different
  processing run;
- no Python job or in-flight task is owned by the affected run;
- none of the affected Go executions reached `running` or a terminal state;
- `started_at` is null and the requesting user is present, so no downstream
  work was accepted and quota refund is appropriate.

If any condition is false or cannot be proved, stop and escalate. Do not infer
ownership from study, modality, or model equality.

For an eligible run:

1. Pause new manual reprocessing and take restorable backups of both databases.
   Record the Redis backup or persistence checkpoint used for the quota audit.
2. Use a reviewed, tenant-and-run-scoped maintenance invocation. It must lock
   and recheck the run before changing only its pending or queued executions to
   `failed`, clearing the foreign `study_service_job_id`, and setting one
   captured UTC completion time plus the generic manual-dispatch correlation
   error. Do not copy identifiers into the error message.
3. Recalculate the run through `RecalculateStudyProcessingRun`, adding
   `DISPATCH_FAILED` and removing `STATE_CONFLICT`. This preserves domain
   aggregation, optimistic versioning, and quota finalization. Do not reproduce
   these side effects with ad hoc SQL.
4. Confirm the run is `TERMINAL` with outcome `FAILED`, no active run remains
   for the study, and all repaired executions have no Python job reference.
5. Confirm the idempotent quota refund removed the active reservation and
   restored usage when that reservation still existed. If the reservation had
   already expired, do not decrement Redis usage directly; retain the audit
   evidence and allow the fixed usage window to expire.
6. Resume manual reprocessing and verify a new run sends `manual_reprocess`
   with a fresh processing execution ID, receives a Python job owned by that
   exact run and execution, and leaves the prior terminal result unchanged.

The maintenance invocation must use the application domain service and a
narrow repository operation that can atomically clear the foreign job
references. It must be peer-reviewed for the exact environment. Do not run
interactive or unreviewed SQL, delete processing runs, edit quota keys directly,
reuse a candidate ID as the idempotency key, or expose identifiers in tickets
or deployment logs.

## Observability gates

Go `/debug/vars` must expose bounded metrics for:

- dispatch attempts and latency;
- callback status/outcome and event-delivery lag;
- committed run phase/outcome/attention and execution state;
- structured skip and attention reason codes;
- reconciliation checked/repaired/failed/unresolved counts;
- active/total SSE connections and notification publish/write failures.

Study-service `/metrics` must expose HTTP, pipeline transition, pipeline
duration, polling-mode, and model execution metrics. Alert on sustained callback
lag, callback errors, reconciliation failures, notification publish failures,
subscriber drops, and unexpected polling fallback enqueue events.

Metric labels must remain enums. Never add run IDs, tenant IDs, study UIDs,
model-generated error text, DICOM metadata, or inference results as labels.

## Rollback decision and procedure

Rollback is required for structural verification failure, cross-tenant data,
unrecoverable callback correlation failures, incorrect history, or sustained
processing failure after cutoff.

1. Set `INFERENCE_REQUIRE_PROCESSING_RUN_ID=false` and restart Go.
2. Pause dispatch and callbacks.
3. Retain failing JSON reports and bounded metrics.
4. Prefer application rollback while leaving additive schemas in place.
5. To revert only the `LEGACY_IMPORT` mapping, obtain fresh imported totals and
   run the guarded command under database-operator supervision:

   ```bash
   go run ./cmd/legacy-processing-run-backfill \
     --rollback \
     --confirm=ROLLBACK_LEGACY_IMPORT \
     --expected-studies=<fresh-imported-studies> \
     --expected-executions=<fresh-linked-executions>
   ```

   It processes one run per transaction, locks the study, verifies the exact
   execution count, sets `processing_run_id` to `NULL`, verifies zero remaining
   links, and only then deletes the `LEGACY_IMPORT` run. It preserves callback
   state recorded on execution rows. Re-run dry-run afterward; reverted
   executions should again appear as legacy candidates.
6. If broader database state must be reverted, restore both databases from the
   tested pre-write backups into an isolated validation target first.
7. Promote the restored databases only through the database operator's normal
   recovery procedure.
8. Re-run dry-run, service health, tenant-isolation, and legacy compatibility
   checks before resuming traffic.

Do not delete `LEGACY_IMPORT` runs directly: the execution foreign key uses
`ON DELETE CASCADE`, so an unplanned delete can remove processing executions.
Do not run schema down migrations as an incident response shortcut.

## Removal criteria

Nullable run-ID and run-less compatibility paths may be removed in a later
issue only when:

- the rollback window has expired;
- no run-less dispatch or callback has been observed for the agreed period;
- all active Python jobs are correlated or terminal;
- the backfill verification evidence is retained;
- backups have passed retention requirements;
- frontend REST/SSE rollout is stable.
