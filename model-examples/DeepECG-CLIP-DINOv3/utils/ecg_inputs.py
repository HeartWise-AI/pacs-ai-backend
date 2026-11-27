"""Turn the DICOM objects of an ECG study into the page image the model reads.

Three DICOM inputs are accepted, in this order of preference:

1. ``waveform``: a 12-lead or general ECG waveform (SOP classes 1.2.840.10008.5.1.4.1.1.9.1.1
   and 1.2.840.10008.5.1.4.1.1.9.1.2). Leads I, II and V1-V6 of the rhythm group are
   converted to MUSE counts (4.88 uV per count), brought to 250 Hz exactly as the training
   data were (500 Hz: every other sample; MUSE XML was decimated this way), III, aVR, aVL and
   aVF are derived from I and II with the formulas of the training converter, and the page is
   drawn with the training renderer (utils/ecg_page_renderer.py). Same pixels as training.
2. ``pdf``: an Encapsulated PDF (1.2.840.10008.5.1.4.1.1.104.1) holding a vector ECG in the
   standard 3x4 + lead II rhythm layout, read exactly as DeepECG_Docker reads a PDF file
   (utils/ecg_pdf.py and utils/image_preprocessing.py, vendored unchanged): the traces are read
   from the vector paths and redrawn with the training renderer. The leads
   are interpolated from the drawing, so the page is close to, not identical with, a render
   of the original signal. Scanned (raster) PDFs and other layouts are refused.
3. ``image``: any other single-frame DICOM image of an ECG page (e.g. secondary capture).
   The pixels are used as they are. The model only saw such pages through its training
   augmentation (other layouts, photographs), so this input is the least faithful.

Nothing here writes patient data to disk except the temporary PDF handed to the PDF reader,
which is deleted at once.
"""
from __future__ import annotations

import os
import tempfile
from dataclasses import dataclass
from math import gcd
from typing import Optional

import numpy as np
import pydicom
from PIL import Image
from scipy.signal import resample_poly

from utils.ecg_page_renderer import LEAD_ORDER, SAMPLES, UV_PER_COUNT, render_training_page

WAVEFORM_SOP_CLASSES = {
    "1.2.840.10008.5.1.4.1.1.9.1.1": "12-lead ECG waveform",
    "1.2.840.10008.5.1.4.1.1.9.1.2": "general ECG waveform",
}
ENCAPSULATED_PDF_SOP_CLASS = "1.2.840.10008.5.1.4.1.1.104.1"
SOURCE_PRIORITY = ("waveform", "pdf", "image")
TARGET_FS = 250
RECORD_SECONDS = SAMPLES // TARGET_FS
MEASURED_LEADS = ("I", "II", "V1", "V2", "V3", "V4", "V5", "V6")
_UNIT_TO_UV = {"uv": 1.0, "µv": 1.0, "mv": 1000.0, "v": 1_000_000.0}


class EcgInputError(ValueError):
    """The object is not an ECG this service can read. The message never quotes patient data."""


@dataclass
class EcgPage:
    image: Image.Image
    source: str            # waveform | pdf | image


# -- classification and ordering ------------------------------------------------

def input_kind(dicom: pydicom.Dataset) -> Optional[str]:
    """``waveform``, ``pdf``, ``image`` or None when the object holds no ECG this service reads."""
    sop_class = str(getattr(dicom, "SOPClassUID", ""))
    if sop_class in WAVEFORM_SOP_CLASSES and "WaveformSequence" in dicom:
        return "waveform"
    if sop_class == ENCAPSULATED_PDF_SOP_CLASS and "EncapsulatedDocument" in dicom:
        return "pdf"
    if "PixelData" in dicom and int(getattr(dicom, "NumberOfFrames", 1) or 1) == 1:
        return "image"
    return None


def _acquisition_key(dicom: pydicom.Dataset) -> str:
    """Sort key of the object's acquisition time, YYYYMMDDHHMMSS[.ffffff] (earliest first): the acquisition
    timestamp (AcquisitionDateTime, the Waveform IOD's type 1 attribute, or AcquisitionDate/Time) before the
    content, series and study fallbacks, so two ECGs of one study are told apart by when they were acquired."""
    acquired = str(getattr(dicom, "AcquisitionDateTime", "") or "").strip()
    if acquired:
        return acquired.split("+")[0].split("-")[0]   # the UTC offset suffix does not sort
    for date_tag, time_tag in (("AcquisitionDate", "AcquisitionTime"), ("ContentDate", "ContentTime"),
                               ("SeriesDate", "SeriesTime"), ("StudyDate", "StudyTime")):
        date, time = str(getattr(dicom, date_tag, "") or ""), str(getattr(dicom, time_tag, "") or "")
        if date or time:
            return f"{date:8}{time}"
    return "~"   # undated objects last


