#!/usr/bin/env python3
"""Benchmark one PACS-AI model container with a prebuilt DICOM payload."""

from __future__ import annotations

import argparse
import base64
import json
import statistics
import threading
import time
from pathlib import Path
from typing import Any

import pydicom
import requests
from pynvml import (
    nvmlDeviceGetHandleByIndex,
    nvmlDeviceGetMemoryInfo,
    nvmlInit,
    nvmlShutdown,
)


MIB = 1024 * 1024


def collect_dicom_files(directory: Path, maximum: int) -> list[Path]:
    files = sorted(
        (path for path in directory.rglob("*") if path.suffix.lower() in {".dcm", ".dicom"}),
        key=lambda path: path.stat().st_size,
        reverse=True,
    )
    if len(files) < maximum:
        raise ValueError(f"need {maximum} DICOM files, found {len(files)}")
    return files[:maximum]


def build_payload(
    dicom_files: list[Path],
    include_metadata: bool = True,
    include_pixel_metadata: bool = True,
    output_mode: str = "JSON",
    single_series: bool = False,
) -> bytes:
    series_images: dict[int, dict[str, str]] = {}
    series_metadata: dict[int, dict[str, dict[str, Any]]] = {}

    for file_number, path in enumerate(dicom_files, start=1):
        dataset = pydicom.dcmread(path)
        series_number = 1 if single_series else file_number
        instance_number = str(file_number)
        series_images.setdefault(series_number, {})[instance_number] = (
            base64.b64encode(path.read_bytes()).decode("ascii")
        )
        if not include_metadata:
            continue

        metadata = {
            "00280008": {"Value": [getattr(dataset, "NumberOfFrames", 1)], "vr": "IS"},
            "00280002": {"Value": [getattr(dataset, "SamplesPerPixel", 1)], "vr": "US"},
            "00280010": {"Value": [getattr(dataset, "Rows", 0)], "vr": "US"},
            "00280011": {"Value": [getattr(dataset, "Columns", 0)], "vr": "US"},
            "00280100": {"Value": [getattr(dataset, "BitsAllocated", 8)], "vr": "US"},
            "00280101": {"Value": [getattr(dataset, "BitsStored", 8)], "vr": "US"},
            "00101010": {"Value": [getattr(dataset, "PatientAge", "")], "vr": "AS"},
        }
        if include_pixel_metadata:
            metadata["7FE00010"] = {
                "InlineBinary": base64.b64encode(dataset.pixel_array.tobytes()).decode("ascii"),
                "vr": "OB",
            }
        series_metadata.setdefault(series_number, {})[instance_number] = metadata

    payload: dict[str, Any] = {
        "additionalMetadata": {},
        "outputMode": output_mode,
        "seriesInstanceImages": series_images,
    }
    if include_metadata:
        payload["seriesInstanceMetadata"] = series_metadata
    return json.dumps(payload, separators=(",", ":")).encode("utf-8")


class VramSampler:
    def __init__(self, gpu_index: int, interval_seconds: float = 0.05):
        self.handle = nvmlDeviceGetHandleByIndex(gpu_index)
        self.interval_seconds = interval_seconds
        self.samples_mib: list[float] = []
        self._stop = threading.Event()
        self._thread: threading.Thread | None = None

    def used_mib(self) -> float:
        return nvmlDeviceGetMemoryInfo(self.handle).used / MIB

    def start(self) -> None:
        self.samples_mib = [self.used_mib()]
        self._stop.clear()
        self._thread = threading.Thread(target=self._sample, daemon=True)
        self._thread.start()

    def _sample(self) -> None:
        while not self._stop.wait(self.interval_seconds):
            self.samples_mib.append(self.used_mib())

    def finish(self) -> float:
        self._stop.set()
        if self._thread is not None:
            self._thread.join()
        self.samples_mib.append(self.used_mib())
        return max(self.samples_mib)


def make_request(url: str, payload: bytes, timeout: float, sampler: VramSampler) -> dict[str, Any]:
    sampler.start()
    started = time.perf_counter()
    response: requests.Response | None = None
    error: str | None = None
    try:
        response = requests.post(
            url,
            data=payload,
            headers={"Content-Type": "application/json"},
            timeout=timeout,
        )
        elapsed = time.perf_counter() - started
        try:
            body = response.json()
            success = bool(body.get("success"))
            message = body.get("message")
        except (ValueError, AttributeError):
            success = response.ok
            message = response.text[:500]
    except requests.RequestException as exc:
        elapsed = time.perf_counter() - started
        success = False
        message = None
        error = str(exc)
    peak_mib = sampler.finish()
    return {
        "elapsed_seconds": elapsed,
        "peak_used_vram_mib": peak_mib,
        "status_code": response.status_code if response is not None else None,
        "success": success,
        "message": message,
        "error": error,
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--model", required=True)
    parser.add_argument("--url", required=True)
    parser.add_argument("--dicom-dir", type=Path, required=True)
    parser.add_argument("--max-files", type=int, required=True)
    parser.add_argument("--output-mode", default="JSON")
    parser.add_argument(
        "--single-series",
        action="store_true",
        help="Group every selected DICOM instance into one series.",
    )
    parser.add_argument("--warm-runs", type=int, default=3)
    parser.add_argument("--timeout", type=float, default=1800)
    parser.add_argument("--gpu-index", type=int, default=0)
    parser.add_argument("--settle-seconds", type=float, default=2.0)
    parser.add_argument(
        "--omit-pixel-metadata",
        action="store_true",
        help="Send full DICOM files without duplicating decoded pixels in metadata.",
    )
    parser.add_argument(
        "--omit-metadata",
        action="store_true",
        help="Omit seriesInstanceMetadata for models that only consume full DICOM files.",
    )
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()

    selected_files = collect_dicom_files(args.dicom_dir, args.max_files)
    payload_started = time.perf_counter()
    payload = build_payload(
        selected_files,
        include_metadata=not args.omit_metadata,
        include_pixel_metadata=not args.omit_pixel_metadata,
        output_mode=args.output_mode,
        single_series=args.single_series,
    )
    payload_seconds = time.perf_counter() - payload_started

    nvmlInit()
    try:
        sampler = VramSampler(args.gpu_index)
        baseline_mib = sampler.used_mib()
        cold = make_request(args.url, payload, args.timeout, sampler)
        warm = [
            make_request(args.url, payload, args.timeout, sampler)
            for _ in range(args.warm_runs)
        ]
        time.sleep(args.settle_seconds)
        resident_mib = sampler.used_mib()
        result = {
            "model": args.model,
            "selected_files": [str(path) for path in selected_files],
            "payload_bytes": len(payload),
            "payload_build_seconds": payload_seconds,
            "baseline_used_vram_mib": baseline_mib,
            "resident_used_vram_mib": resident_mib,
            "resident_delta_vram_mib": resident_mib - baseline_mib,
            "peak_used_vram_mib": max(
                [cold["peak_used_vram_mib"]]
                + [run["peak_used_vram_mib"] for run in warm]
            ),
            "peak_delta_vram_mib": max(
                [cold["peak_used_vram_mib"]]
                + [run["peak_used_vram_mib"] for run in warm]
            )
            - baseline_mib,
            "cold": cold,
            "warm": warm,
            "warm_median_seconds": statistics.median(
                run["elapsed_seconds"] for run in warm
            ),
        }
    finally:
        nvmlShutdown()

    rendered = json.dumps(result, indent=2)
    if args.output:
        args.output.write_text(rendered + "\n")
    print(rendered)
    if not cold["success"] or any(not run["success"] for run in warm):
        raise SystemExit(2)


if __name__ == "__main__":
    main()
