"""/inference/render-report: the HTML of a stored JSON-mode result without the model. It is the HTML-mode report
except for the four J-CTO component probability cells, printed as stored (three decimals) under a note."""
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

import render_cases as cases  # noqa: E402
from logic import CustomPredictionService  # noqa: E402
from report_input import NO_VIDEO_HTML, NO_VIDEO_RESULT, ResultError  # noqa: E402
from utils.http_utils import JsonPredictionResponse  # noqa: E402

SCRIPT = "<script>alert(1)</script>"
JSON_HEADERS = {"content-type": "application/json"}


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
    monkeypatch.setattr(CustomPredictionService, "_class_mapping", copy.deepcopy(cases.CLASS_MAPPING), raising=False)
    return CustomPredictionService()


def _live_json(service, preds: dict) -> dict:
    """What JSON-mode predict answers for these raw outputs, as the caller stores it."""
    preds = copy.deepcopy(preds)
    return json.loads(json.dumps(JsonPredictionResponse(
        diagnosis=service._diagnosis_text(preds),
        predictions=service._format_predictions(preds),
        modelRecommendations=service._recommendations(preds),
    ).model_dump()))


def _live_html(service, preds: dict) -> str:
    """What HTML-mode predict renders for these raw outputs."""
    preds = copy.deepcopy(preds)
    return service._render_html(preds, service._recommendations(preds))


def _predicting(monkeypatch, postprocessed: dict | None = None, outputs: dict | None = None):
    """predict answers without DICOMs, weights or GPU: either from raw head outputs (through the real _postprocess)
    or from already post-processed values."""
    monkeypatch.setattr(CustomPredictionService, "is_initialized", True)
    monkeypatch.setattr(CustomPredictionService, "_class_mapping", copy.deepcopy(cases.CLASS_MAPPING), raising=False)
    monkeypatch.setattr(CustomPredictionService, "load_model", lambda self, config: None)
    monkeypatch.setattr(CustomPredictionService, "_decode_dicoms", lambda self, request: [])
    monkeypatch.setattr(CustomPredictionService, "_run_inference",
                        lambda self, *args, **kwargs: outputs if outputs is not None else {})
    if postprocessed is not None:
        monkeypatch.setattr(CustomPredictionService, "_postprocess", lambda self, outs: copy.deepcopy(postprocessed))


def _no_model(monkeypatch):
    def refuse(*args, **kwargs):
        raise AssertionError("render-report must not touch the model")
    monkeypatch.setattr(CustomPredictionService, "is_initialized", False)
    monkeypatch.setattr(CustomPredictionService, "models", {})
    monkeypatch.setattr(CustomPredictionService, "load_model", refuse)
    monkeypatch.setattr(CustomPredictionService, "_run_inference", refuse)
    monkeypatch.setattr(CustomPredictionService, "_postprocess", refuse)
    monkeypatch.setattr(torch, "load", refuse)


def _stored(preds: dict | None = None) -> dict:
    monkey = pytest.MonkeyPatch()
    try:
        monkey.setattr(CustomPredictionService, "_class_mapping", copy.deepcopy(cases.CLASS_MAPPING), raising=False)
        return _live_json(CustomPredictionService(), preds or cases.EXAMPLE)
    finally:
        monkey.undo()


def _b64_html(text: str) -> str:
    return base64.b64decode(text).decode("utf-8")


def _same(stored_html: str, live_html: str) -> bool:
    """Equal once the stored-result note is removed and the four component probability cells are blanked; the
    note is on the stored report only."""
    assert stored_html.count(cases.NOTE) == 1 and cases.NOTE not in live_html
    return cases.normalise(stored_html) == cases.normalise(live_html)


def _html(response) -> str:
    return base64.b64decode(response.json()["data"]["htmlBase64"]).decode("utf-8")


def _set_main(result: dict, value) -> None:
    node = result
    for key in cases.MAIN[:-1]:
        node = node[key]
    node[cases.MAIN[-1]] = value


# -- stored result == live HTML ------------------------------------------------------------------------------------

