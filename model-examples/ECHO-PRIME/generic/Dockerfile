FROM nvcr.io/nvidia/pytorch:24.01-py3

# Install nginx and supervisor
RUN apt-get update && apt-get install -y --no-install-recommends \
    nginx \
    supervisor \
    && rm -rf /var/lib/apt/lists/*

ARG USE_FULL_DICOM=false
ENV USE_FULL_DICOM=${USE_FULL_DICOM}

WORKDIR /app

# Copy requirements first to leverage Docker cache
COPY requirements.txt .
RUN apt-get update
RUN python -m pip install --upgrade pip
RUN python -m pip uninstall -y \
    opencv-python \
    opencv-python-headless \
    opencv-contrib-python \
    opencv-contrib-python-headless || true
RUN rm -rf \
    /usr/local/lib/python3.10/dist-packages/cv2 \
    /usr/local/lib/python3.10/dist-packages/cv2.abi3.so \
    /usr/local/lib/python3.10/dist-packages/opencv_python* \
    /usr/local/lib/python3.10/dist-packages/opencv_contrib_python* || true
RUN pip install --no-cache-dir -r requirements.txt
RUN rm -rf /usr/local/lib/python3.10/dist-packages/cv2/typing || true
RUN python -c "import cv2, numpy; print('cv2', cv2.__version__, 'numpy', numpy.__version__)"

# Copy application files
COPY main.py .
COPY EchoPrime.py .
COPY logic.py .
COPY config.json .
COPY data/ ./data/
COPY docs/ ./docs/
COPY models/ ./models/
COPY utils/ ./utils/

# Copy configuration files
COPY nginx.conf /etc/nginx/nginx.conf
COPY supervisord.conf /etc/supervisord.conf

# Expose port 80
EXPOSE 80

# Start supervisor
CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
