#!/bin/sh
# Nightly logical backup of the cardio_agent database (cardio-postgres).
#
# Runs inside the postgres:16 image as the `cardio-postgres-backup` compose service.
# Every day at BACKUP_AT (local time, TZ) it writes
#   $BACKUP_DIR/cardio_agent_YYYYmmdd_HHMMSS.dump   (pg_dump -Fc, custom format)
# checks that pg_restore can read it, and deletes dumps older than BACKUP_KEEP_DAYS days.
# A dump file on NFS is fine; a live Postgres data directory on NFS is NOT (see
# docs/cardio-postgres.md).
#
#   cardio-postgres-backup.sh loop   # default: wait for BACKUP_AT, back up, repeat
#   cardio-postgres-backup.sh once   # back up now (e.g. docker exec cardio-postgres-backup /backup.sh once)
#
# Connection: libpq env (PGHOST, PGPORT, PGUSER, PGPASSWORD, PGDATABASE).
set -eu

BACKUP_DIR=${BACKUP_DIR:-/backups}
BACKUP_KEEP_DAYS=${BACKUP_KEEP_DAYS:-14}
BACKUP_AT=${BACKUP_AT:-02:30}
PGDATABASE=${PGDATABASE:-cardio_agent}
export PGDATABASE

log() { echo "$(date '+%Y-%m-%d %H:%M:%S %Z') cardio-postgres-backup: $*"; }

backup_once() {
  umask 077
  mkdir -p "$BACKUP_DIR"
  ts=$(date +%Y%m%d_%H%M%S)
  out="$BACKUP_DIR/${PGDATABASE}_${ts}.dump"
  tmp="$BACKUP_DIR/.${PGDATABASE}_${ts}.dump.partial"
  if ! pg_dump -Fc -Z 6 -f "$tmp"; then
    rm -f "$tmp"; log "FAILED: pg_dump of $PGDATABASE"; return 1
  fi
  if ! pg_restore -l "$tmp" >/dev/null; then
    rm -f "$tmp"; log "FAILED: pg_restore cannot read the dump"; return 1
  fi
  mv "$tmp" "$out"
  log "OK $out ($(du -h "$out" | cut -f1))"
  date +%s > "$BACKUP_DIR/.last_success"
  # Keep BACKUP_KEEP_DAYS days of dumps (find -mtime +N = older than N+1 days).
  find "$BACKUP_DIR" -maxdepth 1 -type f -name "${PGDATABASE}_*.dump" -mtime +"$((BACKUP_KEEP_DAYS - 1))" -print -delete \
    | while read -r f; do log "pruned $f"; done
  find "$BACKUP_DIR" -maxdepth 1 -type f -name ".${PGDATABASE}_*.dump.partial" -mmin +720 -delete
}

seconds_until() {
  now=$(date +%s)
  target=$(date -d "today $1" +%s)
  [ "$target" -le "$now" ] && target=$(date -d "tomorrow $1" +%s)
  echo $((target - now))
}

case "${1:-loop}" in
  once) backup_once ;;
  loop)
    log "scheduled daily at $BACKUP_AT ($(date +%Z)), keep $BACKUP_KEEP_DAYS days, dir $BACKUP_DIR"
    while :; do
      wait_s=$(seconds_until "$BACKUP_AT")
      log "next backup in ${wait_s}s"
      sleep "$wait_s"
      backup_once || log "backup failed; next attempt at the next scheduled time"
      sleep 61
    done ;;
  *) echo "usage: $0 [loop|once]" >&2; exit 2 ;;
esac
