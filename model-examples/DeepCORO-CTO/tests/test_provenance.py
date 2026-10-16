"""DeepCORO-CTO 2.0.1 artifact identity and build verification."""

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

    assert manifest["version"] == "2.0.1"
    assert config["ModelStateDict"]["model_path"] == provenance["weightsPath"]
    assert provenance == {
        "sourceRepository": "HeartWise-AI/pacs-ai-backend",
        "sourceRevision": None,
        "modelRepository": "heartwise/DeepCORO_CTO",
        "modelRevision": "8511afe6f33574052123d2244a19959da85d6334",
        "weightsPath": "best_model_epoch_18.pt",
        "weightsSha256": "1fb7e0bd0d5e66562052a811a2091be07b806517270b52ff5715ac39869d38cb",
    }


def test_weight_verification_accepts_only_the_expected_bytes(tmp_path):
    relative_path = Path("candidate/model.pt")
    weights = tmp_path / relative_path
    weights.parent.mkdir(parents=True)
    weights.write_bytes(b"verified-cto-weights")
    expected = hashlib.sha256(weights.read_bytes()).hexdigest()

    assert download_model.verify_weights(tmp_path, str(relative_path), expected) == weights

    weights.write_bytes(b"unexpected-weights")
    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        download_model.verify_weights(tmp_path, str(relative_path), expected)


def test_release_revision_and_version_are_injected_into_model_info():
    revision = "a" * 40
    model_info = load_model_info(ROOT / "data/model_info.json")
    candidate_version = "2.0.1-rc.393.aaaaaaaaaaaa"
    payload = model_info_payload(
        model_info,
        source_revision=revision,
        model_version=candidate_version,
    )

    assert payload["provenance"]["sourceRevision"] == revision
    assert payload["version"] == candidate_version

    with pytest.raises(ValueError, match="sourceRevision"):
        model_info_payload(model_info, source_revision="not-a-git-sha")


def test_official_dockerfile_consumes_release_metadata():
    dockerfile = (ROOT / "Dockerfile").read_text(encoding="utf-8")

    for build_argument in (
        "PACS_AI_SOURCE_REPOSITORY",
        "PACS_AI_SOURCE_REVISION",
        "PACS_AI_MODEL_VERSION",
        "PACS_AI_MODEL_REPOSITORY",
        "PACS_AI_MODEL_REVISION",
        "PACS_AI_WEIGHTS_PATH",
        "PACS_AI_WEIGHTS_SHA256",
    ):
        assert f"ARG {build_argument}" in dockerfile
        assert f"${{{build_argument}}}" in dockerfile

    assert "mount=type=secret,id=hf_token" in dockerfile
    assert "--token-file /run/secrets/hf_token" in dockerfile
