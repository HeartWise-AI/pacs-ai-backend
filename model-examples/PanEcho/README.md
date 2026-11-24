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
