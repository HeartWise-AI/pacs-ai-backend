#!/bin/sh

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
nginx -g 'daemon off;'
