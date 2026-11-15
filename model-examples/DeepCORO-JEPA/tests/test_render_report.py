"""/inference/render-report: the HTML of a stored JSON-mode result, identical to HTML-mode predict, without the model."""
from __future__ import annotations

import base64
import copy
import json
import os
import sys
from pathlib import Path

import cv2 as cv
import numpy as np
import pytest
import torch
from fastapi.testclient import TestClient

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from logic import CustomPredictionService  # noqa: E402
from report_input import ResultError, predictions_of  # noqa: E402
from test_logic_units import GROUPS, THRESHOLDS, VALID_DOMINANCE, _dominance_request  # noqa: E402


@pytest.fixture(scope="module")
def client():
    cwd = os.getcwd()
    os.chdir(ROOT)  # main.py reads config.json, data/ and docs/ from the working directory
    try:
        import main
    finally:
        os.chdir(cwd)
    return TestClient(main.app)  # no `with`: the startup idle watcher is not started


def _jpeg(seed: int) -> str:
    image = np.random.default_rng(seed).integers(0, 255, (48, 48, 3), dtype=np.uint8)
    ok, buf = cv.imencode(".jpg", image)
    assert ok
    return base64.b64encode(buf.tobytes()).decode("ascii")


def _prediction() -> dict:
    """Synthetic v13 prediction with dominance, a CTO, a thrombus, physiology and explanation images."""
    severity, prob = torch.full((18,), 20.0), torch.full((18,), 0.05)
    severity[GROUPS.index("mid_rca")], prob[GROUPS.index("mid_rca")] = 72.0, 0.81
    severity[GROUPS.index("prox_lad")], prob[GROUPS.index("prox_lad")] = 64.0, 0.55
    cto, thrombus = torch.zeros(18), torch.zeros(18)
    cto[GROUPS.index("mid_rca")] = 0.4
    thrombus[GROUPS.index("prox_lad")] = 0.3
    res = {"encoder_passes": 2, "severity": severity, "binary_probability": prob,
           "physiology": {"ffr": torch.tensor([0.78]), "ifr": torch.tensor([0.9]),
                          "ffr_logit": torch.tensor([0.5]), "ifr_logit": torch.tensor([-1.0])},
           "lesion_calcif_probability": torch.full((18,), 0.01), "lesion_thrombus_probability": thrombus,
           "lesion_cto_probability": cto}
    preds = CustomPredictionService.postprocess(GROUPS, res, n_videos=4, thresholds=THRESHOLDS)
    preds["coronary_dominance"] = CustomPredictionService.coronary_dominance(_dominance_request(**VALID_DOMINANCE))
    preds["explainability"] = [
        {"segment": "mid_rca", "has_crop": True, "video": 1, "frame": 6, "crop_box_yxhw": [10.0, 20.0, 40.0, 40.0],
         "bag_attention": 0.7, "max_full_view_attention": 0.2, "peak_in_crop_xy": [0.625, 0.275],
         "top10pct_token_share": 0.41, "images": {"crop": _jpeg(1), "frame": _jpeg(2)},
         "series_instance_uid": "1.2.3.4", "sop_instance_uid": "1.2.3.4.5"},
        {"segment": "prox_lad", "has_crop": False, "max_full_view_attention": 0.9},
    ]
    return preds


@pytest.fixture
def predicting(monkeypatch):
    """predict answers with the synthetic prediction without weights or GPU."""
    preds = _prediction()
    monkeypatch.setattr(CustomPredictionService, "is_initialized", True)
    monkeypatch.setattr(CustomPredictionService, "load_model", lambda self, config: None)
    monkeypatch.setattr(CustomPredictionService, "_predict", lambda self, request: copy.deepcopy(preds))
    return preds


def _no_model(monkeypatch):
    def refuse(*args, **kwargs):
        raise AssertionError("render-report must not touch the model")
    monkeypatch.setattr(CustomPredictionService, "is_initialized", False)
    monkeypatch.setattr(CustomPredictionService, "load_model", refuse)
    monkeypatch.setattr(CustomPredictionService, "_run_inference", refuse)


