---
name: pacs-ai-model-mapping
description: Map a trained model (esp. a DeepCORO/CLIP-family video model) onto the PACS-AI backend model-examples architecture — the files to author, the config/class_mapping/model_info schemas, the VideoMILWrapper padding-mask contract, the HuggingFace checkpoint wiring, deployment, and how to reproduce a prediction locally. Use when adding/porting/debugging a PACS-AI inference model, when a model asks for the wrong Step-2 variables page, or when a deployed prediction needs to be reproduced.
---

# PACS-AI model mapping

Each PACS-AI model is a **self-contained FastAPI container** under `model-examples/<ModelName>/`.
The backend (`api-pacs`, Go) ingests DICOMs, calls the model's `/predict`, and renders the result.
You author a small, fixed set of files; shared plumbing lives in `utils/genericLogic.py`
(`BasePredictionService`).

## Directory layout (copy an existing CLIP model, e.g. `CathEF-CLIP`, as the template)
```
model-examples/<ModelName>/
  logic.py            # CustomPredictionService(BasePredictionService) — YOU write (model-specific)
  main.py             # FastAPI entrypoint — copy verbatim
  config.json         # {"modelDirectory":"models","models":{}} — copy verbatim
  requirements.txt    # torch, torchvision, pydicom, opencv, fastapi, huggingface_hub…
  Dockerfile          # downloads weights from HF at build via hf_token secret
  download_model.py   # snapshot_download(repo_id=…) — YOU set this model's HF repo_id
  data/
    model_info.json   # PACS-AI UI/ingestion contract  ← drives upload rules + Step-2 page
    model_facts.json  # {"en": "...model card text..."}
  models/
    config.json       # architecture + checkpoint filename + normalization  ← YOU write
    class_mapping.json# head → {head_dim, task, name, threshold?, min?, max?}  ← YOU write
    video_encoder.py, multi_instance_linear_probing.py, attention_pool.py,
    rope_3d.py, video_aggregator.py   # architecture classes — copy from a CLIP model
    <checkpoint>.pt   # downloaded from HF at build (NOT committed)
  utils/genericLogic.py, http_utils.py  # copy verbatim (html_parser.py only if that model uses it)
```

## The files you actually edit

### 1. `models/config.json` — architecture + checkpoint + normalization
```json
{
  "VideoEncoder": { "backbone":"mvit", "input_channels":3, "num_frames":16,
    "pretrained":false, "output_dim":512, "freeze_ratio":<f>, "dropout":<f>,
    "num_heads":16, "aggregator_depth":1, "aggregate_videos_tokens":false, "per_video_pool":false },
  "MultiInstanceLinearProbing": { "embedding_dim":512, "pooling_mode":"attention+cls_token",
    "attention_hidden":128, "dropout":<f>, "use_cls_token":true, "num_attention_heads":8,
    "separate_video_attention":true, "normalization_strategy":"post_norm" },
  "VideoMILWrapper": { "num_videos":<N>, "stride":2, "resize":224 },
  "ModelStateDict": { "model_path":"best_model_epoch_<K>.pt" },
  "dataset_mean": [m,m,m], "dataset_std": [s,s,s]
}
```
- **`num_videos`** = clips pooled per study (CathEF-CLIP=4, DeepRV-CLIP=6, DeepCoro_CLIP varies). Studies are padded up to it with ZERO videos (see mask contract).
- **`dataset_mean`/`dataset_std`** are per-model and MUST match training (CathEF≈96.6/44.8, DeepRV≈134.0/27.3, DeepCoro stenosis≈122.1/28.8). Wrong stats → garbage.
- `model_path` must equal the checkpoint filename inside the HF repo.

### 2. `models/class_mapping.json` — the output heads
```json
{
  "Value":       { "head_dim":1, "task":"regression",            "name":"LVEF", "unit":"%", "min":0, "max":100 },
  "y_true_cat":  { "head_dim":1, "task":"binary_classification", "name":"Reduced EF (<40%)", "threshold":0.5 }
}
```
- `head_structure = {head: head_dim}` is passed to `MultiInstanceLinearProbing`. Head NAMES must match the checkpoint keys `mil_model.module.heads.<name>.{weight,bias}`.
- `task` ∈ {`regression`, `binary_classification`}. Regression → clamp using mapping `min`/`max` (or model defaults); binary → sigmoid (in `_postprocess`).
- A model can have multiple heads (regression + classification, as CathEF does).

### 3. `data/model_info.json` — PACS-AI contract  ⚠️ **controls the Step-2 variables page**
Key fields: `modelId`, `modelName`, `modality` ("Angiogram"), `dicomUploadMin`/`dicomUploadMax`,
`supportedOutputModes` (["HTML","JSON"]), feedback questionnaires, and:

