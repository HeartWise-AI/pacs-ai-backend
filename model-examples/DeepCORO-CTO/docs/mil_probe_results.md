# Attention-MIL probe on cached per-view embeddings (DeepECG-CLIP FAST-mode analog)

## Encoder: ft (5-seed ensemble; test n=105)

| head | val AUROC | test AUROC | test within-artery AUROC (n) |
|---|---|---|---|
| jcto_blunt_stump | 0.77 (95% CI 0.67–0.86) | 0.78 (95% CI 0.68–0.87) |  |
| jcto_calcification | 0.77 (95% CI 0.67–0.86) | 0.79 (95% CI 0.69–0.87) |  |
| jcto_bending_gt45 | 0.71 (95% CI 0.60–0.82) | 0.57 (95% CI 0.39–0.75) |  |
| jcto_occlusion_length_gt20 | 0.73 (95% CI 0.62–0.83) | 0.83 (95% CI 0.75–0.91) |  |
| jcto_blunt_stump_lad | 0.77 (95% CI 0.61–0.90) | 0.90 (95% CI 0.80–0.97) | 0.85 (95% CI 0.70–0.96) (37) |
| jcto_calcification_lad | 0.92 (95% CI 0.84–0.98) | 0.83 (95% CI 0.71–0.94) | 0.71 (95% CI 0.54–0.87) (37) |
| jcto_bending_gt45_lad | 0.38 (95% CI 0.02–0.77) | n/a | n/a (37) |
| jcto_occlusion_length_gt20_lad | 0.79 (95% CI 0.67–0.90) | 0.86 (95% CI 0.77–0.95) | 0.81 (95% CI 0.67–0.94) (37) |
| jcto_blunt_stump_rca | 0.90 (95% CI 0.84–0.96) | 0.83 (95% CI 0.75–0.90) | 0.71 (95% CI 0.58–0.83) (73) |
| jcto_calcification_rca | 0.84 (95% CI 0.75–0.92) | 0.89 (95% CI 0.83–0.95) | 0.81 (95% CI 0.71–0.90) (73) |
| jcto_bending_gt45_rca | 0.84 (95% CI 0.73–0.92) | 0.72 (95% CI 0.56–0.88) | 0.62 (95% CI 0.42–0.82) (73) |
| jcto_occlusion_length_gt20_rca | 0.86 (95% CI 0.78–0.93) | 0.88 (95% CI 0.80–0.94) | 0.78 (95% CI 0.65–0.88) (73) |
| jcto_blunt_stump_lcx | 0.59 (95% CI 0.39–0.78) | 0.60 (95% CI 0.42–0.78) | 0.43 (95% CI 0.18–0.69) (20) |
| jcto_calcification_lcx | 0.56 (95% CI 0.37–0.73) | 0.74 (95% CI 0.55–0.89) | 0.76 (95% CI 0.51–0.95) (20) |
| jcto_bending_gt45_lcx | 0.73 (95% CI 0.50–0.90) | 0.52 (95% CI 0.25–0.79) | 0.39 (95% CI 0.11–0.71) (20) |
| jcto_occlusion_length_gt20_lcx | 0.71 (95% CI 0.55–0.86) | 0.70 (95% CI 0.53–0.85) | 0.59 (95% CI 0.32–0.84) (20) |
| cto_lad | 0.86 (95% CI 0.79–0.93) | 0.86 (95% CI 0.77–0.93) |  |
| cto_rca | 0.89 (95% CI 0.82–0.95) | 0.92 (95% CI 0.85–0.98) |  |
| cto_lcx | 0.64 (95% CI 0.50–0.77) | 0.79 (95% CI 0.67–0.89) |  |
| jcto_score (≥3) | 0.77 (95% CI 0.67–0.86) | 0.79 (95% CI 0.70–0.88), MAE 0.80 |  |
| jcto_score_lad (≥3) | 0.81 (95% CI 0.67–0.93) | 0.88 (95% CI 0.75–0.98), MAE 0.52 | 0.80 (95% CI 0.62–0.94) (37), MAE 0.92 |
| jcto_score_rca (≥3) | 0.88 (95% CI 0.80–0.95) | 0.86 (95% CI 0.78–0.93), MAE 0.81 | 0.78 (95% CI 0.67–0.88) (73), MAE 0.89 |
| jcto_score_lcx (≥3) | 0.70 (95% CI 0.53–0.85) | 0.73 (95% CI 0.50–0.90), MAE 0.45 | 0.43 (95% CI 0.18–0.70) (20), MAE 1.41 |

