#!/usr/bin/env python3
"""Build and publish a provenance-aware PACS-AI inference image."""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid
from collections.abc import Sequence
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path, PurePosixPath
from typing import Any

REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
ALLOWED_EXECUTABLES = frozenset({"docker", "git"})
GIT_REVISION_PATTERN = re.compile(r"^[0-9a-f]{40}$")
REPOSITORY_PATTERN = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$")
SHA256_PATTERN = re.compile(r"^[0-9a-f]{64}$")
SEMVER_PATTERN = re.compile(
    r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
    r"(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?"
    r"(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$"
)
DOCKER_TAG_PATTERN = re.compile(r"^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$")
IMAGE_REPOSITORY_PATTERN = re.compile(
    r"^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)+$"
)
DIGEST_PATTERN = re.compile(r"sha256:[0-9a-f]{64}")

PROVENANCE_FIELDS = {
    "sourceRepository",
    "sourceRevision",
    "modelRepository",
    "modelRevision",
    "weightsPath",
    "weightsSha256",
}
PROVENANCE_BUILD_ARGS = {
    "PACS_AI_SOURCE_REPOSITORY",
    "PACS_AI_SOURCE_REVISION",
    "PACS_AI_MODEL_VERSION",
    "PACS_AI_MODEL_REPOSITORY",
    "PACS_AI_MODEL_REVISION",
    "PACS_AI_WEIGHTS_PATH",
    "PACS_AI_WEIGHTS_SHA256",
}
DOWNLOADER_ARGUMENTS = {
    "--repository": "PACS_AI_MODEL_REPOSITORY",
    "--revision": "PACS_AI_MODEL_REVISION",
    "--weights-path": "PACS_AI_WEIGHTS_PATH",
    "--weights-sha256": "PACS_AI_WEIGHTS_SHA256",
}


class ReleaseError(RuntimeError):
    """A release preflight, build, or verification failure."""


@dataclass(frozen=True)
class ReleaseMetadata:
    model_id: str
    model_name: str
    version: str
    source_repository: str
    source_revision: str
    model_repository: str
    model_revision: str
    weights_path: str
    weights_sha256: str

    @property
    def provenance(self) -> dict[str, str]:
        return {
            "sourceRepository": self.source_repository,
            "sourceRevision": self.source_revision,
            "modelRepository": self.model_repository,
            "modelRevision": self.model_revision,
            "weightsPath": self.weights_path,
            "weightsSha256": self.weights_sha256,
        }

    @property
    def labels(self) -> dict[str, str]:
        return {
            "org.opencontainers.image.title": self.model_name,
            "org.opencontainers.image.version": self.version,
            "org.opencontainers.image.revision": self.source_revision,
            "org.opencontainers.image.source": (f"https://github.com/{self.source_repository}"),
            "ai.heartwise.model.repository": self.model_repository,
            "ai.heartwise.model.revision": self.model_revision,
            "ai.heartwise.model.weights.path": self.weights_path,
            "ai.heartwise.model.weights.sha256": self.weights_sha256,
        }

    @property
    def build_args(self) -> dict[str, str]:
        return {
            "PACS_AI_SOURCE_REPOSITORY": self.source_repository,
            "PACS_AI_SOURCE_REVISION": self.source_revision,
            "PACS_AI_MODEL_VERSION": self.version,
            "PACS_AI_MODEL_REPOSITORY": self.model_repository,
            "PACS_AI_MODEL_REVISION": self.model_revision,
            "PACS_AI_WEIGHTS_PATH": self.weights_path,
            "PACS_AI_WEIGHTS_SHA256": self.weights_sha256,
        }


def log(message: str) -> None:
    print(f"[release] {message}", flush=True)


def run_command(
    command: Sequence[str],
    *,
    check: bool = True,
    cwd: Path = REPOSITORY_ROOT,
    capture_output: bool = True,
) -> subprocess.CompletedProcess[str]:
    if not command or command[0] not in ALLOWED_EXECUTABLES:
        executable = command[0] if command else "<empty>"
        raise ReleaseError(f"Release commands cannot execute {executable!r}")

    try:
        # The executable is allowlisted above and arguments never pass through a shell.
        result = subprocess.run(  # nosec B603
            list(command),
            cwd=cwd,
            text=True,
            capture_output=capture_output,
            check=False,
            shell=False,
        )
    except OSError as exc:
        raise ReleaseError(f"Could not execute {command[0]}: {exc}") from exc
    if check and result.returncode != 0:
        detail = (result.stderr or result.stdout or "").strip()
        raise ReleaseError(detail or f"Command failed with exit code {result.returncode}")
    return result


