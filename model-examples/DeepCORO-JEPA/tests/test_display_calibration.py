"""Display calibration of the stenosis estimate: display only, flags unchanged. data/stenosis_display.json is the
mapping of the served model (v19); data/stenosis_display_v15.json and _v13.json are kept for stored results only."""
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


def test_v19_table_is_the_v19_dossier_fit_and_the_cto_flag_alone_is_not_100():
    t = cal.calibration()
    xs, ys = t["knots"]["estimate_pct"], t["knots"]["display_pct"]
    assert t["model_version"] == "v19" and t["version"] == "cal1" and t["rounding"] == "down"
    assert t["zero_below_pct"] == 10.5 and xs == [10.5, 23.5, 33.5, 42.5, 100] and ys == [20, 30, 50, 70, 100]
    assert t["cto_flag_shown_occluded"] is False
    fit = t["provenance"]["validation_metrics_full_fit"]
    assert (fit["exact"], fit["weighted_kappa"]) == (0.663, 0.5911)
    assert "validation split only" in t["provenance"]["dataset"].lower()
    # v19: only the occluded flag shows 100%; the CTO flag alone keeps the mapped value
    assert cal.display_pct(seg(30.0, cto=True)) == 43 and cal.display_category(seg(30.0, cto=True)) == "moderate"
    occluded = {**seg(55.0), "occluded": True}
    assert cal.display_pct(occluded) == 100 and cal.display_category(occluded) == "occluded"
    # v15 / v13 results keep their rule: a CTO flag is shown 100% (occluded)
    assert cal.display_pct(seg(30.0, cto=True), "v15") == 100 == cal.display_pct(seg(30.0, cto=True), "v13")
    for x, shown, name in ((10.4, 0, "normal"), (10.5, 20, "mild"), (23.4, 29, "mild"), (23.5, 30, "moderate"),
                           (33.5, 50, "intermediate"), (42.4, 69, "intermediate"), (42.5, 70, "severe")):
        assert (cal.display_pct(seg(x)), cal.display_category(seg(x))) == (shown, name), x


def test_table_is_monotone_with_provenance():
    t = cal.calibration("v15")
    xs, ys = t["knots"]["estimate_pct"], t["knots"]["display_pct"]
    assert t["model_version"] == "v15" and t["version"] == "cal1" and t["rounding"] == "down"
    assert t["zero_below_pct"] == xs[0] == 9.5 and xs == [9.5, 21.5, 34.5, 49.5, 100] and xs[-1] == ys[-1] == 100
    assert xs == sorted(xs) and ys == [20, 30, 50, 70, 100]  # knots sit on the category bounds
    assert "validation split only" in t["provenance"]["dataset"].lower() and "LVP" in t["provenance"]["dataset"]
    for key in ("date", "model", "dataset", "method", "validation_metrics_cross_validated"):
        assert t["provenance"][key]
    # the numbers of display_mapping.json in the v15 release dossier
    p = t["provenance"]
    assert "v15" in p["model"] and p["validation_metrics_full_fit"] == {
        "n": 78999, "exact": 0.6444, "weighted_kappa": 0.5841, "report_ge70_shown_normal": 0.1158, "report0_shown_normal": 0.7444}
    assert (p["validation_metrics_cross_validated"]["exact"], p["validation_metrics_cross_validated"]["weighted_kappa"]) == (0.6486, 0.5833)
    flagged = p["validation_metrics_with_flag_rule"]
    assert (flagged["exact"], flagged["weighted_kappa"], flagged["report_ge70_shown_normal"]) == (0.6016, 0.5781, 0.0768)


