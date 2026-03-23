FROM python:3.12.1-slim

RUN apt-get update && apt-get install -y --no-install-recommends nginx supervisor

# Create necessary directories
WORKDIR /app

# Copy requirements first to leverage Docker cache
COPY requirements.txt .
RUN pip install -r requirements.txt

# Copy application files
COPY main.py .
COPY logic.py .
COPY dataset.json .
COPY plans.json .
COPY predict_from_raw_data_args.json .
COPY config.json .
COPY data/ ./data/
COPY docs/ ./docs/
COPY models/ ./models/
COPY utils/ ./utils/
COPY nginx.conf /etc/nginx/nginx.conf
COPY supervisord.conf /etc/supervisord.conf

ENV nnUNet_results=/app/models
ENV nnUNet_raw=/app/nnUNet_raw
ENV nnUNet_preprocessed=/app/nnUNet_preprocessed


# Expose port 80
EXPOSE 80

# Override default entrypoint from tensorflow/serving
ENTRYPOINT []

# Start supervisor
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
