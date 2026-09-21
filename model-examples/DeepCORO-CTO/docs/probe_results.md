# Linear probes on frozen embeddings — refreshed J-CTO dataset (2026-09-21)

Studies: train 487 / val 98 / test 105.
AUROC with 95% bootstrap CI on the held-out test split; C/alpha chosen on val.

## Working model (fbvf4gzt ep19) head outputs on the refreshed splits

| head | val AUROC | test AUROC |
|---|---|---|
| jcto_blunt_stump | 0.78 (95% CI 0.68–0.87) | 0.74 (95% CI 0.64–0.83) |
| jcto_calcification | 0.75 (95% CI 0.66–0.85) | 0.78 (95% CI 0.69–0.87) |
| jcto_bending_gt45 | 0.81 (95% CI 0.72–0.90) | 0.60 (95% CI 0.42–0.76) |
| jcto_occlusion_length_gt20 | 0.73 (95% CI 0.62–0.82) | 0.83 (95% CI 0.75–0.91) |
| jcto_score (≥3) | 0.78 (95% CI 0.68–0.87) | 0.78 (95% CI 0.68–0.87) |

## Features: ft_pooled

| target | best C/α | val AUROC | test AUROC | test within-artery AUROC (n) |
|---|---|---|---|---|
| jcto_blunt_stump | 0.003 | 0.78 (95% CI 0.68–0.87) | 0.76 (95% CI 0.66–0.85) |  |
| jcto_calcification | 0.03 | 0.76 (95% CI 0.66–0.85) | 0.79 (95% CI 0.69–0.87) |  |
| jcto_bending_gt45 | 0.003 | 0.79 (95% CI 0.68–0.88) | 0.56 (95% CI 0.34–0.76) |  |
| jcto_occlusion_length_gt20 | 0.003 | 0.74 (95% CI 0.63–0.84) | 0.83 (95% CI 0.75–0.91) |  |
| jcto_blunt_stump_lad | 1.0 | 0.79 (95% CI 0.66–0.91) | 0.82 (95% CI 0.70–0.92) | 0.79 (95% CI 0.63–0.92) (37) |
| jcto_calcification_lad | 1.0 | 0.92 (95% CI 0.85–0.98) | 0.70 (95% CI 0.54–0.86) | 0.63 (95% CI 0.45–0.81) (37) |
| jcto_bending_gt45_lad | — | too few positives | | |
| jcto_occlusion_length_gt20_lad | 1.0 | 0.80 (95% CI 0.69–0.90) | 0.77 (95% CI 0.63–0.90) | 0.73 (95% CI 0.58–0.88) (37) |
| jcto_blunt_stump_rca | 0.03 | 0.88 (95% CI 0.80–0.94) | 0.80 (95% CI 0.71–0.89) | 0.68 (95% CI 0.54–0.81) (73) |
| jcto_calcification_rca | 0.1 | 0.84 (95% CI 0.76–0.92) | 0.86 (95% CI 0.79–0.93) | 0.79 (95% CI 0.68–0.89) (73) |
| jcto_bending_gt45_rca | 0.1 | 0.88 (95% CI 0.80–0.95) | 0.61 (95% CI 0.35–0.82) | 0.51 (95% CI 0.28–0.74) (73) |
| jcto_occlusion_length_gt20_rca | 0.03 | 0.87 (95% CI 0.80–0.94) | 0.86 (95% CI 0.79–0.93) | 0.81 (95% CI 0.68–0.91) (73) |
| jcto_blunt_stump_lcx | 0.3 | 0.59 (95% CI 0.38–0.80) | 0.58 (95% CI 0.35–0.81) | 0.47 (95% CI 0.20–0.75) (20) |
| jcto_calcification_lcx | 0.3 | 0.66 (95% CI 0.47–0.85) | 0.70 (95% CI 0.46–0.87) | 0.61 (95% CI 0.32–0.87) (20) |
| jcto_bending_gt45_lcx | 1.0 | 0.76 (95% CI 0.50–1.00) | 0.54 (95% CI 0.16–0.85) | 0.43 (95% CI 0.11–0.76) (20) |
| jcto_occlusion_length_gt20_lcx | 0.3 | 0.62 (95% CI 0.43–0.81) | 0.60 (95% CI 0.39–0.78) | 0.43 (95% CI 0.18–0.69) (20) |
| cto_lad | 1.0 | 0.86 (95% CI 0.78–0.92) | 0.77 (95% CI 0.67–0.87) |  |
| cto_rca | 1.0 | 0.89 (95% CI 0.81–0.96) | 0.87 (95% CI 0.78–0.94) |  |
| cto_lcx | 1.0 | 0.64 (95% CI 0.48–0.79) | 0.71 (95% CI 0.57–0.84) |  |
| jcto_score (≥3) | 10 | 0.76 (95% CI 0.66–0.85) | 0.80 (95% CI 0.70–0.88), MAE 0.88 |  |
| jcto_score_lad (≥3) | 10 | 0.82 (95% CI 0.69–0.93) | 0.88 (95% CI 0.75–0.98), MAE 0.63 | 0.83 (95% CI 0.66–0.96) (37), MAE 1.13 |
| jcto_score_rca (≥3) | 10 | 0.86 (95% CI 0.79–0.93) | 0.83 (95% CI 0.74–0.90), MAE 0.92 | 0.74 (95% CI 0.62–0.85) (73), MAE 1.05 |
| jcto_score_lcx (≥3) | 100 | 0.59 (95% CI 0.38–0.81) | 0.65 (95% CI 0.30–0.96), MAE 0.58 | 0.52 (95% CI 0.17–0.89) (20), MAE 1.37 |

