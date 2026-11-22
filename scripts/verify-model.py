#!/usr/bin/env python3
"""Verify PACS-AI model source contracts and immutable Docker images."""

from __future__ import annotations

import argparse
import importlib.util
import json
import re
import shutil
import subprocess
import sys
from collections.abc import Callable, Iterable, Sequence
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

try:
    from scripts.model_image_repository import (
        DEFAULT_MAPPING_PATH,
        ModelImageRepositoryError,
        load_repository_mapping,
        resolve_image_repository,
    )
except ModuleNotFoundError:  # Direct execution adds scripts/, not its parent, to sys.path.
    from model_image_repository import (
        DEFAULT_MAPPING_PATH,
        ModelImageRepositoryError,
        load_repository_mapping,
        resolve_image_repository,
    )


REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
MODEL_ROOT = REPOSITORY_ROOT / "model-examples"
TEMPLATE_ROOT = REPOSITORY_ROOT / "model-template"
RELEASE_SCRIPT = REPOSITORY_ROOT / "scripts" / "release-model.py"
IMMUTABLE_IMAGE_PATTERN = re.compile(
    r"^(?P<repository>[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)+)"
    r"@(?P<digest>sha256:[0-9a-f]{64})$"
)
REQUIRED_OPENAPI_PATHS = frozenset(
    {
        "/inference/predict",
        "/inference/model-info",
        "/inference/model-facts",
        "/inference/model/runtime",
        "/inference/model/load",
        "/inference/model/unload",
    }
)
SHARED_CONTRACT_PATHS = frozenset(
    {
        ".claude/skills/pacs-ai-model-verification/SKILL.md",
        ".github/model-image-repositories.json",
        ".github/workflows/model-verification.yml",
        "AGENTS.md",
        "scripts/model_image_repository.py",
        "scripts/release-model.py",
        "scripts/tests/test_verify_model.py",
        "scripts/verify-model.py",
    }
)
ALLOWED_OUTPUT_MODES = frozenset({"HTML", "JSON", "OHIF_ANNOTATIONS", "PDF", "WEB_APP"})


class VerificationError(RuntimeError):
    """A model verification contract was violated."""


class SkipCheck(RuntimeError):
    """A verification check does not apply or lacks an optional prerequisite."""


