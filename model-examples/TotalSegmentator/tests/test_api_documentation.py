"""Keep the served TotalSegmentator documentation aligned with registration metadata."""

import json
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def test_openapi_examples_match_registered_metadata():
    document = json.loads((ROOT / "docs/openapi.json").read_text(encoding="utf-8"))

    for endpoint, filename in (("model-info", "model_info"), ("model-facts", "model_facts")):
        response = document["paths"][f"/inference/{endpoint}"]["get"]["responses"]["200"]
        actual = response["content"]["application/json"]["example"]["data"]
        expected = json.loads((ROOT / f"data/{filename}.json").read_text(encoding="utf-8"))
        assert actual == expected
