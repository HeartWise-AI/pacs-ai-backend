FROM python:3.12.1-slim

ARG PACS_AI_SOURCE_REVISION=""

LABEL org.opencontainers.image.title="DeepCORO-SYNTAX" \
      org.opencontainers.image.version="6.0.0" \
      org.opencontainers.image.revision="${PACS_AI_SOURCE_REVISION}" \
      org.opencontainers.image.source="https://github.com/HeartWise-AI/pacs-ai-backend" \
      ai.heartwise.model.repository="heartwise/deepcoro_clip_cardiosyntax" \
      ai.heartwise.model.revision="1ffb38cfc10fa10c4b60f44746063feb860eeba0" \
      ai.heartwise.model.weights.path="v6_20260929-203527/models/best_model_epoch_19.pt" \
      ai.heartwise.model.weights.sha256="856f5d6523c25a45c62886bf6cd1d351297821f60c3465a20b962aa46fa08ec0"

ENV PACS_AI_SOURCE_REVISION="${PACS_AI_SOURCE_REVISION}"

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
    python download_model.py --token-file /run/secrets/hf_token

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

# Expose port 8000
EXPOSE 8000

# Override default entrypoint from tensorflow/serving
ENTRYPOINT []

# Start supervisor
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
