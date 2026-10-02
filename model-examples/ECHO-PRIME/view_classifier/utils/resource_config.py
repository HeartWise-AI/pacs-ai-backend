from __future__ import annotations

import asyncio
import json
from pathlib import Path

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    StrictBool,
    StrictInt,
    ValidationError,
    field_validator,
    model_validator,
)


class ModelResources(BaseModel):
    """Runtime resource contract declared by an inference image."""

    model_config = ConfigDict(extra="forbid")

    gpuRequired: StrictBool = Field(
        description="Whether CUDA/GPU execution is required."
    )
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
            raise ValueError(
                "residentMemoryMiB and peakMemoryMiB must equal 0 for a CPU model"
            )
        return self


class ModelInfo(BaseModel):
    """Typed subset of model_info.json used for resource admission."""

    model_config = ConfigDict(extra="allow")

    modelId: str
    modelName: str
    version: str
    resources: ModelResources


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
            f"Invalid resource metadata for {model_label} at {model_info_path}: {exc}"
        ) from exc


def create_inference_semaphore(model_info: ModelInfo) -> asyncio.Semaphore:
    return asyncio.Semaphore(model_info.resources.maxConcurrentInferences)
