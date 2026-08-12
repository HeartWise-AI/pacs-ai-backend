#!/bin/sh
set -eu

: "${SERVER_NAME:=localhost}"
: "${NGINX_FRONTEND_MAX_BODY_SIZE:=1m}"
: "${NGINX_API_MAX_BODY_SIZE:=16m}"
: "${NGINX_DICOMWEB_MAX_BODY_SIZE:=6g}"
export SERVER_NAME NGINX_FRONTEND_MAX_BODY_SIZE NGINX_API_MAX_BODY_SIZE NGINX_DICOMWEB_MAX_BODY_SIZE

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
envsubst '${SERVER_NAME} ${NGINX_FRONTEND_MAX_BODY_SIZE} ${NGINX_API_MAX_BODY_SIZE} ${NGINX_DICOMWEB_MAX_BODY_SIZE}' < /etc/nginx/nginx.conf.template > /etc/nginx/conf.d/default.conf

# Start Nginx
exec nginx -g 'daemon off;'
