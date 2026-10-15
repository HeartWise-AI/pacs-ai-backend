import copy
import importlib.util
import io
import json
import sys
import tempfile
import unittest
from pathlib import Path

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
SCRIPT_PATH = REPOSITORY_ROOT / "scripts" / "validate_catalog_recovery.py"


def load_module():
    spec = importlib.util.spec_from_file_location("pacs_ai_validate_catalog_recovery", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"Unable to load {SCRIPT_PATH}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


RECOVERY = load_module()


class Response:
    def __init__(self, payload):
        self.payload = payload

    def __enter__(self):
        return io.StringIO(json.dumps(self.payload))

    def __exit__(self, *_args):
        return False


class ValidateCatalogRecoveryTests(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)
        self.root = Path(self.temp_dir.name)
        self.model_directory = self.root / "model-examples" / "DeepRV-CLIP"
        (self.model_directory / "data").mkdir(parents=True)
        (self.root / ".github").mkdir()
        self.mapping_path = self.root / ".github" / "model-image-repositories.json"
        self.mapping_path.write_text(
            json.dumps(
                {
                    "schemaVersion": 1,
                    "repositories": {
                        "model-examples/DeepRV-CLIP": "heartwisehub/deeprv-clip"
                    },
                }
            ),
            encoding="utf-8",
        )
        self.model_info = {
            "modelId": "DeepRV_CLIP",
            "modelName": "DeepRV-CLIP",
            "version": "1.0.1",
            "provenance": {
                "sourceRepository": "HeartWise-AI/pacs-ai-backend",
                "sourceRevision": None,
                "modelRepository": "heartwise/DeepRV_CLIP",
                "modelRevision": "b" * 40,
                "weightsPath": "best_model_epoch_4.pt",
                "weightsSha256": "c" * 64,
            },
        }
        (self.model_directory / "data" / "model_info.json").write_text(
            json.dumps(self.model_info), encoding="utf-8"
        )
        self.run_id = 38763511171
        self.run_attempt = 1
        self.source_revision = "a" * 40
        self.run = {
            "id": self.run_id,
            "run_attempt": self.run_attempt,
            "repository": {"full_name": "HeartWise-AI/pacs-ai-backend"},
            "path": ".github/workflows/publish-model-image.yml",
            "event": "workflow_dispatch",
            "head_branch": "master",
            "head_sha": self.source_revision,
            "status": "completed",
            "conclusion": "failure",
        }
        digest = "sha256:" + "d" * 64
        self.evidence = {
            "status": "published",
            "automation": {
                "repository": "HeartWise-AI/pacs-ai-backend",
                "runId": str(self.run_id),
                "runAttempt": str(self.run_attempt),
            },
            "model": {"id": "DeepRV_CLIP", "name": "DeepRV-CLIP", "version": "1.0.1"},
            "release": {
                "channel": "RELEASE",
                "baseVersion": "1.0.1",
                "workflowRun": str(self.run_id),
            },
            "source": {
                "repository": "HeartWise-AI/pacs-ai-backend",
                "revision": self.source_revision,
            },
            "weights": {
                "repository": "heartwise/DeepRV_CLIP",
                "revision": "b" * 40,
                "path": "best_model_epoch_4.pt",
                "sha256": "c" * 64,
            },
            "image": {
                "reference": "heartwisehub/deeprv-clip:1.0.1",
                "immutableReference": f"heartwisehub/deeprv-clip@{digest}",
                "registryDigest": digest,
            },
        }
        self.evidence_path = self.root / "model-release-evidence.json"
        self.write_evidence()
        self.original_root = RECOVERY.REPOSITORY_ROOT
        RECOVERY.REPOSITORY_ROOT = self.root
        self.addCleanup(setattr, RECOVERY, "REPOSITORY_ROOT", self.original_root)

    def write_evidence(self):
        self.evidence_path.write_text(json.dumps(self.evidence), encoding="utf-8")

    def validate(self, run=None):
        source_revision = RECOVERY.validate_source_run(
            run or self.run,
            repository="HeartWise-AI/pacs-ai-backend",
            run_id=self.run_id,
            run_attempt=self.run_attempt,
        )
        RECOVERY.validate_evidence(
            self.evidence_path,
            self.model_directory,
            mapping_path=self.mapping_path,
            repository="HeartWise-AI/pacs-ai-backend",
            run_id=self.run_id,
            run_attempt=self.run_attempt,
            source_revision=source_revision,
        )

    def test_validates_trusted_release_evidence(self):
        self.validate()

    def test_source_run_must_be_the_protected_master_workflow(self):
        invalid_values = {
            "path": ".github/workflows/untrusted.yml",
            "event": "pull_request",
            "head_branch": "feature/untrusted",
            "run_attempt": 2,
        }
        for field, value in invalid_values.items():
            with self.subTest(field=field):
                run = copy.deepcopy(self.run)
                run[field] = value
                with self.assertRaises(RECOVERY.CatalogRecoveryValidationError):
                    self.validate(run)

    def test_evidence_must_match_selected_model_and_source_run(self):
        invalid_values = (
            ("model", "id", "AnotherModel"),
            ("source", "revision", "e" * 40),
            ("automation", "runId", "123"),
            ("image", "reference", "heartwisehub/another-model:1.0.1"),
        )
        for section, field, value in invalid_values:
            with self.subTest(field=f"{section}.{field}"):
                original = self.evidence[section][field]
                self.evidence[section][field] = value
                self.write_evidence()
                with self.assertRaises(RECOVERY.CatalogRecoveryValidationError):
                    self.validate()
                self.evidence[section][field] = original
                self.write_evidence()

    def test_fetch_source_run_uses_authenticated_github_api(self):
        requests = []

        def open_request(request, timeout):
            requests.append((request, timeout))
            return Response(self.run)

        result = RECOVERY.fetch_source_run(
            "HeartWise-AI/pacs-ai-backend",
            self.run_id,
            "token",
            open_request=open_request,
        )

        request, timeout = requests[0]
        self.assertEqual(self.run, result)
        self.assertEqual(30, timeout)
        self.assertEqual(f"Bearer token", request.get_header("Authorization"))
        self.assertEqual("PACS-AI-model-release/1.0", request.get_header("User-agent"))
        self.assertTrue(request.full_url.endswith(f"/actions/runs/{self.run_id}"))


if __name__ == "__main__":
    unittest.main()
