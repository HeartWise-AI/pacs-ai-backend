FROM python:3.12.1-slim

RUN apt-get update && apt-get install -y --no-install-recommends nginx supervisor git curl

# Install uv
RUN pip install uv

# Create necessary directories
WORKDIR /app

# Copy requirements first to leverage Docker cache
COPY requirements.txt .

# Use uv to install dependencies
RUN uv pip install --system -r requirements.txt

# Copy application files
COPY main.py .
COPY logic.py .
COPY config.json .
COPY data/ ./data/
COPY docs/ ./docs/
COPY content/ ./content/
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
