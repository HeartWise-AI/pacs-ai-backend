#!/bin/sh
# Checks the /study-service/ location of default.conf: only NGINX_STUDY_SERVICE_ALLOWED_IP,
# GET/HEAD only, /study-service prefix stripped, X-Real-IP set, and closed by default.
# Builds the nginx image and runs it next to a stub study-service on a throwaway
# network. Needs docker.   ./nginx/test-study-service-route.sh
set -eu
here=$(cd "$(dirname "$0")" && pwd)
id=$$
net=nginx-ss-test-$id
image=nginx-ss-test:$id
tmp=$(mktemp -d)
cleanup() {
  docker rm -f "ss-nginx-$id" "ss-nginx-closed-$id" "ss-stub-$id" "ss-ok-$id" "ss-other-$id" >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
  docker image rm "$image" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

docker build -q -t "$image" "$here" >/dev/null
docker network create --subnet 10.231.7.0/24 "$net" >/dev/null
cat > "$tmp/stub.conf" <<'CONF'
server { listen 8600; location / { default_type text/plain; return 200 "$request_method $request_uri ip=$http_x_real_ip cf=$http_cf_connecting_ip\n"; } }
CONF
chmod 644 "$tmp/stub.conf"
docker run -d --name "ss-stub-$id" --network "$net" --network-alias study-service \
  -v "$tmp/stub.conf:/etc/nginx/conf.d/default.conf:ro" nginx:1.26-alpine3.20-perl >/dev/null
mkdir "$tmp/tls"
openssl req -x509 -nodes -days 2 -newkey rsa:2048 -subj /CN=localhost \
  -keyout "$tmp/tls/server.key" -out "$tmp/tls/server.crt" >/dev/null 2>&1
chmod 644 "$tmp/tls/server.key" "$tmp/tls/server.crt"
docker run -d --name "ss-nginx-$id" --network "$net" --ip 10.231.7.10 -e SERVER_NAME=localhost \
  -e NGINX_STUDY_SERVICE_ALLOWED_IP=10.231.7.20 -v "$tmp/tls:/etc/nginx/remote-tls:ro" "$image" >/dev/null
docker run -d --name "ss-nginx-closed-$id" --network "$net" --ip 10.231.7.11 -e SERVER_NAME=localhost "$image" >/dev/null
docker run -d --name "ss-ok-$id" --network "$net" --ip 10.231.7.20 alpine:3.20 sleep 300 >/dev/null
docker run -d --name "ss-other-$id" --network "$net" --ip 10.231.7.21 alpine:3.20 sleep 300 >/dev/null
for _ in 1 2 3 4 5 6 7 8 9 10; do
  docker exec "ss-ok-$id" wget -q --no-check-certificate -O /dev/null https://10.231.7.10/study-service/models 2>/dev/null && break
  docker exec "ss-ok-$id" sh -c 'read -t 1 x < /dev/zero' 2>/dev/null || true
done

fail=0
req() { # container method url -> "status body"
  docker exec "$1" sh -c "wget -S -q --no-check-certificate -O - $( [ "$2" = POST ] && echo "--post-data=x" ) '$3' 2>&1" | tr -d '\r'
}
expect() { # name got want-substring
  case "$2" in *"$3"*) ;; *) echo "FAIL $1: got [$2] want [$3]"; fail=1 ;; esac
}
got=$(req "ss-ok-$id" GET "https://10.231.7.10/study-service/jobs/by-study/1.2.3/latest-results?modality=angiogram")
expect "allowed GET" "$got" "GET /jobs/by-study/1.2.3/latest-results?modality=angiogram ip=10.231.7.20 cf="
got=$(req "ss-ok-$id" POST "https://10.231.7.10/study-service/jobs/x/rerun")
expect "allowed host POST" "$got" "403"
got=$(req "ss-other-$id" GET "https://10.231.7.10/study-service/models")
expect "other host GET" "$got" "403"
got=$(req "ss-ok-$id" GET "https://10.231.7.11/study-service/models")
expect "closed by default" "$got" "403"
# 8443 listener (own certificate): same rules, nothing else served.
got=$(req "ss-ok-$id" GET "https://10.231.7.10:8443/study-service/jobs/abc")
expect "8443 allowed GET" "$got" "GET /jobs/abc ip=10.231.7.20"
got=$(req "ss-ok-$id" POST "https://10.231.7.10:8443/study-service/jobs/abc/rerun")
expect "8443 POST" "$got" "403"
got=$(req "ss-other-$id" GET "https://10.231.7.10:8443/study-service/models")
expect "8443 other host" "$got" "403"
got=$(req "ss-ok-$id" GET "https://10.231.7.10:8443/api/v1/user/me")
expect "8443 serves nothing else" "$got" "404"
got=$(docker exec "ss-nginx-$id" sh -c 'echo | openssl s_client -connect 127.0.0.1:8443 2>/dev/null | openssl x509 -noout -subject')
expect "8443 uses the mounted certificate" "$(echo "$got" | tr -d " ")" "CN=localhost"
# Without the mount there is no 8443 listener.
got=$(docker exec "ss-nginx-closed-$id" sh -c 'ls /etc/nginx/conf.d')
case "$got" in *remote-study-service*) echo "FAIL listener rendered without certificate"; fail=1 ;; esac
[ "$fail" = 0 ] && echo "nginx study-service route: ok"
exit "$fail"
