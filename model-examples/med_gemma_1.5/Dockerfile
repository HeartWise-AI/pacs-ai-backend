FROM python:3.12.1-slim

RUN apt-get update && apt-get install -y --no-install-recommends nginx supervisor git curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app    

# Install huggingface-cli
RUN curl -LsSf https://hf.co/cli/install.sh | bash

# Install uv and huggingface-cli
RUN pip install uv

# Copy requirements first to leverage Docker cache
COPY requirements.txt .

# Use uv to install dependencies
RUN uv pip install --system -r requirements.txt

ARG MODEL_ID=google/medgemma-1.5-4b-it
ENV MODEL_PATH=/models/medgemma

# Mount secret at build time - not stored in layer
RUN --mount=type=secret,id=hf_token \
    hf download ${MODEL_ID} \
    --local-dir ${MODEL_PATH} \
    --token $(cat /run/secrets/hf_token)

# Copy application files
COPY main.py .
COPY logic.py .
COPY config.json .
COPY data/ ./data/
COPY docs/ ./docs/
COPY model/ ./model/
COPY utils/ ./utils/
COPY nginx.conf /etc/nginx/nginx.conf
COPY supervisord.conf /etc/supervisord.conf

EXPOSE 8000

# Override default entrypoint from tensorflow/serving
ENTRYPOINT []

# Start supervisor
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]