# EchoAlign (research use only)

> **Research use only — EchoAlign (CC BY-NC 4.0), not for clinical decisions.**
> Not for diagnosis or treatment. Non-commercial use with attribution only.

EchoAlign is an echocardiography vision-language foundation model by Ahmed Aly, Darya Taratynova,
Lotfi Abdelkrim Mecharbat, Roman A. Sandler, Joseph Z. Sokol, Joshua Penn, Klaus H. Maier-Hein,
Numan Saeed and Mohammad Yaqub (MBZUAI BioMedIA, with iCardio.ai and DKFZ).

- Paper: https://arxiv.org/abs/2611.27517
- Code: https://github.com/BioMedIA-MBZUAI/EchoAlign (package copied in `models/echoalign/` from commit 7f0d78a)
- Weights and probes: https://huggingface.co/EchoAlign/EchoAlign, revision `7ceeb3e3e3dc342b4e36ecf0312a50edb120adde`
- License: [CC BY-NC 4.0](https://creativecommons.org/licenses/by-nc/4.0/) for code and weights. The model card adds
  "Research use only. Model outputs are not clinical advice."

The weights are not in git. `download_model.py` fetches `EchoAlign.pt` and the released probes from Hugging Face at the
pinned revision and checks every file's sha256 during the Docker build.

## What the container does

- Input: a transthoracic echo study (DICOM, `seriesInstanceImages`), as for PanEcho. TEE and non-image instances are skipped.
  By default only multi-frame clips are used, which is the set PanEcho receives. `additionalMetadata.instanceSelection: "all"`
  also uses still images (the developers' MIMIC evaluation did this).
- Each clip: 16 frames at 24 fps, crop to the DICOM ultrasound regions, square pad, 224 px, then the TimeSformer clip encoder.
- Study: EchoAlign's study transformer pools all clips into 13 vectors, then a PCA embedding of 832 numbers.
  The 53 released linear probes are applied with the developers' thresholds and grade cuts. No view classifier.
- Output (JSON): AS grade and P(severe), P(moderate or severe), AV Vmax / mean / peak gradient estimates, LVEF,
  AR/MR/TR grades, every head (`allHeads`), zero-shot scores, and per-clip AS/Vmax/EF.
  The HTML report starts with the research banner.

## PACS-AI policy

- Registered as a research model. Only OWNER/ADMIN and custom roles that list `EchoAlign` explicitly can see or run it.
  Base USERs and "all models" custom roles do not see it (api-pacs research-model exclusion).
- Runs automatically on new echocardiograms (gateway ingestion job, research-gated as above) and on demand.
  Guards for automatic runs: a request above 16 GiB (`ECHOALIGN_MAX_REQUEST_BYTES`) or a study with more than
  200 usable clips (`ECHOALIGN_MAX_CLIPS`) is skipped with `STUDY_TOO_LARGE`; one inference at a time.
- The viewer shows EchoAlign with the PanEcho template (PACS-AI #591); the HTML report uses PanEcho's layout.

## MHI evaluation (2026-11-27, nothing fitted on MHI data)

The study set was 54 transthoracic studies with structured-report truth: 5 severe AS, 12 any AS, and 6 with LVEF < 40. Four of them are TAVI patients.

| Metric | PanEcho live | EchoAlign (same clips) |
|---|---|---|
| Severe AS AUROC | 0.94 (0.84-1.00) | 0.94 (0.83-1.00) |
| Any AS AUROC | 0.95 (0.88-0.99) | 0.95 (0.88-0.99) |
| Severe AS called by the model's own rule | 0/5 | 0/5 |
| Severe AS graded at least moderate | – | 3/5 (specificity 48/49) |
| AV Vmax r / MAE / slope | 0.80 / 0.56 / 0.32 | 0.79 / 1.10 / 0.12 |
| LVEF < 40 AUROC | 0.96 | 0.96 |

EchoAlign did not call any of the 4 TAVI patients severe. It graded 2 of them moderate, 1 mild and 1 normal. Its AV Vmax
estimate is compressed toward the middle of the range: severe AS comes out at about 2.9 m/s. Use the measured Doppler values.
