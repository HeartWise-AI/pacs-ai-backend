"""DeepRV-CLIP artifact identity and release-build verification."""

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
        "modelRepository": "heartwise/DeepRV_CLIP",
        "modelRevision": "2d9a4847982bac5bdd140bcc552cf9fbec465552",
        "weightsPath": "best_model_epoch_4.pt",
        "weightsSha256": "9e0c9dc43b0554b16483de2968c43be5e6954b3142cb9d28e397e524cd4d373d",
    }


def test_weight_verification_accepts_only_the_expected_bytes(tmp_path):
    relative_path = Path("best_model_epoch_4.pt")
    weights = tmp_path / relative_path
    weights.write_bytes(b"verified-deeprv-clip-weights")
    expected = hashlib.sha256(weights.read_bytes()).hexdigest()

    assert download_model.verify_weights(tmp_path, str(relative_path), expected) == weights

    weights.write_bytes(b"unexpected-weights")
    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        download_model.verify_weights(tmp_path, str(relative_path), expected)


def test_download_is_pinned_to_one_verified_checkpoint(monkeypatch, tmp_path):
    calls = []

    def snapshot_download(**kwargs):
        calls.append(kwargs)
        weights = Path(kwargs["local_dir"]) / "best_model_epoch_4.pt"
        weights.write_bytes(b"expected")

    monkeypatch.setattr("huggingface_hub.snapshot_download", snapshot_download)
    expected = hashlib.sha256(b"expected").hexdigest()

    downloaded = download_model.download_model(
        "token",
        "heartwise/DeepRV_CLIP",
        "a" * 40,
        "best_model_epoch_4.pt",
        expected,
        tmp_path,
    )

    assert downloaded == tmp_path / "best_model_epoch_4.pt"
    assert calls == [
        {
            "repo_id": "heartwise/DeepRV_CLIP",
            "revision": "a" * 40,
            "token": "token",
            "local_dir": str(tmp_path),
            "allow_patterns": ["best_model_epoch_4.pt"],
        }
    ]


def test_release_identity_is_injected_into_runtime_model_info():
    source_revision = "a" * 40
    candidate_version = "1.0.0-rc.375.aaaaaaaaaaaa"
    model_info = load_model_info(ROOT / "data/model_info.json")
    payload = model_info_payload(
        model_info,
        source_revision=source_revision,
        model_version=candidate_version,
    )

    assert payload["provenance"]["sourceRevision"] == source_revision
    assert payload["version"] == candidate_version

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
    assert "--token " not in dockerfile


def test_local_dockerfile_is_marked_local_and_verifies_preloaded_weights():
    dockerfile = (ROOT / "Dockerfile.local").read_text(encoding="utf-8")

    assert 'ai.heartwise.release.channel="local"' in dockerfile
    assert "--verify-only" in dockerfile
    assert (
        "docker build --platform linux/amd64 -f Dockerfile.local "
        "-t deeprv-clip:local" in dockerfile
    )


def test_production_context_excludes_credentials_and_preloaded_weights():
    dockerignore = (ROOT / ".dockerignore").read_text(encoding="utf-8")

    assert "hf_token.txt" in dockerignore
    assert "**/hf_token.txt" in dockerignore
    assert "**/*.pt" in dockerignore
