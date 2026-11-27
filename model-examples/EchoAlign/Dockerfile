# EchoAlign (research use only, CC BY-NC 4.0). Weights and probes are fetched from Hugging Face at a
# pinned revision and verified by sha256 at build time; nothing is committed to git.
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
      --output-dir models

COPY main.py logic.py config.json README.md ./
COPY data/ ./data/
COPY docs/ ./docs/
COPY models/ ./models/
COPY utils/ ./utils/
COPY tests/ ./tests/
COPY nginx.conf /etc/nginx/nginx.conf
COPY supervisord.conf /etc/supervisord.conf

ENV PACS_AI_SOURCE_REVISION="${PACS_AI_SOURCE_REVISION}"
ENV PACS_AI_MODEL_VERSION="${PACS_AI_MODEL_VERSION}"
ENV OMP_NUM_THREADS=6 MKL_NUM_THREADS=6 HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1

LABEL org.opencontainers.image.title="EchoAlign (research use only)" \
      org.opencontainers.image.description="Research use only, not for diagnosis or treatment. EchoAlign by Aly, Taratynova, Mecharbat, Sandler, Sokol, Penn, Maier-Hein, Saeed, Yaqub (MBZUAI BioMedIA, iCardio.ai, DKFZ), arXiv:2611.27517." \
      org.opencontainers.image.licenses="CC-BY-NC-4.0" \
      org.opencontainers.image.version="${PACS_AI_MODEL_VERSION}" \
      org.opencontainers.image.revision="${PACS_AI_SOURCE_REVISION}" \
      org.opencontainers.image.source="https://github.com/${PACS_AI_SOURCE_REPOSITORY}" \
      ai.heartwise.model.license="CC BY-NC 4.0, research use only" \
      ai.heartwise.model.research-only="true" \
      ai.heartwise.model.upstream.code="https://github.com/BioMedIA-MBZUAI/EchoAlign@7f0d78a9285f4c0272de01c9164415517cb664ad" \
      ai.heartwise.model.upstream.weights="https://huggingface.co/EchoAlign/EchoAlign@7ceeb3e3e3dc342b4e36ecf0312a50edb120adde" \
      ai.heartwise.model.attribution="EchoAlign, MBZUAI BioMedIA / iCardio.ai / DKFZ, arXiv:2611.27517" \
      ai.heartwise.model.repository="${PACS_AI_MODEL_REPOSITORY}" \
      ai.heartwise.model.revision="${PACS_AI_MODEL_REVISION}" \
      ai.heartwise.model.weights.path="${PACS_AI_WEIGHTS_PATH}" \
      ai.heartwise.model.weights.sha256="${PACS_AI_WEIGHTS_SHA256}"

EXPOSE 80

ENTRYPOINT []

CMD ["/usr/bin/supervisord", "-c", "/etc/supervisord.conf"]