## Features: ft_video_mean

| target | best C/α | val AUROC | test AUROC | test within-artery AUROC (n) |
|---|---|---|---|---|
| jcto_blunt_stump | 0.003 | 0.73 (95% CI 0.63–0.83) | 0.75 (95% CI 0.65–0.84) |  |
| jcto_calcification | 0.1 | 0.78 (95% CI 0.68–0.87) | 0.75 (95% CI 0.64–0.84) |  |
| jcto_bending_gt45 | 0.003 | 0.72 (95% CI 0.58–0.84) | 0.56 (95% CI 0.36–0.75) |  |
| jcto_occlusion_length_gt20 | 0.003 | 0.66 (95% CI 0.54–0.77) | 0.82 (95% CI 0.71–0.90) |  |
| jcto_blunt_stump_lad | 0.1 | 0.81 (95% CI 0.66–0.94) | 0.88 (95% CI 0.77–0.96) | 0.82 (95% CI 0.67–0.95) (37) |
| jcto_calcification_lad | 0.003 | 0.92 (95% CI 0.85–0.97) | 0.84 (95% CI 0.73–0.94) | 0.72 (95% CI 0.54–0.88) (37) |
| jcto_bending_gt45_lad | — | too few positives | | |
| jcto_occlusion_length_gt20_lad | 0.003 | 0.83 (95% CI 0.73–0.92) | 0.88 (95% CI 0.78–0.96) | 0.80 (95% CI 0.64–0.93) (37) |
| jcto_blunt_stump_rca | 0.003 | 0.87 (95% CI 0.80–0.94) | 0.83 (95% CI 0.74–0.90) | 0.71 (95% CI 0.58–0.83) (73) |
| jcto_calcification_rca | 0.01 | 0.86 (95% CI 0.78–0.93) | 0.88 (95% CI 0.81–0.94) | 0.81 (95% CI 0.70–0.90) (73) |
| jcto_bending_gt45_rca | 0.003 | 0.85 (95% CI 0.73–0.94) | 0.71 (95% CI 0.55–0.87) | 0.63 (95% CI 0.43–0.81) (73) |
| jcto_occlusion_length_gt20_rca | 0.01 | 0.86 (95% CI 0.78–0.93) | 0.88 (95% CI 0.81–0.94) | 0.81 (95% CI 0.69–0.91) (73) |
| jcto_blunt_stump_lcx | 0.003 | 0.73 (95% CI 0.56–0.88) | 0.62 (95% CI 0.35–0.87) | 0.40 (95% CI 0.15–0.67) (20) |
| jcto_calcification_lcx | 0.003 | 0.73 (95% CI 0.56–0.86) | 0.83 (95% CI 0.72–0.92) | 0.59 (95% CI 0.30–0.86) (20) |
| jcto_bending_gt45_lcx | 0.003 | 0.85 (95% CI 0.68–1.00) | 0.49 (95% CI 0.09–0.84) | 0.31 (95% CI 0.06–0.61) (20) |
| jcto_occlusion_length_gt20_lcx | 0.003 | 0.83 (95% CI 0.73–0.92) | 0.69 (95% CI 0.50–0.87) | 0.45 (95% CI 0.17–0.75) (20) |
| cto_lad | 0.003 | 0.86 (95% CI 0.78–0.93) | 0.87 (95% CI 0.79–0.94) |  |
| cto_rca | 0.01 | 0.91 (95% CI 0.83–0.97) | 0.92 (95% CI 0.85–0.97) |  |
| cto_lcx | 0.003 | 0.74 (95% CI 0.61–0.86) | 0.78 (95% CI 0.66–0.89) |  |
| jcto_score (≥3) | 100 | 0.75 (95% CI 0.65–0.85) | 0.70 (95% CI 0.60–0.80), MAE 0.90 |  |
| jcto_score_lad (≥3) | 10000 | 0.81 (95% CI 0.66–0.93) | 0.90 (95% CI 0.77–0.99), MAE 0.58 | 0.81 (95% CI 0.64–0.96) (37), MAE 1.05 |
| jcto_score_rca (≥3) | 1000 | 0.88 (95% CI 0.80–0.94) | 0.84 (95% CI 0.75–0.91), MAE 0.85 | 0.75 (95% CI 0.64–0.86) (73), MAE 0.94 |
| jcto_score_lcx (≥3) | 1000 | 0.79 (95% CI 0.64–0.91) | 0.67 (95% CI 0.30–0.88), MAE 0.58 | 0.37 (95% CI 0.14–0.67) (20), MAE 1.35 |

