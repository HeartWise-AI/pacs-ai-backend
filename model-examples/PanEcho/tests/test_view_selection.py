"""1.2.0: the view classifier ships in the image, PanEcho keeps standard TTE views only, and says
why when it does not run (no standard view, TEE, no clip). Synthetic headers only (no PHI)."""

import asyncio
import hashlib
import json
import re
import sys
from pathlib import Path

import pytest
from pydicom.dataset import Dataset
from pydicom.multival import MultiValue
from pydicom.uid import generate_uid

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

import download_model

logic = pytest.importorskip("logic")

WEIGHTS = Path("/app/weights/best_model_pretrained_echoprime_updated.pth")


def clip(series=None, number=1, **tags) -> Dataset:
    ds = Dataset()
    ds.SeriesInstanceUID = series or generate_uid()
    ds.SOPInstanceUID = generate_uid()
    ds.SeriesNumber = 1
    ds.InstanceNumber = number
    for key, value in tags.items():
        setattr(ds, key, value)
    return ds


def service_with_views(monkeypatch, views):
    """A service whose classifier returns ``views[i]`` for the i-th clip it is given."""
    svc = logic.CustomPredictionService.__new__(logic.CustomPredictionService)
    calls = {"classified": 0}

    def fake_metadata(self, dicoms):
        calls["classified"] += 1
        return {
            logic._clip_key(d): {"predicted_class": v, "confidence": 0.9, "status": "success"}
            for d, v in zip(dicoms, views)
        }

    monkeypatch.setattr(logic.CustomPredictionService, "_generate_view_classifier_metadata", fake_metadata)
    return svc, calls


# --- provenance -------------------------------------------------------------------------------

def test_view_classifier_is_pinned_by_digest_and_sha256_everywhere():
    dockerfile = (ROOT / "Dockerfile").read_text(encoding="utf-8")
    assert f"FROM {download_model.VIEW_CLASSIFIER_SOURCE_IMAGE} AS view-classifier" in dockerfile
    assert re.search(r"@sha256:[0-9a-f]{64} AS view-classifier", dockerfile)
    assert (
        f"COPY --from=view-classifier {download_model.VIEW_CLASSIFIER_SOURCE_PATH} "
        f"{download_model.VIEW_CLASSIFIER_WEIGHTS_PATH}" in dockerfile
    )
    assert f"--verify-view-classifier {download_model.VIEW_CLASSIFIER_WEIGHTS_PATH}" in dockerfile
    assert download_model.VIEW_CLASSIFIER_SHA256 == logic.VIEW_CLASSIFIER_SHA256
    assert f'view-classifier.sha256="{download_model.VIEW_CLASSIFIER_SHA256}"' in dockerfile

    config = json.loads((ROOT / "config.json").read_text(encoding="utf-8"))["models"]["pan_echo"]
    assert config["view_classifier_model_path"] == "/app/" + download_model.VIEW_CLASSIFIER_WEIGHTS_PATH
    assert config["view_classifier_required"] is True


def test_view_classifier_verification_rejects_other_bytes(tmp_path, capsys, monkeypatch):
    fake = tmp_path / "classifier.pth"
    fake.write_bytes(b"not the classifier")
    monkeypatch.setattr(sys, "argv", ["download_model.py", "--verify-view-classifier", str(fake)])
    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        download_model.main()


@pytest.mark.skipif(not WEIGHTS.is_file(), reason="view-classifier weights are only inside the image")
def test_packaged_classifier_is_the_pinned_checkpoint_and_gives_pinned_outputs():
    import torch

    from models.echo_prime_view_classifier import EchoPrimeViewClassifier

    digest = hashlib.sha256(WEIGHTS.read_bytes()).hexdigest()
    assert digest == download_model.VIEW_CLASSIFIER_SHA256

    torch.manual_seed(0)
    video = torch.randn(1, 3, 16, 224, 224)
    model = EchoPrimeViewClassifier(str(WEIGHTS)).eval().to("cpu")
    with torch.no_grad():
        logits = model(video)[0]
    mapping = json.loads((ROOT / "models/view_classifier_class_mapping.json").read_text())
    assert logits.shape == (20,)
    assert len(mapping) == 20
    assert mapping[str(int(logits.argmax()))] == PINNED_CLASS
    assert logits.max().item() == pytest.approx(PINNED_TOP_LOGIT, abs=1e-2)


PINNED_CLASS = "__PIN__"
PINNED_TOP_LOGIT = 0.0


