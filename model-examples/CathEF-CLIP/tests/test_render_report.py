"""/inference/render-report: the HTML of a stored JSON-mode result, identical to HTML-mode predict, without the model."""
from __future__ import annotations

import base64
import copy
import json
import math
import os
import random
import re
import sys
from pathlib import Path

import pytest
import torch
from fastapi.testclient import TestClient

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from logic import NO_VIDEO_HTML, CustomPredictionService  # noqa: E402
from report_input import NO_VIDEO_RESULT, ResultError  # noqa: E402

CLASS_MAPPING = json.loads((ROOT / "models" / "class_mapping.json").read_text(encoding="utf-8"))

# Raw model outputs around every place where the JSON rounds what the report reads unrounded.
EDGE_LVEFS = [0.0, 0.04, 0.05, 12.34, 28.5, 39.94, 39.95, 39.96, 39.999, 40.0, 40.04, 40.05, 49.94, 49.95, 49.96,
              49.999, 50.0, 50.04, 55.0, 60.7, 99.96, 100.0]
EDGE_PROBABILITIES = [0.0, 0.0004, 0.0005, 0.005, 0.0051, 0.1344, 0.1345, 0.1349, 0.135, 0.1351, 0.1354, 0.125,
                      0.4944, 0.4949, 0.495, 0.4951, 0.4995, 0.4996, 0.49999, 0.5, 0.5004, 0.5005, 0.96, 0.9949,
                      0.995, 0.9951, 0.9995, 0.9996, 1.0]


@pytest.fixture(scope="module")
def client():
    cwd = os.getcwd()
    os.chdir(ROOT)  # main.py reads config.json, data/ and docs/ from the working directory
    try:
        import main
    finally:
        os.chdir(cwd)
    return TestClient(main.app)  # no `with`: the startup idle watcher is not started


@pytest.fixture
def service(monkeypatch):
    monkeypatch.setattr(CustomPredictionService, "_class_mapping", copy.deepcopy(CLASS_MAPPING), raising=False)
    return CustomPredictionService()


def _live_json(service, preds: dict) -> dict:
    """What _handle_json_output returns for these raw outputs, as the caller stores it."""
    return json.loads(json.dumps({
        "diagnosis": service._diagnosis_text(preds),
        "predictions": service._format_predictions(preds),
        "modelRecommendations": service._recommendations(preds),
    }))


def _live_html(service, preds: dict) -> str:
    """What _handle_html_output renders for these raw outputs."""
    return service._render_html(preds, service._recommendations(preds))


def _predicting(monkeypatch, postprocessed: dict | None = None, outputs: dict | None = None):
    """predict answers without DICOMs, weights or GPU: either from raw head outputs (through the real _postprocess)
    or from already post-processed values."""
    monkeypatch.setattr(CustomPredictionService, "is_initialized", True)
    monkeypatch.setattr(CustomPredictionService, "_class_mapping", copy.deepcopy(CLASS_MAPPING), raising=False)
    monkeypatch.setattr(CustomPredictionService, "load_model", lambda self, config: None)
    monkeypatch.setattr(CustomPredictionService, "_decode_dicoms", lambda self, request: [])
    monkeypatch.setattr(CustomPredictionService, "_run_inference", lambda self, dicoms: outputs if outputs is not None else {})
    if postprocessed is not None:
        monkeypatch.setattr(CustomPredictionService, "_postprocess", lambda self, outs: dict(postprocessed))


def _no_model(monkeypatch):
    def refuse(*args, **kwargs):
        raise AssertionError("render-report must not touch the model")
    monkeypatch.setattr(CustomPredictionService, "is_initialized", False)
    monkeypatch.setattr(CustomPredictionService, "models", {})
    monkeypatch.setattr(CustomPredictionService, "load_model", refuse)
    monkeypatch.setattr(CustomPredictionService, "_run_inference", refuse)
    monkeypatch.setattr(CustomPredictionService, "_postprocess", refuse)
    monkeypatch.setattr(torch, "load", refuse)


