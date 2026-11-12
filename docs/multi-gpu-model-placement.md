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
GPU_MEMORY_SAFETY_MARGIN_MIB=2048
MODEL_GPU_DEFAULT_UUID=GPU-0fe2f69a-78ce-4eb0-730e-2f5a15f4a1b7
MODEL_GPU_ASSIGNMENTS={"deepcoro-jepa":"GPU-999e1fca-539d-fb99-2531-ad97b91da026","deepcoro-scad":"GPU-999e1fca-539d-fb99-2531-ad97b91da026"}
MODEL_IDLE_SWEEP_SECONDS=30
MODEL_QUEUE_TIMEOUT_SECONDS=600
```

These UUIDs are host-specific examples from the incident. Copying them to
another server will not select its GPUs. Container names are exact and
case-sensitive; match the name supplied when registering the model. Unlisted
models use the explicit default UUID. New registrations and managed upgrades
receive one Docker GPU DeviceRequest with that UUID, rather than `--gpus all`.
An upgrade looks up the active model's name, not its temporary candidate name.
Docker persists the assignment across restarts. With no placement configuration,
legacy container creation and upgrade behavior is retained.

The example reserves the A5000 for JEPA and SCAD while leaving automatic
angiography models on the 3090. It is a starting allocation, not a benchmarked
throughput recommendation. Validate cold/warm peaks for the intended working set
and move additional models if necessary. Placement is explicit configuration;
this change does not dynamically migrate resident models between GPUs.

`GPU_MEMORY_BUDGETS_MIB` enables separate reservation ledgers and replaces the
legacy scalar `GPU_MEMORY_BUDGET_MIB`. The safety margin is subtracted from
**each** GPU. Account for unmanaged applications by lowering that device's
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
2. Configure placement and migrate existing model containers through the managed
   deployment workflow in a maintenance window, preserving registration and
   ingestion history. Placement applies to newly created upgrade candidates;
   setting environment variables alone does not repin an existing container.
   If the managed upgrade requires a newer version, use a properly released
   image rather than changing image provenance or bypassing version checks.
3. Inspect each container's DeviceRequests and confirm its single physical UUID.
   Test CUDA visibility in the container. Leave CPU-only models as CPU models.
4. Enable `MODEL_MANAGER_ENABLED` and confirm startup reconciliation succeeds.
   Enable `PACS_AI_INFERENCE_GATEWAY_ENABLED=true` on study-service **and all
   Celery worker services**, then recreate those services to apply configuration.
   Every GPU caller must use the managed gateway or have an isolated budget;
   direct calls can still exceed reservations. See [the manager guide](go-model-manager.md).
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
