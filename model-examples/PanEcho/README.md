# PanEcho inference image

The production image downloads the official PanEcho v1.0 checkpoint and task metadata from
`CarDS-Yale/PanEcho` during the build. The source revision and SHA-256 fingerprints are fixed by
`data/model_info.json` and verified by `download_model.py`; ignored files under `content/` are not
part of the build context.

Build and verify a local candidate with the repository release tooling:

```bash
python3 scripts/release-model.py model-examples/PanEcho \
  --image-repository heartwisehub/pacs-ai-pan-echo \
  --hf-token-file /secure/path/hf_token
```

Official images are published only after merge through the protected `Publish inference model
image` workflow. The immutable semantic-version tag and registry digest identify the deployed
artifact; `latest` is optional and non-authoritative.

The image does not package the optional EchoPrime view-classifier checkpoint. When that checkpoint
is absent, the existing runtime falls back to request metadata or unfiltered PanEcho inference, which
matches the behavior of the measured 1.0.0 deployment.

## Changelog

- 1.1.3: MHI aortic stenosis calibration (panecho-calib). The AVStenosis and AVPkVel heads use the upstream-aligned input (`utils/upstream_preprocess.py`): crop to the 2D ultrasound regions from SequenceOfUltrasoundRegions, square pad, 224 with the correct aspect, 4 evenly spaced 16-frame clips per video with the logits averaged, then probabilities averaged per study, as the PanEcho authors evaluated. The label (`models/as_calibration.json`, `utils/as_calibration.py`) is `Significant AS likely` when P(Mild|Moderate)+P(Severe) >= 0.2935, else `No significant AS suspected`, labelled as MHI-calibrated screening, not a diagnosis. The AV peak velocity is corrected (2.6045 x raw - 2.7346) and the raw value is kept as `Aortic valve peak velocity (uncorrected)`. `asScreening.calibrated` carries the label and thresholds. Fitted on 235 MHI development studies; locked test of 157: sensitivity 0.81 for moderate or severe AS, specificity 0.91 against echos without AS, velocity MAE 1.04 -> 0.68 m/s. The other 38 heads keep the 1.1.x input and are unchanged: the upstream-aligned input made LVEF, IVSd, LVPWd, LVEDV and LA diameter slightly worse against the MHI SR. Built on 1.1.2 (#609), with no view classifier.
- 1.1.2: aortic stenosis is presented as a screening score, not a diagnosis. PanEcho reads the 2D clips only (no continuous-wave Doppler) and on MHI read severe AS (measured Vmax 3.9-4.8 m/s, TAVI patients) as none or mild at argmax, with the AV peak velocity shrunk towards the training mean (severe AS predicted at about 2.5 m/s). The `diagnosis` value of "Aortic valve stenosis" is now `Screening score, not a diagnosis: P(severe AS) x.xx, P(any AS) x.xx` (P(any AS) = P(Mild|Moderate) + P(Severe); head order Mild|Moderate, None, Severe), "Aortic valve peak velocity" is labelled `AI estimate, under-estimates severe AS` instead of a Normal/stenosis category, the JSON output carries `asScreening` (`pSevere`, `pAny`, `pMildModerate`, `pNone`, `avVmaxEstimate`, `note` en/fr), the HTML report shows them in an "Aortic stenosis screening (AI)" section outside the normal/abnormal counts, and AS no longer drives the recommendation text. `predictions` (raw head outputs), the weights and the other 38 outputs are unchanged.
- 1.1.1: security patch of 1.1.0 only. torch 2.8.0 -> 2.13.0 and torchvision 0.23.0 -> 0.28.0 (cu126 wheels); the other direct dependencies are pinned to the versions the 1.1.0 image already had. Same weights as 1.1.0. torchvision 0.28 resizes uint8 frames with a native kernel that differs by one grey level from 0.23 on about 12% of pixels (LV volumes moved by up to 0.15 mL), so the transform now does the resize the way 0.23 did (float, antialias, rounded): preprocessing is bit-identical to 1.1.0 and predictions are unchanged (side-by-side numbers in the PR). Does not include the 1.2.0 view-classifier changes.
- 1.1.0: the view classifier's results are kept per clip (SOPInstanceUID) instead of per series. Echo studies that store every clip in one series used to take the last clip's view for all clips, so the view filter kept or dropped the whole study by that one view. Predictions change for those studies. Includes the unreleased 1.0.4 changes below.
- 1.0.4: `read_body` reserves at most 64 MiB from `Content-Length` and grows as bytes arrive (PR #520); model-info reports the runtime `PACS_AI_MODEL_VERSION` (PR #526). Predictions unchanged.

## Request handling and performance (1.0.3)

A heavy echo study reaches `POST /inference/predict` as about 2 GB of JSON. Since 1.0.3 the
endpoint reads the body into one buffer, keeps the base64 payloads out of the JSON parser
(`utils/fast_request.py`), decodes each instance once, and decodes only the 16 frames PanEcho
samples from each 8-bit clip. Predictions are unchanged. Every request logs its stage timings:

```text
PanEcho request: 1835 MiB received in 3.9s, parsed in 0.3s
PanEcho predict timings: base64=0.5s dicom_parse=0.5s pixel_decode=0.4s clip_transform=0.6s model_forward=0.9s total=3.0s
```

The model loads without network access: the released checkpoint supplies every backbone tensor,
so the ImageNet initialisation is no longer downloaded and an incomplete checkpoint is rejected.

JPEG-LS input (transfer syntaxes 1.2.840.10008.1.2.4.80/.81) is decoded with the CharLS plugin
(`pyjpegls`) and gives the same pixels and predictions as uncompressed input. Decoding costs
about 10 ms per frame, so a JPEG-LS study is smaller to transfer but slower for the model than
the same study sent uncompressed.

`nginx.conf` allows 1800 s for the upstream response; with the 60 s default nginx answered 504
on long studies while the model was still computing.
