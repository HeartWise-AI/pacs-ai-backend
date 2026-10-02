import asyncio
import importlib.util
import signal
import sys
import unittest
from datetime import datetime, timezone
from pathlib import Path

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
TEMPLATE_ROOT = REPOSITORY_ROOT / "model-template"
LIFECYCLE_MODULE_PATH = TEMPLATE_ROOT / "utils" / "model_lifecycle.py"


def load_lifecycle_module():
    spec = importlib.util.spec_from_file_location(
        "pacs_ai_model_lifecycle", LIFECYCLE_MODULE_PATH
    )
    if spec is None or spec.loader is None:
        raise RuntimeError(f"Unable to load {LIFECYCLE_MODULE_PATH}")
    module = importlib.util.module_from_spec(spec)
    sys.path.insert(0, str(TEMPLATE_ROOT))
    try:
        spec.loader.exec_module(module)
    finally:
        sys.path.remove(str(TEMPLATE_ROOT))
    return module


LIFECYCLE = load_lifecycle_module()


class ModelServiceDouble:
    def __init__(self, *, loaded=False):
        self.is_initialized = loaded
        self.load_calls = 0
        self.stop_calls = 0
        self.fail_next_load = False

    def load_model(self, _config):
        self.load_calls += 1
        if self.fail_next_load:
            self.fail_next_load = False
            raise RuntimeError("load failed")
        self.is_initialized = True

    def stop_model(self):
        self.stop_calls += 1
        self.is_initialized = False


class ClassStateModelServiceDouble:
    is_initialized = True

    def __init__(self):
        self.load_calls = 0
        self.stop_calls = 0

    def load_model(self, _config):
        self.load_calls += 1
        type(self).is_initialized = True

    def stop_model(self):
        self.stop_calls += 1
        type(self).is_initialized = False


