"""DeepCORO-SYNTAX v6 artifact identity and build verification."""

import hashlib
import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

import download_model
from utils.resource_config import load_model_info, model_info_payload


def test_runtime_configuration_and_manifest_identify_the_same_v6_checkpoint():
    config = json.loads((ROOT / "models/config.json").read_text(encoding="utf-8"))
    class_mapping = json.loads((ROOT / "models/class_mapping.json").read_text(encoding="utf-8"))
    manifest = json.loads((ROOT / "data/model_info.json").read_text(encoding="utf-8"))
    provenance = manifest["provenance"]

    assert manifest["version"] == "6.0.0"
    assert config["ModelStateDict"]["model_path"] == provenance["weightsPath"]
    assert class_mapping["syntax_category"]["threshold_binary_ge23"] == 16.979706
    assert provenance == {
        "sourceRepository": "HeartWise-AI/pacs-ai-backend",
        "sourceRevision": None,
        "modelRepository": "heartwise/deepcoro_clip_cardiosyntax",
        "modelRevision": "1ffb38cfc10fa10c4b60f44746063feb860eeba0",
        "weightsPath": "v6_20260929-203527/models/best_model_epoch_19.pt",
        "weightsSha256": "856f5d6523c25a45c62886bf6cd1d351297821f60c3465a20b962aa46fa08ec0",
    }


def test_weight_verification_accepts_only_the_expected_bytes(tmp_path):
    relative_path = Path("candidate/model.pt")
    weights = tmp_path / relative_path
    weights.parent.mkdir(parents=True)
    weights.write_bytes(b"verified-v6-weights")
    expected = hashlib.sha256(weights.read_bytes()).hexdigest()

    assert download_model.verify_weights(tmp_path, str(relative_path), expected) == weights

    weights.write_bytes(b"unexpected-weights")
    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        download_model.verify_weights(tmp_path, str(relative_path), expected)


def test_source_revision_is_validated_and_injected_into_model_info():
    revision = "a" * 40
    model_info = load_model_info(ROOT / "data/model_info.json")
    payload = model_info_payload(model_info, source_revision=revision)
    assert payload["provenance"]["sourceRevision"] == revision

    with pytest.raises(ValueError, match="sourceRevision"):
        model_info_payload(model_info, source_revision="not-a-git-sha")


def test_official_dockerfile_consumes_release_metadata_without_hard_coding_it():
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


def test_local_dockerfile_is_marked_local_and_verifies_preloaded_weights():
    dockerfile = (ROOT / "Dockerfile.local").read_text(encoding="utf-8")

    assert 'ai.heartwise.release.channel="local"' in dockerfile
    assert "--verify-only" in dockerfile
    assert (
        "docker build --platform linux/amd64 -f Dockerfile.local "
        "-t pacs-ai-deepcoro-syntax:local" in dockerfile
    )
