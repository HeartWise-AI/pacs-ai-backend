FROM python:3.12.1-slim

# Install nginx and supervisor
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    nginx \
    supervisor \
    && rm -rf /var/lib/apt/lists/*

RUN pip install uv

WORKDIR /app

# Copy requirements first to leverage Docker cache
COPY requirements.txt .

# Use uv instead of pip for much faster installation
RUN uv pip install -r requirements.txt --system

# Copy the model download script
COPY download_model.py .
RUN --mount=type=secret,id=hf_token \
    python download_model.py --token $(cat /run/secrets/hf_token)

# # Copy application files
COPY main.py .
COPY logic.py .
COPY data/ ./data/
COPY docs/ ./docs/
COPY models/ ./models/
COPY utils/ ./utils/
COPY nginx.conf /etc/nginx/nginx.conf
COPY supervisord.conf /etc/supervisord.conf

# Copy configuration files
COPY nginx.conf /etc/nginx/nginx.conf
COPY supervisord.conf /etc/supervisord.conf

# Expose port 8000
EXPOSE 8000

# Start supervisor
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]