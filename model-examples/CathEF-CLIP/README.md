# CathEF-CLIP

DeepCORO-CLIP based LVEF estimator + binary reduced-EF classifier from left coronary angiograms.

Model weights are hosted on HuggingFace at [heartwise/CathEF_CLIP](https://huggingface.co/heartwise/CathEF_CLIP)
and fetched at Docker build time from the immutable revision recorded in
`data/model_info.json`. The build verifies `best_model_epoch_43.pt` against its
declared SHA-256 before creating the image.

## Official release

Official images are built from reviewed `master` commits by the protected
`Publish inference model image` GitHub Actions workflow. Select
`model-examples/CathEF-CLIP` and leave `publish_latest` disabled. The workflow
publishes the immutable semantic-version tag, records its digest, and registers
the verified artifact in the PACS-AI model catalog.

Do not build or push the official tag manually and do not overwrite the legacy
`heartwisehub/pacs-ai-cathef-clip:1.0.0` image.

## Local build

For local development, place the verified checkpoint in `models/` and build the
explicitly local image:

```
cp /path/to/best_model_epoch_43.pt models/
docker build --platform linux/amd64 -f Dockerfile.local -t cathef-clip:local .
```

The local Dockerfile verifies the checkpoint fingerprint and marks the image
with `ai.heartwise.release.channel=local`; it is not an official release path.

## Run

```
# CPU
docker run -p 8000:8000 cathef-clip:local

# GPU
docker run -p 8000:8000 --gpus all cathef-clip:local

# Interactive shell on pacs-net
docker run -it --network pacs-net --gpus all --entrypoint /bin/bash cathef-clip:local
```

## Inputs

- `main_structure` = `Left Coronary`
- `status` = `diagnostic`
- Up to 4 videos per study (extras are ignored after sorting by SeriesTime).

## Outputs

- `JSON` and `HTML` output modes
- `predictions.LVEF.value` — LVEF % (regression, clamped to [0, 100])
- `predictions.reducedEF.probability` — P(LVEF < 40%) after sigmoid
