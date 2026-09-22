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


# -- v2: per-artery checkpoints ------------------------------------------


def _per_artery_preds() -> dict:
    preds = {}
    for artery, score in (("lad", 0.31), ("rca", 2.1), ("lcx", 0.12)):
        preds[f"jcto_score_{artery}"] = score
        for head, prob in (
            ("jcto_blunt_stump", 0.32),
            ("jcto_calcification", 0.71),
            ("jcto_bending_gt45", 0.64),
            ("jcto_occlusion_length_gt20", 0.41),
        ):
            preds[f"{head}_{artery}"] = prob if artery == "rca" else 0.05
    return preds


def test_class_mapping_lists_fifteen_per_artery_heads(service: CustomPredictionService):
    cm = CustomPredictionService._class_mapping
    expected = {
        f"{h}_{a}"
        for h in CustomPredictionService.COMPONENT_HEADS + [CustomPredictionService.SCORE_HEAD]
        for a in CustomPredictionService.ARTERIES
    }
    assert set(cm) == expected
    for h in CustomPredictionService.COMPONENT_HEADS:
        for a in CustomPredictionService.ARTERIES:
            assert cm[f"{h}_{a}"]["task"] == "binary_classification"
    for a in CustomPredictionService.ARTERIES:
        assert cm[f"jcto_score_{a}"] == {
            "head_dim": 1,
            "task": "regression",
            "name": cm[f"jcto_score_{a}"]["name"],
            "unit": "points",
            "min": 0,
            "max": 4,
        }


@pytest.mark.parametrize(
    ("score", "expected"),
    [(-1.0, 0), (0.0, 0), (0.49, 0), (0.5, 1), (1.49, 1), (2.15, 2), (2.5, 3), (3.9, 4), (4.7, 4)],
)
def test_score_int(score, expected):
    assert CustomPredictionService._score_int(score) == expected


def test_select_artery_picks_highest_score(service: CustomPredictionService):
    preds = service._select_artery(_per_artery_preds())
    assert preds[CustomPredictionService.ARTERY_KEY] == "rca"
    assert preds["jcto_score"] == 2.1
    assert preds["jcto_calcification"] == 0.71
    assert preds["jcto_blunt_stump"] == 0.32
    # per-artery outputs are kept for the perArtery report
    assert preds["jcto_score_lad"] == 0.31


def test_select_artery_passthrough_for_overall_heads(service: CustomPredictionService):
    preds = {"jcto_score": 1.2, "jcto_calcification": 0.9}
    assert service._select_artery(dict(preds)) == preds


def test_format_predictions_per_artery(service: CustomPredictionService):
    preds = service._select_artery(_per_artery_preds())
    formatted = service._format_predictions(preds)
    assert formatted["ctoArtery"] == "RCA"
    assert formatted["jctoScore"]["predicted"] == 2.1
    assert formatted["jctoScore"]["predictedInteger"] == 2
    assert formatted["jctoScore"]["difficulty"] == "difficult"
    assert formatted["jctoScore"]["componentsAboveThreshold"] == 2
    # labels come from the per-artery class_mapping entry, without the artery prefix
    assert formatted["components"]["jcto_calcification"]["name"] == "Calcification at occlusion"
    assert formatted["components"]["jcto_calcification"]["present"] is True
    assert set(formatted["perArtery"]) == {"LAD", "RCA", "LCx"}
    assert formatted["perArtery"]["LAD"]["jctoScore"] == 0.31
    assert formatted["perArtery"]["LAD"]["jctoScoreInteger"] == 0
    assert formatted["perArtery"]["RCA"]["components"]["jcto_bending_gt45"] == 0.64


def test_format_predictions_overall_has_no_artery_fields(service: CustomPredictionService):
    formatted = service._format_predictions({"jcto_score": 1.2, "jcto_calcification": 0.9})
    assert "ctoArtery" not in formatted
    assert "perArtery" not in formatted
    assert formatted["jctoScore"]["predictedInteger"] == 1


def test_diagnosis_text_per_artery(service: CustomPredictionService):
    preds = service._select_artery(_per_artery_preds())
    text = service._diagnosis_text(preds)
    assert text.startswith("DeepCORO-CTO [RCA]: J-CTO score = 2 (raw 2.1, difficult)")
    assert "Calcification at occlusion" in text
    assert "Bending > 45°" in text
    assert "Blunt" not in text


def test_recommendations_and_html_mention_artery(service: CustomPredictionService):
    preds = service._select_artery(_per_artery_preds())
    recs = service._recommendations(preds)
    assert "score 2 in the RCA (raw 2.1, difficult)" in recs["en"]
    assert "prédit 2 pour l’artère coronaire droite (brut 2.1, difficult)" in recs["fr"]
    assert "Caution" not in recs["en"]
    html = service._render_html(preds, recs)
    assert "CTO artery: RCA" in html
    assert "RCA <strong>(selected)</strong>" in html
    assert "<td>LAD</td>" in html


def test_lcx_call_carries_warning(service: CustomPredictionService):
    preds = _per_artery_preds()
    preds["jcto_score_lcx"] = 3.2  # make LCx the selected artery
    preds = service._select_artery(preds)
    assert preds[CustomPredictionService.ARTERY_KEY] == "lcx"
    formatted = service._format_predictions(preds)
    assert formatted["ctoArtery"] == "LCx"
    assert "unreliable" in formatted["ctoArteryWarning"]["en"]
    assert "circonflexe" in formatted["ctoArteryWarning"]["fr"]
    assert service._diagnosis_text(preds).endswith("| CAUTION: LCx predictions unreliable")
    recs = service._recommendations(preds)
    assert "pour l’artère circonflexe" in recs["fr"]
    assert "Caution: LCx predictions are unreliable" in recs["en"]
    assert "Attention : les prédictions pour l’artère circonflexe" in recs["fr"]
    html = service._render_html(preds, recs)
    assert "LCx predictions are unreliable" in html
    assert "LCx <strong>(selected)</strong>" in html


def test_lad_call_uses_french_artery_name(service: CustomPredictionService):
    preds = _per_artery_preds()
    preds["jcto_score_lad"] = 3.6
    preds = service._select_artery(preds)
    recs = service._recommendations(preds)
    assert "pour l’artère interventriculaire antérieure" in recs["fr"]
    assert "ctoArteryWarning" not in service._format_predictions(preds)
