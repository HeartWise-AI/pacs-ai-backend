# DeepCORO-JEPA

**Research use only.**

DeepCORO-JEPA (earlier working name: DeepCORO-Integrated) is one EchoJEPA ViT-L video encoder that segments the coronary tree itself. It reads a diagnostic
angiography study end to end:

- % diameter stenosis, P(significant lesion) and a yes/no flag for **18 coronary segments**;
- **LAD FFR / iFR**: value, P(abnormal) and a yes/no call, from v12 on;
- **calcification / thrombus / chronic total occlusion (CTO)**: a probability and a yes/no flag per segment, from
  v13 on.

Segmentation is not a separate step: nothing has to be computed before the model is called. The same forward pass
predicts the masks, cuts the crops and reads them.

The encoder runs **at most twice per study**:

1. Full runs at 224 px, from which the model predicts its own 26-class SYNTAX masks.
2. Up to 4 tracked 320 px crops per segment.

`measured_forward` counts the encoder calls and fails if there are more than two.

| Container version | Model | Hugging Face revision | Training run (Weights & Biases, `mhi_ai/deepcoro-segmentation`) |
|---|---|---|---|
| **1.1.0** (this one) | **v13** | `heartwise/DeepCORO_JEPA@8144d01` (branch `v13`, tag `v13.1`) | [`400d0a77`](https://wandb.ai/mhi_ai/deepcoro-segmentation/runs/400d0a77) |
| 1.0.0 | v12 | `heartwise/DeepCORO_JEPA@49c8ddf` (branch `main`) | `7983f98e` |

The served weights are the ones exported from the checkpoint of that training run; `data/model_facts.json`
(`Summary.Training_run`) and `model_config.json` of the Hugging Face revision carry the same link.

**What v13 changes against v12:** it adds the calcification, thrombus and CTO outputs. Stenosis is unchanged on the
test set (macro AUROC 0.885 against 0.884). The pressure AUROC is lower (0.69 against 0.72; the difference is within
its confidence interval, see "Verified performance"), and the lesion outputs are not better than the reference
models. Version 1.0.0 stays buildable from commit `82492f2` of `master`.

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
    - `calcif_prob`, `thrombus_prob`, `cto_prob`: probability of calcification (moderate or severe, or a calcified
      nodule), thrombus and chronic total occlusion in that segment.
    - `calcif`, `thrombus`, `cto`: `true` when that probability is strictly above the threshold of the finding.
  - `lesion_thresholds`: `operating_point` and the three thresholds in use (`calcif`, `thrombus`, `cto`); one
    threshold per finding for all segments.
  - `lad_physiology`:
    - `ffr`, `ifr`: predicted values.
    - `ffr_abnormal_prob`: P(FFR ≤ 0.80); `ifr_abnormal_prob`: P(iFR ≤ 0.89).
    - `ffr_abnormal_threshold`, `ifr_abnormal_threshold`, and the calls `ffr_abnormal`, `ifr_abnormal`.
    - `operating_point`: which pressure thresholds were used.
  - `n_videos` and `encoder_passes`.
- **`diagnosis`:** lists the flagged segments, the two pressure calls and the segments flagged for each lesion
  finding.
- **HTML:** the segment table (threshold, flag and the three lesion probabilities) plus LAD physiology.
- **Example:** a response of this container for one test study, with every threshold in it:
  [`docs/examples/predict_response_json.json`](docs/examples/predict_response_json.json) and the HTML report
  [`docs/examples/predict_report.html`](docs/examples/predict_report.html). The same response is the example of
  `/inference/predict` in `docs/openapi.json`. It holds no patient or study identifier.
- **Failures:**
  - No readable run: the request succeeds with empty `predictions` and the text "No video could be extracted".
  - The model call fails (for example the GPU runs out of memory): the request fails with HTTP 500 and
    `errorCode` `PROCESSING_ERROR`. It is never reported as a missing video.

## Decision thresholds

The thresholds are read from `thresholds.json` of the pinned Hugging Face revision. They were chosen on the
**validation** split only (4,647 studies). A call is positive when the probability is strictly above its threshold.

- **Stenosis and pressure:** the rule of the deployed DeepCORO-CLIP checkpoint, i.e. the threshold with the highest
  geometric mean of sensitivity and specificity (`balanced`).
- **Calcification, thrombus, CTO:** the lowest threshold with a validation specificity of at least 0.99
  (`specificity_99`). The balanced point would raise about three flags per study for each finding, nearly all false.

The test split was used only to report the numbers below. One disclosure: when the default lesion operating point
was picked, the test numbers of the three candidate points were visible; the choice was made on the validation
flags per study. FFR ≤ 0.80 and iFR ≤ 0.89 are the label cut-offs the pressure
head was trained on (the cut-offs of FAME and of DEFINE-FLAIR / iFR-SWEDEHEART). The thresholds are internal
operating points, not validated clinical ones.

`models/config.json`:

```json
"Thresholds": {"path": "thresholds.json", "pressure_operating_point": "balanced", "lesion_operating_point": "specificity_99"}
```

| Key | Meaning | Default |
|---|---|---|
| `path` | Thresholds file inside `models/`, fetched at build | `thresholds.json` |
| `pressure_operating_point` | `balanced`, `high_sensitivity` or `high_specificity`; applies to FFR and iFR. Segments always use `balanced`. | `balanced` |
| `lesion_operating_point` | `specificity_99`, `specificity_95` or `balanced`; applies to calcification, thrombus and CTO. Required for every checkpoint, used by those with a lesion head. | `specificity_99` |

All three keys are required. An unknown or missing key, an unknown operating point, a missing file, a missing output,
a threshold outside (0, 1) or a file derived for other weights stops the container at start-up. There is no fallback value.

LAD pressure calls on the held-out test split (FFR: 76 measurements, 29 abnormal; iFR: 198, 97 abnormal):

| Output | Operating point | Threshold | Sensitivity | Specificity | PPV | NPV |
|---|---|---|---|---|---|---|
| FFR ≤ 0.80 | `balanced` | 0.2644 | 0.83 | 0.49 | 0.50 | 0.82 |
| FFR ≤ 0.80 | `high_sensitivity` | 0.2214 | 0.86 | 0.47 | 0.50 | 0.85 |
| FFR ≤ 0.80 | `high_specificity` | 0.4937 | 0.21 | 0.91 | 0.60 | 0.65 |
| iFR ≤ 0.89 | `balanced` | 0.4337 | 0.59 | 0.70 | 0.66 | 0.64 |
| iFR ≤ 0.89 | `high_sensitivity` | 0.1848 | 0.89 | 0.28 | 0.54 | 0.72 |
| iFR ≤ 0.89 | `high_specificity` | 0.5799 | 0.39 | 0.83 | 0.69 | 0.59 |

The FFR `balanced` and `high_sensitivity` thresholds are close to each other, and both give a specificity near 0.5.

Segment flags on the same 3,194 test studies (mean over the 18 segments):

| Rule | Sensitivity | Specificity | PPV | NPV |
|---|---|---|---|---|
| `prob_significant` above the segment's threshold | 0.80 | 0.80 | 0.25 | 0.98 |

Calcification, thrombus and CTO flags on the same test studies: 57,492 study-segment pairs, of which 1,110 have
calcification (1.9%), 104 thrombus (0.2%) and 382 CTO (0.7%) in the report.

| Finding | Operating point | Threshold | Sensitivity | Specificity | PPV | NPV | Flags per study |
|---|---|---|---|---|---|---|---|
| Calcification | `specificity_99` (default) | 0.2225 | 0.19 | 0.990 | 0.27 | 0.984 | 0.25 |
| Calcification | `specificity_95` | 0.0791 | 0.51 | 0.951 | 0.17 | 0.990 | 1.04 |
| Calcification | `balanced` | 0.0161 | 0.82 | 0.830 | 0.09 | 0.996 | 3.29 |
| Thrombus | `specificity_99` (default) | 0.0851 | 0.51 | 0.992 | 0.10 | 0.999 | 0.16 |
| Thrombus | `specificity_95` | 0.0167 | 0.70 | 0.957 | 0.03 | 0.999 | 0.79 |
| Thrombus | `balanced` | 0.0042 | 0.83 | 0.861 | 0.01 | 1.000 | 2.52 |
| CTO | `specificity_99` (default) | 0.1570 | 0.37 | 0.990 | 0.21 | 0.996 | 0.22 |
| CTO | `specificity_95` | 0.0329 | 0.62 | 0.952 | 0.08 | 0.997 | 0.93 |
| CTO | `balanced` | 0.0076 | 0.83 | 0.844 | 0.03 | 0.999 | 2.88 |

Read the flags with these limits in mind:

- **A flag is a prompt to look, not a diagnosis.** One flagged segment in four is significant in the report
  (PPV 0.25). An unflagged segment is rarely significant (NPV 0.98).
- **Most studies get at least one flag.** At least one segment is flagged in 97% of the studies with a significant
  lesion, and in 42% of the studies without one.
- **Rare segments have very low thresholds** (for example 0.02 for distal LCx and below 0.001 for the posterior
  left ventricular branch), because significant lesions there are rare in the validation split.
- **The flag comes from the probability, not from `stenosis_pct`.** The estimated percentage is pulled toward the
  mean, so a rule on the percentage misses most lesions (measured on v12: sensitivity 0.14).
- **Lesion flags miss most findings and most of them are false.** At the default point the model finds 19% of the
  calcified segments, 51% of the segments with thrombus and 37% of the CTO segments, and 10% to 27% of the flags are
  right. A missing flag does not exclude the finding.
- **Every study gets all the outputs.** The container does not check which arteries were filmed. A study with
  right coronary runs only still returns values for the left coronary segments and for LAD physiology, and these
  are not based on images of those vessels.
- **The FFR and iFR calls can disagree.** They are two outputs with two thresholds, and the model does not know
  which wire was used.

## Verified performance

Held-out MHI test: 3,194 studies / 2,941 patients, with patient-bootstrap 95% CIs. The checkpoint was locked on
validation, then the test set was read once.

| | v13 (served) | v12 | DeepCORO-CLIP | EchoJEPA |
|---|---|---|---|---|
| Stenosis macro AUROC (18 segments) | **0.885** [0.878, 0.893] | 0.884 [0.877, 0.891] | 0.850 | 0.851 |
| Mean per-segment Pearson r (segments > 0.5) | 0.50 (9/18) | 0.50 (9/18) | 0.31 (6/18) | 0.44 (7/18) |
| LAD FFR/iFR pooled AUROC (274 measurements) | 0.692 [0.632, 0.752] | 0.721 [0.662, 0.786] | not available | not available |

- **Stenosis:** v13 minus DeepCORO-CLIP +0.035 [0.028, 0.042]; v13 minus EchoJEPA +0.034 [0.027, 0.041]; v13 minus
  v12 +0.001 [-0.003, 0.005].
- **Pressure:** FFR alone 0.72 (76 measurements), iFR alone 0.68 (198). v12 minus v13, pooled: +0.029
  [-0.014, 0.075].
- **Weaker segments:** r is 0.20 to 0.48 for the distal and branch segments.

Lesion outputs, area under the precision-recall curve (AUPRC; "macro" is the mean over segments, "micro" pools all
study-segment pairs). The reference models are DeepCORO-CLIP and two EchoJEPA lesion baselines:

| Finding (macro / micro AUPRC) | v13 | DeepCORO-CLIP | EchoJEPA A | EchoJEPA B |
|---|---|---|---|---|
| Calcification | 0.118 / 0.182 | 0.116 / 0.180 | 0.125 / 0.203 | 0.123 / 0.203 |
| Thrombus | 0.304 / 0.224 | 0.331 / 0.224 | 0.343 / 0.237 | 0.410 / 0.305 |
| CTO | 0.133 / 0.171 | 0.125 / 0.170 | 0.112 / 0.165 | 0.139 / 0.202 |

v13 does not beat the best reference model on any finding. Every paired difference has a 95% CI that includes zero.
The model card on Hugging Face (revision `v13.1`) has the full tables.

## Verification of this version

Done on the development server as `.claude/skills/pacs-ai-model-mapping` prescribes, on a sample of the test set:

- **Hugging Face code on the training videos.** `inference.py` of revision `v13.1` on 120 test studies (625
  videos) reproduces the locked test predictions: the largest difference is 0.0005 stenosis points, 0.00001 on the
  stenosis probability, 0 on the three lesion probabilities and 0.00005 on the pressure probability.
- **Hugging Face code on DICOM.** On 40 of these studies (208 DICOM files) the DICOM path gives the same numbers as
  the training videos for the 33 studies whose training videos were all stored as MJPG. The 7 studies with at least
  one training video stored as H.264 (35 of the 208 videos) differ, by up to 13.9 stenosis points and 0.25 in
  probability, and 9 of the 2,960 yes/no calls change (7 stenosis, 1 CTO, 1 pressure). The test metrics above were
  computed on the training videos, so they include this mix; the full test set was not rerun from DICOM for v13.
- **This container's code on DICOM.** `download_model.py` fetched the weights, `model_config.json` and
  `thresholds.json` at the pinned revision and checked the SHA-256; `logic.py` loaded them and answered the same 40
  studies through `CustomPredictionService.predict`, with the DICOM files of each study sent in shuffled order.
  Against `inference.py` on the same DICOM files: probabilities equal to the rounding (largest difference 0.00005;
  0.0005 on the pressure probability), all 2,960 yes/no calls equal, 2 encoder passes in every study. `stenosis_pct`
  differs only because the container clips it to 0 to 100 (88 of 720 raw values were below 0, the lowest -4.6).
  A second run gave the same output.
- **Fresh download.** A fresh `snapshot_download` of revision `v13.1` and its `inference.py` on 12 studies gives the
  same predictions as above (difference 0).
- **Example.** The files in `docs/examples/` are the response of this code to one of the 40 studies (5 runs).
- **Not done.** No Docker image was built or run on the development server, and the service was not called through
  the PACS.AI backend. The check above calls the same Python code that the image runs.

Checklist of `.claude/skills/pacs-ai-model-mapping`:

| Item | Status |
|---|---|
| `logic.py` and `download_model.py` are written for this model | yes |
| `supportedAdditionalMetadata` | `[]` on purpose: no variables page |
| Padding mask | does not apply: no `VideoMILWrapper`; the model takes a list of 1 to 12 clips |
| Normalisation constants | does not apply: the model normalises inside its forward pass |
| `dicomUploadMax` equals the largest number of views | 12 and 12; `dicomUploadMin` is 1 |
| Order of the runs | sorted in `logic.py` (acquisition time), checked with shuffled uploads |
| Weights file and access | `model.safetensors` of the private Hugging Face repository, read with `hf_token` at build |
| Provenance | commit-pinned revision and weight SHA-256 in `data/model_info.json`, checked at build and by the tests |
| Official image | to build after merge from the merged `master` commit with the release workflow, then repoint and redeploy |
| Registered `outputMode` | a redeploy keeps the registered mode; JSON and HTML are both supported |

## Build and run

```bash
python3 scripts/release-model.py model-examples/DeepCORO-JEPA \
  --image-repository heartwisehub/deepcoro-jepa --hf-token-file /secure/path/hf_token
```

- GPU required: about 1.7 GB resident and up to 9.9 GB peak (largest MHI test studies, bf16, measured with v12).
  With v13 the peak over the 40 verification studies (up to 9 runs each) was 8.6 GiB allocated. Declared 11 GB.
- The official image is built after merge by the release workflow (`.github/workflows/publish-model-image.yml`);
  the deployment is then repointed to the new image digest. The Docker build was not run on the development server.
- **Blackwell (sm_120) GPUs:** cu121 wheels do not run on them. Build with a cu128 torch (≥ 2.7) there.

## Tests

```bash
cd model-examples/DeepCORO-JEPA && python -m pytest -q tests
```

The tests cover:
- video selection and order;
- DICOM to MJPG conversion;
- output postprocessing, the yes/no calls and the strict "above the threshold" rule, for stenosis, pressure and the
  three lesion findings;
- that the model facts name the training run of the served weights;
- that a wrong or incomplete thresholds file or `Thresholds` block fails loudly;
- provenance and weight-hash verification;
- that the OpenAPI examples match `data/`;
- that the example response in `docs/examples/` is the one in the OpenAPI file and agrees with its own thresholds.
