# DeepCORO-JEPA

**Research use only.**

DeepCORO-JEPA (earlier working name: DeepCORO-Integrated) is one EchoJEPA ViT-L video encoder that segments the coronary tree itself. It reads a diagnostic
angiography study end to end:

- % diameter stenosis, P(significant lesion) and a yes/no flag for **18 coronary segments**;
- **LAD FFR / iFR**: value, P(abnormal) and a yes/no call, from v12 on;
- **calcification / thrombus / CTO** probabilities per segment, once a lesion-head checkpoint is released (none is
  served today).

Segmentation is not a separate step: nothing has to be computed before the model is called. The same forward pass
predicts the masks, cuts the crops and reads them.

The encoder runs **at most twice per study**:

1. Full runs at 224 px, from which the model predicts its own 26-class SYNTAX masks.
2. Up to 4 tracked 320 px crops per segment.

`measured_forward` counts the encoder calls and fails if there are more than two.

Version 1.0.0 serves model **v12**, `heartwise/DeepCORO_JEPA@49c8ddf` (tag `v12.1`: the v12 weights, unchanged, plus the decision thresholds).

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
- **Weights:** `model.safetensors`, no pickle, plus `model_config.json` (structure) and `thresholds.json` (decision
  thresholds). All three come from the same pinned revision:
  `download_model.py --extra-file model_config.json --extra-file thresholds.json` checks the weights' SHA-256, and
  `load_thresholds` refuses a thresholds file that was derived for other weights.
- **Model code:** `models/deepcoro_model/` is copied from the DeepCoro research repo. EchoJEPA's ViT code is vendored
  under `echojepa_src/` (MIT).

## Inputs and outputs

- **Upload:** 1-12 XA multi-frame DICOMs, diagnostic left/right coronary contrast runs.
- **No metadata step:** `supportedAdditionalMetadata` is `[]`, so there is no Step-2 page and uploads are not
  filtered. PCI, stent and ventriculography runs were not in training.
- **JSON `predictions`:**
  - `segments.<segment>`:
    - `stenosis_pct`: estimated % diameter stenosis.
    - `prob_significant`: probability that the report calls the segment significant.
    - `prob_threshold`: the decision threshold of that segment.
    - `significant`: `true` when `prob_significant` is strictly above `prob_threshold`.
    - `significant_threshold_pct`: the label definition behind `prob_significant` (70; left main 50). It is not
      used to make the call.
  - `lad_physiology`:
    - `ffr`, `ifr`: predicted values.
    - `ffr_abnormal_prob`: P(FFR ≤ 0.80); `ifr_abnormal_prob`: P(iFR ≤ 0.89).
    - `ffr_abnormal_threshold`, `ifr_abnormal_threshold`, and the calls `ffr_abnormal`, `ifr_abnormal`.
    - `operating_point`: which pressure thresholds were used.
  - `n_videos` and `encoder_passes`.
- **`diagnosis`:** lists the flagged segments and the two pressure calls.
- **HTML:** the segment table (with threshold and flag) plus LAD physiology.

## Decision thresholds

The thresholds are read from `thresholds.json` of the pinned Hugging Face revision. They were chosen on the
**validation** split only, with the rule of the deployed DeepCORO-CLIP checkpoint: the threshold with the highest
geometric mean of sensitivity and specificity; a call is positive when the probability is strictly above it. The
test split was used only to report the numbers below. FFR ≤ 0.80 and iFR ≤ 0.89 are the label cut-offs the pressure
head was trained on (the cut-offs of FAME and of DEFINE-FLAIR / iFR-SWEDEHEART). The thresholds are internal
operating points, not validated clinical ones.

`models/config.json`:

```json
"Thresholds": {"path": "thresholds.json", "pressure_operating_point": "balanced"}
```

| Key | Meaning | Default |
|---|---|---|
| `path` | Thresholds file inside `models/`, fetched at build | `thresholds.json` |
| `pressure_operating_point` | `balanced`, `high_sensitivity` or `high_specificity`; applies to FFR and iFR. Segments always use `balanced`. | `balanced` |

An unknown key, an unknown operating point, a missing file, a missing output, a threshold outside (0, 1) or a file
derived for other weights stops the container at start-up. There is no fallback value.

