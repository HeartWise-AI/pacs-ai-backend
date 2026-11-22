---
name: pacs-ai-model-verification
description: Verify new or updated PACS-AI inference model source and immutable Docker images. Use for model PR review, release readiness, or post-publication image checks. Do not use to publish, register, or deploy a model unless the user separately requests that mutation.
---

# PACS-AI model verification

Use `scripts/verify-model.py` as the authoritative verifier. Do not replace executable checks with a manual checklist.

Read `../pacs-ai-model-mapping/SKILL.md` when a failure concerns model architecture, preprocessing, DICOM selection, output heads, or prediction parity.

When verification passes and CardioAgent must execute the model, hand the exact
verified contract and immutable release evidence to
[`HeartWise-AI/cardio-agent:.agents/skills/add-study-service-model/SKILL.md`](https://github.com/HeartWise-AI/cardio-agent/blob/feat/study-latest-results/.agents/skills/add-study-service-model/SKILL.md).
That downstream workflow owns the Study Service registry, dedicated Celery queue
and worker, Compose wiring, and execution-path validation. Verification alone does
not authorize deployment, study dispatch, polling, or backfill.

## Choose a mode

- Use `source` for a model PR, a new model directory, or an update to model code or metadata.
- Use `image` only for an already built immutable image identified by `repository@sha256:<digest>`.
- Default to `source` when the request does not identify an image digest.

## Source verification

For an explicit model:

```bash
python3 scripts/verify-model.py source model-examples/<ModelName> \
  --json model-verification-evidence.json
```

For all models changed from a known base revision:

```bash
python3 scripts/verify-model.py source --changed-since <base-revision> \
  --json model-verification-evidence.json
```

Add `--run-model-tests` only when the model's Python dependencies are installed. A skipped model-test check means source verification is incomplete for release readiness, even when static verification passes.

The verifier checks the manifest and resource contract, approved image mapping, provenance-aware Dockerfile contract, lifecycle helpers, OpenAPI endpoints, model tests, and forbidden credentials or packaged weights.

## Image verification

Require an immutable digest. Do not accept `latest` or another mutable tag as release identity.

```bash
python3 scripts/verify-model.py image model-examples/<ModelName> \
  --image heartwisehub/<repository>@sha256:<digest> \
  --json model-verification-evidence.json
```

Image verification pulls the artifact, checks its OCI labels and packaged checkpoint hash, and starts a temporary container to compare `/inference/model-info` with the source manifest. Private registry access may require user approval and credentials. Never expose credentials in commands, logs, or evidence.

## Safety

Verification is read-only with respect to PACS-AI and the registry. It may pull a Docker image and start a temporary local container in `image` mode.

Do not publish, push, register, replace, restart, or remove a model. Do not invoke `scripts/deploy-model.sh` or a publication workflow unless the user explicitly requests that separate action.

## Interpret and report

Report every check as `PASS`, `FAIL`, or `SKIP`.

- `PASS`: the check executed and satisfied its contract.
- `FAIL`: the check executed and found a violation.
- `SKIP`: the check could not run or does not apply; preserve the reason.

Distinguish regressions introduced by the change from inherited issues in existing containers. Do not call a model release-ready when any required check failed or when model-specific tests were skipped.

Return the model name and version, source revision, image digest when applicable, overall result, failed or skipped checks, and the evidence path.
