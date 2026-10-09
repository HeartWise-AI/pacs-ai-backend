# Go Model Manager (Phases 3-5)

This implements the scheduler and inference gateways in initiative #339. A
single manager is constructed in the api-pacs dependency container and shared
by user and internal inference paths. It is disabled by default, and the
study-service gateway is independently opt-in. **Merging this code does not
enable managed inference on a deployed host.**

## Configuration

| Setting | Default | Meaning |
| --- | --- | --- |
| `MODEL_MANAGER_ENABLED` | `false` | Route api-pacs inference through the shared manager when enabled. |
| `GPU_MEMORY_BUDGET_MIB` | required when enabled | Upper bound for managed GPU use, before subtracting the safety margin. |
| `GPU_MEMORY_SAFETY_MARGIN_MIB` | `1024` | Headroom excluded from scheduling. |
| `MODEL_QUEUE_TIMEOUT_SECONDS` | `600` | Maximum time waiting for admission, including startup/readiness and eviction. |
| `MODEL_UPGRADE_ALLOWED_NAMESPACE` | `heartwisehub` | Only images from this registry namespace may be activated. |
| `MODEL_UPGRADE_MAX_DRAIN_SECONDS` | `900` | Upper bound accepted for an upgrade's drain timeout. |
| `MODEL_UPGRADE_OPERATION_TIMEOUT_SECONDS` | `1800` | Overall bound for one upgrade or automatic recovery operation. |
| `MODEL_UPGRADE_READINESS_TIMEOUT_SECONDS` | `120` | Candidate container metadata readiness window. |

For example, a budget of 81920 MiB with a 4096 MiB margin gives 77824 MiB
schedulable capacity. The operator must also exclude capacity assigned to other
GPU services. No instantaneous free-memory measurement overrides reservations.
Invalid enabled configuration fails startup. Disabling the flag keeps the
api-pacs direct provider path unchanged. Load plus prediction and detached
cleanup each have a five-minute bound (`Config.OperationTimeout`).

## Prediction contract

`Manager.Predict(ctx, fullContainerID, preparedRequest)` accepts a registered,
canonical 64-character Docker container ID. The gateways resolve this ID from
an authorized model registration; they do not accept an arbitrary host.

1. Queue in arrival order for that model. The oldest eligible request gets
   admission; a busy model does not block another model's queue.
2. Inspect/start Docker and wait for model metadata, reusing the ingestion
   readiness helper. Read and validate the runtime and resource contract.
3. Reserve the model's declared peak before loading or prediction. A resident
   model only needs its resident-to-peak delta. Admission decisions are serialized;
   admitted models can execute concurrently if their combined peaks fit.
4. When capacity is insufficient, unload eligible idle models in LRU order.
   Never evict a model while its load, inference, cleanup or eviction is active.
   Idle eligibility timers wake waiting requests without another incoming request.
5. Retain the resident reservation on success, release the active delta, update
   last use and wake waiters. A CPU-only model still gets one active request.

Queue exhaustion returns `AdmissionTimeout`, wrapping `ErrQueueTimeout`. Its
HTTP mapping is 503 with a one-second Retry-After. Both gateways apply that
mapping. Caller cancellation remains a context error. A model whose peak
exceeds capacity is rejected immediately once its metadata is known.

## Cancellation and unload

An HTTP cancellation does not imply Python stopped computing. Failed loads,
failed predictions, cancellation, timeout and panic therefore perform bounded
cleanup with a context detached from the request. Queue ownership is released
on all paths; panics propagate after cleanup.

The Python unload endpoint acknowledges before terminating its inference process.
The Docker adapter records the supervised `uvicorn main:app` host PID and waits
for a different worker PID plus an UNLOADED runtime before releasing memory.
Pending process identity is retained across failed unload attempts. The container
and Nginx stay running. Multiple API workers are rejected, as required by V1.

If cleanup cannot be confirmed, the model enters ERROR and its reservation stays
visible and unavailable. This deliberate quarantine prevents overbooking an
uncertain GPU process. A subsequent request retries cleanup before admission.
First-touch runtime failures are also accounted conservatively; an unexpectedly
resident model is cleaned up and rejected for that request instead of silently
being adopted without accounting. `Snapshot()` exposes reservations, states,
active ownership, queue depth and last use for tests and future observability.

## Phase 4 gateways

The existing authenticated user prediction route now calls the shared manager
when configured and preserves its quota reserve/refund behavior. The internal
`POST /internal/v1/inference/predict` route accepts prepared prediction payloads
from study-service, authenticates them with `STUDY_SERVICE_CALLBACK_TOKEN`,
resolves the Docker reference to a canonical ID, and verifies that ID against
the requesting tenant's model registry. Queue timeouts return `503` with
`Retry-After` on both HTTP paths.

