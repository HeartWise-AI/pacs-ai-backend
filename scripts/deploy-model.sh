#!/usr/bin/env bash
#
# deploy-model.sh — build, push, and (re)register PACS.AI inference model containers.
#
# Phases (per model):
#   1. Build the Docker image from a model directory (model-examples/<MODEL>)
#   2. Push it to Docker Hub
#   3. Replace the model in the backend via the api-pacs REST API:
#      login -> snapshot existing model + ingestion jobs -> remove -> add -> recreate jobs
#
# Usage:
#   ./scripts/deploy-model.sh <model-dir> [version] [options]   # deploy one model
#   ./scripts/deploy-model.sh --all [options]                   # deploy every model in model-examples/
#
#   <model-dir>   Path to the model directory (must contain Dockerfile and data/model_info.json)
#   [version]     Image tag override; defaults to .version from data/model_info.json
#
# Options:
#   --all            Deploy all model directories under model-examples/ one by one
#                    (directories without a Dockerfile + data/model_info.json are skipped;
#                    failures don't stop the batch — a summary is printed at the end)
#   --name <name>    Model name in the backend; defaults to .modelName from data/model_info.json
#   --hf-token-file <path>
#                    HuggingFace token file for models that download weights at build
#                    time; overrides HF_TOKEN_FILE from the config (default: <repo>/hf_token.txt)
#   --yes            Skip the confirmation prompt before removing an existing model
#   --build-only     Run phase 1 only
#   --push-only      Run phases 1 and 2 only (build + push, no registration)
#   --register-only  Run phase 3 only (assumes image already pushed)
#
# Configuration is read from scripts/.env.deploy (see scripts/.env.deploy.example),
# overridable via DEPLOY_ENV_FILE. With --all, the models root can be overridden
# via MODELS_ROOT (default: <repo>/model-examples).
#
# WARNING (issue #243): removing a model cascade-deletes its ingestion jobs,
# candidates, and processing history in Postgres. This script snapshots the job
# CONFIGS and recreates them against the new container, but the candidate and
# processing HISTORY is lost, and studies still inside recentWindowMinutes will
# be re-discovered and re-processed.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${DEPLOY_ENV_FILE:-$SCRIPT_DIR/.env.deploy}"

log()  { printf '\033[1;34m[deploy]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[deploy]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[deploy]\033[0m %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------- arguments

MODEL_DIR=""
VERSION_ARG=""
MODEL_NAME_OVERRIDE=""
HF_TOKEN_FILE_ARG=""
DEPLOY_ALL=false
ASSUME_YES=false
BUILD_ONLY=false
PUSH_ONLY=false
REGISTER_ONLY=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --all)           DEPLOY_ALL=true; shift ;;
    --name)          MODEL_NAME_OVERRIDE="$2"; shift 2 ;;
    --hf-token-file) HF_TOKEN_FILE_ARG="$2"; shift 2 ;;
    --yes)           ASSUME_YES=true; shift ;;
    --build-only)    BUILD_ONLY=true; shift ;;
    --push-only)     PUSH_ONLY=true; shift ;;
    --register-only) REGISTER_ONLY=true; shift ;;
    -h|--help)       awk 'NR>1 && !/^#/{exit} NR>1{sub(/^# ?/,""); print}' "$0"; exit 0 ;;
    -*)              die "Unknown option: $1" ;;
    *)
      if [[ -z "$MODEL_DIR" ]]; then MODEL_DIR="$1"
      elif [[ -z "$VERSION_ARG" ]]; then VERSION_ARG="$1"
      else die "Unexpected argument: $1"
      fi
      shift ;;
  esac
done

if $DEPLOY_ALL; then
  [[ -z "$MODEL_DIR" ]] || die "--all cannot be combined with a model directory"
  [[ -z "$VERSION_ARG" ]] || die "--all cannot be combined with a version override (versions come from each model_info.json)"
  [[ -z "$MODEL_NAME_OVERRIDE" ]] || die "--all cannot be combined with --name"
else
  [[ -n "$MODEL_DIR" ]] || die "Usage: $0 <model-dir> [version] [options], or $0 --all (see --help)"
fi

command -v jq >/dev/null || die "jq is required"
command -v docker >/dev/null || die "docker is required"

# ---------------------------------------------------------------- config

[[ -f "$ENV_FILE" ]] || die "Config file not found: $ENV_FILE (copy scripts/.env.deploy.example and fill it in)"
# shellcheck source=/dev/null
source "$ENV_FILE"

for var in DOCKERHUB_USER; do
  [[ -n "${!var:-}" ]] || die "$var must be set in $ENV_FILE"