- **Upload bounds:** existing CLIP models use **`dicomUploadMin: 1`** and **`dicomUploadMax: num_videos`**.
  Short studies are zero-padded in `logic.py`. Do **not** set min = `num_videos` unless the model truly
  requires exactly N uploads (that would reject valid shorter studies).
- **`supportedAdditionalMetadata`** — list of per-DICOM clinical variables the UI collects on a
  **second (Step-2) page** before inference. `["main_structure","status"]` → the user is asked to
  tag each video; **`[]` → NO Step-2 page** (inference runs on the raw uploaded videos).
  > This is the #1 source of "why does model X ask for variables and Y doesn't": it's this field.
  > If two models differ in the live UI but the repo shows both `[]`, the difference is **deployment
  > skew** — the container with the page is an OLD image built before the field was emptied. Fix by
  > **rebuilding + redeploying** that model's image from current `master`. (This is exactly what
  > happened with DeepRV-CLIP vs CathEF-CLIP after PR #242.)
- **Registered `outputMode`** (the mode api-pacs actually calls: JSON vs HTML) is set at model
  registration time, not by `supportedOutputModes` alone. See Deploy below.

### 4. `logic.py` — required model-specific code
Subclass `BasePredictionService`. This is **not** template plumbing — each model’s heads, report text,
filters, and video selection live here. At minimum implement/adapt:
- `load_model`, `_run_inference` (incl. padding `video_mask`), `_postprocess`
- `_handle_json_output` / `_handle_html_output` (and any clinical formatting)
- **SeriesTime ordering inside the container:** sort DICOMs by SeriesTime, then take the first
  `dicomUploadMax`. The Go backend sorts request UIDs by **numeric UID suffix**, not SeriesTime —
  relying on backend order alone will truncate the wrong videos.
- Optional `_filter_dicoms_with_metadata` when Step-2 metadata is used (CathEF keeps Left-Coronary
  diagnostic; DeepRV keeps L+R). Only runs when `additionalMetadata` is supplied.

### 5. `download_model.py` — required model-specific edit
Hard-code this model’s gated HF `repo_id` (e.g. `heartwise/CathEF_CLIP`, `heartwise/DeepRV_CLIP`,
`heartwise/DeepCORO_CTO`). Leaving a copied template’s `repo_id` downloads the wrong checkpoint.

## The VideoMILWrapper padding-mask contract  ⚠️ (critical correctness bug)
CLIP models pad each study up to `num_videos` with **zero videos**. The MIL attention+CLS pooling must
**exclude those padded slots**, or their constant encoder embedding dominates pooling and collapses the
head (EF saturates ~100%, RV over/under-calls). Every model's `logic.py` MUST:

1. In `_run_inference`: build a real-video boolean mask and pass it in:
   ```python
   video_mask = torch.zeros((1, max_videos), dtype=torch.bool, device=device)
   video_mask[:, :num_real_videos] = True
   outputs = model(video_batch, video_mask=video_mask)   # video_batch [1, num_videos, T, H, W, C]
   ```