def _stored(lvef: float = 28.5, probability: float = 0.9612) -> dict:
    monkey = pytest.MonkeyPatch()
    try:
        monkey.setattr(CustomPredictionService, "_class_mapping", copy.deepcopy(CLASS_MAPPING), raising=False)
        return _live_json(CustomPredictionService(), {"Value": lvef, "y_true_cat": probability})
    finally:
        monkey.undo()


def _html(response) -> str:
    return base64.b64decode(response.json()["data"]["htmlBase64"]).decode("utf-8")


# -- stored result == live HTML ------------------------------------------------------------------------------------

@pytest.mark.parametrize("lvef, probability", [(28.5, 0.9612), (39.96, 0.2), (40.0, 0.2), (49.96, 0.01), (50.0, 0.01),
                                               (61.3, 0.1349), (61.3, 0.1351), (44.0, 0.4996), (44.0, 0.5),
                                               (72.25, 0.0), (0.0, 1.0)])
def test_render_report_equals_html_mode_predict(client, monkeypatch, lvef, probability):
    _predicting(monkeypatch, postprocessed={"Value": lvef, "y_true_cat": probability})
    stored = client.post("/inference/predict", json={"outputMode": "JSON"}).json()
    html_mode = client.post("/inference/predict", json={"outputMode": "HTML"}).json()
    assert stored["success"] and html_mode["success"]

    _no_model(monkeypatch)
    result = json.loads(json.dumps(stored["data"]))  # as stored by the caller
    rendered = client.post("/inference/render-report", json={"result": result})
    assert rendered.status_code == 200, rendered.text
    body = rendered.json()
    import main
    assert body["success"] is True and body["data"]["modelVersion"] == main.served_model_info["version"] == "1.0.2"
    assert body["data"]["htmlBase64"] == html_mode["data"]["htmlBase64"]
    assert f"{round(lvef, 1)}%" in _html(rendered)
    # the whole response envelope is accepted too
    again = client.post("/inference/render-report", json={"result": stored})
    assert again.json()["data"]["htmlBase64"] == html_mode["data"]["htmlBase64"]
    assert CustomPredictionService.is_initialized is False and CustomPredictionService.models == {}


def test_render_report_equals_html_mode_predict_from_raw_head_outputs(client, monkeypatch):
    """Through the real _postprocess (float32 sigmoid and clamp)."""
    rng = random.Random(7)
    for _ in range(40):
        outputs = {"Value": torch.tensor([[rng.uniform(-5.0, 105.0)]]), "y_true_cat": torch.tensor([[rng.uniform(-8.0, 8.0)]])}
        with monkeypatch.context() as patch:
            _predicting(patch, outputs=outputs)
            stored = client.post("/inference/predict", json={"outputMode": "JSON"}).json()["data"]
            html_mode = client.post("/inference/predict", json={"outputMode": "HTML"}).json()["data"]["htmlBase64"]
        with monkeypatch.context() as patch:
            _no_model(patch)
            rendered = client.post("/inference/render-report", json={"result": stored})
        assert rendered.status_code == 200, (outputs, rendered.text)
        assert rendered.json()["data"]["htmlBase64"] == html_mode


def test_every_rounding_edge_renders_the_live_html(service):
    for lvef in EDGE_LVEFS:
        for probability in EDGE_PROBABILITIES:
            preds = {"Value": lvef, "y_true_cat": probability}
            assert CustomPredictionService.report_html(_live_json(service, preds)) == _live_html(service, preds), preds


def test_dense_sweep_renders_the_live_html(service):
    """LVEF in steps of 0.01 and probability in steps of 0.0001, each against the other's edge values, plus random
    pairs: the stored JSON always determines the HTML-mode document."""
    rng = random.Random(20261114)
    pairs = [(i / 100.0, p) for i in range(10001) for p in (0.0, 0.1349, 0.4996, 0.5, 0.9951)]
    pairs += [(v, i / 10000.0) for i in range(10001) for v in (12.3, 39.96, 40.0, 49.96, 50.0, 71.0)]
    pairs += [(rng.uniform(0.0, 100.0), rng.random()) for _ in range(20000)]
    pairs += [(float(torch.tensor(rng.uniform(0.0, 100.0), dtype=torch.float32)),
               float(torch.sigmoid(torch.tensor(rng.uniform(-9.0, 9.0), dtype=torch.float32)))) for _ in range(5000)]
    for lvef, probability in pairs:
        preds = {"Value": lvef, "y_true_cat": probability}
        assert CustomPredictionService.report_html(_live_json(service, preds)) == _live_html(service, preds), preds


