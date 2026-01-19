FROM nvidia/cuda:12.1.1-cudnn8-runtime-ubuntu22.04

# 2. On installe Python manuellement (car l'image Nvidia est vide)
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3.10 \
    python3-pip \
    python3-dev \
    git \
    && rm -rf /var/lib/apt/lists/*

# On crée un lien pour que "python" lance bien python3.10
RUN ln -s /usr/bin/python3 /usr/bin/python

WORKDIR /app

# 3. Installation des dépendances (Beaucoup plus rapide et propre)
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# 4. Copie de ton code
COPY main.py .
COPY config.json .
COPY utils/ ./utils/
COPY otter/ ./otter/
COPY docs/ ./docs/

# 5. Placeholders pour le dev (écrasés par les volumes -v)
COPY logic.py .
COPY pipeline_script.py .

# Création du dossier pour les checkpoints
RUN mkdir -p /app/models/OTTER_CLIP_BRAINGPT_hf

EXPOSE 8000
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]