def git_output(*arguments: str) -> str:
    return run_command(("git", *arguments)).stdout.strip()


def require_clean_worktree() -> None:
    dirty = git_output("status", "--porcelain", "--untracked-files=all")
    if dirty:
        raise ReleaseError(
            "Cannot stamp source provenance from a dirty Git worktree; "
            "commit or stash tracked and untracked changes first"
        )


def resolve_source_revision() -> str:
    revision = git_output("rev-parse", "HEAD")
    if not GIT_REVISION_PATTERN.fullmatch(revision):
        raise ReleaseError("Git HEAD did not resolve to a full lowercase commit SHA")
    return revision


def resolve_source_repository() -> str:
    remote = git_output("remote", "get-url", "origin")
    patterns = (
        r"^https://github\.com/(?P<repository>[^/]+/[^/]+?)(?:\.git)?$",
        r"^ssh://git@github\.com/(?P<repository>[^/]+/[^/]+?)(?:\.git)?$",
        r"^git@github\.com:(?P<repository>[^/]+/[^/]+?)(?:\.git)?$",
    )
    for pattern in patterns:
        match = re.fullmatch(pattern, remote)
        if match:
            return match.group("repository")
    raise ReleaseError("Git origin must identify a GitHub owner/repository")


def require_merged_source(revision: str) -> None:
    result = run_command(
        ("git", "merge-base", "--is-ancestor", revision, "origin/master"),
        check=False,
    )
    if result.returncode != 0:
        raise ReleaseError(
            "Published images must be built from a commit reachable from origin/master"
        )


def require_string(value: Any, field: str) -> str:
    if not isinstance(value, str) or not value:
        raise ReleaseError(f"{field} must be a non-empty string")
    return value


def valid_weights_path(value: str) -> bool:
    parts = value.split("/")
    return not (
        not value
        or "\\" in value
        or value.startswith("/")
        or PurePosixPath(value).as_posix() != value
        or any(part in {"", ".", ".."} for part in parts)
    )


def load_release_metadata(model_dir: Path, source_revision: str) -> ReleaseMetadata:
    manifest_path = model_dir / "data" / "model_info.json"
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        raise ReleaseError(f"Model manifest not found: {manifest_path}") from exc
    except json.JSONDecodeError as exc:
        raise ReleaseError(f"Invalid JSON in {manifest_path}: {exc}") from exc

    model_id = require_string(manifest.get("modelId"), "modelId")
    model_name = require_string(manifest.get("modelName"), "modelName")
    version = require_string(manifest.get("version"), "version")
    if not SEMVER_PATTERN.fullmatch(version):
        raise ReleaseError("version must be a semantic version such as 1.0.0")
    if not DOCKER_TAG_PATTERN.fullmatch(version):
        raise ReleaseError(
            "version must also be a valid Docker tag; SemVer build metadata (+...) "
            "is not supported"
        )

    provenance = manifest.get("provenance")
    if not isinstance(provenance, dict):
        raise ReleaseError(
            "Official releases require a complete provenance object in model_info.json"
        )
    fields = set(provenance)
    missing = sorted(PROVENANCE_FIELDS - fields)
    unknown = sorted(fields - PROVENANCE_FIELDS)
    if missing:
        raise ReleaseError(f"Missing provenance fields: {', '.join(missing)}")
    if unknown:
        raise ReleaseError(f"Unsupported provenance fields: {', '.join(unknown)}")

    source_repository = require_string(
        provenance["sourceRepository"], "provenance.sourceRepository"
    )
    model_repository = require_string(provenance["modelRepository"], "provenance.modelRepository")
    if not REPOSITORY_PATTERN.fullmatch(source_repository):
        raise ReleaseError("provenance.sourceRepository must use owner/repository")
    if not REPOSITORY_PATTERN.fullmatch(model_repository):
        raise ReleaseError("provenance.modelRepository must use owner/repository")

    declared_source_revision = provenance["sourceRevision"]
    if declared_source_revision is not None:
        if not isinstance(declared_source_revision, str) or not GIT_REVISION_PATTERN.fullmatch(
            declared_source_revision
        ):
            raise ReleaseError(
                "provenance.sourceRevision must be null or a full lowercase Git SHA"
            )
        if declared_source_revision != source_revision:
            raise ReleaseError(
                "provenance.sourceRevision does not match the checked-out Git commit"
            )

    model_revision = require_string(provenance["modelRevision"], "provenance.modelRevision")
    if not GIT_REVISION_PATTERN.fullmatch(model_revision):
        raise ReleaseError("provenance.modelRevision must be a 40-character lowercase Git SHA")

    weights_path = require_string(provenance["weightsPath"], "provenance.weightsPath")
    if not valid_weights_path(weights_path):
        raise ReleaseError(
            "provenance.weightsPath must be a normalized repository-relative POSIX path"
        )

    weights_sha256 = require_string(provenance["weightsSha256"], "provenance.weightsSha256")
    if not SHA256_PATTERN.fullmatch(weights_sha256):
        raise ReleaseError("provenance.weightsSha256 must be a 64-character lowercase SHA-256")

    return ReleaseMetadata(
        model_id=model_id,
        model_name=model_name,
        version=version,
        source_repository=source_repository,
        source_revision=source_revision,
        model_repository=model_repository,
        model_revision=model_revision,
        weights_path=weights_path,
        weights_sha256=weights_sha256,
    )


