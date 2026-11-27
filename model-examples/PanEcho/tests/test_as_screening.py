"""1.1.2: aortic stenosis is a screening score, not a diagnosis.

Synthetic head outputs only (no PHI). Loads logic.py without the model weights.
"""

import json
import os
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

logic = pytest.importorskip("logic")
torch = pytest.importorskip("torch")
from utils.html_parser import HTMLParser  # noqa: E402


@pytest.fixture(autouse=True)
def _model_dir(monkeypatch):
    monkeypatch.chdir(ROOT)


def service():
    return logic.CustomPredictionService.__new__(logic.CustomPredictionService)


def heads(p_mild_moderate=0.37, p_none=0.43, p_severe=0.20, vmax=2.40, lvef=55.0):
    return {
        "AVStenosis": torch.tensor([[p_mild_moderate, p_none, p_severe]]),
        "AVPkVel(m|s)": torch.tensor([[vmax]]),
        "EF": torch.tensor([[lvef]]),
        "AVRegurg": torch.tensor([[0.1, 0.1, 0.8]]),
    }


def test_stenosis_shows_screening_scores_instead_of_the_argmax_class():
    diagnosis, predictions = service().postprocess_predictions(heads())

    assert diagnosis["Aortic valve stenosis"] == (
        "Screening score, not a diagnosis: P(severe AS) 0.20, P(any AS) 0.57"
    )
    # The raw head output (class order Mild|Moderate, None, Severe) is unchanged.
    assert predictions["Aortic valve stenosis"] == pytest.approx([0.37, 0.43, 0.20])


def test_peak_velocity_is_an_ai_estimate_without_a_normal_category():
    diagnosis, predictions = service().postprocess_predictions(heads(vmax=2.13))

    assert diagnosis["Aortic valve peak velocity"] == "2.1 m/s (AI estimate, under-estimates severe AS)"
    assert "Normal" not in diagnosis["Aortic valve peak velocity"]
    assert predictions["Aortic valve peak velocity"] == pytest.approx(2.13)


def test_other_heads_are_unchanged():
    diagnosis, _ = service().postprocess_predictions(heads())

    assert diagnosis["Aortic valve regurgitation"] == "None|Trace"
    assert diagnosis["Left ventricular (LV) ejection fraction"].startswith("55.0")


def test_screening_object_has_p_severe_and_p_any():
    _, predictions = service().postprocess_predictions(heads())

    screening = logic.aortic_stenosis_screening(predictions)

    assert screening["pSevere"] == pytest.approx(0.20, abs=1e-4)
    assert screening["pAny"] == pytest.approx(0.57, abs=1e-4)
    assert screening["kind"] == "screening"
    assert screening["avVmaxEstimate"] == {"value": 2.4, "unit": "m/s", "label": "AI estimate, under-estimates severe AS"}
    assert "not a diagnosis" in screening["note"]["en"]
    assert "pas un diagnostic" in screening["note"]["fr"]
    assert logic.aortic_stenosis_screening({}) is None


def test_screening_never_drives_the_recommendation():
    diagnosis, _ = service().postprocess_predictions(heads(p_mild_moderate=0.1, p_none=0.1, p_severe=0.8))

    assert "aortic stenosis" not in service()._get_recommendations(diagnosis, "en")
    assert "Normal echocardiogram" in service()._get_recommendations(diagnosis, "en")


def test_html_report_shows_screening_section_outside_the_findings():
    diagnosis, predictions = service().postprocess_predictions(heads())
    html = HTMLParser.generate_detection_results({
        "diagnosis": json.dumps(diagnosis),
        "asScreening": logic.aortic_stenosis_screening(predictions),
        "probability": predictions,
        "recommendations": {"en": "x", "fr": "y"},
    })

    assert "Aortic stenosis screening (AI)" in html
    assert "P(severe AS)" in html and "0.20" in html
    assert "P(any AS)" in html and "0.57" in html
    assert "AI estimate, under-estimates severe AS" in html
    # Not counted as an urgent/abnormal finding and not shown as a diagnosis card.
    assert "Urgent Abnormalities Detected" not in html
    assert ">Aortic valve stenosis<" not in html


def test_model_facts_and_manifest_are_1_1_2():
    facts = json.loads((ROOT / "data/model_facts.json").read_text(encoding="utf-8"))
    info = json.loads((ROOT / "data/model_info.json").read_text(encoding="utf-8"))

    assert info["version"] == "1.1.2"
    for language in ("en", "fr"):
        assert facts[language]["Summary"]["Version"] == "1.1.2"
        assert "1.1.2" in facts[language]["Changelogs"]
    assert "screening" in facts["en"]["Warnings_and_limitations"]["Risks"]
