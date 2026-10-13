#!/usr/bin/env python3
"""Resolve an approved Docker repository for a PACS-AI model directory."""

from __future__ import annotations

import argparse
import json
import re
import sys
from collections.abc import Mapping, Sequence
from pathlib import Path, PurePosixPath
from typing import Any

REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
DEFAULT_MAPPING_PATH = REPOSITORY_ROOT / ".github" / "model-image-repositories.json"
IMAGE_REPOSITORY_PATTERN = re.compile(
    r"^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)+$"
)


class ModelImageRepositoryError(RuntimeError):
    """The repository mapping or requested model directory is invalid."""


def model_directory_key(model_directory: Path, repository_root: Path = REPOSITORY_ROOT) -> str:
    root = repository_root.resolve()
    candidate = model_directory
    if not candidate.is_absolute():
        candidate = root / candidate
    try:
        relative = candidate.resolve().relative_to(root)
    except ValueError as exc:
        raise ModelImageRepositoryError("Model directory must be inside the repository") from exc
    key = relative.as_posix()
    if not key.startswith("model-examples/") or key == "model-examples":
        raise ModelImageRepositoryError("Model directory must be under model-examples/")
    return key


def load_repository_mapping(mapping_path: Path = DEFAULT_MAPPING_PATH) -> dict[str, str]:
    try:
        payload: Any = json.loads(mapping_path.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        raise ModelImageRepositoryError(f"Repository mapping not found: {mapping_path}") from exc
    except json.JSONDecodeError as exc:
        raise ModelImageRepositoryError(f"Repository mapping is invalid JSON: {exc}") from exc

    if not isinstance(payload, Mapping) or set(payload) != {"schemaVersion", "repositories"}:
        raise ModelImageRepositoryError(
            "Repository mapping must contain schemaVersion and repositories"
        )
    if payload["schemaVersion"] != 1:
        raise ModelImageRepositoryError("Unsupported repository mapping schemaVersion")
    repositories = payload["repositories"]
    if not isinstance(repositories, Mapping) or not repositories:
        raise ModelImageRepositoryError("Repository mapping must contain at least one model")

    normalized: dict[str, str] = {}
    assigned_repositories: set[str] = set()
    for raw_model, raw_repository in repositories.items():
        if not isinstance(raw_model, str) or not isinstance(raw_repository, str):
            raise ModelImageRepositoryError("Repository mapping keys and values must be strings")
        model = PurePosixPath(raw_model)
        if (
            model.as_posix() != raw_model
            or not raw_model.startswith("model-examples/")
            or ".." in model.parts
            or model.name in {"", "."}
        ):
            raise ModelImageRepositoryError(
                f"Invalid model directory in repository mapping: {raw_model}"
            )
        if not IMAGE_REPOSITORY_PATTERN.fullmatch(raw_repository):
            raise ModelImageRepositoryError(
                f"Invalid Docker repository in mapping: {raw_repository}"
            )
        if raw_repository in assigned_repositories:
            raise ModelImageRepositoryError(
                f"Docker repository is assigned more than once: {raw_repository}"
            )
        normalized[raw_model] = raw_repository
        assigned_repositories.add(raw_repository)
    return normalized


def resolve_image_repository(
    model_directory: Path,
    *,
    mapping_path: Path = DEFAULT_MAPPING_PATH,
    repository_root: Path = REPOSITORY_ROOT,
) -> str:
    key = model_directory_key(model_directory, repository_root)
    mapping = load_repository_mapping(mapping_path)
    try:
        return mapping[key]
    except KeyError as exc:
        raise ModelImageRepositoryError(
            f"Model directory is not approved for image publication: {key}"
        ) from exc


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("model_directory", help="Model directory under model-examples/")
    parser.add_argument(
        "--mapping",
        default=str(DEFAULT_MAPPING_PATH),
        help="Approved model repository mapping",
    )
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        repository = resolve_image_repository(
            Path(args.model_directory),
            mapping_path=Path(args.mapping),
        )
    except ModelImageRepositoryError as exc:
        print(f"[repository-map] ERROR: {exc}", file=sys.stderr)
        return 1
    print(repository)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