def test_each_model_version_has_its_own_mapping_and_no_other_is_used():
    assert list(cal.calibrations()) == ["v19", "v15", "v13"] and cal.served_model_version() == "v19"
    v13 = cal.calibration("v13")
    assert v13["knots"]["estimate_pct"] == [10, 23, 37, 51, 100] and v13.get("rounding", "nearest") == "nearest"
    assert "v13" in v13["provenance"]["model"] and cal.label("v13") == "v13-cal1" and cal.label("v15") == "v15-cal1" and cal.label() == "v19-cal1"
    with pytest.raises(ValueError, match="No display calibration for model version 'v16'"):
        cal.calibration("v16")
    with pytest.raises(ValueError):
        cal.display_pct(seg(30.0), "v12")
    # the same estimate is shown differently by the two models
    assert (cal.display_pct(seg(22.0), "v13"), cal.display_pct(seg(22.0), "v15")) == (29, 30)
    assert (cal.display_pct(seg(9.7), "v13"), cal.display_pct(seg(9.7), "v15")) == (0, 20)
    assert cal.result_model_version({}) == "v13" and cal.result_model_version({"model_version": "v15"}) == "v15"
    from report_html import MODEL_REPORT_FACTS
    from logic import PRESSURE_TEST_AUROC
    assert set(MODEL_REPORT_FACTS) == set(PRESSURE_TEST_AUROC) == set(cal.calibrations())  # report figures per model


@pytest.mark.parametrize("pct, shown, category", [
    (0.0, 0, "normal"), (8.0, 0, "normal"), (9.4, 0, "normal"),  # very mild estimates are shown as normal
    (9.5, 20, "mild"), (15.5, 25, "mild"), (21.4, 29, "mild"), (21.5, 30, "moderate"),
    (34.4, 49, "moderate"), (34.5, 50, "intermediate"), (49.4, 69, "intermediate"), (49.5, 70, "severe"),
    (74.75, 85, "severe"), (99.9, 99, "severe"), (100.0, 100, "severe")])
def test_mapping_through_the_knots(pct, shown, category):
    """v15: the category changes exactly at the fitted estimates 9.5 / 21.5 / 34.5 / 49.5 (half points kept, the
    interpolated value rounded down so an estimate just below a knot stays in the category below)."""
    assert cal.display_pct(seg(pct), "v15") == shown and cal.display_category(seg(pct), "v15") == category


def test_v15_categories_are_the_fitted_cuts_for_every_returned_estimate():
    cuts = [9.5, 21.5, 34.5, 49.5]
    names = ["normal", "mild", "moderate", "intermediate", "severe"]
    for tenth in range(0, 1001):  # stenosis_pct is returned with one decimal
        x = tenth / 10
        assert cal.display_category(seg(x), "v15") == names[sum(x >= c for c in cuts)], x


@pytest.mark.parametrize("pct, shown, category", [
    (0.0, 0, "normal"), (8.0, 0, "normal"), (9.99, 0, "normal"),
    (10.0, 20, "mild"), (16.5, 25, "mild"), (22.9, 30, "moderate"), (23.0, 30, "moderate"),
    (36.9, 50, "intermediate"), (37.0, 50, "intermediate"), (50.9, 70, "severe"), (51.0, 70, "severe"),
    (75.5, 85, "severe"), (100.0, 100, "severe")])
def test_the_v13_mapping_is_unchanged_for_stored_v13_results(pct, shown, category):
    """Exactly the values 1.4.0 and 1.4.1 returned (nearest rounding)."""
    assert cal.display_pct(seg(pct), "v13") == shown and cal.display_category(seg(pct), "v13") == category


@pytest.mark.parametrize("version", ["v13", "v15"])
def test_mapping_is_monotone_and_bounded(version):
    values = [cal.display_pct(seg(x / 10), version) for x in range(0, 1001)]
    assert values == sorted(values) and values[0] == 0 and values[-1] == 100
    assert not any(0 < v < 20 for v in values)  # nothing between 0 and the mild floor (no 8% or 10%)


@pytest.mark.parametrize("version", ["v13", "v15"])
def test_cto_is_occluded_and_a_flag_is_never_normal(version):
    shown = lambda v: (cal.display_pct(v, version), cal.display_category(v, version))
    assert shown(seg(3.0, cto=True)) == (100, "occluded")
    assert shown(seg(4.0, significant=True)) == (20, "mild")
    assert all(cal.display_pct(seg(x / 10, significant=True), version) >= 20 for x in range(0, 1001))
    assert cal.display_pct(seg(40.0, significant=True), version) == cal.display_pct(seg(40.0), version)  # above T0 the flag changes nothing


