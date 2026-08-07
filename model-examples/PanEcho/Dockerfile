FROM python:3.12.1-slim

ARG DOWNLOAD_VIEW_CLASSIFIER_WEIGHTS=false

RUN apt-get update && apt-get install -y --no-install-recommends nginx supervisor git curl

# Install uv
RUN pip install uv

# Create necessary directories
WORKDIR /app

# Copy requirements first to leverage Docker cache
COPY requirements.txt .

# Use uv to install dependencies
RUN uv pip install --system -r requirements.txt

# Optional view-classifier weights download
COPY download_model.py .
RUN --mount=type=secret,id=hf_token,required=false \
    if [ "$DOWNLOAD_VIEW_CLASSIFIER_WEIGHTS" = "true" ]; then \
        if [ ! -f /run/secrets/hf_token ]; then \
            echo "Missing hf_token secret while DOWNLOAD_VIEW_CLASSIFIER_WEIGHTS=true"; \
            exit 1; \
        fi; \
        python download_model.py --token "$(cat /run/secrets/hf_token)" --output-dir weights; \
    else \
        mkdir -p weights; \
        echo "Skipping optional PanEcho view-classifier weight download"; \
    fi

# Copy application files
COPY main.py .
COPY logic.py .
COPY config.json .
COPY data/ ./data/
COPY docs/ ./docs/
COPY content/ ./content/
COPY models/ ./models/
COPY weights/ ./weights/
COPY utils/ ./utils/
COPY nginx.conf /etc/nginx/nginx.conf
COPY supervisord.conf /etc/supervisord.conf

# Copy init_model.py
COPY init_model.py .

# Run init_model.py to download pretrained pytorch video weights
RUN python init_model.py

# Expose port 80
EXPOSE 8000

# Override default entrypoint from tensorflow/serving
ENTRYPOINT []

# Start supervisor
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
