# CardioAgent on another host (wise-training): access to PACS-AI results

Status: live on MHI since 2026-11-27. Decision by Robert, 2026-11-27, after the
cardio-postgres incident (see "Incident lesson" below).

## Architecture

```
                 wise-training (10.128.228.32)                      MHI (10.128.228.213)
 ┌──────────────────────────────────────────────┐      ┌───────────────────────────────────────────────┐
 │ CardioAgent UI                               │      │ nginx :8443 (own cert)  /study-service/        │
 │ medical-agent (chat backend) ── HTTPS GET ───┼─────▶│   allow 10.128.228.32, GET/HEAD only           │
 │ medical-agent-discovery                      │      │        │ X-Real-IP                             │
 │   │                                          │      │        ▼                                       │
 │   ▼                                          │      │ study-service :8600  (remote token: read+run,  │
 │ its OWN Postgres on local disk               │      │   pinned tenant, read routes only)             │
 │ (chat, skills, discovery)                    │      │        │                                       │
 └──────────────────────────────────────────────┘      │        ▼                                       │
                                                       │ cardio-postgres (local volume) ← workers,     │
                                                       │ api-pacs, MHI chat — the ONLY writers          │
                                                       └───────────────────────────────────────────────┘
```

Rules:

1. **MHI is the only writer of PACS-AI inference data.** Jobs, results,
   labels, media and preprocessing rows are written only by MHI's
   study-service and workers, and runs are started only by MHI (the gateway,
   ingestion, MHI's chat, or a lookup with the remote token's `run` scope that
   MHI's study-service itself enqueues).
2. **wise-training reads through the study-service HTTP API**, relayed by
   MHI's nginx, with a scoped remote token. CardioAgent needs no code change:
   it sets `STUDY_SERVICE_URL` and `STUDY_SERVICE_OPERATOR_TOKEN` (the name of
   the variable its `StudyServiceClient` sends as the bearer).
3. **wise-training's chat backend (medical-agent) and medical-agent-discovery
   use their OWN Postgres on wise-training's local disk.** Never NFS, never a
   shared folder, never MHI's cardio-postgres.
4. **No study-service, Celery workers or migrations on wise-training against
   MHI's database.** wise-training's study-service and workers are retired.
5. No direct database access from wise-training to MHI. The `cardio` role's
   pg_hba rule for 10.128.228.32 and the 5434 allowlist are leftovers to remove
   (see "Follow-ups"). A read-only DB role (`wise_reader`) was considered and
   dropped: the HTTP route covers every read CardioAgent does, with one
   authorization model (tenant, routes, scopes) instead of two.

## The route

| | |
|---|---|
| URL (`STUDY_SERVICE_URL`) | `https://sgbmavram01.icm.local:8443/study-service` (the certificate also names `sgbmavram01` and `10.128.228.213`) |
| TLS | nginx's 8443 listener uses its own self-signed certificate (CN/SAN `sgbmavram01.icm.local`, `sgbmavram01`, IP `10.128.228.213`, valid to 2036-11-16), the same one cardio-postgres uses. It does not depend on the browser certificate on 443 (expired, PACS-437). Public cert: `secrets/cardio-postgres-tls/server.crt` on MHI. |
| nginx gate | `NGINX_STUDY_SERVICE_ALLOWED_IP=10.128.228.32` (nginx/.env); GET/HEAD only (`limit_except GET`); `X-Real-IP` set from the socket address; `/study-service` stripped. The same location also exists on 443 for when the browser certificate is renewed. Default `127.0.0.1` = closed. |
| study-service gate | `STUDY_SERVICE_REMOTE_*` in `cardio-agent/study-service/.env`: token SHA-256, tenant `mhi1-ygu02`, scopes `read,run`, allowed CIDR `10.128.228.32/32`, trusted proxy `172.20.0.0/16` (pacs-net). |
| Routes | the GET read routes of CardioAgent's `StudyServiceClient` (cardio-agent `study-service/docs/remote-token.md`); everything else 403. |
| Running a model | `GET /jobs/by-study/{uid}?modality=…&model_name=…&enqueue_if_missing=true` — what CardioAgent's chat already calls when it may run a model. Needs scope `run` (granted). The run is enqueued by MHI's study-service on MHI's workers and appears in the viewer's AI panel (latest results). |
| Tenant | always `mhi1-ygu02`, whatever `X-Tenant-ID` the caller sends. |
| Audit | study-service logs `[audit] event=remote_token_request decision=… principal=svc:wise-training client_ip=… method=… route=<template>`; nginx logs the request (UIDs redacted). |

Why not the gateway (api-pacs)? CardioAgent has no code for gateway run/read
routes; it already speaks the study-service API. A gateway service token would
be new code on both sides for the same result. If CardioAgent later needs
runs that show in the gateway's processing history (AI Predictions page,
quotas, run labels), add a scoped gateway token then.

