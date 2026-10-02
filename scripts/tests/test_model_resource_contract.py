import ast
import asyncio
import importlib.util
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path


REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
TEMPLATE_ROOT = REPOSITORY_ROOT / "model-template"
RESOURCE_MODULE_PATH = TEMPLATE_ROOT / "utils" / "resource_config.py"


def load_resource_module():
    spec = importlib.util.spec_from_file_location("pacs_ai_resource_config", RESOURCE_MODULE_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"Unable to load {RESOURCE_MODULE_PATH}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


RESOURCE_CONFIG = load_resource_module()


def discover_model_manifests() -> list[Path]:
    manifests = sorted((REPOSITORY_ROOT / "model-examples").glob("**/data/model_info.json"))
    return [TEMPLATE_ROOT / "data" / "model_info.json", *manifests]


def load_template_main():
    spec = importlib.util.spec_from_file_location("pacs_ai_model_template_main", TEMPLATE_ROOT / "main.py")
    if spec is None or spec.loader is None:
        raise RuntimeError("Unable to load model-template/main.py")
    module = importlib.util.module_from_spec(spec)
    previous_directory = Path.cwd()
    sys.path.insert(0, str(TEMPLATE_ROOT))
    try:
        os.chdir(TEMPLATE_ROOT)
        spec.loader.exec_module(module)
    finally:
        os.chdir(previous_directory)
        sys.path.remove(str(TEMPLATE_ROOT))
    return module


def calls_named(node: ast.AST, object_name: str, method_name: str) -> bool:
    for child in ast.walk(node):
        if not isinstance(child, ast.Call) or not isinstance(child.func, ast.Attribute):
            continue
        owner = child.func.value
        if (
            child.func.attr == method_name
            and isinstance(owner, ast.Name)
            and owner.id == object_name
        ):
            return True
    return False


class ModelResourceContractTests(unittest.TestCase):
    def valid_model_info(self, **resource_overrides):
        resources = {
            "gpuRequired": True,
            "residentMemoryMiB": 100,
            "peakMemoryMiB": 200,
            "maxConcurrentInferences": 1,
            "idleTimeoutSeconds": 600,
        }
        resources.update(resource_overrides)
        return {
            "modelId": "TestModel",
            "modelName": "Test Model",
            "version": "1.0.0",
            "resources": resources,
        }

    def test_valid_gpu_and_cpu_contracts(self):
        gpu = RESOURCE_CONFIG.ModelInfo.model_validate(self.valid_model_info())
        self.assertTrue(gpu.resources.gpuRequired)

        cpu = RESOURCE_CONFIG.ModelInfo.model_validate(
            self.valid_model_info(
                gpuRequired=False,
                residentMemoryMiB=0,
                peakMemoryMiB=0,
            )
        )
        self.assertFalse(cpu.resources.gpuRequired)

    def test_invalid_contracts_are_rejected(self):
        invalid_cases = {
            "missing resources": {key: value for key, value in self.valid_model_info().items() if key != "resources"},
            "string integer": self.valid_model_info(residentMemoryMiB="100"),
            "floating point integer": self.valid_model_info(peakMemoryMiB=200.5),
            "zero GPU memory": self.valid_model_info(residentMemoryMiB=0),
            "negative GPU memory": self.valid_model_info(peakMemoryMiB=-1),
            "peak below resident": self.valid_model_info(peakMemoryMiB=99),
            "zero concurrency": self.valid_model_info(maxConcurrentInferences=0),
            "unsupported V1 concurrency": self.valid_model_info(maxConcurrentInferences=2),
            "zero idle timeout": self.valid_model_info(idleTimeoutSeconds=0),
            "CPU with GPU memory": self.valid_model_info(gpuRequired=False),
        }

        for name, model_info in invalid_cases.items():
            with self.subTest(name=name):
                with self.assertRaises(ValueError):
                    RESOURCE_CONFIG.ModelInfo.model_validate(model_info)

    def test_every_resource_field_is_required(self):
        for field in (
            "gpuRequired",
            "residentMemoryMiB",
            "peakMemoryMiB",
            "maxConcurrentInferences",
            "idleTimeoutSeconds",
        ):
            with self.subTest(field=field):
                model_info = self.valid_model_info()
                model_info["resources"].pop(field)
                with self.assertRaises(ValueError) as context:
                    RESOURCE_CONFIG.ModelInfo.model_validate(model_info)
                self.assertIn(field, str(context.exception))

    def test_loader_error_names_model_path_and_invalid_field(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "model_info.json"
            path.write_text(json.dumps(self.valid_model_info(peakMemoryMiB=10)), encoding="utf-8")

            with self.assertRaises(ValueError) as context:
                RESOURCE_CONFIG.load_model_info(path)

        message = str(context.exception)
        self.assertIn("TestModel", message)
        self.assertIn("model_info.json", message)
        self.assertIn("peakMemoryMiB", message)

    def test_semaphore_serializes_and_releases_after_failure(self):
        async def exercise():
            model_info = RESOURCE_CONFIG.ModelInfo.model_validate(self.valid_model_info())
            semaphore = RESOURCE_CONFIG.create_inference_semaphore(model_info)
            active = 0
            max_active = 0

            async def prediction(should_fail=False):
                nonlocal active, max_active
                async with semaphore:
                    active += 1
                    max_active = max(max_active, active)
                    await asyncio.sleep(0.01)
                    active -= 1
                    if should_fail:
                        raise RuntimeError("prediction failed")

            await asyncio.gather(prediction(), prediction())
            with self.assertRaises(RuntimeError):
                await prediction(should_fail=True)
            await asyncio.wait_for(prediction(), timeout=0.5)
            return max_active

        self.assertEqual(1, asyncio.run(exercise()))

    def test_template_endpoint_serializes_load_and_prediction_and_recovers(self):
        template_main = load_template_main()

        class PredictionServiceDouble:
            is_initialized = False

            def __init__(self):
                self.active = 0
                self.max_active = 0
                self.fail_next_load = False

            def load_model(self, _config):
                if self.fail_next_load:
                    self.fail_next_load = False
                    raise RuntimeError("load failed")

            async def predict(self, _request):
                self.active += 1
                self.max_active = max(self.max_active, self.active)
                await asyncio.sleep(0.01)
                self.active -= 1
                return True, {"predictions": [], "modelRecommendations": None}

        async def exercise():
            service = PredictionServiceDouble()
            template_main.PredictionService = service
            request = template_main.PredictRequest(outputMode="JSON")

            concurrent = await asyncio.gather(
                template_main.predict(request),
                template_main.predict(request),
            )
            service.fail_next_load = True
            failed = await template_main.predict(request)
            recovered = await asyncio.wait_for(template_main.predict(request), timeout=0.5)
            return service.max_active, concurrent, failed, recovered

        max_active, concurrent, failed, recovered = asyncio.run(exercise())
        self.assertEqual(1, max_active)
        self.assertEqual([200, 200], [response.status_code for response in concurrent])
        self.assertEqual(500, failed.status_code)
        self.assertEqual(200, recovered.status_code)

    def test_every_model_declares_resources_and_uses_configured_semaphore(self):
        manifests = discover_model_manifests()
        self.assertGreaterEqual(len(manifests), 19)
        canonical_resource_module = RESOURCE_MODULE_PATH.read_text(encoding="utf-8")

        for manifest_path in manifests:
            with self.subTest(manifest=str(manifest_path.relative_to(REPOSITORY_ROOT))):
                model_root = manifest_path.parent.parent
                model_info = RESOURCE_CONFIG.load_model_info(manifest_path)
                self.assertEqual(1, model_info.resources.maxConcurrentInferences)

                local_resource_module = model_root / "utils" / "resource_config.py"
                self.assertTrue(local_resource_module.is_file())
                self.assertEqual(
                    canonical_resource_module,
                    local_resource_module.read_text(encoding="utf-8"),
                )

                main_path = model_root / "main.py"
                tree = ast.parse(main_path.read_text(encoding="utf-8"), filename=str(main_path))

                hardcoded_semaphores = [
                    node
                    for node in ast.walk(tree)
                    if isinstance(node, ast.Call)
                    and isinstance(node.func, ast.Attribute)
                    and isinstance(node.func.value, ast.Name)
                    and node.func.value.id == "asyncio"
                    and node.func.attr == "Semaphore"
                ]
                self.assertEqual([], hardcoded_semaphores)

                configured_lock = any(
                    isinstance(node, ast.Assign)
                    and any(isinstance(target, ast.Name) and target.id == "inference_lock" for target in node.targets)
                    and isinstance(node.value, ast.Call)
                    and isinstance(node.value.func, ast.Name)
                    and node.value.func.id == "create_inference_semaphore"
                    for node in ast.walk(tree)
                )
                self.assertTrue(configured_lock)

                predict_functions = [
                    node
                    for node in tree.body
                    if isinstance(node, ast.AsyncFunctionDef) and node.name == "predict"
                ]
                self.assertEqual(1, len(predict_functions))
                critical_sections = [
                    node
                    for node in ast.walk(predict_functions[0])
                    if isinstance(node, ast.AsyncWith)
                    and any(
                        isinstance(item.context_expr, ast.Name)
                        and item.context_expr.id == "inference_lock"
                        for item in node.items
                    )
                ]
                self.assertEqual(1, len(critical_sections))
                self.assertTrue(calls_named(critical_sections[0], "PredictionService", "load_model"))
                self.assertTrue(calls_named(critical_sections[0], "PredictionService", "predict"))

    def test_inference_servers_do_not_configure_multiple_workers(self):
        model_roots = [path.parent.parent for path in discover_model_manifests()]
        for model_root in model_roots:
            with self.subTest(model=str(model_root.relative_to(REPOSITORY_ROOT))):
                for config_path in model_root.glob("supervisord*.conf"):
                    command_lines = [
                        line.strip()
                        for line in config_path.read_text(encoding="utf-8").splitlines()
                        if line.strip().startswith("command=") and "uvicorn" in line
                    ]
                    for command in command_lines:
                        self.assertNotIn("--workers", command)
                        self.assertNotIn(" -w ", command)


if __name__ == "__main__":
    unittest.main()
