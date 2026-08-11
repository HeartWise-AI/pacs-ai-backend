# PACS-AI Backend — Deployment Guide

> **Audience:** hospital IT / security reviewers (sections 1–4, 7, 13) and operations engineers performing the install and Day-2 ops (sections 5–6, 9–12).
>
> **Status:** filled to first draft. A small number of items are still flagged **TODO** — these are deployment-time placeholders (customer registry FQDN, internal mailbox confirmation) or future-work pointers (compliance attestations as they're produced). Resolve those during customer-specific install planning.

---

## 1. Overview & scope

### 1.1 What this system does

PACS-AI is a containerized DICOM ingestion and inference platform deployed on-premises at the customer site. It connects to the hospital's existing PACS, automatically retrieves studies of configured modalities (echo, XA, CT), runs them through registered AI inference models, and exposes structured results — along with the original imaging — via a multi-tenant REST API and an embedded Orthanc viewer. Authentication is delegated to a customer-owned Firebase project; all DICOM data lives in a 24-hour rolling cache with no long-term imaging storage in PACS-AI itself.

### 1.2 Architecture
See [architecture diagrams](./architecture.md) (Mermaid + Graphviz). The system has five logical planes:

1. **Edge** — nginx (TLS, reverse proxy)
2. **Control plane** — `api-pacs` (Go REST API + 3 background workers)
3. **DICOM plane** — Orthanc DICOM store
4. **Execution plane** — `cardio-agent/study-service` (FastAPI + Celery + model containers)
5. **Data stores** — PostgreSQL (×2), Redis, Elasticsearch

### 1.3 Deployment model covered by this guide
- Single-host Docker Compose deployment on a Linux VM provided by the customer.
- Optional GPU host(s) for model inference.

**Out of scope:** multi-host Kubernetes deployments are not covered by this guide. Air-gapped installs are partially covered as a Compose override in §15.3D.

### 1.4 Versions
- **Application version:** pinned to a specific git commit SHA — record the full 40-char SHA in the customer's install record (e.g. `d8079b4f3e1a…`). Each install ships from a tagged GitHub Release; the release page lists the exact SHA.
- Compatible Docker Engine: ≥ 24.0 (required for Compose `include:` support).
- Compatible Docker Compose: ≥ v2.20.
- Pinned base images (from compose files): Postgres 17.2 (Orthanc index), Postgres 16 (cardio-agent), Orthanc `orthancteam/orthanc:24.12.0-full`.

---

## 2. Prerequisites (customer-supplied)

### 2.1 Host(s)

Three sizing tiers are supported. Pick the tier that matches the customer's expected concurrent inference load and study throughput.

| Component | Minimum | Recommended | Production |
|---|---|---|---|
| OS | RHEL 9 / Ubuntu 22.04 | RHEL 9 / Ubuntu 22.04 | RHEL 9 / Ubuntu 22.04 |
| GPU | NVIDIA RTX 3090 (24 GB) | NVIDIA RTX 4090 (24 GB) | NVIDIA A6000 (48 GB) or multi-GPU |
| GPU VRAM | 16 GB | 24 GB | 48 GB+ |
| System RAM | 64 GB | 128 GB | 256 GB |
| CPU | 8 cores | 16 cores | 32+ cores |
| Storage (system) | 256 GB SSD | 500 GB NVMe | 1 TB+ NVMe RAID |
| Storage (DICOM) | size separately — see §2.2 | size separately — see §2.2 | size separately — see §2.2 |
| Network | 1 Gbps | 1 Gbps | 10 Gbps |
| CUDA | 12.1+ | 12.1+ | 12.1+ |
| GPU driver | matching CUDA 12.1 (≥ 525.x) | matching CUDA 12.1 (≥ 525.x) | matching CUDA 12.1 (≥ 525.x) |

> The GPU tier applies only to hosts running model containers. If model inference is deployed on a separate host, the api-pacs / Orthanc / data-store host can drop to the **Minimum** CPU/RAM column without a GPU.

> Storage tiers above cover OS + container layers + Postgres + Elasticsearch + Redis. **DICOM storage is sized separately** from study volume and retention — see §2.2.

### 2.2 DICOM storage sizing

**Orthanc is a rolling 24-hour cache, not an archive.** Studies are pulled from the remote PACS, processed by the inference pipeline, and purged after 24 h. Storage only needs to cover the working set in flight at any one time — not a multi-day retention window.

Approximate per-modality study size (industry rules of thumb for cardiology workflows; **verify against a sample of the customer's data** before sizing — protocols vary widely):

| Modality | Typical range | Planning value | Notes |
|---|---|---|---|
| Echo (US, TTE) | 100–500 MB | **300 MB** | Cine-loop heavy; stress echo or pediatric can hit 1 GB |
| XA (coronary angiography) | 200–800 MB | **500 MB** | Multiple cine runs at 15–30 fps; ad-hoc runs increase size |
| CT (cardiac / CTA) | 300 MB–1.5 GB | **800 MB** | Thin-slice CCTA can reach 2 GB; chest-only CT closer to 200 MB |

Use the **planning value** column as a starting point; collect a 2-week sample on first install and recalibrate.

Plan disk = `avg_study_size × studies_per_day × 1.3`
(1.3 ≈ headroom for in-flight studies overlapping the cleanup boundary + Orthanc index overhead.)

> Cache TTL is controlled by `ORTHANC_LOCAL_CACHE_EXPIRATION_IN_HOURS` in api-pacs `.env` (default `24`). If the customer requires a longer working window (e.g. for retries after model failure), increase this value and re-do the disk math accordingly.

### 2.3 Network
- Static IP and resolvable hostname for the API endpoint.
- (Optional) separate hostname for Orthanc if exposed.
- Internal DNS or `/etc/hosts` entries for service-to-service when not all on one host.

### 2.4 External egress required
The deployment needs outbound HTTPS access to:

| Destination | Purpose | Port | Required? |
|---|---|---|---|
| `*.googleapis.com`, `securetoken.google.com` | Firebase auth | 443 | Yes |
| `hub.docker.com`, `registry-1.docker.io`, `*.cloudflare.docker.com` | Docker Hub — pull PACS-AI images from `heartwisehub/*` (https://hub.docker.com/u/heartwisehub) | 443 | Yes |
| `huggingface.co`, `cdn-lfs.huggingface.co` | Model weights download (first run) | 443 | Yes |
| `api.mailgun.net` | Outbound transactional email | 443 | Yes (used by api-pacs) |
| `*.api.mailchimp.com` | Marketing list sync | 443 | Optional |
| `*.docusign.net`, `*.docusign.com` | DocuSign integration | 443 | Optional |
| `challenges.cloudflare.com` | Cloudflare Turnstile (bot challenge) | 443 | Optional |

### 2.5 Inbound from remote PACS
Customer must whitelist the api-pacs host in their source PACS.

| Direction | Protocol | Port | Purpose |
|---|---|---|---|
| api-pacs → remote PACS | DICOM (DIMSE) | _customer-defined_ | C-FIND, C-MOVE — port is set on the customer's source PACS; ask their PACS admin during the §7.1 kickoff call. Common values seen in the field: 104, 11112, or a hospital-assigned high port. |
| remote PACS → Orthanc | DICOM (DIMSE) | 4242 | C-STORE (study transfer) |

---

## 3. Network & ports

| Service | Container port | Host port | Exposure | Consumer |
|---|---|---|---|---|
| nginx | 80 | 80 | Public | Browsers, redirect to 443 |
| nginx | 443 | 443 | Public | Browsers, API clients |
| api-pacs | 8000 | — (proxied) | Internal | nginx |
| Orthanc REST | 8042 | — | Internal | api-pacs, study-service |
| Orthanc DIMSE | 4242 | 4242 | Customer LAN | Remote PACS (C-STORE) |
| study-service | 8600 | — | Internal | api-pacs |
| PostgreSQL (control) | 5432 | 5433 | Loopback only | api-pacs |
| PostgreSQL (cardio) | 5432 | 5434 | Loopback only | study-service, Celery |
| Redis | 6379 | — | Internal | study-service, Celery, api-pacs |
| Elasticsearch | 9200 | — | Internal | api-pacs |
| Ollama (optional) | 11434 | — | Internal | orchestrator |
| Orchestrator (optional) | 8585 | — | Internal | api-pacs |

> Verify with `ss -tlnp` after install — nothing in the "Internal" rows should be reachable from outside the host.

---

## 4. TLS & DNS

### 4.1 Certificates
- **Source:** customer-provided cert/key, ACME (Let's Encrypt), or internal CA.
- **Location:** mount cert + key into the nginx container — see [nginx/](../nginx/).
- **Rotation:** replace the cert/key on the mounted volume, then restart the container:
  ```bash
  docker compose restart nginx
  ```
  Expect a few seconds of dropped connections during the restart. Schedule rotations during a maintenance window or coordinate with the customer's change-management process.

### 4.2 DNS records to create
- `api.<customer-domain>` → A record → host IP.
- (Optional) `orthanc.<customer-domain>` if Orthanc is exposed externally.

---

## 5. Identity & secrets

### 5.1 Firebase

**Each customer provisions their own Firebase project and credentials.** No project IDs or service-account keys are shipped with this guide — every install is self-tenanted.

Env vars consumed by api-pacs:
- `FIREBASE_PROJECT_ID` — the customer's Firebase project ID (e.g. `acme-pacs-prod`).
- `FIREBASE_CONFIG_FILE_PATH` — in-container path to the mounted service-account JSON.
- `FIREBASE_SUPERUSER_KEY` — privileged API key used by api-pacs for admin operations.

#### Customer setup procedure

The customer's IT / security owner performs these steps once before install:

1. **Create a Firebase project** at https://console.firebase.google.com (or use an existing one dedicated to this deployment).
2. **Enable Identity Platform / Firebase Authentication** with the sign-in methods the customer requires (email/password, Google, SAML, OIDC, etc.).
3. **(Optional) Enable multi-tenancy** under *Authentication → Settings → User actions* if the customer needs multiple isolated user pools within the same project.
4. **Generate a service account key**:
   - *Project Settings → Service Accounts → Generate new private key*.
   - Download the JSON file. **Treat as a secret** — store it in the customer's secrets vault.
5. **Generate a Web API key** (for `FIREBASE_SUPERUSER_KEY`):
   - *Project Settings → General → Web API Key* (or create a restricted API key under *Google Cloud Console → APIs & Services → Credentials*).
   - Restrict it to the API host's IP / referrer where possible.
6. **Configure authorized origins / redirect URIs** to include the customer's API hostname (and any frontend origins).

#### Wiring it into the install

```bash
# 1. Stage the service-account JSON on the host (outside the repo, in a secrets dir).
sudo mkdir -p /etc/pacs-ai/secrets
sudo cp <downloaded>.json /etc/pacs-ai/secrets/firebase-sa.json
sudo chmod 600 /etc/pacs-ai/secrets/firebase-sa.json

# 2. Mount it into the api-pacs container — add to docker-compose.override.yml:
#    services:
#      api-pacs:
#        volumes:
#          - /etc/pacs-ai/secrets/firebase-sa.json:/run/secrets/firebase-sa.json:ro

# 3. Set env vars in the root .env:
#    FIREBASE_PROJECT_ID=acme-pacs-prod
#    FIREBASE_CONFIG_FILE_PATH=/run/secrets/firebase-sa.json
#    FIREBASE_SUPERUSER_KEY=<web-api-key>
```

> Never commit `firebase-sa.json` to the repo. The path `api-pacs/configs/firebase/` is for *dev-only* local staging — production installs should mount from a host secrets directory or a secrets manager.

### 5.2 Outbound mail (Mailgun)
- Env vars: `MAILGUN_API_KEY`, `MAILGUN_DOMAIN`, `MAILGUN_SENDER_EMAIL`.
- Optional Mailchimp list sync: `MAILCHIMP_API_KEY`, `MAILCHIMP_BASE_URL`, `MAILCHIMP_LIST_ID`.

### 5.3 Auth tokens between services
study-service ↔ api-pacs callback authentication uses three shared secrets — generate each with `openssl rand -hex 32`:
- `STUDY_SERVICE_INGEST_TOKEN` — api-pacs → study-service `POST /ingest/study`.
- `STUDY_SERVICE_OPERATOR_TOKEN` — operator/control routes (`/jobs`, `/settings`, `/metrics`).
- `STUDY_SERVICE_CALLBACK_TOKEN` — study-service → api-pacs status callbacks.

The same value must appear in both api-pacs `.env` and study-service `.env` for each token.

### 5.4 Third-party integrations (optional)
- **DocuSign:** `DOCUSIGN_ACCOUNT_BASE_URI`, `DOCUSIGN_ACCOUNT_ID`, `DOCUSIGN_AUTH_SERVER`, `DOCUSIGN_INTEGRATION_KEY`, `DOCUSIGN_PRIVATE_KEY`, `DOCUSIGN_USER_ID`.
- **Cloudflare Turnstile:** `CLOUDFLARE_SECRET_KEY`, `CLOUDFLARE_TURNSTILE_BASE_URL`.
- **OpenAPI docs gate:** `OPENAPI_DOCS_PASSWORD` (basic-auth on the `/docs` route).

### 5.5 Database credentials
- Two Postgres instances — control plane (`5433`) and cardio-agent (`5434`).
- api-pacs uses `POSTGRES_DB_HOST`, `POSTGRES_DB_PORT`, `POSTGRES_DB_DATABASE`, `POSTGRES_DB_USERNAME`, `POSTGRES_DB_PASSWORD`.
- cardio-agent uses `DATABASE_URL` (full Postgres URI).
- Defaults in repo (`cardio:cardio`) are **for dev only** — replace before any non-dev install.

### 5.6 Redis
- api-pacs: `REDIS_HOST`, `REDIS_PORT`, `REDIS_PASSWORD`, `REDIS_IAM_DB`.
- cardio-agent: `REDIS_URL` (full URI; password embedded). Integrated mode shares the root Redis on DB 2.

### 5.7 Secret storage
| Secret | Where it lives | Rotation procedure |
|---|---|---|
| Firebase service account JSON | mounted file at `FIREBASE_CONFIG_FILE_PATH` | Replace file, restart api-pacs |
| Postgres passwords | `.env` | Update `.env`, restart Postgres + dependents (api-pacs, study-service) |
| Mailgun API key | `.env` (`MAILGUN_API_KEY`) | Update `.env`, restart api-pacs |
| Study-service tokens | `.env` on both sides | Rotate all 3 tokens together; restart api-pacs and study-service |
| TLS cert / key | nginx volume | Replace files on the host volume, then `docker compose restart nginx` (brief downtime — schedule a maintenance window) |
| `FIREBASE_SUPERUSER_KEY` | `.env` | Regenerate Web API key in Firebase / GCP Console; update `.env`; restart api-pacs |

---

## 6. Installation procedure

### 6.1 Get the code / images
```bash
git clone <repo-url> pacs-ai-backend
cd pacs-ai-backend
git checkout master
```

For private container registry installs: `docker login <registry>` first, then `docker compose pull` (skip the `git checkout` if shipping images only).

### 6.2 Configure `.env`
Copy and edit:
```bash
cp .env.example .env
$EDITOR .env
```

Variables to set: **see the full reference in §15.1** (covers both api-pacs root `.env` and `cardio-agent/study-service/.env`). At minimum, before first boot replace every `password` / `key` / `secret` value with a strong unique value generated by `openssl rand -hex 32`.

### 6.3 Start the stack
The root [docker-compose.yml](../docker-compose.yml) merges per-service compose files via `include:`. Required external network:
```bash
docker network create pacs-net
docker compose pull
docker compose up -d
```

Verify all containers are `healthy`:
```bash
docker compose ps
```

### 6.4 Database bootstrap

api-pacs splits state across **two** persistence backends — neither one stores both halves of the IAM picture, so be deliberate:

| Backend | What lives there |
|---|---|
| **PostgreSQL (5433)** | Ingestion control plane only — `inference_ingestion_jobs`, `inference_ingestion_candidates`, `inference_ingestion_processing_jobs`. **No tenant or user data.** |
| **PostgreSQL (5434)** | cardio-agent execution state — `pipeline_jobs`, `pipeline_results`. |
| **Firebase Auth + Firestore** | All IAM data — `tenants`, `users`, `user_metadata`, email invites, onboarding-questionnaire answers (see [api-pacs/module/tenant/](../api-pacs/module/tenant/) and [api-pacs/module/user/](../api-pacs/module/user/) — both repositories use `cloud.google.com/go/firestore`). |

**SQL migrations:**
- **api-pacs** (golang-migrate; SQL files under `api-pacs/infrastructures/database/postgresql/migrations/`):
  ```bash
  cd api-pacs
  make migrate-up
  # Diagnostics:
  make migrate-version          # current schema version
  STEPS=1 make migrate-down     # roll back one step
  STEPS=<v> make migrate-force  # forcibly mark version (recovery only)
  ```
  Requires `POSTGRES_DB_HOST/PORT/DATABASE/USERNAME/PASSWORD` exported in the shell. The `migrate` binary must be installed on the host (https://github.com/golang-migrate/migrate). For containerized installs, run from inside the api-pacs container.
- **cardio-agent Alembic migrations:** `docker compose exec study-service alembic upgrade head`.

**Firestore seed data (tenants):** there is no SQL seed — tenants are created manually as Firestore documents during install. See §6.5.2.

**Firestore seed data (roles):** roles are not stored as a separate collection — they're a value on each user document (`role: "owner" | "admin" | …`). The first owner is created via the privileged endpoint in §6.5.1.

### 6.5 First admin user & tenant

The first admin user is created **manually in the customer's Firebase Console** — there is no `make seed-admin` or bootstrap-from-env flow. The api-pacs DB picks the user up on first login.

#### Procedure

1. **Confirm Firebase prerequisites are in place** (see §5.1):
   - Firebase project exists for this customer.
   - Authentication is enabled with at least one sign-in provider (typically email/password).
   - api-pacs is running and pointed at the project (`FIREBASE_PROJECT_ID`, service-account JSON mounted).

2. **Create the admin user in Firebase Console:**
   - Open https://console.firebase.google.com → select the customer's project.
   - *Authentication → Users → Add user*.
   - Enter the admin's email and a temporary password (or send an email-link sign-in if enabled).
   - Note the **UID** that Firebase generates — useful when reconciling against the api-pacs DB.

3. **Communicate credentials to the admin** out-of-band (password manager, secure email, etc.). Do not record them in `.env` or git.

4. **Admin completes first login** against the api-pacs API (or the connected web UI). The Firebase ID token verification in api-pacs creates the user record on the api-pacs side.

5. **Assign admin role + tenant** — see §6.5.1 below.

#### 6.5.1 Granting tenant ownership

api-pacs exposes a privileged endpoint to bind a newly-created Firebase user to a tenant as its **owner**. The endpoint is gated by the `FirebaseSuperUserGuard` middleware ([api-pacs/interfaces/http/rest/middlewares/iam/IAMMiddleware.go](../api-pacs/interfaces/http/rest/middlewares/iam/IAMMiddleware.go)) — authenticate it with the `FIREBASE_SUPERUSER_KEY` value from `.env`, sent in the `X-FB-SUDO-KEY` header.

**Endpoint:** `POST /v1/user/owner/add`
**Auth header:** `X-FB-SUDO-KEY: <FIREBASE_SUPERUSER_KEY>`
**Body:**
```json
{
  "tenantId":  "<tenant-id>",
  "role":      "owner",
  "name":      "Alice Admin",
  "email":     "alice@hospital.org",
  "licenseNo": "MD12345",
  "specialty": "<doctor-specialty>"
}
```

> The `role` field **must** be `"owner"` — the handler rejects any other value with HTTP 403.

**Example (run from the api-pacs host):**
```bash
curl -X POST https://api.<customer-domain>/v1/user/owner/add \
  -H "X-FB-SUDO-KEY: ${FIREBASE_SUPERUSER_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "tenantId":  "acme-pacs",
    "role":      "owner",
    "name":      "Alice Admin",
    "email":     "alice@hospital.org",
    "licenseNo": "MD12345",
    "specialty": "Cardiology"
  }'
```

The handler creates the api-pacs DB record bound to that Firebase email and returns the user's generated initial password (or 409 if already exists).

#### 6.5.2 Tenant must exist first (Firestore)

The owner-creation endpoint requires an existing `tenantId`. There is **no public REST endpoint to create a tenant** — tenants are stored as documents in **Firestore**, not in Postgres, and are created manually in the Firebase Console.

#### Procedure

1. Open https://console.firebase.google.com → select the customer's project.
2. *Firestore Database → Data → Start collection* (or pick the existing `tenants` collection if other documents already exist).
3. Collection ID: `tenants`.
4. Create a new document with **Document ID = your `tenantId`** (e.g. `acme-pacs`). Use a stable, lowercase, kebab-case slug — this value is referenced by every user, study, and inference record.
5. Add the fields below:

| Field | Type | Example | Notes |
|---|---|---|---|
| `id` | string | `acme-pacs` | Same as the document ID |
| `name` | string | `Acme Cardiology Hospital` | Display name |
| `address` | string | `123 Main St, Montreal, QC` | Free-form |
| `onboarding_questionnaires` | string | `null` | Optional JSON blob; leave empty unless used |
| `onboarding_enable_registration` | bool | `false` | Self-serve user registration toggle |
| `onboarding_enable_consent` | bool | `false` | Consent-flow toggle |
| `onboarding_consent_link` | string | `""` | URL shown during consent step |
| `created_at` | int | `1715000000` | Unix epoch seconds |
| `updated_at` | int | `1715000000` | Unix epoch seconds |

> The schema comes from [api-pacs/module/tenant/domain/entity/Tenant.go](../api-pacs/module/tenant/domain/entity/Tenant.go). If the entity changes, update this list.

6. Save the document. The `tenantId` is now ready to use in the §6.5.1 `POST /v1/user/owner/add` call.

> **Multi-tenant installs:** repeat for each tenant. Each tenant has its own owner created via §6.5.1.

### 6.6 Orthanc modality configuration
Configure each remote PACS as a modality in Orthanc:
- File: [orthanc/](../orthanc/) configuration.
- Required fields: AE title, host, port.
- Restart Orthanc to pick up changes.

### 6.7 Smoke tests
Run after install, before handoff. Steps 1–3 are CLI checks; step 4 is driven through the PACS-AI web UI.

**Prereqs:** install `dcmtk` on the api-pacs host if not present (`apt-get install dcmtk` / `dnf install dcmtk`) — supplies `echoscu`, `findscu`, `movescu`.

```bash
# 1. C-ECHO from api-pacs host to remote PACS — confirms DIMSE association
echoscu -aet ${ORTHANC_AET} -aec <REMOTE_AE> <REMOTE_HOST> <REMOTE_PORT>

# 2. Orthanc REST reachable
curl -u <user>:<pass> http://localhost:8042/system

# 3. api-pacs reachable (root status route — see §10.1)
curl https://api.<customer-domain>/
```

**4. End-to-end ingestion test (web UI)**

End-to-end verification is performed through the PACS-AI frontend (deployed alongside this backend — see [../PACS-AI](../../PACS-AI/) repo).

1. Log in as the tenant owner created in §6.5.
2. Navigate to *Inference → Ingestion jobs → Add* (or the equivalent in your build).
3. Create a job with the customer's modality, a short recent-window (e.g. 30–60 min), and the Orthanc destination AE.
4. Watch the job's candidates progress through the lifecycle states:
   `DISCOVERED → STABLE → RETRIEVAL_QUEUED → RETRIEVED → PROCESSING_QUEUED → PROCESSED`
   See [docs/ingestion-architecture-plan.md](./ingestion-architecture-plan.md) for the full state machine.
5. Confirm the resulting study is visible in the Orthanc viewer and that an inference result row landed in the cardio-agent DB:
   ```bash
   docker compose exec postgres psql -U cardio -d cardio_agent \
     -c "SELECT id, study_instance_uid, status, created_at FROM pipeline_jobs ORDER BY created_at DESC LIMIT 5;"
   ```

If the candidate gets stuck in any state for more than ~5 min, see §12.1 (candidate stuck in a state).

---

## 7. PACS integration runbook

### 7.1 AE title / IP / port matrix

> **Discuss with the customer's IT / PACS administrator.** Only they can supply the remote PACS AE Title, host, and port, and they typically also assign the AE Title and host that they will accept from us. Fill this table together during the kickoff call, **before** install. Both sides must register the values for DIMSE association to succeed.

| Role | AE Title | Host / IP | Port | Source |
|---|---|---|---|---|
| api-pacs (calling AE) | _to be assigned by customer IT_ | this host's IP | — | Customer IT |
| Orthanc (C-STORE listener) | _to be assigned by customer IT_ | this host's IP | `${ORTHANC_DICOM_PORT}` (default `4242`) | Customer IT (AET); us (port) |
| Remote PACS (called AE) | _from customer IT_ | _from customer IT_ | _from customer IT_ | Customer IT |

Once filled in, copy the assigned values into:
- api-pacs `.env`: `ORTHANC_AET`, `ORTHANC_DICOM_AET`, `ORTHANC_DICOM_PORT`.
- Orthanc modality config (see §6.6).
- Customer's PACS — they must whitelist our AE Title + IP and route C-STORE responses back to us.

### 7.2 DIMSE services used
- **C-FIND** — study discovery (api-pacs ingestion runner).
- **C-MOVE** — study retrieval (api-pacs retrieval worker → Orthanc as destination).
- **C-STORE** — Orthanc receives DICOM instances.
- Modality Worklist (MWL): **No** — PACS-AI does not provide or consume MWL. We only do C-FIND/C-MOVE for retrieval and C-STORE for receipt. The hospital's own RIS/PACS owns worklist for the imaging modalities.

### 7.3 Polling load and intervals
Configurable via api-pacs `.env`:

| Env var | Purpose | Default |
|---|---|---|
| `INFERENCE_INGESTION_RUNNER_INTERVAL_MINUTES` | Discovery (C-FIND) loop period | 1 |
| `INFERENCE_INGESTION_RETRIEVAL_WORKER_INTERVAL_MINUTES` | C-MOVE retrieval loop period | 1 |
| `INFERENCE_INGESTION_RECONCILIATION_INTERVAL_MINUTES` | Reconciliation loop period | 5 |
| `INFERENCE_INGESTION_RECONCILIATION_STALE_MINUTES` | Threshold to consider a processing job stale | 15 |
| `INFERENCE_INGESTION_DEFAULT_RECENT_WINDOW_MINUTES` | C-FIND lookback window | 240 (4 h) |

**Estimated query rate against the remote PACS:** 1 C-FIND per ingestion job per minute (default `INFERENCE_INGESTION_RUNNER_INTERVAL_MINUTES=1`), plus up to 1 C-MOVE per queued candidate per minute (default `INFERENCE_INGESTION_RETRIEVAL_WORKER_INTERVAL_MINUTES=1`). With **N** ingestion jobs configured and a peak of **M** concurrent retrievals, the steady-state load is **N + M** queries/min. Confirm acceptable with the hospital PACS admin during the §7.1 kickoff call.

### 7.4 Connectivity validation
```bash
# C-ECHO
echoscu -aet PACS_AI -aec <REMOTE_AE> <REMOTE_HOST> <REMOTE_PORT>

# C-FIND for a known StudyInstanceUID
findscu -v -S -aet PACS_AI -aec <REMOTE_AE> \
  -k QueryRetrieveLevel=STUDY -k StudyInstanceUID=<UID> \
  <REMOTE_HOST> <REMOTE_PORT>
```

---

## 8. Data lifecycle & retention

### 8.1 What we store
| Data | Location | Lifetime | PHI? |
|---|---|---|---|
| DICOM instances | Orthanc volume | **24 h rolling cache** (see §8.2) | Yes |
| Ingestion candidates / jobs | Postgres (5433) | Long-term | Yes (study UIDs, timestamps) |
| Inference results | Postgres (5434) `pipeline_results` | Long-term | Yes (derived) |
| Audit / activity logs | Elasticsearch | Long-term (subject to ES index retention) | Yes |

### 8.2 Retention policy

**DICOM (Orthanc) — rolling 24 h cache.**
- Studies are pulled, processed, and purged on a 24-hour rolling window. Orthanc is a working cache, not an archive.
- Knob: `ORTHANC_LOCAL_CACHE_EXPIRATION_IN_HOURS` (default `24`). Increase only if the customer needs a longer retry/replay window — disk usage scales linearly.

**Long-term data — kept until explicitly deleted.**
- **Inference candidate / job rows** (Postgres 5433): retained for audit and reporting. No automatic deletion.
- **Inference results** (Postgres 5434, `pipeline_results`): retained indefinitely. Customer decides on archival.
- **Audit / activity logs** (Elasticsearch): no default Index Lifecycle Management (ILM) policy is shipped — **the customer must define one**. Without an ILM policy, indices grow indefinitely and will eventually fill the Elasticsearch volume. Common retention windows seen in the field: 30 days (cost-sensitive), 90 days (typical operational), 365 days (compliance-driven). See §8.2.1 for a sample policy template.

#### 8.2.1 Sample ES ILM policy

Replace `<retention-days>` with the customer's chosen window and apply once during install:

```bash
# 1. Create the policy
curl -X PUT "http://localhost:9200/_ilm/policy/pacs-ai-logs" \
  -H 'Content-Type: application/json' \
  -d '{
    "policy": {
      "phases": {
        "hot": {
          "actions": {
            "rollover": { "max_age": "30d", "max_primary_shard_size": "50gb" }
          }
        },
        "delete": {
          "min_age": "<retention-days>d",
          "actions": { "delete": {} }
        }
      }
    }
  }'

# 2. Bind the policy to api-pacs log indices via an index template
curl -X PUT "http://localhost:9200/_index_template/pacs-ai-logs-template" \
  -H 'Content-Type: application/json' \
  -d '{
    "index_patterns": ["pacs-ai-*", "logs-*"],
    "template": {
      "settings": {
        "index.lifecycle.name": "pacs-ai-logs",
        "index.lifecycle.rollover_alias": "pacs-ai-logs"
      }
    }
  }'
```

> Adjust `index_patterns` to match the index names api-pacs actually writes (verify with `curl http://localhost:9200/_cat/indices`). Validate the policy is active with `curl http://localhost:9200/_ilm/policy/pacs-ai-logs`.

The customer adjusts the cache TTL via api-pacs `.env`; long-term retention is policy-driven (not enforced by env vars at present).

### 8.3 Cleanup / deletion
- **DICOM cleanup** is enforced by the `ORTHANC_LOCAL_CACHE_EXPIRATION_IN_HOURS` mechanism described above — automatic and active by default.
- **Phase-4 candidate-cleanup worker** described in [ingestion-architecture-plan.md](./ingestion-architecture-plan.md) is **not yet shipped** — only the discovery, retrieval, and reconciliation workers are wired up in [api-pacs/interfaces/cron.go](../api-pacs/interfaces/cron.go). Track its arrival in release notes; until then, candidate rows for processed/failed studies persist in Postgres.

### 8.4 PHI handling
- **In transit:** all HTTP traffic terminates TLS at nginx; DIMSE between api-pacs and Orthanc runs on the internal `pacs-net` Docker network (not exposed externally).
- **DIMSE to remote PACS:** Orthanc supports DICOM-TLS (configurable via `ORTHANC__DICOM_TLS_*` env keys), but **the shipped Orthanc compose file does not enable it**. If the customer requires DICOM-TLS, add the relevant config to [orthanc/docker-compose.yml](../orthanc/docker-compose.yml) and provision the cert/key.
- **At rest:** Full-disk encryption (LUKS for bare metal, cloud-provider volume encryption for VMs) is **recommended** but the choice and configuration is the customer's. PACS-AI does not bundle Postgres-level TDE or per-column encryption — encryption at rest is delegated to the underlying storage layer.
- **Pseudonymization / de-identification:** **Not configured.** DICOM studies flow through PACS-AI with full PHI in headers and pixel metadata — nothing is stripped at retrieval, processing, or storage. The privacy controls in the deployment are (1) network/tenant access controls (Firebase auth, RBAC), (2) the 24-hour rolling cache (§8.2) which bounds how long imaging is retained, and (3) the host security posture provided by the customer. Customers requiring de-identification must perform it upstream — either at the source PACS before C-MOVE or in a hospital-side pipeline before studies enter our retrieval window.

---

## 9. Backups & disaster recovery

### 9.1 What to back up

> **Current state (as of this revision): no application-data backups are configured.** The customer's host-level snapshot or backup tool (if any) is the only safety net for application state. Customers who require formal DR must add backups themselves — see §9.4 for what to add and why.

| Component | Method | Frequency | Retention | Status |
|---|---|---|---|---|
| Postgres (5433) — ingestion control plane | not configured | — | — | **No backups** |
| Postgres (5434) — cardio-agent results | not configured | — | — | **No backups** |
| Orthanc DICOM storage | not configured | — | — | **No backups** (24h rolling cache; re-fillable from the remote PACS — see §8.2) |
| Elasticsearch indices | not configured | — | — | **No backups** (logs only; treated as rebuildable) |
| `.env` and Firebase service-account JSON | customer's secrets vault | on change | indefinite | Customer-owned |
| TLS certs | customer's secrets vault | on rotation | indefinite | Customer-owned |

### 9.2 Restore procedure
Not applicable today — see §9.1. If the host fails, the system is reinstalled from scratch (per §6) and Orthanc refills from the remote PACS over the next 24 h.

> **What is lost on host failure** (today): all `pipeline_results` (AI inference outputs), all ingestion job/candidate history (which studies were processed, when, by which model). These can be regenerated by re-running ingestion against the same source-PACS window — but only if the studies are still present at the source.

### 9.3 RPO / RTO targets
- **RPO (Recovery Point Objective):** how much data the customer can afford to lose, measured in time — i.e. how recent the most recent backup must be. With no backups configured, the **effective RPO is unbounded** (everything since the last manual snapshot, if any, is at risk).
- **RTO (Recovery Time Objective):** how fast the system must be back online after a failure. With no backups, the **effective RTO is "time to reinstall + 24 h to refill Orthanc"** — typically 4–24 h depending on the customer's runbook.
- **Targets committed to customers:** none today. Per-customer targets (and the backup configuration to meet them) should be agreed during contract scoping.
- **Restore drill cadence:** N/A until backups exist; recommended quarterly once a backup strategy is in place.

### 9.4 Recommended additions (when DR becomes a requirement)

The architecture makes most components rebuildable, so a minimal backup strategy can be inexpensive:

- **Postgres 5434 (`pipeline_results`)** — the only data that is truly costly to regenerate (re-running models is expensive and depends on the source studies still being available). **Highest priority.** `pg_dump` cron + offsite copy is sufficient.
- **Postgres 5433 (ingestion control plane)** — useful for audit history (which studies were retrieved/processed when). Medium priority. Same `pg_dump` pattern.
- **Firestore (tenants/users)** — managed by Firebase; rely on Google Cloud's project-level export tooling if customer requires a copy.
- **Orthanc DICOM storage** — skip; refills from the remote PACS within the cache TTL.
- **Elasticsearch** — skip; logs are rebuildable from container output if needed.
- **Secrets** (`.env`, Firebase service-account, TLS certs) — already in the customer's secrets vault per §5.7.

---

## 10. Observability

### 10.1 Health checks
| Service | Endpoint | Expected |
|---|---|---|
| api-pacs | `GET /` | 200 (root status route) |
| study-service | `GET /health` | `{"status":"healthy"}` |
| study-service (deep) | `GET /health/detailed` | DB + Redis + Celery worker reachability |
| study-service (metrics) | `GET /metrics` | Prometheus exposition |
| Orthanc | `GET /system` | 200 |
| Postgres | `pg_isready` | exit 0 |
| Redis | `redis-cli PING` | `PONG` |
| Elasticsearch | `GET /_cluster/health` | `green` or `yellow` |

> The deep study-service endpoints (`/health/detailed`, `/metrics`, `/jobs*`, `/settings`) require `STUDY_SERVICE_OPERATOR_TOKEN` as a bearer header unless `ALLOW_UNAUTHENTICATED_OPERATOR_ROUTES=true` (dev only).

### 10.2 Logs
- All services log to stdout — captured by Docker.
- View locally: `docker compose logs -f <service>` or `docker compose logs --since 1h --tail 200 api-pacs`.

**Forwarding to customer SIEM — pick one of three patterns:**

**A) Native Docker `syslog` driver** (simplest; per-service):
```yaml
# docker-compose.override.yml
services:
  api-pacs:
    logging:
      driver: syslog
      options:
        syslog-address: "udp://siem.customer.local:514"
        tag: "pacs-ai/{{.Name}}"
```

**B) Filebeat sidecar** (richer parsing, JSON output to Elasticsearch / Logstash):
```yaml
# docker-compose.override.yml
services:
  filebeat:
    image: docker.elastic.co/beats/filebeat:8.13.4
    user: root
    volumes:
      - /var/lib/docker/containers:/var/lib/docker/containers:ro
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./filebeat/filebeat.yml:/usr/share/filebeat/filebeat.yml:ro
    command: ["-strict.perms=false"]
    networks: [pacs-net]
```
```yaml
# filebeat/filebeat.yml
filebeat.autodiscover:
  providers:
    - type: docker
      hints.enabled: true
output.logstash:
  hosts: ["logstash.customer.local:5044"]
```

**C) Vector** (lightweight; recommended when forwarding to non-Elastic backends):
```yaml
# docker-compose.override.yml
services:
  vector:
    image: timberio/vector:0.39.0-debian
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - ./vector/vector.toml:/etc/vector/vector.toml:ro
    networks: [pacs-net]
```
```toml
# vector/vector.toml
[sources.docker]
type = "docker_logs"

[sinks.siem]
type = "socket"
inputs = ["docker"]
address = "siem.customer.local:514"
mode = "tcp"
encoding.codec = "json"
```

Pick **A** for low-volume / quick wins, **B** when the customer already runs ELK, **C** for everything else.

### 10.3 Metrics
Prometheus instrumentation is **wired up but not actively scraped** by default — there is no bundled Prometheus / Grafana stack. Customers who want metrics-based monitoring point their existing observability platform at the endpoints below.

| Service | Endpoint | Auth | Notes |
|---|---|---|---|
| study-service | `GET /metrics` | Bearer `STUDY_SERVICE_OPERATOR_TOKEN` | FastAPI + Celery exposition; uses `PROMETHEUS_MULTIPROC_DIR` shared between the API process and the worker so worker counters are aggregated |
| api-pacs | `GET /debug/vars` | superuser-guarded (`X-FB-SUDO-KEY`) | Go [expvar](https://pkg.go.dev/expvar) — not Prometheus format; readable as JSON for ad-hoc inspection |

To collect metrics, deploy Prometheus alongside the stack and add a scrape job for `study-service:8600/metrics` with the operator-token header. There's no built-in alerting — see §10.4 for recommended rules to author.

### 10.4 Recommended alerts
- Candidate stuck in `RETRIEVAL_QUEUED` > N minutes.
- Celery queue depth > N.
- Orthanc disk > 80% full.
- Any service unhealthy > 5 min.
- Failed C-MOVE rate > N% over 15 min.

---

## 11. Upgrade & rollback

### 11.1 Versioning
- **Image and release tags follow git commit SHAs.** A release is a tagged commit on the main repo; the corresponding GitHub Release lists the SHA, the changelog, and any breaking-change notes.
- Release notes are published as **GitHub Releases** on this repository: https://github.com/HeartWise-AI/pacs-ai-backend/releases.
- Customer install records should pin the exact SHA shipped (recorded at install time per §1.4).

### 11.2 Pre-upgrade checks
- Read release notes for breaking changes and migration notes.
- Verify backups completed within last 24 h.
- Drain the Celery queue:
  ```bash
  # Stop accepting new tasks (api-pacs ingestion runner) — temporarily scale retrieval worker to 0
  # OR pause dispatch by stopping api-pacs:
  docker compose stop api-pacs

  # Wait for in-flight tasks to finish:
  until [ "$(docker compose exec -T redis redis-cli -n 2 LLEN celery)" = "0" ]; do
    echo "queue depth: $(docker compose exec -T redis redis-cli -n 2 LLEN celery)"
    sleep 5
  done

  # Confirm no active workers:
  docker compose exec study-service celery -A workers.celery_app inspect active
  ```
- Snapshot both Postgres volumes (5433 control plane + 5434 cardio-agent).

### 11.3 Upgrade sequence
```bash
git fetch && git checkout <new-tag>
docker compose pull
docker compose up -d                                          # rolling per service

# api-pacs migrations (golang-migrate; from inside the api-pacs container, or any host with the migrate binary
# and POSTGRES_DB_* env vars exported):
docker compose exec api-pacs make -C /app migrate-up          # adjust path if image WORKDIR differs
# or, from a host shell with envvars set:
#   cd api-pacs && make migrate-up

# cardio-agent migrations:
docker compose exec study-service alembic upgrade head
```

### 11.4 Rollback
- Revert to previous image tags and re-run `docker compose up -d`.
- **api-pacs migrations** (golang-migrate): reversible migrations support `STEPS=1 make migrate-down`. Not all migrations have `down` SQL — inspect [api-pacs/infrastructures/database/postgresql/migrations/](../api-pacs/infrastructures/database/postgresql/migrations/) before relying on it.
- **cardio-agent migrations** (Alembic): `docker compose exec study-service alembic downgrade -1`.
- If an irreversible migration was applied, restore from the snapshot taken in §11.2.

---

## 12. Operational runbooks (Day-2)

### 12.1 Candidate stuck in a state

Inspect:
```sql
-- candidates that have been queued for retrieval too long
SELECT id, study_instance_uid, status, updated_at, last_error
FROM inference_ingestion_candidates
WHERE status = 'RETRIEVAL_QUEUED' AND updated_at < now() - interval '15 min'
ORDER BY updated_at;

-- breakdown of stuck states
SELECT status, count(*), min(updated_at), max(updated_at)
FROM inference_ingestion_candidates
WHERE updated_at < now() - interval '30 min'
GROUP BY status;
```

There is **no manual retry endpoint** in the current api-pacs build. The reconciliation worker handles stale `PROCESSING_*` states automatically (see §7.3 — `INFERENCE_INGESTION_RECONCILIATION_STALE_MINUTES`, default `15`). For other states, requeue manually:

```sql
-- requeue a stuck retrieval (use with care; only after confirming the C-MOVE is truly dead, not in flight)
UPDATE inference_ingestion_candidates
SET status = 'STABLE', updated_at = now(), last_error = NULL
WHERE id = '<candidate-id>' AND status = 'RETRIEVAL_QUEUED';
```

The next retrieval-worker tick (≤ 1 min) will pick it up. Always copy the row to a one-off audit table before mutating production state.

### 12.2 Celery queue backed up
```bash
docker compose exec redis redis-cli -n 2 LLEN celery     # cardio-agent uses Redis DB 2 in integrated mode
docker compose logs --tail=200 study-celery-worker
```
Mitigation:
- Increase per-worker concurrency: set `CELERY_WORKER_CONCURRENCY` (default `2`) and restart the worker.
- Scale worker replicas: `docker compose up -d --scale study-celery-worker=N`.

### 12.3 Orthanc disk full

The Phase-4 candidate-cleanup worker is **not yet shipped** (see §8.3); rely on Orthanc's own TTL eviction plus manual culling in incidents.

Inspect:
```bash
# Disk usage on the Orthanc volume
docker compose exec orthanc df -h /var/lib/orthanc/db

# Total studies in Orthanc
curl -s http://localhost:8042/statistics | jq

# Oldest 50 studies (LastUpdate ascending)
curl -s "http://localhost:8042/studies?expand&limit=50" \
  | jq 'sort_by(.LastUpdate) | .[0:50] | .[] | {ID, LastUpdate, MainDicomTags}'
```

Mitigations, in order of preference:

```bash
# 1. Confirm/lower the cache TTL, then restart api-pacs to pick up the new value.
#    Default is 24h; setting to e.g. 12h forces the next eviction sweep sooner.
$EDITOR .env   # ORTHANC_LOCAL_CACHE_EXPIRATION_IN_HOURS=12
docker compose up -d api-pacs

# 2. Manually delete a single study by Orthanc ID
curl -X DELETE http://localhost:8042/studies/<orthanc-study-id>

# 3. Bulk delete studies older than N hours (CAREFUL — irreversible)
HOURS=24
curl -s "http://localhost:8042/studies?expand" \
  | jq -r --arg cutoff "$(date -u -d "$HOURS hours ago" +%Y%m%dT%H%M%S)" \
       '.[] | select(.LastUpdate < $cutoff) | .ID' \
  | xargs -I{} curl -X DELETE "http://localhost:8042/studies/{}"
```

Add disk-usage monitoring (§10.4) to avoid hitting this state.

### 12.4 Model container crash-looping
```bash
docker compose logs --tail=200 <model-service>
```
Common causes: GPU driver mismatch, missing weights, OOM.

### 12.5 Remote PACS unreachable
1. `echoscu` from host (section 7.4).
2. Check api-pacs ingestion runner logs.
3. Confirm hospital firewall hasn't blocked us.

### 12.6 Firebase auth outage
- All API calls return 401. Confirm via Firebase status page.
- No local mitigation — wait for upstream.

---

## 13. Security & compliance

### 13.1 Trust boundaries
1. Browser ↔ nginx (TLS terminates at nginx).
2. Remote PACS ↔ api-pacs / Orthanc — DIMSE over TCP. **DICOM-TLS is supported by Orthanc but not enabled by default**; enable it (see §8.4) when the customer's PACS requires it or when the link traverses untrusted infrastructure.
3. Model containers run inference code — isolated by Docker, no host filesystem mounts beyond the model cache and a read-only model-weights volume.
4. Internal service-to-service traffic (api-pacs ↔ study-service ↔ Celery ↔ Orthanc ↔ Postgres ↔ Redis ↔ Elasticsearch) runs on the `pacs-net` Docker network and is not exposed externally.

### 13.2 Authentication & authorization
- Firebase ID tokens verified by api-pacs on every request.
- Multi-tenant: every record scoped to `tenant_id`; cross-tenant reads are blocked at the application layer.
- **Roles** (from [api-pacs/module/iam/domain/entity/Role.go](../api-pacs/module/iam/domain/entity/Role.go)):

| Role | Scope |
|---|---|
| `OWNER` | Tenant owner — full control over the tenant; created during install via `POST /v1/user/owner/add` (§6.5); can add or remove `ADMIN` and `USER` members. |
| `ADMIN` | Tenant administrator — manages users, ingestion jobs, and inference models; cannot transfer or delete the tenant. |
| `USER` | Clinician / regular user — can view studies and inference results; cannot change tenant configuration or membership. |

Roles are enforced via Chi middleware: `RBACOwnerGuard` (owner only) and `RBACOwnerOrAdminGuard` (owner or admin) — see [IAMMiddleware.go](../api-pacs/interfaces/http/rest/middlewares/iam/IAMMiddleware.go).

### 13.3 Audit logging

**Sink:** Elasticsearch only — no separate immutable archive ships by default. Customers requiring tamper-resistant audit must replicate the indices to write-once storage on their side (see §10.2 for forwarding patterns).

**Export format:** Kibana (deployed alongside Elasticsearch — see [docker-compose.yml](../docker-compose.yml)). Customers run audit queries in Kibana Discover and export results as **CSV or JSON** via *Share → CSV Reports* / *Inspect → Download CSV*. Direct `_search` API queries (`curl http://elasticsearch:9200/<index>/_search ...`) are also supported for scripted dumps.

**Events logged** — derived from the audit entities under [api-pacs/module/elasticsearch/domain/entity/](../api-pacs/module/elasticsearch/domain/entity/). Every event carries `tenant_id`, `tenant_name`, `user_id`, `email`, `name`, and `timestamp` (Unix epoch), so each is attributable to a tenant + user.

| Event | ES index | When emitted | Notable fields |
|---|---|---|---|
| User login | `logins` | Successful authentication | `session_id`, `role`, `specialty` |
| Admin invite sent | `admin_invites` | Tenant admin invites a new user | invitee `email` |
| User management | `admin_members` | User created, role changed, or removed | `action` (`CREATE` / `UPDATE` / `DELETE`), target user, role, license |
| Remote-PACS query (C-FIND) | `find_modality_studies` | api-pacs queries source PACS for studies | `modality_id`, `query_id` |
| Study retrieved (C-MOVE) | `retrieved_studies` | Successful C-MOVE into local Orthanc | `study_instance_uid`, `modality_id` |
| Inference run | `predict_inference_models` | Model container produces a prediction | `inference_model_id`, `docker_image`, `model`, `study_instance_uid`, `series_instance_uids`, free-form `additional_metadata` |
| Consent signed | `signed_consents` | User signs the onboarding consent | (tenant + user only) |
| Custom series stored | `stored_custom_series` | AI-generated DICOM series persisted back into Orthanc | `study_instance_uid`, `series_instance_uids`, `custom_series_instance_uid`, `custom_sop_instance_uid`, `model_name`, `model_version`, `patient_id` |

> The `predict_inference_models` and `stored_custom_series` events are the audit trail for AI activity — they are the canonical "what model touched what study, when, on whose behalf" record for compliance review.

### 13.4 Compliance posture

PACS-AI's compliance program is **actively in development** in coordination with external legal counsel — covering HIPAA, GDPR, Quebec Law 25, and adjacent healthcare-data frameworks. **No third-party attestations or certifications (SOC 2 report, HITRUST, ISO 27001, etc.) ship today.** Specific compliance commitments — including BAAs and DPAs — are negotiated per customer engagement based on the applicable framework and the customer's deployment context.

The customer's compliance / privacy / security team should treat PACS-AI as **a technical platform that supports their existing compliance program**: the customer remains the data controller / covered entity; PACS-AI is the technology they operate within their compliance perimeter.

The **technical safeguards** in the system (which a reviewer can map onto whichever framework applies) are listed below. These are derived from the codebase, not from a third-party assessment:

| Control area | Implementation | Reference |
|---|---|---|
| Authentication | Firebase ID token verification on every API request; sessions tracked in Redis | [IAMMiddleware.go](../api-pacs/interfaces/http/rest/middlewares/iam/IAMMiddleware.go) |
| Authorization (RBAC) | Three roles (`OWNER` / `ADMIN` / `USER`) enforced via Chi middleware | §13.2; [Role.go](../api-pacs/module/iam/domain/entity/Role.go) |
| Multi-tenant isolation | Every record scoped to `tenant_id`; cross-tenant access blocked at the application layer | §13.2 |
| Privileged operations | Separate `X-FB-SUDO-KEY` superuser guard for tenant/owner provisioning | §6.5.1 |
| Encryption in transit (external) | TLS terminates at nginx for browser/API traffic | §4 |
| Encryption in transit (DIMSE) | DICOM-TLS supported by Orthanc, customer-enabled when required | §8.4 |
| Encryption at rest | Delegated to host-level FDE (recommended, customer-owned) | §8.4 |
| Data minimization | 24-hour rolling DICOM cache; no long-term imaging storage | §2.2, §8.2 |
| Audit logging | 8 event types indexed to Elasticsearch (login, user mgmt, C-FIND, C-MOVE, inference runs, consents, custom series) | §13.3 |
| Service-to-service auth | Bearer-token auth between api-pacs and study-service (3 distinct tokens) | §5.3 |
| Secrets handling | Service-account JSON mounted from a host secrets dir, never committed | §5.1, §5.7 |
| Network isolation | Internal services on a dedicated `pacs-net` Docker network, not exposed externally | §3, §13.1 |
| Container isolation | Each component in its own container; model containers run with no host filesystem mounts beyond model cache | §13.1 |

**HIPAA** — BAAs are negotiated per US-covered-entity engagement as part of contracting; no BAA template is published with this technical guide. The technical safeguards above support the HIPAA Security Rule's Technical Safeguards (164.312) — access control, audit controls, integrity, person/entity authentication, transmission security.

**GDPR / Quebec Law 25 / other regional** — No regional compliance attestation. The customer's legal team should map the safeguards above onto the applicable framework. Operational defaults relevant to regional reviews: timezone defaults to `America/Toronto` (Canadian operational origin); Firebase data residency follows the project the customer provisions; all DICOM data is held in a 24-hour rolling cache with no long-term imaging persistence (data minimization).

**SOC 2** — No SOC 2 report ships today. Whether to pursue Type I / Type II is part of the active compliance discussion with counsel.

> When a customer requests a compliance posture document, share this section as the technical-safeguards inventory and route contractual / legal questions (BAA, DPA, jurisdiction-specific commitments) to PACS-AI's compliance owner / legal counsel.

### 13.5 Vulnerability management

A formal vulnerability-management program is **in progress** — coordinated with external legal/compliance counsel. Specific artifacts (CI scan reports, third-party attestations) are not available today. The defaults below are commitments PACS-AI makes to customers in the absence of a per-customer SLA.

| Control | Stance | Notes |
|---|---|---|
| Container image scanning | Ad-hoc on release; **CI integration on roadmap** | No `.github/workflows/` configured today. When added, expect Trivy or Docker Scout on every release tag; report retention TBD with counsel. |
| Patch response — Critical (CVSS ≥ 9.0) | Patched + released within **14 days** of upstream disclosure | Anchored to NVD publication date. Faster if exploit is in the wild. |
| Patch response — High (CVSS 7.0–8.9) | Patched within **30 days** | Same anchor. |
| Patch response — Medium (CVSS 4.0–6.9) | Patched within **90 days** | Best-effort; bundled into the next regular release. |
| Patch response — Low (CVSS < 4.0) | Best-effort | No SLA. |
| Base-image refresh | Quarterly minimum | All bundled images (Postgres, Orthanc, Redis, ES, Kibana) are pinned; refreshed on each release. |
| CVE disclosure contact | No dedicated channel today | Vulnerability reports should reach the customer's named PACS-AI contact via Slack (§14.1) — **not** GitHub Issues (public). Setting up a private `security@` mailbox is part of the in-progress vulnerability-management program. |

> The patch SLA targets above align with common hospital procurement expectations; tighten or relax in the customer's contract if needed.

### 13.6 Penetration testing
- **Last third-party penetration test:** none conducted to date. Penetration testing is on the compliance roadmap being scoped with external counsel.
- **Report sharing:** when a pen test is conducted, the executive summary will be available to customers under NDA on request; full reports remain internal.
- **Internal security review:** code review of every PR via standard pull-request workflow on https://github.com/HeartWise-AI/pacs-ai-backend.

---

## 14. Support & escalation

### 14.1 Support channels

Two channels are used today:

- **Slack** — a shared channel is provisioned per customer engagement at install time. Use this for questions and incident coordination during business hours.
- **GitHub Issues** — open an issue on https://github.com/HeartWise-AI/pacs-ai-backend/issues for reproducible bugs, feature requests, or anything you'd like tracked publicly. Do **not** include PHI or customer-identifying information in issues.

Use Slack for anything time-sensitive or sensitive in content; use GitHub Issues for everything else.

### 14.2 Hours and SLA

**No formal SLA today.** Support is best-effort during business hours (Eastern Time, Monday–Friday). Customers requiring a contractual SLA — defined response/resolution times, after-hours coverage, named escalation contacts — should raise this in contract scoping; an SLA can be negotiated as part of the engagement.

### 14.3 What to attach to a support ticket
```bash
# Recent logs from all services
docker compose logs --since 1h > pacs-ai-logs.txt

# Container health
docker compose ps > pacs-ai-status.txt

# Versions
docker compose images > pacs-ai-versions.txt
```

---

## 15. Appendices

### 15.1 Full `.env` reference

#### api-pacs (root `.env`)

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `APP_TIMEZONE` | yes | `America/Toronto` | IANA TZ for all containers |
| `APP_URL` | yes | — | Public URL of the API (used in emails/links) |
| `API_NAME` | yes | — | Display name |
| `API_BASE_URL` | yes | — | Internal base URL for the API |
| `API_URL_REST_PORT` | yes | `8000` | api-pacs REST port |
| `DOCKER_NETWORK` | yes | `pacs-net` | Shared external Docker network |
| `DOCKER_USERNAME` / `DOCKER_PASSWORD` | optional | — | Registry login if pulling private images |
| **PostgreSQL (control plane)** | | | |
| `POSTGRES_DB_HOST` | yes | — | Postgres host |
| `POSTGRES_DB_PORT` | yes | `5432` (container) / `5433` (host) | Postgres port |
| `POSTGRES_DB_DATABASE` | yes | — | Database name |
| `POSTGRES_DB_USERNAME` | yes | — | Username |
| `POSTGRES_DB_PASSWORD` | yes | — | Password |
| **Redis** | | | |
| `REDIS_HOST` | yes | `redis` | Redis host |
| `REDIS_PORT` | yes | `6379` | Redis port |
| `REDIS_PASSWORD` | yes | — | Redis password (must match cardio-agent `REDIS_URL` in integrated mode) |
| `REDIS_IAM_DB` | yes | — | DB index used for IAM cache |
| **Elasticsearch / Kibana** | | | |
| `ELASTICSEARCH_URL` | yes | — | ES URL |
| `KIBANA_BASE_URL` | optional | — | Kibana base URL for log links |
| **Firebase** | | | |
| `FIREBASE_PROJECT_ID` | yes | — | Firebase project |
| `FIREBASE_CONFIG_FILE_PATH` | yes | — | Path to mounted service-account JSON |
| `FIREBASE_SUPERUSER_KEY` | yes | — | Privileged key for admin operations |
| **Orthanc** | | | |
| `ORTHANC_BASE_URL` | yes | `http://orthanc:8042` | Orthanc REST URL |
| `ORTHANC_AET` | yes | — | api-pacs AE Title for DIMSE |
| `ORTHANC_LOCAL_CACHE_EXPIRATION_IN_HOURS` | optional | — | Local cache TTL |
| `ORTHANC_DICOM_PORT` | yes (Orthanc compose) | `4242` | DIMSE listening port |
| `ORTHANC_DICOM_AET` | yes (Orthanc compose) | — | Orthanc AE Title |
| **Ingestion timing** | | | |
| `INFERENCE_INGESTION_RUNNER_INTERVAL_MINUTES` | optional | `1` | Discovery loop period |
| `INFERENCE_INGESTION_RETRIEVAL_WORKER_INTERVAL_MINUTES` | optional | `1` | Retrieval loop period |
| `INFERENCE_INGESTION_RECONCILIATION_INTERVAL_MINUTES` | optional | `5` | Reconciliation loop period |
| `INFERENCE_INGESTION_RECONCILIATION_STALE_MINUTES` | optional | `15` | Stale threshold |
| `INFERENCE_INGESTION_DEFAULT_RECENT_WINDOW_MINUTES` | optional | `240` | C-FIND lookback window (minutes) |
| **study-service handoff** | | | |
| `STUDY_SERVICE_BASE_URL` | yes | `http://study-service:8600` | study-service URL |
| `STUDY_SERVICE_INGEST_TOKEN` | yes | — | Bearer token for `POST /ingest/study` |
| `STUDY_SERVICE_OPERATOR_TOKEN` | yes | — | Bearer for control routes |
| `STUDY_SERVICE_CALLBACK_TOKEN` | yes | — | Bearer for incoming callbacks |
| `STUDY_SERVICE_DISPATCH_CONCURRENCY` | optional | — | Parallel dispatch fan-out |
| **Mail** | | | |
| `MAILGUN_API_KEY` / `MAILGUN_DOMAIN` / `MAILGUN_SENDER_EMAIL` | yes | — | Transactional email |
| `MAILCHIMP_API_KEY` / `MAILCHIMP_BASE_URL` / `MAILCHIMP_LIST_ID` | optional | — | Marketing list sync |
| **Public registration protection** | | | |
| `CLOUDFLARE_SECRET_KEY` / `CLOUDFLARE_TURNSTILE_BASE_URL` | required when public registration is enabled | — | Server-side Turnstile registration verification |
| `REGISTRATION_RATE_LIMIT_WINDOW_SECONDS` | optional | `600` | Fixed registration throttle window in seconds |
| `REGISTRATION_RATE_LIMIT_TENANT_ATTEMPTS` | optional | `100` | Maximum attempts per tenant and window |
| `REGISTRATION_RATE_LIMIT_EMAIL_ATTEMPTS` | optional | `5` | Maximum attempts per normalized email within its tenant and window |
| `REGISTRATION_RATE_LIMIT_IP_ATTEMPTS` | optional | `10` | Maximum attempts per trusted client IP within its tenant and window |
| `REGISTRATION_TRUSTED_PROXY_CIDRS` | required behind a reverse proxy | empty | Comma-separated CIDRs of direct proxies allowed to supply `X-Real-IP`; use the exact `pacs-net` subnet for the bundled Nginx deployment |
| **Optional integrations** | | | |
| `DOCUSIGN_*` (6 vars) | optional | — | DocuSign integration |
| `OPENAPI_DOCS_PASSWORD` | optional | — | Basic-auth on `/docs` |
| `ORCHESTRATOR_API_URL` | optional | — | URL of the optional orchestrator service |

Registration counters are stored in the IAM Redis database and use hashed,
tenant-scoped identifiers. When `REGISTRATION_TRUSTED_PROXY_CIDRS` is empty or
the direct peer is outside those networks, api-pacs ignores `X-Real-IP` and
uses the socket peer address. Throttled requests return
`REGISTRATION_RATE_LIMITED`, HTTP 429, and a `Retry-After` header in seconds.

#### cardio-agent / study-service (`cardio-agent/study-service/.env`)

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `APP_TIMEZONE` | yes | inherited | IANA TZ |
| `HOST` | yes | `0.0.0.0` | bind address |
| `PORT` | yes | `8600` | service port |
| `RELOAD` | optional | `false` | uvicorn reload (dev) |
| `LOG_LEVEL` | optional | `info` | log level |
| `LOG_FORMAT` | optional | `text` | `text` or `json` |
| `DATABASE_URL` | yes | — | Postgres URI for cardio-agent DB (5434) |
| `REDIS_URL` | yes | — | Redis URI; integrated default uses DB `2` |
| `ORTHANC_URL` | yes | `http://orthanc:8042` | Orthanc REST |
| `ORTHANC_CHANGES_LIMIT` | optional | `100` | Polling page size |
| `ORTHANC_POLL_START_MODE` | yes (polling mode) | `latest` | `latest` / `backlog` / `since_timestamp` |
| `ORTHANC_POLL_START_TIMESTAMP` | conditional | — | Required if `since_timestamp` |
| `ORTHANC_POLL_INTERVAL` | optional | — | Polling interval (polling mode) |
| `ORTHANC_POLL_LOCK_TTL` | optional | — | Distributed-lock TTL |
| `ENABLE_ORTHANC_POLLING` | yes | `false` | Mode flag |
| `ENABLE_GO_CALLBACKS` | yes | `true` | Mode flag — exactly one of polling/callbacks must be true |
| `GO_CALLBACK_BASE_URL` | yes (callbacks mode) | — | api-pacs callback base URL |
| `GO_CALLBACK_URL` | optional | — | Override full callback URL |
| `GO_CALLBACK_TIMEOUT_SECONDS` | optional | `5` | Callback HTTP timeout |
| `GO_CALLBACK_MAX_RETRIES` | optional | `4` | Callback retries |
| `GO_CALLBACK_TOKEN` | yes (callbacks mode) | — | Mirror of `STUDY_SERVICE_CALLBACK_TOKEN` |
| `STUDY_SERVICE_INGEST_TOKEN` | yes | — | Mirror of api-pacs value |
| `STUDY_SERVICE_OPERATOR_TOKEN` | yes | — | Mirror of api-pacs value |
| `STUDY_SERVICE_CALLBACK_TOKEN` | yes | — | Mirror of api-pacs value |
| `ALLOW_UNAUTHENTICATED_INGEST` | optional | `false` | Dev-only auth bypass |
| `ALLOW_UNAUTHENTICATED_OPERATOR_ROUTES` | optional | `false` | Dev-only auth bypass |
| `CELERY_WORKER_CONCURRENCY` | optional | `2` | Worker thread pool size |
| `PROMETHEUS_MULTIPROC_DIR` | yes | `/tmp/prometheus-multiproc` | Shared metrics dir |
| `CORS_ORIGINS` | optional | — | Allowed CORS origins |
| `SHARED_DOCKER_NETWORK` / `CARDIO_AGENT_SHARED_NETWORK` | yes | `pacs-net` | External docker network |

> **Mode invariant:** exactly one of `ENABLE_ORTHANC_POLLING` and `ENABLE_GO_CALLBACKS` must be `true`. The integrated pacs-ai-backend topology uses `ENABLE_ORTHANC_POLLING=false` + `ENABLE_GO_CALLBACKS=true` (api-pacs explicitly hands off studies). Standalone study-service uses the inverse.

### 15.2 Port matrix
See section 3.

### 15.3 Common `docker-compose.override.yml` variants

Compose merges `docker-compose.override.yml` into the root [docker-compose.yml](../docker-compose.yml) automatically. Drop one of these into the repo root next to the root compose file.

**A) No GPU host** — keep api-pacs / Orthanc / data stores on this host; run model workers elsewhere.

```yaml
services:
  # Stop the local Celery worker — model containers run on a separate GPU host
  study-celery-worker:
    deploy:
      replicas: 0
    profiles: ["disabled"]

  # Optional: stop ollama if it was enabled
  ollama:
    profiles: ["disabled"]
```

> Point study-service at the remote Celery broker by setting `REDIS_URL` to the GPU host's Redis URL in `cardio-agent/study-service/.env`.

**B) External (managed) PostgreSQL** — use RDS / Cloud SQL / customer-managed Postgres instead of the bundled containers.

```yaml
# 1. Edit the root docker-compose.yml `include:` list and remove:
#      - postgresql/docker-compose.yml
#    Also remove the inline `postgres:` service block (cardio-agent DB) from the root compose
#    if cardio-agent is also moving to managed Postgres.

# 2. Override env to point at the managed instance:
services:
  api-pacs:
    environment:
      POSTGRES_DB_HOST: "pacs-control.<rds-endpoint>"
      POSTGRES_DB_PORT: "5432"
      POSTGRES_DB_DATABASE: "pacs_control"
      POSTGRES_DB_USERNAME: "pacs_app"
      POSTGRES_DB_PASSWORD: "${POSTGRES_DB_PASSWORD}"

  study-service:
    environment:
      DATABASE_URL: "postgresql://cardio:${CARDIO_DB_PASSWORD}@cardio-agent.<rds-endpoint>:5432/cardio_agent"
  study-celery-worker:
    environment:
      DATABASE_URL: "postgresql://cardio:${CARDIO_DB_PASSWORD}@cardio-agent.<rds-endpoint>:5432/cardio_agent"
```

> Run migrations against the managed instance from a host with the `migrate` binary and `POSTGRES_DB_*` exported (see §6.4 / §11.3).

**C) External (managed) Redis** — ElastiCache, Cloud Memorystore, etc.

