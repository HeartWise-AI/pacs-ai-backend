# EchoPrime inference image

The production image downloads the official EchoPrime v1.0.0 model archive from
`echonet/EchoPrime` and the PACS-AI auxiliary artifacts from a pinned
`heartwise/pacs-ai-examples` revision. `download_model.py` verifies the archive, both neural-network
checkpoints, and every auxiliary file before the image is assembled.

Build and verify a local candidate with the repository release tooling:

```bash
python3 scripts/release-model.py model-examples/ECHO-PRIME/generic \
  --image-repository heartwisehub/echo-prime-generic \
  --hf-token-file /secure/path/hf_token
```

Official images are published only after merge through the protected `Publish inference model
image` workflow. The immutable semantic-version tag and registry digest identify the deployed
artifact; `latest` is optional and non-authoritative.