def test_model_refuses_to_load_without_a_required_classifier(monkeypatch, tmp_path):
    from utils.http_utils import Config

    service_cls = logic.CustomPredictionService
    monkeypatch.setattr(service_cls, "is_initialized", False)
    monkeypatch.setattr(service_cls, "models", {})

    class Stub:
        def eval(self):
            return self

        def to(self, device):
            return self

    monkeypatch.setattr(logic, "PanEcho", lambda **kwargs: Stub())
    config = Config(
        modelDirectory="models",
        models={"pan_echo": {
            "model_path": "x", "task_path": "x", "pretrained": True, "image_encoder_only": False,
            "backbone_only": False, "tasks": "all", "activations": False, "clip_len": 16,
            "normalization": "imagenet",
            "view_classifier_model_path": str(tmp_path / "missing.pth"),
            "view_classifier_class_mapping_path": str(ROOT / "models/view_classifier_class_mapping.json"),
            "view_classifier_required": True,
        }},
    )
    with pytest.raises(RuntimeError, match="view classifier is required"):
        service_cls.__new__(service_cls).load_model(config)
    assert service_cls.is_initialized is False


# --- selection --------------------------------------------------------------------------------

def test_standard_views_are_kept_and_the_rest_reported(monkeypatch):
    clips = [clip(number=n) for n in range(1, 7)]
    views = ["A4C", "SUBCOSTAL", "PLAX_DEEP", "A3C", "PSAX_AV", "A2C_ZOOM"]
    svc, _ = service_with_views(monkeypatch, views)

    kept, selection = svc._prepare_dicoms_for_inference(clips, None)

    assert kept == [clips[0], clips[2], clips[4], clips[5]]
    assert selection["status"] == "used" and selection["skipReason"] is None
    assert selection["totalClips"] == 6 and selection["clipsUsed"] == 4
    assert selection["viewCounts"] == {"A2C_ZOOM": 1, "A3C": 1, "A4C": 1, "PLAX_DEEP": 1, "PSAX_AV": 1, "SUBCOSTAL": 1}
    assert selection["usedViewCounts"] == {"A2C_ZOOM": 1, "A4C": 1, "PLAX_DEEP": 1, "PSAX_AV": 1}
    assert selection["classifier"]["sha256"] == download_model.VIEW_CLASSIFIER_SHA256
    assert [c["used"] for c in selection["clips"]] == [True, False, True, False, True, True]
    assert selection["clips"][1] == {"seriesNumber": 1, "instanceNumber": 2, "view": "SUBCOSTAL", "confidence": 0.9, "used": False}
    assert "UID" not in json.dumps(selection)


def test_zero_standard_views_is_an_explicit_skip_not_all_clips(monkeypatch):
    clips = [clip(number=n) for n in range(1, 4)]
    svc, _ = service_with_views(monkeypatch, ["OTHER", "SUBCOSTAL", "SUPRASTERNAL"])

    kept, selection = svc._prepare_dicoms_for_inference(clips, None)

    assert kept == []
    assert selection["status"] == "skipped"
    assert selection["skipReason"] == logic.SKIP_NO_STANDARD_VIEWS == "NO_STANDARD_TTE_VIEWS"
    assert selection["clipsUsed"] == 0 and selection["totalClips"] == 3
    assert selection["viewCounts"] == {"OTHER": 1, "SUBCOSTAL": 1, "SUPRASTERNAL": 1}


def test_unclassifiable_clips_count_as_off_view(monkeypatch):
    clips = [clip(number=1), clip(number=2)]
    svc = logic.CustomPredictionService.__new__(logic.CustomPredictionService)
    monkeypatch.setattr(
        logic.CustomPredictionService, "_generate_view_classifier_metadata",
        lambda self, dicoms: {logic._clip_key(d): {"predicted_class": None, "status": "failed to detect motion"} for d in dicoms},
    )
    kept, selection = svc._prepare_dicoms_for_inference(clips, None)
    assert kept == [] and selection["skipReason"] == logic.SKIP_NO_STANDARD_VIEWS
    assert selection["viewCounts"] == {"unclassified: failed to detect motion": 2}


@pytest.mark.parametrize("tags", [
    {"StudyDescription": "ETO PERIOPERATOIRE"},
    {"StudyDescription": "ECHO DOPPLER CARDIAQUE TRANS-OESOPHAGIENNE"},
    {"SeriesDescription": "TEE intraop"},
    {"ProtocolName": "Transesophageal"},
    {"TransducerData": MultiValue(str, ["X8_2t", "56749", ""])},
    {"TransducerData": "X7-2t"},
])
def test_tee_studies_are_skipped_before_classification(monkeypatch, tags):
    clips = [clip(number=1, StudyDescription="ECHO CARDIAQUE AVEC DOPPLER"), clip(number=2, **tags)]
    svc, calls = service_with_views(monkeypatch, ["A4C", "A4C"])

    kept, selection = svc._prepare_dicoms_for_inference(clips, None)

    assert kept == [] and selection["skipReason"] == logic.SKIP_TEE
    assert selection["teeClips"] == 1
    assert calls["classified"] == 0