Cardio-agent enables the gateway independently with
`PACS_AI_INFERENCE_GATEWAY_ENABLED=true`. Its model and GPU-preprocessor calls
then use this same endpoint; leaving the flag false preserves direct container
calls for standalone and rollback operation.

## Startup reconciliation

When the manager is enabled, api-pacs reads the host-wide set of registered
container IDs from Firestore and completes reconciliation before mounting the
production service. Predictions are rejected until this inventory succeeds.

- Stopped containers remain stopped and are recorded as `UNLOADED` with no GPU
  reservation.
- A confirmed idle `READY` runtime is reconstructed with its declared resident
  reservation and last-use timestamp.
- A running but busy, transitional, or otherwise ambiguous runtime is marked
  `ERROR` and conservatively reserves its declared peak. Its next request must
  confirm an unload before it can be admitted.
- A registration whose Docker container no longer exists is marked `ERROR`
  with no reservation, because it cannot consume GPU memory. Requests for that
  registration fail until the container or registry is repaired, without
  preventing unrelated models from being managed.
- A running container whose metadata cannot be trusted, or a reconstructed
  reservation set that exceeds schedulable capacity, fails startup closed.
- Duplicate registrations sharing one canonical container ID are counted once.

Reconciliation never cold-loads a model or starts a stopped container. Only one
manager process may own this GPU; replicas and multi-GPU placement remain
outside V1.

## Operational metrics

Aggregate Model Manager metrics are exposed through the existing authenticated
`/debug/vars` surface. They use bounded labels and never include tenant,
patient, study, request, container, or arbitrary model identifiers.

| Metric | Meaning |
| --- | --- |
| `model_manager_ready` | `1` only after successful startup reconciliation. |
| `model_manager_models` | Current model count by bounded runtime state. |
| `model_manager_loaded_models` | Count of runtimes known or conservatively assumed loaded. |
| `model_manager_gpu_memory_mib` | Capacity, resident, active, reserved, and available reservations. |
| `model_manager_queue_depth` | Total requests currently waiting for admission. |
| `model_manager_reconciliations_total` | Startup/retry reconciliation outcomes. |
| `model_manager_events_total` | Eviction, recovery, and admission-failure outcomes. |
| `model_manager_duration_seconds_count` | Queue, cold-start, warm/cold inference, eviction, execution, and reconciliation observations. |
| `model_manager_duration_milliseconds_sum` | Duration sum for the same bounded operation/outcome labels. |
| `model_manager_duration_seconds_bucket` | Bounded duration histogram buckets. |

Transactional upgrades add bounded-label counters on the same `/debug/vars`
surface: `model_upgrade_attempts_total`, `model_upgrade_transitions_total`,
`model_upgrade_failures_total`, `model_upgrade_duration_seconds_count`,
`model_upgrade_duration_milliseconds_sum`, and
`model_upgrade_drain_duration_seconds_count` plus
`model_upgrade_drain_duration_milliseconds_sum`. These metrics contain only
state, stage, and outcome labels; model, tenant, container, and user identifiers
remain in access-controlled audit records rather than metric labels.

The memory gauges are declared reservations, not instantaneous NVML readings.
`resident + active = reserved`, and `available = capacity - reserved`.

## Rollout boundary

Merging Phase 5 still does not enable managed inference. Deploy api-pacs and the
study-service workers with both routing flags disabled, verify health and model
metadata, then enable `MODEL_MANAGER_ENABLED` first. Startup must report a
successful inventory and the debug gauges must match the declared working set.
Only then enable `PACS_AI_INFERENCE_GATEWAY_ENABLED` for study-service and run
representative XA, US, and CT cold/warm/eviction tests. Roll back by disabling
the study-service gateway first and the manager second, then restoring the
pre-rollout images if necessary.

## Transactional model upgrades

Owner and administrator users can replace a registered model without editing
the registration or Docker host manually. The feature is available only while
the Model Manager is enabled:

- `POST /v1/inference/model/{modelID}/upgrade` accepts an explicit SemVer image
  tag or immutable digest, an optional expected `sha256` digest, and an optional
  drain timeout. It returns `202` with a durable upgrade ID.
- `GET /v1/inference/model/{modelID}/upgrade/{upgradeID}` returns the current
  state and the previous/candidate deployment identities.
- `POST /v1/inference/model/{modelID}/upgrade/{upgradeID}/cancel` requests
  cancellation. Cancellation may complete through rollback if activation has
  already started. Once verified activation enters its final commit phase,
  cancellation returns a conflict instead of racing the success transaction;
  completed attempts also return a conflict.

