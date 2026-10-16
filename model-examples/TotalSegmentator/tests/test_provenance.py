"""TotalSegmentator 2.9.1 artifact identity and build verification."""

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


def test_manifest_identifies_the_validated_task_297_archive():
    manifest = json.loads((ROOT / "data/model_info.json").read_text(encoding="utf-8"))

    assert manifest["version"] == "2.9.1"
    assert manifest["provenance"] == {
        "sourceRepository": "HeartWise-AI/pacs-ai-backend",
        "sourceRevision": None,
        "modelRepository": download_model.EXPECTED_REPOSITORY,
        "modelRevision": download_model.EXPECTED_REVISION,
        "weightsPath": download_model.EXPECTED_WEIGHTS_PATH,
        "weightsSha256": "0baa2c8de2975600eb31801dd5c1825cd2b356f794498659cf3348714c073394",
    }


def test_archive_verification_accepts_only_the_expected_bytes(tmp_path):
    archive = tmp_path / "weights.zip"
    archive.write_bytes(b"validated-total-segmentator-weights")
    expected = hashlib.sha256(archive.read_bytes()).hexdigest()

    assert download_model.verify_weights(tmp_path, archive.name, expected) == archive

    archive.write_bytes(b"different-weights")
    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        download_model.verify_weights(tmp_path, archive.name, expected)


def test_extraction_rejects_paths_outside_the_install_directory(tmp_path):
    archive = tmp_path / "unsafe.zip"
    with zipfile.ZipFile(archive, "w") as bundle:
        bundle.writestr("../checkpoint.pth", b"not-safe")

    with pytest.raises(ValueError, match="Unsafe path"):
        download_model.safe_extract(archive, tmp_path / "install")


def test_release_revision_and_version_are_injected_into_model_info():
    revision = "a" * 40
    model_info = load_model_info(ROOT / "data/model_info.json")
    candidate_version = "2.9.1-rc.397.aaaaaaaaaaaa"
    payload = model_info_payload(
        model_info,
        source_revision=revision,
        model_version=candidate_version,
    )

    assert payload["provenance"]["sourceRevision"] == revision
    assert payload["version"] == candidate_version


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


def test_inference_stack_pins_match_the_validated_runtime():
    requirements = (ROOT / "requirements.txt").read_text(encoding="utf-8").splitlines()

    assert {
        "TotalSegmentator==2.9.0",
        "nnunetv2==2.6.2",
        "dynamic-network-architectures==0.4.2",
        "numpy==1.26.4",
        "scipy==1.15.3",
        "scikit-image==0.25.2",
        "torch==2.7.1",
        "torchvision==0.22.1",
        "triton==3.3.1",
    }.issubset(requirements)