def validate_image_repository(image_repository: str, allowed_namespace: str) -> None:
    if not IMAGE_REPOSITORY_PATTERN.fullmatch(image_repository):
        raise ReleaseError(
            "Image repository must be a lowercase namespace/repository without a tag or digest"
        )
    if image_repository.split("/", maxsplit=1)[0] != allowed_namespace:
        raise ReleaseError(
            f"Image repository must use the approved {allowed_namespace}/ namespace"
        )


def validate_dockerfile_contract(model_dir: Path) -> tuple[Path, bool]:
    dockerfile = model_dir / "Dockerfile"
    if not dockerfile.is_file():
        raise ReleaseError(f"Official Dockerfile not found: {dockerfile}")
    contents = dockerfile.read_text(encoding="utf-8")

    declared_args = set(re.findall(r"(?m)^\s*ARG\s+([A-Za-z_][A-Za-z0-9_]*)(?:=.*)?$", contents))
    missing_args = sorted(PROVENANCE_BUILD_ARGS - declared_args)
    if missing_args:
        raise ReleaseError(
            "Official Dockerfile is missing provenance build arguments: "
            + ", ".join(missing_args)
        )
    if re.search(r"(?mi)^\s*(?:ARG|ENV)\s+(?:HF_TOKEN|HUGGINGFACE_TOKEN)\b", contents):
        raise ReleaseError("Hugging Face credentials must use a BuildKit secret, not ARG or ENV")
    if not re.search(
        r"(?m)^\s*ENV\s+PACS_AI_SOURCE_REVISION=[\"']?\$\{PACS_AI_SOURCE_REVISION\}",
        contents,
    ):
        raise ReleaseError(
            "Official Dockerfile must expose PACS_AI_SOURCE_REVISION to the inference runtime"
        )
    missing_downloader_arguments = [
        option
        for option, build_argument in DOWNLOADER_ARGUMENTS.items()
        if not re.search(
            rf"{re.escape(option)}\s+[\"']?\$\{{{build_argument}\}}[\"']?",
            contents,
        )
    ]
    if missing_downloader_arguments:
        raise ReleaseError(
            "Official Dockerfile must pass release metadata to the model downloader: "
            + ", ".join(missing_downloader_arguments)
        )
    if not re.search(r"(?m)^\s*COPY\s+download_model\.py\s+\.?/?\s*$", contents):
        raise ReleaseError(
            "Official Dockerfile must retain download_model.py for packaged-weight verification"
        )

    uses_hf_secret = "mount=type=secret,id=hf_token" in contents
    if uses_hf_secret:
        dockerignore = model_dir / ".dockerignore"
        if not dockerignore.is_file() or "hf_token" not in dockerignore.read_text(
            encoding="utf-8"
        ):
            raise ReleaseError(
                "Models using the hf_token secret must exclude token files in .dockerignore"
            )
    return dockerfile, uses_hf_secret


