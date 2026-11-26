FROM python:3.12.1-slim

ARG PACS_AI_SOURCE_REPOSITORY
ARG PACS_AI_SOURCE_REVISION
ARG PACS_AI_MODEL_VERSION
ARG PACS_AI_MODEL_REPOSITORY
ARG PACS_AI_MODEL_REVISION
ARG PACS_AI_WEIGHTS_PATH
ARG PACS_AI_WEIGHTS_SHA256

RUN apt-get update && apt-get install -y --no-install-recommends nginx supervisor \
    && rm -rf /var/lib/apt/lists/*

RUN pip install uv

WORKDIR /app

# requirements.txt installs the matched PyTorch/torchvision CUDA 12.6 wheels.
COPY requirements.txt .
RUN uv pip install -r requirements.txt --system

# The downloader reads the pinned checkpoints from models/config.json and verifies probe files
# with models/probes.py, so the model sources come first. Weights: heartwise/deepecg_clip_dinov3_acs
# at the pinned revision, the same bytes as the NAS release folder
# /mnt/data1/models/DeepECG-PNG/acs_ft20_dinov3_siglip2_epoch030/ (SHA256SUMS there).
COPY models/ ./models/
# models/dinov3_bank.py (vendored from DeepECG_Docker) imports utils.log_config at import time, and
# download_model.py validates the probe heads with it: that module must be present before the download.
COPY utils/log_config.py ./utils/log_config.py
COPY download_model.py .
RUN --mount=type=secret,id=hf_token \
    python download_model.py \
      --token-file /run/secrets/hf_token \
      --repository "${PACS_AI_MODEL_REPOSITORY}" \
      --revision "${PACS_AI_MODEL_REVISION}" \
      --weights-path "${PACS_AI_WEIGHTS_PATH}" \
      --weights-sha256 "${PACS_AI_WEIGHTS_SHA256}"

COPY main.py .
COPY logic.py .
COPY report_input.py .
COPY config.json .
COPY data/ ./data/
COPY docs/ ./docs/
COPY utils/ ./utils/
COPY nginx.conf /etc/nginx/nginx.conf
COPY supervisord.conf /etc/supervisord.conf

ENV PACS_AI_SOURCE_REVISION="${PACS_AI_SOURCE_REVISION}"
ENV PACS_AI_MODEL_VERSION="${PACS_AI_MODEL_VERSION}"
ENV HF_HUB_OFFLINE=1

LABEL org.opencontainers.image.title="DeepECG-CLIP-DINOv3" \
      org.opencontainers.image.version="${PACS_AI_MODEL_VERSION}" \
      org.opencontainers.image.revision="${PACS_AI_SOURCE_REVISION}" \
      org.opencontainers.image.source="https://github.com/${PACS_AI_SOURCE_REPOSITORY}" \
      ai.heartwise.model.repository="${PACS_AI_MODEL_REPOSITORY}" \
      ai.heartwise.model.revision="${PACS_AI_MODEL_REVISION}" \
      ai.heartwise.model.weights.path="${PACS_AI_WEIGHTS_PATH}" \
      ai.heartwise.model.weights.sha256="${PACS_AI_WEIGHTS_SHA256}" \
      ai.heartwise.model.license="research use only; weights subject to the Meta DINOv3 license"

EXPOSE 8000

ENTRYPOINT []
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
