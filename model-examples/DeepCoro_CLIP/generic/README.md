# DeepCoro-CLIP generic

DeepCoro-CLIP model for vessel-level coronary angiogram findings, including
stenosis, calcification, chronic total occlusion, and thrombus.

Model weights are hosted on Hugging Face at
[heartwise/DeepCoro_CLIP_generic](https://huggingface.co/heartwise/DeepCoro_CLIP_generic)
and fetched at Docker build time from the immutable revision recorded in
`data/model_info.json`. The build verifies the selected checkpoint against its
declared SHA-256 before creating the image.

The checked-in `models/config.json` is based on `config.json` from that pinned
Hugging Face revision (Git blob `66931ca58515454cd430ed9b1bb58f8d5c8de5f1`).
It also makes the architecture and normalization defaults used by the existing
1.0.0 deployment explicit, so those settings cannot drift with future code.

## Official release

Official images are built from reviewed `master` commits by the protected
`Publish inference model image` GitHub Actions workflow. Select
`model-examples/DeepCoro_CLIP/generic` and leave `publish_latest` disabled. The
workflow publishes the immutable semantic-version tag, records its digest, and
registers the verified artifact in the PACS-AI model catalog.

Do not build or push the official tag manually and do not overwrite the legacy
`heartwisehub/pacs-ai-deepcoro-clip-generic:1.0` image.

## Local build

For local development, place the verified checkpoint in its configured path and
build the explicitly local image:

```bash
mkdir -p models/zcb8cu0l_20260213-033432
cp /path/to/best_model_epoch_11.pt models/zcb8cu0l_20260213-033432/
docker build --platform linux/amd64 -f Dockerfile.local -t deepcoro-clip-generic:local .
```

The local Dockerfile verifies the checkpoint fingerprint and marks the image
with `ai.heartwise.release.channel=local`; it is not an official release path.

## Run

```bash
# CPU
docker run -p 8000:8000 deepcoro-clip-generic:local

# GPU
docker run -p 8000:8000 --gpus all deepcoro-clip-generic:local

# Interactive shell on pacs-net
docker run -it --network pacs-net --gpus all --entrypoint /bin/bash deepcoro-clip-generic:local
```