def remote_version_exists(image: str) -> bool:
    result = run_command(("docker", "manifest", "inspect", image), check=False)
    if result.returncode == 0:
        return True
    detail = f"{result.stdout}\n{result.stderr}".lower()
    if any(
        marker in detail
        for marker in ("manifest unknown", "no such manifest", "not found", "404")
    ):
        return False
    raise ReleaseError(
        "Could not verify whether the versioned image already exists in the registry: "
        + (result.stderr or result.stdout).strip()
    )


def build_image(
    model_dir: Path,
    dockerfile: Path,
    image: str,
    metadata: ReleaseMetadata,
    hf_token_file: Path | None,
) -> None:
    command = ["docker", "build", "--file", str(dockerfile), "--tag", image]
    for name, value in sorted(metadata.build_args.items()):
        command.extend(("--build-arg", f"{name}={value}"))
    for name, value in sorted(metadata.labels.items()):
        command.extend(("--label", f"{name}={value}"))
    if hf_token_file is not None:
        command.extend(("--secret", f"id=hf_token,src={hf_token_file}"))
    command.append(str(model_dir))

    try:
        dockerfile_label = dockerfile.relative_to(REPOSITORY_ROOT)
    except ValueError:
        dockerfile_label = dockerfile
    log(f"Building {image} from {dockerfile_label}")
    run_command(command, capture_output=False)


def inspect_image(image: str) -> tuple[str, dict[str, str], list[str]]:
    result = run_command(("docker", "image", "inspect", image))
    try:
        inspections = json.loads(result.stdout)
        inspection = inspections[0]
        image_id = inspection["Id"]
        labels = inspection.get("Config", {}).get("Labels") or {}
        repo_digests = inspection.get("RepoDigests") or []
    except (json.JSONDecodeError, IndexError, KeyError, TypeError) as exc:
        raise ReleaseError(f"Unexpected docker image inspect response for {image}") from exc
    if not isinstance(image_id, str) or not DIGEST_PATTERN.fullmatch(image_id):
        raise ReleaseError(f"Docker returned an invalid local image ID for {image}")
    if not isinstance(labels, dict) or not all(
        isinstance(key, str) and isinstance(value, str) for key, value in labels.items()
    ):
        raise ReleaseError(f"Docker returned invalid image labels for {image}")
    return image_id, labels, repo_digests


def verify_image_labels(actual: dict[str, str], metadata: ReleaseMetadata) -> None:
    mismatches = [
        f"{name}: expected {expected!r}, received {actual.get(name)!r}"
        for name, expected in metadata.labels.items()
        if actual.get(name) != expected
    ]
    if mismatches:
        raise ReleaseError("Image label verification failed: " + "; ".join(mismatches))


def verify_packaged_weights(image: str, metadata: ReleaseMetadata) -> None:
    log("Verifying the checkpoint packaged inside the image")
    run_command(
        (
            "docker",
            "run",
            "--rm",
            "--entrypoint",
            "python",
            image,
            "/app/download_model.py",
            "--verify-only",
            "--repository",
            metadata.model_repository,
            "--revision",
            metadata.model_revision,
            "--weights-path",
            metadata.weights_path,
            "--weights-sha256",
            metadata.weights_sha256,
            "--output-dir",
            "/app/models",
        )
    )


def fetch_runtime_model_info(port: int) -> dict[str, Any]:
    url = f"http://127.0.0.1:{port}/api/inference/model-info"
    try:
        with urllib.request.urlopen(url, timeout=5) as response:
            payload = json.load(response)
    except (json.JSONDecodeError, UnicodeDecodeError) as exc:
        raise ReleaseError("/inference/model-info did not return valid JSON") from exc
    if not isinstance(payload, dict) or not isinstance(payload.get("data"), dict):
        raise ReleaseError("/inference/model-info returned an unexpected response")
    return payload["data"]


def verify_runtime_payload(payload: dict[str, Any], metadata: ReleaseMetadata) -> None:
    if payload.get("modelId") != metadata.model_id:
        raise ReleaseError("Runtime modelId does not match model_info.json")
    if payload.get("version") != metadata.version:
        raise ReleaseError("Runtime model version does not match model_info.json")
    if payload.get("provenance") != metadata.provenance:
        raise ReleaseError("Runtime provenance does not match the release metadata")


