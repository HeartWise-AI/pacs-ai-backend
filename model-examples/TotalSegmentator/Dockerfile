FROM python:3.12.1-slim

ARG PACS_AI_SOURCE_REPOSITORY
ARG PACS_AI_SOURCE_REVISION
ARG PACS_AI_MODEL_VERSION
ARG PACS_AI_MODEL_REPOSITORY
ARG PACS_AI_MODEL_REVISION
ARG PACS_AI_WEIGHTS_PATH
ARG PACS_AI_WEIGHTS_SHA256

RUN apt-get update && apt-get install -y --no-install-recommends nginx supervisor

# Create necessary directories
WORKDIR /app

# Copy requirements first to leverage Docker cache
COPY requirements.txt .
RUN pip install -r requirements.txt

# Download the immutable task-297 archive and retain it for release verification.
COPY download_model.py .
RUN python download_model.py \
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
ENV PACS_AI_MODEL_VERSION="${PACS_AI_MODEL_VERSION}"

LABEL org.opencontainers.image.title="TotalSegmentator" \
      org.opencontainers.image.version="${PACS_AI_MODEL_VERSION}" \
      org.opencontainers.image.revision="${PACS_AI_SOURCE_REVISION}" \
      org.opencontainers.image.source="https://github.com/${PACS_AI_SOURCE_REPOSITORY}" \
      ai.heartwise.model.repository="${PACS_AI_MODEL_REPOSITORY}" \
      ai.heartwise.model.revision="${PACS_AI_MODEL_REVISION}" \
      ai.heartwise.model.weights.path="${PACS_AI_WEIGHTS_PATH}" \
      ai.heartwise.model.weights.sha256="${PACS_AI_WEIGHTS_SHA256}"

# Expose port 80
EXPOSE 80

# Override default entrypoint from tensorflow/serving
ENTRYPOINT []

# Start supervisor
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
