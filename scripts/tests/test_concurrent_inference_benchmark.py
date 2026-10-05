import asyncio
import contextlib
import importlib.util
import json
import sys
import tempfile
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from unittest import mock

import httpx
import pydicom
from pydicom.dataset import FileDataset, FileMetaDataset
from pydicom.uid import ExplicitVRLittleEndian, SecondaryCaptureImageStorage, generate_uid

from scripts import benchmark_concurrent_inference as benchmark


def load_request_tester():
    model_template = benchmark.REPOSITORY_ROOT / "model-template"
    path = model_template / "request_tester.py"
    spec = importlib.util.spec_from_file_location("request_tester_under_test", path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load {path}")
    module = importlib.util.module_from_spec(spec)
    sys.path.insert(0, str(model_template))
    try:
        spec.loader.exec_module(module)
    finally:
        sys.path.remove(str(model_template))
    return module


def write_dicom(path: Path, instance_number: int) -> None:
    file_meta = FileMetaDataset()
    file_meta.MediaStorageSOPClassUID = SecondaryCaptureImageStorage
    file_meta.MediaStorageSOPInstanceUID = generate_uid()
    file_meta.TransferSyntaxUID = ExplicitVRLittleEndian

    dataset = FileDataset(str(path), {}, file_meta=file_meta, preamble=b"\0" * 128)
    dataset.SOPClassUID = SecondaryCaptureImageStorage
    dataset.SOPInstanceUID = file_meta.MediaStorageSOPInstanceUID
    dataset.StudyInstanceUID = generate_uid()
    dataset.SeriesInstanceUID = generate_uid()
    dataset.InstanceNumber = instance_number
    dataset.PatientID = "must-not-appear-in-results"
    dataset.PatientAge = "040Y"
    dataset.Rows = 2
    dataset.Columns = 2
    dataset.SamplesPerPixel = 1
    dataset.PhotometricInterpretation = "MONOCHROME2"
    dataset.BitsAllocated = 8
    dataset.BitsStored = 8
    dataset.HighBit = 7
    dataset.PixelRepresentation = 0
    dataset.PixelData = bytes([0, 1, 2, 3])
    pydicom.dcmwrite(path, dataset, write_like_original=False)


def prepared_model(name: str = "TestModel", container_ref: str = "TestModel"):
    config = benchmark.ModelConfig(
        name=name,
        container_ref=container_ref,
        fixture_name="fixture",
        dicom_paths=(Path("unused.dcm"),),
    )
    return benchmark.PreparedModel(
        config=config,
        gateway_payload=json.dumps(
            {
                "tenantId": "tenant",
                "containerRef": container_ref,
                "request": {
                    "seriesInstanceImages": {"1": {"1": "ZGljb20="}},
                    "outputMode": "JSON",
                },
            }
        ).encode(),
        payload_bytes=10,
        dicom_file_count=1,
    )


class PayloadTests(unittest.TestCase):
    def test_collects_stably_and_groups_payload_without_reusing_random_keys(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            write_dicom(root / "b.dcm", 2)
            write_dicom(root / "a.dcm", 1)

            paths = benchmark.REQUEST_PAYLOAD.collect_dicom_files([root])
            payload = benchmark.REQUEST_PAYLOAD.build_request_payload(
                paths, output_mode="JSON", group_series=True
            )

        self.assertEqual(["a.dcm", "b.dcm"], [path.name for path in paths])
        self.assertEqual({"1", "2"}, set(payload["seriesInstanceImages"][1]))
        self.assertNotIn("seriesInstanceMetadata", payload)

    def test_metadata_only_payload_omits_full_dicom_images(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "image.dcm"
            write_dicom(path, 1)
            payload = benchmark.REQUEST_PAYLOAD.build_request_payload(
                [path], send_metadata_only=True
            )

        self.assertIn("seriesInstanceMetadata", payload)
        self.assertNotIn("seriesInstanceImages", payload)
        self.assertEqual(
            "040Y", payload["seriesInstanceMetadata"][1]["1"]["00101010"]["Value"][0]
        )

    def test_existing_request_tester_posts_the_shared_payload(self):
        tester = load_request_tester()
        response = object()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "image.dcm"
            write_dicom(path, 1)
            with mock.patch.object(tester.requests, "post", return_value=response) as post:
                returned = tester.send_dicom_data(str(path), "http://model.test/predict")

        self.assertIs(response, returned)
        call = post.call_args
        self.assertEqual("http://model.test/predict", call.args[0])
        self.assertIn("seriesInstanceImages", call.kwargs["json"])
        self.assertEqual(500, call.kwargs["timeout"])

    def test_existing_request_tester_skips_an_invalid_dicom(self):
        tester = load_request_tester()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            valid_path = root / "valid.dcm"
            invalid_path = root / "invalid.dcm"
            write_dicom(valid_path, 1)
            invalid_path.write_text("not a DICOM file")
            with (
                mock.patch.object(tester.requests, "post", return_value=object()) as post,
                mock.patch("builtins.print") as output,
            ):
                tester.send_dicom_data(
                    [str(valid_path), str(invalid_path)],
                    "http://model.test/predict",
                )

        images = post.call_args.kwargs["json"]["seriesInstanceImages"]
        self.assertEqual(1, sum(len(series) for series in images.values()))
        self.assertTrue(any("invalid.dcm" in str(call) for call in output.call_args_list))


class CalculationTests(unittest.TestCase):
    def test_percentiles_use_linear_interpolation_and_support_singletons(self):
        self.assertEqual(4, benchmark.percentile([4], 99))
        self.assertEqual(2.5, benchmark.percentile([1, 2, 3, 4], 50))
        self.assertAlmostEqual(3.85, benchmark.percentile([1, 2, 3, 4], 95))
        self.assertIsNone(benchmark.percentile([], 95))

    def test_metric_delta_keeps_only_changed_numeric_bounded_series(self):
        self.assertEqual(
            {"success": 2, "failed": 1},
            benchmark.numeric_map_delta(
                {"success": 4, "unchanged": 8},
                {"success": 6, "failed": 1, "unchanged": 8},
            ),
        )

    def test_operation_duration_summary_uses_manager_histogram_deltas(self):
        run = benchmark.RunResult(
            run_id="warm-model-c4-r1",
            phase="warm",
            model="Model",
            concurrency=4,
            repetition=1,
            started_at="2026-01-01T00:00:00+00:00",
            duration_seconds=1,
            metric_deltas={
                "model_manager_duration_seconds_count": {
                    "operation=queue_wait,outcome=admitted": 4
                },
                "model_manager_duration_milliseconds_sum": {
                    "operation=queue_wait,outcome=admitted": 1000
                },
                "model_manager_duration_seconds_bucket": {
                    "operation=queue_wait,outcome=admitted,le=0.5": 3,
                    "operation=queue_wait,outcome=admitted,le=1": 4,
                    "operation=queue_wait,outcome=admitted,le=+Inf": 4,
                },
            },
        )

        self.assertEqual(
            {"count": 4, "mean": 0.25, "p95UpperBound": 1.0},
            benchmark.operation_duration_summary([run], "queue_wait", "admitted"),
        )


class ConfigTests(unittest.TestCase):
    def test_rejects_mixed_distribution_that_does_not_sum_to_concurrency(self):
        raw = {
            "gatewayUrl": "http://example.test/internal/v1/inference/predict",
            "tenantId": "tenant",
            "concurrencyLevels": [1, 2],
            "requireColdStart": False,
            "models": [
                {
                    "name": "one",
                    "containerRef": "one",
                    "fixtureName": "fixture",
                    "dicomPaths": ["fixture.dcm"],
                }
            ],
            "mixed": {
                "concurrency": 2,
                "distribution": {"one": 1},
            },
        }
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "config.json"
            path.write_text(json.dumps(raw))
            with self.assertRaisesRegex(ValueError, "must sum"):
                benchmark.load_config(path)

    def test_cold_preflight_requires_manager_metrics(self):
        raw = {
            "gatewayUrl": "http://example.test/internal/v1/inference/predict",
            "tenantId": "tenant",
            "concurrencyLevels": [1],
            "models": [
                {
                    "name": "one",
                    "containerRef": "one",
                    "fixtureName": "fixture",
                    "dicomPaths": ["fixture.dcm"],
                    "lifecycleUrl": "http://one/api/inference",
                }
            ],
        }
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "config.json"
            path.write_text(json.dumps(raw))
            with self.assertRaisesRegex(ValueError, "metricsUrl is required"):
                benchmark.load_config(path)


class FakeGatewayHandler(BaseHTTPRequestHandler):
    active = 0
    max_active = 0
    lifecycle_posts = 0
    lock = threading.Lock()

    def log_message(self, _format, *_args):
        return

    def _write_json(self, status: int, body: dict) -> None:
        encoded = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        with contextlib.suppress(BrokenPipeError):
            self.wfile.write(encoded)

    def do_GET(self):
        if self.path == "/debug/vars":
            self._write_json(
                200,
                {
                    "model_manager_ready": 1,
                    "model_manager_loaded_models": 1,
                    "model_manager_queue_depth": 0,
                    "model_manager_gpu_memory_mib": {
                        "kind=capacity": 100,
                        "kind=resident": 10,
                        "kind=active": 0,
                        "kind=reserved": 10,
                        "kind=available": 90,
                    },
                    "model_manager_events_total": {},
                    "model_manager_duration_seconds_count": {},
                    "model_manager_duration_milliseconds_sum": {},
                },
            )
            return
        if self.path == "/api/inference/model/runtime":
            self._write_json(
                200,
                {
                    "success": True,
                    "data": {
                        "state": "UNLOADED",
                        "loaded": False,
                        "activeRequests": 0,
                    },
                },
            )
            return
        self._write_json(404, {"success": False})

    def do_POST(self):
        if self.path == "/api/inference/model/unload":
            type(self).lifecycle_posts += 1
            self._write_json(200, {"success": True})
            return
        length = int(self.headers.get("Content-Length", "0"))
        request = json.loads(self.rfile.read(length))
        with self.lock:
            type(self).active += 1
            type(self).max_active = max(type(self).max_active, type(self).active)
        try:
            time.sleep(0.05)
            if request.get("containerRef") == "FailModel":
                self._write_json(
                    503,
                    {
                        "success": False,
                        "errorCode": "QUEUE_TIMEOUT",
                        "data": {"private": "discarded"},
                    },
                )
            else:
                self._write_json(
                    200,
                    {"success": True, "data": {"prediction": "discarded"}},
                )
        finally:
            with self.lock:
                type(self).active -= 1


class BurstIntegrationTests(unittest.IsolatedAsyncioTestCase):
    @classmethod
    def setUpClass(cls):
        FakeGatewayHandler.active = 0
        FakeGatewayHandler.max_active = 0
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), FakeGatewayHandler)
        cls.thread = threading.Thread(target=cls.server.serve_forever, daemon=True)
        cls.thread.start()
        cls.base_url = f"http://127.0.0.1:{cls.server.server_port}"

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()
        cls.server.server_close()
        cls.thread.join()

    async def asyncSetUp(self):
        FakeGatewayHandler.max_active = 0
        FakeGatewayHandler.lifecycle_posts = 0
        self.metrics = benchmark.MetricsClient(f"{self.base_url}/debug/vars", "sudo", "password")

    async def asyncTearDown(self):
        await self.metrics.close()

    async def test_synchronized_burst_reaches_requested_concurrency(self):
        requests = []
        samples = []
        async with httpx.AsyncClient(timeout=2) as client:
            run = await benchmark.execute_burst(
                client=client,
                gateway_url=f"{self.base_url}/internal/v1/inference/predict",
                assignments=[prepared_model()] * 4,
                phase="warm",
                repetition=1,
                metrics_client=self.metrics,
                host_sampler=benchmark.HostSampler(False),
                sample_interval_seconds=0.01,
                gate_timeout_seconds=1,
                request_results=requests,
                resource_samples=samples,
            )

        self.assertEqual(4, FakeGatewayHandler.max_active)
        self.assertEqual(4, len(requests))
        self.assertTrue(all(request.success for request in requests))
        self.assertEqual(4, run.concurrency)
        self.assertTrue(samples)

    async def test_small_benchmark_runs_cold_warm_and_concurrent_phases(self):
        with tempfile.TemporaryDirectory() as directory:
            dicom_path = Path(directory) / "image.dcm"
            write_dicom(dicom_path, 1)
            config = benchmark.BenchmarkConfig(
                gateway_url=f"{self.base_url}/internal/v1/inference/predict",
                metrics_url=f"{self.base_url}/debug/vars",
                tenant_id="tenant",
                concurrency_levels=(1, 2),
                repetitions=1,
                timeout_seconds=2,
                sample_interval_seconds=0.01,
                settle_seconds=0,
                require_cold_start=False,
                local_resource_sampling=False,
                deployment_name="test",
                load_generator_location="loopback",
                notes="",
                models=(
                    benchmark.ModelConfig(
                        name="TestModel",
                        container_ref="TestModel",
                        fixture_name="fixture",
                        dicom_paths=(dicom_path,),
                    ),
                ),
                server_configuration={"gpu": "test"},
            )
            prepared, requests, samples, runs, final_metrics = await benchmark.run_benchmark(
                config,
                gateway_token="secret",
                metrics_username="sudo",
                metrics_password="password",
            )

        self.assertEqual({"TestModel"}, set(prepared))
        self.assertEqual(5, len(requests))
        self.assertEqual(["cold", "warmup", "warm", "warm"], [run.phase for run in runs])
        self.assertTrue(all(result.success for result in requests))
        self.assertTrue(samples)
        self.assertTrue(benchmark._metric_is_clean(final_metrics))

    async def test_failure_records_only_bounded_outcome_fields(self):
        requests = []
        async with httpx.AsyncClient(timeout=2) as client:
            await benchmark.execute_burst(
                client=client,
                gateway_url=f"{self.base_url}/internal/v1/inference/predict",
                assignments=[prepared_model("Failure", "FailModel")],
                phase="warm",
                repetition=1,
                metrics_client=self.metrics,
                host_sampler=benchmark.HostSampler(False),
                sample_interval_seconds=0.01,
                gate_timeout_seconds=1,
                request_results=requests,
                resource_samples=[],
            )

        self.assertFalse(requests[0].success)
        self.assertEqual(503, requests[0].status_code)
        self.assertEqual("QUEUE_TIMEOUT", requests[0].error_code)
        self.assertFalse(hasattr(requests[0], "response_body"))

    async def test_cold_preflight_is_read_only(self):
        model = prepared_model().config
        model = benchmark.ModelConfig(
            name=model.name,
            container_ref=model.container_ref,
            fixture_name=model.fixture_name,
            dicom_paths=model.dicom_paths,
            lifecycle_url=f"{self.base_url}/api/inference",
        )
        async with httpx.AsyncClient(timeout=2) as client:
            await benchmark.verify_unloaded_runtime(client, model)

        self.assertEqual(0, FakeGatewayHandler.lifecycle_posts)

    async def test_cancellation_cancels_in_flight_tasks_without_results(self):
        blocker = asyncio.Event()

        async def blocked_response(_request):
            await blocker.wait()
            return httpx.Response(200, json={"success": True})

        requests = []
        metrics = benchmark.MetricsClient(None, "", None)
        async with httpx.AsyncClient(transport=httpx.MockTransport(blocked_response)) as client:
            task = asyncio.create_task(
                benchmark.execute_burst(
                    client=client,
                    gateway_url="http://gateway.test/internal/v1/inference/predict",
                    assignments=[prepared_model()] * 3,
                    phase="warm",
                    repetition=1,
                    metrics_client=metrics,
                    host_sampler=benchmark.HostSampler(False),
                    sample_interval_seconds=0.01,
                    gate_timeout_seconds=1,
                    request_results=requests,
                    resource_samples=[],
                )
            )
            await asyncio.sleep(0.05)
            task.cancel()
            with self.assertRaises(asyncio.CancelledError):
                await task
        await metrics.close()
        self.assertEqual([], requests)


class OutputTests(unittest.TestCase):
    def test_result_bundle_excludes_sensitive_input_and_renders_report(self):
        request = benchmark.RequestResult(
            run_id="warm-model-c1-r1",
            phase="warm",
            model="Model",
            concurrency=1,
            repetition=1,
            request_index=1,
            started_at="2026-01-01T00:00:00+00:00",
            elapsed_seconds=0.5,
            status_code=200,
            success=True,
            error_code=None,
            exception_type=None,
            response_bytes=100,
        )
        run = benchmark.RunResult(
            run_id=request.run_id,
            phase="warm",
            model="Model",
            concurrency=1,
            repetition=1,
            started_at=request.started_at,
            duration_seconds=0.5,
        )
        sample = benchmark.ResourceSample(
            run_id=request.run_id,
            timestamp=request.started_at,
            gpu_used_mib=42,
            manager_queue_depth=0,
        )
        run_config = {
            "createdAt": request.started_at,
            "deploymentName": "test",
            "loadGeneratorLocation": "local",
            "notes": "",
            "serverConfiguration": {"gpu": "test"},
            "loadGeneratorHost": {
                "gitCommit": "abc",
                "platform": "test",
                "cpuModel": "test",
                "logicalCpuCount": 1,
                "systemMemoryMiB": 100,
            },
        }
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "result"
            summary = benchmark.write_results(output, run_config, [request], [sample], [run], {})
            combined = "\n".join(
                path.read_text()
                for path in output.iterdir()
                if path.suffix in {".json", ".csv", ".md"}
            )

        self.assertEqual(1, len(summary))
        self.assertIn("PACS-AI Concurrent Inference Benchmark", combined)
        self.assertNotIn("must-not-appear-in-results", combined)
        self.assertNotIn("discarded", combined)

    def test_partial_results_are_privacy_safe_and_replaced_on_completion(self):
        request = benchmark.RequestResult(
            run_id="warm-model-c1-r1",
            phase="warm",
            model="Model",
            concurrency=1,
            repetition=1,
            request_index=1,
            started_at="2026-01-01T00:00:00+00:00",
            elapsed_seconds=0.5,
            status_code=200,
            success=True,
            error_code=None,
            exception_type=None,
            response_bytes=100,
        )
        run = benchmark.RunResult(
            run_id=request.run_id,
            phase="warm",
            model="Model",
            concurrency=1,
            repetition=1,
            started_at=request.started_at,
            duration_seconds=0.5,
        )
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "result"
            benchmark.write_partial_results(output, [request], [], [run])
            partial = "\n".join(path.read_text() for path in output.iterdir())
            self.assertIn("complete", partial)
            self.assertNotIn("must-not-appear-in-results", partial)

            run_config = {
                "createdAt": request.started_at,
                "deploymentName": "test",
                "loadGeneratorLocation": "local",
                "notes": "",
                "serverConfiguration": {},
                "loadGeneratorHost": {
                    "gitCommit": "abc",
                    "platform": "test",
                    "cpuModel": "test",
                    "logicalCpuCount": 1,
                    "systemMemoryMiB": 100,
                },
            }
            benchmark.write_results(output, run_config, [request], [], [run], {})

            self.assertFalse(list(output.glob("partial-*")))
            self.assertTrue((output / "summary.json").exists())


if __name__ == "__main__":
    unittest.main()
