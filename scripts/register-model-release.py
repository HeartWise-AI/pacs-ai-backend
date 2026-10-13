#!/usr/bin/env python3
"""Register verified model release evidence in the PACS-AI version catalog."""

from __future__ import annotations

import argparse
import json
import time
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Callable, Sequence
from pathlib import Path
from typing import Any


class CatalogRegistrationError(RuntimeError):
    """The release evidence or catalog response was invalid."""


def require_string(value: Any, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise CatalogRegistrationError(f"{field} must be a non-empty string")
    return value.strip()


def load_catalog_payload(evidence_path: Path) -> dict[str, Any]:
    try:
        evidence = json.loads(evidence_path.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        raise CatalogRegistrationError(f"Release evidence not found: {evidence_path}") from exc
    except json.JSONDecodeError as exc:
        raise CatalogRegistrationError(f"Invalid release evidence: {exc}") from exc

    if not isinstance(evidence, dict) or evidence.get("status") != "published":
        raise CatalogRegistrationError("Catalog registration requires published release evidence")
    model = evidence.get("model")
    source = evidence.get("source")
    weights = evidence.get("weights")
    image = evidence.get("image")
    if not all(isinstance(section, dict) for section in (model, source, weights, image)):
        raise CatalogRegistrationError("Release evidence is missing required sections")

    release = evidence.get("release")
    if release is None:
        release = {"channel": "RELEASE", "baseVersion": model.get("version")}
    if not isinstance(release, dict):
        raise CatalogRegistrationError("Release evidence contains an invalid release section")
    channel = require_string(release.get("channel"), "release.channel").upper()
    if channel not in {"RELEASE", "CANDIDATE"}:
        raise CatalogRegistrationError("release.channel must be RELEASE or CANDIDATE")
    base_version = require_string(release.get("baseVersion"), "release.baseVersion")
    source_branch = release.get("sourceBranch")
    pull_request = release.get("pullRequest")
    workflow_run = release.get("workflowRun")
    if channel == "CANDIDATE":
        if not isinstance(source_branch, str) or not source_branch.strip():
            raise CatalogRegistrationError("Candidate evidence requires release.sourceBranch")
        if not isinstance(pull_request, int) or pull_request <= 0:
            raise CatalogRegistrationError("Candidate evidence requires release.pullRequest")
        if not isinstance(workflow_run, str) or not workflow_run.isdigit():
            raise CatalogRegistrationError("Candidate evidence requires release.workflowRun")

    return {
        "modelId": require_string(model.get("id"), "model.id"),
        "version": require_string(model.get("version"), "model.version"),
        "imageReference": require_string(image.get("reference"), "image.reference"),
        "imageDigest": require_string(image.get("registryDigest"), "image.registryDigest"),
        "channel": channel,
        "baseVersion": base_version,
        "sourceBranch": source_branch.strip() if isinstance(source_branch, str) else None,
        "pullRequest": pull_request,
        "workflowRun": workflow_run,
        "provenance": {
            "sourceRepository": require_string(source.get("repository"), "source.repository"),
            "sourceRevision": require_string(source.get("revision"), "source.revision"),
            "modelRepository": require_string(weights.get("repository"), "weights.repository"),
            "modelRevision": require_string(weights.get("revision"), "weights.revision"),
            "weightsPath": require_string(weights.get("path"), "weights.path"),
            "weightsSha256": require_string(weights.get("sha256"), "weights.sha256"),
        },
    }


def catalog_endpoint(api_url: str) -> str:
    parsed = urllib.parse.urlsplit(api_url.strip())
    if parsed.scheme != "https" or not parsed.netloc or parsed.username or parsed.password:
        raise CatalogRegistrationError("Catalog API URL must be an HTTPS origin")
    if parsed.query or parsed.fragment:
        raise CatalogRegistrationError("Catalog API URL cannot contain a query or fragment")
    base_path = parsed.path.rstrip("/")
    return urllib.parse.urlunsplit(
        (
            parsed.scheme,
            parsed.netloc,
            f"{base_path}/v1/inference/model/versions/publish",
            "",
            "",
        )
    )


def read_sudo_key(path: Path) -> str:
    try:
        key = path.read_text(encoding="utf-8").strip()
    except FileNotFoundError as exc:
        raise CatalogRegistrationError(f"Catalog credential not found: {path}") from exc
    if not key:
        raise CatalogRegistrationError("Catalog credential is empty")
    return key


def register_catalog_version(
    *,
    endpoint: str,
    sudo_key: str,
    payload: dict[str, Any],
    attempts: int = 3,
    open_request: Callable[..., Any] = urllib.request.urlopen,
    sleep: Callable[[float], None] = time.sleep,
) -> dict[str, Any]:
    body = json.dumps(payload, separators=(",", ":")).encode("utf-8")
    request = urllib.request.Request(
        endpoint,
        data=body,
        method="POST",
        headers={
            "Content-Type": "application/json",
            "X-FB-SUDO-KEY": sudo_key,
        },
    )
    last_error: Exception | None = None
    for attempt in range(1, attempts + 1):
        try:
            with open_request(request, timeout=30) as response:
                result = json.load(response)
            if not isinstance(result, dict) or result.get("success") is not True:
                raise CatalogRegistrationError("Catalog API returned an unsuccessful response")
            return result
        except urllib.error.HTTPError as exc:
            detail = exc.read().decode("utf-8", errors="replace").strip()
            if exc.code < 500 or attempt == attempts:
                raise CatalogRegistrationError(
                    f"Catalog API returned HTTP {exc.code}: {detail or exc.reason}"
                ) from exc
            last_error = exc
        except (urllib.error.URLError, TimeoutError) as exc:
            if attempt == attempts:
                raise CatalogRegistrationError(f"Catalog API request failed: {exc}") from exc
            last_error = exc
        sleep(float(attempt))
    raise CatalogRegistrationError(f"Catalog API request failed: {last_error}")


def parse_args(argv: Sequence[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("evidence", help="Published model release evidence JSON")
    parser.add_argument("--api-url", required=True, help="PACS-AI backend HTTPS origin")
    parser.add_argument("--sudo-key-file", required=True, help="Path to the backend superuser key")
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        payload = load_catalog_payload(Path(args.evidence).resolve())
        endpoint = catalog_endpoint(args.api_url)
        sudo_key = read_sudo_key(Path(args.sudo_key_file).resolve())
        result = register_catalog_version(
            endpoint=endpoint,
            sudo_key=sudo_key,
            payload=payload,
        )
    except CatalogRegistrationError as exc:
        print(f"[catalog] {exc}")
        return 1
    version_id = result.get("data", {}).get("id", "unknown")
    print(f"[catalog] Registered {payload['modelId']} {payload['version']} as {version_id}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
