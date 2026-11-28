#!/bin/sh
# Checks HTTP/2 on the TLS listeners of default.conf (443) and the remote
# study-service listener (8443), and that the behaviour behind them is unchanged:
# Host is forwarded, an SSE response (X-Accel-Buffering: no, like the worklist
# events stream) is delivered event by event over HTTP/2, the frontend body limit
# still answers 413, and HTTP/1.1 clients still work. Builds the nginx image and
# runs it next to stub upstreams on a throwaway network. Needs docker.
#   ./nginx/test-http2.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
id=$$
net=nginx-h2-test-$id
image=nginx-h2-test:$id
tmp=$(mktemp -d)
cleanup() {
  docker rm -f "h2-nginx-$id" "h2-api-$id" "h2-front-$id" "h2-ss-$id" "h2-client-$id" >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
  docker image rm "$image" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

docker build -q -t "$image" "$here" >/dev/null
docker network create --subnet 10.231.9.0/24 "$net" >/dev/null

# api-pacs stub: /sse sends three events one second apart; everything else echoes
# the method, path and Host it received.
cat > "$tmp/api.py" <<'PY'
import http.server, time
class H(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.0"
    def do_GET(self):
        if self.path.startswith("/sse"):
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.send_header("X-Accel-Buffering", "no")
            self.end_headers()
            for i in range(3):
                self.wfile.write(("data: %d\n\n" % i).encode()); self.wfile.flush()
                time.sleep(1)
            return
        body = ("%s %s host=%s\n" % (self.command, self.path, self.headers.get("Host"))).encode()
        self.send_response(200); self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)
    def do_POST(self):
        n = 0
        if self.headers.get("Transfer-Encoding", "").lower() == "chunked":
            while True:
                size = int(self.rfile.readline().strip(), 16)
                if size == 0:
                    self.rfile.readline(); break
                n += len(self.rfile.read(size)); self.rfile.readline()
        else:
            left = int(self.headers.get("Content-Length", "0"))
            while left > 0:
                chunk = self.rfile.read(min(left, 1 << 20)); n += len(chunk); left -= len(chunk)
        body = ("%s %s bytes=%d\n" % (self.command, self.path, n)).encode()
        self.send_response(200); self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body))); self.end_headers(); self.wfile.write(body)
    def log_message(self, *a): pass
http.server.ThreadingHTTPServer(("", 8000), H).serve_forever()
PY
cat > "$tmp/front.conf" <<'CONF'
server { listen 80; location / { default_type text/plain; return 200 "front $request_uri host=$http_host\n"; } }
CONF
cat > "$tmp/ss.conf" <<'CONF'
server { listen 8600; location / { default_type text/plain; return 200 "ss $request_method $request_uri ip=$http_x_real_ip\n"; } }
CONF
chmod 644 "$tmp/api.py" "$tmp/front.conf" "$tmp/ss.conf"
docker run -d --name "h2-api-$id" --network "$net" --network-alias api-pacs \
  -v "$tmp/api.py:/api.py:ro" python:3.12-slim python -u /api.py >/dev/null
docker run -d --name "h2-front-$id" --network "$net" --network-alias pacs-ai \
  -v "$tmp/front.conf:/etc/nginx/conf.d/default.conf:ro" nginx:1.26-alpine3.20-perl >/dev/null
docker run -d --name "h2-ss-$id" --network "$net" --network-alias study-service \
  -v "$tmp/ss.conf:/etc/nginx/conf.d/default.conf:ro" nginx:1.26-alpine3.20-perl >/dev/null
mkdir "$tmp/tls"
openssl req -x509 -nodes -days 2 -newkey rsa:2048 -subj /CN=localhost \
  -keyout "$tmp/tls/server.key" -out "$tmp/tls/server.crt" >/dev/null 2>&1
chmod 644 "$tmp/tls/server.key" "$tmp/tls/server.crt"
docker run -d --name "h2-nginx-$id" --network "$net" --ip 10.231.9.10 -e SERVER_NAME=localhost \
  -e NGINX_STUDY_SERVICE_ALLOWED_IP=10.231.9.20 -v "$tmp/tls:/etc/nginx/remote-tls:ro" "$image" >/dev/null
