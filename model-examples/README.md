# model-examples — adding an inference model to PACS-AI

Each subdirectory is a **self-contained FastAPI container** for one model. The Go backend (`api-pacs`)
ingests DICOMs and calls the model's `/predict`. Copy an existing model (e.g. **`CathEF-CLIP`**) as your
template.

> **AI agents:** a machine-readable version of this guide lives at
> [`.claude/skills/pacs-ai-model-mapping/SKILL.md`](../.claude/skills/pacs-ai-model-mapping/SKILL.md)
> (Claude Code auto-loads it; Codex/others see [`AGENTS.md`](../AGENTS.md)).

## File layout
```
model-examples/<ModelName>/
  logic.py             # CustomPredictionService(BasePredictionService) — you write (model-specific)
  download_model.py    # HF snapshot_download — you set this model's repo_id
  main.py, config.json # entrypoint + app config — copy verbatim
  requirements.txt, Dockerfile, nginx.conf, supervisord.conf
  data/model_info.json # PACS-AI UI/ingestion contract  ← you write
  data/model_facts.json
  models/config.json           # architecture + checkpoint + normalization  ← you write
  models/class_mapping.json    # output heads                                ← you write
  models/*.py                  # video_encoder, multi_instance_linear_probing, … — copy
  models/<checkpoint>.pt       # pulled from HF at build (not committed)
  utils/*.py                   # BasePredictionService etc. — copy helpers you need
```

## The files you author

**`models/config.json`** — `VideoEncoder` (mvit, 16 frames, output 512), `MultiInstanceLinearProbing`
(attention+cls_token), `VideoMILWrapper` (`num_videos`, `stride`, `resize`), `ModelStateDict.model_path`
(the `.pt` filename inside the HF repo), and **`dataset_mean`/`dataset_std`** (per-model, must match
training — CathEF≈96.6/44.8, DeepRV≈134.0/27.3, DeepCoro stenosis≈122.1/28.8).

**`models/class_mapping.json`** — each output head → `{head_dim, task, name, threshold?, min?, max?}`,
where `task` is `regression` (clamp using mapping bounds) or `binary_classification` (sigmoid). Head names
must match the checkpoint keys `mil_model.module.heads.<name>.*`.

**`data/model_info.json`** — `modelId/modelName/modality`, upload bounds, `supportedOutputModes`, feedback
questionnaires, and **`supportedAdditionalMetadata`**.

**`logic.py`** — required. Owns head post-process, JSON/HTML reports, SeriesTime sort + truncate,
padding `video_mask`, and optional metadata filters. Do not ship a copied sibling model's `logic.py`
unchanged.

**`download_model.py`** — required. Set this model's gated HF `repo_id` (template copies still point at
another model).

### Upload bounds
Existing CLIP models use **`dicomUploadMin: 1`** and **`dicomUploadMax: num_videos`**. Short studies are
zero-padded. Only set min = `num_videos` if the model truly requires exactly N uploads.

## Two things that bite people

### 1. `supportedAdditionalMetadata` → the Step-2 "variables" page
A non-empty list (e.g. `["main_structure","status"]`) makes PACS-AI show a **second page** asking the user
to tag each DICOM before inference; **`[]` shows no page**. **After changing this field you must rebuild and
redeploy the model image** — a running container built before the change keeps the old UI. (DeepRV-CLIP kept
asking for variables after PR #242 because its deployed image predated the change, while CathEF-CLIP had
been redeployed.)

### 2. The zero-padding attention mask (PR #242 — correctness bug)
CLIP models pad each study up to `num_videos` with **zero videos**. The MIL attention+CLS pooling must
exclude the padded slots, or their constant embedding dominates pooling and collapses the head (EF saturates
~100%, RV mis-calls). `logic.py`'s `_run_inference` builds a real-video boolean `video_mask` and
`VideoMILWrapper.forward` applies it when it matches the instance count. **Test:** holding the real video
fixed and changing the padding content must move the logit by exactly **0.0**.

## Weights (HuggingFace)
Weights live in a **gated** repo (e.g. `heartwise/CathEF_CLIP`, `heartwise/DeepRV_CLIP`), downloaded at
build time. Set the correct `repo_id` in `download_model.py`:
```bash
docker build --secret id=hf_token,src=./hf_token.txt -t heartwisehub/<model>:<v> .
```
Keep `hf_token.txt` gitignored. Gated repos return 401 anonymously.

## Publish / register
Preferred:
```bash
DEFAULT_OUTPUT_MODE=HTML ./scripts/deploy-model.sh model-examples/<ModelName> --hf-token-file hf_token.txt
```
Or manually:
```bash
docker login
docker push heartwisehub/<model>:<version>
```
Images live under the **`heartwisehub`** Docker Hub org, so pushing requires **membership**. If you get
`denied: requested access to the resource is denied`, run `docker login` and have a `heartwisehub` org owner
invite your Docker Hub account (Docker Hub → Organizations → heartwisehub → Members → *Invite member*). After
pushing a new tag, **repoint the deployment to it and redeploy** — a running container keeps its old image
and `model_info.json` (the cause of stale Step-2 pages).

**`outputMode` note:** `supportedOutputModes` in `model_info.json` lists what the container can serve.
The mode api-pacs actually calls is the registered model `outputMode`. `DEFAULT_OUTPUT_MODE` in
`deploy-model.sh` applies only to **new** registrations; redeploying an existing model keeps its prior mode
(update via admin UI or `PUT /v1/inference/model/{id}/update`).

## Reproduce a deployed prediction locally
`snapshot_download` the gated checkpoint, rebuild the model from `models/config.json` +
`class_mapping.json` (expect **0 missing / 0 unexpected** keys), then select videos the same way
`logic.py` does: **sort by SeriesTime** (the Go backend sorts by UID suffix, not SeriesTime), apply any
metadata filter when `additionalMetadata` is present, take the first `dicomUploadMax`, preprocess and mask.
A ~0.01 gap is the DICOM-vs-mp4 decode path; a large gap means wrong video selection or wrong
`dataset_mean/std`.
