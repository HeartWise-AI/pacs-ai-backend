"""DeepCORO-SCAD 1.0.0 artifact identity and build verification."""

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
        "modelRepository": "heartwise/deepcoro_clip_scad",
        "modelRevision": "9c03ca0928cd781b82669b8e4cd48f1b7b11ed3d",
        "weightsPath": "20261025-134722/models/best_model_epoch_7.pt",
        "weightsSha256": "77ebd8701feddc677abee17e6ebd872016262404277492308ff0319b702c4bfe",
    }


def test_weight_verification_accepts_only_the_expected_bytes(tmp_path):
    relative_path = Path("candidate/model.pt")
    weights = tmp_path / relative_path
    weights.parent.mkdir(parents=True)
    weights.write_bytes(b"verified-scad-weights")
    expected = hashlib.sha256(weights.read_bytes()).hexdigest()

    assert download_model.verify_weights(tmp_path, str(relative_path), expected) == weights

    weights.write_bytes(b"unexpected-weights")
    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        download_model.verify_weights(tmp_path, str(relative_path), expected)


def test_release_revision_and_version_are_injected_into_model_info():
    revision = "a" * 40
    model_info = load_model_info(ROOT / "data/model_info.json")
    candidate_version = "1.0.0-rc.1.aaaaaaaaaaaa"
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


def test_cuda_torch_and_torchvision_pins_match_the_validated_runtime():
    requirements = (ROOT / "requirements.txt").read_text(encoding="utf-8").splitlines()

    assert "--extra-index-url https://download.pytorch.org/whl/cu124" in requirements
    assert "torch==2.6.0+cu124" in requirements
    assert "torchvision==0.21.0+cu124" in requirements
