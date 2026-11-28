"""MHI AS recalibration (1.1.3): only the AS label and AV Vmax change; every other head is passed through.

Synthetic model outputs only (no PHI). Loads logic.py without the model weights.
"""

import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

torch = pytest.importorskip("torch")
logic = pytest.importorskip("logic")
from utils import as_calibration as asc  # noqa: E402
from utils.html_parser import HTMLParser  # noqa: E402

CAL = {
    "severe_threshold": 0.10,
    "any_as_threshold": 0.30,
    "vmax_map": {"kind": "linear", "slope": 2.0, "intercept": -1.5, "clip": [0.5, 6.0]},
    "scope": "MHI test",
    "version": "test",
}


@pytest.fixture(autouse=True)
def _model_dir(monkeypatch):
    monkeypatch.chdir(ROOT)


def test_label_rule_uses_thresholds_not_argmax():
    # argmax would say None for all of these
    assert asc.calibrated_as_label([0.20, 0.68, 0.12], CAL) == "Significant AS likely"
    assert asc.calibrated_as_label([0.25, 0.70, 0.05], CAL) == "Significant AS likely"
    assert asc.calibrated_as_label([0.10, 0.85, 0.05], CAL) == "No significant AS suspected"
    assert asc.calibrated_as_label([0.20, 0.70, 0.10], CAL) == "Significant AS likely"  # inclusive 0.30


def test_severe_qualifier_only_when_enabled():
    flagged = {**CAL, "severe_flag": True}
    assert asc.calibrated_as_label([0.20, 0.68, 0.12], flagged) == "Significant AS likely, severe possible"
    assert asc.calibrated_as_label([0.25, 0.70, 0.05], flagged) == "Significant AS likely"
    assert asc.calibrated_as_label([0.0, 0.95, 0.05], flagged) == "No significant AS suspected"


def test_vmax_map_is_linear_and_clipped():
    assert asc.corrected_vmax(2.0, CAL) == pytest.approx(2.5)
    assert asc.corrected_vmax(0.1, CAL) == pytest.approx(0.5)
    assert asc.corrected_vmax(5.0, CAL) == pytest.approx(6.0)


def test_shipped_calibration_loads_and_is_sane():
    cal = asc.load_calibration()
    assert cal is not None, "PanEcho 1.1.3 must ship models/as_calibration.json"
    assert 0.0 < cal["severe_threshold"] < 1.0
    assert 0.0 < cal["any_as_threshold"] < 1.0
    assert cal["vmax_map"]["slope"] > 0
    assert cal["scope"].startswith("MHI")
    assert cal["severe_flag"] is False  # primary A label only (Robert)
    assert "not a diagnosis" in cal["note"]["en"] and "pas un diagnostic" in cal["note"]["fr"]
    assert logic._AS_CALIBRATION == cal


def _fake_predictions(p=(0.20, 0.68, 0.12), vmax=2.2):
    mapping = json.loads((ROOT / "models" / "output_class_mapping.json").read_text())
    gen = torch.Generator().manual_seed(0)
    reserved = {"description", "units", "unit", "normal_range", "thresholds", "note", "indexed_normal"}
    preds = {}
    for head, cfg in mapping.items():
        if "regression" in cfg:
            preds[head] = torch.rand(1, 1, generator=gen) * 3
        else:
            classes = [k for k in cfg if k not in reserved]
            if len(classes) == 2:
                preds[head] = torch.rand(1, 1, generator=gen)
            else:
                preds[head] = torch.softmax(torch.randn(1, len(classes), generator=gen), -1)
    preds["AVStenosis"] = torch.tensor([list(p)])
    preds["AVPkVel(m|s)"] = torch.tensor([[vmax]])
    return preds


def _run(monkeypatch, cal, preds):
    monkeypatch.setattr(logic.CustomPredictionService, "_as_calibration_override", cal, raising=False)
    svc = logic.CustomPredictionService.__new__(logic.CustomPredictionService)
    d, p = svc.postprocess_predictions(dict(preds))
    return d, p, logic.aortic_stenosis_screening(p, cal)


def test_only_as_outputs_change(monkeypatch):
    preds = _fake_predictions()
    before_d, before_p, _ = _run(monkeypatch, None, preds)
    after_d, after_p, screening = _run(monkeypatch, CAL, preds)

    as_keys = {"Aortic valve stenosis", "Aortic valve peak velocity"}
    assert len(before_d) == 40
    for key in before_d:
        if key not in as_keys:
            assert after_d[key] == before_d[key], key
    for key in before_p:
        if key not in as_keys:
            assert after_p[key] == before_p[key], key

    assert after_d["Aortic valve stenosis"] == (
        "Significant AS likely (MHI-calibrated screening, not a diagnosis): P(severe AS) 0.12, P(any AS) 0.32")
    assert after_p["Aortic valve stenosis"] == before_p["Aortic valve stenosis"]  # raw softmax kept
    assert after_p["Aortic valve peak velocity (uncorrected)"] == pytest.approx(2.2)
    assert after_p["Aortic valve peak velocity"] == pytest.approx(2.9)
    assert after_d["Aortic valve peak velocity"] == "2.9 m/s (AI estimate, MHI-calibrated; uncorrected 2.2 m/s)"
    assert set(after_d) == set(before_d)
    assert set(after_p) - set(before_p) == {"Aortic valve peak velocity (uncorrected)"}

    assert screening["calibrated"]["label"] == "Significant AS likely"
    assert screening["calibrated"]["severeThreshold"] == 0.10
    assert screening["avVmaxEstimate"] == {"value": 2.9, "unit": "m/s", "label": "AI estimate, MHI-calibrated", "uncorrected": 2.2}
    assert screening["pSevere"] == pytest.approx(0.12)


@pytest.mark.parametrize("p,label", [((0.25, 0.70, 0.05), "Significant AS likely"), ((0.10, 0.85, 0.05), "No significant AS suspected")])
def test_lower_grades(monkeypatch, p, label):
    d, _, s = _run(monkeypatch, CAL, _fake_predictions(p=p))
    assert d["Aortic valve stenosis"].startswith(f"{label} (MHI-calibrated screening")
    assert s["calibrated"]["label"] == label


def test_calibrated_as_never_drives_the_recommendation(monkeypatch):
    d, _, _ = _run(monkeypatch, CAL, _fake_predictions(p=(0.1, 0.1, 0.8)))
    svc = logic.CustomPredictionService.__new__(logic.CustomPredictionService)
    assert "aortic stenosis" not in svc._get_recommendations(d, "en")


def test_html_report_shows_the_calibrated_label(monkeypatch):
    d, p, s = _run(monkeypatch, CAL, _fake_predictions())
    html = HTMLParser.generate_detection_results(
        {"diagnosis": json.dumps(d), "asScreening": s, "probability": p, "recommendations": {"en": "x", "fr": "y"}})
    assert "MHI-calibrated screening label" in html and ">Significant AS likely<" in html
    assert "uncorrected model output" in html and "2.2 m/s" in html
    assert ">Aortic valve stenosis<" not in html  # never a diagnosis card
