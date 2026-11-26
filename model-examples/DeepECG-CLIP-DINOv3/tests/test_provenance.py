"""DeepECG-CLIP-DINOv3 artifact identity and build verification."""

import hashlib
import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(Path(__file__).resolve().parent))

import download_model  # noqa: E402
import probe_fixtures as pf  # noqa: E402
from utils.resource_config import load_model_info, model_info_payload  # noqa: E402

REVISION = "7d35c2e75b8013097c9bb4a578750434e9e10be2"
# /mnt/data1/models/DeepECG-PNG/acs_ft20_dinov3_siglip2_epoch030/SHA256SUMS
NAS_SHA256SUMS = {
    "aco/best_model.pth": "49db22c3f252d6ba13ec87562df07e4da4d68e5d1ca282dffb484b7c10459026",
    "base/epoch_030.pt": "335aaec39064d009c31710a96fe17d98450d85611c5bb0271ce3aad118149ae6",
}


def test_manifest_and_runtime_configuration_identify_the_same_checkpoints():
    config = json.loads((ROOT / "models/config.json").read_text(encoding="utf-8"))
    manifest = json.loads((ROOT / "data/model_info.json").read_text(encoding="utf-8"))
    assert manifest["version"] == "1.4.0"
    assert manifest["provenance"] == {
        "sourceRepository": "HeartWise-AI/pacs-ai-backend",
        "sourceRevision": None,
        "modelRepository": "heartwise/deepecg_clip_dinov3_acs",
        "modelRevision": REVISION,
        "weightsPath": "acs_ft20_dinov3_siglip2_epoch030/aco/best_model.pth",
        "weightsSha256": NAS_SHA256SUMS["aco/best_model.pth"],
    }
    release = "acs_ft20_dinov3_siglip2_epoch030/"
    pinned = {entry["path"]: entry["sha256"] for entry in config["Checkpoints"].values()}
    pinned[config["FrozenEncoder"]["path"]] = config["FrozenEncoder"]["sha256"]
    assert pinned == {release + name: sha for name, sha in NAS_SHA256SUMS.items()}
    assert config["ProbeDirectory"] == release + "probes"
    assert list(config["Checkpoints"]) == list(json.loads((ROOT / "models/class_mapping.json").read_text()))
    # acco_vs_aico (ACCO vs AICO) is not served since 1.4.0: not loaded, not downloaded, not reported
    assert list(config["Checkpoints"]) == ["aco"]
    assert f'PACS_AI_MODEL_REVISION="{REVISION}"' in (ROOT / "Dockerfile.local").read_text()


def _fake_release(tmp_path: Path, probes: bool) -> tuple[str, str]:
    """A models/ folder with the real config.json and stand-in checkpoints pinned by their hashes."""
    config = json.loads((ROOT / "models/config.json").read_text(encoding="utf-8"))
    for head, entry in config["Checkpoints"].items():
        path = tmp_path / entry["path"]
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(head.encode())
        entry["sha256"] = hashlib.sha256(head.encode()).hexdigest()
    frozen = tmp_path / config["FrozenEncoder"]["path"]
    frozen.parent.mkdir(parents=True, exist_ok=True)
    frozen.write_bytes(b"frozen")
    config["FrozenEncoder"]["sha256"] = hashlib.sha256(b"frozen").hexdigest()
    if probes:
        pf.write_head(tmp_path / config["ProbeDirectory"], "shd_clean", ["SHD"], {"SHD": 0.1},
                      encoder_sha256=config["FrozenEncoder"]["sha256"])
        probe_dir = tmp_path / config["ProbeDirectory"]
        files = sorted(p for p in probe_dir.rglob("*") if p.is_file())
        (probe_dir / "SHA256SUMS").write_text("".join(
            f"{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.relative_to(probe_dir).as_posix()}\n" for p in files))
    (tmp_path / "config.json").write_text(json.dumps(config))
    (tmp_path / "encoder_config.json").write_text((ROOT / "models/encoder_config.json").read_text())
    aco = config["Checkpoints"]["aco"]
    return aco["path"], aco["sha256"]


@pytest.mark.parametrize("probes", [False, True])
def test_every_pinned_file_is_verified(tmp_path, probes, monkeypatch):
    path, sha = _fake_release(tmp_path, probes)
    from models import dinov3_bank
    # the fake release's frozen encoder stands in for base/epoch_030.pt, which the bank pins by hash
    monkeypatch.setattr(dinov3_bank, "ENCODER_SHA256", hashlib.sha256(b"frozen").hexdigest())
    verified = download_model.verify_all(tmp_path, path, sha)
    names = {p.relative_to(tmp_path).as_posix() for p in verified}
    assert any(n.endswith("aco/best_model.pth") for n in names)
    assert not any("acco_vs_aico" in n for n in names)
    assert any(n.endswith("base/epoch_030.pt") for n in names) is probes
    assert any(n.endswith("shd_clean/head.json") for n in names) is probes
    assert any(n.endswith("probes/SHA256SUMS") for n in names) is probes

    (tmp_path / "acs_ft20_dinov3_siglip2_epoch030/aco/best_model.pth").write_bytes(b"other")
    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        download_model.verify_all(tmp_path, path, sha)


def test_the_manifest_checkpoint_must_be_a_pinned_head(tmp_path):
    path, _ = _fake_release(tmp_path, probes=False)
    with pytest.raises(ValueError, match="is not a checkpoint"):
        download_model.verify_all(tmp_path, path, "0" * 64)


def test_release_revision_and_version_are_injected_into_model_info():
    revision = "a" * 40
    model_info = load_model_info(ROOT / "data/model_info.json")
    payload = model_info_payload(model_info, source_revision=revision, model_version="1.0.0-rc.1.aaaaaaaaaaaa")
    assert payload["provenance"]["sourceRevision"] == revision
    assert payload["version"] == "1.0.0-rc.1.aaaaaaaaaaaa"
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
    assert "torchvision==0.28.0+cu126" in requirements
    assert "transformers==5.17.0" in requirements
    assert "matplotlib==3.10.9" in requirements and "pillow==12.3.0" in requirements


@pytest.mark.parametrize("dockerfile", ["Dockerfile", "Dockerfile.local"])
def test_download_step_has_every_module_it_imports(dockerfile):
    """download_model.py validates the probes with models/probes.py -> models/dinov3_bank.py, which imports
    utils.log_config: both must be copied into the image before the weights download runs."""
    lines = (ROOT / dockerfile).read_text().splitlines()
    download = next(i for i, line in enumerate(lines) if "download_model.py" in line and line.lstrip().startswith(("RUN", "python")))
    if download and lines[download].lstrip().startswith("python"):
        download = max(i for i in range(download) if lines[i].lstrip().startswith("RUN"))
    copied = " ".join(line for line in lines[:download] if line.startswith("COPY"))
    for needed in ("models/", "download_model.py", "utils/log_config.py"):
        assert needed in copied, f"{dockerfile}: {needed} is copied after the weights download"
