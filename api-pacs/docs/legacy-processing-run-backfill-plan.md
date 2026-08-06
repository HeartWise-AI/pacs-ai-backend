# Legacy processing-run backfill plan

## Purpose

Issue #252 must make current pre-run processing state visible through the new
worklist without pretending to reconstruct unavailable history. The backfill
will create at most one `LEGACY_IMPORT` run for each eligible logical study and
attach its current legacy model executions.

This document fixes the mapping and safety rules before any write migration is
implemented. The repeatable preflight is
[`legacy-processing-run-backfill-audit.sql`](legacy-processing-run-backfill-audit.sql).

## Authoritative legacy sources

| Source | Usable information | Limit |
|---|---|---|
| `ingestion_processing_jobs` | Current model, version, modality, state, error, Python job ID, and available timestamps | Contains current state only; it does not contain prior attempts |
| `ingestion_candidates` | Tenant and Study Instance UID needed to form one logical study | The same study can have multiple candidates because models came from different ingestion jobs |
| `ingestion_processing_runs` | Detects already migrated or newly run-aware studies | Must never be overwritten by the legacy import |

Candidate `processing_status` is a compatibility rollup, not the source used to
construct executions. The execution rows are more precise and the existing run
aggregation function remains authoritative for phase, outcome, counts, and
attention.

## Eligibility and grouping

An execution is legacy-backfill eligible only when:

1. `ingestion_processing_jobs.processing_run_id IS NULL`;
2. its candidate exists;
3. execution and candidate tenant IDs match;
4. tenant ID, Study Instance UID, model name, and status are non-empty/valid.

Eligible executions are grouped by `(tenant_id, study_instance_uid)`, not by
candidate ID. Every group becomes one frozen model plan because the worklist is
study-centric and the historical candidate-per-model layout can span several
ingestion jobs.

The entire group must be skipped and reported for operator review when:

- any processing run already exists for the same tenant/study;
- the same model name occurs more than once across the group's candidates;
- tenant correlation is inconsistent;
- an execution contains an unsupported status.

Skipping an ambiguous group is safer than choosing an arbitrary execution or
creating a misleading run order. The preflight must return zero ambiguous and
conflicting groups before writes are enabled.

## Run mapping

| Run field | Backfill value |
|---|---|
| `id` | A newly generated real run ID; all linked executions receive this exact ID in the same transaction |
| `tenant_id` | Group tenant ID |
| `study_instance_uid` | Group Study Instance UID |
| `run_number` | `1`; groups with an existing run are not eligible |
| `run_trigger` | `LEGACY_IMPORT` |
| `phase` | Calculated from the linked executions by the existing aggregate rules |
| `outcome` | Calculated from the linked executions; remains `NULL` for active runs |
| `attention_required` | Calculated by existing rules, plus structural warnings discovered by validation |
| `attention_reasons` | Existing defined reason codes only; never inferred clinical explanations |
| `version` | `1`, representing the first persisted aggregate snapshot |
| `started_at` | Earliest non-null execution `started_at`, otherwise `NULL` |
| `completed_at` | Latest non-null terminal execution `completed_at` when the run is terminal, otherwise `NULL` |
| `created_at` | Earliest execution `created_at` so ordering reflects the available legacy evidence |
| `updated_at` | Latest execution `updated_at` |

The implementation must call the same pure aggregation logic used by callbacks
and reconciliation. It must not reproduce phase/outcome rules in a separate SQL
case expression.

## Execution mapping

Existing `ingestion_processing_jobs` rows are updated in place. Only
`processing_run_id` is assigned; every other recorded value is preserved.

In particular, the backfill must not synthesize:

- missing `study_service_job_id` values;
- missing start or completion timestamps;
- callback event IDs or sequences;
- skip reasons that were never recorded;
- model versions, modalities, errors, or inference results;
- historical runs preceding the current execution state.

Current execution statuses map through the existing aggregate unchanged:

| Execution state mix | Resulting run state |
|---|---|
| Any `running` | `PROCESSING`, no outcome |
| Otherwise any `pending` or `queued` | `QUEUED`, no outcome |
| All `completed` | `TERMINAL / SUCCESS` |
| Completed plus skipped, no failures | `TERMINAL / SUCCESS_WITH_SKIPS` |
| Completed plus failed | `TERMINAL / PARTIAL_SUCCESS` |
| All failed | `TERMINAL / FAILED` |
| All skipped | `TERMINAL / NO_RESULT` |
| All cancelled | `TERMINAL / CANCELLED` |

Other terminal combinations use the existing aggregate tests and implementation
as the single source of truth.

## Transaction and idempotency contract

Each logical study is processed in one database transaction:

1. acquire the same tenant/study advisory lock used by run creation;
2. lock and reload all orphan execution rows for the group;
3. re-check eligibility and absence of an existing run;
4. calculate the aggregate using those locked executions;
5. insert one `LEGACY_IMPORT` run;
6. link every locked execution to that run;
7. verify the number of linked rows equals the frozen expected count;
8. commit.

On rollback, neither the run nor any links survive. On rerun, already linked
executions are no longer eligible. Concurrent new run creation is serialized by
the advisory lock. These properties make the operation idempotent without
inventing deterministic identifiers.

The command must support a dry-run mode that reports aggregate counts and skip
reasons without writing. Backfill progress metrics must use bounded reason/state
labels and must not include tenant IDs, Study Instance UIDs, patient data, DICOM
metadata, or inference results.

The current read-only command is:

```bash
go run ./cmd/legacy-processing-run-backfill --dry-run
```

It uses the standard `POSTGRES_DB_*` environment variables and refuses to run
without `--dry-run`. No write mode exists until the transactional implementation
and its rollback tests are complete.

## Observed preflight on 2026-08-06

The local inference database contained:

- 1,179 orphan execution rows across 1,179 candidates;
- 669 logical tenant/study groups in one tenant;
- 510 studies whose executions span multiple candidate rows;
- zero duplicate study/model groups;
- zero execution/candidate tenant mismatches;
- zero orphan studies conflicting with existing processing runs;
- 320 queued, 703 completed, and 156 failed executions;
- 474 executions without start/completion timestamps;
- 154 executions without a Python study-service job ID;
- zero missing model versions and modalities.

The status groups were homogeneous: 419 completed studies, 160 queued studies,
and 90 failed studies. These observations validate the grouping strategy for
the current environment but are not implementation assumptions. Every rollout
must rerun the preflight and stop on ambiguity.

## Rollout gates for the implementation step

Before applying the future write migration:

- take a database backup;
- deploy code that continues reading nullable run IDs;
- run the preflight and retain its aggregate output;
- require zero tenant mismatches, duplicate study/model groups, and run conflicts;
- run dry-run and compare eligible execution/study counts with the preflight;
- stop ingestion dispatch during the write window, or prove advisory-lock
  coordination with the running service;
- verify post-write that every imported run's expected count equals its linked
  execution count and that no eligible orphan rows remain.

The later compatibility-removal step may reject new run-less callbacks and
run-less dispatch writes. It must happen only after Go and Python run-aware
deployments are verified and is not part of the backfill transaction itself.