@pytest.mark.parametrize("change", [
    lambda t: t["knots"].update(estimate_pct=[9.5, 34.5, 21.5, 49.5, 100]),
    lambda t: t.pop("model_version"),
    lambda t: t.update(rounding="up"),
    lambda t: t.update(model_version="v13"),  # two mappings for one model
])
def test_bad_table_fails_loudly(tmp_path, monkeypatch, change):
    table = json.loads(cal.CALIBRATION_PATH.read_text())
    change(table)
    bad = tmp_path / "stenosis_display.json"
    bad.write_text(json.dumps(table))
    monkeypatch.setattr(cal, "CALIBRATION_PATH", bad)
    cal.calibrations.cache_clear()
    try:
        with pytest.raises(ValueError):
            cal.calibration()
    finally:
        cal.calibrations.cache_clear()


def _preds(model_version: str = "v15"):
    severity, prob = torch.full((18,), 5.0), torch.full((18,), 0.05)
    severity[GROUPS.index("prox_lad")], prob[GROUPS.index("prox_lad")] = 52.0, 0.6  # flagged, shown 71%
    severity[GROUPS.index("pda")], prob[GROUPS.index("pda")] = 6.0, 0.3  # flagged with a low estimate
    severity[GROUPS.index("mid_rca")] = 30.0  # not flagged, shown 43% (v15) or 40% (v13)
    res = {"encoder_passes": 2, "severity": severity, "binary_probability": prob}
    return CustomPredictionService.postprocess(GROUPS, res, n_videos=2, thresholds=THRESHOLDS, model_version=model_version,
                                               unsupported=("lvp",) if model_version == "v15" else ())


def test_json_keeps_the_estimate_and_flags_and_adds_display_fields():
    preds = _preds()
    lad, pda, d1 = preds["segments"]["prox_lad"], preds["segments"]["pda"], preds["segments"]["D1"]
    assert (lad["stenosis_pct"], lad["significant"], lad["display_pct"], lad["display_category"]) == (52.0, True, 71, "severe")
    assert (pda["stenosis_pct"], pda["significant"], pda["display_pct"], pda["display_category"]) == (6.0, True, 20, "mild")
    assert (d1["stenosis_pct"], d1["significant"], d1["display_pct"], d1["display_category"]) == (5.0, False, 0, "normal")
    assert preds["model_version"] == "v15" and preds["segments"]["mid_rca"]["display_pct"] == 43
    assert predictions_of({"predictions": preds}) is not None
    older = _preds("v13")["segments"]  # the same outputs from v13 weights get the v13 mapping
    assert (older["prox_lad"]["display_pct"], older["mid_rca"]["display_pct"], older["pda"]["display_pct"]) == (71, 40, 20)


def test_report_and_diagnosis_show_the_displayed_value():
    preds = _preds()
    service = CustomPredictionService.__new__(CustomPredictionService)
    text, html = service.diagnosis_text(preds), service.render_html(preds)
    assert "Proximal LAD (P 0.600, shown 71%, model estimate 52%)" in text
    assert "PDA (P 0.300, shown 20%, model estimate 6%)" in text
    assert "Other segments with a shown stenosis of 30% or more: Mid RCA (43%)." in text
    assert "14 of 17 segments shown as normal (model estimate below 9.5%)." in text
    assert "Prox LAD 71%" in html and "Mid RCA 43%" in html and "D1 normal" in html and "PDA 20%" in html
    row = re.search(r'<li class="seg"><span class="sname">First diagonal(.*?)</li>', html).group(1)
    assert '<span class="normal">0%</span>' in row and "model estimate 5.0%" in row
    assert "Proximal LAD (71%)" in html  # summary strip
    assert "Estimates below 9.5% are shown as 0% (normal)" in html and "9.5→20, 21.5→30, 34.5→50, 49.5→70, 100→100" in html
    assert "validation split of model v15" in html and "validation split of model v13" not in html
    assert CustomPredictionService.report_model_version(preds) == "v15-cal1" and "DeepCORO-JEPA v15-cal1</h1>" in html


def test_report_version_names_the_model_and_calibration_of_the_result():
    assert CustomPredictionService.report_model_version() == f"{CustomPredictionService.model_version()}-cal1" == "v19-cal1"
    assert CustomPredictionService.report_model_version({"model_version": "v15", "segments": {}}) == "v15-cal1"
    assert CustomPredictionService.report_model_version({"segments": {}}) == "v13-cal1"  # stored before 2.0.0


