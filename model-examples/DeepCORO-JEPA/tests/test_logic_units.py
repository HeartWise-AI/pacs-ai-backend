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
THRESHOLDS = {"pressure_operating_point": "balanced",
              "values": {**{f"{g}_prob_significant": 0.2 for g in GROUPS}, "ffr_abnormal_prob": 0.4396, "ifr_abnormal_prob": 0.6141}}
SETTINGS = {"path": "thresholds.json", "pressure_operating_point": "balanced"}


def _thresholds_file(tmp_path, **changes):
    table = {f"{g}_prob_significant": {"balanced": 0.1} for g in GROUPS}
    table["ffr_abnormal_prob"] = {"balanced": 0.4396, "high_sensitivity": 0.1831, "high_specificity": 0.6859}
    table["ifr_abnormal_prob"] = {"balanced": 0.6141, "high_sensitivity": 0.1136, "high_specificity": 0.8719}
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
    assert "calcif_prob" not in preds["segments"]["prox_rca"]  # v12 has no lesion head
    # calls: probability strictly greater than the threshold
    assert all(v["significant"] and v["prob_threshold"] == 0.2 for v in preds["segments"].values())  # 0.25 > 0.2
    assert preds["lad_physiology"]["ffr_abnormal"] is True and preds["lad_physiology"]["ffr_abnormal_threshold"] == 0.4396
    assert preds["lad_physiology"]["ifr_abnormal"] is True and preds["lad_physiology"]["operating_point"] == "balanced"
    service = CustomPredictionService.__new__(CustomPredictionService)
    assert service.significant_segments(preds) == list(GROUPS)
    assert "FFR predicted abnormal" in service.diagnosis_text(preds) and "Flagged" in service.render_html(preds)


def test_a_probability_equal_to_the_threshold_is_not_a_positive_call():
    cut = {"pressure_operating_point": "balanced",
           "values": {**{f"{g}_prob_significant": 0.25 for g in GROUPS}, "ffr_abnormal_prob": 0.5, "ifr_abnormal_prob": 0.9}}
    res = {"encoder_passes": 2, "severity": torch.full((18,), 80.0), "binary_probability": torch.full((18,), 0.25),
           "physiology": {"ffr": torch.tensor([0.82]), "ifr": torch.tensor([0.9]),
                          "ffr_logit": torch.tensor([0.0]), "ifr_logit": torch.tensor([2.0])}}
    preds = CustomPredictionService.postprocess(GROUPS, res, n_videos=1, thresholds=cut)
    # an estimated 80% stenosis alone does not flag a segment: the call is on the probability
    assert not any(v["significant"] for v in preds["segments"].values())
    assert preds["lad_physiology"]["ffr_abnormal"] is False and preds["lad_physiology"]["ifr_abnormal"] is False
    service = CustomPredictionService.__new__(CustomPredictionService)
    assert service.diagnosis_text(preds).startswith("No segment flagged")


def test_thresholds_are_read_per_output_and_operating_point(tmp_path):
    models_dir = _thresholds_file(tmp_path)
    got = CustomPredictionService.load_thresholds(models_dir, SETTINGS, GROUPS, True, SHA)
    assert got["values"]["ffr_abnormal_prob"] == 0.4396 and got["values"]["ifr_abnormal_prob"] == 0.6141
    assert len(got["values"]) == 20 and got["pressure_operating_point"] == "balanced"
    sensitive = CustomPredictionService.load_thresholds(
        models_dir, {**SETTINGS, "pressure_operating_point": "high_sensitivity"}, GROUPS, True, SHA)
    assert sensitive["values"]["ifr_abnormal_prob"] == 0.1136 and sensitive["values"]["prox_lad_prob_significant"] == 0.1
    assert len(CustomPredictionService.load_thresholds(models_dir, SETTINGS, GROUPS, False, SHA)["values"]) == 18


def test_thresholds_fail_loudly_instead_of_falling_back(tmp_path):
    models_dir = _thresholds_file(tmp_path)
    with pytest.raises(ValueError, match="Unknown pressure_operating_point"):
        CustomPredictionService.load_thresholds(models_dir, {**SETTINGS, "pressure_operating_point": "youden"}, GROUPS, True, SHA)
    with pytest.raises(ValueError, match="takes exactly"):
        CustomPredictionService.load_thresholds(models_dir, {**SETTINGS, "operating_point": "balanced"}, GROUPS, True, SHA)
    with pytest.raises(ValueError, match="was derived for weights"):
        CustomPredictionService.load_thresholds(models_dir, SETTINGS, GROUPS, True, "d" * 64)
    with pytest.raises(ValueError, match="no threshold for"):
        CustomPredictionService.load_thresholds(models_dir, SETTINGS, GROUPS + ("new_segment",), True, SHA)
    with pytest.raises(FileNotFoundError):
        CustomPredictionService.load_thresholds(tmp_path / "absent", SETTINGS, GROUPS, True, SHA)
    broken = _thresholds_file(tmp_path, thresholds={f"{g}_prob_significant": {"balanced": 1.5} for g in GROUPS})
    with pytest.raises(ValueError, match="outside"):
        CustomPredictionService.load_thresholds(broken, SETTINGS, GROUPS, False, SHA)


def test_lesion_outputs_are_reported_when_the_checkpoint_has_them():
    res = {"encoder_passes": 1, "severity": torch.zeros(18), "binary_probability": torch.zeros(18),
           "lesion_calcif_probability": torch.full((18,), 0.3), "lesion_thrombus_probability": torch.zeros(18),
           "lesion_cto_probability": torch.zeros(18)}
    preds = CustomPredictionService.postprocess(GROUPS, res, n_videos=1, thresholds=THRESHOLDS)
    assert preds["segments"]["mid_lad"]["calcif_prob"] == 0.3 and "lad_physiology" not in preds
