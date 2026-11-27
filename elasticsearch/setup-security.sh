#!/bin/sh
# Sets the kibana_system password and creates the least-privilege api-pacs
# user. Secrets are passed through a private curl config file and stdin, never
# on the command line.
set -eu

ES_URL=http://elasticsearch:9200
API_PACS_ROLE=api_pacs_audit

for name in ELASTIC_PASSWORD KIBANA_SYSTEM_PASSWORD ELASTICSEARCH_API_PACS_USERNAME ELASTICSEARCH_API_PACS_PASSWORD; do
  eval "value=\${$name:-}"
  if [ -z "$value" ]; then
    echo "$name must be set" >&2
    exit 1
  fi
  case "$value" in
    *\"* | *\\*)
      echo "$name must not contain double quotes or backslashes" >&2
      exit 1
      ;;
  esac
done

umask 077
auth_config=$(mktemp)
trap 'rm -f "$auth_config"' EXIT
printf 'user = "elastic:%s"\n' "$ELASTIC_PASSWORD" >"$auth_config"

es() {
  curl -sS -o /dev/null -w '%{http_code}' -K "$auth_config" \
    -H 'Content-Type: application/json' -X "$1" "$ES_URL$2" --data-binary @-
}

# Wait for Elasticsearch, but stop with a clear error when it rejects the
# elastic password (for example an existing data volume whose elastic password
# was changed) instead of retrying forever.
tries=0
while :; do
  status=$(printf '' | es GET /_security/_authenticate || true)
  case "$status" in
    200) break ;;
    401)
      echo "elasticsearch rejected the elastic user: ELASTIC_PASSWORD does not match the cluster" >&2
      exit 1
      ;;
  esac
  tries=$((tries + 1))
  if [ "$tries" -ge 150 ]; then
    echo "elasticsearch did not become ready (last HTTP status: $status)" >&2
    exit 1
  fi
  echo "waiting for elasticsearch (HTTP $status)"
  sleep 2
done

expect_ok() {
  status=$1
  shift
  if [ "$status" != 200 ]; then
    echo "$* failed with HTTP $status" >&2
    exit 1
  fi
}

expect_ok "$(printf '{"password":"%s"}' "$KIBANA_SYSTEM_PASSWORD" |
  es POST /_security/user/kibana_system/_password)" "set kibana_system password"

expect_ok "$(es PUT "/_security/role/$API_PACS_ROLE" <<'JSON'
{
  "cluster": ["monitor"],
  "indices": [
    {
      "names": [
        "admin_invites",
        "admin_members",
        "find_modality_studies",
        "logins",
        "predict_inference_models",
        "retrieved_studies",
        "signed_consents",
        "stored_custom_series"
      ],
      "privileges": ["auto_configure", "create_doc", "read", "view_index_metadata", "monitor"]
    }
  ],
  "applications": [
    {
      "application": "kibana-.kibana",
      "privileges": ["feature_indexPatterns.all", "feature_advancedSettings.all"],
      "resources": ["space:default"]
    }
  ]
}
JSON
)" "create role $API_PACS_ROLE"

expect_ok "$(printf '{"password":"%s","roles":["%s"]}' "$ELASTICSEARCH_API_PACS_PASSWORD" "$API_PACS_ROLE" |
  es PUT "/_security/user/$ELASTICSEARCH_API_PACS_USERNAME")" "create user $ELASTICSEARCH_API_PACS_USERNAME"

echo "elasticsearch security configured"
