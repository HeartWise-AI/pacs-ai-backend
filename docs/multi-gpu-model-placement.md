# Per-GPU placement and idle offloading

The PACS-416 incident on sgbmavram01 exposed two independent issues: all
containers could see both GPUs and chose CUDA's default RTX 3090, while the
RTX A5000 stayed empty; deployed inference paths also had not enabled the
existing admission manager. Changing a global memory budget cannot solve this.

## Configuration

Configure `api-pacs/.env` with physical UUIDs from
`nvidia-smi --query-gpu=name,uuid,memory.total --format=csv`:

```dotenv
MODEL_MANAGER_ENABLED=true
GPU_MEMORY_BUDGETS_MIB={"GPU-999e1fca-539d-fb99-2531-ad97b91da026":24564,"GPU-0fe2f69a-78ce-4eb0-730e-2f5a15f4a1b7":24576}
GPU_MEMORY_SAFETY_MARGIN_MIB=4096
MODEL_GPU_DEFAULT_UUID=GPU-0fe2f69a-78ce-4eb0-730e-2f5a15f4a1b7
MODEL_GPU_ASSIGNMENTS={"deepcoro-jepa":"GPU-999e1fca-539d-fb99-2531-ad97b91da026"}
MODEL_RETAIN_PEAK_WHILE_LOADED=true
MODEL_GPU_MEMORY_RESERVATION_PERCENT=150
MODEL_IDLE_SWEEP_SECONDS=30
MODEL_QUEUE_TIMEOUT_SECONDS=1200
```

These UUIDs are host-specific examples from the incident. Copying them to
another server will not select its GPUs. Container names are exact and
case-sensitive; match the name supplied when registering the model. Unlisted
models use the explicit default UUID. New registrations and managed upgrades
receive one Docker GPU DeviceRequest with that UUID, rather than `--gpus all`.
New containers and upgrade candidates store the placement name in the Docker
label `ai.heartwise.pacs.gpu-placement-name`. Later upgrades reuse that key even
though activated candidates keep temporary container names. For an existing
unlabeled container, use its current inspected name as the initial assignment
key; its next managed upgrade will preserve that key.
Docker persists the assignment across restarts. With no placement configuration,
legacy container creation and upgrade behavior is retained.

The example reserves the A5000 for JEPA while leaving SCAD and automatic
angiography models on the 3090. With conservative 150% peaks and a 4096 MiB
safety margin, JEPA and SCAD cannot coexist within the A5000 budget. It is a starting allocation, not a benchmarked
throughput recommendation. Validate cold/warm peaks for the intended working set
and move additional models if necessary. Placement is explicit configuration;
this change does not dynamically migrate resident models between GPUs. See "Placement planning"
below for the optional planner.

`GPU_MEMORY_BUDGETS_MIB` enables separate reservation ledgers and replaces the
legacy scalar `GPU_MEMORY_BUDGET_MIB`. The safety margin is subtracted from
**each** GPU. Conservative peak scaling is applied before admission and loaded
GPU models retain that scaled peak until confirmed process exit. The example
uses 150% because deployed memory declarations underestimated observed CUDA
allocator residency; remeasure supported workloads before reducing it. Account for unmanaged applications by lowering that device's
budget. Unused capacity on one GPU cannot satisfy an allocation on another.
Pressure eviction only selects models assigned to the requesting GPU. A waiting
request on one device does not hold the global admission gate for the other.

In multi-GPU mode, a GPU model must expose exactly one configured physical UUID.
All-device requests and numeric ordinals are rejected for admission, rather than
guessing CUDA's ordering. Startup fails closed if resident allocations cannot
be attributed. If the models found loaded exceed a device budget, idle ones are
unloaded, least recently used first, until the ledger fits; a model that cannot
be unloaded is quarantined in `ERROR` and that device admits nothing until it
is released, while the manager stays ready for the other device. CPU models remain supported. Existing
legacy single-GPU configuration remains supported unchanged.

## Offloading policy

