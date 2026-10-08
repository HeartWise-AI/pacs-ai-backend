from __future__ import annotations

import asyncio
import json
import re
from pathlib import Path, PurePosixPath
from typing import Any

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StrictBool,
    StrictInt,
    StrictStr,
    ValidationError,
    field_validator,
    model_validator,
)

GIT_REVISION_PATTERN = re.compile(r"^[0-9a-f]{40}$")
REPOSITORY_PATTERN = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$")
SHA256_PATTERN = re.compile(r"^[0-9a-f]{64}$")


class ModelProvenance(BaseModel):
    """Immutable source and weight identity declared by an inference image."""

    model_config = ConfigDict(extra="forbid")

    sourceRepository: StrictStr
    sourceRevision: StrictStr | None
    modelRepository: StrictStr
    modelRevision: StrictStr
    weightsPath: StrictStr
    weightsSha256: StrictStr

    @field_validator("sourceRepository", "modelRepository")
    @classmethod
    def validate_repository(cls, value: str) -> str:
        if not REPOSITORY_PATTERN.fullmatch(value):
            raise ValueError("must use the owner/repository format")
        return value

    @field_validator("sourceRevision", "modelRevision")
    @classmethod
    def validate_revision(cls, value: str | None) -> str | None:
        if value is not None and not GIT_REVISION_PATTERN.fullmatch(value):
            raise ValueError("must be a 40-character lowercase Git SHA")
        return value

    @field_validator("weightsPath")
    @classmethod
    def validate_weights_path(cls, value: str) -> str:
        parts = value.split("/")
        normalized = PurePosixPath(value).as_posix()
        if (
            not value
            or "\\" in value
            or value.startswith("/")
            or normalized != value
            or any(part in {"", ".", ".."} for part in parts)
        ):
            raise ValueError("must be a normalized repository-relative POSIX path")
        return value

    @field_validator("weightsSha256")
    @classmethod
    def validate_weights_sha256(cls, value: str) -> str:
        if not SHA256_PATTERN.fullmatch(value):
            raise ValueError("must be a 64-character lowercase SHA-256")
        return value


class ModelResources(BaseModel):
    """Runtime resource contract declared by an inference image."""

    model_config = ConfigDict(extra="forbid")

    gpuRequired: StrictBool = Field(description="Whether CUDA/GPU execution is required.")
    residentMemoryMiB: StrictInt = Field(
        description="GPU memory retained after model loading, in mebibytes (2^20 bytes)."
    )
    peakMemoryMiB: StrictInt = Field(
        description="Observed high-water GPU memory at declared concurrency, in mebibytes."
    )
    maxConcurrentInferences: StrictInt = Field(
        description="Maximum predictions admitted by one model process."
    )
    idleTimeoutSeconds: StrictInt = Field(
        description="Idle duration before future manager-driven eviction eligibility."
    )

    @field_validator("maxConcurrentInferences")
    @classmethod
    def require_v1_concurrency(cls, value: int) -> int:
        if value != 1:
            raise ValueError("maxConcurrentInferences must equal 1 in V1")
        return value

    @field_validator("idleTimeoutSeconds")
    @classmethod
    def require_positive_idle_timeout(cls, value: int) -> int:
        if value <= 0:
            raise ValueError("idleTimeoutSeconds must be greater than 0")
        return value

    @model_validator(mode="after")
    def validate_gpu_memory(self):
        if self.gpuRequired:
            if self.residentMemoryMiB <= 0:
                raise ValueError("residentMemoryMiB must be greater than 0 when GPU is required")
            if self.peakMemoryMiB <= 0:
                raise ValueError("peakMemoryMiB must be greater than 0 when GPU is required")
            if self.peakMemoryMiB < self.residentMemoryMiB:
                raise ValueError("peakMemoryMiB must be at least residentMemoryMiB")
        elif self.residentMemoryMiB != 0 or self.peakMemoryMiB != 0:
            raise ValueError("residentMemoryMiB and peakMemoryMiB must equal 0 for a CPU model")
        return self


class ModelInfo(BaseModel):
    """Typed model metadata used for provenance and resource admission."""

    model_config = ConfigDict(extra="allow")

    modelId: str
    modelName: str
    version: str
    resources: ModelResources
    provenance: ModelProvenance | None = None

    @model_validator(mode="before")
    @classmethod
    def reject_explicit_null_provenance(cls, value: Any):
        if isinstance(value, dict) and "provenance" in value and value["provenance"] is None:
            raise ValueError("provenance must be omitted or contain a complete object")
        return value


def load_model_info(path: str | Path) -> ModelInfo:
    model_info_path = Path(path)
    try:
        raw_model_info = json.loads(model_info_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"Unable to read model metadata at {model_info_path}: {exc}") from exc

    model_label = raw_model_info.get("modelId", raw_model_info.get("modelName", "unknown model"))
    try:
        return ModelInfo.model_validate(raw_model_info)
    except ValidationError as exc:
        raise ValueError(
            f"Invalid model metadata for {model_label} at {model_info_path}: {exc}"
        ) from exc


def model_info_payload(
    model_info: ModelInfo, source_revision: str | None = None
) -> dict[str, Any]:
    """Serialize validated metadata and apply an image-stamped source revision."""

    payload = model_info.model_dump(mode="json")
    if model_info.provenance is None:
        payload.pop("provenance", None)
        return payload

    if source_revision is not None:
        provenance = model_info.provenance.model_copy(update={"sourceRevision": source_revision})
        provenance = ModelProvenance.model_validate(provenance.model_dump())
        payload["provenance"] = provenance.model_dump(mode="json")
    return payload


def create_inference_semaphore(model_info: ModelInfo) -> asyncio.Semaphore:
    return asyncio.Semaphore(model_info.resources.maxConcurrentInferences)
