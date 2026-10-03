from __future__ import annotations

import asyncio
import os
import signal
from collections.abc import Callable
from contextlib import asynccontextmanager
from datetime import datetime, timezone
from enum import Enum
from typing import Any

from fastapi import BackgroundTasks, FastAPI
from utils.http_utils import HTTPResponse

MODEL_ACTIVITY_PATHS = frozenset(
    {
        "/inference/model/load",
        "/inference/predict",
    }
)


def records_model_activity(path: str) -> bool:
    return path in MODEL_ACTIVITY_PATHS


class ModelRuntimeState(str, Enum):
    UNLOADED = "UNLOADED"
    LOADING = "LOADING"
    READY = "READY"
    BUSY = "BUSY"
    EVICTING = "EVICTING"
    ERROR = "ERROR"


class ModelLifecycleError(RuntimeError):
    pass


class ModelLifecycle:
    """Concurrency-safe runtime state for one supervised inference process."""

    def __init__(
        self,
        service: Any,
        config: Any,
        inference_semaphore: asyncio.Semaphore,
        *,
        now: Callable[[], datetime] | None = None,
        process_terminator: Callable[[int, int], None] = os.kill,
        pid_getter: Callable[[], int] = os.getpid,
        restart_delay_seconds: float = 0.1,
    ):
        self.service = service
        self.config = config
        self.inference_semaphore = inference_semaphore
        self._now = now or (lambda: datetime.now(tz=timezone.utc))
        self._process_terminator = process_terminator
        self._pid_getter = pid_getter
        self._restart_delay_seconds = restart_delay_seconds
        self._state_lock = asyncio.Lock()
        self._active_requests = 0
        self._last_used_at: datetime | None = None
        self._last_error: str | None = None
        self._state = (
            ModelRuntimeState.READY if self._is_loaded() else ModelRuntimeState.UNLOADED
        )

    def _is_loaded(self) -> bool:
        return bool(getattr(self.service, "is_initialized", False))

    def _snapshot_locked(self) -> dict[str, Any]:
        snapshot = {
            "state": self._state.value,
            "loaded": self._is_loaded(),
            "activeRequests": self._active_requests,
            "lastUsedAt": (
                self._last_used_at.isoformat().replace("+00:00", "Z")
                if self._last_used_at is not None
                else None
            ),
        }
        if self._last_error is not None:
            snapshot["error"] = self._last_error
        return snapshot

    def _reconcile_locked(self) -> None:
        if self._state in {
            ModelRuntimeState.LOADING,
            ModelRuntimeState.EVICTING,
            ModelRuntimeState.ERROR,
        }:
            if self._state == ModelRuntimeState.LOADING and self._is_loaded():
                self._state = ModelRuntimeState.BUSY
            return
        if self._active_requests > 0:
            self._state = (
                ModelRuntimeState.BUSY
                if self._is_loaded()
                else ModelRuntimeState.LOADING
            )
        else:
            self._state = (
                ModelRuntimeState.READY
                if self._is_loaded()
                else ModelRuntimeState.UNLOADED
            )

    async def runtime(self) -> dict[str, Any]:
        async with self._state_lock:
            self._reconcile_locked()
            return self._snapshot_locked()

    async def load(self) -> dict[str, Any]:
        async with self.inference_semaphore:
            async with self._state_lock:
                if self._is_loaded():
                    self._state = ModelRuntimeState.READY
                    self._last_error = None
                    self._last_used_at = self._now()
                    return self._snapshot_locked()
                self._state = ModelRuntimeState.LOADING
                self._last_error = None

            try:
                self.service.load_model(self.config)
                if not self._is_loaded():
                    raise RuntimeError("Model service did not report a loaded state")
            except Exception as exc:
                async with self._state_lock:
                    self._state = ModelRuntimeState.ERROR
                    self._last_error = str(exc)
                raise ModelLifecycleError(f"Failed to load model: {exc}") from exc

            async with self._state_lock:
                self._state = ModelRuntimeState.READY
                self._last_used_at = self._now()
                return self._snapshot_locked()

    @asynccontextmanager
    async def inference(self):
        await self.inference_semaphore.acquire()
        entered = False
        failed: BaseException | None = None
        try:
            async with self._state_lock:
                self._active_requests += 1
                entered = True
                self._last_error = None
                self._state = (
                    ModelRuntimeState.BUSY
                    if self._is_loaded()
                    else ModelRuntimeState.LOADING
                )

            try:
                yield
            except BaseException as exc:
                failed = exc
                raise
        finally:
            try:
                if entered:
                    cleanup = asyncio.create_task(self._finish_inference(failed))
                    try:
                        await asyncio.shield(cleanup)
                    except asyncio.CancelledError:
                        await cleanup
                        raise
            finally:
                self.inference_semaphore.release()

    async def _finish_inference(self, failed: BaseException | None) -> None:
        async with self._state_lock:
            self._active_requests -= 1
            if self._is_loaded():
                self._last_used_at = self._now()
                if failed is None:
                    self._state = ModelRuntimeState.READY
                else:
                    self._state = ModelRuntimeState.ERROR
                    self._last_error = str(failed)
            else:
                self._state = ModelRuntimeState.ERROR
                self._last_error = self._last_error or "Model did not load for inference"

    async def unload(self) -> tuple[dict[str, Any], bool]:
        async with self.inference_semaphore:
            async with self._state_lock:
                restart_required = self._is_loaded() or self._state == ModelRuntimeState.ERROR
                if not restart_required:
                    self._state = ModelRuntimeState.UNLOADED
                    self._last_error = None
                    return self._snapshot_locked(), False
                self._state = ModelRuntimeState.EVICTING
                self._last_error = None

            cleanup_error = None
            try:
                self.service.stop_model()
            except Exception as exc:
                cleanup_error = str(exc)
            finally:
                service_type = type(self.service)
                if hasattr(service_type, "is_initialized"):
                    service_type.is_initialized = False
                else:
                    self.service.is_initialized = False

            async with self._state_lock:
                self._state = ModelRuntimeState.UNLOADED
                if cleanup_error is not None:
                    self._last_error = f"In-process cleanup failed; restart scheduled: {cleanup_error}"
                return self._snapshot_locked(), True

    async def restart_process(self) -> None:
        if self._restart_delay_seconds > 0:
            await asyncio.sleep(self._restart_delay_seconds)
        self._process_terminator(self._pid_getter(), signal.SIGTERM)


