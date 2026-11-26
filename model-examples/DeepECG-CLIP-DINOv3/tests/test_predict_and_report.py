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
from logic import CustomPredictionService, HeadSpec, head_registry  # noqa: E402
from models.probes import discover_probes  # noqa: E402
from report_input import NO_ECG_HTML, NO_ECG_RESULT  # noqa: E402

FIELDS = {"id", "head", "label", "description", "task", "kind", "probability", "threshold", "positive",
          "applicable", "applicableWhen"}
ACO_T = 0.05062838


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
    """A loaded service with the fine-tuned aco head and three synthetic probe heads."""
    pf.standard_heads(tmp_path)
    probes = discover_probes(tmp_path, pf.FROZEN_SHA256)
    fake = FakeClassifiers({"aco": 0.0})
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
    served.logits.update(aco=_logit(0.4))
    body = client.post("/inference/predict", json=_waveform_request("JSON")).json()
    assert body["success"], body
    data = body["data"]
    heads = data["predictions"]["heads"]
    ids = [h["id"] for h in heads]
    assert ids == ["aco", *pf.PATTERNS, "LVEF<=40", "SHD"]
    assert all(set(h) == FIELDS for h in heads)
    aco = heads[0]
    assert aco["probability"] == 0.4 and aco["threshold"] == ACO_T and aco["positive"] is True
    assert aco["applicable"] is True and aco["applicableWhen"] is None
    patterns = [h for h in heads if h["head"] == "ecg77_clean"]
    assert len(patterns) == 77
    assert all(h["threshold"] is None and h["positive"] is None and h["task"] == "multilabel" for h in patterns)
    assert next(h for h in heads if h["id"] == "SHD")["kind"] == "linear-probe"
    assert data["predictions"]["input"]["source"] == "waveform"
    assert "occlusion suspected" in data["diagnosis"]
    assert not any(word in json.dumps(data) for word in ("acco", "ACCO", "AICO"))
    assert served.seen.shape == (1, 3, 448, 896)   # the page went through the training preprocessing


def test_acco_vs_aico_is_not_served(client, served):
    """Removed in 1.4.0 (owner's decision): never returned, and asking for it is an unknown head."""
    served.logits.update(aco=_logit(0.6))
    for mode in ("JSON", "HTML"):
        data = client.post("/inference/predict", json=_waveform_request(mode)).json()["data"]
        text = json.dumps(data) if mode == "JSON" else base64.b64decode(data["htmlBase64"]).decode()
        assert not any(word in text for word in ("acco", "ACCO", "AICO"))
    bad = client.post("/inference/predict", json=_waveform_request("JSON", additionalMetadata={"heads": ["acco_vs_aico"]}))
    assert bad.status_code == 500 and "Unknown head(s): acco_vs_aico" in bad.json()["message"]
    assert "acco_vs_aico" not in {h.id for h in CustomPredictionService.registry()}


def test_a_gated_output_is_not_interpreted_when_its_parent_is_negative(client, served, monkeypatch):
    """applicableWhen stays supported for heads of class_mapping.json (none is gated now)."""
    gated = HeadSpec("SHD", "shd_clean", "SHD", "", "binary", "linear-probe", 0.1, applies_when="aco")
    registry = [gated if s.id == "SHD" else s for s in CustomPredictionService.registry()]
    monkeypatch.setattr(CustomPredictionService, "_registry", registry, raising=False)
    served.logits.update(aco=_logit(0.01))
    heads = client.post("/inference/predict", json=_waveform_request("JSON")).json()["data"]["predictions"]["heads"]
    shd = next(h for h in heads if h["id"] == "SHD")
    assert shd["applicable"] is False and shd["positive"] is None and shd["applicableWhen"] == "aco"
    request = _waveform_request("JSON", additionalMetadata={"heads": ["SHD"]})
    heads = client.post("/inference/predict", json=request).json()["data"]["predictions"]["heads"]
    assert [h["id"] for h in heads] == ["aco", "SHD"]


def test_heads_filter_returns_only_the_named_heads_and_their_parents(client, served):
    request = _waveform_request("JSON", additionalMetadata={"heads": ["SHD", "ecg77_clean"]})
    heads = client.post("/inference/predict", json=request).json()["data"]["predictions"]["heads"]
    assert [h["id"] for h in heads] == [*pf.PATTERNS, "SHD"]
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


@pytest.mark.parametrize("aco", [0.4, 0.01, ACO_T, ACO_T - 1e-9, 0.050649, 0.99999])
def test_render_report_equals_html_mode_predict(client, served, aco):
    served.logits.update(aco=_logit(aco))
    for extra in ({}, {"additionalMetadata": {"heads": ["aco", "LVEF<=40", "ecg77_clean"]}}):
        stored = client.post("/inference/predict", json=_waveform_request("JSON", **extra)).json()["data"]
        html = client.post("/inference/predict", json=_waveform_request("HTML", **extra)).json()["data"]["htmlBase64"]
        rendered = client.post("/inference/render-report", json={"result": json.loads(json.dumps(stored))})
        assert rendered.status_code == 200, rendered.json()
        assert rendered.json()["data"]["htmlBase64"] == html


