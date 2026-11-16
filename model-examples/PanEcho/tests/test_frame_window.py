"""Decoding only the sampled frames must give the clip cut from the whole video.

Synthetic pixel data only (no PHI).
"""

import sys
from pathlib import Path
from types import SimpleNamespace

import numpy as np
import pytest
from pydicom.dataset import Dataset, FileMetaDataset
from pydicom.uid import ExplicitVRLittleEndian, JPEGLSLossless, RLELossless, generate_uid

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from models import view_classifier_utils
from models.view_classifier_utils import clip_window, decoding_plugin, load_frame_window, load_pixel_array
from utils.dicom_pixels import to_display_uint8

RNG = np.random.default_rng(3)


def make_dicom(frames: int, photometric: str = "RGB", bits: int = 8, rows: int = 12, cols: int = 10) -> Dataset:
    samples = 1 if photometric.startswith("MONOCHROME") else 3
    ds = Dataset()
    ds.file_meta = FileMetaDataset()
    ds.file_meta.TransferSyntaxUID = ExplicitVRLittleEndian
    ds.SOPClassUID = ds.file_meta.MediaStorageSOPClassUID = "1.2.840.10008.5.1.4.1.1.3.1"
    ds.SOPInstanceUID = ds.file_meta.MediaStorageSOPInstanceUID = generate_uid()
    ds.SeriesInstanceUID = generate_uid()
    ds.Modality = "US"
    ds.Rows, ds.Columns, ds.SamplesPerPixel = rows, cols, samples
    if frames > 1:
        ds.NumberOfFrames = frames
    ds.PhotometricInterpretation = photometric
    if samples == 3:
        ds.PlanarConfiguration = 0
    ds.PixelRepresentation = 0
    ds.BitsAllocated = bits
    ds.BitsStored = bits if bits == 8 else 12
    ds.HighBit = ds.BitsStored - 1
    shape = (frames, rows, cols) + ((3,) if samples == 3 else ())
    dtype = np.uint8 if bits == 8 else np.uint16
    ds.PixelData = RNG.integers(0, 2 ** ds.BitsStored, shape, dtype=dtype).tobytes()
    return ds


def service():
    from logic import CustomPredictionService

    instance = CustomPredictionService.__new__(CustomPredictionService)
    instance.config = SimpleNamespace(models={"pan_echo": SimpleNamespace(normalization="imagenet", clip_len=16)})
    return instance


def whole_video_clip(dicom: Dataset, clip_len: int = 16) -> np.ndarray:
    """The clip as computed before frame-level decoding was introduced."""
    pixels = to_display_uint8(load_pixel_array(dicom), getattr(dicom, "PhotometricInterpretation", None))
    return service()._load_clip_from_dicom(pixels, clip_len=clip_len)


@pytest.mark.parametrize(
    "frame_count,expected",
    [(2, (0, 2)), (15, (0, 15)), (16, (0, 16)), (17, (0, 16)), (18, (1, 17)), (94, (39, 55))],
)
def test_clip_window_is_the_centred_clip(frame_count, expected):
    assert clip_window(frame_count, 16) == expected
    video = np.arange(frame_count, dtype=np.uint8).reshape(frame_count, 1, 1, 1).repeat(3, axis=3)
    clip = service()._load_clip_from_dicom(video, clip_len=16)
    assert clip[: expected[1] - expected[0], 0, 0, 0].tolist() == list(range(*expected))


@pytest.mark.parametrize("photometric", ["RGB", "YBR_FULL", "MONOCHROME2", "MONOCHROME1"])
@pytest.mark.parametrize("frames", [5, 16, 41])
def test_sampled_frames_match_whole_video_decode(photometric, frames):
    dicom = make_dicom(frames, photometric)
    start, stop = clip_window(frames, 16)

    window = load_frame_window(dicom, start, stop)

    assert window is not None and window.dtype == np.uint8
    assert np.array_equal(window, load_pixel_array(make_copy(dicom))[start:stop])


