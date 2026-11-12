# MHI GPU placement rollout

This host has an RTX A5000 (24564 MiB) and RTX 3090 (24576 MiB). Assign JEPA to the A5000 and other currently supported models to the 3090. With a 4096 MiB margin on each card and 150% peak scaling, JEPA reserves 16500 MiB. SCAD's 5025 MiB reservation cannot coexist with JEPA within the A5000's 20468 MiB capacity, so SCAD stays on the 3090.

## Preconditions

All callers must use the shared manager. Pause study-service and every worker, drain active requests and stop api-pacs before running a maintenance coordinator. There must never be two manager processes controlling the same GPU reservations. Snapshot deployment configuration and model identity; keep credentials and tenant-specific migration journals in protected operator storage.

Docker does not apply changed DeviceRequests through the container update endpoint. Existing all-device containers must be replaced. Reuse the existing managed deployment coordinator and the same immutable image/version. Do not delete model registrations or ingestion jobs, change image provenance, or bypass candidate validation.

Five currently supported images already have catalog records. JEPA, SCAD and SYNTAX require catalog registration of their existing immutable release artifacts after verifying image labels, repository digest and live provenance. This does not create or publish new model images. Four stopped legacy registrations lack the current provenance/runtime contract and remain stopped; successful startup reconciliation does not make those images supported for inference.

## Routing and history

The ingestion dispatcher resolves each job's current tenant-scoped model registration before container readiness and dispatch. Stored job IDs, schedules, versions and historical executions remain unchanged. A missing or cross-tenant registration fails before Docker access.

The prepared gateway resolves unambiguous case-insensitive Docker names to canonical IDs because URL parsing lowercases DNS hostnames. Exact names and canonical IDs retain their existing lookup path. Authorization still checks the resolved canonical ID against the tenant registry.

After managed replacement, retain the original container stopped for rollback, remove its network aliases, and give the active candidate the original stable Docker name/aliases. This preserves study-service's configured DNS endpoints. The replacement's placement-name label preserves GPU policy across later upgrades. Reconcile manager state after changing Docker names.

## Activation and verification

Enable UUID-specific budgets only after every running supported model exposes exactly one expected UUID. Keep the scalar bootstrap budget while migrating. Persist the GPU default and name assignments for future deployments. Preserve conservative retention, scaling, idle sweep and queue settings.

Check API startup reconciliation, per-device admission accounting, every worker's managed gateway flag, container DeviceRequests, and the UUID reported inside each container. Test model loading on both GPUs without submitting patient inference just to exercise placement, then unload through the manager before its next reconciliation. Actual cross-device concurrency and authenticated inference require representative workload validation.

Keep prior containers and protected snapshots until acceptance. On a partial migration, remain in scalar mode and keep callers paused while diagnosing; UUID-pinned containers can coexist with legacy all-device containers during bootstrap. Do not blindly restore container IDs after another deployment changes the registration.