2. In `VideoMILWrapper.forward`: apply it only when it lines up with the instance count `N`, else fall
   back to all-valid (encoder modes that collapse `N` don't crash):
   ```python
   attention_mask = None
   if video_mask is not None and video_mask.shape[-1] == N:
       attention_mask = video_mask.to(embeddings.device, torch.bool)
   return self.mil_model(embeddings, mask=attention_mask)
   ```
This was PR #242; apply it to any new CLIP model. Verify: holding the real video fixed and changing the
zero-padding content must shift the logit by **exactly 0.0**.

## `logic.py` contract (subclass `BasePredictionService`)
- `load_model(config)`: build `VideoEncoder(**cfg["VideoEncoder"])`, `MultiInstanceLinearProbing(**cfg["MultiInstanceLinearProbing"], head_structure=…)`, wrap in `VideoMILWrapper`, load
  `torch.load(...)["linear_probing"]` with keys `.replace("module.","")`, `.eval()`, move to CUDA.
- Preprocessing (per clip, ported from DeepCoro_CLIP/generic):
  read frames with `stride`, BGR→RGB, `float32`, `[T,C,H,W]`; if `T<num_frames` repeat last, if `T>num_frames`
  `linspace` sample; `v2.Resize((resize,resize))`, `v2.Normalize(mean,std)`, permute to `[T,H,W,C]`. Stack
  clips → pad to `num_videos` with zeros → `unsqueeze(0)`.
- `_postprocess`: per head, `sigmoid` if binary else clamp using the head’s `min`/`max` from
  `class_mapping.json` (CathEF LVEF uses `[0,100]`; other models may differ, e.g. imaging J-CTO `[0,4]`).
- Implement `_handle_json_output` and `_handle_html_output`; optional `_filter_dicoms_with_metadata`
  (only fires when metadata is supplied).
- Sort by **SeriesTime in `logic.py`**, then take the first `dicomUploadMax` (do not assume the backend
  already ordered by SeriesTime).

## HuggingFace checkpoint wiring
- Weights live in a **gated** HF repo (e.g. `heartwise/CathEF_CLIP`, `heartwise/DeepRV_CLIP`). Repo holds
  `best_model_epoch_<K>.pt`, `config.json`, `training_config.yaml`.
- `download_model.py`: `snapshot_download(repo_id="<this-model-repo>", token=<hf_token>)`.
- Dockerfile: `RUN --mount=type=secret,id=hf_token python download_model.py --token $(cat /run/secrets/hf_token)`.
  Build: `docker build --secret id=hf_token,src=./hf_token.txt -t heartwisehub/<model>:<v> .`
- Gated repos 401 for anonymous API/search — they will NOT show in a public HF org listing.

## Reproduce a deployed prediction locally (verification recipe)
1. `snapshot_download("<this-model-repo>", token=HF_TOKEN, local_dir=ckpt/)`.
2. Copy `models/*.py` + `config.json` + `class_mapping.json` from `model-examples/<ModelName>/models/`.
3. Rebuild `VideoEncoder`+`MultiInstanceLinearProbing`+`VideoMILWrapper`, load `["linear_probing"]` (strip `module.`) — expect **0 missing / 0 unexpected** keys.
4. Select videos the same way production does:
   - Sort by **SeriesTime in `logic.py`**, take the first `dicomUploadMax`.
   - If the deployed request included `additionalMetadata`, apply the same metadata filter before
     truncating; if metadata is empty/`[]`, use the uploaded set unfiltered.
5. Preprocess exactly as `logic.py`; build the `video_mask`.
6. Compare to the deployed number. Residual of ~0.01 is the DICOM→AVI vs pre-converted-mp4 decode path;
   a large gap means wrong video selection or wrong mean/std. (Verified: CathEF-CLIP reproduced 28.5% /
   P=0.96 exactly; DeepRV-CLIP reproduced P≈0.112 vs deployed 0.121.)

## Gotchas checklist
- [ ] `logic.py` and `download_model.py` edited for this model (not a stale template copy).
- [ ] `supportedAdditionalMetadata` set intentionally (`[]` = no Step-2 page); **redeploy** after changing.
- [ ] Padding mask threaded through `VideoMILWrapper` (padding-invariance test = 0.0).
- [ ] `dataset_mean`/`dataset_std` match training (per model).
- [ ] `dicomUploadMax` == `num_videos`; `dicomUploadMin` is usually `1`; head names match checkpoint.
- [ ] SeriesTime sort happens in `logic.py`.
- [ ] `model_path` matches the `.pt` in the HF repo; weights gated behind `hf_token`.
- [ ] Deployed image rebuilt from current `master` (deployment skew hides config fixes).
- [ ] Image built with the `hf_token` secret, pushed to `heartwisehub`, deployment repointed to the new tag.
- [ ] Registered api-pacs `outputMode` is the mode you want (JSON vs HTML); redeploy keeps the old value.

## Build & publish the image (Docker Hub)
```bash
# 1. build — pulls the gated HF weights via the hf_token build secret
docker build --secret id=hf_token,src=./hf_token.txt -t heartwisehub/<model>:<version> .

# 2. authenticate to Docker Hub
docker login

# 3. push to the heartwisehub organization
docker push heartwisehub/<model>:<version>
```
Preferred one-shot path (build + push + register):
```bash
DEFAULT_OUTPUT_MODE=HTML ./scripts/deploy-model.sh model-examples/<ModelName> --hf-token-file hf_token.txt
```
Notes:
- `DEFAULT_OUTPUT_MODE` applies only when the model is **new**. Redeploying an existing model keeps its
  current registered `outputMode`; change it via admin UI / `PUT /v1/inference/model/{id}/update` if needed.
- Model images are published under the **`heartwisehub`** Docker Hub org. Pushing requires **membership in
  that org**. If you see:
  ```
  denied: requested access to the resource is denied
  ```
  you are either not logged in (`docker login`) or your Docker Hub account is not a member of `heartwisehub`.
  **Fix:** `docker login`, then have a `heartwisehub` org owner invite your Docker Hub account
  (Docker Hub → Organizations → heartwisehub → Members → *Invite member*). Org invitation is a Docker Hub
  admin action — it cannot be done from this repo or by an AI agent.
- After pushing a new tag you MUST **repoint the deployment to it and redeploy** the model container — a
  running container keeps its old image (and its baked `model_info.json`), which is exactly what causes the
  stale Step-2 variables page described above.
