import importlib.util
import io
import json
import sys
import tempfile
import unittest
import urllib.error
from pathlib import Path
from unittest import mock

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
SCRIPT_PATH = REPOSITORY_ROOT / "scripts" / "register-model-release.py"


def load_module():
    spec = importlib.util.spec_from_file_location("pacs_ai_register_model_release", SCRIPT_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"Unable to load {SCRIPT_PATH}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


CATALOG = load_module()


class Response:
    def __init__(self, payload):
        self.payload = payload

    def __enter__(self):
        return io.StringIO(json.dumps(self.payload))

    def __exit__(self, *_args):
        return False


class RegisterModelReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)
        self.evidence_path = Path(self.temp_dir.name) / "release.json"
        self.evidence = {
            "status": "published",
            "model": {"id": "DeepCORO-SYNTAX", "version": "6.0.0"},
            "source": {"repository": "HeartWise-AI/pacs-ai-backend", "revision": "a" * 40},
            "weights": {
                "repository": "heartwise/deepcoro_clip_cardiosyntax",
                "revision": "b" * 40,
                "path": "models/model.pt",
                "sha256": "c" * 64,
            },
            "image": {
                "reference": "heartwisehub/pacs-ai-deepcoro-syntax:6.0.0",
                "registryDigest": "sha256:" + "d" * 64,
            },
        }
        self.evidence_path.write_text(json.dumps(self.evidence), encoding="utf-8")

    def test_release_evidence_becomes_catalog_payload(self):
        payload = CATALOG.load_catalog_payload(self.evidence_path)

        self.assertEqual("DeepCORO-SYNTAX", payload["modelId"])
        self.assertEqual(self.evidence["image"]["registryDigest"], payload["imageDigest"])
        self.assertEqual("b" * 40, payload["provenance"]["modelRevision"])
        self.assertEqual("RELEASE", payload["channel"])
        self.assertEqual("6.0.0", payload["baseVersion"])

    def test_candidate_evidence_preserves_release_context(self):
        self.evidence["model"]["version"] = "6.0.1-rc.366.aaaaaaaa"
        self.evidence["image"]["reference"] = (
            "heartwisehub/pacs-ai-deepcoro-syntax:6.0.1-rc.366.aaaaaaaa"
        )
        self.evidence["release"] = {
            "channel": "CANDIDATE",
            "baseVersion": "6.0.1",
            "sourceBranch": "feature/model-release",
            "pullRequest": 366,
            "workflowRun": "123456",
            "reusedExisting": False,
        }
        self.evidence_path.write_text(json.dumps(self.evidence), encoding="utf-8")

        payload = CATALOG.load_catalog_payload(self.evidence_path)

        self.assertEqual("CANDIDATE", payload["channel"])
        self.assertEqual("6.0.1", payload["baseVersion"])
        self.assertEqual("feature/model-release", payload["sourceBranch"])
        self.assertEqual(366, payload["pullRequest"])
        self.assertEqual("123456", payload["workflowRun"])

    def test_only_https_catalog_origins_are_accepted(self):
        self.assertEqual(
            "https://pacs.example/v1/inference/model/versions/publish",
            CATALOG.catalog_endpoint("https://pacs.example/"),
        )
        for url in ("http://pacs.example", "https://user:secret@pacs.example", "https://pacs.example?q=1"):
            with self.subTest(url=url), self.assertRaises(CATALOG.CatalogRegistrationError):
                CATALOG.catalog_endpoint(url)

    def test_registration_posts_catalog_payload_without_logging_the_key(self):
        payload = CATALOG.load_catalog_payload(self.evidence_path)
        open_request = mock.Mock(return_value=Response({"success": True, "data": {"id": "version-id"}}))

        result = CATALOG.register_catalog_version(
            endpoint="https://pacs.example/v1/inference/model/versions/publish",
            sudo_key="secret",
            payload=payload,
            open_request=open_request,
            sleep=mock.Mock(),
        )

        request = open_request.call_args.args[0]
        self.assertEqual("POST", request.method)
        self.assertEqual(
            "PACS-AI-model-release/1.0",
            request.get_header("User-agent"),
        )
        self.assertEqual("secret", request.get_header("X-fb-sudo-key"))
        self.assertEqual(payload, json.loads(request.data))
        self.assertEqual("version-id", result["data"]["id"])

    def test_transient_server_errors_are_retried(self):
        error = urllib.error.HTTPError(
            "https://pacs.example", 503, "Unavailable", {}, io.BytesIO(b"temporary")
        )
        open_request = mock.Mock(
            side_effect=[error, Response({"success": True, "data": {"id": "version-id"}})]
        )
        sleep = mock.Mock()

        CATALOG.register_catalog_version(
            endpoint="https://pacs.example/v1/inference/model/versions/publish",
            sudo_key="secret",
            payload={"modelId": "model"},
            open_request=open_request,
            sleep=sleep,
        )

        self.assertEqual(2, open_request.call_count)
        sleep.assert_called_once_with(1.0)


if __name__ == "__main__":
    unittest.main()
