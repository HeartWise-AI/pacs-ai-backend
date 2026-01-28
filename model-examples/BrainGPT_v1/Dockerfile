
# Base image with CUDA support for GPU inference
FROM nvidia/cuda:12.1.1-cudnn8-runtime-ubuntu22.04

# Install system dependencies
# - python3: Runtime for the application
# - nginx: Reverse proxy for production deployment
# - supervisor: Process manager to run nginx + uvicorn together
# - git: Required for pip to install packages from git repos
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3.10 \
    python3-pip \
    python3-dev \
    git \
    nginx \
    supervisor \
    && rm -rf /var/lib/apt/lists/*

# Create python symlink (Ubuntu uses python3 by default)
RUN ln -s /usr/bin/python3 /usr/bin/python

# Set working directory
WORKDIR /app

# Copy and install Python dependencies first (Docker layer caching)
# This layer will be cached unless requirements.txt changes
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy the download script and execute it
# The model will be downloaded into models/OTTER_CLIP_BRAINGPT_hf/
COPY download_model.py .
RUN python download_model.py
# ============================================

# Copy application code
COPY main.py .
COPY config.json .
COPY utils/ ./utils/
COPY otter/ ./otter/
COPY docs/ ./docs/
COPY data/ ./data/
COPY logic.py .
COPY pipeline_script.py .

# Copy server configuration files
COPY nginx.conf /etc/nginx/nginx.conf
COPY supervisord.conf /etc/supervisord.conf

# Expose the HTTP port
EXPOSE 8000

# Clear any inherited entrypoint
ENTRYPOINT []

# Start supervisor which manages both nginx and uvicorn
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]