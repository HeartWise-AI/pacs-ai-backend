"""/inference/predict end to end without weights, and /inference/render-report of its stored results."""
from __future__ import annotations

import base64
import copy
import json
import os
import sys
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(Path(__file__).resolve().parent))

import ecg_fixtures as fx  # noqa: E402
import logic  # noqa: E402
from logic import CustomPredictionService  # noqa: E402
from report_input import NO_ECG_HTML, NO_ECG_RESULT  # noqa: E402

FIELDS = {"id", "label", "probability", "threshold", "positive", "applicable"}
PATTERNS = ["Sinusal", "Afib", "Left bundle branch block"]   # a 3-label stand-in for the 77 findings
BINARY = {"shd": ("SHD", 0.537370502948761), "lvef_le40": ("LVEF<=40", 0.16951389610767365),
          "hcm": ("confirmed HCM (vs rest)", 0.0027414804009191847), "aco": ("ACO", 0.046955234134749495)}


@pytest.fixture(scope="module")
def client():
    cwd = os.getcwd()
    os.chdir(ROOT)
    try:
        import main
    finally:
        os.chdir(cwd)
    return TestClient(main.app)


def write_heads(folder: Path, heads: list[str], variant: str = "clean") -> None:
    """models/config.json and the head.json / thresholds.json of the enabled heads (no weights)."""
    (folder / "config.json").write_text(json.dumps({"variant": variant, "heads": heads,
                                                    "disabled_heads": ["hcm_3class", "acco_vs_aico"]}))
    for task in heads:
        head = folder / "probes" / f"{task}_{variant}"
        head.mkdir(parents=True)
        if task == "ecg77":
            outputs, thresholds = PATTERNS, {}
        else:
            outputs, thresholds = [BINARY[task][0]], {BINARY[task][0]: BINARY[task][1]}
        (head / "head.json").write_text(json.dumps({"task": f"{task} task", "outputs": outputs}))
        (head / "thresholds.json").write_text(json.dumps({"thresholds": thresholds}))


class FakePredictor:
    """Stands in for DeepECG_DINOv3_Predictor: fixed probabilities, every enabled output."""

    def __init__(self, probabilities: dict[str, float]):
        self.probabilities = probabilities
        self.pages = []

    def predict_image(self, page):
        self.pages.append(page)
        return {"variant": "clean", "heads": [{"id": k, "probability": v} for k, v in self.probabilities.items()]}


@pytest.fixture
def served(monkeypatch, tmp_path):
    write_heads(tmp_path, ["ecg77", "shd", "lvef_le40", "hcm", "aco"])
    monkeypatch.setattr(logic, "MODEL_DIRECTORY", str(tmp_path))
    fake = FakePredictor({"shd": 0.6, "lvef_le40": 0.1, "hcm": 0.001, "aco": 0.4,
                          **{f"ecg77:{p}": 0.1 * (i + 1) for i, p in enumerate(PATTERNS)}})
    monkeypatch.setattr(CustomPredictionService, "is_initialized", True)
    monkeypatch.setattr(CustomPredictionService, "load_model", lambda self, config: None)
    monkeypatch.delattr(CustomPredictionService, "_registry", raising=False)
    monkeypatch.setitem(CustomPredictionService.models, "predictor", fake)
    yield fake
    if hasattr(CustomPredictionService, "_registry"):
        del CustomPredictionService._registry


def _waveform_request(mode: str, **extra) -> dict:
    ds = fx.waveform_dicom(fx.synthetic_counts())
    b64 = base64.b64encode(fx.dicom_bytes(ds)).decode()
    return {"outputMode": mode, "seriesInstanceImages": {"1": {"1": b64}}, **extra}


def test_one_ecg_returns_every_enabled_output(client, served):
    data = client.post("/inference/predict", json=_waveform_request("JSON")).json()["data"]
    heads = data["predictions"]["heads"]
    assert [h["id"] for h in heads] == ["shd", "lvef_le40", "hcm", "aco"] + [f"ecg77:{p}" for p in PATTERNS]
    assert all(set(h) == FIELDS for h in heads)
    rows = {h["id"]: h for h in heads}
    assert rows["shd"] == {"id": "shd", "label": "shd task", "probability": 0.6, "threshold": BINARY["shd"][1],
                           "positive": True, "applicable": True}
    assert rows["aco"]["positive"] is True and rows["lvef_le40"]["positive"] is False
    assert all(rows[f"ecg77:{p}"]["threshold"] is None and rows[f"ecg77:{p}"]["positive"] is None for p in PATTERNS)
    assert data["predictions"]["input"]["source"] == "waveform"
    assert data["predictions"]["model"] == {"encoder": "DINOv3-SigLIP2 zo59vdxk epoch 30", "probes": "clean"}
    assert "occlusion suspected" in data["diagnosis"]
    assert served.pages[0].size == (1250, 625)     # the waveform was drawn as a training page


