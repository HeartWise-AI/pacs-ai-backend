# PACS AI Backend

[![Docker Compose](https://img.shields.io/badge/docker%20compose-%3E%3D%20v2.24.7-blue)](https://docs.docker.com/compose/)

A robust PACS identity and user access management API using Firebase with multi-tenancy support.

## Prerequisites

Before running this repository, ensure you have:

- [Docker](https://docs.docker.com/engine/install/) version >= v2.24.7 (check with `docker compose version`)
- [DockerHub](https://hub.docker.com/) account
- [Google Cloud Platform](https://cloud.google.com/) and [Firebase](https://firebase.google.com/) account (only one account is needed, they are linked)
- [Mailgun](https://www.mailgun.com/) account for email services
- Valid SSL certificate from hospital CA (for Nginx)
- Make utility (`sudo apt-get install make` on Ubuntu or `dnf install make` on CentOS/Rocky Linux/RHEL)

## Getting Started

### 1. Project Setup

1. Create and setup project directory:

   ```bash
   # Create project directory and clone repository
   mkdir pacs-ai
   cd pasc-ai
   git clone https://github.com/HeartWise-AI/pacs-ai-backend.git
   git clone https://github.com/HeartWise-AI/PACS-AI.git

   # Clone agent codebase
   cd pacs-ai-backend
   git clone https://github.com/HeartWise-AI/cardio-agent.git
   cd ..

   # Setup environment files
   cp pacs-ai-backend/.env.example pacs-ai-backend/.env
   cp PACS-AI/platform/app/.env.example PACS-AI/platform/app/.env
   cp pacs-ai-backend/api-pacs/.env.example pacs-ai-backend/api-pacs/.env
   cp pacs-ai-backend/orthanc/.env.example pacs-ai-backend/orthanc/.env
   cp pacs-ai-backend/nginx/.env.example pacs-ai-backend/nginx/.env
   cp pacs-ai-backend/cardio-agent/.env.example pacs-ai-backend/cardio-agent/.env
   cp pacs-ai backend/postgresql/.env.example pacs-ai backend/postgresql/.env
   cp pacs-ai backend/redis/.env.example pacs-ai backend/redis/.env
   ```

2. Create Docker network:
   ```bash
   docker network create pacs-net
   ```

### Environment Files

The root `.env` file is used by Docker Compose for shared values that apply across multiple services. Keep deployment-wide values here, such as:

```env
APP_TIMEZONE=America/Toronto
```

Service-specific `.env` files are loaded by their own compose files and should contain runtime settings and secrets for that service:

```text
api-pacs/.env
postgresql/.env
orthanc/.env
nginx/.env
```

For example, the ingestion runner and retrieval worker intervals belong in `api-pacs/.env`:

```env
INFERENCE_INGESTION_RUNNER_INTERVAL_MINUTES=1
INFERENCE_INGESTION_RETRIEVAL_WORKER_INTERVAL_MINUTES=1
```

If a variable appears in both a service `env_file` and an explicit compose `environment` block, the explicit `environment` value takes precedence.

### 2. External Services Setup

#### 2.1 Google Cloud Platform Setup

1. Create or sign in to your [Google Cloud Platform](https://cloud.google.com/) account
   > Note: You will need to have a billing account linked to your Google account, but you can use the free tier as everything we are using is free.
2. Navigate to the [Google Cloud Console](https://console.cloud.google.com/)
3. Create new project named `pacs-ai-prod`:
   <p align="center">
     <img src="docs/images/image.png" alt="Create new project">
   </p>
   <p align="center">
     <img src="docs/images/image-1.png" alt="Project creation">
   </p>
4. Enable Identity Platform:
   - In the console search bar, search for `Identity Platform` and enable it
   <p align="center">
     <img src="docs/images/image-2.png" alt="Enable Identity Platform">
   </p>
5. Configure Identity Platform:
   - Go to Settings → Security → Multi Tenancy and enable it
   - Go to Tenants and create a new tenant named `prod`
     > Note: A tenant ID will be automatically generated - you will need this ID for the Firebase setup
   - Go to Providers, select your tenant in the `Scope to a tenant` box and enable email/password provider:
     <p align="center">
       <img src="docs/images/image-3.png" alt="Enable provider">
     </p>

#### 2.2 Firebase Setup

1. Sign in to [Firebase Console](https://console.firebase.google.com)
   > Note: Use the same Google account you used for the Google Cloud Platform project
   - You should see a project named `pacs-ai-prod` already created with the same project ID
2. Configure Authentication:
   - Click on the project name to open the project dashboard
   - In the left sidebar, under Build, click on `Authentication`
   - Go to the `Sign-in method` tab and enable the `Email/Password` provider
   <p align="center">
     <img src="docs/images/image-4.png" alt="Authentication setup">
   </p>
3. Setup Firestore Database:

   - In the left sidebar, under Build, click on `Firestore Database`
   - Click on `Create database`
   - Do not change the `Database Id` and set the `Location` to `northamerica-northeast1 (Montréal)`
   <p align="center">
     <img src="docs/images/image-5.png" alt="Database location">
   </p>

   - Select `Start in production mode` and click on `Create`
   <p align="center">
     <img src="docs/images/image-6.png" alt="Production mode">
   </p>

   - Create a new collection named `tenants` with the following structure:
   <p align="center">
     <img src="docs/images/image-7.png" alt="Tenants collection">
   </p>

   ```
   Document ID: <Tenant_ID>  # This is the ID of the tenant you created in GCP
   Fields:
     - name: "PACS AI DEMO"  # This is a descriptive name for the tenant, it will be displayed in the PACS-AI web app
   ```

4. Configure Web Application:

   - Go to the project settings page
   <p align="center">
     <img src="docs/images/image-8.png" alt="Project settings">
   </p>

   - On the `General` tab, add a web app
   <p align="center">
     <img src="docs/images/image-9.png" alt="Add web app">
   </p>

   - Name it `pacs-ai-webapp`, register it and continue to console
   <p align="center">
     <img src="docs/images/image-10.png" alt="Web app registration">
   </p>

   > Note: You will be redirected to the web app dashboard where you can see the `Web SDK configuration` code. Copy these values to `PACS-AI/platform/app/.env`. See the [PACS-AI README](https://github.com/HeartWise-AI/PACS-AI/blob/master/platform/app/README.md) for more details.

5. Setup Service Account:
   - Still on the project settings page, click on `Service accounts` tab
   - Click on `Generate new private key`
   - Rename the downloaded file to `pacs-ai-firebase-admin.json`
   - Save it in `api-pacs/configs/firebase/pacs-ai-firebase-admin.json`
     > Note: This file contains the credentials for the GO API to access the Firebase database

#### 2.3 Mailgun Setup

1. Create a [Mailgun](https://www.mailgun.com/) account
2. Connect your domain to Mailgun
   > Note: This requires domain ownership and DNS configuration knowledge. Contact us if you need assistance with the setup!
3. Once connected, copy the API key to `pacs-ai-backend/nginx/.env`

### 3. Configuration for Production

#### API Configuration

Update `api-pacs/.env` with the following variables:

| Variable                    | Description                                                                         |
| --------------------------- | ----------------------------------------------------------------------------------- |
| `API_NAME`                  | Should be set to `api-pacs` (do not change)                                         |
| `API_URL_REST_PORT`         | Should be set to `8000` (do not change)                                             |
| `APP_URL`                   | Your domain URL (e.g., `https://MyDomain.com`)                                      |
| `DOCKER_USERNAME`           | Your DockerHub username                                                             |
| `DOCKER_PASSWORD`           | Your DockerHub password                                                             |
| `DOCKER_NETWORK`            | Should be set to `pacs-net`                                                         |
| 'DOCUSIGN_INTEGRATION_KEY'  | Docusign integration key                                                            |
| 'DOCUSIGN_USER_ID'          | Docusign user id                                                                    |
| 'DOCUSIGN_ACCOUNT_BASE_URI' | Docusign account base uri                                                           |
| 'DOCUSIGN_AUTH_SERVER'      | Docusign auth server                                                                |
| 'DOCUSIGN_PRIVATE_KEY'      | Docusign private key                                                                |
| 'DOCUSIGN_ACCOUNT_ID'       | Docusign account id                                                                 |
| `ELASTICSEARCH_URL`         | Should be set to `http://elasticsearch:9200`                                        |
| `FIREBASE_CONFIG_FILE_PATH` | Should be set to `/app/build/configs/firebase/pacs-ai-firebase-admin.json`          |
| `FIREBASE_PROJECT_ID`       | Your Firebase project ID (same as in `PACS-AI/platform/app/.env`)                   |
| `FIREBASE_SUPERUSER_KEY`    | Strong password for super user access (used for first user creation and API access) |
| `KIBANA_BASE_URL`           | Should be set to `http://kibana:5601`                                               |
| `MAILGUN_API_KEY`           | Your Mailgun API key                                                                |
| `MAILGUN_DOMAIN`            | Your Mailgun domain                                                                 |
| `MAILGUN_SENDER_EMAIL`      | Your sender email (e.g., `no-reply@MyDomain.com`)                                   |
| `OPENAPI_DOCS_PASSWORD`     | Strong password for API documentation access                                        |
| `ORTHANC_AET`               | Should be set to `PACS_AI`                                                          |
| `ORTHANC_BASE_URL`          | Should be set to `http://orthanc:8042` or correct port                              |
| `ORTHANC_LOCAL_CACHE_EXPIRATION_IN_HOURS`             | Should be set to `24` (default) or desired hour           |
| `INFERENCE_INGESTION_RUNNER_INTERVAL_MINUTES` | Ingestion scheduler interval in minutes. Defaults to `1` if missing or invalid |
| `INFERENCE_INGESTION_RETRIEVAL_WORKER_INTERVAL_MINUTES` | Ingestion retrieval worker interval in minutes. Defaults to `1` if missing or invalid |
| `POSTGRES_DB_HOST`          | Should be set to `postgresql`                                                       |
| `POSTGRES_DB_PORT`          | Should be set to `5432`                                                             |
| `POSTGRES_DB_DATABASE`      | Should be set to `db_pacs`                                                          |
| `POSTGRES_DB_USERNAME`      | Should be set to `pacs`                                                             |
| `POSTGRES_DB_PASSWORD`      | Should be set to `pacs.staging`                                                     |
| `REDIS_HOST`                | Should be set to `redis`                                                            |
| `REDIS_PORT`                | Should be set to `6379` (do not change)                                             |
| `REDIS_PASSWORD`            | Should be set to `pacs.staging` (requires update in `redis/redis.conf` if changed)  |
| `REDIS_IAM_DB`              | Should be set to `1` (do not change)                                                |

#### DICOM Configuration

Update `pacs-ai-backend/orthanc/.env` with appropriate port and AET settings:

- Consult with PACS admins for proper port configuration if you are unsure
- The AET (Application Entity Title) must be unique in your PACS network
- Change `APP_TIMEZONE` for each deployment region instead of modifying code. For example, use `America/Toronto` for Eastern time or `Asia/Dubai` for Abu Dhabi.

#### Network Configuration

1. Update `pacs-ai-backend/nginx/.env` with your domain:

   ```env
   SERVER_NAME=MyDomain.com  # or IP address
   ```

2. SSL Certificates:
   - Place valid SSL certificates in `pacs-ai-backend/nginx/ssl/`:
     - `nginx.crt`: Your SSL certificate
     - `nginx.key`: Your SSL private key
   - If no valid certificates are provided:
     - Self-signed certificates will be auto-generated
     - These will be invalid and not recognized by browsers
     - Users will need to ignore SSL certificate warnings

## Usage

### Launching the Application in Production

From the `pacs-ai-backend` directory:

```bash
make up-prod # This will start the application in production mode
make down-prod # This will stop the application
```

> Note: Initial startup may take several minutes while containers are being built and initialized. Subsequent restarts will be faster as containers are reused.

### Access Points

| Service           | URL                           |
| ----------------- | ----------------------------- |
| API Documentation | https://MyDomain.com/api/docs |

### 4. Configuration for Development

#### API Configuration

Update `api-pacs/.env` with the following variables:

| Variable                    | Description                                                                                                               |
| --------------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| `API_NAME`                  | Should be set to `api-pacs` (do not change)                                                                               |
| `API_URL_REST_PORT`         | Should be set to `8000` (do not change)                                                                                   |
| `APP_TIMEZONE`              | Deployment timezone for API logs, ingestion windows, and PostgreSQL sessions (default: `America/Toronto`)                |
| `APP_URL`                   | Should be set to `http://localhost:3000`                                                                                  |
| `CLOUDFLARE_SECRET_KEY`    | Your Cloudflare secret key                                                                                                 |
| `CLOUDFLARE_TURNSTILE_BASE_URL` | Should be set to `https://challenges.cloudflare.com/turnstile/v0`                                                     |
| `DOCKER_USERNAME`           | Your DockerHub username                                                                                                   |
| `DOCKER_PASSWORD`           | Your DockerHub password                                                                                                   |
| `DOCKER_NETWORK`            | Should be set to `pacs-net`                                                                                               |
| 'DOCUSIGN_INTEGRATION_KEY'  | Docusign integration key                                                                                                  |
| 'DOCUSIGN_USER_ID'          | Docusign user id                                                                                                          |
| 'DOCUSIGN_ACCOUNT_BASE_URI' | Docusign account base uri                                                                                                 |
| 'DOCUSIGN_AUTH_SERVER'      | Docusign auth server                                                                                                      |
| 'DOCUSIGN_PRIVATE_KEY'      | Docusign private key                                                                                                      |
| 'DOCUSIGN_ACCOUNT_ID'       | Docusign account id                                                                                                       |
| `ELASTICSEARCH_URL`         | Should be set to `http://localhost:9200`                                                                                  |
| `FIREBASE_CONFIG_FILE_PATH` | Should be set to `pacs-ai-backend/api-pacs/configs/firebase/pacs-ai-firebase-admin.json`, make sure it's the correct path |
| `FIREBASE_PROJECT_ID`       | Your Firebase project ID (same as in `PACS-AI/platform/app/.env`)                                                         |
| `FIREBASE_SUPERUSER_KEY`    | Strong password for super user access (used for first user creation and API access)                                       |
| `KIBANA_BASE_URL`           | Should be set to `http://localhost:5601`                                                                                  |
| `MAILCHIMP_API_KEY`         | Your Mailchimp API key                                                                                                    |
| `MAILCHIMP_BASE_URL`        | Should be set to `https://us14.api.mailchimp.com`                                                                         |
| `MAILCHIMP_LIST_ID`         | Your Mailchimp list ID                                                                                                    |
| `MAILGUN_API_KEY`           | Your Mailgun API key                                                                                                      |
| `MAILGUN_DOMAIN`            | Your Mailgun domain                                                                                                       |
| `MAILGUN_SENDER_EMAIL`      | Your sender email (e.g., `no-reply@MyDomain.com`)                                                                         |
| `OPENAPI_DOCS_PASSWORD`     | Strong password for API documentation access                                                                              |
| `ORTHANC_AET`               | Should be set to `PACS_AI`                                                                                                |
| `ORTHANC_BASE_URL`          | Should be set to `http://orthanc:8042` or correct port                                                                    |
| `ORTHANC_LOCAL_CACHE_EXPIRATION_IN_HOURS`             | Should be set to `24` (default) or desired hour                                                 |
| `POSTGRES_DB_HOST`          | Should be set to `postgresql`                                                                                             |
| `POSTGRES_DB_PORT`          | Should be set to `5432`                                                                                                   |
| `POSTGRES_DB_DATABASE`      | Should be set to `db_pacs`                                                                                                |
| `POSTGRES_DB_USERNAME`      | Should be set to `pacs`                                                                                                   |
| `POSTGRES_DB_PASSWORD`      | Should be set to `pacs.staging`                                                                                           |
| `REDIS_HOST`                | Should be set to `localhost`                                                                                              |
| `REDIS_PORT`                | Should be set to `6379` (do not change)                                                                                   |
| `REDIS_PASSWORD`            | Should be set to `pacs.staging` (requires update in `redis/redis.conf` if changed)                                        |
| `REDIS_IAM_DB`              | Should be set to `1` (do not change)                                                                                      |

#### Database Migrations

##### Database Migrations using `psql` (no 3rd party required)

```bash
docker exec -i postgresql psql -U ${POSTGRES_DB_USERNAME} -d ${POSTGRES_DB_DATABASE} < /path/to/repo/pacs-ai-backend/api-pacs/infrastructures/database/postgresql/migrations/000001_create_inference_ingestion_jobs_schema.up.sql
docker exec -i postgresql psql -U ${POSTGRES_DB_USERNAME} -d ${POSTGRES_DB_DATABASE} < /path/to/repo/pacs-ai-backend/api-pacs/infrastructures/database/postgresql/migrations/000002_create_inference_ingestion_candidates_schema.up.sql
docker exec -i postgresql psql -U ${POSTGRES_DB_USERNAME} -d ${POSTGRES_DB_DATABASE} < /path/to/repo/pacs-ai-backend/api-pacs/infrastructures/database/postgresql/migrations/000003_create_inference_ingestion_processing_jobs_schema.up.sql
```

Then verify the tables were created:

```bash
docker exec postgresql psql -U ${POSTGRES_DB_USERNAME} -d ${POSTGRES_DB_DATABASE} -c '\dt'
```

Expected tables:

- `ingestion_jobs`
- `ingestion_candidates`
- `ingestion_processing_jobs`

##### Database Migrations using Go / `migrate`

Go database migration instructions are documented in [postgresql/README.md](/home/pacs-ai/pacs-ai-backend/postgresql/README.md).

#### DICOM Configuration

No DICOM configuration is needed for local development, PACS-AI will use emulated DICOM services.

#### Network Configuration

No network configuration is needed for local development

## Usage

### Launching the Application in Production

From the `pacs-ai-backend` directory:

```bash
make up # This will start the application in local mode
make down # This will stop the application
```

## Recipes

### Generating tokens for `api-pacs` ↔ `study-service` auth

The Go backend (`api-pacs`) and the Python `study-service` now use three separate bearer tokens:

- `STUDY_SERVICE_INGEST_TOKEN` — Go signs outbound `POST /ingest/study` calls; `study-service` verifies.
- `STUDY_SERVICE_OPERATOR_TOKEN` — PACS-AI signs study-service job/control-plane reads; `study-service` verifies.
- `STUDY_SERVICE_CALLBACK_TOKEN` — `study-service` signs outbound processing callbacks; Go verifies.

Generate one random value per token:

```bash
openssl rand -hex 32   # → STUDY_SERVICE_INGEST_TOKEN
openssl rand -hex 32   # → STUDY_SERVICE_OPERATOR_TOKEN
openssl rand -hex 32   # → STUDY_SERVICE_CALLBACK_TOKEN
```

Copy them as:

- first generated value -> `STUDY_SERVICE_INGEST_TOKEN`
- second generated value -> `STUDY_SERVICE_OPERATOR_TOKEN`
- third generated value -> `STUDY_SERVICE_CALLBACK_TOKEN`

Set all three values in both services' `.env` files. PACS-AI needs them to sign ingest requests, reconciliation/job API reads, and verify callbacks. Study-service needs them to verify ingest/job API requests and sign callbacks.

```env
# api-pacs/.env
STUDY_SERVICE_INGEST_TOKEN=<first hex string>
STUDY_SERVICE_OPERATOR_TOKEN=<second hex string>
STUDY_SERVICE_CALLBACK_TOKEN=<third hex string>
```

```env
# cardio-agent/study-service/.env
STUDY_SERVICE_INGEST_TOKEN=<first hex string>
STUDY_SERVICE_OPERATOR_TOKEN=<second hex string>
STUDY_SERVICE_CALLBACK_TOKEN=<third hex string>
```

Recommended use:

- `STUDY_SERVICE_INGEST_TOKEN` is only for PACS-AI -> study-service ingest dispatch.
- `STUDY_SERVICE_OPERATOR_TOKEN` is only for study-service job/control routes such as `/jobs`, `/settings`, `/health`, `/health/detailed`, `/metrics`, and `/jobs/stream`.
- `STUDY_SERVICE_CALLBACK_TOKEN` is only for study-service -> PACS-AI callbacks.

> Use a different set per environment (dev / staging / prod), never commit the populated `.env` files, and rotate by restarting both services together.

### Ingestion Defaults

`api-pacs/.env` can provide a default recent search window for ingestion jobs that do not set `recent_window_minutes` explicitly:

```env
INFERENCE_INGESTION_DEFAULT_RECENT_WINDOW_MINUTES=240
```

Per-job `recent_window_minutes` still overrides this value when present.

## Adding an inference model

Each model is a self-contained FastAPI container under [`model-examples/`](model-examples/). To add,
port, or debug one, see **[`model-examples/README.md`](model-examples/README.md)** — it covers the file
layout, the `config.json` / `class_mapping.json` / `model_info.json` schemas, the zero-padding attention-mask
contract (PR #242), the `supportedAdditionalMetadata` → Step-2 variables page, HuggingFace-gated checkpoint
wiring, and how to reproduce a deployed prediction locally.

AI coding agents: the same guidance is available machine-readable at
[`.claude/skills/pacs-ai-model-mapping/SKILL.md`](.claude/skills/pacs-ai-model-mapping/SKILL.md) (Claude Code)
and summarized in [`AGENTS.md`](AGENTS.md) (Codex/others).

## Support

Maintained with ❤️ by [Nuxify](https://nuxify.tech) and [HeartWise AI](https://heartwise.ai)