def verify_runtime_image(image: str, metadata: ReleaseMetadata, timeout_seconds: int) -> None:
    container_name = f"pacs-ai-release-check-{uuid.uuid4().hex[:12]}"
    log("Starting a temporary container for /inference/model-info verification")
    run_command(
        (
            "docker",
            "run",
            "--detach",
            "--rm",
            "--name",
            container_name,
            "--publish",
            "127.0.0.1::80",
            image,
        )
    )
    try:
        port_result = run_command(("docker", "port", container_name, "80/tcp"))
        endpoint = port_result.stdout.strip().splitlines()[-1]
        try:
            port = int(endpoint.rsplit(":", maxsplit=1)[1])
        except (IndexError, ValueError) as exc:
            raise ReleaseError(
                f"Could not determine the temporary container port from {endpoint!r}"
            ) from exc

        deadline = time.monotonic() + timeout_seconds
        last_error = "container did not answer"
        while time.monotonic() < deadline:
            try:
                payload = fetch_runtime_model_info(port)
                verify_runtime_payload(payload, metadata)
                log("Runtime model-info matches the release metadata")
                return
            except (
                ReleaseError,
                urllib.error.URLError,
                ConnectionError,
                TimeoutError,
            ) as exc:
                last_error = str(exc)
                time.sleep(2)
        logs = run_command(("docker", "logs", container_name), check=False)
        detail = (logs.stderr or logs.stdout).strip()
        raise ReleaseError(
            f"Runtime model-info verification timed out: {last_error}"
            + (f"; container logs: {detail}" if detail else "")
        )
    finally:
        run_command(("docker", "rm", "--force", container_name), check=False)


def resolve_pushed_digest(
    image_repository: str,
    push_output: str,
    repo_digests: Sequence[str],
) -> str:
    digest_match = re.search(r"digest:\s*(sha256:[0-9a-f]{64})", push_output)
    if digest_match:
        return digest_match.group(1)
    prefix = f"{image_repository}@"
    for repo_digest in repo_digests:
        if repo_digest.startswith(prefix):
            digest = repo_digest.removeprefix(prefix)
            if DIGEST_PATTERN.fullmatch(digest):
                return digest
    raise ReleaseError("Docker push completed without a resolvable registry digest")


def publish_image(
    image: str,
    image_repository: str,
    publish_latest: bool,
) -> tuple[str, list[str]]:
    log(f"Pushing immutable version tag {image}")
    push_result = run_command(("docker", "push", image))
    _, _, repo_digests = inspect_image(image)
    digest = resolve_pushed_digest(
        image_repository,
        f"{push_result.stdout}\n{push_result.stderr}",
        repo_digests,
    )
    run_command(("docker", "manifest", "inspect", f"{image_repository}@{digest}"))

    aliases: list[str] = []
    if publish_latest:
        latest = f"{image_repository}:latest"
        log("Publishing optional latest alias; it is not release identity")
        run_command(("docker", "tag", image, latest))
        run_command(("docker", "push", latest))
        aliases.append(latest)
    return digest, aliases


def build_evidence(
    *,
    metadata: ReleaseMetadata,
    image: str,
    image_id: str,
    digest: str | None,
    aliases: Sequence[str],
) -> dict[str, Any]:
    return {
        "schemaVersion": 1,
        "createdAt": datetime.now(timezone.utc).isoformat(),
        "status": "published" if digest else "built",
        "model": {
            "id": metadata.model_id,
            "name": metadata.model_name,
            "version": metadata.version,
        },
        "source": {
            "repository": metadata.source_repository,
            "revision": metadata.source_revision,
        },
        "weights": {
            "repository": metadata.model_repository,
            "revision": metadata.model_revision,
            "path": metadata.weights_path,
            "sha256": metadata.weights_sha256,
        },
        "image": {
            "reference": image,
            "localId": image_id,
            "registryDigest": digest,
            "immutableReference": (
                f"{image.split(':', maxsplit=1)[0]}@{digest}" if digest else None
            ),
            "aliases": list(aliases),
        },
        "automation": {
            "actor": os.getenv("GITHUB_ACTOR"),
            "repository": os.getenv("GITHUB_REPOSITORY"),
            "runId": os.getenv("GITHUB_RUN_ID"),
            "runAttempt": os.getenv("GITHUB_RUN_ATTEMPT"),
        },
    }


