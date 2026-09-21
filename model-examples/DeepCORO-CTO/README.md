# DeepCORO-CTO

DeepCORO-CLIP based **imaging J-CTO scorer** for chronic total occlusions on coronary angiography.
From up to 10 videos per study it predicts, **per artery (LAD, RCA, LCx)**, the four morphological
J-CTO components and an aggregate imaging score clamped to `[0, 4]`, then reports the artery with
the highest predicted score as the CTO artery. This is **not** the classic Morino J-CTO score
(0–5): the fifth point for a previously failed crossing attempt is not available from imaging
and is not predicted. The model is **view-aware**: each video's angiographic view class is
inferred at inference time from the DICOM positioner primary/secondary angles
(same rule as `DeepCORO_CLIP_DATASET/classify_angles.py`) and fed to a view embedding.

Model weights are hosted on HuggingFace at [heartwise/DeepCORO_CTO](https://huggingface.co/heartwise/DeepCORO_CTO)
and fetched at Docker build time via `--mount=type=secret,id=hf_token`. `download_model.py` pins
the HF revision (`v2.0` by default; override with `DEEPCORO_CTO_HF_REVISION` or `--revision`).

| Version | HF tag | Checkpoint | Heads | Videos |
| --- | --- | --- | --- | --- |
| 2.0.0 (current) | `v2.0` | `best_model_epoch_18.pt` (W&B `dg1o32md`) | 15 per-artery (`{blunt_stump, calcification, bending_gt45, occlusion_length_gt20, score}` × `{lad, rca, lcx}`) | up to 10 |
| 1.0.0 | `v1.0` | `best_model_epoch_24.pt` (W&B `pdnzpnpy`) | 5 overall | up to 8 |

`logic.py` handles both layouts: with per-artery heads it selects the artery with the highest
predicted J-CTO score and aliases its heads onto the generic names; with overall heads it passes
them through unchanged. Two alternative overall checkpoints live under `alt_models/` on HF
(excluded from the image) — see `docs/RESULTS.md`.

## Build & push

Put your HuggingFace read token in `hf_token.txt` (gitignored via `*.txt`):

```
echo "<your_hf_token>" > hf_token.txt
docker build --secret id=hf_token,src=./hf_token.txt -t heartwisehub/deepcoro-cto:2.0.0 .
docker push heartwisehub/deepcoro-cto:2.0.0
```

## Run

```
# CPU
docker run -p 8000:8000 heartwisehub/deepcoro-cto:2.0.0

# GPU
docker run -p 8000:8000 --gpus all heartwisehub/deepcoro-cto:2.0.0

# Interactive shell on pacs-net
docker run -it --network pacs-net --gpus all --entrypoint /bin/bash heartwisehub/deepcoro-cto:2.0.0
```

## Inputs

- Coronary angiogram DICOMs (modality `XA`).
- Up to 10 videos per study (extras are ignored after sorting by SeriesTime).
- No Step-2 variables page (`supportedAdditionalMetadata: []`); view class is derived
  automatically from `PositionerPrimaryAngle`/`PositionerSecondaryAngle`.

## Outputs

- `JSON` and `HTML` output modes.
- `diagnosis` — e.g. `DeepCORO-CTO [RCA]: J-CTO score = 2 (raw 2.1, difficult) | Calcification at occlusion, Bending > 45°`.
- `predictions.jctoScore.predicted` — imaging J-CTO score of the selected artery (regression, clamped to [0, 4]).
- `predictions.jctoScore.predictedInteger` — the same score rounded to the nearest whole point (half rounds up).
- `predictions.jctoScore.componentsAboveThreshold` — count of morphological heads with
  probability ≥ threshold (may differ from the regression score).
- `predictions.components.<head>.probability` — P(component present) after sigmoid for the selected
  artery, for `jcto_blunt_stump`, `jcto_calcification`, `jcto_bending_gt45`, `jcto_occlusion_length_gt20`.
- `predictions.ctoArtery` — `LAD` / `RCA` / `LCx` (per-artery checkpoints only).
- `predictions.perArtery.<artery>` — raw `jctoScore`, `jctoScoreInteger` and component
  probabilities for every artery (per-artery checkpoints only).

## Deploy

```
./scripts/deploy-model.sh model-examples/DeepCORO-CTO
```

Uses `scripts/.env.deploy` and an HF token file for the gated weight download. The image tag
defaults to `.version` from `data/model_info.json` (`2.0.0`).

## Tests

```
pytest model-examples/DeepCORO-CTO/tests
```

## Validation (MHI held-out test split, 105 studies, 2026-09-21 refresh)

Within-artery AUROC (studies whose CTO is in that artery), 95% bootstrap CI. Full tables,
including the all-studies numbers and the frozen-embedding probes, are in `docs/RESULTS.md`.

| Head | LAD (n=37) | RCA (n=73) | LCx (n=20) |
| --- | --- | --- | --- |
| Blunt / flush stump | 0.75 (0.58-0.90) | 0.65 (0.51-0.78) | 0.38 (0.12-0.66) |
| Calcification | 0.68 (0.50-0.84) | 0.79 (0.67-0.88) | 0.65 (0.37-0.88) |
| Bending > 45° | n/a (no test positives) | 0.56 (0.37-0.73) | n/a |
| Occlusion length ≥ 20 mm | 0.76 (0.59-0.92) | 0.79 (0.65-0.90) | 0.45 (0.20-0.71) |
| J-CTO score ≥ 3 | 0.75 (0.55-0.93) | 0.74 (0.62-0.85) | 0.56 (0.24-0.86) |

CTO artery localisation AUROC: LAD 0.86, RCA 0.92, LCx 0.79. LCx grading is not reliable
(20 test / 108 training CTOs).

Research preview — not validated for clinical use outside MHI.
