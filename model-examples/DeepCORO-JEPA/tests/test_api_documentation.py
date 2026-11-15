"""Keep the served model documentation aligned with its registered metadata."""

import base64
import json
import sys
from pathlib import Path


MODEL_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(MODEL_ROOT))

# thresholds.json of the served weights (Hugging Face revision v15.1, weights 23b8255b...): the file is fetched at
# build, so the values the example must carry are written here.
V15_THRESHOLDS = {"prox_rca": 0.146, "mid_rca": 0.1431, "dist_rca": 0.0764, "pda": 0.0282, "posterolateral": 0.0278,
                  "left_main": 0.0632, "prox_lad": 0.1479, "mid_lad": 0.1738, "dist_lad": 0.0376, "D1": 0.1106,
                  "D2": 0.0477, "prox_lcx": 0.1121, "mid_lcx": 0.0522, "dist_lcx": 0.0082, "om1": 0.0688, "om2": 0.0588,
                  "bx": 0.0262, "lvp": 0.0001}


def test_openapi_describes_registered_model():
    document = json.loads((MODEL_ROOT / "docs/openapi.json").read_text(encoding="utf-8"))
    info = json.loads((MODEL_ROOT / "data/model_info.json").read_text(encoding="utf-8"))

    for endpoint, filename in (("model-info", "model_info"), ("model-facts", "model_facts")):
        response = document["paths"][f"/inference/{endpoint}"]["get"]["responses"]["200"]
        assert response["content"]["application/json"]["example"]["data"] == json.loads(
            (MODEL_ROOT / f"data/{filename}.json").read_text(encoding="utf-8")
        )

    schemas = document["components"]["schemas"]
    assert schemas["PredictRequest"]["properties"]["outputMode"]["enum"] == info[
        "supportedOutputModes"
    ]
    assert "200" in document["paths"]["/inference/predict"]["post"]["responses"]
    assert document["info"]["version"] == info["version"] == "1.5.0"
    rendered = document["paths"]["/inference/render-report"]["post"]["responses"]["200"]["content"]["application/json"]["example"]
    assert rendered["data"]["modelVersion"] == info["version"]


def test_predict_examples_are_the_documented_container_response():
    document = json.loads((MODEL_ROOT / "docs/openapi.json").read_text(encoding="utf-8"))
    examples = document["paths"]["/inference/predict"]["post"]["responses"]["200"]["content"][
        "application/json"
    ]["examples"]
    response = json.loads(
        (MODEL_ROOT / "docs/examples/predict_response_json.json").read_text(encoding="utf-8")
    )
    assert examples["JSON"]["value"] == response
    report = (MODEL_ROOT / "docs/examples/predict_report.html").read_text(encoding="utf-8")
    assert base64.b64decode(examples["HTML"]["value"]["data"]["htmlBase64"]).decode("utf-8") == report.rstrip("\n")

    config = json.loads((MODEL_ROOT / "models/config.json").read_text(encoding="utf-8"))["Thresholds"]
    predictions = response["data"]["predictions"]
    assert predictions["lad_physiology"]["operating_point"] == config["pressure_operating_point"]
    fixed = ", ".join(f"{k}: fixed {v:.2f}" for k, v in config["lesion_threshold_overrides"].items())
    assert predictions["lesion_thresholds"]["operating_point"] == f'{config["lesion_operating_point"]} ({fixed})'
    assert len(predictions["lesion_thresholds"]["operating_point"]) <= 64
    assert predictions["lesion_thresholds"]["thrombus"] == config["lesion_threshold_overrides"]["thrombus"] == 0.4
    # every yes/no value can be recomputed from the returned probability and threshold
    physiology = predictions["lad_physiology"]
    for k in ("ffr", "ifr"):
        assert physiology[f"{k}_abnormal"] == (physiology[f"{k}_abnormal_prob"] > physiology[f"{k}_abnormal_threshold"])
    assert predictions["unsupported_segments"] == ["lvp"]
    for key, segment in predictions["segments"].items():
        if key in predictions["unsupported_segments"]:  # no training target in v15: never flagged, never displayed
            assert segment["unsupported"] is True and "display_pct" not in segment and "display_category" not in segment
            assert not any(segment[name] for name in ("significant", "calcif", "thrombus", "cto"))
            continue
        assert "unsupported" not in segment
        assert segment["significant"] == (segment["prob_significant"] > segment["prob_threshold"])
        for name in ("calcif", "thrombus", "cto"):
            assert segment[name] == (segment[f"{name}_prob"] > predictions["lesion_thresholds"][name])
    dominance = predictions["model_dominance"]
    assert dominance["dominance_left_pred"] == (dominance["dominance_left_prob"] > dominance["dominance_left_threshold"])
    assert set(predictions["study_lesions"]) == {"study_calcif_prob", "study_thrombus_prob", "study_cto_prob"}
    assert all(isinstance(p, float) and 0 <= p <= 1 for p in predictions["study_lesions"].values())  # no yes/no call