class ModelLifecycleTests(unittest.TestCase):
    def make_lifecycle(self, service=None, **overrides):
        service = service or ModelServiceDouble()
        return LIFECYCLE.ModelLifecycle(
            service,
            object(),
            asyncio.Semaphore(1),
            restart_delay_seconds=0,
            **overrides,
        )

    def test_runtime_starts_unloaded_without_recording_activity(self):
        async def exercise():
            lifecycle = self.make_lifecycle()
            first = await lifecycle.runtime()
            second = await lifecycle.runtime()
            return first, second

        first, second = asyncio.run(exercise())
        self.assertEqual("UNLOADED", first["state"])
        self.assertFalse(first["loaded"])
        self.assertEqual(0, first["activeRequests"])
        self.assertIsNone(first["lastUsedAt"])
        self.assertEqual(first, second)

    def test_load_is_idempotent_and_records_successful_use(self):
        async def exercise():
            service = ModelServiceDouble()
            used_at = datetime(2026, 10, 2, 12, 0, tzinfo=timezone.utc)
            timestamps = iter((used_at, used_at.replace(minute=1)))
            lifecycle = self.make_lifecycle(service, now=lambda: next(timestamps))
            first = await lifecycle.load()
            second = await lifecycle.load()
            return service, first, second

        service, first, second = asyncio.run(exercise())
        self.assertEqual(1, service.load_calls)
        self.assertEqual("READY", first["state"])
        self.assertTrue(first["loaded"])
        self.assertEqual("2026-10-02T12:00:00Z", first["lastUsedAt"])
        self.assertEqual("2026-10-02T12:01:00Z", second["lastUsedAt"])

    def test_inference_uses_the_shared_semaphore_and_tracks_runtime(self):
        async def exercise():
            service = ModelServiceDouble(loaded=True)
            lifecycle = self.make_lifecycle(service)
            active = 0
            max_active = 0
            observed_states = []

            async def predict():
                nonlocal active, max_active
                async with lifecycle.inference():
                    active += 1
                    max_active = max(max_active, active)
                    observed_states.append((await lifecycle.runtime())["state"])
                    await asyncio.sleep(0.01)
                    active -= 1

            await asyncio.gather(predict(), predict())
            return max_active, observed_states, await lifecycle.runtime()

        max_active, observed_states, runtime = asyncio.run(exercise())
        self.assertEqual(1, max_active)
        self.assertEqual(["BUSY", "BUSY"], observed_states)
        self.assertEqual("READY", runtime["state"])
        self.assertIsNotNone(runtime["lastUsedAt"])

    def test_unload_waits_for_active_inference(self):
        async def exercise():
            service = ModelServiceDouble(loaded=True)
            lifecycle = self.make_lifecycle(service)
            entered = asyncio.Event()
            release = asyncio.Event()

            async def predict():
                async with lifecycle.inference():
                    entered.set()
                    await release.wait()

            prediction = asyncio.create_task(predict())
            await entered.wait()
            unloading = asyncio.create_task(lifecycle.unload())
            await asyncio.sleep(0)
            waiting = not unloading.done()
            release.set()
            await prediction
            runtime, restart_required = await unloading
            return service, waiting, runtime, restart_required

        service, waiting, runtime, restart_required = asyncio.run(exercise())
        self.assertTrue(waiting)
        self.assertEqual(1, service.stop_calls)
        self.assertTrue(restart_required)
        self.assertEqual("UNLOADED", runtime["state"])
        self.assertFalse(runtime["loaded"])

    def test_unload_is_idempotent_when_already_unloaded(self):
        async def exercise():
            service = ModelServiceDouble()
            lifecycle = self.make_lifecycle(service)
            result = await lifecycle.unload()
            return service, result

        service, (runtime, restart_required) = asyncio.run(exercise())
        self.assertEqual(0, service.stop_calls)
        self.assertFalse(restart_required)
        self.assertEqual("UNLOADED", runtime["state"])

    def test_model_can_load_again_after_unload(self):
        async def exercise():
            ClassStateModelServiceDouble.is_initialized = True
            service = ClassStateModelServiceDouble()
            lifecycle = self.make_lifecycle(service)
            await lifecycle.unload()
            loaded = await lifecycle.load()
            return service, loaded

        service, loaded = asyncio.run(exercise())
        self.assertEqual(1, service.stop_calls)
        self.assertEqual(1, service.load_calls)
        self.assertEqual("READY", loaded["state"])
        self.assertTrue(loaded["loaded"])

    def test_load_failure_enters_error_and_can_recover(self):
        async def exercise():
            service = ModelServiceDouble()
            service.fail_next_load = True
            lifecycle = self.make_lifecycle(service)
            with self.assertRaises(LIFECYCLE.ModelLifecycleError):
                await lifecycle.load()
            failed = await lifecycle.runtime()
            recovered = await lifecycle.load()
            return failed, recovered

        failed, recovered = asyncio.run(exercise())
        self.assertEqual("ERROR", failed["state"])
        self.assertIn("load failed", failed["error"])
        self.assertEqual("READY", recovered["state"])
        self.assertNotIn("error", recovered)

    def test_cancelled_inference_releases_its_lease(self):
        async def exercise():
            service = ModelServiceDouble(loaded=True)
            lifecycle = self.make_lifecycle(service)
            entered = asyncio.Event()

            async def predict():
                async with lifecycle.inference():
                    entered.set()
                    await asyncio.Event().wait()

            prediction = asyncio.create_task(predict())
            await entered.wait()
            prediction.cancel()
            with self.assertRaises(asyncio.CancelledError):
                await prediction
            loaded = await asyncio.wait_for(lifecycle.load(), timeout=0.5)
            runtime = await lifecycle.runtime()
            return loaded, runtime

        loaded, runtime = asyncio.run(exercise())
        self.assertEqual("READY", loaded["state"])
        self.assertEqual(0, runtime["activeRequests"])

    def test_activity_filter_excludes_metadata_and_status_requests(self):
        self.assertTrue(LIFECYCLE.records_model_activity("/inference/predict"))
        self.assertTrue(LIFECYCLE.records_model_activity("/inference/model/load"))
        self.assertFalse(LIFECYCLE.records_model_activity("/inference/model/runtime"))
        self.assertFalse(LIFECYCLE.records_model_activity("/inference/model-info"))
        self.assertFalse(LIFECYCLE.records_model_activity("/docs"))

    def test_process_restart_terminates_the_current_process(self):
        async def exercise():
            calls = []
            lifecycle = self.make_lifecycle(
                process_terminator=lambda pid, process_signal: calls.append(
                    (pid, process_signal)
                ),
                pid_getter=lambda: 1234,
            )
            await lifecycle.restart_process()
            return calls

        calls = asyncio.run(exercise())
        self.assertEqual([(1234, signal.SIGTERM)], calls)

    def test_routes_are_installed_with_expected_methods(self):
        app = LIFECYCLE.FastAPI()
        LIFECYCLE.install_model_lifecycle_routes(app, self.make_lifecycle())
        routes = {
            route.path: route.methods
            for route in app.routes
            if route.path.startswith("/inference/model/")
        }
        self.assertEqual({"GET"}, routes["/inference/model/runtime"])
        self.assertEqual({"POST"}, routes["/inference/model/load"])
        self.assertEqual({"POST"}, routes["/inference/model/unload"])

    def test_unload_route_schedules_restart_after_responding(self):
        async def exercise():
            calls = []
            service = ModelServiceDouble(loaded=True)
            lifecycle = self.make_lifecycle(
                service,
                process_terminator=lambda pid, process_signal: calls.append(
                    (pid, process_signal)
                ),
                pid_getter=lambda: 1234,
            )
            app = LIFECYCLE.FastAPI()
            LIFECYCLE.install_model_lifecycle_routes(app, lifecycle)
            unload_endpoint = next(
                route.endpoint
                for route in app.routes
                if route.path == "/inference/model/unload"
            )
            background_tasks = LIFECYCLE.BackgroundTasks()
            response = await unload_endpoint(background_tasks)
            calls_before_response_completion = list(calls)
            await response.background()
            return response, background_tasks, calls_before_response_completion, calls

        response, background_tasks, before, after = asyncio.run(exercise())
        self.assertEqual(200, response.status_code)
        self.assertIs(background_tasks, response.background)
        self.assertEqual(1, len(background_tasks.tasks))
        self.assertEqual([], before)
        self.assertEqual([(1234, signal.SIGTERM)], after)


if __name__ == "__main__":
    unittest.main()
