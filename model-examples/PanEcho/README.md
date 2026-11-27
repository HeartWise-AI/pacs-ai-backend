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

## View selection (1.2.0)

The image packages the EchoPrime 20-view classifier (MViT-v2-S). Its original Hugging Face
repository (`VoyagerWSH/20EchoViewClassifier`) is private and no longer reachable, so the Dockerfile
copies the checkpoint from the published image that packaged it, pinned by digest
(`heartwisehub/echo-prime_view-classifier@sha256:b2f785a4…`), and `download_model.py
--verify-view-classifier` fails the build unless its SHA-256 is
`5bf6010b17c945b44a8d8890baf02995bfb00ed932c5adbda031c2b4f5174af2` (same bytes as the team copy on
the NAS, `models/PanEcho/view_classifier/`). No token is needed. `config.json` sets
`view_classifier_required`, so the model refuses to load without it rather than predicting on every
clip.

For each request PanEcho:

1. keeps multi-frame clips (single frames and Doppler stills are dropped, as before);
2. skips the study with `TEE_NOT_SUPPORTED` when a clip's study/series description or protocol says
   TEE/ETO/trans-oesophageal, or its `TransducerData` names a TEE probe (e.g. Philips `X8-2t`);
3. classifies every clip on its own (per SOPInstanceUID, since 1.1.0) and keeps those classified as
   A2C, A4C, A5C, PLAX or PSAX, including their zoomed/LV/deep/AV variants (substring match);
4. skips the study with `NO_STANDARD_TTE_VIEWS` when no clip is kept (e.g. non-cardiac ultrasound),
   or `NO_VIDEO_CLIPS` when there is no multi-frame clip.

A skip is a normal answer: `predictions` is empty, `modelRecommendations` explains it in English and
French, and `viewSelection.status` is `skipped` with the `skipReason`. Request
`additionalMetadata` keyed by SOPInstanceUID or SeriesInstanceUID still overrides the classifier.

Every JSON result carries `viewSelection`:

```json
{"status": "used", "skipReason": null, "totalClips": 16, "clipsUsed": 9,
 "standardViews": ["A2C", "A4C", "A5C", "PLAX", "PSAX"],
 "viewCounts": {"A4C": 3, "PLAX": 2, "SUBCOSTAL": 2, "...": 0}, "usedViewCounts": {"A4C": 3, "...": 0},
 "method": "PanEcho view classifier",
 "classifier": {"name": "EchoPrime 20-view classifier (MViT-v2-S)", "sha256": "5bf6010b…"},
 "clips": [{"seriesNumber": 1, "instanceNumber": 3, "view": "A4C", "confidence": 0.97, "used": true}]}
```

Clips are named by series and instance number only, never by UID.

## Changelog

- 1.2.1: 1.2.0 with the same security update as 1.1.1: torch 2.13.0 and torchvision 0.28.0 (cu126), the other direct dependencies pinned, and the torchvision 0.23 resize kept for the PanEcho clips so predictions do not move.
- 1.2.0: ships the pinned view-classifier checkpoint, so the per-clip view filter runs: PanEcho predicts only from A2C/A4C/A5C/PLAX/PSAX clips, skips TEE and studies without a standard TTE view with an explicit reason, and returns `viewSelection`. Predictions change for most studies.
- 1.1.1 (released from the 1.1.0 code, merged into this history): security patch of 1.1.0 only. torch 2.8.0 -> 2.13.0 and torchvision 0.23.0 -> 0.28.0 (cu126 wheels); the other direct dependencies are pinned to the versions the 1.1.0 image already had. Same weights as 1.1.0. torchvision 0.28 resizes uint8 frames with a native kernel that differs by one grey level from 0.23 on about 12% of pixels (LV volumes moved by up to 0.15 mL), so the transform now does the resize the way 0.23 did (float, antialias, rounded): preprocessing is bit-identical to 1.1.0 and predictions are unchanged (side-by-side numbers in the PR). Does not include the 1.2.0 view-classifier changes.
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
