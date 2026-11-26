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

Not in this release: the fitted probes on the zo59vdxk encoder were not found on the reachable disks
(`/mnt/data1`, `/mnt/data2`; they were written to `/volume/.../zo59vdxk_*/probes*/heads/*.joblib` on the
training machine by `scripts/probe_report.py fit`). They are being exported to
`/mnt/data1/models/DeepECG-PNG/acs_ft20_dinov3_siglip2_epoch030/probes/`. Adding them needs no code change:

1. Convert each joblib with `convert_probe.py` (StandardScaler + LogisticRegression to `head.json` +
   `classifier.safetensors`), with the validation Youden threshold of each output and its provenance.
2. Write `probes/SHA256SUMS`, upload `probes/` to the Hugging Face repository next to `aco/`.
3. Bump `modelRevision` in `data/model_info.json` (and `PACS_AI_MODEL_REVISION` in `Dockerfile.local`) and
   the minor version.

The build then also downloads `base/epoch_030.pt` (the frozen pretraining encoder, pinned in
`models/config.json`); the service runs its top 2 blocks on the shared trunk output and feeds the pooled
1536-d features to every probe. The manifest format is documented in `models/probes.py`.

## Inputs

PACS.AI sends the DICOM objects of the study. The service scores one ECG per request, picking the best
readable object: a waveform first, then a PDF, then an image; within a kind, the earliest acquisition.

| input | DICOM | how the page is made | fidelity |
|---|---|---|---|
| waveform | 12-lead / general ECG waveform (`1.2.840.10008.5.1.4.1.1.9.1.1`, `.9.1.2`), modality ECG | leads I, II, V1-V6 of the rhythm group to MUSE counts (4.88 uV), 250 Hz as in training (500 Hz: every other sample), III/aVR/aVL/aVF derived from I and II, drawn by the training renderer (`utils/ecg_page_renderer.py`) | Same pixels as the training pages (checked on real test ECGs, below). Rates that are not a multiple of 250 Hz are resampled. At least 10 s needed. |
| pdf | Encapsulated PDF (`1.2.840.10008.5.1.4.1.1.104.1`), modality DOC | vector traces read from the PDF (`utils/ecg_pdf.py`, standard 3x4 + lead II rhythm layout only; scale and lead labels checked), redrawn by the training renderer | Close, not identical: the traces are interpolated from the drawing. Scanned PDFs and other layouts are refused. |
| image | any other single-frame image (secondary capture), modality OT | the pixels as they are | Least faithful: not the training layout. The model saw such pages only through training augmentation (other layouts, photographs). |

The renderer and the PDF reader are copied from HeartWise-AI/DeepECG_Docker (`utils/ecg_page_renderer.py`,
`utils/ecg_pdf.py`, main 8f42a18). Preprocessing is DeepECG-PNG `utils/image.process_image`: grayscale,
stretch resize to 448x896 (antialiased), 3 channels, ImageNet mean/std.

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
{"id": "aco", "label": "Acute coronary occlusion (ACO)", "description": "...", "task": "binary",
 "kind": "fine-tuned", "group": "acs", "probability": 0.4213, "threshold": 0.05062838,
 "positive": true, "applicable": true, "applicableWhen": null}
```

`positive` is null when the head is not applicable (`acco_vs_aico` with a negative `aco`). Multiclass
probes flag their most probable class. `predictions.input` names the object that was read. Probabilities
are rounded to 4 decimals before anything reads them. Example: `docs/examples/predict_response_json.json`.

Select heads with `additionalMetadata: {"heads": ["aco", "<probe id>", ...]}`; a probe id selects all its
rows, and a head with `applicableWhen` brings its parent. Without it, every head is returned.

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
`5002fe0589debf8e8be7b43e79cafa5eaba49d9c` (tag `v1.0.0`), uploaded from the NAS release folder
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
  training converter, the renderer's golden pixels, PDF and image inputs, the shared trunk against the
  training classifier, probe loading, predict and render-report.
- Against the released checkpoints (CPU, 8 MHI ACS test ECGs, not committed): the service's outputs on
  the stored training pages equal the training-code predictions (max difference 6e-5 ACO, 2e-5 ACCO);
  preprocessing is identical (max difference 0.0). Waveform DICOMs built from the MUSE XML of the 6
  MUSE-era ECGs rendered 100% of the training pixels and gave the same outputs (max difference 1e-6).
- Resources (`data/model_info.json`) are estimates (about 100 M parameters in fp32); measure with
  `scripts/benchmark-model-resources.py` on the built image.

## Changelog

- 1.0.0: first release: `aco` and `acco_vs_aico` fine-tunes, waveform / PDF / image inputs, probe
  extension point.
