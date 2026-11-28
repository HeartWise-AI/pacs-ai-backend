# EchoPrime 20-view classifier checkpoint, pinned by image digest (see download_model.py).
FROM heartwisehub/echo-prime_view-classifier@sha256:b2f785a4efe3eb7fd4377a8cba7ef40fce65631356b091c2a28a2c47db5d91e0 AS view-classifier

FROM python:3.12.1-slim

ARG PACS_AI_SOURCE_REPOSITORY
ARG PACS_AI_SOURCE_REVISION
ARG PACS_AI_MODEL_VERSION
ARG PACS_AI_MODEL_REPOSITORY
ARG PACS_AI_MODEL_REVISION
ARG PACS_AI_WEIGHTS_PATH
ARG PACS_AI_WEIGHTS_SHA256

RUN apt-get update && apt-get install -y --no-install-recommends \
    nginx \
    supervisor \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY download_model.py .
RUN python download_model.py \
      --repository "${PACS_AI_MODEL_REPOSITORY}" \
      --revision "${PACS_AI_MODEL_REVISION}" \
      --weights-path "${PACS_AI_WEIGHTS_PATH}" \
      --weights-sha256 "${PACS_AI_WEIGHTS_SHA256}" \
      --output-dir content

COPY --from=view-classifier /app/weights/best_model_pretrained_echoprime_updated.pth weights/best_model_pretrained_echoprime_updated.pth
RUN python download_model.py --verify-view-classifier weights/best_model_pretrained_echoprime_updated.pth

COPY main.py .
COPY logic.py .
COPY config.json .
COPY data/ ./data/
COPY docs/ ./docs/
COPY models/ ./models/
RUN mv content/panecho.pt content/tasks.pkl models/ && rmdir content
COPY utils/ ./utils/
COPY nginx.conf /etc/nginx/nginx.conf
COPY supervisord.conf /etc/supervisord.conf

ENV PACS_AI_SOURCE_REVISION="${PACS_AI_SOURCE_REVISION}"
ENV PACS_AI_MODEL_VERSION="${PACS_AI_MODEL_VERSION}"

LABEL org.opencontainers.image.title="PanEcho" \
      org.opencontainers.image.version="${PACS_AI_MODEL_VERSION}" \
      org.opencontainers.image.revision="${PACS_AI_SOURCE_REVISION}" \
      org.opencontainers.image.source="https://github.com/${PACS_AI_SOURCE_REPOSITORY}" \
      ai.heartwise.model.repository="${PACS_AI_MODEL_REPOSITORY}" \
      ai.heartwise.model.revision="${PACS_AI_MODEL_REVISION}" \
      ai.heartwise.model.weights.path="${PACS_AI_WEIGHTS_PATH}" \
      ai.heartwise.model.weights.sha256="${PACS_AI_WEIGHTS_SHA256}" \
      ai.heartwise.model.view-classifier.source="heartwisehub/echo-prime_view-classifier@sha256:b2f785a4efe3eb7fd4377a8cba7ef40fce65631356b091c2a28a2c47db5d91e0" \
      ai.heartwise.model.view-classifier.sha256="5bf6010b17c945b44a8d8890baf02995bfb00ed932c5adbda031c2b4f5174af2"

EXPOSE 80

ENTRYPOINT []

CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
