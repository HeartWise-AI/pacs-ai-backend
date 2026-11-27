#!/bin/sh
set -eu

: "${SERVER_NAME:=localhost}"
: "${NGINX_FRONTEND_MAX_BODY_SIZE:=1m}"
: "${NGINX_API_MAX_BODY_SIZE:=16m}"
# One address (or CIDR) allowed to mint sign-in handoffs through nginx (CA-268).
# 127.0.0.1 is never a remote client, so the default keeps the route closed.
: "${NGINX_HANDOFF_ALLOWED_IP:=127.0.0.1}"
: "${NGINX_DICOMWEB_MAX_BODY_SIZE:=6g}"
# One address (or CIDR) allowed on /study-service/ (CardioAgent's study-service
# reads from another host). The default keeps the route closed.
: "${NGINX_STUDY_SERVICE_ALLOWED_IP:=127.0.0.1}"
export SERVER_NAME NGINX_FRONTEND_MAX_BODY_SIZE NGINX_API_MAX_BODY_SIZE NGINX_DICOMWEB_MAX_BODY_SIZE NGINX_HANDOFF_ALLOWED_IP NGINX_STUDY_SERVICE_ALLOWED_IP

validate_body_size() {
  name="$1"
  value="$2"

  case "$value" in
    ""|*[!0-9kKmMgG]*)
      echo "Invalid $name: expected a positive Nginx size such as 1m, 16m, or 6g." >&2
      exit 1
      ;;
  esac

  case "$value" in
    *[kKmMgG]) number="${value%?}" ;;
    *) number="$value" ;;
  esac

  case "$number" in
    ""|*[!0-9]*)
      echo "Invalid $name: body-size limits must be greater than zero." >&2
      exit 1
      ;;
  esac

  positive="$number"
  while [ "${positive#0}" != "$positive" ]; do
    positive="${positive#0}"
  done
  if [ -z "$positive" ]; then
    echo "Invalid $name: body-size limits must be greater than zero." >&2
    exit 1
  fi
}

validate_body_size NGINX_FRONTEND_MAX_BODY_SIZE "$NGINX_FRONTEND_MAX_BODY_SIZE"
validate_body_size NGINX_API_MAX_BODY_SIZE "$NGINX_API_MAX_BODY_SIZE"
validate_body_size NGINX_DICOMWEB_MAX_BODY_SIZE "$NGINX_DICOMWEB_MAX_BODY_SIZE"

SSL_DIR="/etc/nginx/ssl"
CRT_FILE="$SSL_DIR/nginx.crt"
KEY_FILE="$SSL_DIR/nginx.key"

# Create the SSL directory if it doesn't exist
mkdir -p $SSL_DIR

# Check if the necessary SSL files exist
if [ ! -f $CRT_FILE ] || [ ! -f $KEY_FILE ]; then
  echo "SSL files not found, generating self-signed SSL certificate..."

  # Generate a new self-signed certificate
  openssl req -x509 -nodes -days 365 \
    -newkey rsa:2048 \
    -keyout $KEY_FILE \
    -out $CRT_FILE \
    -subj "/CN=${SERVER_NAME:-localhost}"

  echo "Self-signed certificate generated at $CRT_FILE and $KEY_FILE"
else
  echo "Using existing SSL certificate and key."
fi

# Replace environment variables in the Nginx config template with real values
case "$NGINX_HANDOFF_ALLOWED_IP" in
  *[!0-9./]*|'') echo "NGINX_HANDOFF_ALLOWED_IP must be an IPv4 address or CIDR" >&2; exit 1 ;;
esac
case "$NGINX_STUDY_SERVICE_ALLOWED_IP" in
  *[!0-9./]*|'') echo "NGINX_STUDY_SERVICE_ALLOWED_IP must be an IPv4 address or CIDR" >&2; exit 1 ;;
esac

envsubst '${SERVER_NAME} ${NGINX_FRONTEND_MAX_BODY_SIZE} ${NGINX_API_MAX_BODY_SIZE} ${NGINX_DICOMWEB_MAX_BODY_SIZE} ${NGINX_HANDOFF_ALLOWED_IP} ${NGINX_STUDY_SERVICE_ALLOWED_IP}' < /etc/nginx/nginx.conf.template > /etc/nginx/conf.d/default.conf

mkdir -p /etc/nginx/snippets
envsubst '${NGINX_STUDY_SERVICE_ALLOWED_IP}' < /etc/nginx/study-service-location.conf.template > /etc/nginx/snippets/study-service-location.conf

# The 8443 listener for remote machine callers exists only when its certificate
# and key are mounted; without them nginx starts exactly as before.
REMOTE_TLS_DIR=/etc/nginx/remote-tls
if [ -s "$REMOTE_TLS_DIR/server.crt" ] && [ -s "$REMOTE_TLS_DIR/server.key" ]; then
  envsubst '${SERVER_NAME}' < /etc/nginx/remote-study-service.conf.template > /etc/nginx/conf.d/remote-study-service.conf
  echo "Remote study-service listener on 8443 enabled."
else
  rm -f /etc/nginx/conf.d/remote-study-service.conf
fi

# Start Nginx
exec nginx -g 'daemon off;'
