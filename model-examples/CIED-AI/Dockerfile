FROM tensorflow/serving:2.18.0-gpu

# Install runtime tools. Ubuntu 20.04 repositories used by the TensorFlow
# Serving image no longer provide the deadsnakes Python 3.11 packages, so use
# uv's managed Python distribution instead.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    nginx \
    supervisor \
    build-essential \
    && rm -rf /var/lib/apt/lists/*

RUN curl -LsSf https://astral.sh/uv/install.sh | sh \
    && /root/.local/bin/uv venv --python 3.11 /opt/venv
ENV PATH="/opt/venv/bin:/root/.local/bin:${PATH}"

# Create necessary directories
WORKDIR /app

# Copy requirements first to leverage Docker cache
COPY requirements.txt .
RUN uv pip install --python /opt/venv/bin/python --no-cache -r requirements.txt

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

# Expose port 80
EXPOSE 80

# Override default entrypoint from tensorflow/serving
ENTRYPOINT []

# Start supervisor
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
