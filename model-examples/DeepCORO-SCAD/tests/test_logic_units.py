"""Inference contracts exercised with real tensors, without model weights or a GPU."""

import asyncio
import base64
import json
import sys
from pathlib import Path
from types import SimpleNamespace

import numpy as np
import pydicom
import pytest
import torch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

import logic
from logic import CustomPredictionService, VideoMILWrapper
from models.multi_instance_linear_probing import MultiInstanceLinearProbing


@pytest.fixture
def service(monkeypatch):
    monkeypatch.setattr(CustomPredictionService, "_class_mapping", json.loads(
        (ROOT / "models/class_mapping.json").read_text()), raising=False)
    monkeypatch.setattr(CustomPredictionService, "model_config", json.loads(
        (ROOT / "models/config.json").read_text()), raising=False)
    monkeypatch.setattr(CustomPredictionService, "models", {})
    monkeypatch.setattr(CustomPredictionService, "is_initialized", False)
    monkeypatch.setattr(torch.cuda, "is_available", lambda: False)
    return CustomPredictionService.__new__(CustomPredictionService)


def test_load_model_ignores_documentation_config_keys(service, monkeypatch):
    monkeypatch.chdir(ROOT)
    monkeypatch.setattr(logic, "VideoEncoder", lambda **kwargs: torch.nn.Identity())
    monkeypatch.setattr(torch, "load", lambda *args, **kwargs: {"linear_probing": {}})
    monkeypatch.setattr(VideoMILWrapper, "load_state_dict", lambda self, weights: None)
    service.load_model(None)
    assert service.is_initialized
    assert set(service.models["video_mil_wrapper"].mil_model.heads) == set(service._class_mapping)


def test_config_matches_training_release():
    config = json.loads((ROOT / "models/config.json").read_text())
    mapping = json.loads((ROOT / "models/class_mapping.json").read_text())
    assert config["VideoMILWrapper"] == {"num_videos": 4, "stride": 1, "resize": 224}
    assert config["MultiInstanceLinearProbing"]["pooling_mode"] == "mean"
    assert config["VideoEncoder"]["freeze_ratio"] == 0.8
    assert config["dataset_mean"] == [98.33860778808594] * 3
    assert config["dataset_std"] == [33.891441345214844] * 3
    assert {head: spec["task"] for head, spec in mapping.items()} == {
        "scad": "binary_classification",
        "cad": "binary_classification",
        "other": "binary_classification",
        "stenosis": "regression",
    }
    assert mapping["scad"]["threshold"] == 0.0524009
    manifest = json.loads((ROOT / "data/model_info.json").read_text())
    assert manifest["dicomUploadMin"] == 1
    assert manifest["dicomUploadMax"] == config["VideoMILWrapper"]["num_videos"]


def _outputs(scad_logit: float) -> dict[str, torch.Tensor]:
    return {
        "scad": torch.tensor([[scad_logit]]),
        "cad": torch.tensor([[0.3]]),
        "other": torch.tensor([[-0.3]]),
        "stenosis": torch.tensor([[1.7]]),
    }


def test_heads_are_postprocessed_and_only_scad_is_reported(service):
    preds = service._postprocess(_outputs(0.0))
    assert set(preds) == set(service._class_mapping)
    assert preds["scad"] == pytest.approx(0.5)
    assert preds["stenosis"] == 1.0  # clamped regression head

    formatted = service._format_predictions(preds)
    assert set(formatted) == {"scad", "thresholdNote"}
    assert formatted["scad"]["threshold"] == pytest.approx(0.0524009)
    assert formatted["scad"]["scadSuspected"] is True
    assert "SCAD suspected" in service._diagnosis_text(preds)

    recommendations = service._recommendations(preds)
    assert "SCAD suspected" in recommendations["en"]
    assert "DCS suspectée" in recommendations["fr"]

    html = service._render_html(preds, recommendations)
    assert "DeepCORO-SCAD" in html
    assert "SCAD suspected" in html and "DCS suspectée" in html
    assert "Research use only" in html
    assert "Usage en recherche seulement" in html
    assert "does not localise the lesion" in html
    for auxiliary in ("cad", "other", "stenosis"):
        assert service._class_mapping[auxiliary]["name"] not in html


