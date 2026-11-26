"""DICOM ECG objects -> the page the model reads (no weights needed)."""
from __future__ import annotations

import hashlib
import json
import sys
from pathlib import Path

import numpy as np
import pytest
import torch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(Path(__file__).resolve().parent))

import ecg_fixtures as fx  # noqa: E402
from utils.ecg_inputs import EcgInputError, ecg_page, order_candidates, waveform_counts  # noqa: E402
from utils.ecg_page_renderer import LEAD_ORDER, render_training_page  # noqa: E402
from utils.image_preprocessing import preprocess_ecg_image as preprocess_page  # noqa: E402

PREPROCESSING = json.loads((ROOT / "models/encoder_config.json").read_text())["preprocessing"]
# DeepECG_Docker tests/test_pdf_inputs.py: the training renderer's page of a fixed signal, verified
# pixel-identical to the training PNGs with matplotlib 3.10.9 and Pillow 12.3.0.
RENDERER_GOLDEN_PIXELS_SHA256 = "c4fba3cc04ec22c3d577dc9c8c883e3f1a3f3e6bd41e4474cba235afbd8aefc7"


def _training_converter(leads_500hz: dict) -> np.ndarray:
    """DeepECG-PNG dataset_creation/ecg_plotter/xml_converter.py: 500 Hz MUSE leads kept every
    other sample, limb leads derived from I and II."""
    d = {k: np.asarray(v, dtype=np.float64)[::2] for k, v in leads_500hz.items()}
    d["III"] = d["II"] - d["I"]
    d["aVR"] = -(d["I"] + d["II"]) / 2
    d["aVF"] = (d["II"] + d["III"]) / 2
    d["aVL"] = (d["I"] - d["III"]) / 2
    return np.stack([d[k] for k in LEAD_ORDER], axis=1)


def test_the_renderer_draws_the_verified_pixels():
    t = np.arange(2500) / 250.0
    leads = []
    for k in range(12):
        beat = sum(np.exp(-((t - c) ** 2) / (2 * 0.012 ** 2)) for c in np.arange(0.4, 10, 0.85))
        sign = 1 if k % 3 else -1
        leads.append(np.round(sign * (150 + 20 * k) * beat + 40 * np.sin(2 * np.pi * (0.3 + 0.05 * k) * t)))
    page = render_training_page(np.stack(leads, axis=1))
    assert hashlib.sha256(np.asarray(page).tobytes()).hexdigest() == RENDERER_GOLDEN_PIXELS_SHA256


def test_waveform_counts_follow_the_training_converter():
    leads = fx.synthetic_counts(fs=500)
    ds = fx.roundtrip(fx.waveform_dicom(leads, fs=500, median=True))
    counts, qc = waveform_counts(ds)
    assert counts.shape == (2500, 12)
    np.testing.assert_allclose(counts, _training_converter(leads), atol=1e-9)
    assert qc == {"samplingFrequencyHz": 500.0, "resampled": False}


@pytest.mark.parametrize("unit,sensitivity,scale", [("uV", 4.88, 1.0), ("uV", 1.0, 1 / 4.88), ("mV", 0.00488, 1.0)])
def test_waveform_units_are_converted_to_muse_counts(unit, sensitivity, scale):
    leads = fx.synthetic_counts(fs=500)
    counts, _ = waveform_counts(fx.roundtrip(fx.waveform_dicom(leads, sensitivity=sensitivity, unit=unit)))
    np.testing.assert_allclose(counts, _training_converter(leads) * scale, atol=1e-6)


def test_1000_hz_keeps_every_fourth_sample_and_other_rates_are_resampled():
    leads = fx.synthetic_counts(fs=1000)
    counts, qc = waveform_counts(fx.roundtrip(fx.waveform_dicom(leads, fs=1000)))
    np.testing.assert_allclose(counts[:, 0], leads["I"][::4], atol=1e-9)
    assert not qc["resampled"]
    _, qc = waveform_counts(fx.roundtrip(fx.waveform_dicom(fx.synthetic_counts(fs=300), fs=300)))
    assert qc["resampled"]