def test_whole_numbers_stored_without_a_decimal_point_render_the_same(service):
    """A JSON store may hand 55.0 back as 55 and 1.0 as 1."""
    preds = {"Value": 55.0, "y_true_cat": 1.0}
    stored = _live_json(service, preds)
    stored["predictions"]["LVEF"]["value"] = 55
    stored["predictions"]["reducedEF"]["probability"] = 1
    assert CustomPredictionService.report_html(stored) == _live_html(service, preds)
    assert "55.0%" in CustomPredictionService.report_html(stored)


def test_no_video_result_renders_the_no_video_page(client, monkeypatch):
    with monkeypatch.context() as patch:
        _predicting(patch)
        patch.setattr(CustomPredictionService, "_run_inference", lambda self, dicoms: None)
        stored = client.post("/inference/predict", json={"outputMode": "JSON"}).json()["data"]
        html_mode = client.post("/inference/predict", json={"outputMode": "HTML"}).json()["data"]["htmlBase64"]
    assert stored == NO_VIDEO_RESULT
    _no_model(monkeypatch)
    rendered = client.post("/inference/render-report", json={"result": stored})
    assert rendered.status_code == 200 and rendered.json()["data"]["htmlBase64"] == html_mode
    assert _html(rendered) == NO_VIDEO_HTML


# -- unusable and hostile input ------------------------------------------------------------------------------------

SCRIPT = "<script>alert(1)</script>"