def test_render_report_equals_html_mode_predict(client, predicting, monkeypatch):
    stored = client.post("/inference/predict", json={"outputMode": "JSON"}).json()
    html_mode = client.post("/inference/predict", json={"outputMode": "HTML"}).json()
    assert stored["success"] and html_mode["success"]
    item = stored["data"]["predictions"]["explainability"][0]
    assert item["images"] == predicting["explainability"][0]["images"] and "_images" not in item

    _no_model(monkeypatch)
    result = json.loads(json.dumps(stored["data"]))  # as stored by the caller
    rendered = client.post("/inference/render-report", json={"result": result})
    assert rendered.status_code == 200, rendered.text
    body = rendered.json()
    import main
    assert body["success"] is True and body["data"]["modelVersion"] == main.served_model_info["version"]
    assert body["data"]["reportModelVersion"] == CustomPredictionService.report_model_version()
    assert body["data"]["htmlBase64"] == html_mode["data"]["htmlBase64"]
    html = base64.b64decode(body["data"]["htmlBase64"]).decode("utf-8")
    assert "Left dominant" in html and html.count('class="cto-mark"') == 1 and html.count('class="thrombus"') == 1
    assert html.count("data:image/jpeg;base64,") == 2 and "LAD physiology" in html
    # the whole response envelope is accepted too
    again = client.post("/inference/render-report", json={"result": stored})
    assert again.json()["data"]["htmlBase64"] == html_mode["data"]["htmlBase64"]


def test_no_video_result_renders_the_no_video_page(client, monkeypatch):
    _no_model(monkeypatch)
    body = client.post("/inference/render-report", json={"result": {"predictions": {}, "diagnosis": "x"}}).json()
    assert base64.b64decode(body["data"]["htmlBase64"]).decode() == "<h1>No video could be extracted from the current DICOM series</h1>"


def test_a_1_2_0_result_without_images_still_renders(client, monkeypatch):
    _no_model(monkeypatch)
    preds = json.loads(json.dumps(_prediction()))
    del preds["explainability"][0]["images"]
    body = client.post("/inference/render-report", json={"result": {"predictions": preds}}).json()
    html = base64.b64decode(body["data"]["htmlBase64"]).decode()
    assert "data:image/jpeg" not in html and "images were not stored with this result" in html


@pytest.mark.parametrize("mutate, where", [
    (lambda p: p["segments"].update(bogus=p["segments"]["mid_rca"]), "predictions.segments.bogus"),
    (lambda p: p["segments"]["mid_rca"].update(prob_significant=1.5), "prob_significant"),
    (lambda p: p["segments"]["mid_rca"].update(significant="yes"), "significant"),
    (lambda p: p["segments"]["mid_rca"].pop("cto_prob"), "cto_prob"),
    (lambda p: p.pop("segments"), "predictions.segments"),
    (lambda p: p.update(n_videos=True), "n_videos"),
    (lambda p: p["coronary_dominance"].update(left_votes=9), "coronary_dominance"),
    (lambda p: p["lad_physiology"].pop("ifr_abnormal"), "ifr_abnormal"),
    (lambda p: p["explainability"][0]["images"].update(crop='"><script>alert(1)</script>'), "images.crop"),
    (lambda p: p["explainability"][0]["images"].update(frame=base64.b64encode(b"\x89PNG....").decode()), "images.frame"),
    (lambda p: p["explainability"][0].update(video=-1), "video"),
])
def test_unusable_results_are_422_with_the_bad_path(client, monkeypatch, mutate, where):
    _no_model(monkeypatch)
    preds = json.loads(json.dumps(_prediction()))
    mutate(preds)
    response = client.post("/inference/render-report", json={"result": {"predictions": preds}})
    body = response.json()
    assert response.status_code == 422 and body["errorCode"] == "INVALID_RESULT" and where in body["message"]


def test_malformed_bodies_are_rejected(client, monkeypatch):
    _no_model(monkeypatch)
    for raw in (b"not json", b'{"result": NaN}', b"[]", b'{"result": {}}', b'{"result": {"predictions": []}}',
                b'{"result": {"predictions": {}}, "extra": 1}'):
        response = client.post("/inference/render-report", content=raw, headers={"content-type": "application/json"})
        assert response.status_code == 422, raw
    big = b'{"result": "' + b"a" * (5 * 1024 * 1024) + b'"}'
    response = client.post("/inference/render-report", content=big, headers={"content-type": "application/json"})
    assert response.status_code == 413 and response.json()["errorCode"] == "PAYLOAD_TOO_LARGE"


def test_render_report_is_not_model_activity():
    from utils.model_lifecycle import records_model_activity
    assert not records_model_activity("/inference/render-report")


def test_model_info_advertises_render_report(client):
    data = client.get("/inference/model-info").json()["data"]
    assert data["capabilities"] == {"renderReport": True}


def test_predictions_of_reads_the_stored_shapes():
    preds = json.loads(json.dumps(_prediction()))
    assert predictions_of({"predictions": preds}) is preds
    assert predictions_of({"success": True, "data": {"predictions": preds}}) is preds
    with pytest.raises(ResultError, match="result.predictions"):
        predictions_of({"diagnosis": "x"})