## Encoder: base (5-seed ensemble; test n=105)

| head | val AUROC | test AUROC | test within-artery AUROC (n) |
|---|---|---|---|
| jcto_blunt_stump | 0.76 (95% CI 0.66–0.86) | 0.78 (95% CI 0.67–0.87) |  |
| jcto_calcification | 0.78 (95% CI 0.68–0.87) | 0.80 (95% CI 0.70–0.89) |  |
| jcto_bending_gt45 | 0.72 (95% CI 0.60–0.83) | 0.50 (95% CI 0.27–0.73) |  |
| jcto_occlusion_length_gt20 | 0.71 (95% CI 0.59–0.81) | 0.84 (95% CI 0.74–0.92) |  |
| jcto_blunt_stump_lad | 0.81 (95% CI 0.68–0.91) | 0.89 (95% CI 0.79–0.97) | 0.83 (95% CI 0.68–0.95) (37) |
| jcto_calcification_lad | 0.91 (95% CI 0.83–0.98) | 0.83 (95% CI 0.71–0.93) | 0.70 (95% CI 0.54–0.86) (37) |
| jcto_bending_gt45_lad | 0.47 (95% CI 0.06–0.77) | n/a | n/a (37) |
| jcto_occlusion_length_gt20_lad | 0.83 (95% CI 0.72–0.92) | 0.87 (95% CI 0.77–0.95) | 0.78 (95% CI 0.63–0.92) (37) |
| jcto_blunt_stump_rca | 0.88 (95% CI 0.81–0.94) | 0.82 (95% CI 0.73–0.90) | 0.66 (95% CI 0.52–0.80) (73) |
| jcto_calcification_rca | 0.85 (95% CI 0.76–0.92) | 0.89 (95% CI 0.82–0.95) | 0.81 (95% CI 0.69–0.90) (73) |
| jcto_bending_gt45_rca | 0.81 (95% CI 0.68–0.92) | 0.72 (95% CI 0.58–0.87) | 0.62 (95% CI 0.43–0.81) (73) |
| jcto_occlusion_length_gt20_rca | 0.84 (95% CI 0.76–0.92) | 0.90 (95% CI 0.83–0.95) | 0.80 (95% CI 0.66–0.91) (73) |
| jcto_blunt_stump_lcx | 0.55 (95% CI 0.33–0.77) | 0.67 (95% CI 0.49–0.85) | 0.49 (95% CI 0.22–0.77) (20) |
| jcto_calcification_lcx | 0.61 (95% CI 0.43–0.78) | 0.78 (95% CI 0.64–0.92) | 0.69 (95% CI 0.43–0.91) (20) |
| jcto_bending_gt45_lcx | 0.64 (95% CI 0.31–0.96) | 0.60 (95% CI 0.25–0.87) | 0.39 (95% CI 0.06–0.72) (20) |
| jcto_occlusion_length_gt20_lcx | 0.71 (95% CI 0.55–0.85) | 0.70 (95% CI 0.51–0.86) | 0.52 (95% CI 0.25–0.79) (20) |
| cto_lad | 0.87 (95% CI 0.79–0.93) | 0.86 (95% CI 0.78–0.93) |  |
| cto_rca | 0.90 (95% CI 0.82–0.96) | 0.93 (95% CI 0.86–0.98) |  |
| cto_lcx | 0.65 (95% CI 0.52–0.78) | 0.77 (95% CI 0.64–0.87) |  |
| jcto_score (≥3) | 0.77 (95% CI 0.67–0.86) | 0.76 (95% CI 0.66–0.85), MAE 0.80 |  |
| jcto_score_lad (≥3) | 0.84 (95% CI 0.72–0.94) | 0.90 (95% CI 0.78–0.98), MAE 0.51 | 0.81 (95% CI 0.64–0.95) (37), MAE 0.99 |
| jcto_score_rca (≥3) | 0.87 (95% CI 0.79–0.95) | 0.85 (95% CI 0.77–0.92), MAE 0.78 | 0.76 (95% CI 0.64–0.87) (73), MAE 0.89 |
| jcto_score_lcx (≥3) | 0.71 (95% CI 0.54–0.86) | 0.72 (95% CI 0.52–0.86), MAE 0.49 | 0.52 (95% CI 0.27–0.78) (20), MAE 1.40 |
