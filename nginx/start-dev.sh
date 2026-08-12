#!/bin/sh
set -eu

: "${NGINX_API_MAX_BODY_SIZE:=16m}"
: "${NGINX_DICOMWEB_MAX_BODY_SIZE:=6g}"
export NGINX_API_MAX_BODY_SIZE NGINX_DICOMWEB_MAX_BODY_SIZE

envsubst '${NGINX_API_MAX_BODY_SIZE} ${NGINX_DICOMWEB_MAX_BODY_SIZE}' < /etc/nginx/nginx.conf.template > /etc/nginx/conf.d/default.conf
exec nginx -g 'daemon off;'
