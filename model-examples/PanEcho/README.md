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
