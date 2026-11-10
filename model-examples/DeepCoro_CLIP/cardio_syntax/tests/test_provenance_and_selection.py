"""CardioSyntax 1.0.1: pinned artifact identity, build metadata, run selection and normalisation."""

import hashlib
import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

import download_model  # noqa: E402
from utils.resource_config import load_model_info, model_info_payload  # noqa: E402


def test_manifest_pins_the_checkpoint_the_service_runs():
    manifest = json.loads((ROOT / "data/model_info.json").read_text(encoding="utf-8"))
    assert manifest["version"] == "1.0.1"
    assert manifest["dicomUploadMax"] == 10  # = VideoMILWrapper.num_videos of the pinned config.json
    assert manifest["provenance"] == {
        "sourceRepository": "HeartWise-AI/pacs-ai-backend",
        "sourceRevision": None,
        "modelRepository": "heartwise/deepcoro_clip_cardiosyntax",
        "modelRevision": "1ffb38cfc10fa10c4b60f44746063feb860eeba0",
        "weightsPath": "rxt2fz27_20250711-171619/models/best_model_epoch_17.pt",
        "weightsSha256": "7c30f757fadf1c36200f763c733463e8574791cf8ff7116d58390ae76e222a2b",
    }


def test_weight_verification_accepts_only_the_expected_bytes(tmp_path):
    weights = tmp_path / "w.pt"
    weights.write_bytes(b"verified")
    expected = hashlib.sha256(b"verified").hexdigest()
    assert download_model.verify_weights(tmp_path, "w.pt", expected) == weights
    weights.write_bytes(b"other")
    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        download_model.verify_weights(tmp_path, "w.pt", expected)


def test_release_metadata_is_injected():
    payload = model_info_payload(load_model_info(ROOT / "data/model_info.json"), source_revision="b" * 40,
                                 model_version="1.0.1-rc.1.bbbbbbbbbbbb")
    assert payload["provenance"]["sourceRevision"] == "b" * 40


def test_dockerfile_downloads_pinned_weights_and_the_runtime_config():
    dockerfile = (ROOT / "Dockerfile").read_text(encoding="utf-8")
    for arg in ("PACS_AI_MODEL_REPOSITORY", "PACS_AI_MODEL_REVISION", "PACS_AI_WEIGHTS_PATH", "PACS_AI_WEIGHTS_SHA256",
                "PACS_AI_SOURCE_REVISION", "PACS_AI_MODEL_VERSION", "PACS_AI_SOURCE_REPOSITORY"):
        assert f"ARG {arg}" in dockerfile and f"${{{arg}}}" in dockerfile
    assert "--extra-file config.json" in dockerfile
    assert "--token $(cat" not in dockerfile  # token is read from the secret file, never put on a command line


def test_runs_are_taken_in_series_time_order():
    pytest.importorskip("torch")
    from pydicom.dataset import Dataset
    from logic import CustomPredictionService

    def ds(t):
        d = Dataset()
        if t is not None:
            d.SeriesTime = t
        return d

    runs = [ds("101500"), ds(None), ds("093000.5"), ds("100000")]
    ordered = sorted(runs, key=CustomPredictionService._extract_series_time)
    assert [getattr(d, "SeriesTime", None) for d in ordered] == ["093000.5", "100000", "101500", None]


def test_normalisation_defaults_to_training_stats_and_config_overrides():
    pytest.importorskip("torch")
    from logic import CustomPredictionService

    svc = CustomPredictionService.__new__(CustomPredictionService)
    CustomPredictionService.model_config = {}
    assert svc._get_dataset_normalization_stats()[0][0] == pytest.approx(105.27, abs=0.01)
    CustomPredictionService.model_config = {"dataset_mean": [1, 1, 1], "dataset_std": [2, 2, 2]}
    assert svc._get_dataset_normalization_stats() == ([1, 1, 1], [2, 2, 2])