def test_predict_example_is_a_v15_result_with_the_v15_thresholds_and_display_mapping():
    import display_calibration
    from logic import CustomPredictionService
    from report_input import predictions_of

    response = json.loads((MODEL_ROOT / "docs/examples/predict_response_json.json").read_text(encoding="utf-8"))
    predictions = response["data"]["predictions"]
    facts = json.loads((MODEL_ROOT / "data/model_facts.json").read_text(encoding="utf-8"))["en"]["Summary"]
    assert predictions["model_version"] == facts["Model_version"] == "v15"
    assert {g: v["prob_threshold"] for g, v in predictions["segments"].items()} == V15_THRESHOLDS
    assert predictions["lesion_thresholds"] == {"operating_point": "specificity_99 (thrombus: fixed 0.40)",
                                                "calcif": 0.3631, "thrombus": 0.4, "cto": 0.2134}
    physiology = predictions["lad_physiology"]
    assert (physiology["ffr_abnormal_threshold"], physiology["ifr_abnormal_threshold"]) == (0.5936, 0.6939)
    assert predictions["model_dominance"]["dominance_left_threshold"] == 0.0383
    assert "coronary_dominance" not in predictions  # no caller dominance in the example: the schema is right dominant
    for key, segment in predictions["segments"].items():
        if key == "lvp":
            continue
        assert segment["display_pct"] == display_calibration.display_pct(segment, "v15")
        assert segment["display_category"] == display_calibration.display_category(segment, "v15")
    # the report and the diagnosis of the example are what this code writes for these predictions
    stored = predictions_of(response["data"])
    report = (MODEL_ROOT / "docs/examples/predict_report.html").read_text(encoding="utf-8")
    assert CustomPredictionService.report_html(stored) == report.rstrip("\n")
    assert "DeepCORO-JEPA v15-cal1</h1>" in report and "Right dominant (dominance not provided)" in report
    assert "Posterior left ventricular" not in report and "Posterior left ventricular" not in response["data"]["diagnosis"]
    service = CustomPredictionService.__new__(CustomPredictionService)
    assert response["data"]["diagnosis"] == service.diagnosis_text(predictions)
    assert response["data"]["modelRecommendations"] == CustomPredictionService.recommendations(predictions)


def test_render_report_example_is_a_stored_v13_result_drawn_as_v13():
    from logic import CustomPredictionService
    from report_input import predictions_of

    document = json.loads((MODEL_ROOT / "docs/openapi.json").read_text(encoding="utf-8"))
    post = document["paths"]["/inference/render-report"]["post"]
    stored = predictions_of(post["requestBody"]["content"]["application/json"]["example"]["result"])
    assert "model_version" not in stored  # stored before 1.5.0
    answer = post["responses"]["200"]["content"]["application/json"]["example"]["data"]
    assert answer["reportModelVersion"] == CustomPredictionService.report_model_version(stored) == "v13-cal1"
    html = CustomPredictionService.report_html(stored)
    assert "DeepCORO-JEPA v13-cal1</h1>" in html and "validation split of model v13" in html and "v15-cal1" not in html