def load_release_module():
    module_name = "pacs_ai_release_model"
    spec = importlib.util.spec_from_file_location(module_name, RELEASE_SCRIPT)
    if spec is None or spec.loader is None:
        raise VerificationError(f"Unable to load {RELEASE_SCRIPT}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[module_name] = module
    scripts_path = str(RELEASE_SCRIPT.parent)
    inserted = scripts_path not in sys.path
    if inserted:
        sys.path.insert(0, scripts_path)
    try:
        spec.loader.exec_module(module)
    finally:
        if inserted:
            sys.path.remove(scripts_path)
    return module


RELEASE = load_release_module()


def utc_now() -> str:
    return datetime.now(tz=timezone.utc).isoformat().replace("+00:00", "Z")


def run_command(
    command: Sequence[str], *, check: bool = True
) -> subprocess.CompletedProcess[str]:
    try:
        result = subprocess.run(
            command,
            cwd=REPOSITORY_ROOT,
            check=False,
            capture_output=True,
            text=True,
        )
    except OSError as exc:
        raise VerificationError(f"Unable to run {command[0]}: {exc}") from exc
    if check and result.returncode != 0:
        detail = (result.stderr or result.stdout).strip()
        raise VerificationError(f"{' '.join(command)} failed: {detail}")
    return result


def git_revision() -> str:
    return run_command(("git", "rev-parse", "HEAD")).stdout.strip()


def require(condition: bool, message: str) -> None:
    if not condition:
        raise VerificationError(message)


def load_json_object(path: Path) -> dict[str, Any]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        raise VerificationError(f"Required file is missing: {path}") from exc
    except json.JSONDecodeError as exc:
        raise VerificationError(f"Invalid JSON in {path}: {exc}") from exc
    if not isinstance(payload, dict):
        raise VerificationError(f"Expected a JSON object in {path}")
    return payload


def model_directory(path: str | Path) -> Path:
    candidate = Path(path)
    if not candidate.is_absolute():
        candidate = REPOSITORY_ROOT / candidate
    candidate = candidate.resolve()
    try:
        candidate.relative_to(MODEL_ROOT)
    except ValueError as exc:
        raise VerificationError("Model directory must be under model-examples/") from exc
    require(candidate.is_dir(), f"Model directory does not exist: {candidate}")
    return candidate


def model_directory_for_changed_path(path: str, root: Path = REPOSITORY_ROOT) -> Path | None:
    candidate = (root / path).resolve()
    model_root = (root / "model-examples").resolve()
    try:
        candidate.relative_to(model_root)
    except ValueError:
        return None
    current = candidate if candidate.is_dir() else candidate.parent
    while current != model_root:
        if (current / "Dockerfile").is_file() and (
            current / "data" / "model_info.json"
        ).is_file():
            return current
        current = current.parent
    return None


def changed_model_directories(base_revision: str) -> list[Path]:
    result = run_command(
        ("git", "diff", "--name-only", "--diff-filter=ACMR", f"{base_revision}...HEAD")
    )
    changed_paths = [line for line in result.stdout.splitlines() if line]
    shared_changed = any(
        path in SHARED_CONTRACT_PATHS or path.startswith("model-template/")
        for path in changed_paths
    )
    if shared_changed:
        mapping = load_repository_mapping(DEFAULT_MAPPING_PATH)
        return sorted((REPOSITORY_ROOT / path).resolve() for path in mapping)
    directories = {
        directory
        for path in changed_paths
        if (directory := model_directory_for_changed_path(path)) is not None
    }
    return sorted(directories)


def check_result(name: str, operation: Callable[[], str | None]) -> dict[str, str]:
    try:
        detail = operation() or "Contract satisfied"
        return {"name": name, "status": "PASS", "detail": detail}
    except SkipCheck as exc:
        return {"name": name, "status": "SKIP", "detail": str(exc)}
    except (VerificationError, ModelImageRepositoryError, RELEASE.ReleaseError) as exc:
        return {"name": name, "status": "FAIL", "detail": str(exc)}
    except Exception as exc:  # Keep evidence when an unexpected verifier error occurs.
        return {"name": name, "status": "FAIL", "detail": f"Unexpected error: {exc}"}


def validate_required_files(directory: Path) -> str:
    required = (
        "Dockerfile",
        "logic.py",
        "main.py",
        "requirements.txt",
        "nginx.conf",
        "supervisord.conf",
        "data/model_info.json",
        "data/model_facts.json",
        "docs/openapi.json",
        "utils/model_lifecycle.py",
        "utils/resource_config.py",
    )
    missing = [relative for relative in required if not (directory / relative).is_file()]
    require(not missing, "Missing required files: " + ", ".join(missing))
    return f"Found {len(required)} required model files"


def validate_manifest(directory: Path) -> str:
    manifest = load_json_object(directory / "data" / "model_info.json")
    for field in ("modelId", "modelName", "version", "dicomTargetLevel"):
        require(
            isinstance(manifest.get(field), str) and bool(manifest[field]),
            f"{field} must be a non-empty string",
        )

    resources = manifest.get("resources")
    require(isinstance(resources, dict), "resources must be an object")
    required_resources = {
        "gpuRequired",
        "residentMemoryMiB",
        "peakMemoryMiB",
        "maxConcurrentInferences",
        "idleTimeoutSeconds",
    }
    require(set(resources) == required_resources, "resources must contain exactly the V1 fields")
    require(type(resources["gpuRequired"]) is bool, "resources.gpuRequired must be a boolean")
    for field in required_resources - {"gpuRequired"}:
        require(type(resources[field]) is int, f"resources.{field} must be an integer")
    require(
        resources["maxConcurrentInferences"] == 1,
        "resources.maxConcurrentInferences must equal 1",
    )
    require(resources["idleTimeoutSeconds"] > 0, "resources.idleTimeoutSeconds must be positive")
    if resources["gpuRequired"]:
        require(resources["residentMemoryMiB"] > 0, "GPU resident memory must be positive")
        require(
            resources["peakMemoryMiB"] >= resources["residentMemoryMiB"],
            "GPU peak memory must be at least resident memory",
        )
    else:
        require(
            resources["residentMemoryMiB"] == resources["peakMemoryMiB"] == 0,
            "CPU models must declare zero GPU memory",
        )

    minimum = manifest.get("dicomUploadMin")
    maximum = manifest.get("dicomUploadMax")
    require(type(minimum) is int and minimum > 0, "dicomUploadMin must be a positive integer")
    require(
        type(maximum) is int and maximum >= minimum,
        "dicomUploadMax must be an integer no smaller than dicomUploadMin",
    )
    modalities = manifest.get("supportedDicomModalities")
    require(
        isinstance(modalities, list)
        and bool(modalities)
        and all(isinstance(item, str) and item for item in modalities),
        "supportedDicomModalities must be a non-empty string list",
    )
    output_modes = manifest.get("supportedOutputModes")
    require(
        isinstance(output_modes, list) and bool(output_modes),
        "supportedOutputModes must be a non-empty list",
    )
    unknown_modes = sorted(set(output_modes) - ALLOWED_OUTPUT_MODES)
    require(not unknown_modes, "Unsupported output modes: " + ", ".join(unknown_modes))
    require(
        isinstance(manifest.get("supportedAdditionalMetadata"), list),
        "supportedAdditionalMetadata must be a list",
    )
    return f"Validated {manifest['modelId']} {manifest['version']}"


def validate_repository_mapping(directory: Path) -> str:
    repository = resolve_image_repository(directory)
    return f"Approved image repository: {repository}"


def validate_release_contract(directory: Path, source_revision: str) -> str:
    manifest = load_json_object(directory / "data" / "model_info.json")
    if "provenance" not in manifest:
        raise SkipCheck("Legacy model has no provenance contract")
    metadata = RELEASE.load_release_metadata(directory, source_revision)
    _, uses_hf_secret = RELEASE.validate_dockerfile_contract(directory)
    return (
        f"Validated provenance for {metadata.model_repository}@{metadata.model_revision}; "
        f"hf_token secret required={str(uses_hf_secret).lower()}"
    )


def validate_shared_helpers(directory: Path) -> str:
    for relative in ("utils/model_lifecycle.py", "utils/resource_config.py"):
        expected = (TEMPLATE_ROOT / relative).read_bytes()
        actual = (directory / relative).read_bytes()
        require(actual == expected, f"{relative} differs from the canonical model template")
    return "Lifecycle and resource helpers match the model template"


def validate_openapi(directory: Path) -> str:
    openapi = load_json_object(directory / "docs" / "openapi.json")
    paths = openapi.get("paths")
    require(isinstance(paths, dict), "OpenAPI paths must be an object")
    missing = sorted(REQUIRED_OPENAPI_PATHS - set(paths))
    require(not missing, "OpenAPI is missing endpoints: " + ", ".join(missing))
    return f"OpenAPI declares all {len(REQUIRED_OPENAPI_PATHS)} required endpoints"


def validate_model_artifacts(directory: Path) -> str:
    forbidden_credentials = [
        path.relative_to(directory).as_posix()
        for path in directory.rglob("*")
        if path.is_file() and path.name in {".env", "hf_token.txt"}
    ]
    require(
        not forbidden_credentials,
        "Credential-like files are present: " + ", ".join(forbidden_credentials),
    )
    manifest = load_json_object(directory / "data" / "model_info.json")
    if "provenance" in manifest:
        packaged_weights = [
            path.relative_to(directory).as_posix() for path in directory.rglob("*.pt")
        ]
        require(
            not packaged_weights,
            "Provenance-aware source must not commit checkpoints: " + ", ".join(packaged_weights),
        )
    return "No credential files or prohibited packaged checkpoints found"


def validate_model_tests(directory: Path, run_tests: bool) -> str:
    tests = sorted((directory / "tests").glob("test_*.py"))
    manifest = load_json_object(directory / "data" / "model_info.json")
    if not tests:
        if "provenance" in manifest:
            raise VerificationError("Provenance-aware model has no model-specific tests")
        raise SkipCheck("Legacy model has no model-specific tests")
    if not run_tests:
        raise SkipCheck(f"Found {len(tests)} model tests; execution requires --run-model-tests")
    if shutil.which("pytest") is None:
        raise VerificationError("pytest is required to execute model-specific tests")
    result = run_command((sys.executable, "-m", "pytest", str(directory / "tests"), "-q"))
    summary = (result.stdout or result.stderr).strip().splitlines()
    return summary[-1] if summary else f"Executed {len(tests)} model tests"


def source_model_result(directory: Path, source_revision: str, run_tests: bool) -> dict[str, Any]:
    manifest_path = directory / "data" / "model_info.json"
    manifest = load_json_object(manifest_path) if manifest_path.is_file() else {}
    checks = [
        check_result("required-files", lambda: validate_required_files(directory)),
        check_result("manifest", lambda: validate_manifest(directory)),
        check_result("image-repository", lambda: validate_repository_mapping(directory)),
        check_result(
            "release-contract", lambda: validate_release_contract(directory, source_revision)
        ),
        check_result("shared-helpers", lambda: validate_shared_helpers(directory)),
        check_result("openapi", lambda: validate_openapi(directory)),
        check_result("source-artifacts", lambda: validate_model_artifacts(directory)),
        check_result("model-tests", lambda: validate_model_tests(directory, run_tests)),
    ]
    return {
        "modelDirectory": directory.relative_to(REPOSITORY_ROOT).as_posix(),
        "modelId": manifest.get("modelId"),
        "modelName": manifest.get("modelName"),
        "version": manifest.get("version"),
        "overall": overall_status(checks),
        "checks": checks,
    }


def validate_image_reference(image: str) -> tuple[str, str]:
    match = IMMUTABLE_IMAGE_PATTERN.fullmatch(image)
    if match is None:
        raise VerificationError(
            "Image must use an immutable repository@sha256:"
            "<64 lowercase hex characters> reference"
        )
    return match.group("repository"), match.group("digest")


def overall_status(checks: Iterable[dict[str, str]]) -> str:
    statuses = {check["status"] for check in checks}
    if "FAIL" in statuses:
        return "FAIL"
    if "SKIP" in statuses:
        return "PASS_WITH_SKIPS"
    return "PASS"


def image_evidence(directory: Path, image: str, timeout_seconds: int) -> dict[str, Any]:
    state: dict[str, Any] = {}

    def check_reference() -> str:
        repository, digest = validate_image_reference(image)
        expected_repository = resolve_image_repository(directory)
        require(
            repository == expected_repository,
            f"Image repository {repository} does not match approved {expected_repository}",
        )
        state["digest"] = digest
        return f"Immutable image reference uses approved repository and {digest}"

    def pull_image() -> str:
        require("digest" in state, "Immutable image validation must pass before Docker pull")
        require(shutil.which("docker") is not None, "docker is required for image verification")
        run_command(("docker", "pull", image))
        state["pulled"] = True
        return "Image pulled successfully"

    def inspect_labels() -> str:
        require(state.get("pulled") is True, "Docker pull must pass before label verification")
        _, labels, _ = RELEASE.inspect_image(image)
        source_revision = labels.get("org.opencontainers.image.revision")
        require(
            isinstance(source_revision, str)
            and RELEASE.GIT_REVISION_PATTERN.fullmatch(source_revision) is not None,
            "Image has no valid org.opencontainers.image.revision label",
        )
        metadata = RELEASE.load_release_metadata(directory, source_revision)
        RELEASE.verify_image_labels(labels, metadata)
        ancestry = run_command(
            ("git", "merge-base", "--is-ancestor", source_revision, "origin/master"),
            check=False,
        )
        require(
            ancestry.returncode == 0,
            "Image source revision is not reachable from origin/master",
        )
        state["metadata"] = metadata
        state["labels"] = labels
        return f"OCI labels match source revision {source_revision}"

    def verify_weights() -> str:
        metadata = state.get("metadata")
        require(metadata is not None, "Image labels must verify before checkpoint verification")
        RELEASE.verify_packaged_weights(image, metadata)
        return f"Packaged checkpoint matches {metadata.weights_sha256}"

    def verify_runtime() -> str:
        metadata = state.get("metadata")
        require(metadata is not None, "Image labels must verify before runtime verification")
        RELEASE.verify_runtime_image(image, metadata, timeout_seconds)
        return "Runtime model-info matches the source manifest and provenance"

    checks = [
        check_result("immutable-image", check_reference),
        check_result("docker-pull", pull_image),
        check_result("oci-labels", inspect_labels),
        check_result("checkpoint-sha256", verify_weights),
        check_result("runtime-model-info", verify_runtime),
    ]
    manifest = load_json_object(directory / "data" / "model_info.json")
    return {
        "schemaVersion": 1,
        "mode": "image",
        "generatedAt": utc_now(),
        "repositoryRevision": git_revision(),
        "image": image,
        "imageDigest": state.get("digest"),
        "modelDirectory": directory.relative_to(REPOSITORY_ROOT).as_posix(),
        "modelId": manifest.get("modelId"),
        "modelName": manifest.get("modelName"),
        "version": manifest.get("version"),
        "overall": overall_status(checks),
        "checks": checks,
    }


def write_evidence(evidence: dict[str, Any], path: str | None) -> None:
    serialized = json.dumps(evidence, indent=2, sort_keys=True) + "\n"
    if path is None:
        print(serialized, end="")
        return
    output = Path(path)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(serialized, encoding="utf-8")
    print(f"[verify-model] Evidence: {output}")


def source_evidence(
    directories: Sequence[Path], base_revision: str | None, run_tests: bool
) -> dict[str, Any]:
    source_revision = git_revision()
    models = [
        source_model_result(directory, source_revision, run_tests)
        for directory in directories
    ]
    overall = "PASS"
    if any(model["overall"] == "FAIL" for model in models):
        overall = "FAIL"
    elif any(model["overall"] == "PASS_WITH_SKIPS" for model in models):
        overall = "PASS_WITH_SKIPS"
    return {
        "schemaVersion": 1,
        "mode": "source",
        "generatedAt": utc_now(),
        "repositoryRevision": source_revision,
        "baseRevision": base_revision,
        "overall": overall,
        "models": models,
    }


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="mode", required=True)

    source = subparsers.add_parser("source", help="Verify model source contracts")
    source.add_argument("model_directories", nargs="*", help="Model directories to verify")
    source.add_argument("--changed-since", help="Verify models changed since this Git revision")
    source.add_argument(
        "--run-model-tests",
        action="store_true",
        help="Execute each selected model's pytest suite",
    )
    source.add_argument("--json", dest="evidence_path", help="Write JSON evidence to this path")

    image = subparsers.add_parser("image", help="Verify an immutable Docker image")
    image.add_argument("model_directory", help="Model directory matching the image")
    image.add_argument(
        "--image", required=True, help="Immutable repository@sha256 image reference"
    )
    image.add_argument(
        "--runtime-timeout-seconds",
        type=int,
        default=120,
        help="Temporary container startup timeout",
    )
    image.add_argument("--json", dest="evidence_path", help="Write JSON evidence to this path")
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        if args.mode == "source":
            if not args.model_directories and not args.changed_since:
                raise VerificationError(
                    "Provide at least one model directory or use --changed-since"
                )
            directories = {model_directory(path) for path in args.model_directories}
            if args.changed_since:
                directories.update(changed_model_directories(args.changed_since))
            require(bool(directories), "No model directories were selected for verification")
            evidence = source_evidence(
                sorted(directories), args.changed_since, args.run_model_tests
            )
        else:
            require(
                args.runtime_timeout_seconds > 0,
                "--runtime-timeout-seconds must be positive",
            )
            evidence = image_evidence(
                model_directory(args.model_directory),
                args.image,
                args.runtime_timeout_seconds,
            )
    except (VerificationError, ModelImageRepositoryError, RELEASE.ReleaseError) as exc:
        print(f"[verify-model] ERROR: {exc}", file=sys.stderr)
        return 2

    write_evidence(evidence, args.evidence_path)
    return 1 if evidence["overall"] == "FAIL" else 0


if __name__ == "__main__":
    raise SystemExit(main())
