#!/usr/bin/env python3
"""Validate that catalog recovery evidence came from a trusted model release run."""

from __future__ import annotations

import argparse
import json
import os
import re
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Callable, Mapping, Sequence
from pathlib import Path
from typing import Any

try:
    from scripts.model_image_repository import (
        DEFAULT_MAPPING_PATH,
        ModelImageRepositoryError,
        resolve_image_repository,
    )
except ModuleNotFoundError:  # Direct execution adds scripts/, not its parent, to sys.path.
    from model_image_repository import (
        DEFAULT_MAPPING_PATH,
        ModelImageRepositoryError,
        resolve_image_repository,
    )


REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
TRUSTED_WORKFLOW_PATH = ".github/workflows/publish-model-image.yml"
GIT_REVISION_PATTERN = re.compile(r"^[0-9a-f]{40}$")
DIGEST_PATTERN = re.compile(r"^sha256:[0-9a-f]{64}$")
REPOSITORY_PATTERN = re.compile(
    r"^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$"
)


class CatalogRecoveryValidationError(RuntimeError):
    """Recovery evidence or its source workflow run is not trusted."""


def load_json_object(path: Path, label: str) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        raise CatalogRecoveryValidationError(f"{label} not found: {path}") from exc
    except json.JSONDecodeError as exc:
        raise CatalogRecoveryValidationError(f"{label} is invalid JSON: {exc}") from exc
    if not isinstance(value, dict):
        raise CatalogRecoveryValidationError(f"{label} must be a JSON object")
    return value