def install_model_lifecycle_routes(app: FastAPI, lifecycle: ModelLifecycle) -> None:
    async def get_runtime():
        return HTTPResponse(
            status=200,
            success=True,
            message="Model runtime retrieved successfully",
            data=await lifecycle.runtime(),
        ).to_response()

    async def load_model():
        try:
            runtime = await lifecycle.load()
        except ModelLifecycleError as exc:
            return HTTPResponse(
                status=500,
                success=False,
                message=str(exc),
                error_code="MODEL_LOAD_ERROR",
                data=await lifecycle.runtime(),
            ).to_response()
        return HTTPResponse(
            status=200,
            success=True,
            message="Model loaded successfully",
            data=runtime,
        ).to_response()

    async def unload_model(background_tasks: BackgroundTasks):
        runtime, restart_required = await lifecycle.unload()
        if restart_required:
            background_tasks.add_task(lifecycle.restart_process)
        response = HTTPResponse(
            status=200,
            success=True,
            message=(
                "Model unloaded; inference process restart scheduled"
                if restart_required
                else "Model is already unloaded"
            ),
            data=runtime,
        ).to_response()
        if restart_required:
            response.background = background_tasks
        return response

    app.add_api_route(
        "/inference/model/runtime",
        get_runtime,
        methods=["GET"],
        name="get_model_runtime",
    )
    app.add_api_route(
        "/inference/model/load",
        load_model,
        methods=["POST"],
        name="load_model",
    )
    app.add_api_route(
        "/inference/model/unload",
        unload_model,
        methods=["POST"],
        name="unload_model",
    )
