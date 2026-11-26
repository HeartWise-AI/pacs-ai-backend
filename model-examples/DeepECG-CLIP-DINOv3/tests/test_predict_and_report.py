"""/inference/predict end to end without weights, and /inference/render-report of its stored results."""
from __future__ import annotations

import base64
import copy
import json
import math
import os
import random
import sys
from pathlib import Path

import pytest
import torch
from fastapi.testclient import TestClient

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(Path(__file__).resolve().parent))

import ecg_fixtures as fx  # noqa: E402
import probe_fixtures as pf  # noqa: E402
from logic import CustomPredictionService, head_registry  # noqa: E402
from models.probes import discover_probes  # noqa: E402
from report_input import NO_ECG_HTML, NO_ECG_RESULT  # noqa: E402

FIELDS = {"id", "head", "label", "description", "task", "kind", "probability", "threshold", "positive",
          "applicable", "applicableWhen"}
ACO_T, ACCO_T = 0.05062838, 0.6155858


@pytest.fixture(scope="module")
def client():
    cwd = os.getcwd()
    os.chdir(ROOT)
    try:
        import main
    finally:
        os.chdir(cwd)
    return TestClient(main.app)


class FakeClassifiers(torch.nn.Module):
    """Stands in for SharedTrunkClassifiers: fixed logits, features derived from the input."""

    def __init__(self, logits: dict[str, float]):
        super().__init__()
        self.anchor = torch.nn.Parameter(torch.zeros(1))
        self.logits = logits
        self.seen = None

    def forward(self, x, frozen_features=False):
        self.seen = x
        logits = {k: torch.tensor([[v]]) for k, v in self.logits.items()}
        return logits, (x.mean(dim=(2, 3)).repeat(1, 512) if frozen_features else None)


@pytest.fixture
def served(monkeypatch, tmp_path):
    """A loaded service with the two fine-tuned heads and three synthetic probe heads."""
    pf.standard_heads(tmp_path)
    probes = discover_probes(tmp_path, pf.ENCODER_CONFIG, pf.FROZEN_SHA256)
    fake = FakeClassifiers({"aco": 0.0, "acco_vs_aico": 0.0})
    monkeypatch.setattr(CustomPredictionService, "is_initialized", True)
    monkeypatch.setattr(CustomPredictionService, "load_model", lambda self, config: None)
    monkeypatch.setattr(CustomPredictionService, "probes", probes)
    monkeypatch.setattr(CustomPredictionService, "_registry", head_registry(probes), raising=False)
    monkeypatch.setattr(CustomPredictionService, "preprocessing", pf.ENCODER_CONFIG["preprocessing"], raising=False)
    monkeypatch.setitem(CustomPredictionService.models, "classifiers", fake)
    return fake


def _logit(p: float) -> float:
    return math.log(p / (1 - p))


def _waveform_request(mode: str, **extra) -> dict:
    ds = fx.waveform_dicom(fx.synthetic_counts())
    b64 = base64.b64encode(fx.dicom_bytes(ds)).decode()
    return {"outputMode": mode, "seriesInstanceImages": {"1": {"1": b64}}, **extra}


def test_one_ecg_returns_every_head_self_described(client, served):
    served.logits.update(aco=_logit(0.4), acco_vs_aico=_logit(0.7))
    body = client.post("/inference/predict", json=_waveform_request("JSON")).json()
    assert body["success"], body
    data = body["data"]
    heads = data["predictions"]["heads"]
    ids = [h["id"] for h in heads]
    assert ids == ["aco", "acco_vs_aico", "LVEF_le40", "Sinusal", "Afib", "Left bundle branch block", "SHD"]
    assert all(set(h) == FIELDS for h in heads)
    aco, acco = heads[0], heads[1]
    assert aco["probability"] == 0.4 and aco["threshold"] == ACO_T and aco["positive"] is True
    assert acco["applicable"] is True and acco["positive"] is True and acco["applicableWhen"] == "aco"
    patterns = [h for h in heads if h["head"] == "patterns"]
    assert all(h["threshold"] is None and h["positive"] is None and h["task"] == "multilabel" for h in patterns)
    assert next(h for h in heads if h["id"] == "SHD")["kind"] == "linear-probe"
    assert data["predictions"]["input"]["source"] == "waveform"
    assert "occlusion suspected" in data["diagnosis"]
    assert served.seen.shape == (1, 3, 448, 896)   # the page went through the training preprocessing


def test_acco_vs_aico_is_not_interpreted_when_aco_is_negative(client, served):
    served.logits.update(aco=_logit(0.01), acco_vs_aico=_logit(0.9))
    heads = client.post("/inference/predict", json=_waveform_request("JSON")).json()["data"]["predictions"]["heads"]
    acco = next(h for h in heads if h["id"] == "acco_vs_aico")
    assert acco["applicable"] is False and acco["positive"] is None and acco["probability"] == 0.9


