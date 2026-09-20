"""Keep the served model documentation aligned with its registered metadata."""
import json
from pathlib import Path

MODEL_ROOT = Path(__file__).resolve().parents[1]


def test_openapi_describes_registered_model():
    document = json.loads((MODEL_ROOT / 'docs/openapi.json').read_text())
    info = json.loads((MODEL_ROOT / 'data/model_info.json').read_text())
    for endpoint, filename in [('model-info', 'model_info'), ('model-facts', 'model_facts')]:
        response = document['paths'][f'/inference/{endpoint}']['get']['responses']['200']
        assert response['content']['application/json']['example']['data'] == json.loads(
            (MODEL_ROOT / f'data/{filename}.json').read_text()
        )
    schemas = document['components']['schemas']
    assert schemas['PredictRequest']['properties']['outputMode']['enum'] == info['supportedOutputModes']
    assert '200' in document['paths']['/inference/predict']['post']['responses']
    for obsolete in ('CathEF', 'LVEF', 'X3D'):
        assert obsolete not in json.dumps(document)
