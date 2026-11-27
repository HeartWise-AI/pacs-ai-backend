# Active model deployments (PACS-453 / CA-276)

PACS-AI is the authority for the artifact currently registered for each tenant.
DockerHub tags and study-service code pins are not deployment evidence.

`GET /internal/v1/inference/deployments` requires the existing trusted
`STUDY_SERVICE_CALLBACK_TOKEN`. Its version-1 snapshot contains `revision` and
`models`; each entry carries `tenantId`, `registrationId`, `modelName`,
`modelVersion`, `catalogVersionId`, `imageDigest`, `containerId`, `localImageId`
and `state`. The internal service token can read all tenants so a shared
study-service process can converge; public model endpoints retain tenant checks.
Study-service filters its public discovery by authenticated tenant.

The registry reads current Firestore registrations, not deployment history, and
checks their container/image identities against Docker. Modern registrations use
the verified deployment snapshot. Legacy registrations use immutable image labels
only when the model title matches, otherwise the runtime's model-info contract.
Unverifiable registrations become `UNAVAILABLE`, not an invented/default version.
`READY` means the deployment identity is resolvable; it does not promise that a
GPU is admitted or that a cold container is already running. Legacy cold images
without trustworthy labels need their existing readiness path before runtime
metadata can be verified. Registry reads never start inference containers.

The revision is a content hash, not a monotonic version: rollback can restore a
previous hash. On startup and every 30 seconds, PACS-AI reconciles the registry and
POSTs `/ingest/deployments/refresh` using `STUDY_SERVICE_BASE_URL` and
`STUDY_SERVICE_INGEST_TOKEN`. Only a successful acknowledgement advances the last
notified revision. Study-service independently refreshes on startup/periodically,
so either startup order and lost notifications recover without manual intervention.

New dispatches include `deployment`. Study-service persists that identity with
the job and returns it in `X-PACS-Deployment` on predictions. Both buffered and
streaming handlers validate tenant, registration and exact active identity before
admission and route by immutable container ID. A queued request cannot silently
target a replacement image. Old clients without this header retain their existing
gateway behavior for rollout compatibility. Existing ingestion jobs deliberately
pinned to an old version still require an explicit update/new execution; this
does not rewrite ingestion configuration or historical results.

Deploy this backend before enabling the companion study-service change. API and
workers must share the new study-service image and its additive deployment-column
migration; drain or explicitly redispatch legacy unpinned queued jobs. Adapters,
modality safeguards, queues and concurrency remain study-service configuration,
not dynamically executable code from the registry. No model images or frontend
changes are needed. This feature does not itself authorize a production rollout.

## Synthetic validation

The Go suites cover legacy metadata, unavailable identities, stable revisions,
notification retry, authorization, cross-tenant rejection and deployment drift.
For the companion HTTP test, run from `api-pacs`:

```sh
DEPLOYMENT_REGISTRY_FIXTURE_ADDR=127.0.0.1:18763 \
  go test ./module/inference/interfaces/http/rest \
  -run '^TestDeploymentRegistryFixtureServer$' -v -count=1 -timeout=12m
```

Then run cardio-agent's `study-service/tests/integration/test_pacs_deployment_registry.py`
with `PACS_REGISTRY_FIXTURE_URL=http://127.0.0.1:18763`. This uses real Go handlers
and a fake Docker provider with synthetic versions, never installed inference
containers. The Python test restores version 9.0.0; shut down via
`POST /fixture/shutdown`. If containerizing the fixture, publish only to loopback
and use `--rm`. The fixture also expires automatically after ten minutes.
