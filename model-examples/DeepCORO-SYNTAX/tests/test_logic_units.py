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


def test_all_six_heads_are_postprocessed_and_reports_use_syntax(service):
    outputs = {
        "syntax": torch.tensor([[24.5]]),
        "syntax_left": torch.tensor([[-1.0]]),
        "syntax_right": torch.tensor([[7.5]]),
        "syntax_category": torch.tensor([[0.0, 1.0, 3.0, 2.0]]),
        "syntax_left_category": torch.tensor([[0.0, 1.0, 3.0, 2.0]]),
        "syntax_right_category": torch.tensor([[0.0, 1.0, 3.0]]),
    }
    preds = service._postprocess(outputs)
    for name, spec in service._class_mapping.items():
        assert name in preds
        if spec["task"] == "multiclass_classification":
            assert len(preds[name]) == spec["head_dim"]
            assert sum(preds[name]) == pytest.approx(1.0)
            assert preds[f"{name}_argmax"] == 2
    assert preds["syntax_left"] == 0.0
    formatted = service._format_predictions(preds)
    assert formatted["syntax"]["value"] == 24.5
    assert formatted["territory"] == {"left": 0.0, "right": 7.5, "unit": "points"}
    assert formatted["severityHead"]["class"] == "23-32"
    assert formatted["intermediateToHigh"]["positive"] is True
    html = service._render_html(preds, service._recommendations(preds))
    assert "DeepCORO-SYNTAX" in html
    assert "LVEF" not in html
    assert "Research use only" in html


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
    monkeypatch.setattr(service, "_run_inference", lambda inputs: seen.append(inputs))
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
    service.model_config["VideoMILWrapper"] = {"num_videos": 3, "resize": 4, "stride": 1}
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
            assert batch.shape == (1, 3, 2, 4, 4, 3)
            assert video_mask.tolist() == [[True, False, False]]
            assert torch.count_nonzero(batch[:, 1:]) == 0
            return {"syntax": torch.tensor([[1.0]])}

    monkeypatch.setattr(service, "_process_dicom_to_video", decode)
    service.models["video_mil_wrapper"] = RecordingModel()
    outputs = service._run_inference([valid, empty])
    assert outputs is not None
    assert outputs["syntax"].item() == 1.0
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
        pooling_mode="attention+cls_token", attention_hidden=8, dropout=0,
        use_cls_token=True, num_attention_heads=2, separate_video_attention=True,
        normalization_strategy="post_norm",
    )
    model = VideoMILWrapper(torch.nn.Identity(), mil, num_videos=3).eval()
    original = torch.randn(1, 3, 2, 8)
    changed = original.clone()
    changed[:, 1:] = torch.randn_like(changed[:, 1:]) * 100
    mask = torch.tensor([[True, False, False]])
    with torch.no_grad():
        before, after = model(original, mask), model(changed, mask)
    for head in before:
        assert torch.equal(before[head], after[head]), head
