"""DeepCORO-SYNTAX v6 artifact identity and build verification."""

import hashlib
import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

import download_model
from utils.resource_config import load_model_info
from utils.model_provenance import model_info_payload


def test_runtime_configuration_and_manifest_identify_the_same_v6_checkpoint():
    config = json.loads((ROOT / "models/config.json").read_text(encoding="utf-8"))
    manifest = json.loads((ROOT / "data/model_info.json").read_text(encoding="utf-8"))
    provenance = manifest["provenance"]

    assert manifest["version"] == "6.0.0"
    assert config["ModelStateDict"]["model_path"] == download_model.WEIGHTS_PATH
    assert provenance == {
        "sourceRepository": "HeartWise-AI/pacs-ai-backend",
        "sourceRevision": None,
        "modelRepository": download_model.REPOSITORY,
        "modelRevision": download_model.REVISION,
        "weightsPath": download_model.WEIGHTS_PATH,
        "weightsSha256": download_model.WEIGHTS_SHA256,
    }


def test_weight_verification_accepts_only_the_expected_bytes(tmp_path, monkeypatch):
    relative_path = Path("candidate/model.pt")
    weights = tmp_path / relative_path
    weights.parent.mkdir(parents=True)
    weights.write_bytes(b"verified-v6-weights")
    expected = hashlib.sha256(weights.read_bytes()).hexdigest()
    monkeypatch.setattr(download_model, "WEIGHTS_PATH", str(relative_path))
    monkeypatch.setattr(download_model, "WEIGHTS_SHA256", expected)

    assert download_model.verify_weights(tmp_path) == weights

    weights.write_bytes(b"unexpected-weights")
    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        download_model.verify_weights(tmp_path)


def test_source_revision_is_validated_and_injected_into_model_info():
    revision = "a" * 40
    model_info = load_model_info(ROOT / "data/model_info.json")
    payload = model_info_payload(model_info, source_revision=revision)
    assert payload["provenance"]["sourceRevision"] == revision

    with pytest.raises(ValueError, match="sourceRevision"):
        model_info_payload(model_info, source_revision="not-a-git-sha")


def test_dockerfile_labels_match_the_manifest():
    manifest = json.loads((ROOT / "data/model_info.json").read_text(encoding="utf-8"))
    provenance = manifest["provenance"]
    dockerfile = (ROOT / "Dockerfile").read_text(encoding="utf-8")

    for expected in (
        f'org.opencontainers.image.version="{manifest["version"]}"',
        f'ai.heartwise.model.repository="{provenance["modelRepository"]}"',
        f'ai.heartwise.model.revision="{provenance["modelRevision"]}"',
        f'ai.heartwise.model.weights.path="{provenance["weightsPath"]}"',
        f'ai.heartwise.model.weights.sha256="{provenance["weightsSha256"]}"',
    ):
        assert expected in dockerfile