def write_evidence(path: Path, evidence: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(evidence, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    temporary.replace(path)
    log(f"Release evidence written to {path}")


def release(args: argparse.Namespace) -> dict[str, Any]:
    model_dir = Path(args.model_dir).resolve()
    try:
        model_dir.relative_to(REPOSITORY_ROOT / "model-examples")
    except ValueError as exc:
        raise ReleaseError("Model directory must be inside model-examples/") from exc

    require_clean_worktree()
    source_revision = resolve_source_revision()
    if args.publish:
        require_merged_source(source_revision)

    metadata = load_release_metadata(model_dir, source_revision)
    source_repository = resolve_source_repository()
    if metadata.source_repository != source_repository:
        raise ReleaseError("provenance.sourceRepository does not match the Git origin repository")
    image_repository = args.image_repository
    validate_image_repository(image_repository, args.allowed_namespace)
    image = f"{image_repository}:{metadata.version}"
    dockerfile, uses_hf_secret = validate_dockerfile_contract(model_dir)

    hf_token_file: Path | None = None
    if uses_hf_secret:
        if not args.hf_token_file:
            raise ReleaseError(
                "This model requires --hf-token-file or the HF_TOKEN_FILE environment variable"
            )
        hf_token_file = Path(args.hf_token_file).resolve()
        if not hf_token_file.is_file():
            raise ReleaseError(f"Hugging Face token file not found: {hf_token_file}")

    if args.publish and remote_version_exists(image):
        raise ReleaseError(f"Refusing to overwrite existing immutable versioned image {image}")

    log(f"Validated {metadata.model_name} {metadata.version} at {source_revision}")
    build_image(model_dir, dockerfile, image, metadata, hf_token_file)
    image_id, labels, _ = inspect_image(image)
    verify_image_labels(labels, metadata)
    log(f"Image labels verified ({image_id})")
    verify_packaged_weights(image, metadata)
    verify_runtime_image(image, metadata, args.runtime_timeout_seconds)

    digest: str | None = None
    aliases: list[str] = []
    if args.publish:
        digest, aliases = publish_image(
            image,
            image_repository,
            args.publish_latest,
        )
        log(f"Published immutable image {image_repository}@{digest}")

    evidence = build_evidence(
        metadata=metadata,
        image=image,
        image_id=image_id,
        digest=digest,
        aliases=aliases,
    )
    write_evidence(Path(args.evidence).resolve(), evidence)
    return evidence


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Build and optionally publish an immutable, provenance-aware PACS-AI model image"
        )
    )
    parser.add_argument("model_dir", help="Model directory under model-examples/")
    parser.add_argument(
        "--image-repository",
        default=os.getenv("PACS_AI_IMAGE_REPOSITORY"),
        help="Lowercase Docker repository without tag (or PACS_AI_IMAGE_REPOSITORY)",
    )
    parser.add_argument(
        "--allowed-namespace",
        default=os.getenv("PACS_AI_RELEASE_NAMESPACE", "heartwisehub"),
        help="Registry namespace allowed for publication (default: heartwisehub)",
    )
    parser.add_argument(
        "--hf-token-file",
        default=os.getenv("HF_TOKEN_FILE"),
        help="Protected Hugging Face token file mounted as a BuildKit secret",
    )
    parser.add_argument(
        "--publish",
        action="store_true",
        help="Push the immutable version tag after build verification",
    )
    parser.add_argument(
        "--publish-latest",
        action="store_true",
        help="Also push latest as a non-authoritative convenience alias",
    )
    parser.add_argument(
        "--evidence",
        default="release-evidence.json",
        help="Path for machine-readable release evidence",
    )
    parser.add_argument(
        "--runtime-timeout-seconds",
        type=int,
        default=120,
        help="Timeout for temporary container model-info verification",
    )
    args = parser.parse_args(argv)
    if not args.image_repository:
        parser.error("--image-repository or PACS_AI_IMAGE_REPOSITORY is required")
    if args.publish_latest and not args.publish:
        parser.error("--publish-latest requires --publish")
    if args.runtime_timeout_seconds <= 0:
        parser.error("--runtime-timeout-seconds must be greater than zero")
    return args


def main(argv: Sequence[str] | None = None) -> int:
    try:
        release(parse_args(argv))
    except ReleaseError as exc:
        print(f"[release] ERROR: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
