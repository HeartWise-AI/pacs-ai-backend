# Reproducible model image releases

PACS-AI builds an inference image once, identifies it by registry digest, and promotes that same artifact between environments. Pull requests validate release metadata but never publish images. Official publication happens only after merge through the protected `Publish inference model image` workflow.

## Trust boundary

The release job runs on a HeartWise-controlled GitHub Actions runner labeled `pacs-ai-release`. GitHub schedules the job, but the Hugging Face and Docker Hub credentials remain in host-managed files on that runner:

```text
/etc/pacs-ai-release/secrets/hf_token
/etc/pacs-ai-release/secrets/dockerhub_username
/etc/pacs-ai-release/secrets/dockerhub_token
/etc/pacs-ai-release/secrets/firebase_superuser_key
```

Use a dedicated, read-only Hugging Face token with access only to the required gated repositories. The Docker Hub token should be limited to the `heartwisehub` publication scope. Give the files to the runner service account with mode `0600`; they must never be placed in the repository or workspace.

The workflow creates a temporary Docker configuration, authenticates with `--password-stdin`, and removes that configuration after the job. Hugging Face credentials are supplied to Docker with a BuildKit secret and are not stored in image layers, build arguments, logs, or release evidence.

Because model weights are embedded in the image, gated or restricted weights must be published only to a registry repository with equivalent access controls.

The runner must provide Linux, Docker with BuildKit, Python 3.9 or newer, outbound access to Hugging Face, Docker Hub, and the PACS-AI catalog API, and the labels `self-hosted`, `linux`, `x64`, and `pacs-ai-release`. Configure the GitHub `model-release` environment with required reviewers, an HTTPS `PACS_AI_CATALOG_API_URL` environment variable, and an optional positive-integer `PACS_AI_RELEASE_MIN_FREE_GIB` variable (default: `25`) so a queued publication cannot access the runner until an authorized person approves it. Protect `master` and restrict who can dispatch release workflows.

The workflow checks free disk space before building, removes only local image references stamped with the workflow's exact Git revision, and deletes its temporary Docker credentials. It deliberately preserves BuildKit cache for subsequent releases. Configure a size or age limit for that cache on the dedicated runner; do not use an unrestricted system-wide prune on a machine that hosts other Docker workloads. Release evidence is uploaded even when the later catalog-registration step fails, allowing registration to be retried without rebuilding the image.

## Manifest contract

An official release requires complete provenance in `data/model_info.json`. `sourceRevision` may remain `null` in Git because the workflow stamps the exact checked-out commit:

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

The official Dockerfile consumes the corresponding `PACS_AI_*` build arguments. A downloader may use `snapshot_download`, `hf_hub_download`, or model-specific logic, but it must download the declared immutable revision and fail when the declared checkpoint does not match `weightsSha256`.

## Pull request checks

A model release PR establishes the release inputs and tests without receiving publication credentials. It must verify:

- semantic model version and complete provenance;
- immutable Hugging Face revision and safe checkpoint path;
- checkpoint fingerprint behavior using controlled fixtures;
- Dockerfile BuildKit-secret usage and provenance build arguments;
- runtime resource and model-info contracts;
- model-specific startup and inference behavior.

Do not publish from a pull-request commit. The official `sourceRevision` is the reviewed commit reachable from `master` that is actually checked out for the release.

## Local candidate build

Use the same release command without `--publish` to build and verify a candidate from a clean checkout:

```bash
python3 scripts/release-model.py \
  model-examples/DeepCORO-SYNTAX \
  --image-repository heartwisehub/pacs-ai-deepcoro-syntax \
  --hf-token-file /secure/path/hf_token \
  --evidence release-evidence.json
```

This operation:

1. validates the manifest, image namespace, Git state, and Dockerfile contract;
2. stamps the exact Git commit and manifest provenance into build arguments and OCI labels;
3. mounts the Hugging Face token only for the download instruction;
4. builds the semantic-version tag without creating `latest`;
5. inspects the local image labels and independently re-hashes the packaged checkpoint;
6. starts a temporary container and compares `/inference/model-info` with the release inputs;
7. writes build evidence without a registry digest.

