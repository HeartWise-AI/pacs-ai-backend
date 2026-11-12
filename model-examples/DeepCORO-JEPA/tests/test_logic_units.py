"""Unit tests that need neither weights nor a GPU."""

from __future__ import annotations

import sys
from pathlib import Path

import json

import numpy as np
import pydicom
import pytest
import torch
from pydicom.dataset import Dataset

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from logic import CustomPredictionService  # noqa: E402

GROUPS = ("prox_rca", "mid_rca", "dist_rca", "pda", "posterolateral", "left_main", "prox_lad", "mid_lad", "dist_lad",
          "D1", "D2", "prox_lcx", "mid_lcx", "dist_lcx", "om1", "om2", "bx", "lvp")


SHA = "c" * 64
LESION_CUTS = {"calcif_prob": 0.2225, "thrombus_prob": 0.0851, "cto_prob": 0.157}
THRESHOLDS = {"pressure_operating_point": "balanced", "lesion_operating_point": "specificity_99",
              "values": {**{f"{g}_prob_significant": 0.2 for g in GROUPS}, "ffr_abnormal_prob": 0.4396,
                         "ifr_abnormal_prob": 0.6141, **LESION_CUTS}}
SETTINGS = {"path": "thresholds.json", "pressure_operating_point": "balanced", "lesion_operating_point": "specificity_99"}


def _thresholds_file(tmp_path, **changes):
    table = {f"{g}_prob_significant": {"balanced": 0.1} for g in GROUPS}
    table["ffr_abnormal_prob"] = {"balanced": 0.4396, "high_sensitivity": 0.1831, "high_specificity": 0.6859}
    table["ifr_abnormal_prob"] = {"balanced": 0.6141, "high_sensitivity": 0.1136, "high_specificity": 0.8719}
    table["calcif_prob"] = {"balanced": 0.0161, "specificity_95": 0.0791, "specificity_99": 0.2225}
    table["thrombus_prob"] = {"balanced": 0.0042, "specificity_95": 0.0167, "specificity_99": 0.0851}
    table["cto_prob"] = {"balanced": 0.0076, "specificity_95": 0.0329, "specificity_99": 0.157}
    spec = {"weights_sha256": SHA, "thresholds": table}
    spec.update(changes)
    (tmp_path / "thresholds.json").write_text(json.dumps(spec), encoding="utf-8")
    return tmp_path


def _dicom(sop: str, series_time: str | None) -> Dataset:
    ds = Dataset()
    ds.StudyInstanceUID, ds.SOPInstanceUID = "1.2.3", sop
    if series_time is not None:
        ds.SeriesTime = series_time
    return ds


def test_series_time_chooses_runs_and_training_filename_order_feeds_them(monkeypatch):
    service = CustomPredictionService.__new__(CustomPredictionService)
    monkeypatch.setattr(CustomPredictionService, "get_model_info", classmethod(lambda cls: {"dicomUploadMax": 2}))
    dicoms = [_dicom("9", "101000"), _dicom("1", "100500"), _dicom("5", "100000"), _dicom("3", None)]
    chosen = service.select_videos(dicoms)
    # the two earliest runs by SeriesTime (SOP 5 and 1), then ordered as '<study>_<sop>' filenames
    assert [d.SOPInstanceUID for d in chosen] == ["1", "5"]


def test_dicom_conversion_is_the_training_mjpg_avi(tmp_path):
    ds = Dataset()
    ds.PhotometricInterpretation = "MONOCHROME2"
    ds.Rows, ds.Columns, ds.NumberOfFrames = 32, 48, 3
    ds.SamplesPerPixel, ds.BitsAllocated, ds.BitsStored, ds.HighBit, ds.PixelRepresentation = 1, 8, 8, 7, 0
    ds.PixelData = np.random.default_rng(0).integers(0, 255, (3, 32, 48), dtype=np.uint8).tobytes()
    ds.file_meta = pydicom.dataset.FileMetaDataset()
    ds.file_meta.TransferSyntaxUID = pydicom.uid.ExplicitVRLittleEndian
    out = CustomPredictionService.dicom_to_avi(ds, str(tmp_path / "x.avi"))
    import cv2
    cap = cv2.VideoCapture(out)
    assert out and int(cap.get(cv2.CAP_PROP_FRAME_COUNT)) == 3
    assert int(cap.get(cv2.CAP_PROP_FOURCC)).to_bytes(4, "little") == b"MJPG"