def order_candidates(dicoms: list[pydicom.Dataset]) -> list[tuple[str, pydicom.Dataset]]:
    """Readable objects, best input first: waveform, then PDF, then image; within a kind the
    earliest acquisition first."""
    found = [(kind, d) for d in dicoms if (kind := input_kind(d)) is not None]
    return sorted(found, key=lambda item: (SOURCE_PRIORITY.index(item[0]), _acquisition_key(item[1])))


# -- waveform -------------------------------------------------------------------

def _lead_name(channel: pydicom.Dataset) -> Optional[str]:
    """Lead name of a channel from its ChannelSourceSequence (SCP-ECG or MDC code meaning)."""
    try:
        source = channel.ChannelSourceSequence[0]
    except (AttributeError, IndexError):
        return None
    meaning = str(getattr(source, "CodeMeaning", "") or "").strip()
    code = str(getattr(source, "CodeValue", "") or "").strip()
    scp_codes = {"5.6.3-9-1": "I", "5.6.3-9-2": "II", "5.6.3-9-61": "III", "5.6.3-9-62": "aVR",
                 "5.6.3-9-63": "aVL", "5.6.3-9-64": "aVF", "5.6.3-9-3": "V1", "5.6.3-9-4": "V2",
                 "5.6.3-9-5": "V3", "5.6.3-9-6": "V4", "5.6.3-9-7": "V5", "5.6.3-9-8": "V6"}
    if code in scp_codes:
        return scp_codes[code]
    text = meaning.lower().replace("lead", "").replace("(einthoven)", "").replace("(goldberger)", "")
    text = text.replace("-", " ").strip()
    for name in LEAD_ORDER:
        if text == name.lower():
            return name
    mdc = {"mdc_ecg_lead_i": "I", "mdc_ecg_lead_ii": "II", "mdc_ecg_lead_iii": "III",
           "mdc_ecg_lead_avr": "aVR", "mdc_ecg_lead_avl": "aVL", "mdc_ecg_lead_avf": "aVF",
           **{f"mdc_ecg_lead_v{k}": f"V{k}" for k in range(1, 7)}}
    return mdc.get(code.lower())


def _channel_scale_to_uv(channel: pydicom.Dataset) -> float:
    try:
        unit = channel.ChannelSensitivityUnitsSequence[0].CodeValue
    except (AttributeError, IndexError):
        raise EcgInputError("a waveform channel has no sensitivity unit") from None
    factor = _UNIT_TO_UV.get(str(unit).strip().lower())
    if factor is None:
        raise EcgInputError(f"unsupported waveform unit {unit!r}")
    return factor


def _rhythm_group(dicom: pydicom.Dataset) -> int:
    """Index of the multiplex group holding the 10 s rhythm (not the median beats)."""
    best, best_seconds = None, 0.0
    for index, group in enumerate(dicom.WaveformSequence):
        label = str(getattr(group, "MultiplexGroupLabel", "") or "").upper()
        fs = float(getattr(group, "SamplingFrequency", 0) or 0)
        n = int(getattr(group, "NumberOfWaveformSamples", 0) or 0)
        if fs <= 0 or n <= 0 or "MEDIAN" in label:
            continue
        seconds = n / fs
        if seconds > best_seconds:
            best, best_seconds = index, seconds
    if best is None:
        raise EcgInputError("the waveform has no rhythm group")
    if best_seconds < RECORD_SECONDS - 1e-6:
        raise EcgInputError(f"the rhythm lasts {best_seconds:.1f} s; the model needs {RECORD_SECONDS} s")
    return best