def make_copy(dicom: Dataset) -> Dataset:
    import copy

    return copy.deepcopy(dicom)


@pytest.mark.parametrize("photometric", ["RGB", "MONOCHROME1"])
@pytest.mark.parametrize("frames", [5, 16, 41])
def test_service_clip_is_unchanged(photometric, frames):
    dicom = make_dicom(frames, photometric)
    expected = whole_video_clip(make_copy(dicom))

    instance = service()
    clip = instance._load_clip_from_dicom(instance._load_display_pixels(dicom, 16), clip_len=16)

    assert clip.dtype == expected.dtype and np.array_equal(clip, expected)


def test_mislabeled_ybr_full_422_is_retagged_on_both_paths():
    dicom = make_dicom(20, "YBR_FULL")
    dicom.PhotometricInterpretation = "YBR_FULL_422"  # buffer is full-size: vendor mislabel
    reference = load_pixel_array(make_copy(dicom))

    window = load_frame_window(dicom, 2, 18)

    assert dicom.PhotometricInterpretation == "YBR_FULL"
    assert np.array_equal(window, reference[2:18])


def test_rle_encapsulated_frames_match_whole_video_decode():
    dicom = make_dicom(20, "RGB")
    reference = load_pixel_array(make_copy(dicom))
    dicom.compress(RLELossless, encoding_plugin="pydicom")

    window = load_frame_window(dicom, 2, 18)

    assert np.array_equal(window, reference[2:18])
    assert np.array_equal(load_pixel_array(dicom), reference)


def test_jpeg_ls_frames_are_lossless_and_match_whole_video_decode():
    pytest.importorskip("jpeg_ls", reason="pyjpegls provides the JPEG-LS encoder for this fixture")
    dicom = make_dicom(20, "RGB", rows=32, cols=24)
    reference = load_pixel_array(make_copy(dicom))
    dicom.compress(JPEGLSLossless)

    assert decoding_plugin(dicom) == "pyjpegls"
    window = load_frame_window(dicom, 2, 18)

    assert np.array_equal(window, reference[2:18])
    assert np.array_equal(load_pixel_array(dicom), reference)
    instance = service()
    clip = instance._load_clip_from_dicom(instance._load_display_pixels(dicom, 16), clip_len=16)
    assert np.array_equal(clip, reference[2:18])


def test_other_transfer_syntaxes_use_the_default_plugin_order():
    assert decoding_plugin(make_dicom(4)) == ""
    assert decoding_plugin(Dataset()) == ""


@pytest.mark.parametrize(
    "dicom,window",
    [
        (make_dicom(20, "MONOCHROME2", bits=16), (2, 18)),  # windowed by clip-level percentiles
        (make_dicom(1), (0, 1)),
        (make_dicom(20), (10, 30)),
        (make_dicom(20), (5, 5)),
    ],
    ids=["high-bit", "single-frame", "window-past-end", "empty-window"],
)
def test_whole_video_path_is_kept_when_frames_cannot_be_sampled(dicom, window):
    assert load_frame_window(dicom, *window) is None


def test_decoding_failure_falls_back_to_whole_video_path(monkeypatch):
    dicom = make_dicom(20)
    dicom.PixelData = dicom.PixelData[:100]  # truncated
    assert load_frame_window(dicom, 2, 18) is None

    captured = {}

    def whole(dcm):
        captured["called"] = True
        return np.zeros((20, 12, 10, 3), dtype=np.uint8)

    import logic

    monkeypatch.setattr(logic, "load_pixel_array", whole)
    pixels = service()._load_display_pixels(dicom, 16)
    assert captured["called"] and pixels.shape[0] == 20


def test_high_bit_clip_still_uses_whole_clip_statistics():
    dicom = make_dicom(40, "MONOCHROME2", bits=16)
    expected = whole_video_clip(make_copy(dicom))

    instance = service()
    clip = instance._load_clip_from_dicom(instance._load_display_pixels(dicom, 16), clip_len=16)

    assert np.array_equal(clip, expected)
