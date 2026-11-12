"""DeepCORO-JEPA 1.0.0 artifact identity and build verification."""

import hashlib
import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

import download_model
from utils.resource_config import load_model_info, model_info_payload


def test_runtime_configuration_and_manifest_identify_the_same_checkpoint():
    config = json.loads((ROOT / "models/config.json").read_text(encoding="utf-8"))
    manifest = json.loads((ROOT / "data/model_info.json").read_text(encoding="utf-8"))
    provenance = manifest["provenance"]

    assert manifest["version"] == "1.0.0"
    assert config["ModelStateDict"]["model_path"] == provenance["weightsPath"]
    assert provenance == {
        "sourceRepository": "HeartWise-AI/pacs-ai-backend",
        "sourceRevision": None,
        "modelRepository": "heartwise/DeepCORO_JEPA",
        "modelRevision": "49c8ddfcd45739e5994b8ac5055a52e7295c6f5f",
        "weightsPath": "model.safetensors",
        "weightsSha256": "cc9d84a3fe6849579a74061edd744f169121814db40c0b8bfa676fb49c1a7325",
    }


def test_weight_verification_accepts_only_the_expected_bytes(tmp_path):
    weights = tmp_path / "model.safetensors"
    weights.write_bytes(b"verified-weights")
    expected = hashlib.sha256(weights.read_bytes()).hexdigest()
    assert download_model.verify_weights(tmp_path, "model.safetensors", expected) == weights
    weights.write_bytes(b"unexpected-weights")
    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        download_model.verify_weights(tmp_path, "model.safetensors", expected)


def test_release_revision_and_version_are_injected_into_model_info():
    revision = "a" * 40
    payload = model_info_payload(load_model_info(ROOT / "data/model_info.json"), source_revision=revision,
                                 model_version="1.0.0-rc.1.aaaaaaaaaaaa")
    assert payload["provenance"]["sourceRevision"] == revision
    assert payload["version"] == "1.0.0-rc.1.aaaaaaaaaaaa"


def test_official_dockerfile_consumes_release_metadata_and_fetches_the_structure_file():
    dockerfile = (ROOT / "Dockerfile").read_text(encoding="utf-8")
    for build_argument in ("PACS_AI_SOURCE_REPOSITORY", "PACS_AI_SOURCE_REVISION", "PACS_AI_MODEL_VERSION",
                           "PACS_AI_MODEL_REPOSITORY", "PACS_AI_MODEL_REVISION", "PACS_AI_WEIGHTS_PATH",
                           "PACS_AI_WEIGHTS_SHA256"):
        assert f"ARG {build_argument}" in dockerfile
        assert f"${{{build_argument}}}" in dockerfile
    assert "mount=type=secret,id=hf_token" in dockerfile
    assert "--extra-file model_config.json" in dockerfile
