# AGENTS.md — pacs-ai-backend

Guidance for AI coding agents (Claude Code, Codex, Cursor, etc.) working in this repo.

## Repo shape
- `api-pacs/` — Go backend (DICOM ingestion, model orchestration, REST).
- `model-examples/<ModelName>/` — self-contained FastAPI inference containers, one per model.

## Adding / porting / debugging an inference model
Follow **`model-examples/README.md`** (human guide) and the machine-readable skill at
**`.claude/skills/pacs-ai-model-mapping/SKILL.md`** (Claude Code auto-loads it; other agents should
read it directly). It documents the full model→PACS-AI mapping. The essentials:

1. **Author only 3 files** per model: `models/config.json` (architecture + checkpoint + `dataset_mean/std`),
   `models/class_mapping.json` (heads → `head_dim`/`task`/`threshold`), and `data/model_info.json`
   (UI/ingestion contract). Copy `main.py`, `utils/`, and `models/*.py` from an existing CLIP model.
2. **Padding-mask contract (critical, PR #242):** CLIP models pad each study to `num_videos` with ZERO
   videos. `VideoMILWrapper.forward` MUST receive a real-video boolean `video_mask` and exclude padded
   slots, or the constant zero-embedding collapses attention/CLS pooling. Padding-invariance test: holding
   the real video fixed and changing the padding content must move the logit by exactly 0.0.
3. **`data/model_info.json → supportedAdditionalMetadata`** drives the PACS-AI **Step-2 variables page**:
   a non-empty list (e.g. `["main_structure","status"]`) asks the user to tag each DICOM; `[]` = no page.
   After changing it you MUST **rebuild + redeploy** the model image — a live container built before the
   change keeps the old behaviour (this is why DeepRV-CLIP still showed the page after PR #242 while
   CathEF-CLIP did not).
4. **Weights** live in a **gated** HF repo `heartwise/<ModelName>`, downloaded at build via an
   `hf_token` Docker secret (`download_model.py`). Gated repos 401 anonymously and won't appear in HF
   org listings.
5. **Per-model normalization** (`dataset_mean`/`dataset_std`) and `num_videos` must match training; do not
   copy another model's values.

## Verify a prediction locally
Reproduce a deployed number with the recipe in the skill: `snapshot_download` the gated checkpoint, rebuild
`VideoEncoder`+`MultiInstanceLinearProbing`+`VideoMILWrapper` (expect 0 missing/0 unexpected keys), select
the **first `dicomUploadMax` videos by SeriesTime** (not diagnostic-filtered), preprocess and mask exactly
as `logic.py` does. A ~0.01 residual is the DICOM-vs-mp4 decode path; a large gap means wrong video
selection or wrong mean/std.

## Conventions
- Don't commit model weights (they download from HF at build). Keep `hf_token.txt` gitignored.
- Match an existing CLIP model's file layout exactly; the backend relies on it.
