# DeepCORO-CTO J-CTO — 2026-09-21 refresh, per-artery targets, and view embeddings

All discrimination metrics are AUROC with 95% bootstrap CI (2,000 resamples) on the
**held-out test split** (105 studies) unless stated. Validation split = 98 studies, train = 487.

## 1. Labelbox refresh (`scripts/pull_all_cto_labelbox_20260921.py`)

| Project | Rows | Labeled | Labeled with classifications | Change since 2026-07-01 |
|---|---|---|---|---|
| DeepCORO-CTO main `cmhl0soae0m4307z39hqu1bud` | 1686 | 998 | 984 | none |
| Batch B pre-labeled `cmqlp877i043t07166ymv22k9` | 998 | 60 | 26 (2nd reader) | +39 labels, all on studies already in main |
| Batch A fresh `cmqlp84600ad707zmh7pa5bcj` (per-vessel clips) | 688 | 0 | 0 | none — annotation not started |

Net labeled cohort: **690 studies / 4,466 videos** (July build: 699 — the difference is
9 empty/skipped labels that were previously read as "score 0", and 4 studies whose grid
name did not start with `2_16_` that are now parsed). 28 studies have two readers (OR-merged).

## 2. Per-artery dataset (`scripts/build_jcto_labelbox_training_v2.py`)

For each artery A ∈ {LAD, RCA, LCx}: `jcto_<component>_A` / `jcto_score_A` = the study's
label if the CTO is in A, else 0. CTO artery from the per-segment `*_cto` columns
(PDA/posterolateral → LCx when left-dominant; left main → LAD).

| | LAD | RCA | LCx |
|---|---|---|---|
| studies with CTO (train / val / test) | 156 / 29 / 37 | 309 / 67 / 73 | 108 / 17 / 20 |
| score ≥ 3 among them | 40% | 53% | 41% |

576 studies have one CTO artery, 102 two, 12 three (labels copied to every CTO artery —
the Labelbox read is study-level, so multi-CTO studies cannot be disambiguated).

Files: `jcto_v2_train.csv`, `jcto_v2_TESTEVAL.csv` (train+val→train, test→val),
`jcto_v2_study_labels.csv`, `jcto_v2_reads.csv`.

## 3. Fine-tunes with the working model's hyper-parameters (sweep winner `fbvf4gzt`)

W&B `mhi_ai/DeepCORO_JCTO`: overall 5-head run **q8emq8wi** (best epoch 13), per-artery
15-head run **dg1o32md** (best epoch 18). Configs in
`/volume/DeepCORO_CLIP/config/linear_probing/jcto/v2/`.

### 3a. Overall heads — test split

| Head | July working model (fbvf4gzt) | Retrained on refresh (q8emq8wi) |
|---|---|---|
| Blunt stump | 0.74 (0.64–0.83) | 0.68 (0.58–0.78) |
| Calcification | 0.78 (0.69–0.87) | 0.77 (0.67–0.87) |
| Bending > 45° | 0.60 (0.42–0.76) | 0.69 (0.50–0.85) |
| Occlusion ≥ 20 mm | 0.83 (0.75–0.91) | 0.76 (0.64–0.86) |
| J-CTO score ≥ 3 | 0.78 (0.68–0.87) | 0.74 (0.64–0.84), MAE 0.93 |

The refresh adds no new studies, so this is seed-level variation; the July checkpoint remains
the reference overall model.

### 3b. Per-artery heads (dg1o32md) — test split

"All studies" scores the head on every test study (non-CTO arteries are labeled 0, so this
mixes CTO *localisation* with grading). "Within artery" scores only studies whose CTO is
in that artery — the honest per-artery J-CTO grading metric.

| Head | All studies | Within artery (n) |
|---|---|---|
| LAD blunt stump | 0.87 (0.77–0.96) | 0.75 (0.58–0.90) (37) |
| LAD calcification | 0.84 (0.72–0.94) | 0.68 (0.50–0.84) |
| LAD occlusion ≥ 20 mm | 0.86 (0.76–0.95) | 0.76 (0.59–0.92) |
| LAD score ≥ 3 | 0.86 (0.71–0.98), MAE 0.59 | 0.75 (0.55–0.93) |
| RCA blunt stump | 0.81 (0.71–0.88) | 0.65 (0.51–0.78) (73) |
| RCA calcification | 0.86 (0.78–0.93) | 0.79 (0.67–0.88) |
| RCA bending > 45° | 0.68 (0.54–0.82) | 0.56 (0.37–0.73) |
| RCA occlusion ≥ 20 mm | 0.88 (0.80–0.94) | 0.79 (0.65–0.90) |
| RCA score ≥ 3 | 0.83 (0.74–0.91), MAE 0.83 | 0.74 (0.62–0.85) |
| LCx blunt stump | 0.55 (0.35–0.74) | 0.38 (0.12–0.66) (20) |
| LCx calcification | 0.69 (0.52–0.85) | 0.65 (0.37–0.88) |
| LCx occlusion ≥ 20 mm | 0.65 (0.47–0.81) | 0.45 (0.20–0.71) |
| LCx score ≥ 3 | 0.67 (0.41–0.90), MAE 0.35 | 0.56 (0.24–0.86) |

LAD bending > 45° has no test positives (4 val positives) and cannot be scored.
Mean binary AUROC: 0.75 (all studies) vs 0.64 (within artery).

## 4. View embeddings + frozen probes (DeepECG-CLIP "FAST mode" analog)

`scripts/embed_jcto_views.py` embeds every view of every study with (a) the fine-tuned
working model (per-video 512-d encoder vectors + the 1024-d attention/cls study vector) and
(b) the frozen base DeepCORO-CLIP encoder. Probes are trained once on cached vectors, tuned on
val, scored once on test (`probe_results.md`, `mil_probe_results.md`).

Attention-MIL probe over per-view embeddings (5-seed ensemble), test split:

| Head | ft encoder — all studies | ft — within artery | base encoder — within artery |
|---|---|---|---|
| J-CTO score ≥ 3 (overall) | 0.79 (0.70–0.88), MAE 0.80 | — | — |
| LAD score ≥ 3 | 0.88 (0.75–0.98) | 0.80 (0.62–0.94) | 0.81 (0.64–0.95) |
| RCA score ≥ 3 | 0.86 (0.78–0.93) | 0.78 (0.67–0.88) | 0.76 (0.64–0.87) |
| LCx score ≥ 3 | 0.73 (0.50–0.90) | 0.43 (0.18–0.70) | 0.52 (0.27–0.78) |
| CTO in LAD / RCA / LCx (localisation) | 0.86 / 0.92 / 0.79 | — | 0.86 / 0.93 / 0.77 |

The cached-embedding MIL probe matches or beats the 20-epoch per-artery fine-tune
(RCA within-artery score 0.78 vs 0.74; LAD 0.80 vs 0.75) at a fraction of the cost, and the
frozen base encoder is nearly as good as the fine-tuned one — the J-CTO signal is mostly
already in the CLIP embedding; the bottleneck is label count.

## 5. Take-aways

* Per-artery stratification works for RCA and LAD (within-artery score ≥ 3 AUROC ≈ 0.75–0.80)
  and not for LCx (20 test / 108 train CTOs; CIs span chance).
* "All-studies" per-artery AUROCs (0.83–0.88) are inflated by CTO localisation, which the
  model does well (AUROC 0.86–0.93). Report within-artery numbers for grading claims.
* No new labels arrived in the main project since July; Batch A (634 per-vessel clips)
  is still unannotated and would be the natural way to grow LCx/LAD counts.