`MODEL_IDLE_SWEEP_SECONDS=30` enables a periodic idle scan; `0` (the default)
disables proactive scanning. Models become eligible after their metadata's
`idleTimeoutSeconds`. The scan skips active requests, queued requests, and models
blocked for deployment, and uses the same confirmed process-exit unload as
pressure eviction. An unload failure retains its memory reservation in ERROR.
Docker containers stay running; model weights are reloaded on demand.

Automatic ingestion membership is not a permanent residency pin. An idle
automated model can be unloaded and reloaded for its next study just like a manual
model. Do not stop its Docker container or delete its ingestion job to reclaim
idle weights. Active inference cannot be evicted. This policy trades cold-start
latency for lower idle GPU residency, without changing ingestion schedules.

## Measured reservations (stage 2)

Declared peaks scaled by `MODEL_GPU_MEMORY_RESERVATION_PERCENT` are far above
what most models use, so a GPU's budget is exhausted by three or four models
while its memory is mostly free. The manager therefore measures.

While the manager is enabled a sampler runs every
`MODEL_GPU_MEASURE_INTERVAL_SECONDS` (default `5`; `0` disables it). A
prediction also asks for a sample of its own GPU, `MODEL_MEASURED_MIN_BUSY_SECONDS`
after it starts and when it ends, at most once per second per GPU. For each GPU
the sampler executes the fixed read-only command
`timeout -s KILL 2 nvidia-smi --query-compute-apps=pid,gpu_uuid,used_memory`
inside one already-running model container pinned to that GPU (an image without
`timeout` runs the bare command). The driver reports host PIDs for every process
on the device, which are matched to model containers through Docker's process
list; memory of processes in no managed container is reported as foreign, and
memory whose owner could not be determined in that sample as unattributed. The
sampler never starts a container, never holds the admission lock during a
Docker call, gives every Docker call its own deadline, and slows to one sample
per 30 seconds for a GPU with nothing loaded. When a container is not running it
asks the next one; when nvidia-smi times out or fails it does not, and that GPU
is retried with a doubling delay up to five minutes.
`model_manager_gpu_sample_age_seconds` shows per GPU how old the last sample
is (`-1`: never sampled) and keeps growing if sampling stops.

Per container the manager keeps the last sample, the peak (highest sample while
loading, predicting or loaded), the resident value (highest sample while loaded
and idle), the number of observed predictions, and the number of overshoots. A
prediction is observed when a sample was taken at least
`MODEL_MEASURED_MIN_BUSY_SECONDS` (default 1) after inference began, or when it
was sampled twice; a sample while the model loads or at the instant inference
starts does not count, because it shows little more than the idle footprint. The record belongs to the container: a new image
starts again from its declaration, while a container recreated from the same
image (a move to the other GPU) inherits it. Records are stored in the Postgres
table `model_gpu_measurements` (migration `000010`), or in a JSON file when
`MODEL_GPU_MEASURE_STATE_PATH` is set. If storage is unavailable the sampler
keeps working in memory and retries.

`MODEL_RESERVATION_SOURCE` selects what is reserved:

- `declared` (default) is shadow mode. Reservations are exactly as before;
  measurements are only reported.
- `measured` reserves `ceil(peak x MODEL_MEASURED_SAFETY_PERCENT / 100) + 256` MiB
  (default 125%) once `MODEL_MEASURED_MIN_SAMPLES` (default 10) predictions were
  observed, never below the measured resident value scaled the same way and
  never above the declared reservation. Until then the declared reservation
  applies. With `MODEL_RETAIN_PEAK_WHILE_LOADED=false` the resident reservation
  is derived from the measured resident value in the same way, capped at the
  declared resident value and at the model's peak reservation.

A sample above a model's current reservation is an overshoot: it is counted,
logged, and becomes the stored peak at once, so the next reservation is larger.
With `measured` the reservation the model currently holds is raised as far as
the device's free budget allows. Periodic sampling can miss a short
spike; the safety percentage, the 256 MiB allowance and the per-GPU safety
margin are what absorb it. Run in shadow mode first and compare
`reservationMiB` with `measuredPeakMiB` before switching.

