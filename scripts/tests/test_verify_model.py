import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock


REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
SCRIPT_PATH = REPOSITORY_ROOT / "scripts" / "verify-model.py"


def load_module():
    spec = importlib.util.spec_from_file_location("pacs_ai_verify_model", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"Unable to load {SCRIPT_PATH}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


VERIFY = load_module()


class VerifyModelTests(unittest.TestCase):
    def valid_manifest(self):
        return {
            "modelId": "TestModel",
            "modelName": "Test Model",
            "version": "1.0.0",
            "dicomTargetLevel": "STUDY",
            "dicomUploadMin": 1,
            "dicomUploadMax": 4,
            "supportedDicomModalities": ["XA"],
            "supportedAdditionalMetadata": [],
            "supportedOutputModes": ["HTML", "JSON"],
            "resources": {
                "gpuRequired": True,
                "residentMemoryMiB": 100,
                "peakMemoryMiB": 150,
                "maxConcurrentInferences": 1,
                "idleTimeoutSeconds": 600,
            },
        }

    def test_manifest_contract_accepts_valid_resources(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "data").mkdir()
            (root / "data" / "model_info.json").write_text(
                json.dumps(self.valid_manifest()), encoding="utf-8"
            )
            detail = VERIFY.validate_manifest(root)
        self.assertIn("TestModel 1.0.0", detail)

    def test_manifest_contract_rejects_invalid_concurrency(self):
        manifest = self.valid_manifest()
        manifest["resources"]["maxConcurrentInferences"] = 2
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "data").mkdir()
            (root / "data" / "model_info.json").write_text(
                json.dumps(manifest), encoding="utf-8"
            )
            with self.assertRaisesRegex(VERIFY.VerificationError, "must equal 1"):
                VERIFY.validate_manifest(root)

    def test_image_reference_requires_an_immutable_digest(self):
        digest = "a" * 64
        self.assertEqual(
            ("heartwisehub/test-model", f"sha256:{digest}"),
            VERIFY.validate_image_reference(f"heartwisehub/test-model@sha256:{digest}"),
        )
        for image in ("heartwisehub/test-model:1.0.0", "heartwisehub/test-model:latest"):
            with self.subTest(image=image), self.assertRaises(VERIFY.VerificationError):
                VERIFY.validate_image_reference(image)

    def test_invalid_image_reference_never_runs_docker(self):
        model = REPOSITORY_ROOT / "model-examples" / "DeepCORO-SCAD"
        with (
            mock.patch.object(VERIFY, "git_revision", return_value="a" * 40),
            mock.patch.object(VERIFY, "run_command") as run_command,
        ):
            evidence = VERIFY.image_evidence(
                model,
                "heartwisehub/pacs-ai-deepcoro-scad:latest",
                1,
            )
        run_command.assert_not_called()
        self.assertEqual("FAIL", evidence["overall"])
        self.assertEqual("FAIL", evidence["checks"][0]["status"])

    def test_current_scad_source_contract_has_no_failures(self):
        model = REPOSITORY_ROOT / "model-examples" / "DeepCORO-SCAD"
        result = VERIFY.source_model_result(model, VERIFY.git_revision(), False)
        self.assertNotEqual("FAIL", result["overall"])
        self.assertNotIn("FAIL", {check["status"] for check in result["checks"]})

    def test_nested_changed_path_resolves_nearest_model_contract(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            model = root / "model-examples" / "Family" / "variant"
            (model / "data").mkdir(parents=True)
            (model / "Dockerfile").write_text("FROM scratch\n", encoding="utf-8")
            (model / "data" / "model_info.json").write_text("{}\n", encoding="utf-8")
            source = model / "models" / "encoder.py"
            source.parent.mkdir()
            source.write_text("", encoding="utf-8")
            resolved = VERIFY.model_directory_for_changed_path(
                "model-examples/Family/variant/models/encoder.py", root
            )
        self.assertEqual(model.resolve(), resolved)

    def test_overall_status_preserves_skips_and_failures(self):
        self.assertEqual("PASS", VERIFY.overall_status([{"status": "PASS"}]))
        self.assertEqual("PASS_WITH_SKIPS", VERIFY.overall_status([{"status": "SKIP"}]))
        self.assertEqual(
            "FAIL", VERIFY.overall_status([{"status": "SKIP"}, {"status": "FAIL"}])
        )


if __name__ == "__main__":
    unittest.main()
