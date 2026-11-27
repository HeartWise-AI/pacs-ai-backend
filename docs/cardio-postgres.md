# cardio-postgres (Cardio Agent database)

`cardio-postgres` (service `postgres` in `docker-compose.yml`, Postgres 16, database
`cardio_agent`, role `cardio`) holds study-service runs (`pipeline_jobs`, run labels, study
media) and the medical-agent chat (`conversations`, `messages`, feedback, skills). Both apps
create and upgrade their own schema with Alembic on startup (study-service: `alembic_version`;
medical-agent: `backend_alembic_version`) and the medical-agent seeds its shared skills, so an
empty database only needs the services started: postgres first, then study-service, its
workers and medical-agent.

## Rules

- **Never share a Postgres data directory between hosts, and never point two Postgres servers
  at the same directory** (or at a copy that is still being written). Postgres assumes it is
  the only writer; a second postmaster on the same files corrupts the system catalog. On
  2026-11-27 this destroyed the MHI database: its data directory was on the NAS
  (`/mnt/data2/cardio-agent/shared/postgres`) and another host started a Postgres on it while
  MHI's was stopped. There was no snapshot, so the data was lost.
- The data directory lives on **local disk**: the named Docker volume `cardio_postgres_data`
  (under `/var/lib/docker`). `CARDIO_POSTGRES_VOLUME` in `.env` can pin the name of an
  existing local volume (MHI uses `cardio-postgres-data`); do not change it on a running
  deployment unless you mean to start from an empty database. Before deploying a compose
  change, compare `docker compose config | grep -A2 '^volumes'` with `docker volume ls`: a
  different name means Compose would create a new, empty volume. (`docker-compose-dev.yml`
  keeps its own unnamed dev volume.)
- The container refuses to start when its data directory is on NFS, CIFS/SMB or CephFS
  (`REFUSING to start: PGDATA is on nfs`, exit 78), or when the filesystem cannot be checked.
- Other hosts that need the data connect over the network (published port 5434, limited by
  `pg_hba.conf` and the firewall); they never mount the files.

## Backups

The `cardio-postgres-backup` service (image `postgres:16`, script
`scripts/ops/cardio-postgres-backup.sh`) writes a `pg_dump -Fc` of `cardio_agent` every day at
`CARDIO_DB_BACKUP_AT` (default 02:30, local time) to `CARDIO_DB_BACKUP_DIR` (default
`./backups/cardio-postgres`; MHI: `/mnt/data2/cardio-agent/backups/mhi-cardio-postgres`),
checks the dump with `pg_restore -l`, and deletes dumps older than
`CARDIO_DB_BACKUP_KEEP_DAYS` (default 14). A dump file on NFS is fine. The file mode is 600
(the dump contains clinical data).

```bash
docker exec cardio-postgres-backup /bin/sh /backup.sh once     # back up now
docker logs cardio-postgres-backup                              # OK/FAILED lines
```

### Restore test (throwaway container, no network)

```bash
docker run -d --name cardio-restore-test --network none \
  -e POSTGRES_USER=cardio -e POSTGRES_PASSWORD=throwaway -e POSTGRES_DB=cardio_agent \
  -v "$CARDIO_DB_BACKUP_DIR":/backups:ro postgres:16
docker exec cardio-restore-test sh -c 'pg_restore -U cardio -d cardio_agent --no-owner --exit-on-error "$(ls -1t /backups/cardio_agent_*.dump | head -1)"'
docker exec cardio-restore-test psql -U cardio -d cardio_agent -c 'select count(*) from pipeline_jobs'
docker rm -f cardio-restore-test
```

### Restore into production

Stop the writers (study-service, every `study-celery-worker*`, medical-agent), then
`pg_restore -U cardio -d cardio_agent --clean --if-exists --no-owner <dump>` inside
`cardio-postgres`, and start the services again (postgres, study-service, workers,
medical-agent).