def test_short_or_incomplete_waveforms_are_refused():
    with pytest.raises(EcgInputError, match="10 s"):
        waveform_counts(fx.roundtrip(fx.waveform_dicom(fx.synthetic_counts(seconds=8.0))))
    leads = fx.synthetic_counts()
    del leads["V3"]
    with pytest.raises(EcgInputError, match="V3"):
        waveform_counts(fx.roundtrip(fx.waveform_dicom(leads)))


def test_vector_pdf_is_redrawn_close_to_the_waveform_render():
    t, leads_mv = fx.synthetic_leads_mv()
    pdf = fx.roundtrip(fx.pdf_dicom(fx.vector_ecg_pdf(t, leads_mv)))
    page = ecg_page("pdf", pdf)
    assert page.source == "pdf" and page.image.size == (1250, 625)
    counts = {k: v / 0.00488 for k, v in leads_mv.items() if k in fx.SCP_ECG_CODES}
    direct = ecg_page("waveform", fx.roundtrip(fx.waveform_dicom(
        {k: counts[k] for k in ("I", "II", "V1", "V2", "V3", "V4", "V5", "V6")}, sensitivity=4.88)))
    a = preprocess_page(page.image, PREPROCESSING)
    b = preprocess_page(direct.image, PREPROCESSING)
    assert torch.nn.functional.cosine_similarity(a.flatten(), b.flatten(), dim=0) > 0.98


def test_a_pdf_that_is_not_a_vector_ecg_is_refused():
    with pytest.raises(EcgInputError, match="PDF"):
        ecg_page("pdf", fx.roundtrip(fx.pdf_dicom(b"%PDF-1.4\n%%EOF\n")))


def test_image_objects_are_used_as_they_are():
    rgb = np.zeros((300, 600, 3), dtype=np.uint8)
    rgb[:, :, 0] = 255
    page = ecg_page("image", fx.roundtrip(fx.image_dicom(rgb)))
    assert page.source == "image" and page.image.size == (600, 300)
    assert preprocess_page(page.image, PREPROCESSING).shape == (3, 448, 896)


def test_waveforms_are_preferred_then_pdfs_then_images_then_the_earliest():
    late = fx.waveform_dicom(fx.synthetic_counts(), acquisition="20260101130000")
    early = fx.waveform_dicom(fx.synthetic_counts(), acquisition="20260101110000")
    image = fx.image_dicom(np.zeros((10, 20), dtype=np.uint8))
    pdf = fx.pdf_dicom(b"%PDF-1.4\n")
    ordered = order_candidates([fx.roundtrip(d) for d in (image, pdf, late, early)])
    assert [kind for kind, _ in ordered] == ["waveform", "waveform", "pdf", "image"]
    assert ordered[0][1].SOPInstanceUID == early.SOPInstanceUID


def test_deepecg_docker_preprocessing_matches_the_training_pipeline():
    """DeepECG_Docker preprocess_ecg_image (used here) equals DeepECG-PNG utils/image.process_image,
    written out with torchvision: L, stretch resize, 3 channels, [0, 1], ImageNet normalisation."""
    from PIL import Image
    from torchvision.transforms import v2

    page = Image.fromarray((np.random.default_rng(0).random((625, 1250, 4)) * 255).astype(np.uint8), "RGBA")
    expected = v2.Compose([v2.Resize((448, 896), antialias=True), v2.Grayscale(num_output_channels=3),
                           v2.ToImage(), v2.ToDtype(torch.float32, scale=True)])(page.convert("L"))
    expected = v2.Normalize(mean=PREPROCESSING["mean"], std=PREPROCESSING["std"])(expected)
    assert torch.equal(preprocess_page(page, PREPROCESSING), expected)