done
if ! $BUILD_ONLY && ! $PUSH_ONLY; then
  for var in API_BASE_URL TENANT_ID PACS_ADMIN_EMAIL PACS_ADMIN_PASSWORD FIREBASE_API_KEY; do
    [[ -n "${!var:-}" ]] || die "$var must be set in $ENV_FILE"
  done
fi

IMAGE_PREFIX="${IMAGE_PREFIX:-pacs-ai}"
DEFAULT_OUTPUT_MODE="${DEFAULT_OUTPUT_MODE:-JSON}"
NEW_MODEL_ENVS="${NEW_MODEL_ENVS:-[]}"
HEALTH_TIMEOUT_SECONDS="${HEALTH_TIMEOUT_SECONDS:-300}"
MODELS_ROOT="${MODELS_ROOT:-$SCRIPT_DIR/../model-examples}"

API="${API_BASE_URL:-}"
API="${API%/}"
SESSION_TOKEN=""

# ---------------------------------------------------------------- API helpers

# api METHOD PATH [JSON_BODY] -> prints response body; fails on transport error
api() {
  local method="$1" path="$2" body="${3:-}"
  local args=(-sS -X "$method" -H "Accept: application/json" -H "Authorization: Bearer $SESSION_TOKEN")
  [[ -n "$body" ]] && args+=(-H "Content-Type: application/json" -d "$body")
  curl "${args[@]}" "$API$path"
}

# assert_success RESPONSE CONTEXT -> dies with the API message if success != true
assert_success() {
  local resp="$1" context="$2"
  if [[ "$(jq -r '.success' <<<"$resp" 2>/dev/null)" != "true" ]]; then
    die "$context failed: $(jq -r '.message // .' <<<"$resp" 2>/dev/null || echo "$resp")"
  fi
}

# Firebase sign-in, then session token exchange. Sets SESSION_TOKEN (done once per run).
authenticate() {
  log "Signing in as $PACS_ADMIN_EMAIL..."
  local firebase_resp id_token login_resp
  firebase_resp=$(curl -sS -X POST \
    -H "Content-Type: application/json" \
    -d "$(jq -n --arg e "$PACS_ADMIN_EMAIL" --arg p "$PACS_ADMIN_PASSWORD" --arg t "$TENANT_ID" \
          '{email:$e, password:$p, tenantId:$t, returnSecureToken:true}')" \
    "https://identitytoolkit.googleapis.com/v1/accounts:signInWithPassword?key=$FIREBASE_API_KEY")
  id_token=$(jq -r '.idToken // empty' <<<"$firebase_resp")
  [[ -n "$id_token" ]] || die "Firebase sign-in failed: $(jq -r '.error.message // .' <<<"$firebase_resp")"

  login_resp=$(api POST "/v1/iam/login" \
    "$(jq -n --arg t "$TENANT_ID" --arg i "$id_token" '{tenantId:$t, idToken:$i}')")
  assert_success "$login_resp" "API login"
  SESSION_TOKEN=$(jq -r '.data.sessionToken' <<<"$login_resp")
  [[ -n "$SESSION_TOKEN" && "$SESSION_TOKEN" != "null" ]] || die "API login returned no session token"
  log "Authenticated."
}

# ---------------------------------------------------------------- per-model deploy

