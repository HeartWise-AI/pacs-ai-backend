# Automatic inference recovery and staging release

## Observed incident

On 2026-11-13 the deployed API container was restarting during startup. Its fatal
error was model-manager inventory reconciliation of the running legacy
`heartwisehub/medgemma_1.5:1.0` container: `resources is required`. The live
model-info response declared version `1.5.0` without a resource contract. Its
Docker device request exposed all GPUs.

The registered-container catalog read completed before reconciliation failed.
This supports a startup availability failure; it does **not** establish a general
Firestore outage. The fatal startup policy predates
[PR #434](https://github.com/HeartWise-AI/pacs-ai-backend/pull/434).
The reported timing alone does not establish that PR as the cause.

The [worklist design](https://app.notion.com/p/34b0855e87b68029ac46ce60e601497d)
separates Go scheduling/retrieval, Python study-service dispatch and execution,
callbacks, and tenant-scoped worklist/SSE updates. Historical PACS-258/PACS-97
worker work and PACS-416 GPU work do not replace acceptance across this chain.

## Implementation

1. Keep the API control plane available after failed startup GPU reconciliation.
   Managed inference, drain and candidate preparation remain closed until the
   complete inventory is accounted for. GPU administration exposes
   `managerReady: false` during this state.
2. Retry failed startup reconciliation every 30 seconds, with each attempt bounded
   by the configured operation timeout and a fresh Firestore catalog read. Close
   each catalog client. Stop retrying after the first success, so an active ledger
   is never rebuilt periodically.
3. Continue scheduled discovery, stability updates and durable retrieval queueing.
   Defer the retrieval worker before it consumes queued candidates while admission
   is unavailable. Resume on the next normal tick after successful reconciliation.
   Do not turn those queued studies into permanent dispatch failures.
4. Preserve a registration's stored GPU UUID during upgrades under the supported
   scalar memory budget. Explicit GPU moves still require per-device budgets;
   resource validation and capacity limits apply in both cases.
5. Test draft PRs targeting `staging-release-*` on GitHub-hosted runners with a
   read-only token, no deployment secrets and no access to the protected production
   runners. Existing production workflows remain unchanged.

## MedGemma recovery prerequisite

Current source `model-examples/med_gemma_1.5` declares version `1.5.1`, resource
requirements and lifecycle routes. The published `1.5.1` tag was absent when
checked; it must not be treated as a deployable artifact. Source verification
passed static manifest/helper/OpenAPI checks, but skipped the legacy provenance
release contract. The official release command requires complete immutable
provenance and a compatible Dockerfile, so this source is not ready for official
publication through that command.

The existing [radiography benchmark](radiography-model-resource-benchmark-2026-10-02.md)
measured 9,711.8 MiB for one CR input on an A100. The declared 9,712 MiB is backed
by that limited measurement, not a validated worst case for every supported study.
The observed deployment uses 150% reservations and a 4,096 MiB safety margin.
MedGemma's resulting peak reservation is 14,568 MiB; full concurrent model mixes
must still fit the selected physical GPU's available admission budget.

Prepare a development-only candidate from the existing immutable published base
plus current source without changing weights or prediction semantics. Record the
base digest, source commit and resulting image ID. Validate metadata, lifecycle,
synthetic inference and the supported input envelope in isolation. This candidate
does not establish official provenance or authorize publication/deployment.

Before a controlled cutover, record the full old container ID/image digest,
tenant registration, image identity, model version, output mode, GPU request,
history and pending-work counts in restricted operator evidence. Select exactly
one configured physical UUID after evaluating **all** resident reservations.
Never delete a registration/history or invent a resource override to make startup
succeed. Replacing/stopping the legacy all-GPU consumer requires an approved
maintenance window; a second GPU-capable copy cannot be assumed safe beside it.

After the approved replacement, require successful full-inventory reconciliation
and `managerReady: true`. An API that serves worklists while admission remains
closed is partial recovery, not restored automatic inference.

## Paired staging branches and promotion

Create backend and frontend `staging-release-2026-11-13` branches from their
reviewed default-branch commits. Open candidate PRs against those branches and
keep them open while testing. Record both candidate SHAs, backend/frontend image
IDs or digests and the MedGemma artifact in the same acceptance record. Existing
uncommitted deployment worktrees are not release input.

The staging workflows run on draft PRs, synchronizations and retargeting. They
only test candidate commits. They do not publish images, merge, deploy, restart
services or replay studies. Because no staging environment exists today, a
staging branch alone does not isolate runtime traffic. Use an isolated stack
with separate ports/network/database/Redis/Firebase project and synthetic data,
or a separately approved controlled maintenance test.

After acceptance, merge the tested candidates into their staging branches and
open promotion PRs from those branches to backend `master` and frontend `main`.
Any new candidate SHA invalidates the prior acceptance. Keep production promotion
manual and retain rollback artifacts until the observation window finishes.

## Required acceptance evidence

| Gate | Evidence required before promotion |
| --- | --- |
| API availability | API remains up with a legacy/invalid resource contract; auth, Firestore-backed model list, worklist and callbacks respond; manager reports unready. |
| GPU safety | Predict, drain, candidate prepare and admin move cannot admit GPU work before reconciliation; no fabricated free capacity or all-GPU placement. |
| Startup recovery | Fix inventory in isolation; bounded retry succeeds, stops retrying and leaves one idle-eviction loop; manager reports ready. |
| Firestore | Tenant model/config list and controlled synthetic registration reads succeed; wrong tenant fails closed; no credential values in evidence. |
| Discovery | While admission is unready, scheduled C-FIND/stability updates continue and studies become durable retrieval-queued candidates without dispatch failure. |
| Retrieval | After recovery, the next tick processes queued synthetic studies once; C-MOVE timeout/error remains visible without blocking unrelated polling. |
| Dispatch | Go resolves the tenant's current registration and compatible model version, calls study-service `POST /ingest/study`, and receives correlated run/execution IDs. |
| Celery | Task is queued, picked up and completes against the selected current model container; duplicate dispatch does not create duplicate logical execution. |
| Results | Result artifacts and callbacks persist with the same run/execution IDs; no unrelated retained rollback container receives new predictions. |
| Worklist/SSE | Backend REST and live event stream agree through queued/running/completed or failed states; tenant isolation, refresh/reconnect and output display pass in the paired frontend. |
| Model artifact | Candidate metadata/version/resource contract match its recorded source/base; load, inference, unload and confirmed process restart release accounting safely; representative input envelope and mixed-model capacity pass. |
| Rollback | Restore the recorded compatible image/registration/paired frontend without deleting history; reconciliation and one synthetic end-to-end study pass again. |

Existing failed or already-dispatched clinical jobs require an explicit,
deduplicated recovery selection after infrastructure health is proven. Do not
bulk replay, reset terminal states or infer that Celery worker liveness means
the complete processing chain is healthy.

## Validation executed on the patch

The seven affected Go packages pass with the race detector: interfaces, model
manager, model upgrade, inference service/repository/REST and Docker SDK. New
regressions cover bounded retries stopping on recovery, canceled retries, durable
retrieval deferral with discovery still enabled, and stored scalar-budget UUID
preferences remaining distinct from administrative moves. Live end-to-end gates
above remain required before production promotion.
