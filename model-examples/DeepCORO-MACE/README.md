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

The repository is gated. Build with an approved Hugging Face token supplied as a BuildKit secret:

```bash
docker build \
  --secret id=hf_token,src=../../hf_token.txt \
  -t heartwisehub/pacs-ai-deepcoro-mace:1.0.0 .
```

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
