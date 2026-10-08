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

The operating threshold for SYNTAX >=23 on the continuous head is **16.98** (`16.979706`),
selected once on the merged validation partition and held fixed for both held-out cohorts — it
is not tuned on test data. It lives in `models/class_mapping.json`.

## Performance

| | CardioSYNTAX (n=369) | MHI held-out (n=127) |
|---|---|---|
| AUROC (>=23) | 0.97 (95% CI 0.95-0.99) | 0.89 (95% CI 0.79-0.97) |
| Spearman rho | 0.76 (0.71-0.81) | 0.81 (0.71-0.88) |
| ICC(2,1) | 0.86 (0.82-0.89) | 0.79 (0.71-0.86) |
| MAE (points) | 3.21 (2.77-3.64) | 4.47 (3.67-5.30) |
| Bias (points) | -0.35 (-0.87-0.17) | -0.57 (-1.68-0.58) |

This remains a rule-out aid, not a confirmatory test. Across both held-out cohorts, v6 reduced
high-complexity underestimation relative to v5 but retained a -10.2-point bias in the reference
score >=33 group. Prospective and fully external validation remain outstanding.

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
python download_model.py --token-file /path/to/hf_token.txt
```

The service pins Hugging Face revision
`1ffb38cfc10fa10c4b60f44746063feb860eeba0` and downloads only
`v6_20260929-203527/models/best_model_epoch_19.pt`. The expected checkpoint SHA-256 is
`856f5d6523c25a45c62886bf6cd1d351297821f60c3465a20b962aa46fa08ec0`; the build fails if
the downloaded bytes do not match it. Older v5 and `rxt2fz27` artifacts remain in the repository
for provenance but are not downloaded into this image.

## Training provenance

The v6 candidate is run `20260929-203527`, best epoch 19 of 30. It uses the same patient-level
split, modified 16-segment reference standard, architecture, preprocessing, and normalization as
v5, with batch size one and two gradient-accumulation steps. Its pinned training configuration is
[`v6_20260929-203527/config.yaml`](https://huggingface.co/heartwise/deepcoro_clip_cardiosyntax/blob/1ffb38cfc10fa10c4b60f44746063feb860eeba0/v6_20260929-203527/config.yaml).

- `dataset_mean`: `[110.4954833984375, 110.4954833984375, 110.4954833984375]`
- `dataset_std`: `[37.805782318115234, 37.805782318115234, 37.805782318115234]`

These values replace the copied CathEF statistics. The v6 configuration specifies ten videos,
16 frames, stride two, resize 224, and all six head dimensions and class orders used here. The
validation operating threshold is `16.979706`. Hugging Face's held-out comparison reports
that v6 uses the same keys and tensor shapes as v5 and loads in this service unchanged. It also
records only one training seed; v6 remains a release candidate until the deployment acceptance
work in issue #357 is complete.

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

- `MultiInstanceLinearProbing` loads the v6 checkpoint **strictly** — 31 tensors, no missing or
  unexpected keys — and a forward pass returns all six heads with the expected shapes
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

### Candidate release process

Build and publish `heartwisehub/pacs-ai-deepcoro-syntax:6.0.0` as a candidate without replacing
the active v5 deployment. Record the pushed Docker digest, run a representative DICOM smoke test,
and reconcile its prediction with an independently reproduced v6 result. The first Web/staging
v5-to-v6 transition is reserved for the managed-upgrade acceptance test; ICM follows only after
that drain, validation, history-preservation, and rollback workflow succeeds.
