# DeepCORO-SYNTAX

Automated estimation of a modified SYNTAX score from a complete multi-view coronary angiography
study. Fine-tuned from DeepCORO-CLIP; same architecture family as `CathEF-CLIP`, which this
service is derived from.

> **Under peer review — not for clinical use.** EuroIntervention EIJ-D-26-00383. Weights live in
> a **private** HF repo and are released publicly on acceptance.

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

`download_model.py` pulls `heartwise/deepcoro_clip_cardiosyntax_v5`. The repo is **private**, so
`HF_API_KEY` must have read access:

```bash
python download_model.py --token "$HF_API_KEY"
```

## Status of this service

The metadata, weights wiring and model configuration are complete and derived from the training
config that produced the reported numbers:

- `models/config.json` — encoder, MIL and head structure read from the training config
  (`num_videos` 10, `freeze_ratio` 0.918, six heads)
- `models/class_mapping.json` — severity bands and the validation-selected threshold
- `data/model_info.json`, `data/model_facts.json` — registry entry and model-facts card
- `download_model.py` — points at the v5 weights

**`logic.py` is still CathEF-CLIP's and must be adapted before this is deployed.** It is carried
over because the architecture is the same family, but its pre/post-processing and presentation
are written for a single LVEF regression plus a binary head. For SYNTAX it needs:

1. `_postprocess` to return all six heads rather than `Value` / `y_true_cat`.
2. Study-level input assembly for up to **10** projections, not 4.
3. HTML/JSON presentation for a continuous score plus a severity band, with the >=23 threshold.

Until that is done and checked against `05_data/frozen_predictions_v5_epoch12.csv` in the
submission bundle, this directory registers the model but does not serve correct predictions.
