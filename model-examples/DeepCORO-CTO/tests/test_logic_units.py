"""Unit tests that do not require model weights or a GPU."""

from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from logic import CustomPredictionService, classify_view  # noqa: E402


@pytest.fixture
def service() -> CustomPredictionService:
    svc = CustomPredictionService.__new__(CustomPredictionService)
    mapping_path = ROOT / "models" / "class_mapping.json"
    CustomPredictionService._class_mapping = json.loads(mapping_path.read_text())
    return svc


@pytest.mark.parametrize(
    ("primary", "secondary", "expected"),
    [
        (-90, 0, "RAO Lateral"),
        (90, 0, "LAO Lateral"),
        (-30, 30, "RAO Cranial"),
        (0, 30, "AP Cranial"),
        (30, 30, "LAO Cranial"),
        (-30, 0, "RAO Straight"),
        (0, 0, "AP"),
        (30, 0, "LAO Straight"),
        (-30, -30, "RAO Caudal"),
        (0, -30, "AP Caudal"),
        (30, -30, "LAO Caudal"),
        (60, 60, "Other"),
        (None, 0, None),
        (-30, None, None),
        ("bad", 0, None),
    ],
)
def test_classify_view(primary, secondary, expected):
    assert classify_view(primary, secondary) == expected


@pytest.mark.parametrize(
    ("score", "expected"),
    [
        (0.0, "easy"),
        (0.9, "easy"),
        (1.0, "intermediate"),
        (1.9, "intermediate"),
        (2.0, "difficult"),
        (2.9, "difficult"),
        (3.0, "very difficult"),
        (4.0, "very difficult"),
    ],
)
def test_difficulty_band(score, expected):
    assert CustomPredictionService._difficulty_band(score) == expected


def test_format_predictions_renames_threshold_count(service: CustomPredictionService):
    preds = {
        "jcto_blunt_stump": 0.2,
        "jcto_calcification": 0.8,
        "jcto_bending_gt45": 0.6,
        "jcto_occlusion_length_gt20": 0.1,
        "jcto_score": 2.15,
    }
    formatted = service._format_predictions(preds)
    assert "componentsPresent" not in formatted["jctoScore"]
    assert formatted["jctoScore"]["componentsAboveThreshold"] == 2
    assert formatted["jctoScore"]["predicted"] == 2.15
    assert formatted["jctoScore"]["difficulty"] == "difficult"
    assert formatted["components"]["jcto_calcification"]["present"] is True
    assert formatted["components"]["jcto_blunt_stump"]["present"] is False


def test_recommendations_mention_imaging_only_score(service: CustomPredictionService):
    recs = service._recommendations({"jcto_score": 2.1})
    assert "0–4" in recs["en"] or "0-4" in recs["en"]
    assert "previously failed" in recs["en"]
    assert "quatre composantes" in recs["fr"]
    assert recs["presentable"] is True