LAD pressure calls on the held-out test split (FFR: 76 measurements, 29 abnormal; iFR: 198, 97 abnormal):

| Output | Operating point | Threshold | Sensitivity | Specificity | PPV | NPV |
|---|---|---|---|---|---|---|
| FFR ≤ 0.80 | `balanced` | 0.4396 | 0.59 | 0.70 | 0.55 | 0.73 |
| FFR ≤ 0.80 | `high_sensitivity` | 0.1831 | 0.90 | 0.36 | 0.46 | 0.85 |
| FFR ≤ 0.80 | `high_specificity` | 0.6859 | 0.28 | 0.87 | 0.57 | 0.66 |
| iFR ≤ 0.89 | `balanced` | 0.6141 | 0.61 | 0.73 | 0.69 | 0.66 |
| iFR ≤ 0.89 | `high_sensitivity` | 0.1136 | 0.90 | 0.30 | 0.55 | 0.75 |
| iFR ≤ 0.89 | `high_specificity` | 0.8719 | 0.36 | 0.90 | 0.78 | 0.59 |

Segment flags on the same 3,194 test studies (mean over the 18 segments):

| Rule | Sensitivity | Specificity | PPV | NPV |
|---|---|---|---|---|
| Earlier rule: `stenosis_pct` ≥ 70 (left main 50) | 0.14 | 0.996 | not computed | not computed |
| This version: `prob_significant` above the segment's threshold | 0.81 | 0.80 | 0.25 | 0.98 |

Read the flags with these limits in mind:

- **A flag is a prompt to look, not a diagnosis.** One flagged segment in four is significant in the report
  (PPV 0.25). An unflagged segment is rarely significant (NPV 0.98).
- **Most studies get at least one flag.** At least one segment is flagged in 96% of the studies with a significant
  lesion, and in 39% of the studies without one.
- **Rare segments have very low thresholds** (for example 0.02 for distal LCx and below 0.001 for the posterior
  left ventricular branch), because significant lesions there are rare in the validation split.
- **The earlier percentage rule missed most lesions** because the estimated percentage is pulled toward the mean.
  That is why the flag now comes from the probability.
- **The FFR and iFR calls can disagree.** They are two outputs with two thresholds, and the model does not know
  which wire was used.

## Verified performance

Held-out MHI test: 3,194 studies / 2,941 patients, with patient-bootstrap 95% CIs.

| | v12 | DeepCORO-CLIP | EchoJEPA |
|---|---|---|---|
| Stenosis macro AUROC (18 segments) | **0.884** [0.877, 0.891] | 0.850 | 0.851 |
| Mean per-segment Pearson r (segments > 0.5) | 0.50 (9/18) | 0.31 (6/18) | 0.44 (7/18) |
| LAD FFR/iFR pooled AUROC (274 measurements) | 0.721 [0.662, 0.786] | not available | not available |

- The model card on Hugging Face has the full tables and caveats: FFR alone 0.69 (n = 76), iFR alone 0.73, and weaker
  distal/branch segments.
- Running the AVI test inputs through this container's model code reproduces the locked test predictions (max |Δ|
  0.0005).
- **DICOM path:** identical, except for studies whose training videos had been stored as MP4 (13% of test videos).
  Those differ by up to a few points.

## Build and run

```bash
python3 scripts/release-model.py model-examples/DeepCORO-JEPA \
  --image-repository heartwisehub/deepcoro-jepa --hf-token-file /secure/path/hf_token
```

- GPU required: about 1.7 GB resident and up to 9.9 GB peak (largest MHI test studies, bf16); declared 11 GB.
- **Blackwell (sm_120) GPUs:** cu121 wheels do not run on them. Build with a cu128 torch (≥ 2.7) there.

## Tests

```bash
cd model-examples/DeepCORO-JEPA && python -m pytest -q tests
```

The tests cover:
- video selection and order;
- DICOM to MJPG conversion;
- output postprocessing, the yes/no calls and the strict "above the threshold" rule;
- that a wrong or incomplete thresholds file or `Thresholds` block fails loudly;
- provenance and weight-hash verification;
- that the OpenAPI examples match `data/`.