@pytest.mark.parametrize("mutate, where", [
    # stored HTML is never passed through
    (lambda r: r["modelRecommendations"].update(en=r["modelRecommendations"]["en"] + SCRIPT), "modelRecommendations"),
    (lambda r: r["modelRecommendations"].update(en=f"<img src=x onerror=alert(1)>"), "modelRecommendations"),
    (lambda r: r["modelRecommendations"].update(en=r["modelRecommendations"]["en"].replace("28.5", "65.0")), "modelRecommendations"),
    (lambda r: r["modelRecommendations"].update(fr=r["modelRecommendations"]["fr"] + SCRIPT), "modelRecommendations"),
    (lambda r: r["modelRecommendations"].update(en="x" * 1001), "modelRecommendations.en"),
    (lambda r: r["modelRecommendations"].update(en=None), "modelRecommendations.en"),
    (lambda r: r["modelRecommendations"].update(presentable="true"), "modelRecommendations.presentable"),
    (lambda r: r["modelRecommendations"].update(presentable=False), "modelRecommendations.presentable"),
    (lambda r: r["modelRecommendations"].update(extra=1), "modelRecommendations"),
    (lambda r: r.update(modelRecommendations=None), "modelRecommendations"),
    (lambda r: r.update(diagnosis=r["diagnosis"] + SCRIPT), "diagnosis"),
    (lambda r: r.update(diagnosis=r["diagnosis"].replace("P=0.96", "P=0.97")), "diagnosis"),
    (lambda r: r.update(diagnosis=r["diagnosis"].replace("Reduced EF (<40%)", "Preserved EF")), "diagnosis"),
    (lambda r: r.update(diagnosis=7), "diagnosis"),
    (lambda r: r.update(diagnosis="d" * 201), "diagnosis"),
    (lambda r: r.pop("diagnosis"), "result"),
    (lambda r: r.update(html=SCRIPT), "result"),
    # numbers
    (lambda r: r["predictions"]["LVEF"].update(value=101.0), "predictions.LVEF.value"),
    (lambda r: r["predictions"]["LVEF"].update(value=-0.1), "predictions.LVEF.value"),
    (lambda r: r["predictions"]["LVEF"].update(value=28.55), "predictions.LVEF.value"),
    (lambda r: r["predictions"]["LVEF"].update(value="28.5"), "predictions.LVEF.value"),
    (lambda r: r["predictions"]["LVEF"].update(value=True), "predictions.LVEF.value"),
    (lambda r: r["predictions"]["LVEF"].update(value=None), "predictions.LVEF.value"),
    (lambda r: r["predictions"]["LVEF"].update(value=[28.5]), "predictions.LVEF.value"),
    (lambda r: r["predictions"]["LVEF"].update(value=10 ** 400), "predictions.LVEF.value"),
    (lambda r: r["predictions"]["LVEF"].update(value=SCRIPT), "predictions.LVEF.value"),
    (lambda r: r["predictions"]["LVEF"].update(unit=SCRIPT), "predictions.LVEF.unit"),
    (lambda r: r["predictions"]["LVEF"].pop("unit"), "predictions.LVEF"),
    (lambda r: r["predictions"]["reducedEF"].update(probability=1.5), "predictions.reducedEF.probability"),
    (lambda r: r["predictions"]["reducedEF"].update(probability=-0.001), "predictions.reducedEF.probability"),
    (lambda r: r["predictions"]["reducedEF"].update(probability=0.96125), "predictions.reducedEF.probability"),
    (lambda r: r["predictions"]["reducedEF"].update(probability=False), "predictions.reducedEF.probability"),
    (lambda r: r["predictions"]["reducedEF"].update(threshold=0.4), "predictions.reducedEF.threshold"),
    (lambda r: r["predictions"]["reducedEF"].update(threshold="0.5"), "predictions.reducedEF.threshold"),
    (lambda r: r["predictions"]["reducedEF"].update(diagnosis="preserved"), "predictions.reducedEF.diagnosis"),
    (lambda r: r["predictions"]["reducedEF"].update(diagnosis=SCRIPT), "predictions.reducedEF.diagnosis"),
    (lambda r: r["predictions"]["reducedEF"].update(diagnosis=["reduced"]), "predictions.reducedEF.diagnosis"),
    (lambda r: r["predictions"]["reducedEF"].pop("threshold"), "predictions.reducedEF"),
    (lambda r: r["predictions"].update(extra={"html": SCRIPT}), "predictions"),
    (lambda r: r["predictions"].update(LVEF=[]), "predictions.LVEF"),
    (lambda r: r.update(predictions=[]), "predictions"),
    (lambda r: r.update(predictions={}), "predictions"),  # empty predictions with a non-no-video text
    # a consistent result for other numbers, with this result's numbers swapped in
    (lambda r: r["predictions"]["LVEF"].update(value=65.0), "diagnosis"),
    (lambda r: r["predictions"]["reducedEF"].update(probability=0.7), "diagnosis"),
])
def test_unusable_results_are_422_with_the_bad_path(client, monkeypatch, mutate, where):
    _no_model(monkeypatch)
    result = _stored()
    mutate(result)
    response = client.post("/inference/render-report", content=json.dumps({"result": result}),
                           headers={"content-type": "application/json"})
    body = response.json()
    assert response.status_code == 422, body
    assert body["success"] is False and body["errorCode"] == "INVALID_RESULT" and where in body["message"]
    assert "htmlBase64" not in body.get("data", {}) and SCRIPT not in body["message"]


def test_a_tampered_no_video_result_is_refused(client, monkeypatch):
    _no_model(monkeypatch)
    for mutate in (lambda r: r.update(diagnosis=SCRIPT),
                   lambda r: r["modelRecommendations"].update(en=SCRIPT),
                   lambda r: r["modelRecommendations"].update(presentable=True)):
        result = copy.deepcopy(NO_VIDEO_RESULT)
        mutate(result)
        assert client.post("/inference/render-report", json={"result": result}).status_code == 422


def test_rendered_html_contains_only_regenerated_text(service):
    """Every character of the page comes from the template and the checked numbers: rendering the stored result of
    one prediction never shows a string that only the request carried."""
    preds = {"Value": 28.5, "y_true_cat": 0.9612}
    html = CustomPredictionService.report_html(_live_json(service, preds))
    assert html == _live_html(service, preds)
    assert html.count("<strong>") == 1 and "<script" not in html and "onerror" not in html
    assert re.search(r"<div class=\"rec\"><strong>Reduced LVEF detected \(predicted 28\.5%, P\(EF<40%\)=0\.96\)\.</strong> ", html)


