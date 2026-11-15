# DeepCORO-JEPA

**Research use only.**

DeepCORO-JEPA (earlier working name: DeepCORO-Integrated) is one EchoJEPA ViT-L video encoder that segments the coronary tree itself. It reads a diagnostic
angiography study end to end:

- % diameter stenosis, P(significant lesion) and a yes/no flag for **18 coronary segments** (17 supported in v15:
  the posterior left ventricular branch, `lvp`, had no training target and is returned for compatibility only);
- **LAD FFR / iFR**: value, P(abnormal) and a yes/no call, from v12 on;
- **calcification / thrombus / chronic total occlusion (CTO)**: a probability and a yes/no flag per segment, from
  v13 on;
- **study-level lesion probabilities** (any segment; no yes/no call) and the model's own **coronary dominance**
  estimate, from v15 on.

Segmentation is not a separate step: nothing has to be computed before the model is called. The same forward pass
predicts the masks, cuts the crops and reads them.

The encoder runs **at most twice per study**:

1. Full runs at 224 px, from which the model predicts its own 26-class SYNTAX masks.
2. Up to 4 tracked 320 px crops per segment.

`measured_forward` counts the encoder calls and fails if there are more than two.

| Container version | Model | Hugging Face revision | Training run (Weights & Biases, `mhi_ai/deepcoro-segmentation`) |
|---|---|---|---|
| **1.5.0** (this one) | **v15** | `heartwise/DeepCORO_JEPA@ab6d12f` (branch `v15`, tag `v15.1`) | [`2563ce01`](https://wandb.ai/mhi_ai/deepcoro-segmentation/runs/2563ce01) |
| 1.4.1 | v13 | `heartwise/DeepCORO_JEPA@8144d01` (branch `v13`, tag `v13.1`) | [`400d0a77`](https://wandb.ai/mhi_ai/deepcoro-segmentation/runs/400d0a77) |
| 1.4.0 | v13 | same revision | same run |
| 1.3.0 | v13 | same revision | same run |
| 1.2.0 | v13 | same revision | same run |
| 1.1.2 | v13 | same revision | same run |
| 1.1.1 | v13 | same revision | same run |
| 1.1.0 | v13 | same revision | same run |
| 1.0.0 | v12 | `heartwise/DeepCORO_JEPA@49c8ddf` (branch `main`) | `7983f98e` |

The served weights are the ones exported from the checkpoint of that training run; `data/model_facts.json`
(`Summary.Training_run`) and `model_config.json` of the Hugging Face revision carry the same link.

**What 1.5.0 changes against 1.4.1:** it serves **model v15** (weights SHA-256 `23b8255b…c54c`, exported from epoch 1
of run `2563ce01`; release dossier on the model NAS, `DeepCoro_Integrated/v15/`) instead of v13. Every key of the
1.4.1 JSON is still there with the same meaning; the values come from the new weights and thresholds.

- **Performance against v13** (held-out test, 3,194 studies, paired differences v15 minus v13): stenosis is slightly
  lower (mean r over 17 segments 0.508 against 0.521, −0.014 [−0.020, −0.008]; macro AUROC 0.877 against 0.883,
  −0.006 [−0.009, −0.002]); the LAD pressure output is not distinguishable (0.674 against 0.689, −0.015
  [−0.059, 0.030]); lesion detection is better (mean of the six lesion AUPRCs +0.034 [0.012, 0.051]; thrombus,
  macro, 0.402 against 0.304, +0.098 [0.025, 0.159]). See "Verified performance".
- **Model code.** `models/deepcoro_model/` is the code of the Hugging Face revision `v15.1`: the loader enables the
  study lesion pooler, the dominance head and the procedure head from the keys of the state dict and loads strictly.
  The v13 weights still load with it (checked on CPU: 469 keys, no pooler or dominance head).
- **Thresholds** from `thresholds.json` of the same revision (it names the weights' SHA-256): stenosis `balanced`
  per segment, FFR/iFR `balanced` (0.5936 / 0.6939), calcification 0.3631 and CTO 0.2134 (`specificity_99`),
  dominance 0.0383 (`balanced`). The thrombus flag keeps the fixed threshold **0.40** of 1.4.1
  (`lesion_threshold_overrides`). On the v15 validation split (83,646 segment readings, 200 with thrombus in the
  report) 0.40 flags 166 segments, 43 of them with thrombus (PPV 0.26, sensitivity 0.215); the `specificity_99`
  threshold 0.0648 flags 912, 79 with thrombus (PPV 0.087, sensitivity 0.395). With the v13 weights 0.40 flagged 60
  segments (PPV 0.30, sensitivity 0.09). The 0.40 threshold has not been evaluated on the test set.
- **New in `predictions`** (additions only):
  - `model_version`: `"v15"`, the model that produced the result.
  - `study_lesions.study_calcif_prob`, `.study_thrombus_prob`, `.study_cto_prob`: probability that the finding is
    present in any segment of the study. A probability only: no threshold, no yes/no call, and no performance figure
    in the release dossier.
  - `model_dominance.dominance_left_prob`, `.dominance_left_threshold` (0.0383), `.dominance_left_pred` (`true` =
    left dominant), `.operating_point`, `.label_source`: the model's own dominance estimate. Test AUROC 0.923
    [0.908, 0.937] against labels derived from another model, not expert reads.
  - `unsupported_segments`: `["lvp"]`.
- **Dominance and the schema.** The caller-supplied `additionalMetadata.coronaryDominance` contract is unchanged:
  it is returned as `predictions.coronary_dominance` and it alone decides whether the schema is drawn left or right
  dominant. Without it the schema is still drawn right dominant with "dominance not provided"; the model's estimate
  is printed next to it as text ("Model estimate: left dominant (P(left) …, threshold 0.0383; …)") and in the
  diagnosis, and never changes the drawing, because its labels are model-derived.
- **Posterior LV branch (`lvp`).** `model_config.json` lists it under `unsupported_outputs`: it had no training
  target in v15 and its values are not meaningful. `segments.lvp` keeps its numeric keys (`stenosis_pct`,
  `prob_significant`, `prob_threshold`, the three lesion probabilities) as the model writes them, gets
  `unsupported: true`, and its four flags are **always `false`**: with the validation threshold 0.0001 the raw rule
  would flag it in many studies (3 of the 24 studies of the dossier's parity check). It has no `display_pct` or
  `display_category`, and is never in the report, the diagnosis or the explanations.
- **Display calibration `cal1` refitted on v15** (`data/stenosis_display.json`): normal below 9.5; 9.5 → 20,
  21.5 → 30, 34.5 → 50, 49.5 → 70, 100 → 100. The report header reads `v15-cal1`. The v13 mapping moved to
  `data/stenosis_display_v13.json` and is never applied to a v15 result. See "Displayed stenosis".
- **Stored results.** `/inference/render-report` draws a result with the display values, calibration note,
  performance figures, recommendation and header of the model that produced it. A result without `model_version`
  was stored by 1.1.0 to 1.4.1 and is a v13 result: its stored `display_pct` / `display_category` are shown as stored
  (1.4.x), or computed with the v13 mapping when it has none (1.2.0, 1.3.0); its header and `reportModelVersion` read
  `v13-cal1`. The 1.4.1 example (a real v13 response and its report, `tests/fixtures/`) renders byte for byte as
  1.4.1 drew it. A result of a model version without a mapping in this container answers 422.
- **Not produced.** The post-PCI outputs of v15 (stent, final TIMI flow, failed PCI) need post-PCI runs as input.
- **Requirements** are unchanged: the dossier's `deployment/requirements.txt` is the same file as for v13, and the
  pinned PyTorch build (2.14.1, CUDA 13.0) is kept.

**What v13 changes against v12:** it adds the calcification, thrombus and CTO outputs. Stenosis is unchanged on the
test set (macro AUROC 0.885 against 0.884). The pressure AUROC is lower (0.69 against 0.72; the difference is within
its confidence interval, see "Verified performance"), and the lesion outputs are not better than the reference
models. Version 1.0.0 stays buildable from commit `82492f2` of `master`.

**What 1.4.1 changes against 1.4.0:** same weights and probabilities. The thrombus flag needs `thrombus_prob` above
**0.40** instead of 0.0851 (`Thresholds.lesion_threshold_overrides` in `models/config.json`); `lesion_thresholds`
returns `thrombus: 0.4` and `operating_point: "specificity_99 (thrombus: fixed 0.40)"`. The value was chosen on the
validation split of the served weights (4,647 studies, 200 segments with thrombus in the report) to raise the positive
predictive value: at 0.40, 60 segments are flagged and 18 have thrombus in the report (PPV 0.30, sensitivity 0.09);
at 0.0851, 902 are flagged (PPV 0.08, sensitivity 0.36). A PPV of 0.80 is not reachable: above 0.85 one segment is
left. The 0.40 threshold has not been evaluated on the test set. Results stored by earlier versions keep their flags.

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
- **Weights:** `model.safetensors`, no pickle, plus `model_config.json` (structure, heads, `unsupported_outputs`)
  and `thresholds.json` (decision thresholds). All three come from the same pinned revision:
  `download_model.py --extra-file model_config.json --extra-file thresholds.json` checks the weights' SHA-256, and
  `load_thresholds` refuses a thresholds file that was derived for other weights.
- **Model code:** `models/deepcoro_model/` is the `deepcoro_model/` of the Hugging Face revision `v15.1`, with one
  line kept from the earlier copy: `load_encoder` imports EchoJEPA's ViT code from the vendored `echojepa_src/`
  (MIT), where the released file appends `/volume/EchoJEPA` to the path (a directory of the training server).
  `load_model` also checks that the heads named in `model_config.json` are the ones the weights have.

## Inputs and outputs

- **Upload:** 1-12 XA multi-frame DICOMs, diagnostic left/right coronary contrast runs.
- **No metadata step:** `supportedAdditionalMetadata` is `[]`, so there is no Step-2 page and uploads are not
  filtered. PCI, stent and ventriculography runs were not in training.
- **JSON `predictions`:**
  - `model_version` (1.5.0 on): the model that produced the result (`v15`).
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
  - `unsupported_segments` (1.5.0 on): `["lvp"]`. For such a segment the numeric keys above are returned as the
    model writes them but are not meaningful; it has `unsupported: true`, `significant`, `calcif`, `thrombus` and
    `cto` are always `false` (the one exception to "flag = probability above threshold"), and it has no
    `display_pct` or `display_category`. Do not display it.
  - `lesion_thresholds`: `operating_point` and the three thresholds in use (`calcif`, `thrombus`, `cto`); one
    threshold per finding for all segments.
  - `study_lesions` (1.5.0 on): `study_calcif_prob`, `study_thrombus_prob`, `study_cto_prob`, the probability that
    the finding is present in any segment. No threshold and no yes/no call.
  - `lad_physiology`:
    - `ffr`, `ifr`: predicted values.
    - `ffr_abnormal_prob`: P(FFR ≤ 0.80); `ifr_abnormal_prob`: P(iFR ≤ 0.89).
    - `ffr_abnormal_threshold`, `ifr_abnormal_threshold`, and the calls `ffr_abnormal`, `ifr_abnormal`.
    - `operating_point`: which pressure thresholds were used.
  - `model_dominance` (1.5.0 on): `dominance_left_prob` (P(left-dominant circulation)), `dominance_left_threshold`,
    `dominance_left_pred` (`true` when the probability is strictly above the threshold, i.e. left dominant;
    `inference.py` writes the same call as 0/1), `operating_point` (`balanced`) and `label_source`.
  - `coronary_dominance`: only when the caller sent `additionalMetadata.coronaryDominance` (unchanged since 1.2.0).
  - `n_videos` and `encoder_passes`; `explainability` (flagged segments, at most 6).
- **`diagnosis`:** lists the flagged segments (shown % and model estimate), the other segments shown at 30% or
  more, how many segments are shown as normal, the two pressure calls, the segments flagged for each lesion
  finding, the study-level lesion probabilities, the model's dominance estimate and, when sent, the caller's
  dominance.
- **HTML:** the segment table (threshold, flag and the three lesion probabilities) plus LAD physiology.
- **Example:** [`docs/examples/predict_response_json.json`](docs/examples/predict_response_json.json) and the HTML
  report [`docs/examples/predict_report.html`](docs/examples/predict_report.html), with every threshold in them.
  The same response is the example of `/inference/predict` in `docs/openapi.json`. It holds no patient or study
  identifier. **It is derived, not a live response of the built image** (see "Verification of this version"): the
  model outputs are those the released v15 `inference.py` wrote for one validation study in the dossier's parity
  check, passed through this container's `postprocess`, thresholds, display calibration, diagnosis and report
  code. To do: replace it with the response of the built 1.5.0 image for a study.
- **Failures:**
  - No readable run: the request succeeds with empty `predictions` and the text "No video could be extracted".
  - The model call fails (for example the GPU runs out of memory): the request fails with HTTP 500 and
    `errorCode` `PROCESSING_ERROR`. It is never reported as a missing video.

## Displayed stenosis

The regression head shrinks toward the mean. Printed as is, few segments read 0% and few lesions read 70% or more:
on the v15 validation split the raw estimate falls in the report's category for 24% of the segment readings. From
1.4.0 the report shows `display_pct`. The mapping belongs to one model version; this is the one of v15
(`data/stenosis_display.json`, method `cal1` refitted on v15):

| Model estimate (`stenosis_pct`) | Shown |
|---|---|
| below 9.5 | 0% (normal) |
| 9.5 → 21.5 → 34.5 → 49.5 → 100 | 20 → 30 → 50 → 70 → 100%, linear in between, rounded down |
| CTO flag | 100% (occluded) |
| significant flag and estimate below 9.5 | 20% (mild): a flagged segment is never shown as normal |

How it was chosen (segments pooled, because single segments have few lesions; all numbers from
`display_mapping.json` and `thresholds/display_mapping.json` of the v15 release dossier):

- **Data.** Validation split only, served v15 weights (epoch 1): 4,647 studies × 17 segments = 78,999 segment
  readings with the report stenosis. The posterior LV branch is not in the fit and is not displayed. No test data
  was used.
- **T0 (shown as normal below it).** Among cuts in steps of 0.5, the largest cut whose Youden's J for "report
  stenosis > 0%" is within 0.002 of the maximum: 9.5. With it 74.4% of the segments the report calls 0% are shown
  as normal. The dossier does not give the sensitivity, specificity, PPV and NPV of this cut.
- **Knots.** The estimate cuts for 30, 50 and 70% (1-point grid from T0 + 1) maximise the quadratic-weighted kappa
  of the five categories (0, < 30, 30-49, 50-69, ≥ 70): 21.5 / 34.5 / 49.5. They are used as fitted; the half points
  are not rounded. 5-fold cross-validation grouped by patient (3 seeds) gave T0 9.5 to 10.5 and knots 21-22 / 34-35
  / 47-50.5.
- **Rounding.** The fit assigns a category from the cuts (estimate < 21.5 is "< 30"). The interpolated value is
  therefore rounded **down**, so that an estimate of 21.4 is shown as 29% and 21.5 as 30%: the shown category is
  the fitted category for every estimate. (The v13 mapping rounds to the nearest integer, which moves its
  boundaries down by about half a shown point; it is left as it was.)
- **Result on validation.** Exact category agreement 23.8% → 64.4% and weighted kappa 0.393 → 0.584 for the mapping
  (cross-validated: 64.9% and 0.583). With the rule that a flagged segment is never shown as normal, which is what
  the report shows: 60.2% and 0.578 (the rule changes 4,154 readings).
- **Lesions shown as normal.** 11.6% of the lesions reported at 70% or more have an estimate below 9.5% and would
  be shown as normal by the mapping alone; with the flag rule 7.7% are. The raw estimate shows 0.4% of them as
  normal, and 16.9% of the report's 0% segments as normal (mapping: 74.4%; with the flag rule 68.4%). The flags,
  not the shown %, are the screening output.
- **Not in the dossier for v15:** per-vessel and per-segment kappa, the mean report value inside each shown
  category, and confidence intervals. They were published for the v13 mapping (README of 1.4.1).
- **Flags.** Unchanged by the display.

**Results of model v13.** The v13 mapping (`data/stenosis_display_v13.json`: normal below 10; 10 → 20, 23 → 30,
37 → 50, 51 → 70, 100 → 100; rounded to the nearest integer; fitted on the v13 validation split, cross-validated
agreement 66%, kappa 0.59) is kept only so that `/inference/render-report` can draw results stored by 1.1.0 to
1.4.1. Which mapping a result gets depends on the result, never on the served model:

| Stored result | Shown % | Header |
|---|---|---|
| has `display_pct` and `display_category` (1.4.0 on, any model) | the stored values, not recomputed | its own model version |
| no display fields, no `model_version` (1.2.0, 1.3.0: model v13) | computed with the v13 mapping | `v13-cal1` |
| no display fields, `model_version: "v15"` | computed with the v15 mapping | `v15-cal1` |
| `model_version` without a mapping here | refused (422), not drawn with another mapping | |

The same estimate is shown differently by the two models (22.0% → 29% for v13, 30% for v15), so the mappings must
not be exchanged.

## Decision thresholds

The thresholds are read from `thresholds.json` of the pinned Hugging Face revision (v15: derived for weights
`23b8255b…c54c`). They were chosen on the **validation** split only (4,647 studies). A call is positive when the
returned probability (6 decimals) is strictly above its threshold.

- **Stenosis, pressure and dominance:** the rule of the deployed DeepCORO-CLIP checkpoint, i.e. the threshold with
  the highest geometric mean of sensitivity and specificity (`balanced`).
- **Calcification and CTO:** the lowest threshold with a validation specificity of at least 0.99
  (`specificity_99`). The balanced point would raise two to four flags per study for each finding, nearly all false.
- **Thrombus:** the fixed threshold 0.40 (`lesion_threshold_overrides`, since 1.4.1, kept for v15 by the model
  owner), not the `specificity_99` point 0.0648. Validation split of v15 (83,646 segment readings, 200 with thrombus
  in the report): 166 segments flagged at 0.40, 43 with thrombus (PPV 0.26, sensitivity 0.215), against 912 flagged,
  79 with thrombus (PPV 0.087, sensitivity 0.395) at 0.0648. **The 0.40 threshold has not been evaluated on the test
  set.**
- **Study-level lesion probabilities:** no threshold and no call.
- **Posterior LV branch:** `thresholds.json` still holds a threshold for it (0.0001); the container returns it as
  `prob_threshold` but never raises the flag (`unsupported_outputs`).

The test split was used only to report the numbers below; the v15 thresholds were fixed on validation before the
test set was read (`thresholds/report.json` of the release dossier). The rounding check of 1.1.1 (calls on the
6-decimal probability against calls on the unrounded one) was done on v13 predictions and was not repeated for v15.
FFR ≤ 0.80 and iFR ≤ 0.89 are the label cut-offs the pressure head was trained on (the cut-offs of FAME and of
DEFINE-FLAIR / iFR-SWEDEHEART); for v15 the labels are proximal or mid LAD measurements only. The thresholds are
internal operating points, not validated clinical ones.

`models/config.json`:

```json
"Thresholds": {"path": "thresholds.json", "pressure_operating_point": "balanced", "lesion_operating_point": "specificity_99",
               "lesion_threshold_overrides": {"thrombus": 0.4}}
```

| Key | Meaning | Default |
|---|---|---|
| `path` | Thresholds file inside `models/`, fetched at build | `thresholds.json` |
| `pressure_operating_point` | `balanced`, `high_sensitivity` or `high_specificity`; applies to FFR and iFR. Segments always use `balanced`. | `balanced` |
| `lesion_operating_point` | `specificity_99`, `specificity_95` or `balanced`; applies to calcification, thrombus and CTO. Required for every checkpoint, used by those with a lesion head. | `specificity_99` |
| `lesion_threshold_overrides` | Optional. Fixed threshold for one or more lesion findings, replacing the operating point for them: `{"thrombus": 0.4}` (since 1.4.1). Names: `calcif`, `thrombus`, `cto`; values strictly between 0 and 1. | `{"thrombus": 0.4}` |

All three keys are required. An unknown or missing key, an unknown operating point, a missing file, a missing output,
a threshold outside (0, 1) or a file derived for other weights stops the container at start-up. There is no fallback value.

LAD pressure calls on the held-out test split, v15 (proximal or mid LAD measurements. FFR: 76 measurements, 29
abnormal; iFR: 194, 94 abnormal, one of them without a prediction):

| Output | Operating point | Threshold | Sensitivity | Specificity | PPV | NPV |
|---|---|---|---|---|---|---|
| FFR ≤ 0.80 | `balanced` | 0.5936 | 0.759 | 0.702 | 0.611 | 0.825 |
| FFR ≤ 0.80 | `high_sensitivity` | 0.4326 | 0.862 | 0.362 | 0.455 | 0.810 |
| FFR ≤ 0.80 | `high_specificity` | 0.7571 | 0.310 | 0.894 | 0.643 | 0.677 |
| iFR ≤ 0.89 | `balanced` | 0.6939 | 0.532 | 0.720 | 0.641 | 0.621 |
| iFR ≤ 0.89 | `high_sensitivity` | 0.3024 | 0.851 | 0.350 | 0.552 | 0.714 |
| iFR ≤ 0.89 | `high_specificity` | 0.8222 | 0.287 | 0.860 | 0.659 | 0.562 |

Segment flags on the 3,194 test studies (`prob_significant` above the segment's threshold):

| Mean over | Sensitivity | Specificity | PPV | NPV |
|---|---|---|---|---|
| the 17 supported segments (mean of the per-segment values of `thresholds/report.json`) | 0.796 | 0.787 | 0.259 | 0.979 |
| 18 segments, posterior LV branch included (as printed in the dossier) | 0.781 | 0.794 | 0.246 | 0.980 |

Per segment, the test PPV goes from 0.05 (distal LCx) and 0.08 (ramus) to 0.47 (mid LAD); the full table with
confidence intervals is `thresholds/report.json`.

Calcification, thrombus and CTO flags on the same test studies: 57,492 study-segment pairs, of which 1,110 have
calcification (1.9%), 104 thrombus (0.2%) and 382 CTO (0.7%) in the report.

| Finding | Operating point | Threshold | Sensitivity | Specificity | PPV | NPV | Flags per study |
|---|---|---|---|---|---|---|---|
| Calcification | `specificity_99` (default) | 0.3631 | 0.195 | 0.9895 | 0.268 | 0.984 | 0.25 |
| Calcification | `specificity_95` | 0.1203 | 0.523 | 0.951 | 0.173 | 0.990 | 1.05 |
| Calcification | `balanced` | 0.0176 | 0.833 | 0.810 | 0.079 | 0.996 | 3.645 |
| Thrombus | `specificity_99` (not in use: the flag has the fixed threshold 0.40, which was not evaluated on this test set) | 0.0648 | 0.548 | 0.992 | 0.113 | 0.999 | 0.16 |
| Thrombus | `specificity_95` | 0.0103 | 0.712 | 0.961 | 0.032 | 0.9995 | 0.72 |
| Thrombus | `balanced` | 0.0030 | 0.740 | 0.885 | 0.012 | 0.9995 | 2.10 |
| CTO | `specificity_99` (default) | 0.2134 | 0.353 | 0.990 | 0.195 | 0.996 | 0.22 |
| CTO | `specificity_95` | 0.0284 | 0.634 | 0.950 | 0.079 | 0.997 | 0.965 |
| CTO | `balanced` | 0.0063 | 0.812 | 0.863 | 0.038 | 0.999 | 2.54 |

Left dominance (`dominance_left_pred`, threshold 0.0383), validation split only (3,764 studies with a label of
agreement weight ≥ 0.9, 228 left dominant): sensitivity 0.934, specificity 0.850, PPV 0.287, NPV 0.995. The dossier
gives no test operating point for dominance, only the test AUROC (see "Verified performance"). The labels are
model-derived.

Read the flags with these limits in mind:

- **A flag is a prompt to look, not a diagnosis.** One flagged segment in four is significant in the report
  (PPV 0.26). An unflagged segment is rarely significant (NPV 0.98).
- **Most studies get at least one flag.** At least one segment is flagged in 97% of the studies with a significant
  lesion, and in 41% of the studies without one (dossier figures, computed with the posterior LV branch flag that
  this container never raises; the figures without it are not in the dossier).
- **Rare segments have very low thresholds** (for example 0.0082 for distal LCx), because significant lesions there
  are rare in the validation split.
- **The flag comes from the probability, not from `stenosis_pct`.** The estimated percentage is pulled toward the
  mean, so a rule on the percentage misses most lesions (measured on v12: sensitivity 0.14).
- **Lesion flags miss most findings and most of them are false.** At the default point the model finds 19% of the
  calcified segments and 35% of the CTO segments on test, and 27% and 19% of those flags are right. For thrombus at
  0.40 only validation numbers exist (sensitivity 0.215, PPV 0.26). A missing flag does not exclude the finding.
- **Every study gets all the outputs.** The container does not check which arteries were filmed. A study with
  right coronary runs only still returns values for the left coronary segments and for LAD physiology, and these
  are not based on images of those vessels. The dominance estimate is less reliable without a right coronary run.
- **The FFR and iFR calls can disagree.** They are two outputs with two thresholds, and the model does not know
  which wire was used.
- **The model's dominance and the caller's dominance can disagree.** Both are returned; the schema follows the
  caller's.

## Verified performance

Held-out MHI test: 3,194 studies / 2,941 patients, with patient-bootstrap 95% CIs (1,000 replicates). The v15
checkpoint (epoch 1) was locked on validation, then the test set was read once. Numbers from `performance/` of the
v15 release dossier; "17 segments" leaves out the posterior LV branch.

| | v15 (served) | v13 | v12 | DeepCORO-CLIP | EchoJEPA |
|---|---|---|---|---|---|
| Stenosis macro AUROC, 17 segments | **0.877** | 0.883 | 0.881 | 0.846 | 0.848 |
| Stenosis macro AUROC, 18 segments | 0.877 [0.869, 0.885] | 0.885 [0.877, 0.892] | 0.884 [0.877, 0.891] | 0.850 | 0.851 |
| Mean per-segment Pearson r, 17 segments | **0.508** [0.495, 0.519] | 0.521 [0.510, 0.533] | 0.519 [0.507, 0.530] | 0.333 | 0.449 |
| Segments with r > 0.5 | 8 | 9 | 9 | 6 | 7 |
| Mean ICC(A,1), 17 segments | **0.438** [0.426, 0.449] | 0.461 [0.449, 0.473] | 0.465 [0.453, 0.476] | 0.190 | 0.300 |
| LAD FFR/iFR pooled AUROC, proximal or mid LAD (270 measurements) | **0.674** [0.605, 0.737] | 0.689 [0.624, 0.756] | 0.721 [0.654, 0.785] | not available | not available |
| Dominance AUROC, model-derived labels (2,943 studies, 203 left dominant) | **0.923** [0.908, 0.937] | no such output | | | |

Paired differences on the same studies, v15 minus v13:

- **Stenosis is slightly lower.** Mean r −0.014 [−0.020, −0.008]; mean ICC −0.023 [−0.028, −0.017]; macro AUROC
  (17 segments) −0.006 [−0.009, −0.002]. These intervals exclude zero. The per-model intervals of r overlap.
- **Pressure is not distinguishable.** −0.015 [−0.059, 0.030]. Against v12 it is lower: −0.047 [−0.094, −0.002].
  FFR alone 0.71 (76 measurements), iFR alone 0.67 (194).
- **Lesion detection is better.** Mean of the six lesion AUPRCs 0.222 against 0.189, +0.034 [0.012, 0.051];
  thrombus, macro, +0.098 [0.025, 0.159].
- **Weaker segments:** r is 0.24 to 0.45 for the posterolateral branch, distal LAD, distal LCx, diagonals, marginals
  and ramus.

Release decision, as recorded in `decision/release_rule.md`: the rule written before the test read-out (stenosis and
pressure no more than 0.01 below v13, lesions better) was **not met** for stenosis r (−0.014) and pressure
(−0.015). It was amended after the stenosis numbers were seen to "the per-model 95% CIs overlap", and v15 was
released under the amended rule.

Lesion outputs, area under the precision-recall curve (AUPRC; "macro" is the mean over the segments with at least
10 positive studies, "micro" pools all study-segment pairs). The reference models are DeepCORO-CLIP and the two
EchoJEPA lesion baselines (the dossier prints the better one per finding):

| Finding (macro / micro AUPRC) | v15 (served) | v13 | DeepCORO-CLIP | Best EchoJEPA baseline |
|---|---|---|---|---|
| Calcification | **0.122 / 0.188** | 0.117 / 0.181 | 0.116 / 0.180 | 0.125 / 0.203 (A) |
| Thrombus | **0.402 / 0.321** | 0.304 / 0.224 | 0.331 / 0.224 | 0.410 / 0.305 (B) |
| CTO | **0.139 / 0.160** | 0.133 / 0.171 | 0.125 / 0.170 | 0.139 / 0.201 (B) |

v15 is above v13 on five of the six numbers (CTO micro is lower, 0.160 against 0.171). Against the best EchoJEPA
baseline it is above on thrombus micro only, equal on CTO macro and below on the others; the dossier gives no
paired interval for these comparisons.

Not measured or not in the dossier: the study-level lesion probabilities (`study_lesions`); the thrombus flag at
0.40 on the test set; a test operating point for dominance; dominance against expert reads. The post-PCI heads
(treated against untreated diseased segments, AUROC 0.794 [0.763, 0.826] on 754 studies) are not emitted by this
container. The v13 numbers above are those of the v15 dossier; the README of 1.4.1 printed the pressure AUROC of
v13 on all 274 measurements (0.692) and its calcification macro AUPRC as 0.118.

## Verification of this version

**1.5.0 (model v15).** This version was written on the production server from the release dossier, without a GPU:
the model was not run by this container's code.

- **Done by the model team (release dossier, `deployment/`).** `inference.py` of the released code on 24 validation
  studies (the training AVI files) against the training-time validation predictions of epoch 1: largest difference
  0.0005 stenosis points and 0.000005 on every probability (stenosis, lesions per segment and per study, dominance,
  FFR, iFR), no stenosis call changed (`parity.json`). The v13 weights with the v15 code on 5 studies: largest
  stenosis difference 0.0005, no extra output key (`backward_compat_v13.json`).
- **Done here, on CPU.** The vendored model code built the model from `model_config.json` and loaded
  `model.safetensors` of the dossier strictly: 495 keys, none missing or unexpected, lesion head, study lesion
  pooler, dominance head, procedure head and physiology head present; the file's SHA-256 is the one in
  `data/model_info.json`; `load_thresholds` accepted `thresholds.json` (24 thresholds, thrombus 0.40, dominance
  0.0383). The v13 weights of image 1.4.1 loaded strictly with the same code (469 keys). The study lesion pooler
  and the dominance head were called on random features to check the shapes `postprocess` reads.
- **Example.** `docs/examples/` is derived from row 11 of the dossier's `deployment/parity_inference.csv`: the
  outputs of the released `inference.py` for one validation study (7 runs), given to this container's
  `postprocess` with the v15 thresholds, then to its diagnosis and report code. The derivation checks that every
  probability of the example equals the dossier's (which has 5 decimals, so the 6th decimal is 0), and that every
  call equals the released code's call, except the thrombus flags (0.40 here, `specificity_99` there) and the
  posterior LV branch (never flagged here). It has no `explainability` items, because those need the forward pass.
- **Stored results.** The response and the report that 1.4.1 documented (a real v13 result) are kept in
  `tests/fixtures/`; this code draws that result byte for byte as 1.4.1 did, with and without its display fields.
- **Not done, to do on a GPU with the built image.** No forward pass through `CustomPredictionService.predict`;
  no DICOM-path check for v15 (the dossier's parity check used the training AVI files); no comparison of this
  container's output with `inference.py` on the same DICOM files; no check of the encoder-pass count, the peak GPU
  memory (the study lesion pooler attends over all pass-1 tokens) or the explanation crops with v15; no rerun of
  the PyTorch-build comparison below, which was measured with v13: the v15 test metrics come from the development
  environment (NVIDIA build of PyTorch 2.14), not from the pinned build of this image. No image was built.

**Earlier versions (model v13, kept for reference).** Done on the development server as
`.claude/skills/pacs-ai-model-mapping` prescribes, on a sample of the test set:

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
- **Example of 1.1.2 to 1.4.1.** The response of that code, with the pinned requirements of 1.1.2, to one of the 40
  studies (5 runs); now `tests/fixtures/stored_v13_1.4.1_response.json`.
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
| Provenance | commit-pinned revision (`ab6d12f7aa6c547cfdf5b47d3517d95566f6de0c`) and weight SHA-256 in `data/model_info.json`, checked at build and by the tests |
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
docker run -d --rm --gpus all --name deepcoro-jepa -p 127.0.0.1:8080:80 heartwisehub/deepcoro-jepa:1.5.0
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
  Not measured with v15, whose study lesion pooler adds an attention over all pass-1 tokens: measure it on the first
  GPU run of the built image and raise `resources.peakMemoryMiB` if needed.
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
- the display calibration: knots, T0, the category boundaries at the fitted half points, the CTO and flag rules,
  monotonicity, the JSON fields, the report and the diagnosis wording, for the v15 mapping and for the v13 one;
- stored results: a v13 result with display fields, a v13 result without them (1.2.0, 1.3.0) and a new v15 result
  each get their own mapping, header, calibration note and performance figures; the real 1.4.1 example renders byte
  for byte; a result of an unknown model version is refused;
- the v15 outputs: study-level lesion probabilities without a call, the model's dominance and its threshold, the
  caller's dominance keeping the schema, and the posterior LV branch never flagged, displayed or explained;
- that the vendored loader builds the v15 heads from the state dict, loads strictly and still loads a v13 state
  dict;
- output postprocessing, the yes/no calls and the strict "above the threshold" rule, for stenosis, pressure and the
  three lesion findings;
- that every call follows the returned 6-decimal probability, and that the diagnosis and the report never print a
  probability on the wrong side of its threshold;
- that the model facts name the training run of the served weights;
- that a wrong or incomplete thresholds file or `Thresholds` block fails loudly;
- provenance and weight-hash verification;
- that the OpenAPI examples match `data/`;
- that the example response in `docs/examples/` is the one in the OpenAPI file, carries the v15 thresholds and
  display values, agrees with its own thresholds, and that its diagnosis and report are what this code writes.
