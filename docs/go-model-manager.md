# Go Model Manager (Phase 3)

This implements the scheduler foundation in initiative #339. A single manager is
constructed in the api-pacs dependency container and shared by inference command
services. It is disabled by default. **This PR does not route user predictions or
study-service requests through it and does not enable it on a deployed host.**

## Configuration

| Setting | Default | Meaning |
| --- | --- | --- |
| `MODEL_MANAGER_ENABLED` | `false` | Construct the shared manager; routing follows in Phase 4. |
| `GPU_MEMORY_BUDGET_MIB` | required when enabled | Upper bound for managed GPU use, before subtracting the safety margin. |
| `GPU_MEMORY_SAFETY_MARGIN_MIB` | `1024` | Headroom excluded from scheduling. |
| `MODEL_QUEUE_TIMEOUT_SECONDS` | `600` | Maximum time waiting for admission, including startup/readiness and eviction. |

For example, a budget of 81920 MiB with a 4096 MiB margin gives 77824 MiB
schedulable capacity. The operator must also exclude capacity assigned to other
GPU services. No instantaneous free-memory measurement overrides reservations.
Invalid enabled configuration fails startup. Disabling the flag keeps current
prediction paths unchanged. Load plus prediction and detached cleanup each have
a five-minute bound (`Config.OperationTimeout`).

## Prediction contract

`Manager.Predict(ctx, fullContainerID, preparedRequest)` accepts a registered,
canonical 64-character Docker container ID. The future gateway must resolve this
ID from an authorized model registration; it must not accept an arbitrary host.

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
HTTP mapping is 503 with a one-second Retry-After. Phase 4 must apply that mapping
at both gateways. Caller cancellation remains a context error. A model whose peak
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

## Rollout boundary

Before enabling managed production traffic, Phase 4 must route **both** user and
study-service inference through this instance, and route GPU preprocessors through
it or assign them a separate budget. Phase 5 must reconcile all registered runtime
state at startup, including resident models not yet requested, and add operational
metrics. First-touch reconciliation alone is not a host-wide startup inventory.
The existing Python inactivity mechanism remains until that managed path is ready.
Only one manager process may own this GPU; replicas and multi-GPU placement are
outside V1.

## Validation

The Go tests run with `-race` in the model-runtime workflow. They cover per-model
FIFO, concurrent distinct models, resident/peak accounting, safety margin, LRU and
idle timers, queue/operation timeouts, cancellation, partial load failure, panic,
cleanup quarantine and retry, CPU models, malformed lifecycle responses, stopped
container startup races, and unload acknowledgement before actual process exit.
The implementation is tested with fake Docker state and local HTTP lifecycle
servers; no production inference routing is changed by this PR.

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