```yaml
# 1. Remove `redis/docker-compose.yml` from the root compose `include:` list.

# 2. Override env to point at the managed cluster:
services:
  api-pacs:
    environment:
      REDIS_HOST: "pacs-redis.<elasticache-endpoint>"
      REDIS_PORT: "6379"
      REDIS_PASSWORD: "${REDIS_PASSWORD}"
      REDIS_IAM_DB: "0"

  study-service:
    environment:
      REDIS_URL: "redis://:${REDIS_PASSWORD}@pacs-redis.<elasticache-endpoint>:6379/2"
  study-celery-worker:
    environment:
      REDIS_URL: "redis://:${REDIS_PASSWORD}@pacs-redis.<elasticache-endpoint>:6379/2"
```

**D) Air-gapped install** — pre-loaded image tarballs, no outbound egress.

```yaml
# 1. Pre-pull on a connected host:
#      docker compose pull
#      docker save -o pacs-ai-images.tar $(docker compose config --images)
#    Transfer pacs-ai-images.tar + the repo to the air-gapped host.
#
# 2. On the air-gapped host:
#      docker load -i pacs-ai-images.tar
#
# 3. Disable image pulls on `up`:
services:
  api-pacs: { pull_policy: never }
  study-service: { pull_policy: never }
  study-celery-worker: { pull_policy: never }
  orthanc: { pull_policy: never }
  # …repeat for every service
```

> Air-gap also requires hosting Firebase auth alternatives (Firebase needs egress to `*.googleapis.com`). Discuss with the customer whether to disable auth, swap to a self-hosted IdP, or run a tunneled allow-list for that one endpoint only.

### 15.4 Glossary
- **AE Title** — DICOM Application Entity Title; identifier used by DIMSE peers.
- **C-FIND / C-MOVE / C-STORE** — DICOM query, retrieve-instruction, and store services.
- **Candidate** — internal record representing a study under ingestion (see [ingestion architecture](./ingestion-architecture-plan.md)).
- **Tenant** — isolated customer/organization scope; all records carry a tenant ID.
- **Modality** — DICOM source type (CT, US, XA, etc.) and, in Orthanc terminology, a configured remote DIMSE peer.
- **TODO:** add domain-specific terms as they come up.