def require_mapping(value: Any, field: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise CatalogRecoveryValidationError(f"{field} must be an object")
    return value


def require_equal(actual: Any, expected: Any, field: str) -> None:
    if actual != expected:
        raise CatalogRecoveryValidationError(f"{field} does not match the approved release")


def fetch_source_run(
    repository: str,
    run_id: int,
    token: str,
    *,
    open_request: Callable[..., Any] = urllib.request.urlopen,
) -> dict[str, Any]:
    if not REPOSITORY_PATTERN.fullmatch(repository):
        raise CatalogRecoveryValidationError("GitHub repository must be owner/name")
    if run_id <= 0 or not token:
        raise CatalogRecoveryValidationError("A positive run ID and GitHub token are required")
    owner, name = repository.split("/", maxsplit=1)
    endpoint = (
        "https://api.github.com/repos/"
        f"{urllib.parse.quote(owner, safe='')}/{urllib.parse.quote(name, safe='')}"
        f"/actions/runs/{run_id}"
    )
    request = urllib.request.Request(
        endpoint,
        headers={
            "Accept": "application/vnd.github+json",
            "Authorization": f"Bearer {token}",
            "User-Agent": "PACS-AI-model-release/1.0",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    try:
        with open_request(request, timeout=30) as response:
            payload = json.load(response)
    except urllib.error.HTTPError as exc:
        raise CatalogRecoveryValidationError(
            f"GitHub returned HTTP {exc.code} while resolving the source run"
        ) from exc
    except (urllib.error.URLError, TimeoutError) as exc:
        raise CatalogRecoveryValidationError(f"Could not resolve the source run: {exc}") from exc
    if not isinstance(payload, dict):
        raise CatalogRecoveryValidationError("GitHub returned invalid source run metadata")
    return payload


def validate_source_run(
    run: Mapping[str, Any],
    *,
    repository: str,
    run_id: int,
    run_attempt: int,
) -> str:
    run_repository = require_mapping(run.get("repository"), "run.repository")
    require_equal(run.get("id"), run_id, "run.id")
    require_equal(run.get("run_attempt"), run_attempt, "run.run_attempt")
    require_equal(run_repository.get("full_name"), repository, "run.repository.full_name")
    require_equal(run.get("path"), TRUSTED_WORKFLOW_PATH, "run.path")
    require_equal(run.get("event"), "workflow_dispatch", "run.event")
    require_equal(run.get("head_branch"), "master", "run.head_branch")
    require_equal(run.get("status"), "completed", "run.status")
    if run.get("conclusion") not in {"success", "failure"}:
        raise CatalogRecoveryValidationError("run.conclusion is not recoverable")
    source_revision = run.get("head_sha")
    if not isinstance(source_revision, str) or not GIT_REVISION_PATTERN.fullmatch(source_revision):
        raise CatalogRecoveryValidationError("run.head_sha must be a full lowercase Git SHA")
    return source_revision


def validate_evidence(
    evidence_path: Path,
    model_directory: Path,
    *,
    mapping_path: Path,
    repository: str,
    run_id: int,
    run_attempt: int,
    source_revision: str,
) -> None:
    evidence = load_json_object(evidence_path, "Release evidence")
    model_info = load_json_object(model_directory / "data" / "model_info.json", "Model info")
    try:
        image_repository = resolve_image_repository(
            model_directory,
            mapping_path=mapping_path,
            repository_root=REPOSITORY_ROOT,
        )
    except ModelImageRepositoryError as exc:
        raise CatalogRecoveryValidationError(str(exc)) from exc

    automation = require_mapping(evidence.get("automation"), "evidence.automation")
    model = require_mapping(evidence.get("model"), "evidence.model")
    release = require_mapping(evidence.get("release"), "evidence.release")
    source = require_mapping(evidence.get("source"), "evidence.source")
    weights = require_mapping(evidence.get("weights"), "evidence.weights")
    image = require_mapping(evidence.get("image"), "evidence.image")
    provenance = require_mapping(model_info.get("provenance"), "modelInfo.provenance")

    require_equal(evidence.get("status"), "published", "evidence.status")
    require_equal(automation.get("repository"), repository, "evidence.automation.repository")
    require_equal(automation.get("runId"), str(run_id), "evidence.automation.runId")
    require_equal(automation.get("runAttempt"), str(run_attempt), "evidence.automation.runAttempt")
    require_equal(release.get("channel"), "RELEASE", "evidence.release.channel")
    require_equal(release.get("workflowRun"), str(run_id), "evidence.release.workflowRun")
    require_equal(model.get("id"), model_info.get("modelId"), "evidence.model.id")
    require_equal(model.get("name"), model_info.get("modelName"), "evidence.model.name")
    require_equal(model.get("version"), model_info.get("version"), "evidence.model.version")
    require_equal(release.get("baseVersion"), model_info.get("version"), "evidence.release.baseVersion")
    require_equal(source.get("repository"), repository, "evidence.source.repository")
    require_equal(source.get("repository"), provenance.get("sourceRepository"), "modelInfo.provenance.sourceRepository")
    require_equal(source.get("revision"), source_revision, "evidence.source.revision")

    for evidence_key, provenance_key in (
        ("repository", "modelRepository"),
        ("revision", "modelRevision"),
        ("path", "weightsPath"),
        ("sha256", "weightsSha256"),
    ):
        require_equal(
            weights.get(evidence_key),
            provenance.get(provenance_key),
            f"evidence.weights.{evidence_key}",
        )

    version = model_info.get("version")
    expected_reference = f"{image_repository}:{version}"
    digest = image.get("registryDigest")
    if not isinstance(digest, str) or not DIGEST_PATTERN.fullmatch(digest):
        raise CatalogRecoveryValidationError("evidence.image.registryDigest is invalid")
    require_equal(image.get("reference"), expected_reference, "evidence.image.reference")
    require_equal(
        image.get("immutableReference"),
        f"{image_repository}@{digest}",
        "evidence.image.immutableReference",
    )


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("evidence", help="Downloaded model release evidence JSON")
    parser.add_argument("model_directory", help="Selected model directory under model-examples/")
    parser.add_argument("--repository", required=True, help="Expected GitHub owner/repository")
    parser.add_argument("--run-id", required=True, type=int, help="Source workflow run ID")
    parser.add_argument("--run-attempt", required=True, type=int, help="Source workflow attempt")
    parser.add_argument(
        "--repository-map",
        default=str(DEFAULT_MAPPING_PATH),
        help="Approved Docker repository mapping",
    )
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        token = os.environ.get("GITHUB_TOKEN", "").strip()
        run = fetch_source_run(args.repository, args.run_id, token)
        source_revision = validate_source_run(
            run,
            repository=args.repository,
            run_id=args.run_id,
            run_attempt=args.run_attempt,
        )
        validate_evidence(
            Path(args.evidence).resolve(),
            Path(args.model_directory).resolve(),
            mapping_path=Path(args.repository_map).resolve(),
            repository=args.repository,
            run_id=args.run_id,
            run_attempt=args.run_attempt,
            source_revision=source_revision,
        )
    except CatalogRecoveryValidationError as exc:
        print(f"[catalog-recovery] {exc}")
        return 1
    print(f"[catalog-recovery] Trusted release evidence from run {args.run_id} attempt {args.run_attempt}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
