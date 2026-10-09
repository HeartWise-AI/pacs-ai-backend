# AGENTS.md — pacs-ai-backend

Guidance for AI coding agents (Claude Code, Codex, Cursor, etc.) working in this repo.

## Repo shape
- `api-pacs/` — Go backend (DICOM ingestion, model orchestration, REST).
- `model-examples/<ModelName>/` — self-contained FastAPI inference containers, one per model.

## Adding / porting / debugging an inference model
Follow **`model-examples/README.md`** (human guide) and the machine-readable skill at
**`.claude/skills/pacs-ai-model-mapping/SKILL.md`** (Claude Code auto-loads it; other agents should
read it directly). It documents the full model→PACS-AI mapping. The essentials:

1. **Author these model-specific files** (do not leave a copied template unchanged):
   - `models/config.json` — architecture + checkpoint + `dataset_mean/std` + `num_videos`
   - `models/class_mapping.json` — heads → `head_dim`/`task`/`threshold`/`min`/`max`
   - `data/model_info.json` — UI/ingestion contract (`dicomUploadMin`/`Max`, metadata, output modes)
   - `logic.py` — head post-process, HTML/JSON reports, SeriesTime sort, padding mask, optional metadata filters
   - `download_model.py` — hard-coded HuggingFace `repo_id` for this model
   Copy `main.py`, shared `utils/` helpers, and architecture `models/*.py` from an existing CLIP model as the starting point.
2. **Padding-mask contract (critical, PR #242):** CLIP models pad each study to `num_videos` with ZERO
   videos. `VideoMILWrapper.forward` MUST receive a real-video boolean `video_mask` and exclude padded
   slots, or the constant zero-embedding collapses attention/CLS pooling. Padding-invariance test: holding
   the real video fixed and changing the padding content must move the logit by exactly 0.0.
3. **`data/model_info.json → supportedAdditionalMetadata`** drives the PACS-AI **Step-2 variables page**:
   a non-empty list (e.g. `["main_structure","status"]`) asks the user to tag each DICOM; `[]` = no page.
   After changing it you MUST **rebuild + redeploy** the model image — a live container built before the
   change keeps the old behaviour (this is why DeepRV-CLIP still showed the page after PR #242 while
   CathEF-CLIP did not).
4. **Weights** live in a **gated** HF repo (e.g. `heartwise/CathEF_CLIP`), downloaded at build via an
   `hf_token` Docker secret. Set the correct `repo_id` in `download_model.py`. Gated repos 401 anonymously
   and won't appear in HF org listings.
5. **Per-model normalization** (`dataset_mean`/`dataset_std`) and `num_videos` must match training; do not
   copy another model's values.
6. **Upload bounds:** existing CLIP models use `dicomUploadMin: 1` and `dicomUploadMax: num_videos`
   (short studies are zero-padded). Do not set min = `num_videos` unless the model truly requires exactly N videos.
7. **Registered `outputMode`** (JSON/HTML/…) is chosen when the model is added in api-pacs / via
   `scripts/deploy-model.sh` (`DEFAULT_OUTPUT_MODE`). It is separate from `supportedOutputModes` in
   `model_info.json`. Redeploying an existing model keeps its prior `outputMode`.
8. **Provenance is optional only for legacy images:** when `data/model_info.json` contains `provenance`,
   it must be complete and immutable: repository IDs, pinned 40-character revisions, a normalized relative
   checkpoint path, and its lowercase SHA-256. Never add placeholders or partial provenance. See
   `docs/model-provenance-contract.md`.
9. **Official images are released after merge:** use `scripts/release-model.py` or the protected
   `Publish inference model image` workflow. It requires a clean commit reachable from `master`, builds only
   the semantic-version tag by default, verifies labels and live model-info, and records the registry digest.
   `Dockerfile.local` and local tags are development-only. See `docs/model-image-release.md`.

## Verify a prediction locally
Reproduce a deployed number with the recipe in the skill: `snapshot_download` the gated checkpoint, rebuild
`VideoEncoder`+`MultiInstanceLinearProbing`+`VideoMILWrapper` (expect 0 missing/0 unexpected keys), select
the **first `dicomUploadMax` videos by SeriesTime inside `logic.py`** (the Go backend sorts by UID suffix,
not SeriesTime). If the request includes `additionalMetadata`, apply the same metadata filter `logic.py`
uses before truncating. Preprocess and mask exactly as `logic.py` does. A ~0.01 residual is the
DICOM-vs-mp4 decode path; a large gap means wrong video selection or wrong mean/std.

## Conventions
- Don't commit model weights (they download from HF at build). Keep `hf_token.txt` gitignored.
- Match an existing CLIP model's file layout exactly; the backend relies on it.
- Use `scripts/release-model.py` for provenance-aware build/publish operations and
  `scripts/deploy-model.sh --register-only` only for a separately controlled registration. Default branch
  for this repo is `master`.