# deploy_model MODEL_DIR — runs the requested phases for one model. Uses die on
# failure, so in batch mode it must be invoked inside a subshell.
deploy_model() {
  local model_dir="$1"

  [[ -d "$model_dir" ]] || die "Model directory not found: $model_dir"
  [[ -f "$model_dir/Dockerfile" ]] || die "No Dockerfile in $model_dir"
  local model_info="$model_dir/data/model_info.json"
  [[ -f "$model_info" ]] || die "No data/model_info.json in $model_dir (needed for model name/version)"

  local model_name version image_repo image
  model_name="${MODEL_NAME_OVERRIDE:-$(jq -r '.modelName' "$model_info")}"
  version="${VERSION_ARG:-$(jq -r '.version' "$model_info")}"
  [[ -n "$model_name" && "$model_name" != "null" ]] || die "Could not read .modelName from $model_info"
  [[ -n "$version" && "$version" != "null" ]] || die "Could not read .version from $model_info"

  image_repo="$DOCKERHUB_USER/$IMAGE_PREFIX-$(echo "$model_name" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9._-' '-' | sed 's/-*$//')"
  image="$image_repo:$version"

  log "Model:   $model_name"
  log "Version: $version"
  log "Image:   $image"

  # --- phase 1: build
  if ! $REGISTER_ONLY; then
    local build_args=() hf_token_file
    if grep -q 'mount=type=secret,id=hf_token' "$model_dir/Dockerfile"; then
      # precedence: --hf-token-file arg > HF_TOKEN_FILE from config > repo root's hf_token.txt
      hf_token_file="${HF_TOKEN_FILE_ARG:-${HF_TOKEN_FILE:-$SCRIPT_DIR/../hf_token.txt}}"
      [[ -f "$hf_token_file" ]] || die "This model's Dockerfile downloads weights with a HuggingFace token secret, but no token file was found at $hf_token_file. Pass --hf-token-file <path> or set HF_TOKEN_FILE in $ENV_FILE."
      build_args+=(--secret "id=hf_token,src=$hf_token_file")
      log "Passing HuggingFace token secret from $hf_token_file."
    fi
    log "Phase 1/3: building image..."
    docker build "${build_args[@]}" -t "$image" -t "$image_repo:latest" "$model_dir" || die "docker build failed for $model_dir"
    log "Build complete."
    $BUILD_ONLY && return 0
  fi

  # --- phase 2: push
  if ! $REGISTER_ONLY; then
    log "Phase 2/3: pushing to Docker Hub..."
    docker push "$image" || die "docker push failed for $image"
    docker push "$image_repo:latest" || die "docker push failed for $image_repo:latest"
    log "Push complete."
    $PUSH_ONLY && return 0
  fi

  # --- phase 3: register
  log "Phase 3/3: registering model with the backend..."

  # 3a. look up existing model by name
  local list_resp existing envs output_mode jobs_snapshot
  list_resp=$(api GET "/v1/inference/model/list")
  existing=$(jq --arg n "$model_name" '[.data // [] | .[] | select(.name == $n)] | first // empty' <<<"$list_resp")

  envs="$NEW_MODEL_ENVS"
  output_mode="$DEFAULT_OUTPUT_MODE"
  jobs_snapshot="[]"

  if [[ -n "$existing" ]]; then
    local existing_id existing_container_id existing_image jobs_resp job_count
    existing_id=$(jq -r '.id' <<<"$existing")
    existing_container_id=$(jq -r '.container.id' <<<"$existing")
    existing_image=$(jq -r '.dockerImage' <<<"$existing")
    envs=$(jq '.envs // []' <<<"$existing")
    output_mode=$(jq -r '.outputMode' <<<"$existing")

    log "Found existing model '$model_name' (id=$existing_id, image=$existing_image)."
    log "Carrying over envs and outputMode=$output_mode."

    # snapshot ingestion job configs attached to the old container
    jobs_resp=$(api GET "/v1/inference/ingestion/jobs")
    jobs_snapshot=$(jq --arg c "$existing_container_id" \
      '[.data // [] | .[] | select(.containerId == $c)]' <<<"$jobs_resp")
    job_count=$(jq 'length' <<<"$jobs_snapshot")

    warn "Removing this model deletes its ingestion jobs AND all candidate/processing history (issue #243)."
    warn "$job_count ingestion job config(s) will be snapshotted and recreated; history is NOT recoverable."
    if ! $ASSUME_YES; then
      local answer
      read -r -p "Remove '$model_name' and redeploy as $image? [y/N] " answer </dev/tty
      [[ "$answer" =~ ^[Yy] ]] || die "Aborted."
    fi

    local remove_resp
    remove_resp=$(api DELETE "/v1/inference/model/$existing_id/remove")
    assert_success "$remove_resp" "Model removal"
    log "Old model removed."
  else
    log "No existing model named '$model_name' — registering as new (outputMode=$output_mode)."
  fi

  # 3b. add the model (backend pulls the image, creates + starts the container)
  log "Adding model with image $image (backend is pulling — may take a while)..."
  local add_resp
  add_resp=$(api POST "/v1/inference/model/add" \
    "$(jq -n --arg n "$model_name" --arg i "$image" --arg o "$output_mode" --argjson e "$envs" \
        '{name:$n, dockerImage:$i, envs:$e, outputMode:$o}')")
  assert_success "$add_resp" "Model add"
  log "Model registered."

  # 3c. resolve the new container and wait until the inference API responds
  local new_model new_model_id new_container_id
  list_resp=$(api GET "/v1/inference/model/list")
  new_model=$(jq --arg n "$model_name" '[.data // [] | .[] | select(.name == $n)] | first // empty' <<<"$list_resp")
  [[ -n "$new_model" ]] || die "Model was added but not found in the list afterwards"
  new_model_id=$(jq -r '.id' <<<"$new_model")
  new_container_id=$(jq -r '.container.id' <<<"$new_model")

  log "Waiting for container $new_container_id to serve /inference/model-info (timeout ${HEALTH_TIMEOUT_SECONDS}s)..."
  local deadline info_resp reported_version
  deadline=$(( $(date +%s) + HEALTH_TIMEOUT_SECONDS ))
  while true; do
    info_resp=$(api GET "/v1/inference/model/proxy/container/$new_container_id/info" || true)
    if [[ "$(jq -r '.success' <<<"$info_resp" 2>/dev/null)" == "true" ]]; then
      # models return .data as an object or an array of objects — handle both
      reported_version=$(jq -r '(.data | if type == "array" then .[0] else . end | .version?) // "unknown"' <<<"$info_resp" 2>/dev/null || echo "unknown")
      log "Container is healthy (model reports version $reported_version)."
      break
    fi
    [[ $(date +%s) -lt $deadline ]] || die "Container did not become healthy within ${HEALTH_TIMEOUT_SECONDS}s. Check: docker logs, then /v1/inference/model/container/$new_container_id/info"
    sleep 5
  done

  # 3d. recreate ingestion jobs against the new container
  local job_count i job_payload job_resp
  job_count=$(jq 'length' <<<"$jobs_snapshot")
  if [[ "$job_count" -gt 0 ]]; then
    log "Recreating $job_count ingestion job(s) for the new container..."
    for i in $(seq 0 $((job_count - 1))); do
      job_payload=$(jq --argjson idx "$i" --arg cid "$new_container_id" --arg ver "$version" '
        .[$idx] | {
          dicomModality:          .dicomModality,
          containerId:            $cid,
          modelId:                .modelId,
          modelName:              .modelName,
          modelVersion:           $ver,
          modalities:             .modalities,
          stabilityMinutes:       .stabilityMinutes,
          recentWindowMinutes:    .recentWindowMinutes,
          missingPollsThreshold:  .missingPollsThreshold,
          scheduleStartTimestamp: .scheduleStartTimestamp,
          scheduleEndTimestamp:   .scheduleEndTimestamp
        }
        + (if .studyTimeStart != "" and .studyTimeStart != null then {studyTimeStart: .studyTimeStart} else {} end)
        + (if .studyTimeEnd   != "" and .studyTimeEnd   != null then {studyTimeEnd:   .studyTimeEnd}   else {} end)
      ' <<<"$jobs_snapshot")
      job_resp=$(api POST "/v1/inference/ingestion/job/create" "$job_payload")
      if [[ "$(jq -r '.success' <<<"$job_resp" 2>/dev/null)" != "true" ]]; then
        warn "Failed to recreate ingestion job #$((i + 1)): $(jq -r '.message // .' <<<"$job_resp")"
        warn "Payload was: $job_payload"
      else
        log "Recreated ingestion job #$((i + 1)) ($(jq -r '.dicomModality' <<<"$job_payload"))."
      fi
    done
  fi

  log "Done. '$model_name' is running $image (model id=$new_model_id, container=$new_container_id)."
}

# ---------------------------------------------------------------- main

# Authenticate once up front if phase 3 will run
if ! $BUILD_ONLY && ! $PUSH_ONLY; then
  authenticate
fi

if ! $DEPLOY_ALL; then
  deploy_model "$MODEL_DIR"
  exit 0
fi

# --all: iterate over every deployable directory under MODELS_ROOT
[[ -d "$MODELS_ROOT" ]] || die "Models root not found: $MODELS_ROOT"

DEPLOYED=()
SKIPPED=()
FAILED=()

for dir in "$MODELS_ROOT"/*/; do
  dir="${dir%/}"
  name="$(basename "$dir")"

  if [[ ! -f "$dir/Dockerfile" || ! -f "$dir/data/model_info.json" ]]; then
    warn "Skipping $name (no Dockerfile and/or data/model_info.json)."
    SKIPPED+=("$name")
    continue
  fi

  log ""
  log "================================================================"
  log "Deploying $name"
  log "================================================================"

  # Subshell isolates die/exit; set +e around it so a failure doesn't kill the batch.
  set +e
  ( set -e; deploy_model "$dir" )
  rc=$?
  set -e

  if [[ $rc -eq 0 ]]; then
    DEPLOYED+=("$name")
  else
    warn "Deployment of $name FAILED (continuing with remaining models)."
    FAILED+=("$name")
  fi
done

log ""
log "================================================================"
log "Batch summary"
log "================================================================"
log "Deployed: ${#DEPLOYED[@]} (${DEPLOYED[*]:-none})"
log "Skipped:  ${#SKIPPED[@]} (${SKIPPED[*]:-none})"
if [[ ${#FAILED[@]} -gt 0 ]]; then
  warn "Failed:   ${#FAILED[@]} (${FAILED[*]})"
  exit 1
fi
log "Failed:   0"