def _to_250hz(signal: np.ndarray, fs: float) -> np.ndarray:
    """[n, leads] at fs -> the first 2500 samples at 250 Hz. Integer multiples of 250 Hz keep
    every k-th sample, as the training converter did for 500 Hz MUSE files (no filter);
    other rates are resampled with an anti-aliasing filter."""
    if abs(fs - round(fs)) > 1e-6:
        raise EcgInputError(f"unsupported sampling frequency {fs} Hz")
    fs = int(round(fs))
    if fs % TARGET_FS == 0:
        out = signal[:: fs // TARGET_FS]
    else:
        g = gcd(fs, TARGET_FS)
        out = resample_poly(signal, TARGET_FS // g, fs // g, axis=0)
    if out.shape[0] < SAMPLES:
        raise EcgInputError("the rhythm is shorter than 10 s")
    return out[:SAMPLES]


def waveform_counts(dicom: pydicom.Dataset) -> tuple[np.ndarray, dict]:
    """(2500, 12) MUSE counts in LEAD_ORDER, as the training renderer reads them, and a QC dict."""
    index = _rhythm_group(dicom)
    group = dicom.WaveformSequence[index]
    try:
        values = np.asarray(dicom.waveform_array(index), dtype=np.float64)   # sensitivity + baseline applied
    except Exception as e:
        raise EcgInputError(f"cannot decode the waveform ({type(e).__name__})") from None
    channels = list(group.ChannelDefinitionSequence)
    if values.ndim != 2 or values.shape[1] != len(channels):
        raise EcgInputError("the waveform channels do not match their definitions")

    leads: dict[str, np.ndarray] = {}
    for column, channel in enumerate(channels):
        name = _lead_name(channel)
        if name is not None and name not in leads:
            leads[name] = values[:, column] * _channel_scale_to_uv(channel) / UV_PER_COUNT
    missing = [name for name in MEASURED_LEADS if name not in leads]
    if missing:
        raise EcgInputError(f"the waveform lacks lead(s) {', '.join(missing)}")

    fs = float(group.SamplingFrequency)
    measured = _to_250hz(np.stack([leads[name] for name in MEASURED_LEADS], axis=1), fs)
    by_name = dict(zip(MEASURED_LEADS, measured.T))
    lead_i, lead_ii = by_name["I"], by_name["II"]
    # The training converter (DeepECG-PNG dataset_creation/ecg_plotter/xml_converter.py)
    # always derived the limb leads from I and II, whatever else the file held.
    lead_iii = lead_ii - lead_i
    by_name.update({"III": lead_iii, "aVR": -(lead_i + lead_ii) / 2,
                    "aVF": (lead_ii + lead_iii) / 2, "aVL": (lead_i - lead_iii) / 2})
    counts = np.stack([by_name[name] for name in LEAD_ORDER], axis=1)
    if not np.isfinite(counts).all():
        raise EcgInputError("the waveform has non-finite samples")
    return counts, {"samplingFrequencyHz": fs, "resampled": int(round(fs)) % TARGET_FS != 0}


# -- PDF and image ----------------------------------------------------------------

def _pdf_page(dicom: pydicom.Dataset) -> Image.Image:
    """The page of an Encapsulated PDF, exactly as DeepECG_Docker utils/image_preprocessing.load_ecg_pdf
    makes it (extract_png_from_pdf, then open_png_bytes and the page-size check)."""
    from utils.ecg_pdf import PAGE_SIZE, EcgPdfError, extract_png_from_pdf   # pypdf: PDF inputs only
    from utils.image_preprocessing import open_png_bytes

    document = bytes(dicom.EncapsulatedDocument)
    length = getattr(dicom, "EncapsulatedDocumentLength", None)
    if length:
        document = document[: int(length)]
    handle, path = tempfile.mkstemp(suffix=".pdf")   # ecg_pdf reads a path; removed at once
    try:
        with os.fdopen(handle, "wb") as out:
            out.write(document)
        png = extract_png_from_pdf(path)
    except EcgPdfError as e:
        raise EcgInputError(f"the PDF is not a readable vector ECG: {e}") from None
    finally:
        try:
            os.remove(path)
        except OSError:
            pass
    with open_png_bytes(png) as image:
        if image.size != PAGE_SIZE:
            raise EcgInputError(f"extracted page is {image.size[0]}x{image.size[1]}, expected {PAGE_SIZE}")
        image.load()
        return image.copy()


def _image_page(dicom: pydicom.Dataset) -> Image.Image:
    """The page of a single-frame image object, decoded with the shared helper
    (utils.dicom_pixels: high-bit data windowed to 8 bit, MONOCHROME1 inverted; 8-bit data unchanged)."""
    from utils.dicom_pixels import dicom_display_pixels

    try:
        pixels = np.asarray(dicom_display_pixels(dicom))
    except Exception as e:
        raise EcgInputError(f"cannot decode the image ({type(e).__name__})") from None
    photometric = str(getattr(dicom, "PhotometricInterpretation", "MONOCHROME2")).strip().upper()
    if pixels.dtype != np.uint8:
        raise EcgInputError(f"unsupported {photometric} image of {pixels.dtype} values")
    if pixels.ndim == 2:
        return Image.fromarray(pixels, mode="L")
    if pixels.ndim == 3 and pixels.shape[-1] == 3:
        # pydicom 3 ``pixel_array`` already returns RGB for every YBR_* interpretation (``as_rgb`` defaults
        # to True; JPEG decoders apply their own colour transform), so the pixels are used as they come.
        return Image.fromarray(np.asarray(pixels, dtype=np.uint8), mode="RGB")
    raise EcgInputError("the image is not a single grayscale or RGB frame")


def ecg_page(kind: str, dicom: pydicom.Dataset) -> EcgPage:
    if kind == "waveform":
        counts, _ = waveform_counts(dicom)
        return EcgPage(render_training_page(counts), "waveform")
    if kind == "pdf":
        return EcgPage(_pdf_page(dicom), "pdf")
    if kind == "image":
        return EcgPage(_image_page(dicom), "image")
    raise EcgInputError(f"unknown input kind {kind}")
