# DeepECG-CLIP-DINOv3

Research-only PACS.AI inference service for HeartWise's DeepECG-CLIP DINOv3 12-lead ECG model
(HeartWise-AI/DeepECG-PNG). One ECG goes in; every available head comes out in one result.

> **Not for clinical use.** Each output is a model probability compared with a validation threshold. It is
> not a calibrated risk or a diagnosis. There is no external validation and no regulatory clearance. The
> weights derive from Meta DINOv3 and are subject to the DINOv3 license.

## Heads

| id | kind | output | threshold (validation Youden) | held-out test (MHI ACS v6, 9,021 ECGs) |
|---|---|---|---|---|
| `aco` | fine-tuned | acute coronary occlusion vs none | 0.05062838 | AUROC 0.92 [0.89, 0.93]; sens 0.83, spec 0.87, NPV 0.99 |
| probes | linear probe | any head released under `probes/` (see below) | per head | per head (`model-facts`) |

The `aco` fine-tune comes from the DINOv3-SigLIP2 pretraining (W&B `mhi_ai` run zo59vdxk, epoch 30) with the
top 2 blocks, the final norm and a linear head trained (DeepECG-PNG commit 1f6fb84, PR #224). The service runs
the first 10 blocks as a shared trunk, then the head's own top blocks (`models/dinov3_heads.py`, which can
serve several fine-tunes that share those blocks and refuses checkpoints that do not).

