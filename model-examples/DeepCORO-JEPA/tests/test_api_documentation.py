"""Keep the served model documentation aligned with its registered metadata."""

import base64
import json
from pathlib import Path


MODEL_ROOT = Path(__file__).resolve().parents[1]


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
    assert predictions["lesion_thresholds"]["operating_point"] == config["lesion_operating_point"]
    # every yes/no value can be recomputed from the returned probability and threshold
    physiology = predictions["lad_physiology"]
    for k in ("ffr", "ifr"):
        assert physiology[f"{k}_abnormal"] == (physiology[f"{k}_abnormal_prob"] > physiology[f"{k}_abnormal_threshold"])
    for segment in predictions["segments"].values():
        assert segment["significant"] == (segment["prob_significant"] > segment["prob_threshold"])
        for name in ("calcif", "thrombus", "cto"):
            assert segment[name] == (segment[f"{name}_prob"] > predictions["lesion_thresholds"][name])