def test_threshold_boundary_flags(service):
    threshold = 0.0524009
    logit = float(np.log(threshold / (1 - threshold)))
    below = service._postprocess(_outputs(logit - 1e-3))
    above = service._postprocess(_outputs(logit + 1e-3))
    assert service._format_predictions(below)["scad"]["scadSuspected"] is False
    assert service._format_predictions(above)["scad"]["scadSuspected"] is True
    assert "SCAD not suspected" in service._diagnosis_text(below)
    assert "DCS non suspectée" in service._render_html(below, service._recommendations(below))


def test_unknown_head_is_rejected(service):
    with pytest.raises(ValueError, match="Unknown checkpoint head"):
        service._postprocess({"unknown_head": torch.tensor([[0.0]])})


def test_metadata_ordering_interleaves_left_and_right_views(service):
    def dicom(uid, time):
        item = pydicom.Dataset()
        item.SeriesInstanceUID = uid
        item.SeriesTime = time
        return item

    left = {"1.1": "090000", "1.2": "090100", "1.3": "090200"}
    right = {"2.1": "091000", "2.2": "091100"}
    dicoms = [dicom(uid, time) for uid, time in {**left, **right}.items()]
    metadata = {uid: {"status": "diagnostic",
                      "main_structure": "Left Coronary" if uid in left else "Right Coronary"}
                for uid in {**left, **right}}
    ordered = service._order_dicoms(list(reversed(dicoms)), metadata)
    assert [d.SeriesInstanceUID for d in ordered] == ["1.1", "2.1", "1.2", "2.2", "1.3"]
    assert [d.SeriesInstanceUID for d in service._order_dicoms(list(reversed(dicoms)))] == [
        "1.1", "1.2", "1.3", "2.1", "2.2"]


def test_metadata_keeps_both_diagnostic_coronary_territories(service):
    dicoms = [SimpleNamespace(SeriesInstanceUID=str(i)) for i in range(4)]
    metadata = {
        "0": {"status": "diagnostic", "main_structure": "Left Coronary"},
        "1": {"status": "diagnostic", "main_structure": "Right Coronary"},
        "2": {"status": "diagnostic", "main_structure": "Aorta"},
        "3": {"status": "non-diagnostic", "main_structure": "Right Coronary"},
    }
    assert service._filter_dicoms_with_metadata(dicoms, metadata) == dicoms[:2]


@pytest.mark.parametrize("handler", ["_handle_json_output", "_handle_html_output"])
def test_metadata_excluding_every_video_does_not_restore_excluded_inputs(service, monkeypatch, handler):
    dicoms = [SimpleNamespace(SeriesInstanceUID="1")]
    request = SimpleNamespace(additionalMetadata={"1": {"status": "non-diagnostic"}})
    monkeypatch.setattr(service, "_decode_dicoms", lambda request: dicoms)
    seen = []
    monkeypatch.setattr(service, "_run_inference", lambda inputs, metadata=None: seen.append(inputs))
    result = asyncio.run(getattr(service, handler)(request))
    assert seen == [[]]
    if handler == "_handle_json_output":
        assert result["predictions"] == {}
    else:
        assert b"No video" in base64.b64decode(result["htmlBase64"])


def test_zero_frame_avi_returns_none_and_releases_capture(service, monkeypatch, tmp_path):
    path = tmp_path / "empty.avi"
    path.touch()
    released = []
    capture = SimpleNamespace(read=lambda: (False, None), release=lambda: released.append(True))
    monkeypatch.setattr(logic, "_dicom_to_avi", lambda *args: str(path))
    monkeypatch.setattr(logic.cv, "VideoCapture", lambda path: capture)
    assert service._process_dicom_to_video(None, str(tmp_path / "empty")) is None
    assert released == [True]
    assert not path.exists()