def test_heads_filter_returns_only_the_named_heads_and_their_parents(client, served):
    request = _waveform_request("JSON", additionalMetadata={"heads": ["acco_vs_aico", "patterns"]})
    heads = client.post("/inference/predict", json=request).json()["data"]["predictions"]["heads"]
    assert [h["id"] for h in heads] == ["aco", "acco_vs_aico", "Sinusal", "Afib", "Left bundle branch block"]
    bad = client.post("/inference/predict", json=_waveform_request("JSON", additionalMetadata={"heads": ["nope"]}))
    assert bad.status_code == 500 and "Unknown head" in bad.json()["message"]


def test_no_readable_ecg_gives_the_no_ecg_answer(client, served):
    ds = fx.image_dicom(__import__("numpy").zeros((4, 4, 4), dtype="uint8")[..., 0])
    ds.NumberOfFrames = 2   # a cine object is not an ECG page
    request = {"outputMode": "JSON", "seriesInstanceImages": {"1": {"1": base64.b64encode(fx.dicom_bytes(ds)).decode()}}}
    data = client.post("/inference/predict", json=request).json()["data"]
    assert data == NO_ECG_RESULT
    html = client.post("/inference/predict", json={**request, "outputMode": "HTML"}).json()["data"]["htmlBase64"]
    assert base64.b64decode(html).decode() == NO_ECG_HTML
    assert client.post("/inference/render-report", json={"result": data}).status_code == 200


@pytest.mark.parametrize("aco,acco", [(0.4, 0.7), (0.01, 0.9), (ACO_T, ACCO_T), (ACO_T - 1e-9, 0.5),
                                      (0.050649, 0.61555), (0.99999, 0.00001)])
def test_render_report_equals_html_mode_predict(client, served, aco, acco):
    served.logits.update(aco=_logit(aco), acco_vs_aico=_logit(acco))
    for extra in ({}, {"additionalMetadata": {"heads": ["aco", "LVEF_le40", "patterns"]}}):
        stored = client.post("/inference/predict", json=_waveform_request("JSON", **extra)).json()["data"]
        html = client.post("/inference/predict", json=_waveform_request("HTML", **extra)).json()["data"]["htmlBase64"]
        rendered = client.post("/inference/render-report", json={"result": json.loads(json.dumps(stored))})
        assert rendered.status_code == 200, rendered.json()
        assert rendered.json()["data"]["htmlBase64"] == html


def test_random_outputs_render_the_live_html(client, served):
    rng = random.Random(0)
    for _ in range(20):
        served.logits.update(aco=rng.uniform(-6, 6), acco_vs_aico=rng.uniform(-3, 3))
        stored = client.post("/inference/predict", json=_waveform_request("JSON")).json()["data"]
        html = client.post("/inference/predict", json=_waveform_request("HTML")).json()["data"]["htmlBase64"]
        assert client.post("/inference/render-report", json={"result": stored}).json()["data"]["htmlBase64"] == html


@pytest.mark.parametrize("mutate", [
    lambda r: r["predictions"]["heads"][0].update(positive=not r["predictions"]["heads"][0]["positive"]),
    lambda r: r["predictions"]["heads"][0].update(probability=0.12345),
    lambda r: r["predictions"]["heads"][0].update(label="<script>alert(1)</script>"),
    lambda r: r["predictions"]["heads"].reverse(),
    lambda r: r["predictions"]["heads"].append(dict(r["predictions"]["heads"][0])),
    lambda r: r["predictions"]["input"].update(source="xml"),
    lambda r: r.update(diagnosis="edited"),
    lambda r: r["modelRecommendations"].update(en="<b>edited</b>"),
    lambda r: r["predictions"]["heads"][1].update(id="unknown"),
])
def test_tampered_results_are_refused(client, served, mutate):
    served.logits.update(aco=_logit(0.4), acco_vs_aico=_logit(0.7))
    stored = client.post("/inference/predict", json=_waveform_request("JSON")).json()["data"]
    tampered = copy.deepcopy(stored)
    mutate(tampered)
    assert client.post("/inference/render-report", json={"result": tampered}).status_code == 422


def test_render_report_does_not_load_the_model(client, monkeypatch):
    monkeypatch.setattr(CustomPredictionService, "is_initialized", False)
    monkeypatch.setattr(CustomPredictionService, "load_model",
                        lambda *a: (_ for _ in ()).throw(AssertionError("no model load")))
    monkeypatch.delattr(CustomPredictionService, "_registry", raising=False)
    service = CustomPredictionService()
    heads = CustomPredictionService.registry()   # read from class_mapping.json and the (absent) probe folder
    assert [h.id for h in heads] == ["aco", "acco_vs_aico"]
    preds = {"aco": 0.3, "acco_vs_aico": 0.2}
    stored = json.loads(json.dumps(service._json_result(preds, "pdf", heads)))
    response = client.post("/inference/render-report", json={"result": stored})
    assert response.status_code == 200
    assert base64.b64decode(response.json()["data"]["htmlBase64"]).decode() == service._render_html(preds, "pdf", heads)


def test_model_info_advertises_render_report(client):
    data = client.get("/inference/model-info").json()["data"]
    assert data["capabilities"] == {"renderReport": True}
    assert data["modelId"] == "DeepECG_CLIP_DINOv3"
