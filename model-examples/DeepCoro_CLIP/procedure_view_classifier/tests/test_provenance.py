"""The release provenance, the downloader pins and the served contract agree."""

import hashlib
import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

import download_model  # noqa: E402

MANIFEST = json.loads((ROOT / "data" / "model_info.json").read_text(encoding="utf-8"))
PROVENANCE = MANIFEST["provenance"]


def test_provenance_matches_the_downloader_pins_and_the_preprocessing_contract():
    assert PROVENANCE["sourceRepository"] == "HeartWise-AI/pacs-ai-backend"
    assert PROVENANCE["sourceRevision"] is None
    assert PROVENANCE["modelRepository"] == download_model.EXPECTED_REPOSITORY
    assert PROVENANCE["modelRevision"] == download_model.EXPECTED_REVISION
    assert PROVENANCE["weightsPath"] == download_model.EXPECTED_WEIGHTS_PATH
    contract = MANIFEST["preprocessingContract"]
    assert PROVENANCE["weightsSha256"] == contract["weightsSha256"]
    assert download_model.EXTRA_FILES_SHA256["class_mapping.json"] == contract["classMappingSha256"]


@pytest.mark.parametrize(
    "field", ["repository", "revision", "weights_path"],
)
def test_release_metadata_must_identify_the_validated_artifacts(field):
    identity = {
        "repository": download_model.EXPECTED_REPOSITORY,
        "revision": download_model.EXPECTED_REVISION,
        "weights_path": download_model.EXPECTED_WEIGHTS_PATH,
    }
    download_model.require_release_identity(**identity)
    identity[field] = "other"
    with pytest.raises(ValueError):
        download_model.require_release_identity(**identity)


def _write(path: Path, content: bytes) -> str:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(content)
    return hashlib.sha256(content).hexdigest()


def test_verify_only_checks_every_packaged_artifact(tmp_path, monkeypatch):
    models = tmp_path / "models"
    weights_sha = _write(models / "vaso_vision.pt", b"weights")
    extras = {name: _write(models / name, name.encode()) for name in download_model.EXTRA_FILES_SHA256}
    monkeypatch.setattr(download_model, "EXTRA_FILES_SHA256", extras)
    hub = tmp_path / "hub"
    _write(hub / "hubconf.py", b"")
    _write(hub / ".git" / "HEAD", download_model.PYTORCHVIDEO_REVISION.encode() + b"\n")
    checkpoint = tmp_path / "torch" / "X3D_M.pyth"
    monkeypatch.setattr(download_model, "X3D_M_SHA256", _write(checkpoint, b"x3d"))
    monkeypatch.setattr(download_model, "torch_hub_checkpoint", lambda: checkpoint)

    download_model.verify_artifacts(models, "vaso_vision.pt", weights_sha, hub)

    with pytest.raises(ValueError):
        download_model.verify_artifacts(models, "vaso_vision.pt", "0" * 64, hub)
    (models / "class_mapping.json").write_bytes(b"changed")
    with pytest.raises(ValueError):
        download_model.verify_artifacts(models, "vaso_vision.pt", weights_sha, hub)


def test_an_unpinned_pytorchvideo_checkout_is_rejected(tmp_path):
    _write(tmp_path / "hubconf.py", b"")
    _write(tmp_path / ".git" / "HEAD", b"ref: refs/heads/main\n")
    with pytest.raises(ValueError):
        download_model.pytorchvideo_head(tmp_path)
