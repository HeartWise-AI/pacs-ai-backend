"""Hemorrhage 1.0.1 artifact identity and build verification."""

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


def test_manifest_identifies_the_validated_model_bundle():
    manifest = json.loads((ROOT / "data/model_info.json").read_text(encoding="utf-8"))

    assert manifest["version"] == "1.0.1"
    assert manifest["provenance"] == {
        "sourceRepository": "HeartWise-AI/pacs-ai-backend",
        "sourceRevision": None,
        "modelRepository": download_model.EXPECTED_REPOSITORY,
        "modelRevision": download_model.EXPECTED_REVISION,
        "weightsPath": download_model.EXPECTED_WEIGHTS_PATH,
        "weightsSha256": "11b3036f5286aae4314e122b20c0c00abd58b5f4f80fb883e1af604700a0cdf5",
    }


def test_archive_verification_accepts_only_the_expected_bytes(tmp_path):
    archive = tmp_path / "weights.zip"
    archive.write_bytes(b"validated-hemorrhage-weights")
    expected = hashlib.sha256(archive.read_bytes()).hexdigest()

    assert download_model.verify_weights(tmp_path, archive.name, expected) == archive

    archive.write_bytes(b"different-weights")
    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        download_model.verify_weights(tmp_path, archive.name, expected)


def test_extraction_rejects_paths_outside_the_model_directory(tmp_path):
    archive = tmp_path / "unsafe.zip"
    with zipfile.ZipFile(archive, "w") as bundle:
        bundle.writestr("../checkpoint.pth", b"not-safe")

    with pytest.raises(ValueError, match="Unsafe path"):
        download_model.safe_extract(archive, tmp_path / "models")


def test_release_revision_and_version_are_injected_into_model_info():
    revision = "a" * 40
    model_info = load_model_info(ROOT / "data/model_info.json")
    candidate_version = "1.0.1-rc.398.aaaaaaaaaaaa"
    payload = model_info_payload(
        model_info,
        source_revision=revision,
        model_version=candidate_version,
    )

    assert payload["provenance"]["sourceRevision"] == revision
    assert payload["version"] == candidate_version


def test_official_dockerfile_consumes_release_metadata_and_secret():
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


def test_inference_stack_pins_match_the_validated_runtime():
    requirements = set((ROOT / "requirements.txt").read_text(encoding="utf-8").splitlines())

    assert {
        "nnunetv2==2.5.2",
        "dynamic-network-architectures==0.3.1",
        "numpy==1.26.4",
        "scipy==1.15.1",
        "scikit-image==0.25.1",
        "torch==2.5.1",
        "torchvision==0.20.1",
        "torchaudio==2.5.1",
        "triton==3.1.0",
    }.issubset(requirements)


def test_runtime_selects_the_verified_fold_zero_checkpoint():
    logic = (ROOT / "logic.py").read_text(encoding="utf-8")

    assert "use_folds = 0," in logic
    assert "checkpoint_name='checkpoint_final.pth'" in logic

