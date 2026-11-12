"""High-bit and MONOCHROME1 DICOM input, using synthetic pixel data (no PHI).

8-bit MONOCHROME2 input must stay byte-identical to the previous behaviour; 12-bit input
must be windowed to uint8 (0.5-99.5 percentile) and MONOCHROME1 input inverted, as in
``utils/dicom_pixels.py``.
"""

import sys
from pathlib import Path

import numpy as np
import pytest
from pydicom.dataset import Dataset, FileMetaDataset
from pydicom.uid import ExplicitVRLittleEndian, generate_uid

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

RNG = np.random.default_rng(0)
EIGHT_BIT = RNG.integers(0, 256, (3, 16, 16), dtype=np.uint8)
TWELVE_BIT = RNG.integers(0, 4096, (3, 16, 16), dtype=np.uint16)


def make_dicom(pixels: np.ndarray, photometric: str) -> Dataset:
    ds = Dataset()
    ds.file_meta = FileMetaDataset()
    ds.file_meta.TransferSyntaxUID = ExplicitVRLittleEndian
    ds.SOPClassUID = ds.file_meta.MediaStorageSOPClassUID = "1.2.840.10008.5.1.4.1.1.12.1"
    ds.SOPInstanceUID = ds.file_meta.MediaStorageSOPInstanceUID = generate_uid()
    ds.SeriesInstanceUID = generate_uid()
    ds.Modality = "XA"
    ds.NumberOfFrames, ds.Rows, ds.Columns = pixels.shape
    ds.SamplesPerPixel = 1
    ds.PhotometricInterpretation = photometric
    ds.PixelRepresentation = 0
    ds.BitsAllocated = 8 if pixels.dtype == np.uint8 else 16
    ds.BitsStored = 8 if pixels.dtype == np.uint8 else 12
    ds.HighBit = ds.BitsStored - 1
    ds.PixelData = pixels.tobytes()
    return ds


def windowed(raw: np.ndarray) -> np.ndarray:
    values = raw.astype(np.float32)
    low, high = np.percentile(values, 0.5), np.percentile(values, 99.5)
    return (np.clip((values - low) / max(high - low, 1e-6), 0, 1) * 255).astype(np.uint8)


CASES = [
    ("MONOCHROME2", EIGHT_BIT, EIGHT_BIT),
    ("MONOCHROME1", EIGHT_BIT, 255 - EIGHT_BIT),
    ("MONOCHROME2", TWELVE_BIT, windowed(TWELVE_BIT)),
    ("MONOCHROME1", TWELVE_BIT, 255 - windowed(TWELVE_BIT)),
]
CASE_IDS = ["8bit-mono2-unchanged", "8bit-mono1", "12bit-mono2", "12bit-mono1"]


class _Captured(Exception):
    pass

import logic


class _Writer:
    def __init__(self):
        self.frames = []

    def write(self, frame):
        self.frames.append(frame.copy())

    def release(self):
        pass


@pytest.mark.parametrize("photometric,raw,expected", CASES, ids=CASE_IDS)
def test_video_writer_receives_display_uint8_frames(monkeypatch, photometric, raw, expected):
    writer = _Writer()
    monkeypatch.setattr(logic.cv, "VideoWriter", lambda *args: writer)
    assert logic.process_dicom_video(make_dicom(raw, photometric), "synthetic.avi") == "synthetic.avi"
    frames = np.stack(writer.frames)
    assert frames.dtype == np.uint8
    assert np.array_equal(frames, np.stack([logic.cv.cvtColor(f, logic.cv.COLOR_GRAY2BGR) for f in expected]))


def test_eight_bit_monochrome2_is_byte_identical_to_previous_conversion(monkeypatch):
    writer = _Writer()
    monkeypatch.setattr(logic.cv, "VideoWriter", lambda *args: writer)
    dicom = make_dicom(EIGHT_BIT, "MONOCHROME2")
    logic.process_dicom_video(dicom, "synthetic.avi")
    previous = [logic.cv.cvtColor(frame, logic.cv.COLOR_GRAY2BGR) for frame in dicom.pixel_array]
    assert b"".join(f.tobytes() for f in writer.frames) == b"".join(f.tobytes() for f in previous)
