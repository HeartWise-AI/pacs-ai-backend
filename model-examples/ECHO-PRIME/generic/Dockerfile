FROM nvcr.io/nvidia/pytorch:24.01-py3

ARG PACS_AI_SOURCE_REPOSITORY
ARG PACS_AI_SOURCE_REVISION
ARG PACS_AI_MODEL_VERSION
ARG PACS_AI_MODEL_REPOSITORY
ARG PACS_AI_MODEL_REVISION
ARG PACS_AI_WEIGHTS_PATH
ARG PACS_AI_WEIGHTS_SHA256
ARG USE_FULL_DICOM=false

ENV USE_FULL_DICOM=${USE_FULL_DICOM}
ENV PIP_INDEX_URL="https://pypi.org/simple" \
    PIP_EXTRA_INDEX_URL="" \
    PIP_CONFIG_FILE="/dev/null"

RUN apt-get update && apt-get install -y --no-install-recommends \
    nginx \
    supervisor \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt .
RUN python -m pip --isolated install --index-url https://pypi.org/simple --upgrade pip \
    && python -m pip --isolated uninstall -y \
      opencv-python \
      opencv-python-headless \
      opencv-contrib-python \
      opencv-contrib-python-headless || true
RUN rm -rf \
      /usr/local/lib/python3.10/dist-packages/cv2 \
      /usr/local/lib/python3.10/dist-packages/cv2.abi3.so \
      /usr/local/lib/python3.10/dist-packages/opencv_python* \
      /usr/local/lib/python3.10/dist-packages/opencv_contrib_python* || true
RUN python -m pip --isolated install --index-url https://pypi.org/simple --no-cache-dir -r requirements.txt \
    && rm -rf /usr/local/lib/python3.10/dist-packages/cv2/typing \
    && python -c "import cv2, numpy; print('cv2', cv2.__version__, 'numpy', numpy.__version__)"

COPY download_model.py .
RUN python download_model.py \
      --repository "${PACS_AI_MODEL_REPOSITORY}" \
      --revision "${PACS_AI_MODEL_REVISION}" \
      --weights-path "${PACS_AI_WEIGHTS_PATH}" \
      --weights-sha256 "${PACS_AI_WEIGHTS_SHA256}" \
      --output-dir models

COPY main.py .
COPY EchoPrime.py .
COPY logic.py .
COPY config.json .
COPY data/ ./data/
COPY docs/ ./docs/
COPY models/ ./models/
COPY utils/ ./utils/
COPY nginx.conf /etc/nginx/nginx.conf
COPY supervisord.conf /etc/supervisord.conf

ENV PACS_AI_SOURCE_REVISION="${PACS_AI_SOURCE_REVISION}"
ENV PACS_AI_MODEL_VERSION="${PACS_AI_MODEL_VERSION}"

LABEL org.opencontainers.image.title="EchoPrime" \
      org.opencontainers.image.version="${PACS_AI_MODEL_VERSION}" \
      org.opencontainers.image.revision="${PACS_AI_SOURCE_REVISION}" \
      org.opencontainers.image.source="https://github.com/${PACS_AI_SOURCE_REPOSITORY}" \
      ai.heartwise.model.repository="${PACS_AI_MODEL_REPOSITORY}" \
      ai.heartwise.model.revision="${PACS_AI_MODEL_REVISION}" \
      ai.heartwise.model.weights.path="${PACS_AI_WEIGHTS_PATH}" \
      ai.heartwise.model.weights.sha256="${PACS_AI_WEIGHTS_SHA256}"

EXPOSE 80

CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
