# CardioAgent on another host: access to PACS-AI results

Status: in use since 2026-11-27 for the CardioAgent server ("remote host") that reads
a PACS-AI host's results, decided after the cardio-postgres incident (see
"Incident lesson"). Site-specific values (addresses, host names, tenant) are kept
out of this public repository; the operators' hand-off notes hold them.

## Architecture

```
                 remote host (CardioAgent)                          PACS-AI host
 ┌──────────────────────────────────────────────┐      ┌───────────────────────────────────────────────┐
 │ CardioAgent UI                               │      │ nginx :8443 (own cert)  /study-service/        │
 │ medical-agent (chat backend) ── HTTPS GET ───┼─────▶│   allow <remote IP>, GET/HEAD only             │
 │ medical-agent-discovery                      │      │        │ X-Real-IP                             │
 │   │                                          │      │        ▼                                       │
 │   ▼                                          │      │ study-service :8600  (remote token: read+run,  │
 │ its OWN Postgres on local disk               │      │   pinned tenant, read routes only)             │
 │ (chat, skills, discovery)                    │      │        │                                       │
 └──────────────────────────────────────────────┘      │        ▼                                       │
                                                       │ cardio-postgres (local volume) ← workers,     │
                                                       │ api-pacs, local chat — the ONLY writers        │
                                                       └───────────────────────────────────────────────┘
```

Rules:

1. **The PACS-AI host is the only writer of PACS-AI inference data.** Jobs,
   results, labels, media and preprocessing rows are written only by its
   study-service and workers, and runs are started only there (the gateway,
   ingestion, its chat, or a lookup with the remote token's `run` scope that
   its study-service itself enqueues).
2. **The remote host reads through the study-service HTTP API**, relayed by
   the PACS-AI nginx, with a scoped remote token. CardioAgent needs no code change:
   it sets `STUDY_SERVICE_URL` and `STUDY_SERVICE_OPERATOR_TOKEN` (the name of
   the variable its `StudyServiceClient` sends as the bearer).
3. **The remote host's chat backend (medical-agent) and medical-agent-discovery
   use their OWN Postgres on the remote host's local disk.** Never NFS, never a
   shared folder, never the PACS-AI host's cardio-postgres.
4. **No study-service, Celery workers or migrations on the remote host against
   the PACS-AI database.** The remote host runs no study-service or workers.
5. No direct database access from the remote host to the PACS-AI database
   (publish cardio-postgres on 127.0.0.1 only; see "Follow-ups"). A read-only DB role (`wise_reader`) was considered and
   dropped: the HTTP route covers every read CardioAgent does, with one
   authorization model (tenant, routes, scopes) instead of two.

## The route

| | |
|---|---|
| URL (`STUDY_SERVICE_URL`) | `https://<PACS-AI host>:8443/study-service` (a name the 8443 certificate covers) |
| TLS | nginx's 8443 listener uses its own certificate from `NGINX_REMOTE_TLS_DIR` (`server.crt`/`server.key`), e.g. the same self-signed pair as cardio-postgres's opt-in TLS (`secrets/cardio-postgres-tls`). It does not depend on the browser certificate on 443. The remote host trusts that certificate explicitly. |
| nginx gate | `NGINX_STUDY_SERVICE_ALLOWED_IP=<remote IP>` (nginx/.env); GET/HEAD only (`limit_except GET`); `X-Real-IP` set from the socket address; `/study-service` stripped. The same location also exists on 443 for when the browser certificate is renewed. Default `127.0.0.1` = closed. |
| study-service gate | `STUDY_SERVICE_REMOTE_*` in `cardio-agent/study-service/.env`: token SHA-256, tenant, scopes `read,run`, allowed CIDR `<remote IP>/32`, trusted proxy = the Docker network nginx is on. |
| Routes | the GET read routes of CardioAgent's `StudyServiceClient` (cardio-agent `study-service/docs/remote-token.md`); everything else 403. |
| Running a model | `GET /jobs/by-study/{uid}?modality=…&model_name=…&enqueue_if_missing=true` — what CardioAgent's chat already calls when it may run a model. Needs scope `run`, and only for a study the tenant already has jobs for. The run is enqueued by the PACS-AI host's study-service on its workers and appears in the viewer's AI panel (latest results). |
| Tenant | always `STUDY_SERVICE_REMOTE_TENANT_ID`, whatever `X-Tenant-ID` the caller sends. |
| Audit | study-service logs `[audit] event=remote_token_request decision=… principal=svc:<name> client_ip=… method=… route=<template>`; nginx logs the request (UIDs redacted). |