Since 1.4.0 the `acco_vs_aico` fine-tune (complete vs incomplete occlusion, AUROC 0.69 among occlusions) is no
longer served or downloaded (owner's decision, 2026-11-26): its checkpoint stays in the model revision and on the NAS.

### Frozen linear probes (77 patterns, SHD, LVEF, AFib 5 y, HCM, ...)

Since 1.1.0 the release serves six clean probes from `acs_ft20_dinov3_siglip2_epoch030/probes/`
(fitted on frozen features of clean rendered pages, scored on clean MHI test pages; threshold = validation
Youden; 95% CI = 1,000 resamples of 80% of the patients):

| folder | outputs | test AUROC [95% CI] | threshold |
|---|---|---|---|
| `ecg77_clean` | 77 ECG patterns | 0.97 [0.97, 0.97] (macro) | probabilities only |
| `shd_clean` | `SHD` | 0.77 [0.76, 0.78] | 0.5374 |
| `lvef_le40_clean` | `LVEF<=40` | 0.89 [0.88, 0.90] | 0.1695 |
| `lvef_lt50_clean` | `LVEF<50` | 0.87 [0.86, 0.88] | 0.2696 |
| `afib_5y_clean` | `iAFib5` | 0.68 [0.65, 0.70] | 0.1113 |
| `hcm_clean` | `confirmed HCM (vs rest)` | 0.71 [0.66, 0.75] | 0.0027 |

Not served: the ACO and ACCO-vs-AICO probes (the ACO fine-tune is better on the same task), the
probes fitted on augmented pages (same output ids as the clean ones), and the 3-class HCM head (softmax).
All 18 probe heads (clean and augmented, float64 copies, full provenance) are in the private Hugging Face
repository `heartwise/DeepECG-PNG_DINOv3-SigLIP2_epoch030`. Each served head reproduces its saved
validation scores from the cached features within 3e-6.

Since 1.3.0 the probe heads are loaded, validated and scored by **DeepECG_Docker's own code**:
`models/dinov3_bank.py` is vendored unchanged from HeartWise-AI/DeepECG_Docker (`_DeepECG_DINOv3_Bank.load_head`,
`head_probabilities`), and the head folders are in its format (the same files as
`heartwise/DeepECG-PNG_DINOv3-SigLIP2_epoch030`, which DeepECG_Docker serves):

- `probes/config.json`: encoder checkpoint (`base/epoch_030.pt`, sha256, `image_encoder`, strict), preprocessing
  (448x896 stretch, grayscale, ImageNet mean/std) and feature (dim 1536); every head's `config.json` equals it;
- `head.json`: `name` = `heartwise/DeepECG-PNG_DINOv3-SigLIP2_epoch030/probes/<folder>`, outputs (those of
  `dinov3_bank.PROBE_OUTPUTS` for the folder's task), encoder {repo, `base/epoch_030.pt`, sha256}, classifier
  {linear, float32, input_dim 1536, standardize, sigmoid or softmax};
- `thresholds.json`: the validation Youden cutoff of each output, or `{}` for a probability-only head;
- folder names `<task>_clean` or `<task>_aug`.

`models/probes.py` runs the bank's configuration and encoder-hash checks and its `load_head` unchanged, but
not its encoder: the frozen features (concat[CLS, mean patch tokens] of `base/epoch_030.pt`, 1536-d) come
from this container's shared trunk, which runs the frozen encoder's top 2 blocks on the trunk output in the
same forward pass as the fine-tunes. With the same weights, 1.3.0 returns exactly the outputs of 1.2.1
(336 outputs over 4 ECGs, max |diff| 0).

Adding heads needs no code change: upload the head folders under `probes/` in the Hugging Face repository,
then bump `modelRevision` in `data/model_info.json` (and `PACS_AI_MODEL_REVISION` in `Dockerfile.local`)
and the minor version. `download_model.py` checks every head with the bank and every file of
`probes/SHA256SUMS`.

### Reuse of DeepECG_Docker

Decision: share the formats and vendor DeepECG_Docker's ECG input code unchanged; do not depend on the
DeepECG_Docker package.

- Vendored unchanged from DeepECG_Docker `a46499c5` (hashes checked by `tests/test_vendored.py`; update by
  copying newer files): `models/dinov3_bank.py` (probe heads), `utils/log_config.py`,
  `utils/ecg_page_renderer.py` (training renderer), `utils/ecg_pdf.py` (vector PDF
  reader), `utils/image_preprocessing.py` (`preprocess_ecg_image`, `open_png_bytes`; with the bank's
  `PAGE_FIXES`: EXIF orientation and transparency on white, no-ops on training pages). A PDF is read exactly
  as DeepECG_Docker's `load_ecg_pdf` reads one; every page goes through `preprocess_ecg_image` with the
  `preprocessing` block of `models/encoder_config.json`.
- Same head format and probability formula as DeepECG_Docker's SigLIP2 heads (above).
- Not reused: DeepECG_Docker's DINOv3 encoder class (this container's shared trunk computes the same
  features once for the fine-tunes and the probes) and its CSV batch pipeline; PACS-AI needs a FastAPI
  service with the PACS-AI predict, model-info and provenance contracts.

## Inputs

PACS.AI sends the DICOM objects of the study. The service scores one ECG per request, picking the best
readable object: a waveform first, then a PDF, then an image; within a kind, the earliest acquisition.

| input | DICOM | how the page is made | fidelity |
|---|---|---|---|
| waveform | 12-lead / general ECG waveform (`1.2.840.10008.5.1.4.1.1.9.1.1`, `.9.1.2`), modality ECG | leads I, II, V1-V6 of the rhythm group to MUSE counts (4.88 uV), 250 Hz as in training (500 Hz: every other sample), III/aVR/aVL/aVF derived from I and II, drawn by the training renderer (`utils/ecg_page_renderer.py`) | Same pixels as the training pages (checked on real test ECGs, below). Rates that are not a multiple of 250 Hz are resampled. At least 10 s needed. |
| pdf | Encapsulated PDF (`1.2.840.10008.5.1.4.1.1.104.1`), modality DOC | vector traces read from the PDF (`utils/ecg_pdf.py`, standard 3x4 + lead II rhythm layout only; scale and lead labels checked), redrawn by the training renderer | Close, not identical: the traces are interpolated from the drawing. Scanned PDFs and other layouts are refused. |
| image | any other single-frame image (secondary capture), modality OT | the pixels as they are | Least faithful: not the training layout. The model saw such pages only through training augmentation (other layouts, photographs). |

The renderer, the PDF reader and the preprocessing are DeepECG_Docker's (see above). Preprocessing equals
DeepECG-PNG `utils/image.process_image`: grayscale, stretch resize to 448x896, 3 channels, ImageNet
mean/std (`tests/test_ecg_inputs.py` checks it against the torchvision pipeline of training).

Render styles: about 70% of the training and test pages are the MUSE-era style the renderer draws; the
other 30% came from a later export (2500x1250, black lead ticks, smaller labels). On such ECGs a waveform
input gives slightly different outputs from the stored training page (largest change seen: ACO 0.890 vs
0.864).

**Routing.** PACS.AI does not yet send ECG studies to any model: study-service maps modality ECG to `ecg`
but has no ECG model in its registry, and drops DOC and OT series. Registering this model in CardioAgent
(`add-study-service-model`) and accepting DOC/OT are separate steps.

## Result (JSON)

`predictions.heads` has one self-describing entry per head, in a fixed order:

```json
{"id": "aco", "head": "aco", "label": "Acute coronary occlusion (ACO)", "description": "...",
 "task": "binary", "kind": "fine-tuned", "probability": 0.4213, "threshold": 0.05062838,
 "positive": true, "applicable": true, "applicableWhen": null}
```

`id` is the output name (a probe head's `head.json` `outputs`, the column names DeepECG_Docker writes);
`head` is the head it comes from (a probe folder name). `positive` is null when the output is not
applicable (an output with `applicableWhen` whose parent is negative; none is served now) or has no threshold (probability-only head). `predictions.input` names the object that was read. Probabilities
are rounded to 6 decimals before anything reads them (flags included), so a flag differs from the unrounded
rule only within 5e-7 of its cutoff. The report shows 3 decimals, or all 6 for an output whose cutoff is below
0.01 (HCM, cutoff 0.0027), with that cutoff to 7 decimals. Example: `docs/examples/predict_response_json.json`.

Select outputs with `additionalMetadata: {"heads": ["aco", "<head key>", "<output id>", ...]}`; a head key
selects all its outputs, and an output with `applicableWhen` brings its parent. Without it, every head is returned.

### Calling it

Through api-pacs (permission `models.use`; the container must be registered):

```
POST /v1/inference/model/proxy/container/{containerID}/predict
{"studyInstanceUID": "...", "seriesInstanceUIDs": ["..."], "forceJSON": true,
 "additionalMetadata": {"heads": ["aco", "shd_clean"]}}
```

Directly against the container (port 80 behind nginx, or 8000):

```
POST /api/inference/predict
{"outputMode": "JSON", "seriesInstanceImages": {"1": {"1": "<base64 DICOM>"}},
 "additionalMetadata": {"heads": ["aco"]}}
```

`GET /api/inference/model-info` and `/api/inference/model-facts` describe the model;
`POST /api/inference/render-report` with `{"result": <data of a JSON-mode response>}` returns the HTML
report of a stored result without loading the model (422 for a result this version would not write).

## Weights

Hugging Face `heartwise/deepecg_clip_dinov3_acs` (private), revision
`7d35c2e75b8013097c9bb4a578750434e9e10be2` (tag `v1.3.0`; probes/ in the DeepECG_Docker bank format), uploaded from the NAS release folder
`/mnt/data1/models/DeepECG-PNG/acs_ft20_dinov3_siglip2_epoch030/` (same bytes; `SHA256SUMS` there):

| file | SHA-256 |
|---|---|
| `aco/best_model.pth` (manifest checkpoint) | `49db22c3f252d6ba13ec87562df07e4da4d68e5d1ca282dffb484b7c10459026` |
| `acco_vs_aico/best_model.pth` (not downloaded since 1.4.0) | `55091186aaf36d66eab1fb048a1c4177fa1534bd25cc84ac4a0e4e413f74ba29` |
| `base/epoch_030.pt` (only with probes) | `335aaec39064d009c31710a96fe17d98450d85611c5bb0271ce3aad118149ae6` |

`download_model.py` fails the build on any missing file or hash mismatch. The architecture is built from
`models/config.json`; the gated `facebook/dinov3-vitb16-pretrain-lvd1689m` repository is never contacted.
The checkpoints pickle the training config object; `models/dinov3_heads.py` loads them without the
DeepECG-PNG code by replacing only that class.

## Build

```bash
python3 scripts/release-model.py model-examples/DeepECG-CLIP-DINOv3 \
  --image-repository heartwisehub/pacs-ai-deepecg-clip-dinov3 \
  --hf-token-file /secure/path/hf_token
```

The token needs read access to `heartwise/deepecg_clip_dinov3_acs`. `Dockerfile.local` builds from the NAS
copy instead (instructions at its top) and is for development only.

## Verification

- `pytest model-examples/DeepECG-CLIP-DINOv3/tests` (CPU, no weights): input conversion against the
  training converter, the renderer's golden pixels, PDF and image inputs, the vendored files, the shared
  trunk against the training classifier, DeepECG_Docker-format heads, predict and render-report.
- Against the released checkpoints (CPU, 8 MHI ACS test ECGs, not committed): the service's outputs on
  the stored training pages equal the training-code predictions (max difference 6e-5 ACO, 2e-5 ACCO);
  preprocessing is identical (max difference 0.0). Waveform DICOMs built from the MUSE XML of the 6
  MUSE-era ECGs rendered 100% of the training pixels and gave the same outputs (max difference 1e-6).
- Resources (`data/model_info.json`): measured on image 1.1.0 (one RTX A6000, synthetic waveform ECGs,
  `scripts/benchmark-model-resources.py`): 1,510 MiB VRAM resident and peak (598 MiB of it at startup),
  4.2 s first request, 1.63 s median after (full HTTP round trip with page rendering), 1.7 GiB RAM.
  `residentMemoryMiB` 1600 and `peakMemoryMiB` 2000 leave headroom above that.

## Changelog

- 1.5.1: security patch only. Pillow 12.3.0 -> 13.0.0 (Dependabot advisories for Pillow <= 12.3.0). The renderer golden-pixel test and
  the predictions on the fixtures and synthetic ECGs are unchanged (see the PR for the side-by-side numbers).
- 1.5.0: image (secondary capture) ECGs in YBR_FULL / YBR_FULL_422 are no longer colour-converted twice
  (pydicom 3 `pixel_array` already returns RGB; the second conversion shifted the page colours and the
  intensities the model reads). When a study holds several ECGs of one kind, the earliest is now chosen by
  `AcquisitionDateTime` before the content, series and study timestamps (it was consulted only when none of
  them was set, so ECGs sharing a study date/time were taken in request order). Same weights, heads and
  outputs; predictions change only for those inputs. Minor bump (inference behaviour change).
- 1.4.0: `acco_vs_aico` (ACCO vs AICO) removed from the service by the owner's decision: no longer loaded,
  downloaded, returned or mentioned in the report; the reject-feedback option "wrong occlusion type" goes too.
  83 outputs (`aco` + the six probe heads); every remaining output is unchanged. Minor bump (prediction output changes).
- 1.3.0: probe heads loaded, validated and scored by DeepECG_Docker's vendored `models/dinov3_bank.py`
  (a46499c5), in its head format, model revision `7d35c2e` (same weights); probes/SHA256SUMS verified at
  download; image preprocessing with the bank's page fixes. Outputs identical to 1.2.1.
- 1.2.1: `residentMemoryMiB` 1400 -> 1600 (measured 1,510 MiB on 1.1.0). Same weights (model revision `badd718`).
- 1.2.0: probabilities stored and compared at 6 decimals instead of 4. At 4 decimals the HCM flag
  (cutoff 0.0027415) was effectively `p >= 0.00275`, and the report showed HCM probabilities and cutoff
  with too few decimals to tell them apart; outputs with a cutoff below 0.01 are now shown with all stored
  decimals. Same weights and heads; flags can change only for HCM probabilities in [0.0027415, 0.00275)
  or other outputs within 5e-5 of their cutoff. Minor bump (prediction output changes, `docs/model-image-release.md`).
- 1.1.0: six frozen linear-probe heads (77 patterns, SHD, LVEF<=40, LVEF<50, AFib 5 y, HCM) at
  model revision `badd718`; image inputs decoded with the shared `utils/dicom_pixels.py` helper.
- 1.0.0: first release: `aco` and `acco_vs_aico` fine-tunes, waveform / PDF / image inputs, probe
  heads in the DeepECG_Docker head format.
