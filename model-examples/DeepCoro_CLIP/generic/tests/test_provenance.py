"""DeepCoro-CLIP generic artifact identity and release-build verification."""

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

    assert manifest["version"] == "1.0.1"
    assert config == {
        "VideoEncoder": {
            "backbone": "mvit",
            "num_frames": 16,
            "pretrained": False,
            "output_dim": 512,
            "freeze_ratio": 1.0,
            "dropout": 0.2,
            "num_heads": 4,
            "aggregator_depth": 1,
            "aggregate_videos_tokens": False,
            "per_video_pool": False,
            "use_rope": False,
        },
        "MultiInstanceLinearProbing": {
            "embedding_dim": 512,
            "pooling_mode": "attention+cls_token",
            "attention_hidden": 256,
            "use_cls_token": True,
            "separate_video_attention": True,
            "normalization_strategy": "post_norm",
        },
        "VideoMILWrapper": {
            "num_videos": 10,
            "stride": 1,
            "resize": 224,
        },
        "ModelStateDict": {
            "model_path": "zcb8cu0l_20260213-033432/best_model_epoch_11.pt",
        },
        "dataset_mean": [122.09012603759766] * 3,
        "dataset_std": [28.790834426879883] * 3,
    }
    assert config["ModelStateDict"]["model_path"] == provenance["weightsPath"]
    assert provenance == {
        "sourceRepository": "HeartWise-AI/pacs-ai-backend",
        "sourceRevision": None,
        "modelRepository": "heartwise/DeepCoro_CLIP_generic",
        "modelRevision": "bb3f5e4785eed5f493d20a5406feed40cace84cf",
        "weightsPath": "zcb8cu0l_20260213-033432/best_model_epoch_11.pt",
        "weightsSha256": "4eac7676bc24930466d84f04f813fcbc1b9e70b056232cad603f18c84d008267",
    }


def test_manifest_upload_limit_matches_model_video_capacity():
    config = json.loads((ROOT / "models/config.json").read_text(encoding="utf-8"))
    manifest = json.loads((ROOT / "data/model_info.json").read_text(encoding="utf-8"))

    assert manifest["dicomUploadMax"] == config["VideoMILWrapper"]["num_videos"] == 10


def test_weight_verification_accepts_only_the_expected_bytes(tmp_path):
    relative_path = Path("zcb8cu0l_20260213-033432/best_model_epoch_11.pt")
    weights = tmp_path / relative_path
    weights.parent.mkdir(parents=True)
    weights.write_bytes(b"verified-deepcoro-clip-generic-weights")
    expected = hashlib.sha256(weights.read_bytes()).hexdigest()

    assert download_model.verify_weights(tmp_path, str(relative_path), expected) == weights

    weights.write_bytes(b"unexpected-weights")
    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        download_model.verify_weights(tmp_path, str(relative_path), expected)


def test_download_is_pinned_to_one_verified_checkpoint(monkeypatch, tmp_path):
    calls = []
    weights_path = "zcb8cu0l_20260213-033432/best_model_epoch_11.pt"

    def snapshot_download(**kwargs):
        calls.append(kwargs)
        weights = Path(kwargs["local_dir"]) / weights_path
        weights.parent.mkdir(parents=True)
        weights.write_bytes(b"expected")

    monkeypatch.setattr("huggingface_hub.snapshot_download", snapshot_download)
    expected = hashlib.sha256(b"expected").hexdigest()

    downloaded = download_model.download_model(
        "token",
        "heartwise/DeepCoro_CLIP_generic",
        "a" * 40,
        weights_path,
        expected,
        tmp_path,
    )

    assert downloaded == tmp_path / weights_path
    assert calls == [
        {
            "repo_id": "heartwise/DeepCoro_CLIP_generic",
            "revision": "a" * 40,
            "token": "token",
            "local_dir": str(tmp_path),
            "allow_patterns": [weights_path],
        }
    ]


def test_release_identity_is_injected_into_runtime_model_info():
    source_revision = "a" * 40
    candidate_version = "1.0.1-rc.999.aaaaaaaaaaaa"
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
        "-t deepcoro-clip-generic:local" in dockerfile
    )


def test_production_context_excludes_credentials_and_preloaded_weights():
    dockerignore = (ROOT / ".dockerignore").read_text(encoding="utf-8")

    assert "hf_token.txt" in dockerignore
    assert "**/hf_token.txt" in dockerignore
    assert "**/*.pt" in dockerignore