Only one upgrade may hold a model registration lock at a time. The service pulls
the candidate without registry credentials in the request, resolves its registry
digest, verifies `/model-info` and all provenance labels, and checks that its
model ID matches the active container and its version increases. It then blocks
new admissions, drains and unloads the active model, loads the candidate under
normal GPU accounting, atomically switches the Firestore registration, and runs
a lifecycle smoke test before reopening admission. Candidate creation uses the
immutable local image ID returned by inspection, so a concurrent update to the
requested tag cannot change the validated artifact. The activation transaction
rechecks the registration's current output mode against the candidate's
advertised modes before switching containers. PostgreSQL ingestion jobs
that target the previous container are moved to the candidate container and
model version before the old container can be removed; rollback moves those
targets back without recreating jobs or their processing history. A durable
PostgreSQL container redirect and transaction-scoped advisory lock serialize
job creation/import with this retargeting boundary, so a concurrent insert
cannot retain the container that is about to be removed. Inserts also lock the
resolved target, and each retarget flattens earlier aliases to the new target,
covering concurrent rollback and later upgrades. A dispatch that already
captured a job re-resolves this redirect and holds the same target locks through
the study-service handoff, so retargeting and old-container removal wait for the
in-flight dispatch to finish.

Inference requests that arrive while this gate is closed receive retryable
`503 INFERENCE_ADMISSION_BLOCKED` responses with `Retry-After: 1`; they are not
routed to either the draining or staged container.

Every attempt is retained in `inference_model_upgrades` with actor, timestamps,
state, digest, previous deployment, candidate deployment, and bounded error
information. Failures before activation leave the previous registration active;
failures after activation automatically restore it. A failed rollback enters
`degraded`, keeps the registration lock, and requires operator repair. On API
startup, non-terminal attempts are reconciled against the registered container
and safely rolled back or marked degraded before the HTTP server opens
admission. A degraded attempt that still owns the registration lock is also
rediscovered at startup, and its registered deployment is kept blocked and
unloaded until operator repair. An unexpected registered container is blocked
before the attempt is retained as degraded. Model deletion first acquires an
atomic Firestore claim; it returns a conflict while the upgrade lock is present,
and new upgrades return a conflict while deletion owns the claim. The claim is
deterministic and retained across a Docker failure so retrying the deletion can
resume without exposing a partially removed deployment to an upgrade.
A drain timeout cancels the admission block while allowing the active inference
lease to finish normally. Container cleanup identities are also
persisted, so a restart can remove an orphaned candidate or finish removing a
previous container without reversing an already successful activation. Registry
tokens and model environment variables are never copied into upgrade history or
logs. If the result of the activation transaction cannot be read safely, both
containers are retained under the degraded lock. Success finalization, rollback
registration, and rollback finalization are retried and read back in-process for
the configured operation timeout. If a success commit still cannot be
determined, neither deployment is removed or rolled back; the durable record is
left for safe restart recovery. If Docker loses or obscures a candidate-create
response, the service retries discovery by deterministic container name and
retains that name as pending cleanup when Docker remains unavailable.

Firestore requires no destructive schema migration for this rollout. The new
registration fields are optional, so legacy records continue to decode; their
current `container_id` and `docker_image` become the rollback snapshot on the
first upgrade, and a successful activation backfills the complete immutable
deployment identity.

## Validation

The Go tests run with `-race` in the model-runtime workflow. They cover per-model
FIFO, concurrent distinct models, resident/peak accounting, safety margin, LRU and
idle timers, queue/operation timeouts, cancellation, partial load failure, panic,
cleanup quarantine and retry, CPU models, malformed lifecycle responses, stopped
container startup races, and unload acknowledgement before actual process exit.
The implementation is tested with fake Docker state and local HTTP lifecycle
servers. Production routing remains unchanged until its flags are enabled.

## Live GPU acceptance (2026-10-03)

Validated the Go manager on an A100 80 GB host against temporary CathEF,
EchoPrime, and TotalSegmentator containers using existing local DICOM fixtures.
The test used the actual model memory declarations, a 5154 MiB schedulable budget,
and a one-second idle eligibility timeout in the temporary containers only.

All acceptance checks passed:

- Start a stopped container and complete a cold prediction.
- Queue real CathEF predictions with maximum active concurrency of one, and cancel
  a queued request without sending it to the model.
- Complete angiography, echo and CT predictions through the manager.
- Evict idle models to admit another peak reservation; confirm LRU order across
  two resident models and confirm that Docker containers stay running.
- Cancel an EchoPrime request after its HTTP body is sent. Retain its full 5154 MiB
  reservation while remote work is uncertain, then confirm process cleanup and
  successfully run the next prediction.
- Unload all test models and remove all three temporary containers.

The run exposed an opaque cancellation error from the legacy prediction client;
the manager now preserves the operation context cause alongside provider errors,
with a dedicated regression test. Host GPU use returned from a 4413 MiB observed
peak to its 4 MiB baseline. Production container IDs and start times were unchanged.
This is isolated acceptance evidence; it does not activate production gateways or
replace the host-wide startup reconciliation still required for rollout.