docker run -d --name "h2-client-$id" --network "$net" --ip 10.231.9.20 --entrypoint sleep curlimages/curl:8.11.1 300 >/dev/null

c() { docker exec "h2-client-$id" curl -sk --max-time 20 "$@"; }
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  [ "$(c -o /dev/null -w '%{http_code}' https://10.231.9.10/api/ping 2>/dev/null || true)" = 200 ] && break
  docker exec "h2-client-$id" sh -c 'read -t 1 x < /dev/zero' 2>/dev/null || true
done

fail=0
expect() { # name got want-substring
  case "$2" in *"$3"*) ;; *) echo "FAIL $1: got [$2] want [$3]"; fail=1 ;; esac
}
expect "nginx -t" "$(docker exec "h2-nginx-$id" nginx -t 2>&1)" "test is successful"
expect "443 negotiates HTTP/2" "$(c --http2 -o /dev/null -w '%{http_version}' -H 'Host: localhost' https://10.231.9.10/)" "2"
expect "443 frontend over h2, Host forwarded" "$(c --http2 -H 'Host: localhost' https://10.231.9.10/viewer)" "front /viewer host=localhost"
expect "443 api over h2, Host forwarded" "$(c --http2 https://localhost/api/v1/x?y=1 --resolve localhost:443:10.231.9.10)" "GET /v1/x?y=1 host=localhost"
expect "443 HTTP/1.1 still served" "$(c --http1.1 -o /dev/null -w '%{http_version} %{http_code}' https://10.231.9.10/api/ping)" "1.1 200"
expect "443 frontend body limit still 413" "$(c --http2 -o /dev/null -w '%{http_code}' -X POST --data-binary @/etc/os-release -H 'Content-Length: 2000000' https://10.231.9.10/ 2>/dev/null || true)" "413"
# Large unbuffered upload (DICOMweb STOW route, proxy_request_buffering off) over h2.
docker exec "h2-client-$id" sh -c 'head -c 52428800 /dev/zero > /tmp/up.bin'
expect "443 50 MB DICOMweb upload over h2" "$(c --http2 -X POST --data-binary @/tmp/up.bin -H 'Content-Type: application/dicom' https://10.231.9.10/api/proxy/orthanc/dicom-web/studies)" "POST /proxy/orthanc/dicom-web/studies bytes=52428800"
# SSE: the first event must arrive about 2 s before the stream ends (no buffering).
sse=$(docker exec "h2-client-$id" sh -c 'curl -sk --http2 -N --max-time 20 https://10.231.9.10/api/sse | while IFS= read -r l; do if [ -n "$l" ]; then echo "$(date +%s) $l"; fi; done' || true)
first=$(echo "$sse" | head -1 | cut -d" " -f1); last=$(echo "$sse" | tail -1 | cut -d" " -f1)
expect "SSE events over h2" "$(echo "$sse" | cut -d" " -f2- | tr '\n' ' ')" "data: 0 data: 1 data: 2"
[ $((last - first)) -ge 1 ] || { echo "FAIL SSE buffered: $sse"; fail=1; }
expect "8443 negotiates HTTP/2" "$(c --http2 -o /dev/null -w '%{http_version}' https://10.231.9.10:8443/study-service/models)" "2"
expect "8443 allowed GET over h2" "$(c --http2 https://10.231.9.10:8443/study-service/jobs/abc)" "ss GET /jobs/abc ip=10.231.9.20"
expect "8443 POST still 403" "$(c --http2 -o /dev/null -w '%{http_code}' -X POST https://10.231.9.10:8443/study-service/jobs/abc/rerun)" "403"
expect "8443 serves nothing else" "$(c --http2 -o /dev/null -w '%{http_code}' https://10.231.9.10:8443/api/v1/user/me)" "404"
[ "$fail" = 0 ] && echo "nginx http2: ok"
exit "$fail"
