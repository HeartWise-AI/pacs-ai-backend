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
this change does not dynamically migrate resident models between GPUs.

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
be attributed or exceed a device budget. CPU models remain supported. Existing
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
