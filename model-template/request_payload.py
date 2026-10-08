"""Build deterministic PACS-AI inference payloads from DICOM files."""

from __future__ import annotations

import base64
from collections.abc import Callable, Iterable
from pathlib import Path
from typing import Any

import pydicom

DICOM_SUFFIXES = {".dcm", ".dicom"}


def collect_dicom_files(paths: Iterable[str | Path]) -> list[Path]:
    """Collect DICOM files from files and directories in stable path order."""
    dicom_files: list[Path] = []
    for raw_path in paths:
        path = Path(raw_path)
        if path.is_file() and path.suffix.lower() in DICOM_SUFFIXES:
            dicom_files.append(path)
        elif path.is_dir():
            dicom_files.extend(
                candidate
                for candidate in path.rglob("*")
                if candidate.is_file() and candidate.suffix.lower() in DICOM_SUFFIXES
            )
    return sorted(set(dicom_files), key=lambda candidate: str(candidate))


def build_request_payload(
    dicom_paths: Iterable[str | Path],
    *,
    output_mode: str = "JSON",
    send_metadata_only: bool = False,
    group_series: bool = False,
    additional_metadata: dict[str, Any] | None = None,
    error_handler: Callable[[Path, Exception], None] | None = None,
) -> dict[str, Any]:
    """Read DICOM files once and build the standard inference request body."""
    paths = [Path(path) for path in dicom_paths]
    if not paths:
        raise ValueError("at least one DICOM file is required")

    series_instance_metadata: dict[int, dict[str, dict[str, Any]]] = {}
    series_instance_images: dict[int, dict[str, str]] = {}

    for index, path in enumerate(paths, start=1):
        try:
            dataset = pydicom.dcmread(path)
            series_number = 1 if group_series else index
            instance_number = str(index)

            if send_metadata_only:
                metadata = {
                    "00280008": {
                        "Value": [getattr(dataset, "NumberOfFrames", 1)],
                        "vr": "IS",
                    },
                    "00280002": {
                        "Value": [getattr(dataset, "SamplesPerPixel", 1)],
                        "vr": "US",
                    },
                    "00280010": {
                        "Value": [getattr(dataset, "Rows", 0)],
                        "vr": "US",
                    },
                    "00280011": {
                        "Value": [getattr(dataset, "Columns", 0)],
                        "vr": "US",
                    },
                    "00280100": {
                        "Value": [getattr(dataset, "BitsAllocated", 8)],
                        "vr": "US",
                    },
                    "00280101": {
                        "Value": [getattr(dataset, "BitsStored", 8)],
                        "vr": "US",
                    },
                    "00101010": {
                        "Value": [getattr(dataset, "PatientAge", "29")],
                        "vr": "AS",
                    },
                    "7FE00010": {
                        "InlineBinary": base64.b64encode(dataset.pixel_array.tobytes()).decode(
                            "ascii"
                        ),
                        "vr": "OB",
                    },
                }
                series_instance_metadata.setdefault(series_number, {})[instance_number] = metadata
            else:
                series_instance_images.setdefault(series_number, {})[instance_number] = (
                    base64.b64encode(path.read_bytes()).decode("ascii")
                )
        except Exception as exc:
            if error_handler is None:
                raise
            error_handler(path, exc)

    payload: dict[str, Any] = {
        "additionalMetadata": additional_metadata or {"smoker": "false"},
        "outputMode": output_mode,
    }
    if send_metadata_only:
        payload["seriesInstanceMetadata"] = series_instance_metadata
    else:
        payload["seriesInstanceImages"] = series_instance_images
    return payload