Why not the gateway (api-pacs)? CardioAgent has no code for gateway run/read
routes; it already speaks the study-service API. A gateway service token would
be new code on both sides for the same result. If CardioAgent later needs
runs that show in the gateway's processing history (AI Predictions page,
quotas, run labels), add a scoped gateway token then.

## Connection settings on the remote host (placeholders)

```
# backend/.env (chat backend) and the discovery service
DATABASE_URL=postgresql://<local_user>:<local_password>@<local-postgres-host>:5432/<local_db>   # its own DB
STUDY_SERVICE_URL=https://<PACS-AI host>:8443/study-service
STUDY_SERVICE_OPERATOR_TOKEN=<remote token, pst_…>
SSL_CERT_FILE=/path/to/ca-bundle-with-pacs-ai.pem   # certifi bundle + the 8443 server.crt (httpx honours it)
```

Build the bundle once: `cat "$(python -m certifi)" pacs-ai-8443.crt > ca-bundle-with-pacs-ai.pem`.
Replacing the bundle with that certificate alone breaks every other HTTPS
call (LLM APIs, Firebase). Add the hospital CA too and the same bundle trusts
the browser certificate on 443.

## Rotation

Token (study-service remote token):

1. `token="pst_$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=')"`; `printf '%s' "$token" | sha256sum`.
2. Append the new hash to `STUDY_SERVICE_REMOTE_TOKEN_SHA256` (comma) in
   `cardio-agent/study-service/.env` (back it up first), then
   `docker compose up -d --no-deps --no-build study-service` (API container only).
3. Hand the new token over (below); the remote host updates
   `STUDY_SERVICE_OPERATOR_TOKEN` and restarts its backend.
4. Remove the old hash, recreate study-service again. Revocation = step 4 alone.

Certificate (8443): replace `server.crt`/`server.key` in
`NGINX_REMOTE_TLS_DIR` (shared with cardio-postgres if it is the same pair),
recreate nginx (and cardio-postgres), and give the remote host the new public
certificate.

Hand-over: an operator puts the token in a mode-600 file in the agreed hand-off
folder (as for the CA-268 sign-in token); the remote host copies it into its
`.env` and deletes the file. The PACS-AI operator keeps a private mode-600 copy.

## Version rule

- The remote host never runs migrations against the PACS-AI database (it has no
  access to it). The PACS-AI study-service owns its schema.
- The remote API is the study-service API of the PACS-AI host's deployed revision. A
  CardioAgent backend on the remote host should not be **newer** than the
  PACS-AI study-service in what it expects from that API; deploy study-service
  on the PACS-AI host first when a CardioAgent change needs a new field or route.
- The remote host's own chat database follows its medical-agent revision only.

## Incident lesson (2026-11-27)

A production cardio-postgres data directory sat on a shared NFS folder. While
that Postgres was stopped for a Docker upgrade, a second Postgres on the remote
host started on the same directory; the catalog was corrupted and the data lost.
**Never share a Postgres data directory between hosts or containers, and never
put a live data directory on NFS.** cardio-postgres now lives on a local
Docker volume and refuses to start on a network filesystem (docs/cardio-postgres.md).
Each host has its own database; data crosses hosts only through an API.

## Follow-ups

- Remove any pg_hba rule or firewall allowance that still lets the remote host
  reach cardio-postgres, and publish it on 127.0.0.1 only (master compose does).
- Renew the browser certificate on 443 if it has expired.