def test_postprocess_reports_every_segment_and_physiology():
    res = {"encoder_passes": 2, "severity": torch.linspace(0, 110, 18), "binary_probability": torch.full((18,), 0.25),
           "physiology": {"ffr": torch.tensor([0.82]), "ifr": torch.tensor([0.9]),
                          "ffr_logit": torch.tensor([0.0]), "ifr_logit": torch.tensor([2.0])}}
    preds = CustomPredictionService.postprocess(GROUPS, res, n_videos=7, thresholds=THRESHOLDS)
    assert list(preds["segments"]) == list(GROUPS) and preds["n_videos"] == 7 and preds["encoder_passes"] == 2
    assert preds["segments"]["lvp"]["stenosis_pct"] == 100.0  # clamped
    assert preds["segments"]["left_main"]["significant_threshold_pct"] == 50
    assert preds["segments"]["prox_lad"]["significant_threshold_pct"] == 70
    assert preds["lad_physiology"]["ffr_abnormal_prob"] == 0.5
    assert "calcif_prob" not in preds["segments"]["prox_rca"] and "lesion_thresholds" not in preds  # no lesion head
    # calls: probability strictly greater than the threshold
    assert all(v["significant"] and v["prob_threshold"] == 0.2 for v in preds["segments"].values())  # 0.25 > 0.2
    assert preds["lad_physiology"]["ffr_abnormal"] is True and preds["lad_physiology"]["ffr_abnormal_threshold"] == 0.4396
    assert preds["lad_physiology"]["ifr_abnormal"] is True and preds["lad_physiology"]["operating_point"] == "balanced"
    service = CustomPredictionService.__new__(CustomPredictionService)
    assert service.significant_segments(preds) == list(GROUPS)
    assert "FFR predicted abnormal" in service.diagnosis_text(preds) and "Flagged" in service.render_html(preds)


def test_a_probability_equal_to_the_threshold_is_not_a_positive_call():
    cut = {"pressure_operating_point": "balanced", "lesion_operating_point": "specificity_99",
           "values": {**{f"{g}_prob_significant": 0.25 for g in GROUPS}, "ffr_abnormal_prob": 0.5, "ifr_abnormal_prob": 0.9,
                      "calcif_prob": 0.25, "thrombus_prob": 0.25, "cto_prob": 0.25}}
    res = {"encoder_passes": 2, "severity": torch.full((18,), 80.0), "binary_probability": torch.full((18,), 0.25),
           "physiology": {"ffr": torch.tensor([0.82]), "ifr": torch.tensor([0.9]),
                          "ffr_logit": torch.tensor([0.0]), "ifr_logit": torch.tensor([2.0])},
           **{f"lesion_{name}_probability": torch.full((18,), 0.25) for name in ("calcif", "thrombus", "cto")}}
    preds = CustomPredictionService.postprocess(GROUPS, res, n_videos=1, thresholds=cut)
    # an estimated 80% stenosis alone does not flag a segment: the call is on the probability
    assert not any(v["significant"] or v["calcif"] or v["thrombus"] or v["cto"] for v in preds["segments"].values())
    assert preds["lad_physiology"]["ffr_abnormal"] is False and preds["lad_physiology"]["ifr_abnormal"] is False
    service = CustomPredictionService.__new__(CustomPredictionService)
    text = service.diagnosis_text(preds)
    assert text.startswith("No segment flagged") and "No segment flagged for chronic total occlusion." in text