## Features: base_video_mean

| target | best C/α | val AUROC | test AUROC | test within-artery AUROC (n) |
|---|---|---|---|---|
| jcto_blunt_stump | 0.003 | 0.70 (95% CI 0.59–0.81) | 0.72 (95% CI 0.61–0.81) |  |
| jcto_calcification | 0.1 | 0.81 (95% CI 0.73–0.90) | 0.73 (95% CI 0.63–0.82) |  |
| jcto_bending_gt45 | 0.003 | 0.66 (95% CI 0.50–0.80) | 0.45 (95% CI 0.26–0.65) |  |
| jcto_occlusion_length_gt20 | 0.003 | 0.62 (95% CI 0.50–0.74) | 0.79 (95% CI 0.68–0.89) |  |
| jcto_blunt_stump_lad | 0.1 | 0.85 (95% CI 0.71–0.96) | 0.88 (95% CI 0.78–0.96) | 0.80 (95% CI 0.64–0.94) (37) |
| jcto_calcification_lad | 0.003 | 0.92 (95% CI 0.86–0.97) | 0.85 (95% CI 0.74–0.95) | 0.74 (95% CI 0.56–0.90) (37) |
| jcto_bending_gt45_lad | — | too few positives | | |
| jcto_occlusion_length_gt20_lad | 0.003 | 0.85 (95% CI 0.75–0.93) | 0.87 (95% CI 0.77–0.95) | 0.78 (95% CI 0.61–0.93) (37) |
| jcto_blunt_stump_rca | 0.003 | 0.85 (95% CI 0.77–0.92) | 0.80 (95% CI 0.71–0.89) | 0.65 (95% CI 0.52–0.79) (73) |
| jcto_calcification_rca | 0.01 | 0.86 (95% CI 0.78–0.93) | 0.87 (95% CI 0.80–0.93) | 0.80 (95% CI 0.68–0.90) (73) |
| jcto_bending_gt45_rca | 0.003 | 0.81 (95% CI 0.65–0.94) | 0.68 (95% CI 0.51–0.86) | 0.60 (95% CI 0.38–0.80) (73) |
| jcto_occlusion_length_gt20_rca | 0.003 | 0.85 (95% CI 0.76–0.92) | 0.88 (95% CI 0.80–0.94) | 0.76 (95% CI 0.63–0.89) (73) |
| jcto_blunt_stump_lcx | 0.03 | 0.80 (95% CI 0.61–0.94) | 0.52 (95% CI 0.29–0.75) | 0.35 (95% CI 0.08–0.64) (20) |
| jcto_calcification_lcx | 0.03 | 0.78 (95% CI 0.63–0.91) | 0.78 (95% CI 0.69–0.87) | 0.55 (95% CI 0.26–0.82) (20) |
| jcto_bending_gt45_lcx | 0.03 | 0.85 (95% CI 0.63–1.00) | 0.36 (95% CI 0.02–0.88) | 0.25 (95% CI 0.00–0.68) (20) |
| jcto_occlusion_length_gt20_lcx | 0.01 | 0.87 (95% CI 0.77–0.94) | 0.67 (95% CI 0.48–0.85) | 0.44 (95% CI 0.17–0.72) (20) |
| cto_lad | 0.3 | 0.88 (95% CI 0.80–0.95) | 0.85 (95% CI 0.77–0.93) |  |
| cto_rca | 0.03 | 0.91 (95% CI 0.83–0.97) | 0.92 (95% CI 0.86–0.97) |  |
| cto_lcx | 0.03 | 0.75 (95% CI 0.63–0.87) | 0.71 (95% CI 0.58–0.83) |  |
| jcto_score (≥3) | 100 | 0.75 (95% CI 0.65–0.84) | 0.66 (95% CI 0.55–0.76), MAE 0.93 |  |
| jcto_score_lad (≥3) | 1000 | 0.86 (95% CI 0.74–0.95) | 0.91 (95% CI 0.77–0.99), MAE 0.58 | 0.84 (95% CI 0.67–0.98) (37), MAE 0.99 |
| jcto_score_rca (≥3) | 1000 | 0.87 (95% CI 0.79–0.94) | 0.81 (95% CI 0.72–0.89), MAE 0.88 | 0.70 (95% CI 0.58–0.82) (73), MAE 0.99 |
| jcto_score_lcx (≥3) | 1000 | 0.82 (95% CI 0.67–0.93) | 0.65 (95% CI 0.30–0.86), MAE 0.59 | 0.36 (95% CI 0.12–0.64) (20), MAE 1.36 |
