#!/usr/bin/env python3
"""Run reproducible concurrent inference benchmarks through the PACS-AI gateway."""

from __future__ import annotations

import argparse
import asyncio
import csv
import importlib.util
import json
import math
import os
import platform
import shutil
import subprocess
import sys
import time
from collections import defaultdict
from collections.abc import Iterable
from dataclasses import asdict, dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import httpx

REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
PAYLOAD_MODULE_PATH = REPOSITORY_ROOT / "model-template" / "request_payload.py"
RESULT_FIELDS = (
    "run_id",
    "phase",
    "model",
    "concurrency",
    "repetition",
    "request_index",
    "started_at",
    "elapsed_seconds",
    "status_code",
    "success",
    "error_code",
    "exception_type",
    "response_bytes",
)
SAMPLE_FIELDS = (
    "run_id",
    "timestamp",
    "cpu_percent",
    "ram_used_mib",
    "ram_percent",
    "gpu_used_mib",
    "gpu_total_mib",
    "manager_ready",
    "manager_loaded_models",
    "manager_queue_depth",
    "manager_capacity_mib",
    "manager_resident_mib",
    "manager_active_mib",
    "manager_reserved_mib",
    "manager_available_mib",
)


def _load_payload_module() -> Any:
    spec = importlib.util.spec_from_file_location("pacs_ai_request_payload", PAYLOAD_MODULE_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load {PAYLOAD_MODULE_PATH}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


REQUEST_PAYLOAD = _load_payload_module()


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat()


def percentile(values: Iterable[float], percentage: float) -> float | None:
    ordered = sorted(values)
    if not ordered:
        return None
    if not 0 <= percentage <= 100:
        raise ValueError("percentage must be between 0 and 100")
    if len(ordered) == 1:
        return ordered[0]
    position = (len(ordered) - 1) * percentage / 100
    lower = math.floor(position)
    upper = math.ceil(position)
    if lower == upper:
        return ordered[lower]
    weight = position - lower
    return ordered[lower] * (1 - weight) + ordered[upper] * weight


def numeric_map_delta(before: Any, after: Any) -> dict[str, int | float]:
    if not isinstance(before, dict) or not isinstance(after, dict):
        return {}
    delta: dict[str, int | float] = {}
    for key in sorted(set(before) | set(after)):
        old_value = before.get(key, 0)
        new_value = after.get(key, 0)
        if isinstance(old_value, (int, float)) and isinstance(new_value, (int, float)):
            difference = new_value - old_value
            if difference:
                delta[key] = difference
    return delta


@dataclass(frozen=True)
class ModelConfig:
    name: str
    container_ref: str
    fixture_name: str
    dicom_paths: tuple[Path, ...]
    output_mode: str = "JSON"
    group_series: bool = False
    metadata_only: bool = False
    lifecycle_url: str | None = None


@dataclass(frozen=True)
class MixedConfig:
    concurrency: int
    repetitions: int
    distribution: dict[str, int]


@dataclass(frozen=True)
class BenchmarkConfig:
    gateway_url: str
    metrics_url: str | None
    tenant_id: str
    concurrency_levels: tuple[int, ...]
    repetitions: int
    timeout_seconds: float
    sample_interval_seconds: float
    settle_seconds: float
    require_cold_start: bool
    local_resource_sampling: bool
    deployment_name: str
    load_generator_location: str
    notes: str
    models: tuple[ModelConfig, ...]
    server_configuration: dict[str, Any] = field(default_factory=dict)
    mixed: MixedConfig | None = None


@dataclass
class PreparedModel:
    config: ModelConfig
    gateway_payload: bytes
    payload_bytes: int
    dicom_file_count: int


@dataclass
class RequestResult:
    run_id: str
    phase: str
    model: str
    concurrency: int
    repetition: int
    request_index: int
    started_at: str
    elapsed_seconds: float
    status_code: int | None
    success: bool
    error_code: str | None
    exception_type: str | None
    response_bytes: int


@dataclass
class ResourceSample:
    run_id: str
    timestamp: str
    cpu_percent: float | None = None
    ram_used_mib: float | None = None
    ram_percent: float | None = None
    gpu_used_mib: float | None = None
    gpu_total_mib: float | None = None
    manager_ready: int | None = None
    manager_loaded_models: int | None = None
    manager_queue_depth: int | None = None
    manager_capacity_mib: int | None = None
    manager_resident_mib: int | None = None
    manager_active_mib: int | None = None
    manager_reserved_mib: int | None = None
    manager_available_mib: int | None = None


@dataclass
class RunResult:
    run_id: str
    phase: str
    model: str
    concurrency: int
    repetition: int
    started_at: str
    duration_seconds: float
    metric_deltas: dict[str, dict[str, int | float]] = field(default_factory=dict)


class StartGate:
    """Release a known number of coroutines at the same instant."""

    def __init__(self, participants: int):
        if participants <= 0:
            raise ValueError("participants must be greater than zero")
        self.participants = participants
        self._waiting = 0
        self._lock = asyncio.Lock()
        self._ready = asyncio.Event()
        self._released = asyncio.Event()

    async def wait(self) -> None:
        async with self._lock:
            self._waiting += 1
            if self._waiting == self.participants:
                self._ready.set()
        await self._released.wait()

    async def release_when_ready(self, timeout_seconds: float) -> None:
        await asyncio.wait_for(self._ready.wait(), timeout=timeout_seconds)
        self._released.set()


class HostSampler:
    def __init__(self, enabled: bool):
        self.enabled = enabled
        self.nvidia_smi = shutil.which("nvidia-smi") if enabled else None
        self._previous_cpu: tuple[int, int] | None = None

    def _cpu_percent(self) -> float | None:
        if not self.enabled:
            return None
        try:
            fields = Path("/proc/stat").read_text().splitlines()[0].split()[1:]
            values = [int(value) for value in fields]
        except (OSError, ValueError, IndexError):
            return None
        idle = values[3] + (values[4] if len(values) > 4 else 0)
        total = sum(values)
        previous = self._previous_cpu
        self._previous_cpu = (idle, total)
        if previous is None or total == previous[1]:
            return None
        idle_delta = idle - previous[0]
        total_delta = total - previous[1]
        return round(100 * (1 - idle_delta / total_delta), 3)

    @staticmethod
    def _memory() -> tuple[float | None, float | None]:
        try:
            fields: dict[str, int] = {}
            for line in Path("/proc/meminfo").read_text().splitlines():
                key, value = line.split(":", 1)
                fields[key] = int(value.strip().split()[0])
            total_kib = fields["MemTotal"]
            available_kib = fields["MemAvailable"]
        except (OSError, ValueError, KeyError, IndexError):
            return None, None
        used_kib = total_kib - available_kib
        return round(used_kib / 1024, 3), round(100 * used_kib / total_kib, 3)

    def _gpu(self) -> tuple[float | None, float | None]:
        if self.nvidia_smi is None:
            return None, None
        try:
            completed = subprocess.run(
                [
                    "nvidia-smi",
                    "--query-gpu=memory.used,memory.total",
                    "--format=csv,noheader,nounits",
                ],
                check=True,
                capture_output=True,
                text=True,
                timeout=5,
            )
            first_gpu = completed.stdout.splitlines()[0]
            used, total = (float(value.strip()) for value in first_gpu.split(","))
            return used, total
        except (OSError, ValueError, IndexError, subprocess.SubprocessError):
            return None, None

    async def sample(
        self,
    ) -> tuple[float | None, float | None, float | None, float | None, float | None]:
        if not self.enabled:
            return None, None, None, None, None
        cpu_percent = self._cpu_percent()
        ram_used_mib, ram_percent = self._memory()
        gpu_used_mib, gpu_total_mib = await asyncio.to_thread(self._gpu)
        return cpu_percent, ram_used_mib, ram_percent, gpu_used_mib, gpu_total_mib


class MetricsClient:
    def __init__(
        self,
        url: str | None,
        username: str,
        password: str | None,
        timeout_seconds: float = 10,
    ):
        self.url = url
        self.client = (
            httpx.AsyncClient(
                auth=httpx.BasicAuth(username, password or ""),
                timeout=timeout_seconds,
            )
            if url
            else None
        )

    async def close(self) -> None:
        if self.client is not None:
            await self.client.aclose()

    async def fetch(self) -> dict[str, Any]:
        if self.client is None or self.url is None:
            return {}
        response = await self.client.get(self.url)
        response.raise_for_status()
        body = response.json()
        return body if isinstance(body, dict) else {}


def manager_sample(run_id: str, metrics: dict[str, Any]) -> ResourceSample:
    memory = metrics.get("model_manager_gpu_memory_mib", {})
    return ResourceSample(
        run_id=run_id,
        timestamp=utc_now(),
        manager_ready=_integer_or_none(metrics.get("model_manager_ready")),
        manager_loaded_models=_integer_or_none(metrics.get("model_manager_loaded_models")),
        manager_queue_depth=_integer_or_none(metrics.get("model_manager_queue_depth")),
        manager_capacity_mib=_integer_or_none(memory.get("kind=capacity")),
        manager_resident_mib=_integer_or_none(memory.get("kind=resident")),
        manager_active_mib=_integer_or_none(memory.get("kind=active")),
        manager_reserved_mib=_integer_or_none(memory.get("kind=reserved")),
        manager_available_mib=_integer_or_none(memory.get("kind=available")),
    )


def _integer_or_none(value: Any) -> int | None:
    if isinstance(value, (int, float)):
        return int(value)
    return None


async def sample_resources(
    run_id: str,
    stop: asyncio.Event,
    interval_seconds: float,
    metrics_client: MetricsClient,
    host_sampler: HostSampler,
    samples: list[ResourceSample],
) -> None:
    while True:
        try:
            metrics, host = await asyncio.gather(metrics_client.fetch(), host_sampler.sample())
        except (httpx.HTTPError, OSError, ValueError):
            metrics = {}
            host = await host_sampler.sample()
        sample = manager_sample(run_id, metrics)
        (
            sample.cpu_percent,
            sample.ram_used_mib,
            sample.ram_percent,
            sample.gpu_used_mib,
            sample.gpu_total_mib,
        ) = host
        samples.append(sample)
        try:
            await asyncio.wait_for(stop.wait(), timeout=interval_seconds)
            return
        except (TimeoutError, asyncio.TimeoutError):
            continue


def load_config(path: Path) -> BenchmarkConfig:
    raw = json.loads(path.read_text())
    model_configs = [
        ModelConfig(
            name=_required_string(raw_model, "name"),
            container_ref=_required_string(raw_model, "containerRef"),
            fixture_name=_required_string(raw_model, "fixtureName"),
            dicom_paths=tuple(
                Path(item).expanduser() for item in raw_model.get("dicomPaths", [])
            ),
            output_mode=str(raw_model.get("outputMode", "JSON")),
            group_series=bool(raw_model.get("groupSeries", False)),
            metadata_only=bool(raw_model.get("metadataOnly", False)),
            lifecycle_url=_optional_string(raw_model.get("lifecycleUrl")),
        )
        for raw_model in raw.get("models", [])
    ]
    if not model_configs:
        raise ValueError("config.models must contain at least one model")

    concurrency_levels = tuple(int(value) for value in raw.get("concurrencyLevels", []))
    if not concurrency_levels or any(value <= 0 for value in concurrency_levels):
        raise ValueError("concurrencyLevels must contain positive integers")

    mixed_raw = raw.get("mixed")
    mixed = None
    if isinstance(mixed_raw, dict) and mixed_raw.get("enabled", True):
        distribution = {
            str(name): int(count) for name, count in mixed_raw.get("distribution", {}).items()
        }
        mixed = MixedConfig(
            concurrency=int(mixed_raw.get("concurrency", sum(distribution.values()))),
            repetitions=int(mixed_raw.get("repetitions", raw.get("repetitions", 3))),
            distribution=distribution,
        )

    config = BenchmarkConfig(
        gateway_url=_required_string(raw, "gatewayUrl"),
        metrics_url=_optional_string(raw.get("metricsUrl")),
        tenant_id=_required_string(raw, "tenantId"),
        concurrency_levels=concurrency_levels,
        repetitions=int(raw.get("repetitions", 3)),
        timeout_seconds=float(raw.get("timeoutSeconds", 960)),
        sample_interval_seconds=float(raw.get("sampleIntervalSeconds", 1)),
        settle_seconds=float(raw.get("settleSeconds", 2)),
        require_cold_start=bool(raw.get("requireColdStart", True)),
        local_resource_sampling=bool(raw.get("localResourceSampling", True)),
        deployment_name=str(raw.get("deploymentName", "unspecified")),
        load_generator_location=str(raw.get("loadGeneratorLocation", "unspecified")),
        notes=str(raw.get("notes", "")),
        models=tuple(model_configs),
        server_configuration=dict(raw.get("serverConfiguration", {})),
        mixed=mixed,
    )
    validate_config(config)
    return config


def _required_string(raw: dict[str, Any], key: str) -> str:
    value = raw.get(key)
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{key} must be a non-empty string")
    return value.strip()


def _optional_string(value: Any) -> str | None:
    if value is None:
        return None
    normalized = str(value).strip()
    return normalized or None


def validate_config(config: BenchmarkConfig) -> None:
    if config.repetitions <= 0:
        raise ValueError("repetitions must be greater than zero")
    if config.timeout_seconds <= 0 or config.sample_interval_seconds <= 0:
        raise ValueError("timeouts and sampling intervals must be greater than zero")
    names = {model.name for model in config.models}
    if len(names) != len(config.models):
        raise ValueError("model names must be unique")
    if config.require_cold_start and config.metrics_url is None:
        raise ValueError("metricsUrl is required when requireColdStart is enabled")
    for model in config.models:
        if not model.dicom_paths:
            raise ValueError(f"{model.name} must define dicomPaths")
        if config.require_cold_start and model.lifecycle_url is None:
            raise ValueError(
                f"{model.name} must define lifecycleUrl when requireColdStart is enabled"
            )
    if config.mixed is not None:
        if config.mixed.concurrency <= 0 or config.mixed.repetitions <= 0:
            raise ValueError("mixed concurrency and repetitions must be positive")
        if set(config.mixed.distribution) - names:
            raise ValueError("mixed distribution references an unknown model")
        if any(value <= 0 for value in config.mixed.distribution.values()):
            raise ValueError("mixed distribution counts must be positive")
        if sum(config.mixed.distribution.values()) != config.mixed.concurrency:
            raise ValueError("mixed distribution must sum to mixed concurrency")


def prepare_models(config: BenchmarkConfig) -> dict[str, PreparedModel]:
    prepared: dict[str, PreparedModel] = {}
    for model in config.models:
        dicom_files = REQUEST_PAYLOAD.collect_dicom_files(model.dicom_paths)
        if not dicom_files:
            raise ValueError(f"no DICOM files found for {model.name}")
        request = REQUEST_PAYLOAD.build_request_payload(
            dicom_files,
            output_mode=model.output_mode,
            send_metadata_only=model.metadata_only,
            group_series=model.group_series,
        )
        gateway_payload = json.dumps(
            {
                "tenantId": config.tenant_id,
                "containerRef": model.container_ref,
                "request": request,
            },
            separators=(",", ":"),
        ).encode("utf-8")
        prepared[model.name] = PreparedModel(
            config=model,
            gateway_payload=gateway_payload,
            payload_bytes=len(gateway_payload),
            dicom_file_count=len(dicom_files),
        )
    return prepared


async def verify_unloaded_runtime(client: httpx.AsyncClient, model: ModelConfig) -> None:
    if model.lifecycle_url is None:
        return
    base_url = model.lifecycle_url.rstrip("/")
    runtime_url = f"{base_url}/model/runtime"
    response = await client.get(runtime_url)
    response.raise_for_status()
    runtime = response.json().get("data", {})
    if runtime.get("state") != "UNLOADED" or runtime.get("loaded") is not False:
        raise RuntimeError(
            f"{model.name} is not unloaded; unload all benchmark models and restart "
            "api-pacs before measuring cold starts"
        )


async def send_request(
    client: httpx.AsyncClient,
    gateway_url: str,
    prepared: PreparedModel,
    gate: StartGate,
    run_id: str,
    phase: str,
    concurrency: int,
    repetition: int,
    request_index: int,
) -> RequestResult:
    await gate.wait()
    started_at = utc_now()
    started = time.perf_counter()
    status_code = None
    success = False
    error_code = None
    exception_type = None
    response_bytes = 0
    try:
        response_buffer = bytearray()
        async with client.stream(
            "POST",
            gateway_url,
            content=prepared.gateway_payload,
            headers={"Content-Type": "application/json"},
        ) as response:
            status_code = response.status_code
            async for chunk in response.aiter_bytes():
                response_bytes += len(chunk)
                if len(response_buffer) < 65536:
                    response_buffer.extend(chunk[: 65536 - len(response_buffer)])
        elapsed_seconds = time.perf_counter() - started
        try:
            body = json.loads(response_buffer) if response_bytes <= 65536 else {}
        except (UnicodeDecodeError, ValueError):
            body = {}
        if not isinstance(body, dict):
            body = {}
        success = (
            status_code is not None
            and 200 <= status_code < 300
            and body.get("success", True) is True
        )
        raw_error_code = body.get("errorCode") if isinstance(body, dict) else None
        error_code = str(raw_error_code)[:80] if raw_error_code else None
    except httpx.HTTPError as exc:
        elapsed_seconds = time.perf_counter() - started
        exception_type = type(exc).__name__

    return RequestResult(
        run_id=run_id,
        phase=phase,
        model=prepared.config.name,
        concurrency=concurrency,
        repetition=repetition,
        request_index=request_index,
        started_at=started_at,
        elapsed_seconds=elapsed_seconds,
        status_code=status_code,
        success=success,
        error_code=error_code,
        exception_type=exception_type,
        response_bytes=response_bytes,
    )


async def execute_burst(
    *,
    client: httpx.AsyncClient,
    gateway_url: str,
    assignments: list[PreparedModel],
    phase: str,
    repetition: int,
    metrics_client: MetricsClient,
    host_sampler: HostSampler,
    sample_interval_seconds: float,
    gate_timeout_seconds: float,
    request_results: list[RequestResult],
    resource_samples: list[ResourceSample],
) -> RunResult:
    concurrency = len(assignments)
    model_name = (
        assignments[0].config.name
        if len({item.config.name for item in assignments}) == 1
        else "mixed"
    )
    run_id = f"{phase}-{model_name}-c{concurrency}-r{repetition}"
    gate = StartGate(concurrency)
    metrics_before = await metrics_client.fetch()
    stop_sampling = asyncio.Event()
    sampler_task = asyncio.create_task(
        sample_resources(
            run_id,
            stop_sampling,
            sample_interval_seconds,
            metrics_client,
            host_sampler,
            resource_samples,
        )
    )
    tasks = [
        asyncio.create_task(
            send_request(
                client,
                gateway_url,
                prepared,
                gate,
                run_id,
                phase,
                concurrency,
                repetition,
                index,
            )
        )
        for index, prepared in enumerate(assignments, start=1)
    ]
    started_at = utc_now()
    started = time.perf_counter()
    try:
        await gate.release_when_ready(gate_timeout_seconds)
        request_results.extend(await asyncio.gather(*tasks))
    except BaseException:
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        raise
    finally:
        stop_sampling.set()
        await sampler_task
    duration_seconds = time.perf_counter() - started
    metrics_after = await metrics_client.fetch()
    metric_deltas = {
        key: numeric_map_delta(metrics_before.get(key), metrics_after.get(key))
        for key in (
            "model_manager_events_total",
            "model_manager_duration_seconds_count",
            "model_manager_duration_milliseconds_sum",
            "model_manager_duration_seconds_bucket",
        )
    }
    return RunResult(
        run_id=run_id,
        phase=phase,
        model=model_name,
        concurrency=concurrency,
        repetition=repetition,
        started_at=started_at,
        duration_seconds=duration_seconds,
        metric_deltas={key: value for key, value in metric_deltas.items() if value},
    )


def mixed_assignments(
    mixed: MixedConfig, prepared_models: dict[str, PreparedModel]
) -> list[PreparedModel]:
    assignments: list[PreparedModel] = []
    remaining = dict(mixed.distribution)
    names = sorted(remaining)
    while remaining:
        for name in names:
            if name not in remaining:
                continue
            assignments.append(prepared_models[name])
            remaining[name] -= 1
            if remaining[name] == 0:
                del remaining[name]
    return assignments


def _metric_is_clean(metrics: dict[str, Any]) -> bool:
    memory = metrics.get("model_manager_gpu_memory_mib", {})
    return (
        metrics.get("model_manager_ready") == 1
        and metrics.get("model_manager_queue_depth") == 0
        and memory.get("kind=active") == 0
    )


async def run_benchmark(
    config: BenchmarkConfig,
    *,
    gateway_token: str,
    metrics_username: str,
    metrics_password: str | None,
) -> tuple[
    dict[str, PreparedModel],
    list[RequestResult],
    list[ResourceSample],
    list[RunResult],
    dict[str, Any],
]:
    prepared_models = prepare_models(config)
    request_results: list[RequestResult] = []
    resource_samples: list[ResourceSample] = []
    run_results: list[RunResult] = []
    headers = {"Authorization": f"Bearer {gateway_token}"}
    metrics_client = MetricsClient(config.metrics_url, metrics_username, metrics_password)
    host_sampler = HostSampler(config.local_resource_sampling)
    try:
        async with (
            httpx.AsyncClient(headers=headers, timeout=config.timeout_seconds) as client,
            httpx.AsyncClient(timeout=config.timeout_seconds) as lifecycle_client,
        ):
            initial_metrics = await metrics_client.fetch()
            if initial_metrics and initial_metrics.get("model_manager_ready") != 1:
                raise RuntimeError("Model Manager is not ready")
            if config.require_cold_start:
                initial_memory = initial_metrics.get("model_manager_gpu_memory_mib", {})
                if initial_metrics and (
                    initial_metrics.get("model_manager_loaded_models") != 0
                    or initial_memory.get("kind=resident") != 0
                ):
                    raise RuntimeError(
                        "Model Manager is not cold; unload all benchmark models and restart "
                        "api-pacs before the run"
                    )
                for model in config.models:
                    await verify_unloaded_runtime(lifecycle_client, model)

            for model in config.models:
                prepared = prepared_models[model.name]
                run_results.append(
                    await execute_burst(
                        client=client,
                        gateway_url=config.gateway_url,
                        assignments=[prepared],
                        phase="cold",
                        repetition=1,
                        metrics_client=metrics_client,
                        host_sampler=host_sampler,
                        sample_interval_seconds=config.sample_interval_seconds,
                        gate_timeout_seconds=config.timeout_seconds,
                        request_results=request_results,
                        resource_samples=resource_samples,
                    )
                )
                run_results.append(
                    await execute_burst(
                        client=client,
                        gateway_url=config.gateway_url,
                        assignments=[prepared],
                        phase="warmup",
                        repetition=1,
                        metrics_client=metrics_client,
                        host_sampler=host_sampler,
                        sample_interval_seconds=config.sample_interval_seconds,
                        gate_timeout_seconds=config.timeout_seconds,
                        request_results=request_results,
                        resource_samples=resource_samples,
                    )
                )
                for concurrency in config.concurrency_levels:
                    for repetition in range(1, config.repetitions + 1):
                        run_results.append(
                            await execute_burst(
                                client=client,
                                gateway_url=config.gateway_url,
                                assignments=[prepared] * concurrency,
                                phase="warm",
                                repetition=repetition,
                                metrics_client=metrics_client,
                                host_sampler=host_sampler,
                                sample_interval_seconds=config.sample_interval_seconds,
                                gate_timeout_seconds=config.timeout_seconds,
                                request_results=request_results,
                                resource_samples=resource_samples,
                            )
                        )
                        await asyncio.sleep(config.settle_seconds)

            if config.mixed is not None:
                assignments = mixed_assignments(config.mixed, prepared_models)
                for repetition in range(1, config.mixed.repetitions + 1):
                    run_results.append(
                        await execute_burst(
                            client=client,
                            gateway_url=config.gateway_url,
                            assignments=assignments,
                            phase="mixed",
                            repetition=repetition,
                            metrics_client=metrics_client,
                            host_sampler=host_sampler,
                            sample_interval_seconds=config.sample_interval_seconds,
                            gate_timeout_seconds=config.timeout_seconds,
                            request_results=request_results,
                            resource_samples=resource_samples,
                        )
                    )
                    await asyncio.sleep(config.settle_seconds)

            final_metrics = await metrics_client.fetch()
            if final_metrics and not _metric_is_clean(final_metrics):
                raise RuntimeError(
                    "Model Manager did not finish ready with an empty queue and zero active reservation"
                )
    finally:
        await metrics_client.close()
    return (
        prepared_models,
        request_results,
        resource_samples,
        run_results,
        final_metrics if "final_metrics" in locals() else {},
    )


def summarize_results(
    request_results: list[RequestResult],
    resource_samples: list[ResourceSample],
    run_results: list[RunResult],
) -> list[dict[str, Any]]:
    runs_by_group: dict[tuple[str, str, int], list[RunResult]] = defaultdict(list)
    requests_by_group: dict[tuple[str, str, int], list[RequestResult]] = defaultdict(list)
    samples_by_run: dict[str, list[ResourceSample]] = defaultdict(list)
    for run in run_results:
        if run.phase != "warmup":
            runs_by_group[(run.phase, run.model, run.concurrency)].append(run)
    for request in request_results:
        if request.phase != "warmup":
            group_model = "mixed" if request.phase == "mixed" else request.model
            requests_by_group[(request.phase, group_model, request.concurrency)].append(request)
    for sample in resource_samples:
        samples_by_run[sample.run_id].append(sample)

    summary = []
    for group in sorted(runs_by_group):
        runs = runs_by_group[group]
        requests = requests_by_group[group]
        successful = [request for request in requests if request.success]
        latencies = [request.elapsed_seconds for request in successful]
        samples = [sample for run in runs for sample in samples_by_run[run.run_id]]
        duration = sum(run.duration_seconds for run in runs)
        summary.append(
            {
                "phase": group[0],
                "model": group[1],
                "concurrency": group[2],
                "repetitions": len(runs),
                "requests": len(requests),
                "successRate": round(len(successful) / len(requests), 6) if requests else 0,
                "throughputRequestsPerMinute": round(60 * len(requests) / duration, 3)
                if duration
                else None,
                "latencySeconds": {
                    "mean": round(sum(latencies) / len(latencies), 6) if latencies else None,
                    "median": _rounded(percentile(latencies, 50)),
                    "p95": _rounded(percentile(latencies, 95)),
                    "p99": _rounded(percentile(latencies, 99)),
                    "max": _rounded(max(latencies) if latencies else None),
                },
                "peakGpuUsedMiB": _rounded(_maximum(sample.gpu_used_mib for sample in samples)),
                "peakRamUsedMiB": _rounded(_maximum(sample.ram_used_mib for sample in samples)),
                "peakCpuPercent": _rounded(_maximum(sample.cpu_percent for sample in samples)),
                "peakQueueDepth": _maximum(sample.manager_queue_depth for sample in samples),
                "peakManagerReservedMiB": _maximum(
                    sample.manager_reserved_mib for sample in samples
                ),
                "queueWaitSeconds": operation_duration_summary(runs, "queue_wait", "admitted"),
                "statusCodes": _count_values(request.status_code for request in requests),
                "errorCodes": _count_values(request.error_code for request in requests),
                "exceptions": _count_values(request.exception_type for request in requests),
            }
        )
    return summary


def _rounded(value: float | None) -> float | None:
    return round(value, 6) if value is not None else None


def _maximum(values: Iterable[float | int | None]) -> float | int | None:
    present = [value for value in values if value is not None]
    return max(present) if present else None


def _count_values(values: Iterable[Any]) -> dict[str, int]:
    counts: dict[str, int] = defaultdict(int)
    for value in values:
        if value is not None:
            counts[str(value)] += 1
    return dict(sorted(counts.items()))


def operation_duration_summary(
    runs: Iterable[RunResult], operation: str, outcome: str
) -> dict[str, float | int | None]:
    metric_key = f"operation={operation},outcome={outcome}"
    count = 0
    sum_milliseconds = 0.0
    buckets: dict[float, int] = defaultdict(int)
    infinity_count = 0
    for run in runs:
        count += int(
            run.metric_deltas.get("model_manager_duration_seconds_count", {}).get(metric_key, 0)
        )
        sum_milliseconds += float(
            run.metric_deltas.get("model_manager_duration_milliseconds_sum", {}).get(
                metric_key, 0
            )
        )
        for key, value in run.metric_deltas.get(
            "model_manager_duration_seconds_bucket", {}
        ).items():
            prefix = f"{metric_key},le="
            if not key.startswith(prefix):
                continue
            boundary = key.removeprefix(prefix)
            if boundary == "+Inf":
                infinity_count += int(value)
            else:
                try:
                    buckets[float(boundary)] += int(value)
                except ValueError:
                    continue

    p95_upper_bound = None
    observations = count or infinity_count
    if observations:
        rank = math.ceil(observations * 0.95)
        for boundary, bucket_count in sorted(buckets.items()):
            if bucket_count >= rank:
                p95_upper_bound = boundary
                break
    return {
        "count": count,
        "mean": round(sum_milliseconds / count / 1000, 6) if count else None,
        "p95UpperBound": p95_upper_bound,
    }


def host_metadata() -> dict[str, Any]:
    cpu_model = "unknown"
    try:
        for line in Path("/proc/cpuinfo").read_text().splitlines():
            if line.startswith("model name"):
                cpu_model = line.split(":", 1)[1].strip()
                break
    except OSError:
        pass
    memory_total_mib = None
    try:
        mem_total_line = next(
            line
            for line in Path("/proc/meminfo").read_text().splitlines()
            if line.startswith("MemTotal:")
        )
        memory_total_mib = round(int(mem_total_line.split()[1]) / 1024, 3)
    except (OSError, StopIteration, ValueError, IndexError):
        pass
    return {
        "hostname": platform.node(),
        "platform": platform.platform(),
        "pythonVersion": platform.python_version(),
        "cpuModel": cpu_model,
        "logicalCpuCount": os.cpu_count(),
        "systemMemoryMiB": memory_total_mib,
        "gitCommit": _git_commit(),
    }


def _git_commit() -> str | None:
    try:
        return subprocess.run(
            ["git", "rev-parse", "HEAD"],
            cwd=REPOSITORY_ROOT,
            check=True,
            capture_output=True,
            text=True,
            timeout=5,
        ).stdout.strip()
    except (OSError, subprocess.SubprocessError):
        return None


def sanitized_run_config(
    config: BenchmarkConfig,
    prepared_models: dict[str, PreparedModel],
    gateway_token_env: str,
    metrics_password_env: str,
) -> dict[str, Any]:
    return {
        "createdAt": utc_now(),
        "deploymentName": config.deployment_name,
        "loadGeneratorLocation": config.load_generator_location,
        "notes": config.notes,
        "gatewayUrl": config.gateway_url,
        "metricsUrl": config.metrics_url,
        "tenantId": config.tenant_id,
        "gatewayTokenEnv": gateway_token_env,
        "metricsPasswordEnv": metrics_password_env,
        "concurrencyLevels": list(config.concurrency_levels),
        "repetitions": config.repetitions,
        "timeoutSeconds": config.timeout_seconds,
        "sampleIntervalSeconds": config.sample_interval_seconds,
        "requireColdStart": config.require_cold_start,
        "localResourceSampling": config.local_resource_sampling,
        "models": [
            {
                "name": prepared.config.name,
                "containerRef": prepared.config.container_ref,
                "fixtureName": prepared.config.fixture_name,
                "outputMode": prepared.config.output_mode,
                "groupSeries": prepared.config.group_series,
                "metadataOnly": prepared.config.metadata_only,
                "dicomFileCount": prepared.dicom_file_count,
                "payloadBytes": prepared.payload_bytes,
                "coldStartVerified": config.require_cold_start,
            }
            for prepared in prepared_models.values()
        ],
        "mixed": asdict(config.mixed) if config.mixed is not None else None,
        "serverConfiguration": config.server_configuration,
        "loadGeneratorHost": host_metadata(),
    }


def render_report(run_config: dict[str, Any], summary: list[dict[str, Any]]) -> str:
    lines = [
        "# PACS-AI Concurrent Inference Benchmark",
        "",
        f"- Deployment: `{run_config['deploymentName']}`",
        f"- Load generator: `{run_config['loadGeneratorLocation']}`",
        f"- Revision: `{run_config['loadGeneratorHost'].get('gitCommit') or 'unknown'}`",
        f"- Generated: `{run_config['createdAt']}`",
        "",
        "## Method",
        "",
        "Cold starts were measured separately. Warm scenarios used synchronized request bursts through the managed inference gateway. Each configured model retained its declared one-inference semaphore; queued latency is therefore part of the end-to-end result.",
        "",
        "No prediction content, tokens, DICOM paths, or patient metadata were written to this result bundle.",
        "",
        "## Results",
        "",
        "| Phase | Model | Users | Success | Req/min | Median (s) | p95 (s) | p99 (s) | Max (s) | Queue mean (s) | Queue p95 <= (s) | Peak VRAM MiB | Peak queue |",
        "| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for row in summary:
        latency = row["latencySeconds"]
        lines.append(
            "| {phase} | {model} | {concurrency} | {success:.1%} | {throughput} | {median} | {p95} | {p99} | {maximum} | {queue_mean} | {queue_p95} | {vram} | {queue} |".format(
                phase=row["phase"],
                model=row["model"],
                concurrency=row["concurrency"],
                success=row["successRate"],
                throughput=_display(row["throughputRequestsPerMinute"]),
                median=_display(latency["median"]),
                p95=_display(latency["p95"]),
                p99=_display(latency["p99"]),
                maximum=_display(latency["max"]),
                queue_mean=_display(row["queueWaitSeconds"]["mean"]),
                queue_p95=_display(row["queueWaitSeconds"]["p95UpperBound"]),
                vram=_display(row["peakGpuUsedMiB"]),
                queue=_display(row["peakQueueDepth"]),
            )
        )
    lines.extend(
        [
            "",
            "## Environment",
            "",
            f"- Load-generator platform: `{run_config['loadGeneratorHost']['platform']}`",
            f"- Load-generator CPU: `{run_config['loadGeneratorHost']['cpuModel']}` ({run_config['loadGeneratorHost']['logicalCpuCount']} logical CPUs)",
            f"- Load-generator memory: `{_display(run_config['loadGeneratorHost']['systemMemoryMiB'])} MiB`",
            f"- Network placement: `{run_config['loadGeneratorLocation']}`",
        ]
    )
    for key, value in sorted(run_config.get("serverConfiguration", {}).items()):
        lines.append(f"- PACS-AI {key}: `{value}`")
    lines.extend(
        [
            "",
            "## Limitations",
            "",
            "These measurements characterize infrastructure behavior and do not evaluate clinical accuracy. Reused de-identified fixtures control input variability but do not represent the full distribution of clinical study sizes.",
        ]
    )
    if run_config.get("notes"):
        lines.extend(["", "## Notes", "", str(run_config["notes"])])
    return "\n".join(lines) + "\n"


def _display(value: Any) -> str:
    return "n/a" if value is None else str(value)


def write_results(
    output_dir: Path,
    run_config: dict[str, Any],
    request_results: list[RequestResult],
    resource_samples: list[ResourceSample],
    run_results: list[RunResult],
    final_metrics: dict[str, Any],
) -> list[dict[str, Any]]:
    output_dir.mkdir(parents=True, exist_ok=False)
    summary = summarize_results(request_results, resource_samples, run_results)
    (output_dir / "run-config.json").write_text(
        json.dumps(run_config, indent=2, sort_keys=True) + "\n"
    )
    with (output_dir / "requests.csv").open("w", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=RESULT_FIELDS)
        writer.writeheader()
        writer.writerows(asdict(result) for result in request_results)
    with (output_dir / "samples.csv").open("w", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=SAMPLE_FIELDS)
        writer.writeheader()
        writer.writerows(asdict(sample) for sample in resource_samples)
    (output_dir / "summary.json").write_text(
        json.dumps(
            {
                "summary": summary,
                "runs": [asdict(run) for run in run_results],
                "finalManagerMetrics": {
                    key: final_metrics.get(key)
                    for key in (
                        "model_manager_ready",
                        "model_manager_models",
                        "model_manager_loaded_models",
                        "model_manager_gpu_memory_mib",
                        "model_manager_queue_depth",
                    )
                    if key in final_metrics
                },
            },
            indent=2,
            sort_keys=True,
        )
        + "\n"
    )
    (output_dir / "report.md").write_text(render_report(run_config, summary))
    return summary


def default_output_dir() -> Path:
    timestamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    return REPOSITORY_ROOT / "benchmark-results" / timestamp


async def async_main(args: argparse.Namespace) -> int:
    config = load_config(args.config)
    gateway_token = os.environ.get(args.gateway_token_env, "")
    if not gateway_token:
        raise ValueError(f"{args.gateway_token_env} is not set")
    metrics_password = os.environ.get(args.metrics_password_env)
    if config.metrics_url and not metrics_password:
        raise ValueError(f"{args.metrics_password_env} is not set")

    (
        prepared_models,
        request_results,
        resource_samples,
        run_results,
        final_metrics,
    ) = await run_benchmark(
        config,
        gateway_token=gateway_token,
        metrics_username=args.metrics_username,
        metrics_password=metrics_password,
    )
    run_config = sanitized_run_config(
        config,
        prepared_models,
        args.gateway_token_env,
        args.metrics_password_env,
    )
    output_dir = args.output_dir or default_output_dir()
    summary = write_results(
        output_dir,
        run_config,
        request_results,
        resource_samples,
        run_results,
        final_metrics,
    )
    print(json.dumps({"outputDirectory": str(output_dir), "summary": summary}, indent=2))
    return 0 if all(row["successRate"] == 1 for row in summary) else 2


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path)
    parser.add_argument("--gateway-token-env", default="STUDY_SERVICE_CALLBACK_TOKEN")
    parser.add_argument("--metrics-username", default="sudo")
    parser.add_argument("--metrics-password-env", default="OPENAPI_DOCS_PASSWORD")
    return parser.parse_args()


def main() -> int:
    try:
        return asyncio.run(async_main(parse_args()))
    except (ValueError, RuntimeError, TimeoutError, httpx.HTTPError) as exc:
        print(f"benchmark failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
