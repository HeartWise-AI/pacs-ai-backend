FROM python:3.12.1-slim

ARG PACS_AI_SOURCE_REPOSITORY
ARG PACS_AI_SOURCE_REVISION
ARG PACS_AI_MODEL_VERSION
ARG PACS_AI_MODEL_REPOSITORY
ARG PACS_AI_MODEL_REVISION
ARG PACS_AI_WEIGHTS_PATH
ARG PACS_AI_WEIGHTS_SHA256

RUN apt-get update && apt-get install -y --no-install-recommends nginx supervisor git

# Install uv
RUN pip install uv

# Create necessary directories
WORKDIR /app

# Copy requirements first to leverage Docker cache
# requirements.txt installs the matched PyTorch/torchvision CUDA 12.4 wheels.
COPY requirements.txt .

# Use uv instead of pip for much faster installation
RUN uv pip install -r requirements.txt --system

# Copy the model download script
COPY download_model.py .
RUN --mount=type=secret,id=hf_token \
    python download_model.py \
      --token-file /run/secrets/hf_token \
      --repository "${PACS_AI_MODEL_REPOSITORY}" \
      --revision "${PACS_AI_MODEL_REVISION}" \
      --weights-path "${PACS_AI_WEIGHTS_PATH}" \
      --weights-sha256 "${PACS_AI_WEIGHTS_SHA256}"

# Copy application files
COPY main.py .
COPY logic.py .
COPY config.json .
COPY data/ ./data/
COPY docs/ ./docs/
COPY models/ ./models/
COPY utils/ ./utils/
COPY nginx.conf /etc/nginx/nginx.conf
COPY supervisord.conf /etc/supervisord.conf

ENV PACS_AI_SOURCE_REVISION="${PACS_AI_SOURCE_REVISION}"

LABEL org.opencontainers.image.title="DeepCORO-SYNTAX" \
      org.opencontainers.image.version="${PACS_AI_MODEL_VERSION}" \
      org.opencontainers.image.revision="${PACS_AI_SOURCE_REVISION}" \
      org.opencontainers.image.source="https://github.com/${PACS_AI_SOURCE_REPOSITORY}" \
      ai.heartwise.model.repository="${PACS_AI_MODEL_REPOSITORY}" \
      ai.heartwise.model.revision="${PACS_AI_MODEL_REVISION}" \
      ai.heartwise.model.weights.path="${PACS_AI_WEIGHTS_PATH}" \
      ai.heartwise.model.weights.sha256="${PACS_AI_WEIGHTS_SHA256}"

# Expose port 8000
EXPOSE 8000

# Override default entrypoint from tensorflow/serving
ENTRYPOINT []

# Start supervisor
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