def test_thresholds_are_read_per_output_and_operating_point(tmp_path):
    models_dir = _thresholds_file(tmp_path)
    got = CustomPredictionService.load_thresholds(models_dir, SETTINGS, GROUPS, True, True, SHA)
    assert got["values"]["ffr_abnormal_prob"] == 0.4396 and got["values"]["ifr_abnormal_prob"] == 0.6141
    assert {name: got["values"][name] for name in LESION_CUTS} == LESION_CUTS  # one threshold per finding
    assert len(got["values"]) == 23 and got["pressure_operating_point"] == "balanced"
    assert got["lesion_operating_point"] == "specificity_99"
    other = CustomPredictionService.load_thresholds(
        models_dir, {**SETTINGS, "pressure_operating_point": "high_sensitivity", "lesion_operating_point": "specificity_95"},
        GROUPS, True, True, SHA)
    assert other["values"]["ifr_abnormal_prob"] == 0.1136 and other["values"]["prox_lad_prob_significant"] == 0.1
    assert other["values"]["thrombus_prob"] == 0.0167
    # a checkpoint without those heads needs no pressure or lesion thresholds
    assert len(CustomPredictionService.load_thresholds(models_dir, SETTINGS, GROUPS, True, False, SHA)["values"]) == 20
    assert len(CustomPredictionService.load_thresholds(models_dir, SETTINGS, GROUPS, False, False, SHA)["values"]) == 18


def test_thresholds_fail_loudly_instead_of_falling_back(tmp_path):
    models_dir = _thresholds_file(tmp_path)
    with pytest.raises(ValueError, match="Unknown pressure_operating_point"):
        CustomPredictionService.load_thresholds(models_dir, {**SETTINGS, "pressure_operating_point": "youden"}, GROUPS, True, True, SHA)
    with pytest.raises(ValueError, match="Unknown lesion_operating_point"):  # checked even without a lesion head
        CustomPredictionService.load_thresholds(models_dir, {**SETTINGS, "lesion_operating_point": "high_sensitivity"}, GROUPS, True, False, SHA)
    with pytest.raises(ValueError, match="takes exactly"):
        CustomPredictionService.load_thresholds(models_dir, {**SETTINGS, "operating_point": "balanced"}, GROUPS, True, True, SHA)
    with pytest.raises(ValueError, match="takes exactly"):  # the 1.0.0 block, without lesion_operating_point
        CustomPredictionService.load_thresholds(
            models_dir, {"path": "thresholds.json", "pressure_operating_point": "balanced"}, GROUPS, True, True, SHA)
    with pytest.raises(ValueError, match="was derived for weights"):
        CustomPredictionService.load_thresholds(models_dir, SETTINGS, GROUPS, True, True, "d" * 64)
    with pytest.raises(ValueError, match="no threshold for"):
        CustomPredictionService.load_thresholds(models_dir, SETTINGS, GROUPS + ("new_segment",), True, True, SHA)
    with pytest.raises(FileNotFoundError):
        CustomPredictionService.load_thresholds(tmp_path / "absent", SETTINGS, GROUPS, True, True, SHA)
    stenosis_only = {f"{g}_prob_significant": {"balanced": 0.1} for g in GROUPS}
    with pytest.raises(ValueError, match=r"no threshold for \['calcif_prob', 'thrombus_prob', 'cto_prob'\]"):
        CustomPredictionService.load_thresholds(_thresholds_file(tmp_path, thresholds=stenosis_only), SETTINGS, GROUPS, False, True, SHA)
    broken = _thresholds_file(tmp_path, thresholds={f"{g}_prob_significant": {"balanced": 1.5} for g in GROUPS})
    with pytest.raises(ValueError, match="outside"):
        CustomPredictionService.load_thresholds(broken, SETTINGS, GROUPS, False, False, SHA)


