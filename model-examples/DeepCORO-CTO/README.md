# DeepCORO-CTO

DeepCORO-CLIP based **imaging J-CTO scorer** for chronic total occlusions on coronary angiography.
From up to 8 videos per study it predicts the four morphological J-CTO components and an
aggregate imaging score clamped to `[0, 4]`. This is **not** the classic Morino J-CTO score
(0–5): the fifth point for a previously failed crossing attempt is not available from imaging
and is not predicted. The model is **view-aware**: each video's angiographic view class is
inferred at inference time from the DICOM positioner primary/secondary angles
(same rule as `DeepCORO_CLIP_DATASET/classify_angles.py`) and fed to a view embedding.

Model weights are hosted on HuggingFace at [heartwise/DeepCORO_CTO](https://huggingface.co/heartwise/DeepCORO_CTO)
and fetched at Docker build time via `--mount=type=secret,id=hf_token`.

## Build & push

Put your HuggingFace read token in `hf_token.txt` (gitignored via `*.txt`):

```
echo "<your_hf_token>" > hf_token.txt
docker build --secret id=hf_token,src=./hf_token.txt -t heartwisehub/deepcoro-cto:1.0 .
docker push heartwisehub/deepcoro-cto:1.0
```

## Run

```
# CPU
docker run -p 8000:8000 heartwisehub/deepcoro-cto:1.0

# GPU
docker run -p 8000:8000 --gpus all heartwisehub/deepcoro-cto:1.0

# Interactive shell on pacs-net
docker run -it --network pacs-net --gpus all --entrypoint /bin/bash heartwisehub/deepcoro-cto:1.0
```

## Inputs

- Coronary angiogram DICOMs (modality `XA`).
- Up to 8 videos per study (extras are ignored after sorting by SeriesTime).
- No Step-2 variables page (`supportedAdditionalMetadata: []`); view class is derived
  automatically from `PositionerPrimaryAngle`/`PositionerSecondaryAngle`.

## Outputs

- `JSON` and `HTML` output modes.
- `predictions.jctoScore.predicted` — imaging J-CTO score (regression, clamped to [0, 4]).
- `predictions.jctoScore.componentsAboveThreshold` — count of morphological heads with
  probability ≥ threshold (may differ from the regression score).
- `predictions.components.<head>.probability` — P(component present) after sigmoid, for
  `jcto_blunt_stump`, `jcto_calcification`, `jcto_bending_gt45`, `jcto_occlusion_length_gt20`.

## Deploy

```
./scripts/deploy-model.sh model-examples/DeepCORO-CTO
```

Uses `scripts/.env.deploy` and an HF token file for the gated weight download.

## Validation (MHI held-out, 99 studies, epoch 24)

| Head | AUROC (95% CI) |
| --- | --- |
| Blunt / flush stump | 0.70 (0.59-0.80) |
| Calcification | 0.70 (0.59-0.80) |
| Bending > 45° | 0.65 (0.50-0.79) |
| Occlusion length ≥ 20 mm | 0.66 (0.53-0.79) |
| J-CTO score ≥ 3 | 0.68 (0.57-0.79), MAE 1.01 |

Research preview — not validated for clinical use outside MHI.
