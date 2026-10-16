"""EchoPrime 1.2.2 artifact identity and build verification."""

import hashlib
import json
import sys
import zipfile
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

import download_model
from utils.resource_config import load_model_info, model_info_payload


def test_manifest_identifies_the_validated_echoprime_encoder():
    manifest = json.loads((ROOT / "data/model_info.json").read_text(encoding="utf-8"))

    assert manifest["version"] == "1.2.2"
    assert manifest["provenance"] == {
        "sourceRepository": "HeartWise-AI/pacs-ai-backend",
        "sourceRevision": None,
        "modelRepository": download_model.EXPECTED_REPOSITORY,
        "modelRevision": download_model.EXPECTED_REVISION,
        "weightsPath": download_model.EXPECTED_WEIGHTS_PATH,
        "weightsSha256": download_model.WEIGHT_FILES[download_model.EXPECTED_WEIGHTS_PATH],
    }


def test_file_verification_rejects_changed_bytes(tmp_path):
    artifact = tmp_path / "echo_prime_encoder.pt"
    artifact.write_bytes(b"validated-echoprime-weights")
    expected = hashlib.sha256(artifact.read_bytes()).hexdigest()

    assert download_model.verify_file(artifact, expected) == artifact

    artifact.write_bytes(b"different-weights")
    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        download_model.verify_file(artifact, expected)


def test_extraction_requires_every_validated_checkpoint(tmp_path):
    archive = tmp_path / "model_data.zip"
    with zipfile.ZipFile(archive, "w") as bundle:
        bundle.writestr(download_model.EXPECTED_WEIGHTS_PATH, b"encoder-only")

    with pytest.raises((FileNotFoundError, ValueError)):
        download_model.extract_weights(archive, tmp_path / "models")


def test_download_rejects_unapproved_release_identity(tmp_path):
    with pytest.raises(ValueError, match="validated v1.0.0 encoder"):
        download_model.download_model(
            download_model.EXPECTED_REPOSITORY,
            "a" * 40,
            download_model.EXPECTED_WEIGHTS_PATH,
            download_model.WEIGHT_FILES[download_model.EXPECTED_WEIGHTS_PATH],
            tmp_path,
        )


def test_release_revision_and_version_are_injected_into_model_info():
    revision = "a" * 40
    model_info = load_model_info(ROOT / "data/model_info.json")
    payload = model_info_payload(
        model_info,
        source_revision=revision,
        model_version="1.2.2-rc.404.aaaaaaaaaaaa",
    )

    assert payload["provenance"]["sourceRevision"] == revision
    assert payload["version"] == "1.2.2-rc.404.aaaaaaaaaaaa"


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

    assert "python download_model.py" in dockerfile
    assert "--output-dir models" in dockerfile
    assert "EXPOSE 80" in dockerfile


def test_runtime_accepts_the_official_plain_view_classifier_state_dict():
    logic = (ROOT / "logic.py").read_text(encoding="utf-8")

    assert 'if "state_dict" in vc_checkpoint:' in logic
    assert "vc_state_dict = vc_checkpoint" in logic