def _stored_v13(display_fields: bool) -> dict:
    """A result as 1.4.x (with display fields) or 1.2.0 / 1.3.0 (without) stored it: model v13, no model_version."""
    stored = json.loads(json.dumps(_preds("v13")))
    assert stored.pop("model_version") == "v13" and "unsupported_segments" not in stored
    if not display_fields:
        for v in stored["segments"].values():
            del v["display_pct"], v["display_category"]
    return stored


@pytest.mark.parametrize("display_fields", [True, False])
def test_a_stored_v13_result_is_never_given_the_v15_mapping(display_fields):
    stored = _stored_v13(display_fields)
    html = CustomPredictionService.report_html(predictions_of({"predictions": stored}))
    # Mid RCA, estimate 30%: 40% with the v13 mapping, 43% with the v15 one
    assert "Mid RCA 40%" in html and "Mid RCA 43%" not in html and "Prox LAD 71%" in html and "PDA 20%" in html
    assert "DeepCORO-JEPA v13-cal1</h1>" in html and "v15-cal1" not in html
    assert "Estimates below 10% are shown as 0% (normal)" in html and "10→20, 23→30, 37→50, 51→70, 100→100" in html
    assert "validation split of model v13" in html and "below 9.5%" not in html
    assert "Test AUROC about 0.69" not in html  # no physiology in this result
    assert "<dd>0.885 (DeepCORO-CLIP 0.850)</dd>" in html and "<dd>0.877" not in html  # v13 performance figures
    assert "test AUROC about 0.69" in html  # the v13 recommendation
    text = CustomPredictionService.__new__(CustomPredictionService).diagnosis_text(stored)
    assert "Mid RCA (40%)" in text and "model estimate below 10%" in text


def test_stored_v13_results_render_the_same_with_or_without_display_fields():
    with_fields, without = _stored_v13(True), _stored_v13(False)
    assert (CustomPredictionService.report_html(predictions_of({"predictions": without}))
            == CustomPredictionService.report_html(predictions_of({"predictions": with_fields})))


def test_stored_display_values_are_shown_as_stored_not_recomputed():
    """A v13 estimate of 22.0% was stored as 29% (mild); the v15 mapping would give 30% (moderate)."""
    stored = _stored_v13(True)
    stored["segments"]["om1"].update(stenosis_pct=22.0, display_pct=29, display_category="mild")
    html = CustomPredictionService.report_html(predictions_of({"predictions": stored}))
    assert "OM1 29%" in html and "OM1 30%" not in html
    # and a v15 result keeps its own stored values
    new = json.loads(json.dumps(_preds()))
    html = CustomPredictionService.report_html(predictions_of({"predictions": new}))
    assert "Mid RCA 43%" in html and "DeepCORO-JEPA v15-cal1</h1>" in html and "<dd>0.877" in html


def test_a_new_v15_result_renders_with_the_v15_mapping_even_without_display_fields():
    new = json.loads(json.dumps(_preds()))
    assert new["model_version"] == "v15"
    full = CustomPredictionService.report_html(predictions_of({"predictions": new}))
    for v in new["segments"].values():
        v.pop("display_pct", None), v.pop("display_category", None)
    assert CustomPredictionService.report_html(predictions_of({"predictions": new})) == full
    assert "Mid RCA 43%" in full and "validation split of model v15" in full and "v13-cal1" not in full


def test_stored_results_are_checked():
    preds = _preds()
    for change, where in ((lambda p: p["segments"]["pda"].update(display_category="huge"), "display_category"),
                          (lambda p: p["segments"]["pda"].update(display_category="severe"), "does not match display_pct"),
                          (lambda p: p["segments"]["pda"].update(display_pct=20.5), "display_pct"),
                          (lambda p: p.update(model_version="v16"), "predictions.model_version"),
                          (lambda p: p.update(model_version=15), "predictions.model_version"),
                          (lambda p: p.update(unsupported_segments=["nope"]), "unsupported_segments"),
                          (lambda p: p["segments"]["lvp"].update(unsupported="yes"), "unsupported")):
        bad = copy.deepcopy(preds)
        change(bad)
        with pytest.raises(ResultError, match=where):
            predictions_of({"predictions": bad})