`GET /v1/inference/gpus` adds per model `declaredPeakMiB`,
`declaredReservationMiB`, `reservationMiB` (what would be reserved now),
`measuredPeakMiB`, `measuredResidentMiB`, `measuredLastMiB`, `measuredSamples`,
`measuredOvershoots`, `measuredAt` and `reservationSource`, and per device
`foreignMiB`, `managedMiB` and `processSampledAt`. `/debug/vars` adds
`model_manager_gpu_process_memory_mib` (managed and foreign per GPU),
`model_manager_reservation_mib` (total declared, effective, and what `measured`
would reserve today), `model_manager_measured_models`,
`model_manager_reservation_overshoots_total`, and the events `gpu_sample`,
`reservation_overshoot` and `placement_move`.

## Placement planning (stage 3)

A running container cannot change GPU, so placement is a plan plus an optional
applier that uses the existing "Move to GPU" redeployment.
`MODEL_GPU_PLACEMENT` selects the behaviour:

- `pinned` (default): no planning; the plan endpoint answers 409.
- `propose`: `GET /v1/inference/gpus/placement-plan` (owner/admin, same guards
  as the GPU status) returns the current and proposed GPU of every model, the
  moves, and for each GPU the reserved total and the number of models that fit
  before and after. It has no side effects.
- `auto`: in addition, every `MODEL_GPU_PLACEMENT_INTERVAL_SECONDS` (default
  300) the gateway applies at most one planned move for the workspace named by
  `MODEL_GPU_PLACEMENT_TENANT_ID`, recorded under `MODEL_GPU_PLACEMENT_ACTOR`.

The plan uses each model's current peak reservation (measured or declared) and
each GPU's schedulable budget. It maximises the number of models whose
reservations fit at the same time, then minimises the number of moves, then
keeps the tightest GPU as free as possible. A placement in which everything
already fits is never changed. Models that cannot be redeployed
(`placementSupported: false`, for example without an immutable catalog
deployment) stay where they are, a model is never sent to a GPU it would not fit
on alone, and `MODEL_GPU_ALLOWED_UUIDS` limits the destinations. Reservations of
other workspaces count as fixed load. Up to 16 movable models every assignment
is examined; above that a first-fit-decreasing pass is used.

The automatic loop moves a model only when the plan is complete and lets more
models fit, the time is inside `MODEL_GPU_PLACEMENT_WINDOW` (for example
`22:00-06:00`, server local time; empty allows any time), no request is waiting
for admission, no deployment is in progress, and the model is unloaded or has
been loaded and idle for `MODEL_GPU_PLACEMENT_MIN_IDLE_SECONDS` (default 900):
a move drains and redeploys the container and requests for it are refused
meanwhile. Of the planned moves it applies only one that, applied alone, does
not reduce how many models fit (`modelsFitAlone`), and it computes the plan
again before the next. A plan is `incomplete`, and never applied, when some
model's reservation is unknown: a container that was stopped when the gateway
started has not reported its resource contract, and a model with no known GPU
could load anywhere.

A model is not moved again for six intervals (at least 30 minutes), and nothing
else moves while the previous move is still deploying. A refused move, a
deployment that ends failed or rolled back, or one that has not replaced the
container after that period counts as a failure: the next attempt for that
model waits twice as long each time, and after three failures the model is
excluded until the gateway restarts or an administrator moves it. These
counters are in memory and listed under `auto.failures`. Every move is logged with its reason; every refusal is reported in
the plan response (`auto.blockedReason`, `auto.lastOutcome`, and
`blockedReason` on each move) instead of being forced. A move redeploys the
container, so the model's next prediction is a cold start.

## Controlled rollout

1. Merge and deploy the code with both managed routing flags disabled. Keep a
   snapshot of model registrations, image digests, environment, and ingestion
   configuration. Do not run the legacy remove/add deploy script as a GPU
   migration: deleting registrations loses processing history.
