# DeepCORO-Integrated

**Research use only.**

DeepCORO-Integrated is one EchoJEPA ViT-L video encoder that segments the coronary tree itself. It reads a diagnostic
angiography study end to end:

- % diameter stenosis and P(significant lesion) for **18 coronary segments**;
- **LAD FFR / iFR**: value and P(abnormal), from v12 on;
- **calcification / thrombus / CTO** probabilities per segment, once a lesion-head checkpoint (v13) is released.

The encoder runs **at most twice per study**:

1. Full runs at 224 px, from which the model predicts its own 26-class SYNTAX masks.
2. Up to 4 tracked 320 px crops per segment.

`measured_forward` counts the encoder calls and fails if there are more than two.

Version 1.0.0 serves model **v12**, `heartwise/DeepCORO_Integrated@927beaf` (tag `v12`).

## How it differs from the CLIP models

There is no `VideoMILWrapper` and no zero-video padding.

- **Video set:** the model takes the real runs as a set.
- **Video selection** (`logic.select_videos`):
  1. Sort by **SeriesTime**, because the backend orders by UID suffix.
  2. Keep the first `dicomUploadMax` (12).
  3. Feed them in training order, i.e. by the `<StudyInstanceUID>_<SOPInstanceUID>` filenames the training videos
     carried. Crop selection breaks ties by order.
- **Preprocessing** (`logic.dicom_to_avi`):
  1. Convert the pixel array to an MJPG AVI at the DICOM frame rate, exactly as the training videos were made
     (verified bit-identical on MHI DICOMs).
  2. Read it back as grayscale.
  3. Sample 16 frames around peak opacification.
  4. The model scales frames to [0, 1] and normalizes them internally. There is no `dataset_mean`/`dataset_std`.
- **Weights:** `model.safetensors`, no pickle, plus `model_config.json` (structure). Both come from the same pinned
  revision. `download_model.py --extra-file model_config.json` checks the weights' SHA-256.
- **Model code:** `models/deepcoro_model/` is copied from the DeepCoro research repo. EchoJEPA's ViT code is vendored
  under `echojepa_src/` (MIT).

## Inputs and outputs

- **Upload:** 1-12 XA multi-frame DICOMs, diagnostic left/right coronary contrast runs.
- **No metadata step:** `supportedAdditionalMetadata` is `[]`, so there is no Step-2 page and uploads are not
  filtered. PCI, stent and ventriculography runs were not in training.
- **JSON `predictions`:**
  - `segments.<segment>` with `stenosis_pct`, `prob_significant` and `significant_threshold_pct` (70; left main 50).
  - `lad_physiology` with `ffr`, `ifr`, `ffr_abnormal_prob` (≤ 0.80) and `ifr_abnormal_prob` (≤ 0.89).
  - `n_videos` and `encoder_passes`.
- **HTML:** the segment table plus LAD physiology.

## Verified performance

Held-out MHI test: 3,194 studies / 2,941 patients, with patient-bootstrap 95% CIs.

| | v12 | DeepCORO-CLIP | EchoJEPA |
|---|---|---|---|
| Stenosis macro AUROC (18 segments) | **0.884** [0.877, 0.891] | 0.850 | 0.851 |
| Mean per-segment Pearson r (segments > 0.5) | 0.50 (9/18) | 0.31 (6/18) | 0.44 (7/18) |
| LAD FFR/iFR pooled AUROC (274 measurements) | 0.721 [0.662, 0.786] | — | — |

- The model card on Hugging Face has the full tables and caveats: FFR alone 0.69 (n = 76), iFR alone 0.73, and weaker
  distal/branch segments.
- Running the AVI test inputs through this container's model code reproduces the locked test predictions (max |Δ|
  0.0005).
- **DICOM path:** identical, except for studies whose training videos had been stored as MP4 (13% of test videos).
  Those differ by up to a few points.

## Build and run

```bash
python3 scripts/release-model.py model-examples/DeepCORO-Integrated \
  --image-repository heartwisehub/deepcoro-integrated --hf-token-file /secure/path/hf_token
```

- GPU required: about 1.7 GB resident and 9.5 GB peak, for a 10-video study in bf16.
- **Blackwell (sm_120) GPUs:** cu121 wheels do not run on them. Build with a cu128 torch (≥ 2.7) there.

## Tests

```bash
cd model-examples/DeepCORO-Integrated && python -m pytest -q tests
```

The tests cover:
- video selection and order;
- DICOM → MJPG conversion;
- output postprocessing for v12 and lesion-head checkpoints;
- provenance and weight-hash verification;
- that the OpenAPI examples match `data/`.