def test_heads_filter_and_disabled_heads(client, served):
    request = _waveform_request("JSON", additionalMetadata={"heads": ["aco", "ecg77"]})
    heads = client.post("/inference/predict", json=request).json()["data"]["predictions"]["heads"]
    assert [h["id"] for h in heads] == ["aco"] + [f"ecg77:{p}" for p in PATTERNS]
    for name in ("acco_vs_aico", "hcm_3class", "nope"):
        bad = client.post("/inference/predict", json=_waveform_request("JSON", additionalMetadata={"heads": [name]}))
        assert bad.status_code == 500 and "Unknown or disabled head" in bad.json()["message"]


def test_no_readable_ecg_gives_the_no_ecg_answer(client, served):
    ds = fx.image_dicom(__import__("numpy").zeros((4, 4), dtype="uint8"))
    ds.NumberOfFrames = 2   # a cine object is not an ECG page
    request = {"outputMode": "JSON", "seriesInstanceImages": {"1": {"1": base64.b64encode(fx.dicom_bytes(ds)).decode()}}}
    data = client.post("/inference/predict", json=request).json()["data"]
    assert data == NO_ECG_RESULT
    html = client.post("/inference/predict", json={**request, "outputMode": "HTML"}).json()["data"]["htmlBase64"]
    assert base64.b64decode(html).decode() == NO_ECG_HTML
    assert client.post("/inference/render-report", json={"result": data}).status_code == 200


@pytest.mark.parametrize("aco,shd", [(0.4, 0.6), (0.01, 0.2), (0.0470, 0.5374), (0.0469, 0.5373), (0.9999, 0.0001)])
def test_render_report_equals_html_mode_predict(client, served, aco, shd):
    served.probabilities.update(aco=aco, shd=shd)
    for extra in ({}, {"additionalMetadata": {"heads": ["aco", "ecg77"]}}):
        stored = client.post("/inference/predict", json=_waveform_request("JSON", **extra)).json()["data"]
        html = client.post("/inference/predict", json=_waveform_request("HTML", **extra)).json()["data"]["htmlBase64"]
        rendered = client.post("/inference/render-report", json={"result": json.loads(json.dumps(stored))})
        assert rendered.status_code == 200, rendered.json()
        assert rendered.json()["data"]["htmlBase64"] == html


def test_raw_probabilities_are_rounded_before_flags(client, served):
    served.probabilities.update(shd=0.53736)          # rounds to 0.5374 >= 0.537370...: positive
    rows = {h["id"]: h for h in client.post("/inference/predict", json=_waveform_request("JSON")).json()
            ["data"]["predictions"]["heads"]}
    assert rows["shd"]["probability"] == 0.5374 and rows["shd"]["positive"] is True


@pytest.mark.parametrize("mutate", [
    lambda r: r["predictions"]["heads"][0].update(positive=not r["predictions"]["heads"][0]["positive"]),
    lambda r: r["predictions"]["heads"][0].update(probability=0.12345),
    lambda r: r["predictions"]["heads"][0].update(label="<script>alert(1)</script>"),
    lambda r: r["predictions"]["heads"].reverse(),
    lambda r: r["predictions"]["heads"].append(dict(r["predictions"]["heads"][0])),
    lambda r: r["predictions"]["input"].update(source="xml"),
    lambda r: r["predictions"]["model"].update(probes="aug"),
    lambda r: r.update(diagnosis="edited"),
    lambda r: r["modelRecommendations"].update(en="<b>edited</b>"),
    lambda r: r["predictions"]["heads"][1].update(id="unknown"),
])
def test_tampered_results_are_refused(client, served, mutate):
    stored = client.post("/inference/predict", json=_waveform_request("JSON")).json()["data"]
    tampered = copy.deepcopy(stored)
    mutate(tampered)
    assert client.post("/inference/render-report", json={"result": tampered}).status_code == 422


def test_render_report_does_not_load_the_model(client, served, monkeypatch):
    stored = client.post("/inference/predict", json=_waveform_request("JSON")).json()["data"]
    monkeypatch.setattr(CustomPredictionService, "is_initialized", False)
    monkeypatch.setattr(CustomPredictionService, "models", {})
    monkeypatch.delattr(CustomPredictionService, "_registry", raising=False)
    response = client.post("/inference/render-report", json={"result": stored})
    assert response.status_code == 200


def test_model_info_advertises_render_report(client):
    data = client.get("/inference/model-info").json()["data"]
    assert data["capabilities"] == {"renderReport": True}
    assert data["modelId"] == "DeepECG_CLIP_DINOv3"
