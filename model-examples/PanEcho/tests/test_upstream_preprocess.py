"""1.1.3: upstream-aligned input for the AS heads only; synthetic data (no PHI)."""
import sys
from pathlib import Path

import numpy as np
import pytest
from pydicom.dataset import Dataset

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
torch = pytest.importorskip("torch")
logic = pytest.importorskip("logic")
from utils import upstream_preprocess as up  # noqa: E402


def region(x0, y0, x1, y1, fmt=1):
    r = Dataset()
    r.RegionSpatialFormat = fmt
    r.RegionLocationMinX0, r.RegionLocationMinY0, r.RegionLocationMaxX1, r.RegionLocationMaxY1 = x0, y0, x1, y1
    return r


def test_sector_box_is_the_union_of_2d_regions_only():
    d = Dataset()
    d.SequenceOfUltrasoundRegions = [region(100, 50, 600, 500), region(120, 40, 640, 480), region(0, 600, 1000, 700, fmt=3)]
    assert up.sector_box(d) == (100, 40, 641, 501)
    assert up.sector_box(Dataset()) is None


def test_crop_keeps_aspect_and_four_clips_span_the_video():
    frames = np.zeros((60, 708, 1016, 3), np.uint8)
    frames[:, 100:300, 200:600] = 200          # a 400 x 200 bright box
    d = Dataset(); d.SequenceOfUltrasoundRegions = [region(200, 100, 599, 299)]; d.FrameTime = 33.3
    clips = up.video_clips(frames, d)
    assert len(clips) == 4 and all(c.shape == (3, 16, 224, 224) for c in clips)
    sq = up.crop_square(frames[:1], up.sector_box(d))[0]
    rows = np.where(sq[:, 112, 0] > 100)[0]
    assert 108 <= len(rows) <= 116             # 200/400 of 224 rows: aspect preserved (not squashed)
    assert up.clip_starts(60, 1) == [0, 14, 29, 44]


def test_hybrid_takes_only_the_as_heads_from_the_upstream_input(monkeypatch):
    live = {"AVStenosis": torch.tensor([0.1, 0.8, 0.1]), "AVPkVel(m|s)": torch.tensor([1.9]), "EF": torch.tensor([55.0])}
    upstream = {"AVStenosis": torch.tensor([0.3, 0.4, 0.3]), "AVPkVel(m|s)": torch.tensor([2.5]), "EF": torch.tensor([61.0])}
    svc = logic.CustomPredictionService.__new__(logic.CustomPredictionService)
    monkeypatch.setattr(svc, "_run_inference_live", lambda d: dict(live), raising=False)
    monkeypatch.setattr(svc, "_run_inference_upstream", lambda d: dict(upstream), raising=False)
    out = svc._run_inference([])
    assert torch.equal(out["EF"], live["EF"])
    assert torch.equal(out["AVStenosis"], upstream["AVStenosis"])
    assert torch.equal(out["AVPkVel(m|s)"], upstream["AVPkVel(m|s)"])
