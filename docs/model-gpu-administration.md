# GPU status and placement in workspace administration

Owner/admin users can inspect physical GPU memory and change the GPU assigned to
one registered inference model. These controls use the shared admission manager;
they do not change image versions, delete registrations, or edit historical runs.

## Status endpoint

`GET /v1/inference/gpus` uses the existing session, policy and owner/admin guards.
Model entries and telemetry probe containers are restricted to the authenticated
tenant. The response uses the standard API envelope and contains:

- `sampledAt`, `managerEnabled`, `managerReady`;
- `devices`: configured UUID, nullable name and physical memory/utilization,
  `telemetryStatus`, nullable `telemetrySampledAt`, and separate scheduler
  `managerCapacityMiB`, `managerReservedMiB`, `managerAvailableMiB`;
- `models`: registration `modelId`, current `containerId`, nullable `gpuUuid` and
  `desiredGpuUuid`, `placementSupported`, and a bounded unsupported reason.

Physical counters come from a fixed `nvidia-smi` command executed inside an
already-running, UUID-pinned registered model container. This read never creates
or starts a helper container and never loads a model. It accepts no shell command
or user-supplied executable arguments. Probe reads have a two-second timeout,
16 KiB output limits, and a five-second cache including failures. Unknown driver
errors are not returned to the browser.

The NVML device index inside a pinned container is local to its visible devices:
both physical cards can report index zero. Therefore `index` is deliberately
null. Use the physical UUID and name to identify a card.

Scheduler reservations and physical memory are different measurements. Never
substitute a reservation value for a failed physical sample. A device without an
eligible running probe, or with failed telemetry, returns `unavailable` with null
physical counters. An empty tenant model registry is a successful empty list;
configured cards remain visible with unavailable physical telemetry. Device
counters describe the shared physical hardware without exposing other tenants'
model registrations, processes or payloads.

Poll every five seconds only while the administration panel is visible. Cancel
polling on unmount, logout or authorization loss, and show unavailable/stale
telemetry explicitly.

## Moving a registered model

```http
POST /v1/inference/model/{modelId}/gpu-placement
Content-Type: application/json

{"gpuUuid":"GPU-999e1fca-539d-fb99-2531-ad97b91da026","expectedContainerId":"CURRENT_CANONICAL_CONTAINER_ID"}
```

The tenant and actor come from the authenticated session. The body cannot select
a different tenant. `expectedContainerId` is required: a stale model view returns
409 instead of changing a replacement deployment. The asynchronous coordinator
checks the expected source again before creating a candidate.

Placement requires a running model with explicit GPU placement, an immutable
active catalog deployment, a ready manager with per-device budgets, and a target
UUID whose capacity fits the model's scaled peak reservation. CPU-only models,
unconfigured UUIDs, unsupported legacy registrations, and the current GPU are
rejected. An existing deployment operation keeps its normal conflict lock.

The server selects the currently deployed catalog artifact and returns the
existing deployment-operation response with HTTP 202. Use the existing operation
status/events endpoints for progress. Changing GPUs recreates the inference
container after draining active work; the control must explain this before
submission. Do not label the GPU as changed until the operation completes.

The target UUID is persisted in the operation. It overrides the registration's
stored preference, which overrides environment placement defaults. The candidate
must expose that exact physical UUID before activation. The normal registration
compare-and-swap stores `preferred_gpu_uuid` and `deployment.gpu_uuid`; rollback
restores the prior placement preference. Future version deployments retain that
preference without mutating the process-wide SDK assignment map.

Managed candidates may keep temporary Docker names. Prepared inference resolves
stable placement names against exactly one current registration in the requesting
tenant, then reauthorizes its canonical container ID. Legacy Docker names and
case-folded URL hostnames therefore continue following the current registration;
IDs and URLs cannot invoke this alias fallback. Ambiguous matches fail closed.

## Verification

Focused Go tests cover physical telemetry parsing and unavailable probes, tenant
isolation, cache behavior, empty registries, stale source rejection, scaled target
capacity, immutable artifact reuse, persistent preferences, actual candidate UUID
verification, stable gateway resolution, bounded requests and safe errors.

```sh
go test -race ./infrastructures/providers/sdk/docker \
  ./module/inference/infrastructure/modelmanager \
  ./module/inference/infrastructure/modelupgrade \
  ./module/inference/infrastructure/repository \
  ./module/inference/infrastructure/service \
  ./module/inference/interfaces/http/rest ./interfaces
```

Deployment checks must also verify actual CUDA visibility and GPU memory on the
host. A successful build or HTTP status alone does not verify an authenticated
admin interaction or a completed live placement operation.
