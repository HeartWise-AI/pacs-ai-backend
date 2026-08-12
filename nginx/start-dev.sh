#!/bin/sh
set -eu

: "${NGINX_API_MAX_BODY_SIZE:=16m}"
: "${NGINX_DICOMWEB_MAX_BODY_SIZE:=6g}"
export NGINX_API_MAX_BODY_SIZE NGINX_DICOMWEB_MAX_BODY_SIZE

validate_body_size() {
	name="$1"
	value="$2"

	case "$value" in
		""|*[!0-9kKmMgG]*)
			echo "Invalid $name: expected a positive Nginx size such as 16m or 6g." >&2
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

validate_body_size NGINX_API_MAX_BODY_SIZE "$NGINX_API_MAX_BODY_SIZE"
validate_body_size NGINX_DICOMWEB_MAX_BODY_SIZE "$NGINX_DICOMWEB_MAX_BODY_SIZE"

envsubst '${NGINX_API_MAX_BODY_SIZE} ${NGINX_DICOMWEB_MAX_BODY_SIZE}' < /etc/nginx/nginx.conf.template > /etc/nginx/conf.d/default.conf
exec nginx -g 'daemon off;'
