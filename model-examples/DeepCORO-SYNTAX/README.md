# DeepCORO-SYNTAX

Automated estimation of a modified SYNTAX score from a complete multi-view coronary angiography
study. Fine-tuned from DeepCORO-CLIP; same architecture family as `CathEF-CLIP`, which this
service is derived from.

> **Under peer review — not for clinical use.** EuroIntervention EIJ-D-26-00383; these results
> are not yet peer-reviewed. No regulatory clearance.

## Outputs

| Head | Type | Meaning |
|---|---|---|
| `syntax` | regression | Global modified SYNTAX score, points |
| `syntax_left` / `syntax_right` | regression | Territory scores, points |
| `syntax_category` | 4-class | zero / 1-22 / 23-32 / >=33 |
| `syntax_left_category` / `syntax_right_category` | 4- and 3-class | Territory severity |

The operating threshold for SYNTAX >=23 on the continuous head is **16.98** (16.979706), selected once on the
merged validation partition and held fixed for both held-out cohorts — it is not tuned on test
data. It lives in `models/class_mapping.json`.

## Performance

| | CardioSYNTAX (n=369) | MHI held-out (n=127) |
|---|---|---|
| AUROC (>=23) | 0.97 (95% CI 0.95-0.99) | 0.89 (95% CI 0.79-0.96) |
| Sensitivity / Specificity | 0.91 / 0.93 | 0.83 / 0.78 |
| PPV / NPV | 0.57 / 0.99 | 0.38 / 0.97 |
| Spearman rho / ICC(2,1) | 0.76 / 0.86 | 0.81 / 0.80 |
| MAE (points) | 3.33 | 4.56 |

High NPV at modest PPV — a rule-out step, not a confirmatory test. The continuous score is least
accurate at the top of the range (MAE 10.6 points for reference scores >=33 in CardioSYNTAX), so
the >=23 flag, not the point estimate, is the output intended for triage. Against physicians on the
171 studies with three complete ratings: AI accuracy 0.75 (kappa 0.77) versus the physician
consensus 0.70 (kappa 0.69). Comparable, not superior.

## What the score is, and is not

The reference standard is a **modified SYNTAX score (16-segment, segment-based)** and is not
interchangeable with a core-laboratory SYNTAX score. It covers 16 of 25 segments (the Ramus and
first obtuse marginal are one segment, not two), never records severe tortuosity, and captures
none of the five total-occlusion sub-modifiers. Every omission is one-directional: the score is
biased **downward**, most in occlusive and multivessel disease. The exact routine ships with the
weights at `scoring/score_syntax_v5.py`.

Other limits that bear on deployment: no fully external validation; proportional bias persists at
high complexity; no segment-level attribution; untested in grafted anatomy.

## Weights

`download_model.py` pulls `heartwise/deepcoro_clip_cardiosyntax`. The repository metadata is
public, but weight and configuration downloads require an approved Hugging Face account
and its access token (the repository uses manual gating):

```bash
python download_model.py --token "$HF_API_KEY"
```

That repo holds three versions. The service uses `v6_20260929-203527/` (`best_model_epoch_19.pt`,
SHA-256 `856f5d6523c25a45c62886bf6cd1d351297821f60c3465a20b962aa46fa08ec0`), the model reported in
the manuscript. The siblings `v5_20260822-164504/` (previous, epoch 12, threshold 16.903) and
`rxt2fz27_20250711-171619/` (CardioSYNTAX-only) are superseded and must not be loaded.

## Training provenance

The served model is run `20260929-203527` (`cardiosyntax_mhi_v3_R0_v2recipe`, hosted as
`v6_20260929-203527/`), epoch 19 of 30, selected on validation loss. It uses the same recipe,
data and splits as the previous run (1,859 / 340 / 496 studies), with a batch of 1 and two-step
gradient accumulation instead of a batch of 2. The architecture and normalization are unchanged
from the previous version, so `models/config.json` differs only in the checkpoint path.
Normalization is taken from the training configuration of the previous frozen submission bundle
`SUBMISSION_DeepCORO_SYNTAX_FINAL_20260825/SUBMISSION/06_provenance/training_config.yaml`
and matches the v6 run's `dataset_mean` and `dataset_std`:

