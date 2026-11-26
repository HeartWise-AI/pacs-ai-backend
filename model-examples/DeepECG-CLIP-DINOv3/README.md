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
| `acco_vs_aico` | fine-tuned | complete (ACCO) vs incomplete (AICO) occlusion; only interpreted when `aco` is positive | 0.6155858 | AUROC 0.69 [0.62, 0.76] among the 589 occlusions |
| probes | linear probe | any head released under `probes/` (see below) | per head | per head (`model-facts`) |

Both fine-tunes come from the DINOv3-SigLIP2 pretraining (W&B `mhi_ai` run zo59vdxk, epoch 30) with the top
2 blocks, the final norm and a linear head trained (DeepECG-PNG commit 1f6fb84, PR #224). Their first 10
blocks are bit-identical, so the service runs those once per ECG and then each head's own top blocks
(`models/dinov3_heads.py`; loading refuses checkpoints that do not share them).

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

Not served: the ACO and ACCO-vs-AICO probes (the fine-tunes above are better on the same task), the
probes fitted on augmented pages (same output ids as the clean ones), and the 3-class HCM head (softmax).
All 18 probe heads (clean and augmented, float64 copies, full provenance) are in the private Hugging Face
repository `heartwise/DeepECG-PNG_DINOv3-SigLIP2_epoch030`. Each served head reproduces its saved
validation scores from the cached features within 3e-6.

A probe head uses **the DeepECG_Docker head format, unchanged**: one folder per head with
`config.json`, `head.json`, `thresholds.json` and `classifier.safetensors`, exactly the files of a
DeepECG_Docker classifier repository such as `heartwise/SigLIP2_SHD`, validated with the same checks
as `_DeepECG_SigLIP2_Bank.load_head` (`models/probes.py`). For this model:

- `config.json` equals `models/encoder_config.json` (same schema as DeepECG_Docker's encoder config:
  `model`, `vision_config`, `preprocessing`, `feature`);
- `head.json` `encoder.sha256` is the SHA-256 of `base/epoch_030.pt`, and `classifier.input_dim` is
  1536 (concat[CLS, mean patch tokens] of the frozen encoder);
- `thresholds.json` holds the validation Youden cutoff of each output, or `{}` for a probability-only
  head (as DeepECG_Docker's 77-class head).

A `scripts/probe_report.py` probe (StandardScaler + LogisticRegression) maps field for field:
`weight = coef_`, `bias = intercept_`, `mean = mean_`, `scale = scale_`, `standardize: true`. Only sigmoid
heads exist in this format, so a 3-class HCM head would need a format change in DeepECG_Docker first.

Adding heads needs no code change: upload the head folders under `probes/` in the Hugging Face repository,
then bump `modelRevision` in `data/model_info.json` (and `PACS_AI_MODEL_REVISION` in `Dockerfile.local`)
and the minor version. The build then also downloads `base/epoch_030.pt` (pinned in `models/config.json`);
the service runs its top 2 blocks on the shared trunk output and feeds the pooled features to every head,
in the same forward pass as the fine-tunes. The same folders can be published as their own repositories
for DeepECG_Docker once it has a DINOv3 encoder.

### Reuse of DeepECG_Docker

Decision: share the formats and vendor DeepECG_Docker's ECG input code unchanged; do not depend on the
DeepECG_Docker package.

- Vendored unchanged from DeepECG_Docker `8f42a184` (hashes checked by `tests/test_vendored.py`; update by
  copying newer files): `utils/ecg_page_renderer.py` (training renderer), `utils/ecg_pdf.py` (vector PDF
  reader), `utils/image_preprocessing.py` (`preprocess_ecg_image`, `open_png_bytes`). A PDF is read exactly
  as DeepECG_Docker's `load_ecg_pdf` reads one; every page goes through `preprocess_ecg_image` with the
  `preprocessing` block of `models/encoder_config.json`.
- Same head format and probability formula as DeepECG_Docker's SigLIP2 heads (above).
- Not reused: DeepECG_Docker's model classes and pipeline. Its image models are the SigLIP2 encoder only
  (no DINOv3), its loader is tied to its CSV batch pipeline (`main.py`, `heartwise.config`, `weights/`
  download per repository), and PACS-AI needs a FastAPI service with the PACS-AI predict, model-info and
  provenance contracts. Adding the DINOv3 encoder to DeepECG_Docker, reading the same head folders, is the
  follow-up that would let both pipelines run every head.

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
applicable (`acco_vs_aico` with a negative `aco`) or has no threshold (probability-only head). `predictions.input` names the object that was read. Probabilities
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
 "additionalMetadata": {"heads": ["aco", "acco_vs_aico"]}}
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
`badd718a6e26be24c9ad2dd3c911160a7d4fbe06` (tag `v1.1.0`), uploaded from the NAS release folder
`/mnt/data1/models/DeepECG-PNG/acs_ft20_dinov3_siglip2_epoch030/` (same bytes; `SHA256SUMS` there):

| file | SHA-256 |
|---|---|
| `aco/best_model.pth` (manifest checkpoint) | `49db22c3f252d6ba13ec87562df07e4da4d68e5d1ca282dffb484b7c10459026` |
| `acco_vs_aico/best_model.pth` | `55091186aaf36d66eab1fb048a1c4177fa1534bd25cc84ac4a0e4e413f74ba29` |
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