2. In a maintenance window, pause ingestion and manual inference and let active
   work finish. Configure placement, enable `MODEL_MANAGER_ENABLED=true` with
   the legacy scalar `GPU_MEMORY_BUDGET_MIB`, leave `GPU_MEMORY_BUDGETS_MIB`
   unset, and keep `MODEL_IDLE_SWEEP_SECONDS=0`. The managed upgrade workflow
   requires the manager, while per-GPU reconciliation rejects resident all-GPU
   containers. Use a conservative scalar capacity that fits the physical GPU
   holding legacy allocations; if reconciliation would exceed it, explicitly
   unload idle residents before startup instead of inflating the budget.
   Confirm reconciliation, then migrate containers through managed upgrades,
   preserving registration and ingestion history. Keep callers paused throughout
   this bootstrap. Placement applies to newly created upgrade candidates;
   setting environment variables alone does not repin an existing container.
   The current catalog deployment path accepts the same semantic version and
   immutable artifact for placement-only recreation; no fabricated version bump
   is needed. Existing releases must still pass OCI/provenance validation.
3. Inspect each container's DeviceRequests and confirm its single physical UUID.
   Test CUDA visibility in the container. Leave CPU-only models as CPU models.
4. Configure `GPU_MEMORY_BUDGETS_MIB`, restart api-pacs in per-GPU mode, and
   confirm startup reconciliation succeeds.
   Enable `PACS_AI_INFERENCE_GATEWAY_ENABLED=true` on study-service **and all
   Celery worker services**, then recreate those services to apply configuration.
   Every GPU caller must use the managed gateway or have an isolated budget;
   direct calls can still exceed reservations. Resume callers after checking
   managed routing. See [the manager guide](go-model-manager.md).
5. Enable the idle sweep. Run JEPA alongside representative automatic jobs;
   check cold starts, queued admission, peak reservations, idle unload and reload.
   Do not interrupt an active production inference to test eviction.

Diagnostics expose per-GPU `capacity`, `resident`, `active`, `reserved`, and
`available` gauges through authenticated `/debug/vars`, keyed by configured UUID.
`Manager.Snapshot()` includes GPU UUID per model and a separate device ledger.
These are reservations, not live NVML measurements. Compare with nvidia-smi to
detect unmanaged consumers or inaccurate declarations.

Roll back by disabling the study-service gateway first, then the manager and idle
sweep. UUID-pinned containers may remain pinned. Restore previous images through
the normal controlled deployment process if needed; restarting Docker alone
does not change a container's device assignment.

## Same-artifact maintenance CLI

`api-pacs/cmd/gpu-placement` reuses `CandidateValidator`, `UpgradeCoordinator`,
the admission manager and the normal registration compare-and-swap repository.
It does not instantiate the HTTP dependency container or a second background
manager. Build from `api-pacs` with:

```sh
go build -o gpu-placement ./cmd/gpu-placement
```

Run inside the deployment network with the API's existing credential-file mount,
environment and Docker socket. Set explicit UUID placement configuration. Audit
mode performs reads and writes a protected local plan and original Docker
snapshots only; the selected model containers must already be running and their
runtimes UNLOADED so that release identity validation remains read-only:

```sh
gpu-placement --journal /deploy/placement-audit
```

The default selection is the eight modern angiography model containers. Use
`--containers` to specify an exact comma-separated subset. Absent immutable
catalog records are planned from verified live model metadata and matching OCI
labels; they are published only during apply, never during audit. An existing
conflicting or revoked catalog artifact is rejected. No provenance is invented.

After draining and stopping the API and all study workers, use a new protected
journal directory and record the authorized operator identity:

```sh
gpu-placement --journal /deploy/placement-apply --apply --actor OPERATOR_ID
```

Apply requires scalar manager configuration during bootstrap. It recreates the
same immutable images, retains the old containers for rollback, unloads the new
model before name stabilization, disconnects and renames the stopped old
container, and gives the new container its original stable name and network
alias. It rebuilds the manager inventory after each rename. Registration IDs,
output modes, stored ingestion jobs and historical runs are retained. For jobs
with a registration `model_id`, Go dispatch now resolves its current container
inside the same tenant instead of restarting a stale historical container ID.
The stored job and prior execution records are never rewritten by resolution.

Snapshots and journals may contain deployment environment secrets and are created
with directory mode 0700 and file mode 0600. Use separate audit/apply directories;
existing snapshots are never overwritten. Review all stage and activation files
before re-enabling callers. Production per-GPU budgets and restarted callers
remain an explicit subsequent rollout step. Configure gateway container
references to resolve stable names correctly, including case-sensitive Docker
names.
