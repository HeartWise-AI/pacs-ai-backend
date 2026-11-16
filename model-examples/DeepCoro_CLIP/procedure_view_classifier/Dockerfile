FROM python:3.12.1-slim

ARG PACS_AI_SOURCE_REPOSITORY
ARG PACS_AI_SOURCE_REVISION
ARG PACS_AI_MODEL_VERSION
ARG PACS_AI_MODEL_REPOSITORY
ARG PACS_AI_MODEL_REVISION
ARG PACS_AI_WEIGHTS_PATH
ARG PACS_AI_WEIGHTS_SHA256

# git fetches the pinned pytorchvideo hub commit (see download_model.py)
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    nginx \
    supervisor \
    && rm -rf /var/lib/apt/lists/*

RUN pip install uv==0.9.27

WORKDIR /app

# Copy requirements first to leverage Docker cache
COPY requirements.txt .
RUN uv pip install -r requirements.txt --system

# Pinned VasoVision weights, config and class mapping, the pinned pytorchvideo hub
# repository and the X3D-M checkpoint, each verified by commit or SHA-256.
COPY download_model.py .
RUN --mount=type=secret,id=hf_token,required=true \
    python download_model.py \
      --token-file /run/secrets/hf_token \
      --repository "${PACS_AI_MODEL_REPOSITORY}" \
      --revision "${PACS_AI_MODEL_REVISION}" \
      --weights-path "${PACS_AI_WEIGHTS_PATH}" \
      --weights-sha256 "${PACS_AI_WEIGHTS_SHA256}" \
      --output-dir models

# Copy application files
COPY main.py .
COPY logic.py .
COPY data/ ./data/
COPY docs/ ./docs/
COPY models/ ./models/
COPY utils/ ./utils/
COPY nginx.conf /etc/nginx/nginx.conf
COPY supervisord.conf /etc/supervisord.conf

ENV PACS_AI_SOURCE_REVISION="${PACS_AI_SOURCE_REVISION}"
ENV PACS_AI_MODEL_VERSION="${PACS_AI_MODEL_VERSION}"

LABEL org.opencontainers.image.title="DeepCoro_CLIP_ProcedureViewClassifier" \
      org.opencontainers.image.version="${PACS_AI_MODEL_VERSION}" \
      org.opencontainers.image.revision="${PACS_AI_SOURCE_REVISION}" \
      org.opencontainers.image.source="https://github.com/${PACS_AI_SOURCE_REPOSITORY}" \
      ai.heartwise.model.repository="${PACS_AI_MODEL_REPOSITORY}" \
      ai.heartwise.model.revision="${PACS_AI_MODEL_REVISION}" \
      ai.heartwise.model.weights.path="${PACS_AI_WEIGHTS_PATH}" \
      ai.heartwise.model.weights.sha256="${PACS_AI_WEIGHTS_SHA256}"

# nginx listens on 80 and proxies to uvicorn on 8000
EXPOSE 80

ENTRYPOINT []

# Start supervisor
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
