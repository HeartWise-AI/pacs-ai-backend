FROM python:3.12.1-slim

RUN apt-get update && apt-get install -y --no-install-recommends nginx supervisor git

# Install uv
RUN pip install uv

# Create necessary directories
WORKDIR /app

# Copy requirements first to leverage Docker cache
COPY requirements.txt .
RUN uv pip install -r requirements.txt --system

ARG HF_API_KEY

# Copy the model download script
COPY download_model.py .
RUN python download_model.py --token ${HF_API_KEY}

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
EXPOSE 8000

# Override default entrypoint from tensorflow/serving
ENTRYPOINT []

# Start supervisor
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