def test_empty_video_does_not_abort_later_valid_video_and_padding_mask(service, monkeypatch):
    service.model_config["VideoMILWrapper"] = {"num_videos": 4, "resize": 4, "stride": 1}
    service.model_config["VideoEncoder"]["num_frames"] = 2
    empty = pydicom.Dataset()
    empty.SeriesInstanceUID = "1.1"
    empty.SeriesTime = "090000"
    valid = pydicom.Dataset()
    valid.SeriesInstanceUID = "1.2"
    valid.SeriesTime = "100000"
    seen = []

    def decode(dicom, name):
        seen.append(name)
        return np.array([]) if dicom is empty else np.ones((1, 4, 4, 3), dtype=np.float32)

    class RecordingModel(torch.nn.Module):
        def forward(self, batch, video_mask):
            assert batch.shape == (1, 4, 2, 4, 4, 3)
            assert video_mask.tolist() == [[True, False, False, False]]
            assert torch.count_nonzero(batch[:, 1:]) == 0
            return {"scad": torch.tensor([[1.0]])}

    monkeypatch.setattr(service, "_process_dicom_to_video", decode)
    service.models["video_mil_wrapper"] = RecordingModel()
    outputs = service._run_inference([valid, empty])
    assert outputs is not None
    assert outputs["scad"].item() == 1.0
    assert seen == ["1.1", "1.2"]


def test_rectangular_dicom_uses_rows_as_height_and_columns_as_width(monkeypatch):
    class FakeDicom(dict):
        pixel_array = np.zeros((2, 12, 20), dtype=np.uint8)
        PhotometricInterpretation = "MONOCHROME2"

    dicom = FakeDicom({(0x28, 0x10): SimpleNamespace(value=12),
                       (0x28, 0x11): SimpleNamespace(value=20)})
    sizes, frames = [], []
    writer = SimpleNamespace(write=lambda frame: frames.append(frame), release=lambda: None)
    monkeypatch.setattr(logic.cv, "VideoWriter", lambda path, codec, fps, size: sizes.append(size) or writer)
    assert logic._dicom_to_avi(dicom, "rectangular.avi") == "rectangular.avi"
    assert sizes == [(20, 12)]
    assert len(frames) == 2


def test_actual_mil_logits_are_exactly_invariant_to_padded_content(service):
    torch.manual_seed(12)
    mil = MultiInstanceLinearProbing(
        embedding_dim=8, head_structure={k: v["head_dim"] for k, v in service._class_mapping.items()},
        pooling_mode="mean", attention_hidden=8, dropout=0,
        use_cls_token=False, num_attention_heads=2, separate_video_attention=True,
        normalization_strategy="post_norm",
    )
    model = VideoMILWrapper(torch.nn.Identity(), mil, num_videos=4).eval()
    original = torch.randn(1, 4, 8)
    changed = original.clone()
    changed[:, 1:] = torch.randn_like(changed[:, 1:]) * 100
    mask = torch.tensor([[True, False, False, False]])
    with torch.no_grad():
        before, after = model(original, mask), model(changed, mask)
    for head in before:
        assert torch.equal(before[head], after[head]), head


def test_high_bit_depth_dicom_is_windowed_to_uint8_like_training():
    video = np.linspace(0, 4095, 2 * 4 * 4).reshape(2, 4, 4).astype(np.uint16)
    converted = logic._to_display_uint8(video, "MONOCHROME2")
    assert converted.dtype == np.uint8
    assert converted.min() == 0 and converted.max() == 255
    eight_bit = np.arange(32, dtype=np.uint8).reshape(2, 4, 4)
    assert np.array_equal(logic._to_display_uint8(eight_bit, "MONOCHROME2"), eight_bit)
    assert np.array_equal(logic._to_display_uint8(eight_bit, "MONOCHROME1"), 255 - eight_bit)
