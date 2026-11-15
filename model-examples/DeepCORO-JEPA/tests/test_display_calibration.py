"""Display calibration of the stenosis estimate (data/stenosis_display.json): display only, flags unchanged."""
from __future__ import annotations

import copy
import json
import re
import sys
from pathlib import Path

import pytest
import torch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

import display_calibration as cal  # noqa: E402
from logic import CustomPredictionService  # noqa: E402
from report_input import ResultError, predictions_of  # noqa: E402
from test_logic_units import GROUPS, THRESHOLDS  # noqa: E402


def seg(pct, significant=False, cto=False):
    return {"stenosis_pct": pct, "significant": significant, "cto": cto}


def test_table_is_monotone_with_provenance():
    t = cal.calibration()
    xs, ys = t["knots"]["estimate_pct"], t["knots"]["display_pct"]
    assert t["zero_below_pct"] == xs[0] == 10 and xs == [10, 23, 37, 51, 100] and xs[-1] == ys[-1] == 100
    assert xs == sorted(xs) and ys == sorted(ys) and ys[1:4] == [30, 50, 70]  # knots sit on the category bounds
    assert "validation split only" in t["provenance"]["dataset"].lower() and "LVP" in t["provenance"]["dataset"]
    for key in ("date", "model", "dataset", "method", "validation_metrics_cross_validated"):
        assert t["provenance"][key]


@pytest.mark.parametrize("pct, shown, category", [
    (0.0, 0, "normal"), (8.0, 0, "normal"), (9.99, 0, "normal"),  # very mild estimates are shown as normal
    (10.0, 20, "mild"), (16.5, 25, "mild"), (22.9, 30, "moderate"), (23.0, 30, "moderate"),
    (36.9, 50, "intermediate"), (37.0, 50, "intermediate"), (50.9, 70, "severe"), (51.0, 70, "severe"),
    (75.5, 85, "severe"), (100.0, 100, "severe")])
def test_mapping_through_the_knots(pct, shown, category):
    assert cal.display_pct(seg(pct)) == shown and cal.display_category(seg(pct)) == category


def test_mapping_is_monotone_and_bounded():
    values = [cal.display_pct(seg(x / 10)) for x in range(0, 1001)]
    assert values == sorted(values) and values[0] == 0 and values[-1] == 100
    assert not any(0 < v < 20 for v in values)  # nothing between 0 and the mild floor (no 8% or 10%)


def test_cto_is_occluded_and_a_flag_is_never_normal():
    assert (cal.display_pct(seg(3.0, cto=True)), cal.display_category(seg(3.0, cto=True))) == (100, "occluded")
    assert (cal.display_pct(seg(4.0, significant=True)), cal.display_category(seg(4.0, significant=True))) == (20, "mild")
    assert cal.display_pct(seg(40.0, significant=True)) == cal.display_pct(seg(40.0))  # above T0 the flag changes nothing


def test_bad_table_fails_loudly(tmp_path, monkeypatch):
    table = json.loads(cal.CALIBRATION_PATH.read_text())
    table["knots"]["estimate_pct"] = [10, 37, 23, 51, 100]
    bad = tmp_path / "stenosis_display.json"
    bad.write_text(json.dumps(table))
    monkeypatch.setattr(cal, "CALIBRATION_PATH", bad)
    cal.calibration.cache_clear()
    try:
        with pytest.raises(ValueError):
            cal.calibration()
    finally:
        cal.calibration.cache_clear()


def _preds():
    severity, prob = torch.full((18,), 5.0), torch.full((18,), 0.05)
    severity[GROUPS.index("prox_lad")], prob[GROUPS.index("prox_lad")] = 52.0, 0.6  # flagged, shown 71%
    severity[GROUPS.index("pda")], prob[GROUPS.index("pda")] = 6.0, 0.3  # flagged with a low estimate
    severity[GROUPS.index("mid_rca")] = 30.0  # not flagged, shown 40%
    res = {"encoder_passes": 2, "severity": severity, "binary_probability": prob}
    return CustomPredictionService.postprocess(GROUPS, res, n_videos=2, thresholds=THRESHOLDS)


def test_json_keeps_the_estimate_and_flags_and_adds_display_fields():
    preds = _preds()
    lad, pda, d1 = preds["segments"]["prox_lad"], preds["segments"]["pda"], preds["segments"]["D1"]
    assert (lad["stenosis_pct"], lad["significant"], lad["display_pct"], lad["display_category"]) == (52.0, True, 71, "severe")
    assert (pda["stenosis_pct"], pda["significant"], pda["display_pct"], pda["display_category"]) == (6.0, True, 20, "mild")
    assert (d1["stenosis_pct"], d1["significant"], d1["display_pct"], d1["display_category"]) == (5.0, False, 0, "normal")
    assert predictions_of({"predictions": preds}) is not None


def test_report_and_diagnosis_show_the_displayed_value():
    preds = _preds()
    service = CustomPredictionService.__new__(CustomPredictionService)
    text, html = service.diagnosis_text(preds), service.render_html(preds)
    assert "Proximal LAD (P 0.600, shown 71%, model estimate 52%)" in text
    assert "PDA (P 0.300, shown 20%, model estimate 6%)" in text
    assert "Other segments with a shown stenosis of 30% or more: Mid RCA (40%)." in text
    assert "14 of 17 segments shown as normal (model estimate below 10%)." in text
    assert "Prox LAD 71%" in html and "Mid RCA 40%" in html and "D1 normal" in html and "PDA 20%" in html
    row = re.search(r'<li class="seg"><span class="sname">First diagonal(.*?)</li>', html).group(1)
    assert '<span class="normal">0%</span>' in row and "model estimate 5.0%" in row
    assert "Proximal LAD (71%)" in html  # summary strip
    assert "Estimates below 10% are shown as 0% (normal)" in html
    assert f"DeepCORO-JEPA {CustomPredictionService.report_model_version()}</h1>" in html


def test_report_version_names_the_calibration():
    assert CustomPredictionService.report_model_version() == f"{CustomPredictionService.model_version()}-cal1"


def test_stored_results_render_with_or_without_display_fields():
    preds = _preds()
    older = copy.deepcopy(preds)
    for v in older["segments"].values():
        del v["display_pct"], v["display_category"]  # a 1.3.0 result
    assert CustomPredictionService.report_html(predictions_of({"predictions": older})) == CustomPredictionService.report_html(preds)
    bad = copy.deepcopy(preds)
    bad["segments"]["pda"]["display_category"] = "huge"
    with pytest.raises(ResultError):
        predictions_of({"predictions": bad})
