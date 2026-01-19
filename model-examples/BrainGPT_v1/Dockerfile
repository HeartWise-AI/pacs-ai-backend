FROM nvidia/cuda:12.1.1-cudnn8-runtime-ubuntu22.04


RUN apt-get update && apt-get install -y --no-install-recommends \
    python3.10 \
    python3-pip \
    python3-dev \
    git \
    && rm -rf /var/lib/apt/lists/*

RUN ln -s /usr/bin/python3 /usr/bin/python

WORKDIR /app


COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt


COPY main.py .
COPY config.json .
COPY utils/ ./utils/
COPY otter/ ./otter/
COPY docs/ ./docs/
COPY logic.py .
COPY pipeline_script.py .

# Création du dossier pour les checkpoints
RUN mkdir -p /app/models/OTTER_CLIP_BRAINGPT_hf

EXPOSE 8000
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]