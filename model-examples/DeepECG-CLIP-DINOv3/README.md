# DeepECG-CLIP-DINOv3

Research-only PACS.AI inference service for the DINOv3-SigLIP2 ECG image encoder of DeepECG-PNG (W&B
`mhi_ai/deepecg_dinov3_siglip2` run zo59vdxk, epoch 30) and its frozen linear-probe heads. One ECG goes in; every
enabled head comes out in one result, from one encoder pass.

> **Not for clinical use.** Each output is a model probability; a flag is given only where a validation threshold
> exists. No calibration analysis, no external validation, no regulatory clearance. The encoder derives from Meta
> DINOv3 and is subject to the DINOv3 license.

## Heads

Probe version **clean** (heads fitted on features of clean rendered pages; PACS-AI receives native digital ECGs),
set in `models/config.json` (`variant`: `clean` or `aug`).

| id | output | threshold (validation Youden) | held-out MHI test AUROC [95% CI] (clean pages) |
|---|---|---|---|
| `aco` | acute coronary occlusion | 0.0470 | 0.87 [0.84, 0.89] (9,021 ECGs, 589 occlusions; sens 0.73, spec 0.85) |
| `lvef_le40` | LVEF <= 40% | 0.1695 | 0.89 [0.88, 0.90] |
| `lvef_lt50` | LVEF < 50% | 0.2696 | 0.87 [0.86, 0.88] |
| `shd` | structural heart disease (EchoNext definition) | 0.5374 | 0.77 [0.76, 0.78] |
| `hcm` | confirmed HCM vs rest | 0.0027 | 0.71 [0.66, 0.75] |
| `afib_5y` | incident AFib at 5 years | 0.1113 | 0.68 [0.65, 0.70] |
| `ecg77:<label>` | 77 ECG findings | none (probabilities only) | 0.97 macro |

Disabled by decision (2026-11-26, `disabled_heads`): `hcm_3class` (softmax, probabilities only) and `acco_vs_aico`
(test AUROC 0.59-0.61, only meaningful for occlusion ECGs). There is no amyloidosis head. The two full fine-tunes of
the first version of this PR (ACO AUROC 0.92, ACCO vs AICO 0.69; `heartwise/deepecg_clip_dinov3_acs`) are not served.

## Reuse of DeepECG_Docker

The model code is DeepECG_Docker's, vendored unchanged and pinned by hash (`tests/test_vendored.py`), from
HeartWise-AI/DeepECG_Docker PR #108 (branch `feat/dinov3-siglip2-bank`, `482d6c9`; re-pin to `main` once merged):

- `models/dinov3_bank.py`: the encoder (architecture built locally; the gated Meta repository is never contacted;
  `base/epoch_030.pt` checked by SHA-256 and loaded strictly), bfloat16 features, the head loader with every check of
  the DeepECG_Docker heads (encoder file and SHA-256, shapes, dtype, outputs, thresholds), and
  `DeepECG_DINOv3_Predictor`, whose rows this service returns;
- `utils/image_preprocessing.py` (`preprocess_ecg_image`, PDF page checks), `utils/ecg_pdf.py`,
  `utils/ecg_page_renderer.py`, `utils/log_config.py`.

This service adds only the PACS-AI parts: the DICOM inputs (`utils/ecg_inputs.py`), the head selection, the JSON and
HTML reports, render-report, and the provenance contract.

## Inputs

PACS.AI sends the DICOM objects of the study. The service scores one ECG per request, picking the best
readable object: a waveform first, then a PDF, then an image; within a kind, the earliest acquisition.

| input | DICOM | how the page is made | fidelity |
|---|---|---|---|
| waveform | 12-lead / general ECG waveform (`1.2.840.10008.5.1.4.1.1.9.1.1`, `.9.1.2`), modality ECG | leads I, II, V1-V6 of the rhythm group to MUSE counts (4.88 uV), 250 Hz as in training (500 Hz: every other sample), III/aVR/aVL/aVF derived from I and II, drawn by the training renderer (`utils/ecg_page_renderer.py`) | Same pixels as the training pages (checked on real test ECGs, below). Rates that are not a multiple of 250 Hz are resampled. At least 10 s needed. |
| pdf | Encapsulated PDF (`1.2.840.10008.5.1.4.1.1.104.1`), modality DOC | vector traces read from the PDF (`utils/ecg_pdf.py`, standard 3x4 + lead II rhythm layout only; scale and lead labels checked), redrawn by the training renderer | Close, not identical: the traces are interpolated from the drawing. Scanned PDFs and other layouts are refused. |
| image | any other single-frame image (secondary capture), modality OT | the pixels as they are | Least faithful: not the training layout. The model saw such pages only through training augmentation (other layouts, photographs). |