def test_random_outputs_render_the_live_html(client, served):
    rng = random.Random(0)
    for _ in range(20):
        served.logits.update(aco=rng.uniform(-6, 6))
        stored = client.post("/inference/predict", json=_waveform_request("JSON")).json()["data"]
        html = client.post("/inference/predict", json=_waveform_request("HTML")).json()["data"]["htmlBase64"]
        assert client.post("/inference/render-report", json={"result": stored}).json()["data"]["htmlBase64"] == html


@pytest.mark.parametrize("mutate", [
    lambda r: r["predictions"]["heads"][0].update(positive=not r["predictions"]["heads"][0]["positive"]),
    lambda r: r["predictions"]["heads"][0].update(probability=0.1234567),
    lambda r: r["predictions"]["heads"][0].update(label="<script>alert(1)</script>"),
    lambda r: r["predictions"]["heads"].reverse(),
    lambda r: r["predictions"]["heads"].append(dict(r["predictions"]["heads"][0])),
    lambda r: r["predictions"]["input"].update(source="xml"),
    lambda r: r.update(diagnosis="edited"),
    lambda r: r["modelRecommendations"].update(en="<b>edited</b>"),
    lambda r: r["predictions"]["heads"][1].update(id="unknown"),
])
def test_tampered_results_are_refused(client, served, mutate):
    served.logits.update(aco=_logit(0.4))
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
    assert [h.id for h in heads] == ["aco"]
    preds = {"aco": 0.3}
    stored = json.loads(json.dumps(service._json_result(preds, "pdf", heads)))
    response = client.post("/inference/render-report", json={"result": stored})
    assert response.status_code == 200
    assert base64.b64decode(response.json()["data"]["htmlBase64"]).decode() == service._render_html(preds, "pdf", heads)


def test_model_info_advertises_render_report(client):
    data = client.get("/inference/model-info").json()["data"]
    assert data["capabilities"] == {"renderReport": True}
    assert data["modelId"] == "DeepECG_CLIP_DINOv3"


HCM_T = 0.0027414804009191847   # hcm_clean cutoff (model revisions badd718 and 7d35c2e)


@pytest.mark.parametrize("p,positive", [(0.0027420, True), (0.0027449, True), (0.0027410, False),
                                         (0.0026, False), (0.0031, True)])
def test_low_cutoff_flag_follows_the_unrounded_rule(monkeypatch, p, positive):
    """1.1.0 rounded to 4 decimals before the flag, so HCM was effectively p >= 0.00275 and
    p in [0.0027415, 0.00275) came out negative. At 6 decimals the flag is p >= cutoff."""
    monkeypatch.setattr(CustomPredictionService, "probes", [])
    service = CustomPredictionService()
    spec = HeadSpec("hcm", "hcm", "HCM", "", "binary", "linear-probe", HCM_T)
    preds = service._postprocess({"hcm": torch.tensor([[_logit(p)]])})
    assert preds["hcm"] == round(p, 6)
    assert service._positive(spec, preds) is positive is (p >= HCM_T)


def test_low_cutoff_is_shown_with_enough_decimals(monkeypatch):
    monkeypatch.setattr(CustomPredictionService, "probes", [])
    service = CustomPredictionService()
    spec = HeadSpec("hcm", "hcm", "HCM", "", "binary", "linear-probe", HCM_T)
    preds = {"hcm": 0.002745}
    assert "HCM (0.002745)" in service._diagnosis_text(preds, "waveform", [spec])
    html = service._render_html(preds, "waveform", [spec])
    assert "<td>0.002745</td><td>0.0027415</td>" in html and "flag-pos" in html
    # an ordinary cutoff keeps the 3-decimal probability and 4-decimal cutoff
    shd = HeadSpec("SHD", "shd", "SHD", "", "binary", "linear-probe", 0.537370502948761)
    assert "<td>0.612345</td>" not in service._render_html({"SHD": 0.612345}, "waveform", [shd])
    assert "<td>0.612</td><td>0.5374</td>" in service._render_html({"SHD": 0.612345}, "waveform", [shd])


def test_stored_result_at_six_decimals_renders_again(client, served):
    served.logits.update(aco=_logit(0.0012345))
    stored = client.post("/inference/predict", json=_waveform_request("JSON")).json()["data"]
    assert stored["predictions"]["heads"][0]["probability"] == round(0.0012345, 6)
    html = client.post("/inference/predict", json=_waveform_request("HTML")).json()["data"]["htmlBase64"]
    assert client.post("/inference/render-report", json={"result": stored}).json()["data"]["htmlBase64"] == html
