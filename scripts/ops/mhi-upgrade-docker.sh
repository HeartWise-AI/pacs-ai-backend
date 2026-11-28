#!/usr/bin/env bash
# Upgrade Docker Engine on MHI (sgbmavram01) to the latest release of a major version (default 28; --major 29),
# at least 28.3.3 (fixes moby#49325),
# upgrade nvidia-container-toolkit, and install NVIDIA's fix so a systemd reload no longer takes the GPUs
# away from running containers. Downtime: all containers stop and start again (about 5-10 min).
#
# Usage (as root):  sudo bash upgrade-docker.sh            # preflight + upgrade + restart + checks
#                   sudo bash upgrade-docker.sh --check    # preflight only, changes nothing
#                   sudo bash upgrade-docker.sh --force    # skip the "no running jobs" check
#                   sudo bash upgrade-docker.sh --major 29 [--check]
# Log and snapshots: $UPGRADE_LOG_DIR (default /root/docker-upgrade)/run-<timestamp>/ (MHI 2026-11-27: /home/ravram/docker-upgrade-20261127)
set -euo pipefail

MODE=upgrade; FORCE=0; MAJOR=28
while [ $# -gt 0 ]; do case "$1" in --check) MODE=check;; --force) FORCE=1;; --major) MAJOR=$2; shift;; *) echo "unknown arg $1"; exit 2;; esac; shift; done
[ "$(id -u)" = 0 ] || { echo "Run as root: sudo bash $0"; exit 1; }

BASE=${UPGRADE_LOG_DIR:-/root/docker-upgrade}
RUN=$BASE/run-$(date +%Y%m%d-%H%M%S); mkdir -p "$RUN"
exec > >(tee -a "$RUN/log.txt") 2>&1
say() { printf '\n=== %s  %s\n' "$(date +%H:%M:%S)" "$*"; }
die() { echo "ABORT: $*"; exit 1; }

MIN_DOCKER=28.3.3
HOST_IP=10.128.228.213
GUARD=/usr/local/sbin/docker-loopback-guard.sh
GPU_MODELS="DeepECG-CLIP-DINOv3 deepcoro-scad DeepCORO-CTO deepcoro-clip-generic CathEF-CLIP panecho deepcoro-jepa TotalSegmentator angiogram-procedure-view-classifier DeepRV-CLIP DeepCORO-SYNTAX DeepCORO-MACE"

# ---------------------------------------------------------------- 0. preflight
say "Preflight"
docker info >/dev/null || die "docker is not answering"
echo "Docker now: $(docker version --format '{{.Server.Version}}')"
rpm -qa | grep -E '^(docker|containerd|nvidia-container)' | sort | tee "$RUN/packages-before.txt"
avail=$(df -BG --output=avail /var/lib/docker | tail -1 | tr -dc 0-9); echo "Free disk: ${avail}G"
[ "$avail" -gt 20 ] || die "less than 20G free"

dnf -q makecache --repo docker-ce-stable --repo nvidia-container-toolkit >/dev/null
TARGET=$(dnf -q list --showduplicates docker-ce --repo docker-ce-stable 2>/dev/null \
  | awk '$1 ~ /^docker-ce/ {print $2}' | sed 's/^[0-9]*://; s/-.*//' | grep "^${MAJOR}\." | sort -V | tail -1)
[ -n "$TARGET" ] || die "no docker-ce ${MAJOR}.x in the repo"
printf '%s\n%s\n' "$MIN_DOCKER" "$TARGET" | sort -V -C || die "repo's newest 28.x ($TARGET) is older than $MIN_DOCKER"
echo "Target Docker: $TARGET (major $MAJOR; storage pinned to overlay2, so existing images and containers are kept)"
CUR=$(docker version --format '{{.Server.Version}}')
[ "$CUR" = "$TARGET" ] && [ "$MODE" != check ] && echo "Already on $TARGET: only re-applying config and checks."

# Active inference jobs (study-service) - don't cut a running job.
active=$(echo "select count(*) from pipeline_jobs where status in ('running','pending','queued','started') and created_at > now() - interval '6 hours';" \
  | docker exec -i cardio-postgres sh -c 'psql -U "$POSTGRES_USER" -d cardio_agent -At' 2>/dev/null || echo "?")
echo "Active pipeline jobs: $active"
if [ "$active" != 0 ] && [ "$FORCE" = 0 ]; then die "jobs are running ($active). Wait, or rerun with --force"; fi

# Snapshot of what is running, so everything comes back exactly as before.
docker ps --format '{{.Names}}' | sort > "$RUN/running-before.txt"
docker ps --format '{{.Names}}\t{{.Image}}\t{{.Ports}}' | sort > "$RUN/ps-before.tsv"
cp -p /etc/docker/daemon.json "$RUN/daemon.json.bak"
iptables-save > "$RUN/iptables-before.txt"
echo "$(wc -l < "$RUN/running-before.txt") containers running; snapshot in $RUN"
[ "$MODE" = check ] && { say "Check only: nothing changed. Run without --check to upgrade."; exit 0; }

# ---------------------------------------------------------------- 1. stop the PACS-AI ingestion cleanly
say "Stopping containers (graceful, 60 s each)"
# Workers first so no new job starts, then everything else.
docker ps --format '{{.Names}}' | grep -E '^study-' | xargs -r docker stop -t 60 >/dev/null || true
docker ps -q | xargs -r docker stop -t 60 >/dev/null || true

# ---------------------------------------------------------------- 2. packages
say "Installing docker-ce $TARGET + latest containerd, compose, buildx, nvidia-container-toolkit"
dnf -y install --repo docker-ce-stable --repo nvidia-container-toolkit --allowerasing \
  "docker-ce-${TARGET}" "docker-ce-cli-${TARGET}" "docker-ce-rootless-extras-${TARGET}" \
  containerd.io docker-buildx-plugin docker-compose-plugin \
  nvidia-container-toolkit nvidia-container-toolkit-base
rpm -qa | grep -E '^(docker|containerd|nvidia-container)' | sort | tee "$RUN/packages-after.txt"

# ---------------------------------------------------------------- 2b. host hardening (Docker is stopped, so a firewall reload is safe here)
say "Host hardening: rpcbind off (NFS mounts are v4.2), cockpit not reachable from the network"
mount -t nfs,nfs4 | grep -q 'vers=[23][,)]' && echo "NFSv3 mount found: leaving rpcbind on" \
  || { systemctl disable --now rpcbind.socket rpcbind.service 2>&1 | tail -2 || true; }
if firewall-cmd --permanent --zone=public --query-service=cockpit >/dev/null 2>&1; then
  firewall-cmd --permanent --zone=public --remove-service=cockpit
  firewall-cmd --reload
  echo "cockpit removed from the public zone (use: ssh -L 9090:localhost:9090 sgbmavram01)"
fi

# ---------------------------------------------------------------- 3. NVIDIA runtime + GPU-loss fix
say "NVIDIA runtime config and /dev/char symlinks (stops daemon-reload from revoking GPU access)"
nvidia-ctk runtime configure --runtime=docker
# Log rotation for containers created from now on (existing containers keep their old setting until recreated).
python3 - <<'PY'
import json
p = '/etc/docker/daemon.json'
d = json.load(open(p))
d.setdefault('log-driver', 'json-file')
d.setdefault('log-opts', {}).update({'max-size': '50m', 'max-file': '5'})
# Docker 29 defaults new installs to the containerd image store; keep overlay2 so no image or container is lost.
d['storage-driver'] = 'overlay2'
d.setdefault('features', {})['containerd-snapshotter'] = False
json.dump(d, open(p, 'w'), indent=4)
PY
diff "$RUN/daemon.json.bak" /etc/docker/daemon.json || true
nvidia-ctk system create-dev-char-symlinks --create-all
cat > /etc/udev/rules.d/71-nvidia-dev-char.rules <<'EOF'
# NVIDIA: recreate /dev/char symlinks so systemd-managed containers keep GPU access after daemon-reload.
ACTION=="add", DEVPATH=="/bus/pci/drivers/nvidia", RUN+="/usr/bin/nvidia-ctk system create-dev-char-symlinks --create-all"
EOF

# ---------------------------------------------------------------- 4. restart docker
say "Restarting containerd and docker"
systemctl daemon-reload
systemctl restart containerd
systemctl restart docker
for i in $(seq 1 60); do docker info >/dev/null 2>&1 && break; sleep 2; done
docker info >/dev/null || die "docker did not come back. See: journalctl -u docker -n 100. Rollback below."
echo "Docker now: $(docker version --format '{{.Server.Version}}')"

# ---------------------------------------------------------------- 5. bring everything back
say "Starting the containers that were running before"
# Data services first, then the rest.
for c in postgresql cardio-postgres orthanc orthanc-index redis elasticsearch; do
  grep -qx "$c" "$RUN/running-before.txt" && docker start "$c" >/dev/null 2>&1 || true
done
sleep 15
# Containers with a fixed IP (gateway-managed models) next, before anything that gets a dynamic IP
# could take their address ("Address already in use").
while read -r c; do
  ip=$(docker inspect "$c" --format '{{range .NetworkSettings.Networks}}{{if .IPAMConfig}}{{.IPAMConfig.IPv4Address}}{{end}}{{end}}' 2>/dev/null || true)
  [ -n "$ip" ] && docker start "$c" >/dev/null 2>&1 || true
done < "$RUN/running-before.txt"
xargs -r -a "$RUN/running-before.txt" docker start >/dev/null 2>&1 || true
sleep 30
docker ps --format '{{.Names}}' | sort > "$RUN/running-after.txt"
missing=$(comm -23 "$RUN/running-before.txt" "$RUN/running-after.txt" || true)
[ -z "$missing" ] && echo "All $(wc -l < "$RUN/running-before.txt") containers are back." \
  || { echo "NOT RUNNING: $missing"; for c in $missing; do docker start "$c" || true; done; }

# Firewall guard (5434 allowlist; the loopback guard is harmless on 28.x and kept).
[ -x "$GUARD" ] && "$GUARD" || true

# ---------------------------------------------------------------- 6. checks
say "Checks"
fail=0
check() { if eval "$2" >/dev/null 2>&1; then echo "OK   $1"; else echo "FAIL $1"; fail=1; fi; }
printf '%s\n%s\n' "$MIN_DOCKER" "$(docker version --format '{{.Server.Version}}')" | sort -V -C \
  && echo "OK   docker >= $MIN_DOCKER" || { echo "FAIL docker version"; fail=1; }
for m in $GPU_MODELS; do
  grep -qx "$m" "$RUN/running-before.txt" || continue
  check "GPU in $m" "docker exec $m nvidia-smi -L"
done
check "viewer https"                  "curl -sk -o /dev/null -f https://sgbmavram01.icm.local/"
for i in $(seq 1 30); do docker exec api-pacs wget -qO- http://medical-agent:8585/version >/dev/null 2>&1 && break; sleep 4; done
check "gateway reaches medical-agent" "docker exec api-pacs wget -qO- http://medical-agent:8585/version"
check "Orthanc DICOM 4000 listening"  "ss -ltn | grep -q ':4000 '"
check "storage driver overlay2"      "[ \"$(docker info --format '{{.Driver}}')\" = overlay2 ]"
check "Orthanc REST on loopback"      "curl -s -o /dev/null http://127.0.0.1:8042/system"
for p in 8042 5433 5601 8600 8000 8585 9200; do
  check "port $p closed on host IP" "! timeout 3 bash -c '</dev/tcp/$HOST_IP/$p'"
done
check "rpcbind 111 not listening"     "! ss -ltn | grep -q ':111 '"
check "NFS mounts still there"        "mountpoint -q /mnt/data1 && mountpoint -q /mnt/data2 && ls /mnt/data1 >/dev/null"
check "GPU survives a daemon-reload"  "systemctl daemon-reload && sleep 3 && docker exec deepcoro-jepa nvidia-smi -L"
if ! docker exec deepcoro-jepa nvidia-smi -L >/dev/null 2>&1; then
  echo "GPU access was lost after the reload: restarting the model containers"
  for m in $GPU_MODELS; do grep -qx "$m" "$RUN/running-before.txt" && docker restart -t 30 "$m" >/dev/null; done
  for m in $GPU_MODELS; do grep -qx "$m" "$RUN/running-before.txt" && check "GPU in $m (after restart)" "docker exec $m nvidia-smi -L"; done
fi

say "Done. Log: $RUN/log.txt"
[ "$fail" = 0 ] && echo "ALL CHECKS PASSED" || echo "SOME CHECKS FAILED - see above"
# Package names with versions as they were before this run, e.g. docker-ce-28.5.2 containerd.io-1.7.28
PREV=$(awk '{print}' "$RUN/packages-before.txt" | sed -E 's/\.el9.*|\.x86_64$//; s/-[0-9]+(\.[0-9]+)?$//' | tr '\n' ' ')
cat <<EOF

Rollback (if needed), back to the packages from before this run:
  dnf -y downgrade $PREV
  cp -p $RUN/daemon.json.bak /etc/docker/daemon.json
  systemctl restart containerd docker
  xargs -a $RUN/running-before.txt docker start   # if "Address already in use": start the fixed-IP model containers first
  # host hardening:  systemctl enable --now rpcbind.socket
  #                  firewall-cmd --permanent --zone=public --add-service=cockpit   (then restart docker: a firewalld reload drops Docker's rules)
EOF