The renderer, the PDF reader and the preprocessing are DeepECG_Docker's (see Reuse). Preprocessing equals
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

`predictions.heads` has one row per output, the fields of DeepECG_Docker's `DeepECG_DINOv3_Predictor`:

```json
{"id": "aco", "label": "ACO", "probability": 0.4213, "threshold": 0.046955234134749495, "positive": true, "applicable": true}
{"id": "ecg77:Afib", "label": "Afib", "probability": 0.0103, "threshold": null, "positive": null, "applicable": true}
```

Probabilities are rounded to 4 decimals before the flags are computed, so a stored result holds everything its
report needs. `predictions.input` names the DICOM object that was read; `predictions.model` gives the encoder and
probe version. Example: `docs/examples/predict_response_json.json`.

Select outputs with `additionalMetadata: {"heads": ["aco", "ecg77", "ecg77:Afib", ...]}` (a head name selects all its
outputs); a disabled head is refused. Without it, every enabled output is returned.

### Calling it

Through api-pacs (permission `models.use`; the container must be registered):

```
POST /v1/inference/model/proxy/container/{containerID}/predict
{"studyInstanceUID": "...", "seriesInstanceUIDs": ["..."], "forceJSON": true,
 "additionalMetadata": {"heads": ["aco", "lvef_le40"]}}
```

Directly against the container:

```
POST /api/inference/predict
{"outputMode": "JSON", "seriesInstanceImages": {"1": {"1": "<base64 DICOM>"}}}
```

`GET /api/inference/model-info` and `/api/inference/model-facts` describe the model; `POST /api/inference/render-report`
with `{"result": <data of a JSON-mode response>}` returns the HTML of a stored result without loading the model.

## Weights

Hugging Face `heartwise/DeepECG-PNG_DINOv3-SigLIP2_epoch030` (private), revision
`3eab18b498f1b05f7749052fd40d00980b4c16d1`, mirrored on the NAS at
`/mnt/data1/models/DeepECG-PNG/acs_ft20_dinov3_siglip2_epoch030/{base,probes}`:

- `base/epoch_030.pt` (manifest checkpoint), SHA-256 `335aaec39064d009c31710a96fe17d98450d85611c5bb0271ce3aad118149ae6`;
- `probes/**`: every file checked against `probes/SHA256SUMS` at build.

## Build

```bash
python3 scripts/release-model.py model-examples/DeepECG-CLIP-DINOv3 \
  --image-repository heartwisehub/pacs-ai-deepecg-clip-dinov3 \
  --hf-token-file /secure/path/hf_token
```

The token needs read access to `heartwise/DeepECG-PNG_DINOv3-SigLIP2_epoch030`; it is a BuildKit secret, never in
the image. `Dockerfile.local` builds from the NAS copy (instructions at its top), for development only.

## Verification

- `pytest model-examples/DeepECG-CLIP-DINOv3/tests` (CPU, no weights): DICOM inputs against the training converter
  and renderer, the vendored files, head selection and disabled heads, rounding and flags, predict and render-report,
  provenance and download verification.
- The model code is tested in DeepECG_Docker (PR #108): all 18 published heads load against the published encoder;
  on 8 MHI test pages preprocessing equals training exactly, fp32 features match the training class (min cosine
  0.9999997); GPU (RTX A5000): 557 MiB peak, about 110 ms per ECG for every head. Feature -> score parity against
  the saved fit scores runs on the training host (`scripts/dinov3_probe_parity.py` there).
- `resources` in `data/model_info.json` are estimates from that measurement plus the CUDA context; confirm with
  `scripts/benchmark-model-resources.py` on the built image.

## Changelog

- 1.1.0: serves the published frozen-probe heads of the DINOv3-SigLIP2 encoder
  (`heartwise/DeepECG-PNG_DINOv3-SigLIP2_epoch030` @ 3eab18b, clean) through DeepECG_Docker's bank; heads per the
  decision of 2026-11-26. Supersedes the unpublished 1.0.0 (ACS fine-tunes) and the interim 1.1.0 of
  `heartwise/deepecg_clip_dinov3_acs` revision badd718.
- 1.0.0 (never published): ACS fine-tunes (aco, acco_vs_aico), waveform / PDF / image inputs.
