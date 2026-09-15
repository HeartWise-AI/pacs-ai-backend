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

The operating threshold for SYNTAX >=23 on the continuous head is **16.903**, selected once on the
merged validation partition and held fixed for both held-out cohorts — it is not tuned on test
data. It lives in `models/class_mapping.json`.

## Performance

| | CardioSYNTAX (n=369) | MHI held-out (n=127) |
|---|---|---|
| AUROC (>=23) | 0.97 (95% CI 0.94-0.99) | 0.88 (95% CI 0.79-0.95) |
| Sensitivity / Specificity | 0.86 / 0.93 | 0.72 / 0.80 |
| NPV | 0.98 | 0.95 |
| Spearman rho / ICC(2,1) | 0.77 / 0.83 | 0.80 / 0.76 |
| MAE (points) | 3.36 | 4.98 |

High NPV at modest PPV — a rule-out step, not a confirmatory test. Against physicians on the 171
studies with three complete ratings: AI accuracy 0.74 (kappa 0.71) versus the physician consensus
0.70 (kappa 0.69). Comparable, not superior.

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

`download_model.py` pulls `heartwise/deepcoro_clip_cardiosyntax`, which is public. The token
argument is kept for interface parity with the other model services but is not required for read
access:

```bash
python download_model.py --token "$HF_API_KEY"
```

That repo holds both versions. The service uses `v5_20260822-164504/` — the sibling
`rxt2fz27_20250711-171619/` is the superseded CardioSYNTAX-only model and must not be loaded.

## Status of this service

`logic.py` has been adapted from CathEF-CLIP and the model configuration is verified against the
checkpoint:

- `MultiInstanceLinearProbing` loads the v5 checkpoint **strictly** — 31 tensors, no missing or
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
| `num_attention_heads` | 8 | **8** — the training config records 24, which cannot divide the 512-d embedding; `DeepCORO_CLIP/scripts/attention_rollout.py` rebuilds this checkpoint with 8 |

### Still to do before deployment

End-to-end serving has **not** been exercised: no DICOM study has been pushed through the running
container. What is verified is that the model builds, loads strictly and runs a forward pass.
Before this serves patients, run a study through the container and reconcile the output against
`05_data/frozen_predictions_v5_epoch12.csv` in the submission bundle, which holds the per-study
predictions for all 496 held-out studies.