- `dataset_mean`: `[110.4954833984375, 110.4954833984375, 110.4954833984375]`
- `dataset_std`: `[37.805782318115234, 37.805782318115234, 37.805782318115234]`
- Source file SHA-256: `a3b4e6db824714cd81758f782d57fc5c915e4f41dc15953e2a63cf446f1691c6`.

These values replace the copied CathEF statistics. The same source specifies ten videos,
16 frames, stride two, resize 224, and all six head dimensions and class orders used here.
`05_data/manuscript_numbers_v6.json` of the resubmission bundle identifies
`best_model_epoch_19.pt` and the validation operating threshold `16.979706`. The submission bundle contains clinical
study data and is kept in approved storage; only configuration provenance is recorded here.
The corresponding hosted configuration is
[`v6_20260929-203527/config.yaml`](https://huggingface.co/heartwise/deepcoro_clip_cardiosyntax/blob/main/v6_20260929-203527/config.yaml);
its contents require approved access and were not used for this local verification.

The YAML records `num_attention_heads: 24`, but the tracked training and inference
[constructors](https://github.com/HeartWise-AI/DeepCORO_CLIP/blob/ddefcce38c89f1e3d3c6eae2b21a66e767add4c3/projects/linear_probing_project.py#L439-L446)
do not pass that field. They use the MIL class
[default of eight heads](https://github.com/HeartWise-AI/DeepCORO_CLIP/blob/ddefcce38c89f1e3d3c6eae2b21a66e767add4c3/models/multi_instance_linear_probing.py#L104-L115).
These links pin the latest project-file revision preceding the training run. The bundle
does not record the experiment working-tree commit, so this establishes the tracked code
path, not whether the experiment had uncommitted changes. Strict checkpoint loading alone
cannot verify the attention head count because it does not change parameter shapes.

## Status of this service

`logic.py` has been adapted from CathEF-CLIP and the model configuration is verified against the
checkpoint:

- `MultiInstanceLinearProbing` loads the v6 epoch-19 checkpoint **strictly** through this service's
  own model code (all 444 state-dict tensors, no missing or unexpected keys), the Hugging Face copy
  is byte-identical to the manuscript checkpoint (SHA-256 above), and a forward pass returns all six
  heads with the expected shapes
  (`syntax` 1, `syntax_left` 1, `syntax_right` 1, `syntax_category` 4, `syntax_left_category` 4,
  `syntax_right_category` 3).
- `_postprocess` returns every head: continuous scores clamped at zero, severity heads softmaxed
  with an argmax class.
- `_filter_dicoms_with_metadata` keeps **both** coronary territories. This is the substantive
  difference from CathEF, which sees left coronary acquisitions only; dropping the right
  acquisitions would remove the evidence for the right-territory head.
- Study assembly uses `num_videos` from `models/config.json` (**10**, not CathEF's 4).
- HTML and JSON output report the continuous score, both territory scores, the severity band and
  the >=23 decision, with the rule-out framing and the modified-reference-standard caveat on the
  report itself.

Two configuration values were wrong when this was first drafted and are worth recording, because
both would have loaded silently as a differently-shaped model:

| Value | CathEF default | Correct for this checkpoint |
|---|---|---|
| `attention_hidden` | 128 | **512** |
| `num_attention_heads` | 8 | **8** — the tracked training constructor uses the class default, ignoring the YAML value of 24; see Training provenance |

### Still to do before deployment

End-to-end serving has **not** been exercised: no DICOM study has been pushed through the running
container. What is verified is that the model builds, loads strictly and runs a forward pass.
Before this serves patients, run a study through the container and reconcile the output against
`05_data/frozen_predictions_v6_epoch19.csv` in the resubmission bundle, which holds the per-study
predictions for all 496 held-out studies.
