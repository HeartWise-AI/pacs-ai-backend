#!/bin/sh
# Checks the "redacted" access log format of default.conf: no query string, no
# Referer, DICOM UID path segments replaced by "uid". Needs docker.
#   ./nginx/test-log-redaction.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
name=nginx-log-redaction-test-$$
trap 'docker rm -f "$name" >/dev/null 2>&1 || true; rm -rf "$tmp"' EXIT

# The map and log_format from default.conf, plus a server that echoes the result.
sed -n '/^map \$uri \$redacted_uri/,/^server {/p' "$here/default.conf" | sed '$d' > "$tmp/t.conf"
cat >> "$tmp/t.conf" <<'CONF'
server { listen 8099; access_log /dev/stdout redacted; location / { default_type text/plain; return 200 "$redacted_uri\n"; } }
CONF
chmod 644 "$tmp/t.conf"
docker run -d --name "$name" --network none -v "$tmp/t.conf:/etc/nginx/conf.d/default.conf:ro" nginx:1.26-alpine3.20-perl >/dev/null
docker exec "$name" nginx -t >/dev/null 2>&1

# Synthetic identifiers only.
U1=1.2.826.0.1.3680043.8.498.1
U2=2.25.329800735698586629295641978511506172918
U3=1.2.826.0.1.3680043.8.498.3
fail=0
check() {
  got=$(docker exec "$name" wget -qO- --header "Referer: https://x/viewer?patientId=TEST001" "http://127.0.0.1:8099$1")
  if [ "$got" != "$2" ]; then echo "FAIL $1 -> $got (want $2)"; fail=1; fi
}
check "/api/proxy/orthanc/dicom-web/studies/$U1/series/$U2/instances/$U3/frames/1" /api/proxy/orthanc/dicom-web/studies/uid/series/uid/instances/uid/frames/1
check "/api/proxy/orthanc/dicom-web/studies/$U1/series/$U2/metadata" /api/proxy/orthanc/dicom-web/studies/uid/series/uid/metadata
check "/api/proxy/orthanc/dicom-web/studies/$U1/series?includefield=0008103E" /api/proxy/orthanc/dicom-web/studies/uid/series
check "/api/proxy/orthanc/dicom-web/studies?00100020=TEST001&limit=1" /api/proxy/orthanc/dicom-web/studies
check "/api/v1/inference/worklist/studies/$U1/latest-results" /api/v1/inference/worklist/studies/uid/latest-results
check "/api/v1/orchestrator/threads?studyInstanceUID=$U1" /api/v1/orchestrator/threads
check "/api/v1/inference/model/PanEcho-1.1.0/feedback" /api/v1/inference/model/PanEcho-1.1.0/feedback
check "/viewer?StudyInstanceUIDs=$U1&patientId=TEST001" /viewer

logs=$(docker logs "$name" 2>/dev/null)
for leak in TEST001 "$U1" "$U2" "$U3" 00100020= patientId; do
  if printf '%s' "$logs" | grep -q "$leak"; then echo "FAIL access log contains $leak"; fail=1; fi
done
[ "$fail" = 0 ] && echo "nginx log redaction: ok"
exit "$fail"