@pytest.mark.parametrize("tags", [
    {"StudyDescription": "ECHO CARDIAQUE AVEC DOPPLER"},
    {"StudyDescription": "ECHO CARDIAQUE AVEC DOPPLER CONGÉNITAUX"},
    {"StudyDescription": "ETT"},
    {"StudyDescription": "Echo stress (TTE)"},
    {"TransducerData": MultiValue(str, ["M5Sc-D", "", ""])},
    {"TransducerData": "X5-1"},
])
def test_transthoracic_headers_are_not_mistaken_for_tee(tags):
    assert not logic.is_tee_clip(clip(**tags))


def test_no_video_clip_is_an_explicit_skip():
    svc = logic.CustomPredictionService.__new__(logic.CustomPredictionService)
    kept, selection = svc._prepare_dicoms_for_inference([], None)
    assert kept == [] and selection["skipReason"] == logic.SKIP_NO_VIDEO_CLIPS


def test_without_a_classifier_the_result_says_it_is_unfiltered(monkeypatch):
    clips = [clip(number=1), clip(number=2)]
    svc = logic.CustomPredictionService.__new__(logic.CustomPredictionService)
    monkeypatch.setattr(logic.CustomPredictionService, "_generate_view_classifier_metadata", lambda self, d: None)
    kept, selection = svc._prepare_dicoms_for_inference(clips, None)
    assert kept == clips
    assert selection["status"] == "unfiltered" and selection["clipsUsed"] == 2


# --- responses --------------------------------------------------------------------------------

def test_json_skip_returns_reason_and_never_runs_panecho(monkeypatch):
    clips = [clip(number=1), clip(number=2)]
    svc, _ = service_with_views(monkeypatch, ["OTHER", "SUBCOSTAL"])
    monkeypatch.setattr(logic.CustomPredictionService, "_extract_dicoms", lambda self, request: clips)

    def must_not_run(self, **kwargs):
        raise AssertionError("PanEcho ran on off-view clips")

    monkeypatch.setattr(logic.CustomPredictionService, "_run_inference", must_not_run)

    class Request:
        additionalMetadata = None

    response = asyncio.run(svc._json_output(Request()))

    from utils.http_utils import JsonPredictionResponse

    body = JsonPredictionResponse(**response).model_dump()
    assert body["predictions"] == {}
    assert body["viewSelection"]["skipReason"] == logic.SKIP_NO_STANDARD_VIEWS
    assert "none of the 2 clips" in body["modelRecommendations"]["en"]
    assert "aucune des 2 séquences" in body["modelRecommendations"]["fr"]


def test_json_result_carries_the_view_selection(monkeypatch):
    clips = [clip(number=1), clip(number=2)]
    svc, _ = service_with_views(monkeypatch, ["A4C", "OTHER"])
    monkeypatch.setattr(logic.CustomPredictionService, "_extract_dicoms", lambda self, request: clips)
    seen = {}

    def fake_inference(self, dicoms):
        seen["dicoms"] = dicoms
        return {}

    monkeypatch.setattr(logic.CustomPredictionService, "_run_inference", fake_inference)
    monkeypatch.setattr(logic.CustomPredictionService, "postprocess_predictions", lambda self, p: ({}, {"EF": 55.0}))
    monkeypatch.setattr(logic.CustomPredictionService, "_get_recommendations", lambda self, d, lang: "ok")

    class Request:
        additionalMetadata = None

    response = asyncio.run(svc._json_output(Request()))
    assert seen["dicoms"] == [clips[0]]
    assert response["viewSelection"]["clipsUsed"] == 1
    assert response["viewSelection"]["usedViewCounts"] == {"A4C": 1}


def test_html_skip_explains_the_reason(monkeypatch):
    import base64

    clips = [clip(number=1, StudyDescription="ETO PERIOPERATOIRE")]
    svc = logic.CustomPredictionService.__new__(logic.CustomPredictionService)
    monkeypatch.setattr(logic.CustomPredictionService, "_extract_dicoms", lambda self, request: clips)

    class Request:
        additionalMetadata = None

    response = asyncio.run(svc._html_output(Request()))
    page = base64.b64decode(response["htmlBase64"]).decode()
    assert "transesophageal" in page and "TEE_NOT_SUPPORTED" in page