def test_malformed_bodies_are_rejected(client, monkeypatch):
    _no_model(monkeypatch)
    good = json.dumps(_stored()).encode()
    deep = b'{"result": ' + b"[" * 20000 + b"]" * 20000 + b"}"
    for raw in (b"", b"not json", b'{"result": NaN}', b"[]", b"null", b'"result"', b'{"result": {}}', b'{"result": []}',
                b'{"result": {"predictions": []}}', b'{"result": ' + good + b', "extra": 1}', b'{"Result": ' + good + b"}",
                good.replace(b"28.5", b"NaN"), good.replace(b"28.5", b"Infinity"), b'{"result": {"data": {}}}',
                b'{"result": ' + good.replace(b"0.961", b"1e999") + b"}", b"\xff\xfe", deep):
        response = client.post("/inference/render-report", content=raw, headers={"content-type": "application/json"})
        assert response.status_code == 422, raw[:80]
        assert response.json()["errorCode"] == "INVALID_RESULT"


def test_oversize_body_is_413(client, monkeypatch):
    _no_model(monkeypatch)
    import main
    assert main.RENDER_REPORT_MAX_BYTES == 64 * 1024
    big = b'{"result": "' + b"a" * main.RENDER_REPORT_MAX_BYTES + b'"}'
    response = client.post("/inference/render-report", content=big, headers={"content-type": "application/json"})
    assert response.status_code == 413 and response.json()["errorCode"] == "PAYLOAD_TOO_LARGE"
    padded = json.dumps({"result": _stored()}).encode()
    padded += b" " * (main.RENDER_REPORT_MAX_BYTES - len(padded))  # exactly at the limit
    assert client.post("/inference/render-report", content=padded,
                       headers={"content-type": "application/json"}).status_code == 200
    assert client.post("/inference/render-report", content=padded + b" ",
                       headers={"content-type": "application/json"}).status_code == 413


def test_get_is_not_allowed(client):
    assert client.get("/inference/render-report").status_code == 405


# -- no model, no activity, advertised -----------------------------------------------------------------------------

def test_render_report_does_not_load_the_model_or_need_class_mapping_from_load(client, monkeypatch):
    _no_model(monkeypatch)
    monkeypatch.delattr(CustomPredictionService, "_class_mapping", raising=False)  # as in a container that never predicted
    import main
    before = main.last_request_time
    response = client.post("/inference/render-report", json={"result": _stored(61.3, 0.02)})
    assert response.status_code == 200 and "61.3%" in _html(response)
    assert CustomPredictionService.is_initialized is False and CustomPredictionService.models == {}
    assert main.last_request_time == before  # the idle timer is left alone
    assert main.inference_lock._value == 1  # the inference semaphore was not taken
    runtime = client.get("/inference/model/runtime").json()["data"]
    assert runtime["state"] == "UNLOADED"


def test_render_report_is_not_model_activity():
    from utils.model_lifecycle import records_model_activity
    assert not records_model_activity("/inference/render-report")


def test_model_info_advertises_render_report(client):
    data = client.get("/inference/model-info").json()["data"]
    assert data["capabilities"] == {"renderReport": True}
    assert data["version"] == "1.0.2" and "build" not in data
    manifest = json.loads((ROOT / "data" / "model_info.json").read_text(encoding="utf-8"))
    for dockerfile in ("Dockerfile", "Dockerfile.local"):
        text = (ROOT / dockerfile).read_text(encoding="utf-8")
        assert "COPY report_input.py ." in text


def test_openapi_documents_render_report():
    openapi = json.loads((ROOT / "docs" / "openapi.json").read_text(encoding="utf-8"))
    operation = openapi["paths"]["/inference/render-report"]["post"]
    assert set(operation["responses"]) == {"200", "413", "422"}
    example = operation["requestBody"]["content"]["application/json"]["example"]["result"]
    assert "60.7%" in CustomPredictionService.report_html(example)
    assert {"RenderReportRequest", "RenderReportResponse"} <= set(openapi["components"]["schemas"])


def test_checked_result_rejects_non_finite_python_floats():
    for bad in (math.nan, math.inf, -math.inf):
        result = _stored()
        result["predictions"]["LVEF"]["value"] = bad
        with pytest.raises(ResultError, match="predictions.LVEF.value"):
            CustomPredictionService.report_html(result)
