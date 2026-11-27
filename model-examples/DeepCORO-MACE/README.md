# DeepCORO-MACE

Research-only PACS.AI inference service for the gated
[`heartwise/deepcoro_clip_mace`](https://huggingface.co/heartwise/deepcoro_clip_mace)
checkpoint. It processes up to three coronary angiography videos and returns nine one-year
cardiovascular outcome scores.

> **Not for clinical use.** The outputs are model sigmoid scores, not calibrated absolute risks
> or diagnoses. The 0.5 flags are uncalibrated research thresholds. No regulatory clearance.

## Outputs

The report emphasizes the composite MACE, urgent revascularization, non-fatal myocardial
infarction, and complete coronary occlusion heads. Five low-event component heads are labelled
exploratory: cardiovascular death, non-fatal stroke, heart-failure hospitalization,
life-threatening arrhythmia, and cardiogenic shock. Cardiovascular death had only four events in
the held-out cohort and must not be interpreted as a reliable standalone estimate.

The API supports `HTML` and `JSON`. The HTML report presents endpoint labels, recommendations, and
research/calibration warnings in English and French. JSON keeps the primary and exploratory
outputs separate and includes the threshold and `aboveResearchThreshold` flag for every endpoint.

## Pinned artifact and architecture

- Hugging Face repository: `heartwise/deepcoro_clip_mace`
- Revision: `7be030d1c7b62e41f45a4dd35adff9dae21d884f`
- Checkpoint: `11zt0zl5_20250723-162407/models/best_model_epoch_18.pt`
- State-dict key: `linear_probing`
- Architecture: MViT video encoder plus multiple-instance linear probing
- Input: 3 videos, 16 frames, stride 1, resize 224
- Pooling: CLS token, 512-dimensional embeddings, 8 attention heads

`models/config.json` records the effective constructor values and normalization statistics. Model
loading is strict so a head or architecture mismatch fails startup instead of silently serving a
partially initialized model.

## Build

The repository is gated. Official images are built after merge by the protected release workflow (see
`docs/model-image-release.md`). For a local candidate build, which supplies the release build arguments
from `data/model_info.json`:

```bash
python3 scripts/release-model.py model-examples/DeepCORO-MACE \
  --image-repository heartwisehub/pacs-ai-deepcoro-mace \
  --hf-token-file /secure/path/hf_token
```

A plain `docker build` without the `PACS_AI_MODEL_*` / `PACS_AI_WEIGHTS_*` build arguments fails at the
weights download by design. `Dockerfile.local` builds from a checkpoint already on disk, verifies its
SHA-256 and carries the `local` release-channel label. It is for development only.

From the backend root, the standard deployment helper can build, push, and register the model:

```bash
DEFAULT_OUTPUT_MODE=HTML ./scripts/deploy-model.sh model-examples/DeepCORO-MACE
```

The token and downloaded checkpoint are excluded from the Docker build context and Git.

## Validation before deployment

1. Run the unit and documentation-contract tests.
2. Build the image and confirm the checkpoint loads strictly.
3. Confirm `/inference/model-info` and `/inference/model-facts` respond.
4. Run a representative XA study end to end and have the research team reconcile the outputs
   against the reference inference pipeline before production registration.

## Report of a stored result

`POST /inference/render-report` with `{"result": <data of a JSON-mode predict response>}` returns
`{"htmlBase64", "modelVersion"}`: the same HTML document HTML-mode predict returns for that prediction.
It runs on the CPU, does not load the model, and does not count as model activity.
`GET /inference/model-info` advertises it as `capabilities.renderReport: true`.

Nothing stored is copied into the page. The stored numbers are checked (type, finite, range, decimals),
then `CustomPredictionService.report_html` looks for a raw model output whose JSON output, regenerated
by the predict code, is the stored result, and renders the report from it. A result whose values or
text are not what this model version writes for those numbers (an edited recommendation, a result of
another model version with other thresholds) is refused with 422 (`INVALID_RESULT`); a body over
64 KB is refused with 413.

## Changelog

- 1.0.4: security patch: PyTorch 2.13.0 (CUDA 12.6 build), torchvision 0.28.0, closing the open Dependabot
  PyTorch advisories (incl. critical GHSA-53q9-r3pm-6pq6 and high GHSA-63cw-57p8-fm3p). Unpinned Python
  dependencies are now pinned to the versions of the previous image. Same weights, preprocessing, thresholds
  and report; predictions checked against the previous image on synthetic and real XA studies.
- 1.0.3: adds `POST /inference/render-report` and `capabilities.renderReport` in model-info.
  No change to the model, weights, preprocessing, predictions or live report; the version is
  bumped because an image of 1.0.2 is already published.
