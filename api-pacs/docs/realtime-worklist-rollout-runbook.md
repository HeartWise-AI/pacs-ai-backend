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
