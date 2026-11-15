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
| **1.4.0** (this one) | **v13** | `heartwise/DeepCORO_JEPA@8144d01` (branch `v13`, tag `v13.1`) | [`400d0a77`](https://wandb.ai/mhi_ai/deepcoro-segmentation/runs/400d0a77) |
| 1.3.0 | v13 | same revision | same run |
| 1.2.0 | v13 | same revision | same run |
| 1.1.2 | v13 | same revision | same run |
| 1.1.1 | v13 | same revision | same run |
| 1.1.0 | v13 | same revision | same run |
| 1.0.0 | v12 | `heartwise/DeepCORO_JEPA@49c8ddf` (branch `main`) | `7983f98e` |

The served weights are the ones exported from the checkpoint of that training run; `data/model_facts.json`
(`Summary.Training_run`) and `model_config.json` of the Hugging Face revision carry the same link.

**What v13 changes against v12:** it adds the calcification, thrombus and CTO outputs. Stenosis is unchanged on the
test set (macro AUROC 0.885 against 0.884). The pressure AUROC is lower (0.69 against 0.72; the difference is within
its confidence interval, see "Verified performance"), and the lesion outputs are not better than the reference
models. Version 1.0.0 stays buildable from commit `82492f2` of `master`.

**What 1.4.0 changes against 1.3.0:** same weights and thresholds; `stenosis_pct` and every flag are unchanged.
The report shows a calibrated stenosis instead of the raw estimate (display calibration `cal1`,
`data/stenosis_display.json`, applied by `display_calibration.py`), and each segment of the JSON gets
`display_pct` and `display_category`. The report header reads `v13-cal1` (`reportModelVersion`); the HF tag `v13.1`
is the weights revision and is not changed. See "Displayed stenosis" below.
The schema also draws the three lesion flags (drawing only, `report_html.py`; a stored result re-rendered by
`/inference/render-report` gets the same drawing): a **calcified** segment lies on a grey band wider than the vessel;
beyond a **CTO** flag the vessel is grey, meaning no antegrade flow is shown: the rest of the occluded segment after
the block mark, every segment distal to it and the branches arising at or beyond the mark, following the drawn tree
(`PARENT`, `PARENT_LEFT` for a left-dominant schema); branches arising before the mark stay red. The mark sits at the
middle of the segment because the model does not localise the lesion inside a segment, so whether a branch of the
occluded segment itself is grey follows the drawing, not the angiogram. A **thrombus** flag is a clot symbol in the
lumen (lobulated, outlined, just proximal to the block mark when the segment is also a CTO). The calcified band also
carries pale calcium plates along the vessel wall. The legend shows the three cartoons at small size. No angiographic
image is drawn on the schema: the explanation crops stay in "Where the model looked", lower in the report.

**What 1.3.0 changes against 1.2.0:** same weights, thresholds, JSON values and report. `POST
/inference/render-report` with `{"result": <data of a JSON-mode predict>}` answers `{"htmlBase64", "modelVersion",
"reportModelVersion"}`: the HTML report of that stored result, byte for byte what HTML-mode predict returns for the
same prediction (both call `CustomPredictionService.report_html` on the JSON predictions). It runs on the CPU, never
loads the model, does not touch the inference semaphore or the idle timer, and works before the weights are loaded.
Bodies above 5 MB answer 413; a result the renderer cannot use answers 422 `INVALID_RESULT` with the path of the
first bad value (`report_input.py` checks types, ranges, segment names and that images are base64 JPEG).
`predictions.explainability[i].images` = `{crop, frame}` (base64 JPEG, about 20-40 KB each) is now kept in the JSON
so a stored result renders with its pictures; 1.2.0 results render with a note instead. `GET
/inference/model-info` advertises `capabilities.renderReport: true`.

**What 1.2.0 changes against 1.1.1:** same weights, thresholds and JSON values. The HTML report
(`report_html.py`, no torch) draws the 18 segments on a coronary schema: flagged segments in blue boxes, plaque
narrowing the lumen to the estimated stenosis, the CTO flag, then LAD physiology and per-vessel detail. The caller may
send `additionalMetadata.coronaryDominance` = `{call: left|right, method: majority|tie_mean_probability, leftVotes,
rightVotes, videoCount, meanLeftProbability, source}`: the study majority of the angiogram procedure classifier's
per-video dominance over the diagnostic videos sent to this model (cardio-agent computes it during diagnostic
selection). It is checked against its own votes, returned as `predictions.coronary_dominance`, appended to the
diagnosis and used to draw the schema left or right dominant. Without it the schema is drawn right dominant and says
so. A segment with a CTO flag is drawn blocked (vessel pale beyond the occlusion) and reported as 100% in the
report and the diagnosis, whatever its stenosis estimate; `stenosis_pct` in the JSON stays the model estimate. A
thrombus flag is drawn as a filling defect in the lumen. The posterior LV branch (`lvp`) is left out of the report and
the diagnosis but stays in the JSON. **Explainability** (`explain.py`): for each flagged stenosis segment (significant or CTO, at most 6, most
probable first) the report shows the tracked pass-2 crop with the highest bag attention, the within-crop token
attention the model used for that segment (softmax of the segment query and the mask prior, recomputed from the
captured `_pool_study` inputs of the same call, no extra encoder pass) at the time slot holding the most attention,
and the full frame with the crop box and the attention peak. `predictions.explainability` carries the same metadata
(SOP, frame, box, attention shares, peak position) without images. Attention shows where a segment was read, not a
measured lesion. `supportedAdditionalMetadata` stays empty because a non-empty list opens the user tagging page.

**What 1.1.2 changes against 1.1.1:** same weights, thresholds and code. `requirements.txt` now installs PyTorch
2.14.1 built for CUDA 13.0 instead of 2.5.1 built for CUDA 12.1, which does not run on NVIDIA Blackwell GPUs. The
numbers returned for a study are not identical to those of the environment in which the test metrics were computed;
see "Effect of the PyTorch build" under "Verification of this version". Image 1.1.1 was published on 2026-11-12 with
the CUDA 12.1 build; it is not usable on Blackwell GPUs.

**What 1.1.1 changes against 1.1.0** (commit `5b3a825`): same weights and thresholds. Probabilities are returned
with 6 decimals instead of 4, and every yes/no call is taken on the returned probability. In 1.1.0 the call was
taken on the unrounded probability, so a response could show a probability equal to its threshold next to a
positive call. On the saved test predictions (3,321 studies) that is the case for 17 of the 59,778 stenosis calls,
all in the left ventricular branch (threshold 0.0006), and for 2 calcification calls. It did not occur in the 40
studies of the verification below. The diagnosis sentence and the HTML report still print 2 or 3 decimals, but now
print more whenever fewer would show a probability on the other side of its threshold than the call, and they print
every threshold with 4 decimals (1.1.0 printed the pressure thresholds with 2, for example "P 0.26, threshold 0.26").

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
    - `stenosis_pct`: estimated % diameter stenosis (the model estimate, pulled toward the mean).
    - `display_pct`, `display_category` (1.4.0 on): the % the report shows and its category (`normal`, `mild` < 30,
      `moderate` 30-49, `intermediate` 50-69, `severe` ≥ 70, `occluded` for a CTO flag). See "Displayed stenosis".
    - `prob_significant`: probability that the report calls the segment significant.
    - `prob_threshold`: the decision threshold of that segment.
    - `significant`: `true` when `prob_significant` is strictly above `prob_threshold`.
      Every probability in the response has 6 decimals, and every yes/no value is taken on that returned number.
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
- **`diagnosis`:** lists the flagged segments (shown % and model estimate), the other segments shown at 30% or
  more, how many segments are shown as normal, the two pressure calls and the segments flagged for each lesion
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

## Displayed stenosis

The regression head shrinks toward the mean: on the validation split, segments the report calls 0% have a median
estimate of 4.5% (a quarter above 9.5%), and lesions of 70%, 80% and 95-100% have medians of 33%, 44% and 45-52%.
Printed as is, few segments read 0% and few lesions read 70% or more. From 1.4.0 the report shows `display_pct`:

| Model estimate (`stenosis_pct`) | Shown |
|---|---|
| below 10 | 0% (normal) |
| 10 → 23 → 37 → 51 → 100 | 20 → 30 → 50 → 70 → 100%, linear in between, rounded |
| CTO flag | 100% (occluded) |
| significant flag and estimate below 10 | 20% (mild): a flagged segment is never shown as normal |

How it was chosen (segments pooled, because single segments have few lesions):

- **Data.** Validation split only, served v13 weights (epoch 0): 4,647 studies, labels 0/20/40/50/70/80/95/100%
  from the angiography report. The 4,647 posterior LV branch (LVP) rows are left out of the fit because report
  grading of LVP is weak (31 have any stenosis), which leaves 78,999 segment readings (17 segments). The mapping is
  still applied to every displayed segment and to LVP in the JSON. No test data was used.
- **T0 (shown as normal below it).** Among cuts in steps of 0.5, the largest cut whose Youden's J for "report
  stenosis > 0%" is within 0.002 of the maximum (J is 0.500-0.503 from 8.5% to 10%). At 10%: sensitivity 0.74
  [0.73, 0.75], specificity 0.76 [0.76, 0.77], PPV 0.51 [0.50, 0.51], NPV 0.90 [0.89, 0.90] (prevalence 0.25;
  patient bootstrap).
- **Knots.** The estimate cuts for 30, 50 and 70% (1-point grid above T0) maximise the quadratic-weighted kappa of
  the five categories (0, < 30, 30-49, 50-69, ≥ 70). 5-fold cross-validation grouped by patient gave T0 10 in every
  fold and knots 23 / 36-37 / 48-52; the shipped values are the fit on all validation rows (10 / 23 / 37 / 51).
  With the LVP rows included the fit is 9.5 / 23.5 / 35.5 / 51.5 (kappa 0.589), the same within the fold spread.
- **Result (cross-validated).** Exact category agreement 22% → 66%, weighted kappa 0.40 → 0.59 ([0.58, 0.59]),
  within one category 90% → 88%. Every vessel and every segment improves (kappa LAD 0.39 → 0.57, LCx 0.26 → 0.49,
  LM 0.50 → 0.68, RCA 0.49 → 0.65; weakest segments D2 0.19 and ramus 0.21).
- **What it does not fix.** The estimate is still noisy (r about 0.5). Within a shown category the report value is
  lower on average: shown < 30 → mean report 14%, 30-49 → 27%, 50-69 → 39%, ≥ 70 → 62% (68% of these are ≥ 70% on
  the report, against 82% for raw estimates ≥ 70, which found only 20% of these lesions; shown ≥ 70 finds 38%).
  Stretching the scale trades this per-bin calibration for categories that match the report more often.
- **Lesions shown as normal.** 13% of the lesions reported at 70% or more (and 12% of left main lesions of 50% or
  more) have an estimate below 10%. The raw number hid them as "5%"; the report now says "normal" unless the
  segment is flagged, in which case it is shown as mild. Lesions of 70% or more shown as normal or mild: 38% before,
  30% after. The flags, not the shown %, are the screening output.
- **Flags.** Unchanged. The validation file has no probabilities, so the flag rule was checked on the earlier v12
  test predictions only: no segment without a flag was shown at 70% or more, and 17% of flags (mostly ramus,
  posterolateral, distal LCx, distal LAD and posterior LV with very low thresholds) had an estimate below 10%; 89% of
  those were 0% on the report, and they are shown at 20% (mild) instead of normal. Raising the thresholds of these
  five segments would remove most of them; that is a flag change and is not part of 1.4.0.
- **History.** The first values of this mapping (10 / 23 / 35 / 49, never released) came from the same method on
  v12 predictions of the test split. On the v13 validation rows the two mappings are equivalent (weighted kappa
  difference 0.000 [-0.001, 0.001]).

## Decision thresholds

The thresholds are read from `thresholds.json` of the pinned Hugging Face revision. They were chosen on the
**validation** split only (4,647 studies). A call is positive when the returned probability (6 decimals) is
strictly above its threshold. On the saved validation and test predictions (7,968 studies) this gives the same
call as the unrounded probability for every stenosis and pressure output and for the lesion outputs at the default
point; 3 of about 860,000 lesion calls differ at the two other lesion points.

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
  Against `inference.py` on the same DICOM files (rerun with the code of 1.1.1): probabilities equal to the rounding
  (largest difference 0.000005, `inference.py` writes 5 decimals; 0.0005 on the estimated FFR and iFR values, which
  the container returns with 3 decimals), all 2,960 yes/no calls equal, 2 encoder passes in every study.
  `stenosis_pct` differs only because the container clips it to 0 to 100 (88 of 720 raw values were below 0, the
  lowest -4.6). The 2,960 calls are also the same as those of 1.1.0, and a second run gave the same output.
  `inference.py` takes its calls on the unrounded probability, the container on the returned 6-decimal one.
- **Fresh download.** A fresh `snapshot_download` of revision `v13.1` and its `inference.py` on 12 studies gives the
  same predictions as above (difference 0).
- **Effect of the PyTorch build (1.1.2).** The checks above ran in the development environment (an NVIDIA build of
  PyTorch 2.14, CUDA 13.4), in which the test metrics were also computed. The pinned requirements of 1.1.2 (PyTorch
  2.14.1, CUDA 13.0) were then installed in a clean environment and run on an RTX PRO 5000 Blackwell GPU. The inputs
  are the same (the decoded clips are identical bit for bit), but the outputs are not:
  - 120 test studies with `inference.py` (the training videos): 44 of the 8,880 yes/no calls differ (39 stenosis, 2
    calcification, 1 thrombus, 1 CTO, 1 pressure), in 31 studies. The stenosis probability differs by more than 0.01
    in 86 studies; the largest single differences are 0.57 in probability and 43 stenosis points. Against the reports
    of these 120 studies the two environments perform alike: pooled AUROC 0.908 and 0.908, macro AUROC over the 15
    segments with at least 3 positive studies 0.854 and 0.852, mean segment r 0.484 and 0.481.
  - The 40 DICOM studies through `CustomPredictionService.predict`: 10 of the 2,960 calls differ from the check
    above, largest differences 0.14 in probability and 8.4 stenosis points; 2 encoder passes in every study, peak 8.7
    GiB allocated.
  - Cause, as far as it was traced on 10 studies: the model runs in bfloat16, and its result depends on the build.
    Between the two builds the largest difference was 6.5 stenosis points in bfloat16 and 0.8 in float32; within one
    build, bfloat16 and float32 differ by up to 13 points. Small numerical differences are amplified by the steps
    that take a decision (which crops of a segment are kept).
  - Consequence: the metrics under "Verified performance" were measured in the development environment. The full
    test set was not rerun with the pinned requirements, so for this image they are supported only by the
    120-study comparison above. A single segment's number can change between PyTorch builds and should not be read
    as exact.
- **Example.** The files in `docs/examples/` are the response of this code, with the pinned requirements of 1.1.2, to
  one of the 40 studies (5 runs).
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

Run every command from the root of a clean checkout of `master`.

**1. Candidate image** (builds, verifies the manifest and answers `model-info` from a temporary container; nothing is
pushed without `--publish`):

```bash
python3 scripts/release-model.py model-examples/DeepCORO-JEPA \
  --image-repository heartwisehub/deepcoro-jepa --hf-token-file /secure/path/hf_token
```

**2. Start it and wait until it answers.** The web server needs a few seconds after `docker run` returns, so poll
instead of calling at once:

```bash
docker run -d --rm --gpus all --name deepcoro-jepa -p 127.0.0.1:8080:80 heartwisehub/deepcoro-jepa:1.1.2
for i in $(seq 1 60); do
  curl -sf http://127.0.0.1:8080/api/inference/model-info > /dev/null && break
  sleep 2
done
curl -sf http://127.0.0.1:8080/api/inference/model-info
```

**3. Time limit of one prediction: 5 minutes.** nginx in the image stops waiting for the model after 300 seconds, and
the PACS.AI backend's own client also gives up after 5 minutes, so a longer client timeout does not help. The model is
loaded on the first `/inference/predict` call, which is therefore the slowest one.

**4. Publish** from `master` (the workflow refuses any other ref, and refuses to overwrite a version that exists):

```bash
gh workflow run publish-model-image.yml --ref master -f model_directory=model-examples/DeepCORO-JEPA
```

**5. Deploy.**
- Model already registered in a workspace: an owner or administrator selects the published version in Workspace
  Settings (see `docs/model-image-release.md`). Do not run `scripts/deploy-model.sh` again for it: the script removes
  the existing model first, and that deletes its ingestion jobs and processing history.
- First registration only: `scripts/deploy-model.sh model-examples/DeepCORO-JEPA --register-only`.

Notes:
- GPU required: about 1.7 GB resident and up to 9.9 GB peak (largest MHI test studies, bf16, measured with v12).
  With v13 the peak over the 40 verification studies (up to 9 runs each) was 8.7 GiB allocated. Declared 11 GB.
- **PyTorch build.** `requirements.txt` pins `torch 2.14.1+cu130` and `torchvision 0.29.1+cu130` (CUDA 13.0). That
  build contains kernels for NVIDIA GPUs from Turing (sm_75) to Blackwell (sm_120); it has none for older GPUs
  (Pascal, Volta). It was run on an RTX PRO 5000 Blackwell with driver 615.71. Versions up to 1.1.1 pinned a CUDA 12.1
  build that does not run on Blackwell GPUs. The index, torch and torchvision lines must name the same build; a test
  checks it.
- No Docker image was built or run on the development server; steps 1 to 5 are written from the release scripts and
  `docs/model-image-release.md`.

## Tests

```bash
cd model-examples/DeepCORO-JEPA && python -m pytest -q tests
```

If the host Python has no pytest, use a virtual environment (the tests run on CPU):

```bash
cd model-examples/DeepCORO-JEPA && python3 -m venv /tmp/deepcoro-jepa-venv
/tmp/deepcoro-jepa-venv/bin/pip install -r requirements.txt pytest
/tmp/deepcoro-jepa-venv/bin/python -m pytest -q tests
```

The tests cover:
- video selection and order;
- DICOM to MJPG conversion;
- the display calibration: knots, T0, the CTO and flag rules, monotonicity, the JSON fields, the report and the
  diagnosis wording, and that stored 1.3.0 results render the same;
- output postprocessing, the yes/no calls and the strict "above the threshold" rule, for stenosis, pressure and the
  three lesion findings;
- that every call follows the returned 6-decimal probability, and that the diagnosis and the report never print a
  probability on the wrong side of its threshold;
- that the model facts name the training run of the served weights;
- that a wrong or incomplete thresholds file or `Thresholds` block fails loudly;
- provenance and weight-hash verification;
- that the OpenAPI examples match `data/`;
- that the example response in `docs/examples/` is the one in the OpenAPI file and agrees with its own thresholds.