def test_lesion_outputs_are_reported_and_flagged_when_the_checkpoint_has_them():
    cto = torch.zeros(18)
    cto[GROUPS.index("prox_rca")] = 0.4
    res = {"encoder_passes": 1, "severity": torch.zeros(18), "binary_probability": torch.zeros(18),
           "lesion_calcif_probability": torch.full((18,), 0.3), "lesion_thrombus_probability": torch.zeros(18),
           "lesion_cto_probability": cto}
    preds = CustomPredictionService.postprocess(GROUPS, res, n_videos=1, thresholds=THRESHOLDS)
    assert preds["segments"]["mid_lad"]["calcif_prob"] == 0.3 and "lad_physiology" not in preds
    # one threshold per finding for all segments, reported once
    assert preds["lesion_thresholds"] == {"operating_point": "specificity_99", "calcif": 0.2225, "thrombus": 0.0851, "cto": 0.157}
    assert all(v["calcif"] and not v["thrombus"] for v in preds["segments"].values())  # 0.3 > 0.2225, 0 < 0.0851
    assert [g for g, v in preds["segments"].items() if v["cto"]] == ["prox_rca"]
    service = CustomPredictionService.__new__(CustomPredictionService)
    text = service.diagnosis_text(preds)
    assert "Flagged for chronic total occlusion (P above 0.157): Proximal RCA (P 0.40)." in text
    assert "No segment flagged for thrombus." in text and "Flagged for calcification" in text
    html = service.render_html(preds)
    assert "<th>P(CTO)</th>" in html and "<b>0.40 flagged</b>" in html and "operating point specificity_99" in html


def test_model_facts_name_the_training_run_of_the_served_weights():
    facts = json.loads((ROOT / "data/model_facts.json").read_text(encoding="utf-8"))["en"]
    info = json.loads((ROOT / "data/model_info.json").read_text(encoding="utf-8"))
    assert "https://wandb.ai/mhi_ai/deepcoro-segmentation/runs/400d0a77" in facts["Summary"]["Training_run"]
    assert facts["Summary"]["Version"] == info["version"]


def _service_with_one_readable_clip(monkeypatch, measured_forward):
    """A service whose DICOM decoding succeeds, so only the model call decides the outcome."""
    import types
    monkeypatch.setitem(sys.modules, "integrated_data", types.SimpleNamespace(load_video_clip=lambda path, sampling: torch.zeros(16, 8, 8)))
    monkeypatch.setitem(sys.modules, "integrated_study", types.SimpleNamespace(measured_forward=measured_forward))
    monkeypatch.setattr(CustomPredictionService, "models", {"deepcoro_jepa": torch.nn.Linear(1, 1)}, raising=False)
    monkeypatch.setattr(CustomPredictionService, "model_config", {"Forward": {
        "full_res": 224, "crop_res": 320, "max_views": 12, "crops_per_group": 4, "frame_sampling": "peak_s2"}}, raising=False)
    monkeypatch.setattr(CustomPredictionService, "supported_output_modes", ["json"])
    monkeypatch.setattr(CustomPredictionService, "is_initialized", True)
    monkeypatch.setattr(CustomPredictionService, "get_model_info", classmethod(lambda cls: {}))
    monkeypatch.setattr(CustomPredictionService, "_decode_dicoms", lambda self, request: [object()])
    monkeypatch.setattr(CustomPredictionService, "select_videos", lambda self, dicoms: dicoms)
    monkeypatch.setattr(CustomPredictionService, "dicom_to_avi", staticmethod(lambda dicom, output_path: output_path))
    return CustomPredictionService.__new__(CustomPredictionService)


def test_a_failed_forward_pass_is_an_error_not_a_no_video_report(monkeypatch):
    import asyncio
    import types

    def out_of_memory(model, clips, args):
        raise RuntimeError("CUDA out of memory")

    service = _service_with_one_readable_clip(monkeypatch, out_of_memory)
    with pytest.raises(RuntimeError, match="out of memory"):
        service._run_inference([object()])
    success, response = asyncio.run(service.predict(types.SimpleNamespace(outputMode="json")))
    body = json.loads(response.body)
    assert success is False and response.status_code == 500
    assert body["success"] is False and body["errorCode"] == "PROCESSING_ERROR" and "out of memory" in body["message"]


def test_no_usable_clip_is_still_reported_as_no_video(monkeypatch):
    import asyncio
    import types
    service = _service_with_one_readable_clip(monkeypatch, measured_forward=None)
    monkeypatch.setattr(CustomPredictionService, "dicom_to_avi", staticmethod(lambda dicom, output_path: None))
    success, result = asyncio.run(service.predict(types.SimpleNamespace(outputMode="json")))
    assert success is True and result["predictions"] == {} and result["modelRecommendations"]["presentable"] is False
