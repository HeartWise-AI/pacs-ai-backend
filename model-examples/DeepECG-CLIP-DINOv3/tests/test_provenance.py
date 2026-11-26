"""DeepECG-CLIP-DINOv3 artifact identity and build verification."""

import hashlib
import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

import download_model  # noqa: E402
from utils.resource_config import load_model_info, model_info_payload  # noqa: E402

REVISION = "3eab18b498f1b05f7749052fd40d00980b4c16d1"
ENCODER_SHA256 = "335aaec39064d009c31710a96fe17d98450d85611c5bb0271ce3aad118149ae6"


def test_manifest_pins_the_published_encoder_and_heads():
    manifest = json.loads((ROOT / "data/model_info.json").read_text(encoding="utf-8"))
    assert manifest["version"] == "1.1.0"
    assert manifest["provenance"] == {
        "sourceRepository": "HeartWise-AI/pacs-ai-backend",
        "sourceRevision": None,
        "modelRepository": "heartwise/DeepECG-PNG_DINOv3-SigLIP2_epoch030",
        "modelRevision": REVISION,
        "weightsPath": "base/epoch_030.pt",
        "weightsSha256": ENCODER_SHA256,
    }
    assert f'PACS_AI_MODEL_REVISION="{REVISION}"' in (ROOT / "Dockerfile.local").read_text()
    sys.path.insert(0, str(ROOT))
    from models.dinov3_bank import ENCODER_SHA256 as BANK_SHA256, REPO_ID
    assert (BANK_SHA256, REPO_ID) == (ENCODER_SHA256, manifest["provenance"]["modelRepository"])


def test_heads_enabled_by_decision_of_2026_11_26():
    config = json.loads((ROOT / "models/config.json").read_text(encoding="utf-8"))
    assert config["variant"] == "clean"
    assert config["heads"] == ["ecg77", "shd", "lvef_le40", "lvef_lt50", "afib_5y", "hcm"]
    assert config["disabled_heads"] == ["aco", "hcm_3class", "acco_vs_aico"]   # the probe ACO: the fine-tune serves aco
    # the fully fine-tuned ACO model: latest revision of heartwise/deepecg_clip_dinov3_acs, same bytes as the NAS copy
    # (/mnt/data1/models/DeepECG-PNG/acs_ft20_dinov3_siglip2_epoch030/SHA256SUMS)
    assert config["fine_tuned"]["aco"] == {
        "label": "ACO (acute coronary occlusion), fully fine-tuned DINOv3",
        "threshold": 0.05062838,
        "repository": "heartwise/deepecg_clip_dinov3_acs",
        "revision": "badd718a6e26be24c9ad2dd3c911160a7d4fbe06",
        "path": "acs_ft20_dinov3_siglip2_epoch030/aco/best_model.pth",
        "sha256": "49db22c3f252d6ba13ec87562df07e4da4d68e5d1ca282dffb484b7c10459026",
    }


def _fake_release(tmp_path: Path) -> str:
    aco = tmp_path / "ft/aco.pth"
    aco.parent.mkdir()
    aco.write_bytes(b"fine-tune")
    (tmp_path / "config.json").write_text(json.dumps({"fine_tuned": {"aco": {
        "path": "ft/aco.pth", "sha256": hashlib.sha256(b"fine-tune").hexdigest()}}}))
    (tmp_path / "base").mkdir()
    (tmp_path / "base/epoch_030.pt").write_bytes(b"encoder")
    head = tmp_path / "probes/shd_clean"
    head.mkdir(parents=True)
    for name in ("head.json", "classifier.safetensors"):
        (head / name).write_bytes(name.encode())
    (tmp_path / "probes/config.json").write_text("{}")
    lines = [f"{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.relative_to(tmp_path / 'probes').as_posix()}"
             for p in sorted((tmp_path / "probes").rglob("*")) if p.is_file()]
    (tmp_path / "probes/SHA256SUMS").write_text("\n".join(lines) + "\n")
    return hashlib.sha256(b"encoder").hexdigest()


def test_encoder_and_every_listed_probe_file_are_verified(tmp_path):
    sha = _fake_release(tmp_path)
    verified = download_model.verify_all(tmp_path, "base/epoch_030.pt", sha)
    assert len(verified) == 5 and verified[-1] == tmp_path / "ft/aco.pth"
    (tmp_path / "ft/aco.pth").write_bytes(b"other")
    with pytest.raises(ValueError, match="aco.pth"):
        download_model.verify_all(tmp_path, "base/epoch_030.pt", sha)
    (tmp_path / "ft/aco.pth").write_bytes(b"fine-tune")
    (tmp_path / "probes/shd_clean/head.json").write_text("changed")
    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        download_model.verify_all(tmp_path, "base/epoch_030.pt", sha)
    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        download_model.verify_weights(tmp_path, "base/epoch_030.pt", "0" * 64)
    (tmp_path / "probes/shd_clean/head.json").unlink()
    with pytest.raises(FileNotFoundError):
        download_model.verify_probes(tmp_path)


def test_release_revision_and_version_are_injected_into_model_info():
    revision = "a" * 40
    model_info = load_model_info(ROOT / "data/model_info.json")
    payload = model_info_payload(model_info, source_revision=revision, model_version="1.1.0-rc.1.aaaaaaaaaaaa")
    assert payload["provenance"]["sourceRevision"] == revision
    assert payload["version"] == "1.1.0-rc.1.aaaaaaaaaaaa"
    with pytest.raises(ValueError, match="sourceRevision"):
        model_info_payload(model_info, source_revision="not-a-git-sha")


def test_official_dockerfile_consumes_release_metadata():
    dockerfile = (ROOT / "Dockerfile").read_text(encoding="utf-8")
    for build_argument in (
        "PACS_AI_SOURCE_REPOSITORY", "PACS_AI_SOURCE_REVISION", "PACS_AI_MODEL_VERSION",
        "PACS_AI_MODEL_REPOSITORY", "PACS_AI_MODEL_REVISION", "PACS_AI_WEIGHTS_PATH", "PACS_AI_WEIGHTS_SHA256",
    ):
        assert f"ARG {build_argument}" in dockerfile
        assert f"${{{build_argument}}}" in dockerfile
    assert "mount=type=secret,id=hf_token" in dockerfile
    assert "--token-file /run/secrets/hf_token" in dockerfile


def test_torch_and_transformers_pins_match_the_training_runtime():
    requirements = (ROOT / "requirements.txt").read_text(encoding="utf-8").splitlines()
    assert "--extra-index-url https://download.pytorch.org/whl/cu126" in requirements
    assert "torch==2.13.0+cu126" in requirements
    assert "transformers==5.17.0" in requirements
    assert "matplotlib==3.10.9" in requirements and "pillow==12.3.0" in requirements
