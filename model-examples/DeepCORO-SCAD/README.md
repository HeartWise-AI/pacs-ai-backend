# DeepCORO-SCAD

Research-only PACS.AI inference service for the gated
[`heartwise/deepcoro_clip_scad`](https://huggingface.co/heartwise/deepcoro_clip_scad)
checkpoint. It processes up to four coronary angiography videos from one study and returns the
study-level probability of spontaneous coronary artery dissection (SCAD).

> **Not for clinical use.** The output is a model sigmoid score, not a calibrated clinical risk or a
> diagnosis. A result below the threshold does not exclude SCAD. No regulatory clearance.

## Output

- `scad` head: SCAD probability (sigmoid). Flag **SCAD suspected** when the probability is at or above
  **0.0524009**, a threshold fixed on the validation set to favour sensitivity.
- The checkpoint also contains three auxiliary training heads (`cad`, `other`, `stenosis`). They are
  loaded so the checkpoint loads strictly, but they are not reported.

The API supports `HTML` and `JSON`. The HTML report shows the SCAD probability, threshold, flag,
recommendation and research disclaimer in English and French.

## Held-out test performance

| Set | n | AUROC (95% CI) | Sensitivity | Specificity | NPV |
|---|---|---|---|---|---|
| All sites | 270 | 0.95 (0.91-0.97) | 0.91 | 0.84 | 0.97 |
| Montreal Heart Institute | 229 | 0.87 (0.78-0.94) | 0.69 | 0.85 | 0.97 |

External sites contributed SCAD-positive studies only, so the all-sites estimate can be inflated by
site differences. The model does not localise the lesion.

## Input selection

Training used four clips per study (`num_videos: 4`); the first four clips of each study alternate left
and right coronary views. The service accepts 1 to 4 XA series (`dicomUploadMin: 1`,
`dicomUploadMax: 4`), orders them by DICOM SeriesTime and uses the first four processable videos.
Missing slots are zero-padded and masked out of the mean pooling, so a shorter study is scored on its
real videos only. Upload two left and two right coronary views when possible. If per-series
`additionalMetadata` is supplied, only diagnostic left/right coronary views are kept and the two
territories are interleaved before truncation.

## Pinned artifact and architecture

- Hugging Face repository: `heartwise/deepcoro_clip_scad` (tag `v1.0.0`)
- Revision: `9c03ca0928cd781b82669b8e4cd48f1b7b11ed3d`
- Checkpoint: `20261025-134722/models/best_model_epoch_7.pt`
  (SHA-256 `77ebd8701feddc677abee17e6ebd872016262404277492308ff0319b702c4bfe`)
- State-dict key: `linear_probing`
- Architecture: DeepCORO-CLIP MViT video encoder plus multiple-instance linear probing
- Input: 4 videos, 16 frames, stride 1, resize 224, normalisation mean 98.339 / std 33.891
- Pooling: masked mean over videos, 512-dimensional embeddings
- Training run: W&B `mhi_ai/DeepCORO_SCAD` run `msn8m3rv`; code HeartWise-AI/DeepCORO_CLIP

`models/config.json` records the effective constructor values and normalisation statistics. Model
loading is strict, so a head or architecture mismatch fails startup.

## Build

The repository is gated. Official images are built after merge by the protected release workflow (see
`docs/model-image-release.md`). For a local candidate build:

```bash
python3 scripts/release-model.py model-examples/DeepCORO-SCAD \
  --image-repository heartwisehub/pacs-ai-deepcoro-scad \
  --hf-token-file /secure/path/hf_token
```

`Dockerfile.local` builds from a checkpoint already on disk, verifies its SHA-256 and carries the
`local` release-channel label. It is for development only.

## Validation before deployment

1. Run `pytest model-examples/DeepCORO-SCAD/tests`.
2. Build the image and confirm the checkpoint loads strictly.
3. Confirm `/inference/model-info` and `/inference/model-facts` respond.
4. Run a representative XA study end to end and compare with the reference DeepCORO_CLIP pipeline.
