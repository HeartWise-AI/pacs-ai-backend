# Model provenance contract

PACS-AI model provenance identifies the source code and exact model weights served by an inference image. It complements the semantic model version and Docker image digest; none of those identifiers substitutes for the others.

## Manifest schema

Inference images declare provenance in `data/model_info.json`:

```json
{
  "version": "6.0.0",
  "provenance": {
    "sourceRepository": "HeartWise-AI/pacs-ai-backend",
    "sourceRevision": null,
    "modelRepository": "heartwise/deepcoro_clip_cardiosyntax",
    "modelRevision": "1ffb38cfc10fa10c4b60f44746063feb860eeba0",
    "weightsPath": "v6_20260929-203527/models/best_model_epoch_19.pt",
    "weightsSha256": "856f5d6523c25a45c62886bf6cd1d351297821f60c3465a20b962aa46fa08ec0"
  }
}
```

The Python inference runtime and Go backend apply the same rules:

- `sourceRepository` and `modelRepository` use `owner/repository` identifiers, not URLs.
- `sourceRevision` is either `null` in the source manifest or a 40-character lowercase Git commit SHA.
- `modelRevision` is a required 40-character lowercase commit SHA for the model repository.
- `weightsPath` is a normalized, repository-relative POSIX path. Absolute paths, backslashes, empty segments, `.` and `..` are rejected.
- `weightsSha256` is the 64-character lowercase SHA-256 of the checkpoint bytes.
- Unknown or missing provenance fields are rejected.

The reproducible image build supplies `PACS_AI_SOURCE_REVISION`. The inference runtime validates that value and substitutes it for the source manifest's `null` value in `/inference/model-info`. The source manifest remains reusable and does not change during the build.

Official inference images are built after merge with `scripts/release-model.py` and the protected GitHub Actions workflow described in [Reproducible model image releases](model-image-release.md). The release process verifies that the manifest, build inputs, OCI labels, runtime response, local image ID, and pushed registry digest describe the same artifact.

## Migration behavior

Provenance is optional while existing images are migrated. A legacy manifest that omits `provenance` remains valid and its API response omits the field. Explicit `null`, partial objects, placeholders, mutable branches, and invented fingerprints are not valid migration states.

For each legacy model:

1. Identify the exact model-repository commit, checkpoint path, and checkpoint SHA-256.
2. Add the complete provenance object and pin the downloader to the same model revision.
3. Make the image build verify the checkpoint fingerprint and stamp the PACS-AI source revision.
4. Rebuild, test, and publish a new immutable semantic version and Docker digest.
5. Verify `/inference/model-info`, OCI labels, and the Docker digest before activation.

This contract phase does not rebuild legacy images or make provenance mandatory for already registered containers. The managed-upgrade workflow will require complete provenance for a candidate before it can become active.

## Backend exposure

The backend preserves the optional provenance object returned by an inference container through:

- `GET /v1/inference/model/proxy/container/{containerID}/info`
- the available-inference-model response used by administration workflows

Persistence of resolved Docker digests, upgrade history, and rollback metadata belongs to the transactional managed-upgrade phase.