`Dockerfile.local` is a development-only path for weights already present on disk. It must verify those bytes before use, carry the `ai.heartwise.release.channel=local` label, and use a visibly local tag. It is never accepted by `release-model.py` and must not publish an official version.

## Official publication

From the GitHub Actions page, select `Publish inference model image` on `master` and provide:

- the model directory under `model-examples/`;
- the lowercase `heartwisehub/...` repository without a tag;
- whether to publish the optional `latest` alias.

The protected `model-release` environment provides manual approval. The workflow refuses commits not reachable from `origin/master` and refuses to overwrite an existing semantic-version tag. It then runs:

```text
preflight -> build -> inspect -> runtime check -> push version -> resolve digest -> evidence
```

The versioned image and immutable digest are authoritative. `latest`, when explicitly requested, is only a convenience alias and is never recorded as deployment identity. After publication, the workflow sends the verified release evidence to the catalog API. This creates an immutable, idempotent Firestore catalog entry; it does not deploy the image.

## Candidate publication contract

The release engine also supports protected pre-merge candidate builds. The source manifest keeps the intended stable version, while candidate mode derives an immutable prerelease version from the pull request and the first 12 characters of the source revision. For example, manifest version `6.0.1`, pull request `366`, and source revision `df1984511ec9...` produce `6.0.1-rc.366.df1984511ec9`. The full revision remains in provenance. The derived version is stamped into the Docker tag, OCI labels, container environment, runtime model-info response, and release evidence without modifying the source manifest.

Candidate publication requires explicit branch, pull-request, and workflow-run identity:

```bash
python3 scripts/release-model.py \
  model-examples/DeepCORO-SYNTAX \
  --channel candidate \
  --source-branch issue-365-transactional-model-upgrades \
  --pull-request 366 \
  --workflow-run 123456 \
  --image-repository heartwisehub/pacs-ai-deepcoro-syntax \
  --hf-token-file /secure/path/hf_token \
  --publish \
  --reuse-existing \
  --evidence model-release-evidence.json
```

Candidate mode permits an unmerged clean commit but never publishes `latest`. `--reuse-existing` pulls and fully revalidates an existing immutable tag instead of rebuilding or pushing it; any label, runtime metadata, or packaged-weight mismatch fails the run. The candidate GitHub workflow must use restricted candidate credentials and an approved environment. Official publication remains restricted to stable versions whose commits are reachable from `origin/master`.

## Release evidence

The workflow uploads `model-release-evidence.json` and includes it in the job summary. It records:

- model ID, name, and semantic version;
- PACS-AI source repository and commit;
- model repository and immutable revision;
- checkpoint path and SHA-256;
- versioned image reference and local image ID;
- resolved registry digest and immutable image reference;
- GitHub actor and workflow run identity.
- release channel, intended stable version, source branch, pull request, and whether an existing image was reused.

It never records a token, password, secret path, or secret value. If catalog registration must be retried independently, download the evidence artifact and run `scripts/register-model-release.py` with the protected catalog URL and credential file. The operation is idempotent for the same artifact and rejects conflicting metadata.

## Version policy

- New or retrained weights increment the major version.
- Compatible inference behavior changes increment the minor version.
- Packaging or operational fixes with equivalent predictions increment the patch version.
- A published version tag is immutable and cannot be overwritten with different contents.
- Rebuilding an existing reviewed release may produce a different image digest, but it cannot replace the original version tag; publish a new patch version when the artifact changes.

## Adding a model

During development, use disposable local tags and local BuildKit secrets. Before the release PR is merged:

1. publish or identify the final model artifact in its source repository;
2. record its immutable revision, checkpoint path, and SHA-256;
3. add complete provenance and the initial semantic version to the manifest;
4. make the official Dockerfile consume all required `PACS_AI_*` arguments;
5. make the downloader pin and verify the supplied artifact;
6. measure resources and run startup and inference tests;
7. merge the reviewed code, then manually approve the official release.

Publication adds the immutable artifact to the version catalog but does not replace a running PACS-AI model. Initial registration remains a separate controlled operation. For an existing registration, an owner or administrator selects any available catalog version in Workspace Settings. PACS-AI validates and activates that exact artifact transactionally; failed activation automatically restores the previous deployment, and selecting an older available version performs a deliberate rollback.