## Connection settings for wise-training (placeholders)

```
# backend/.env (chat backend) and the discovery service
DATABASE_URL=postgresql://<local_user>:<local_password>@<local-postgres-host>:5432/<local_db>   # wise-training's own DB
STUDY_SERVICE_URL=https://sgbmavram01.icm.local:8443/study-service
STUDY_SERVICE_OPERATOR_TOKEN=<remote token, pst_…>
SSL_CERT_FILE=/path/to/ca-bundle-with-mhi.pem   # certifi bundle + MHI's server.crt (httpx honours it)
```

Build the bundle once: `cat "$(python -m certifi)" mhi-cardio-tls.crt > ca-bundle-with-mhi.pem`.
Replacing the bundle with MHI's certificate alone breaks every other HTTPS
call (LLM APIs, Firebase). With SICMCA added too, the same bundle trusts
the 443 certificate once it is renewed.

## Rotation

Token (study-service remote token):

1. `token="pst_$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=')"`; `printf '%s' "$token" | sha256sum`.
2. Append the new hash to `STUDY_SERVICE_REMOTE_TOKEN_SHA256` (comma) in
   `cardio-agent/study-service/.env` (back it up first), then
   `docker compose up -d --no-deps --no-build study-service` (API container only).
3. Hand the new token over (below); wise-training updates
   `STUDY_SERVICE_OPERATOR_TOKEN` and restarts its backend.
4. Remove the old hash, recreate study-service again. Revocation = step 4 alone.

Certificate (8443): replace `server.crt`/`server.key` in
`secrets/cardio-postgres-tls` (shared with cardio-postgres), recreate nginx and
cardio-postgres, and give wise-training the new public certificate.

Hand-over: Robert drops the token in `/mnt/data2/cardio-agent/shared/handoff/`
as a mode-600 file (as for CA-268); wise-training reads it into its `.env`
and deletes the file. MHI keeps a private copy in
`~/.config/pacs-ai/mhi-wise-token.env` (600).

## Version rule

- wise-training never runs migrations against MHI (it has no MHI database
  access at all now). MHI's study-service owns its schema.
- The remote API is the study-service API of MHI's deployed revision. A
  CardioAgent backend on wise-training should not be **newer** than MHI's
  study-service in what it expects from that API; deploy study-service on MHI
  first when a CardioAgent change needs a new study-service field or route.
- wise-training's own chat database follows wise-training's medical-agent
  revision only.

## Incident lesson (2026-11-27)

MHI's cardio-postgres data directory sat on a shared NFS folder. While MHI's
Postgres was stopped for a Docker upgrade, a second Postgres (wise-training)
started on the same directory; the catalog was corrupted and the data lost.
**Never share a Postgres data directory between hosts or containers, and never
put a live data directory on NFS.** MHI's cardio-postgres now lives on a local
Docker volume and refuses to start on a network filesystem (docs/cardio-postgres.md).
Each host has its own database; data crosses hosts only through an API.

## Follow-ups

- Remove `host all cardio 10.128.228.32/32` from cardio-postgres's pg_hba and
  the DOCKER-USER 5434 allowlist, then publish 5434 on 127.0.0.1 only (master
  compose already does): wise-training no longer connects.
- Browser certificate on 443 (PACS-437).