def test_render_report_equals_html_mode_predict(client, monkeypatch):
    import main
    edge = cases.edge_preds()
    for preds in [cases.EXAMPLE] + edge[:: max(1, len(edge) // 12)]:
        with monkeypatch.context() as patch:
            _predicting(patch, postprocessed=preds)
            stored = client.post("/inference/predict", json={"outputMode": "JSON"}).json()
            html_mode = client.post("/inference/predict", json={"outputMode": "HTML"}).json()
        assert stored["success"] and html_mode["success"], (stored, html_mode)
        with monkeypatch.context() as patch:
            _no_model(patch)
            result = json.loads(json.dumps(stored["data"]))  # as stored by the caller
            rendered = client.post("/inference/render-report", json={"result": result})
            assert rendered.status_code == 200, (preds, rendered.text)
            body = rendered.json()
            assert body["success"] is True and set(body["data"]) == {"htmlBase64", "modelVersion"}
            assert body["data"]["modelVersion"] == main.served_model_info["version"] == cases.VERSION
            assert _same(_html(rendered), _b64_html(html_mode["data"]["htmlBase64"])), preds
            assert _html(rendered) == cases.as_stored(_b64_html(html_mode["data"]["htmlBase64"]), preds), preds
            # the whole response envelope is accepted too
            again = client.post("/inference/render-report", json={"result": stored})
            assert again.json()["data"]["htmlBase64"] == body["data"]["htmlBase64"]
            assert CustomPredictionService.is_initialized is False and CustomPredictionService.models == {}


def test_render_report_equals_html_mode_predict_from_raw_head_outputs(client, monkeypatch):
    """Through the real _postprocess (float32 sigmoid, softmax and clamp)."""
    rng = random.Random(7)
    for _ in range(40):
        outputs = cases.raw_outputs(rng)
        with monkeypatch.context() as patch:
            _predicting(patch, outputs=outputs)
            stored = client.post("/inference/predict", json={"outputMode": "JSON"}).json()["data"]
            html_mode = client.post("/inference/predict", json={"outputMode": "HTML"}).json()["data"]["htmlBase64"]
        with monkeypatch.context() as patch:
            _no_model(patch)
            rendered = client.post("/inference/render-report", json={"result": stored})
        assert rendered.status_code == 200, (outputs, rendered.text)
        assert _same(_html(rendered), _b64_html(html_mode))


def test_every_rounding_edge_renders_the_live_html(service):
    for preds in cases.edge_preds():
        rendered = CustomPredictionService.report_html(_live_json(service, preds))
        assert _same(rendered, _live_html(service, preds)), preds
        assert rendered == cases.as_stored(_live_html(service, preds), preds), preds


def test_dense_sweep_renders_the_live_html(service):
    for preds in cases.sweep_preds(random.Random(20261114)):
        rendered = CustomPredictionService.report_html(_live_json(service, preds))
        assert _same(rendered, _live_html(service, preds)), preds
        assert rendered == cases.as_stored(_live_html(service, preds), preds), preds


def test_whole_numbers_stored_without_a_decimal_point_render_the_same(service):
    """A JSON store may hand 1.0 back as 1 and 0.0 as 0."""
    def whole(node):
        if isinstance(node, dict):
            return {key: whole(value) for key, value in node.items()}
        if isinstance(node, float) and node == int(node):
            return int(node)
        return node
    changed = 0
    for preds in cases.edge_preds():
        stored = _live_json(service, preds)
        changed += whole(stored) != stored or json.dumps(whole(stored)) != json.dumps(stored)
        assert CustomPredictionService.report_html(whole(stored)) == cases.as_stored(_live_html(service, preds), preds), preds
    assert changed


def test_no_video_result_renders_the_no_video_page(client, monkeypatch):
    with monkeypatch.context() as patch:
        _predicting(patch)
        patch.setattr(CustomPredictionService, "_run_inference", lambda self, *args, **kwargs: None)
        stored = client.post("/inference/predict", json={"outputMode": "JSON"}).json()["data"]
        html_mode = client.post("/inference/predict", json={"outputMode": "HTML"}).json()["data"]["htmlBase64"]
    assert stored == NO_VIDEO_RESULT
    _no_model(monkeypatch)
    rendered = client.post("/inference/render-report", json={"result": stored})
    assert rendered.status_code == 200 and rendered.json()["data"]["htmlBase64"] == html_mode
    assert _html(rendered) == NO_VIDEO_HTML


# -- unusable and hostile input ------------------------------------------------------------------------------------

MAIN_PATH = ".".join(cases.MAIN)


@pytest.mark.parametrize("mutate, where", [
    # stored HTML or text is never passed through
    (lambda r: r["modelRecommendations"].update(en=r["modelRecommendations"]["en"] + SCRIPT), "modelRecommendations.en"),
    (lambda r: r["modelRecommendations"].update(en="<img src=x onerror=alert(1)>"), "modelRecommendations.en"),
    (lambda r: r["modelRecommendations"].update(fr=r["modelRecommendations"]["fr"] + SCRIPT), "modelRecommendations.fr"),
    (lambda r: r["modelRecommendations"].update(en="x" * 5000), "modelRecommendations.en"),
    (lambda r: r["modelRecommendations"].update(en=None), "modelRecommendations.en"),
    (lambda r: r["modelRecommendations"].update(en=["<b>"]), "modelRecommendations.en"),
    (lambda r: r["modelRecommendations"].update(presentable="true"), "modelRecommendations.presentable"),
    (lambda r: r["modelRecommendations"].update(presentable=0), "modelRecommendations.presentable"),
    (lambda r: r["modelRecommendations"].update(extra=SCRIPT), "modelRecommendations"),
    (lambda r: r["modelRecommendations"].pop("fr"), "modelRecommendations.fr"),
    (lambda r: r.update(modelRecommendations=None), "modelRecommendations"),
    (lambda r: r.update(modelRecommendations=SCRIPT), "modelRecommendations"),
    (lambda r: r.update(diagnosis=r["diagnosis"] + SCRIPT), "diagnosis"),
    (lambda r: r.update(diagnosis=7), "diagnosis"),
    (lambda r: r.update(diagnosis=None), "diagnosis"),
    (lambda r: r.pop("diagnosis"), "result"),
    (lambda r: r.update(html=SCRIPT), "result"),
    # the stored number
    (lambda r: _set_main(r, cases.MAIN_BAD["too_low"]), MAIN_PATH),
    (lambda r: _set_main(r, cases.MAIN_BAD["too_high"]), MAIN_PATH),
    (lambda r: _set_main(r, cases.MAIN_BAD["too_many_decimals"]), MAIN_PATH),
    (lambda r: _set_main(r, "0.5"), MAIN_PATH),
    (lambda r: _set_main(r, True), MAIN_PATH),
    (lambda r: _set_main(r, None), MAIN_PATH),
    (lambda r: _set_main(r, [0.5]), MAIN_PATH),
    (lambda r: _set_main(r, {"value": 0.5}), MAIN_PATH),
    (lambda r: _set_main(r, 10 ** 400), MAIN_PATH),
    (lambda r: _set_main(r, SCRIPT), MAIN_PATH),
    # another valid number under this result's text
    (lambda r: _set_main(r, cases.MAIN_BAD["other"]), ""),
    # structure
    (lambda r: r["predictions"].update(extra={"html": SCRIPT}), "predictions"),
    (lambda r: r["predictions"].pop(cases.MAIN[1]), "predictions"),
    (lambda r: r["predictions"].update({cases.MAIN[1]: []}), "predictions"),
    (lambda r: r["predictions"].update({cases.MAIN[1]: SCRIPT}), "predictions"),
    (lambda r: r.update(predictions=[]), "predictions"),
    (lambda r: r.update(predictions=SCRIPT), "predictions"),
    (lambda r: r.update(predictions={}), "predictions"),  # empty predictions under a non-no-video text
])
def test_unusable_results_are_422_with_the_bad_path(client, monkeypatch, mutate, where):
    _no_model(monkeypatch)
    result = _stored()
    mutate(result)
    response = client.post("/inference/render-report", content=json.dumps({"result": result}), headers=JSON_HEADERS)
    body = response.json()
    assert response.status_code == 422, body
    assert body["success"] is False and body["errorCode"] == "INVALID_RESULT" and where in body["message"]
    assert "htmlBase64" not in body.get("data", {}) and SCRIPT not in body["message"]


def test_every_stored_string_and_flag_is_checked(service):
    """Changing any single string, boolean or null of a stored result makes it unrenderable, and changing any
    number either does too or yields the report of the changed prediction (never a mix)."""
    def leaves(node, path=()):
        if isinstance(node, dict):
            for key, value in node.items():
                yield from leaves(value, path + (key,))
        else:
            yield path, node

    def put(root, path, value):
        for key in path[:-1]:
            root = root[key]
        root[path[-1]] = value

    for preds in [cases.EXAMPLE] + cases.edge_preds()[:6]:
        stored = _live_json(service, preds)
        for path, value in leaves(stored):
            changed = copy.deepcopy(stored)
            if isinstance(value, bool):
                put(changed, path, not value)
            elif isinstance(value, str):
                put(changed, path, value + " <b>x</b>")
            elif value is None:
                put(changed, path, "x")
            else:
                continue
            with pytest.raises(ResultError):
                CustomPredictionService.report_html(changed)


def test_a_tampered_no_video_result_is_refused(client, monkeypatch):
    _no_model(monkeypatch)
    for mutate in (lambda r: r.update(diagnosis=SCRIPT),
                   lambda r: r["modelRecommendations"].update(en=SCRIPT),
                   lambda r: r["modelRecommendations"].update(presentable=True),
                   lambda r: r["modelRecommendations"].update(presentable=0)):
        result = copy.deepcopy(NO_VIDEO_RESULT)
        mutate(result)
        assert client.post("/inference/render-report", json={"result": result}).status_code == 422


def test_malformed_bodies_are_rejected(client, monkeypatch):
    _no_model(monkeypatch)
    good = json.dumps(_stored()).encode()
    deep = b'{"result": ' + b"[" * 20000 + b"]" * 20000 + b"}"
    leaf = b'"' + cases.MAIN[-1].encode() + b'": '
    assert leaf in good and b'"threshold": ' in good
    for raw in (b"", b"not json", b'{"result": NaN}', b"[]", b"null", b'"result"', b'{"result": {}}', b'{"result": []}',
                b'{"result": {"predictions": []}}', b'{"result": ' + good + b', "extra": 1}', b'{"Result": ' + good + b"}",
                b'{"result": ' + good.replace(leaf, leaf + b'NaN, "x": ', 1) + b"}",
                b'{"result": ' + good.replace(leaf, leaf + b'-Infinity, "x": ', 1) + b"}",
                b'{"result": ' + good.replace(b'"threshold": ', b'"threshold": 1e999, "x": ') + b"}",
                b'{"result": {"data": {}}}', b"\xff\xfe", deep):
        response = client.post("/inference/render-report", content=raw, headers=JSON_HEADERS)
        assert response.status_code == 422, raw[:80]
        assert response.json()["errorCode"] == "INVALID_RESULT"


def test_oversize_body_is_413(client, monkeypatch):
    _no_model(monkeypatch)
    import main
    assert main.RENDER_REPORT_MAX_BYTES == 64 * 1024
    big = b'{"result": "' + b"a" * main.RENDER_REPORT_MAX_BYTES + b'"}'
    response = client.post("/inference/render-report", content=big, headers=JSON_HEADERS)
    assert response.status_code == 413 and response.json()["errorCode"] == "PAYLOAD_TOO_LARGE"
    padded = json.dumps({"result": _stored()}).encode()
    padded += b" " * (main.RENDER_REPORT_MAX_BYTES - len(padded))  # exactly at the limit
    assert client.post("/inference/render-report", content=padded, headers=JSON_HEADERS).status_code == 200
    assert client.post("/inference/render-report", content=padded + b" ", headers=JSON_HEADERS).status_code == 413


def test_get_is_not_allowed(client):
    assert client.get("/inference/render-report").status_code == 405


# -- no model, no activity, advertised -----------------------------------------------------------------------------

def test_render_report_does_not_load_the_model_or_need_class_mapping_from_load(client, monkeypatch):
    _no_model(monkeypatch)
    monkeypatch.delattr(CustomPredictionService, "_class_mapping", raising=False)  # as in a container that never predicted
    import main
    before = main.last_request_time
    response = client.post("/inference/render-report", json={"result": _stored()})
    assert response.status_code == 200 and _html(response).startswith("<!DOCTYPE html>")
    assert CustomPredictionService.is_initialized is False and CustomPredictionService.models == {}
    assert main.last_request_time == before  # the idle timer is left alone
    assert main.inference_lock._value == 1  # the inference semaphore was not taken
    runtime = client.get("/inference/model/runtime").json()["data"]
    assert runtime["state"] == "UNLOADED"


def test_render_report_is_not_model_activity():
    from utils.model_lifecycle import records_model_activity
    assert not records_model_activity("/inference/render-report")


def test_model_info_advertises_render_report_and_keeps_the_version(client):
    data = client.get("/inference/model-info").json()["data"]
    assert data["capabilities"] == {"renderReport": True}
    assert data["version"] == cases.VERSION and data["build"] == "render-report-1"
    manifest = json.loads((ROOT / "data" / "model_info.json").read_text(encoding="utf-8"))
    assert manifest["version"] == cases.VERSION
    dockerfile = (ROOT / "Dockerfile").read_text(encoding="utf-8")
    assert f'ai.heartwise.pacs.build="{manifest["build"]}"' in dockerfile
    for name in ("Dockerfile", "Dockerfile.local"):
        if (ROOT / name).exists():
            assert "COPY report_input.py ." in (ROOT / name).read_text(encoding="utf-8")


def test_openapi_documents_render_report(service):
    openapi = json.loads((ROOT / "docs" / "openapi.json").read_text(encoding="utf-8"))
    operation = openapi["paths"]["/inference/render-report"]["post"]
    assert set(operation["responses"]) == {"200", "413", "422"}
    example = operation["requestBody"]["content"]["application/json"]["example"]["result"]
    assert example == _live_json(service, cases.EXAMPLE)
    assert CustomPredictionService.report_html(example) == cases.as_stored(_live_html(service, cases.EXAMPLE), cases.EXAMPLE)
    assert {"RenderReportRequest", "RenderReportResponse"} <= set(openapi["components"]["schemas"])


def test_non_finite_python_floats_are_rejected():
    for bad in (math.nan, math.inf, -math.inf):
        result = _stored()
        _set_main(result, bad)
        with pytest.raises(ResultError, match=MAIN_PATH.replace(".", r"\.")):
            CustomPredictionService.report_html(result)


# -- the four component cells --------------------------------------------------------------------------------------

def _cells(html: str) -> list[str]:
    return [m.group(2) for m in cases.COMPONENT_CELL.finditer(html)]


def test_stored_report_prints_component_probabilities_with_three_decimals(service):
    preds = cases.preds_of({"lad": 2.6, "rca": 1.0, "lcx": 0.3},
                           {"lad": [0.2451, 0.5, 0.0004, 0.99961], "rca": [0.1] * 4, "lcx": [0.2] * 4})
    live, stored = _live_html(service, preds), CustomPredictionService.report_html(_live_json(service, preds))
    assert _cells(live) == ["0.25", "0.50", "0.00", "1.00"]  # the live report is unchanged: two decimals
    assert _cells(stored) == ["0.245", "0.500", "0.000", "1.000"]
    assert stored.count(cases.NOTE) == 1 and cases.NOTE not in live
    assert "shown as stored, to three decimals" in stored
    # the note sits between the component table and the per-artery table
    assert stored.index("J-CTO component") < stored.index(cases.NOTE) < stored.index("<th>Artery</th>")
    # nothing else differs
    assert stored == cases.as_stored(live, preds) and _same(stored, live)
    assert len(stored) == len(live) + len(cases.NOTE) + 4


def test_values_on_either_side_of_a_two_decimal_tie_render_the_same_from_storage(service):
    """Raw 0.1349 and 0.1351 are both stored as 0.135; the live report shows 0.13 and 0.14."""
    def preds(probability):
        return cases.preds_of({"lad": 1.2, "rca": 0.4, "lcx": 0.1},
                              {"lad": [probability, 0.6, 0.3, 0.2], "rca": [0.1] * 4, "lcx": [0.2] * 4})
    below, above = preds(0.1349), preds(0.1351)
    assert _live_json(service, below) == _live_json(service, above)
    assert _cells(_live_html(service, below))[0] == "0.13" and _cells(_live_html(service, above))[0] == "0.14"
    from_below = CustomPredictionService.report_html(_live_json(service, below))
    from_above = CustomPredictionService.report_html(_live_json(service, above))
    assert from_below == from_above and _cells(from_below)[0] == "0.135"
    assert _same(from_below, _live_html(service, below)) and _same(from_above, _live_html(service, above))


def test_live_html_mode_report_has_no_note_and_two_decimals(client, monkeypatch):
    _predicting(monkeypatch, postprocessed=cases.EXAMPLE)
    html = _b64_html(client.post("/inference/predict", json={"outputMode": "HTML"}).json()["data"]["htmlBase64"])
    assert cases.NOTE not in html and "as stored" not in html
    assert all(len(cell) == 4 for cell in _cells(html)) and len(_cells(html)) == 4


def test_the_selected_artery_must_have_the_highest_stored_score(service):
    stored = _live_json(service, cases.EXAMPLE)
    stored["predictions"]["perArtery"]["RCA"]["jctoScore"] = 3.9
    stored["predictions"]["perArtery"]["RCA"]["jctoScoreInteger"] = 4
    with pytest.raises(ResultError, match="predictions.ctoArtery"):
        CustomPredictionService.report_html(stored)


def test_lcx_result_keeps_the_warning(service):
    preds = cases.preds_of({"lad": 0.2, "rca": 1.7, "lcx": 2.6}, {a: [0.9, 0.8, 0.7, 0.6] for a in cases.ARTERIES})
    stored = _live_json(service, preds)
    assert stored["predictions"]["ctoArtery"] == "LCx" and "ctoArteryWarning" in stored["predictions"]
    html = CustomPredictionService.report_html(stored)
    assert "LCx predictions are unreliable" in html and html == cases.as_stored(_live_html(service, preds), preds)
