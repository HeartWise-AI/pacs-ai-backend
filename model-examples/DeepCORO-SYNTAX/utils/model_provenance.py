"""Validate and expose build-time provenance without changing the shared resource contract."""

from __future__ import annotations

import re
from typing import Any


GIT_REVISION = re.compile(r"^[0-9a-f]{40}$")
SHA256 = re.compile(r"^[0-9a-f]{64}$")
REQUIRED_FIELDS = (
    "sourceRepository",
    "modelRepository",
    "modelRevision",
    "weightsPath",
    "weightsSha256",
)


def model_info_payload(model_info: Any, source_revision: str | None = None) -> dict[str, Any]:
    payload = model_info.model_dump(mode="json")
    provenance = payload.get("provenance")
    if not isinstance(provenance, dict):
        raise ValueError("DeepCORO-SYNTAX model metadata requires provenance")

    for field in REQUIRED_FIELDS:
        if not isinstance(provenance.get(field), str) or not provenance[field]:
            raise ValueError(f"provenance.{field} must be a non-empty string")

    if not GIT_REVISION.fullmatch(provenance["modelRevision"]):
        raise ValueError("provenance.modelRevision must be a 40-character lowercase Git SHA")
    if not SHA256.fullmatch(provenance["weightsSha256"]):
        raise ValueError("provenance.weightsSha256 must be a 64-character lowercase SHA-256")

    if source_revision:
        if not GIT_REVISION.fullmatch(source_revision):
            raise ValueError("sourceRevision must be a 40-character lowercase Git SHA")
        provenance["sourceRevision"] = source_revision
    return payload
