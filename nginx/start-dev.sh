#!/bin/sh
set -eu

envsubst '${NGINX_API_MAX_BODY_SIZE} ${NGINX_DICOMWEB_MAX_BODY_SIZE}' < /etc/nginx/nginx.conf.template > /etc/nginx/conf.d/default.conf
exec nginx -g 'daemon off;'
