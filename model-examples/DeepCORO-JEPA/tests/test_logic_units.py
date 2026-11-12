"""Unit tests that need neither weights nor a GPU."""

from __future__ import annotations

import sys
from pathlib import Path

import numpy as np
import pydicom
import torch
from pydicom.dataset import Dataset

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from logic import CustomPredictionService  # noqa: E402

GROUPS = ("prox_rca", "mid_rca", "dist_rca", "pda", "posterolateral", "left_main", "prox_lad", "mid_lad", "dist_lad",
          "D1", "D2", "prox_lcx", "mid_lcx", "dist_lcx", "om1", "om2", "bx", "lvp")


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
    preds = CustomPredictionService.postprocess(GROUPS, res, n_videos=7)
    assert list(preds["segments"]) == list(GROUPS) and preds["n_videos"] == 7 and preds["encoder_passes"] == 2
    assert preds["segments"]["lvp"]["stenosis_pct"] == 100.0  # clamped
    assert preds["segments"]["left_main"]["significant_threshold_pct"] == 50
    assert preds["segments"]["prox_lad"]["significant_threshold_pct"] == 70
    assert preds["lad_physiology"]["ffr_abnormal_prob"] == 0.5
    assert "calcif_prob" not in preds["segments"]["prox_rca"]  # v12 has no lesion head


def test_lesion_outputs_are_reported_when_the_checkpoint_has_them():
    res = {"encoder_passes": 1, "severity": torch.zeros(18), "binary_probability": torch.zeros(18),
           "lesion_calcif_probability": torch.full((18,), 0.3), "lesion_thrombus_probability": torch.zeros(18),
           "lesion_cto_probability": torch.zeros(18)}
    preds = CustomPredictionService.postprocess(GROUPS, res, n_videos=1)
    assert preds["segments"]["mid_lad"]["calcif_prob"] == 0.3 and "lad_physiology" not in preds
