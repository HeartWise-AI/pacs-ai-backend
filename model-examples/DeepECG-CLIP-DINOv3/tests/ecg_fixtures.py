"""Synthetic ECG DICOM objects for the tests (no patient data)."""
from __future__ import annotations

import io

import numpy as np
import pydicom
from pydicom.dataset import Dataset, FileDataset, FileMetaDataset
from pydicom.uid import ExplicitVRLittleEndian, generate_uid

SCP_ECG_CODES = {"I": "5.6.3-9-1", "II": "5.6.3-9-2", "III": "5.6.3-9-61", "aVR": "5.6.3-9-62",
                 "aVL": "5.6.3-9-63", "aVF": "5.6.3-9-64", "V1": "5.6.3-9-3", "V2": "5.6.3-9-4",
                 "V3": "5.6.3-9-5", "V4": "5.6.3-9-6", "V5": "5.6.3-9-7", "V6": "5.6.3-9-8"}
TWELVE_LEAD = "1.2.840.10008.5.1.4.1.1.9.1.1"
ENCAPSULATED_PDF = "1.2.840.10008.5.1.4.1.1.104.1"
SECONDARY_CAPTURE = "1.2.840.10008.5.1.4.1.1.7"


def synthetic_counts(fs: int = 500, seconds: float = 10.0, seed: int = 0) -> dict[str, np.ndarray]:
    """I, II and V1-V6 as integer MUSE counts (4.88 uV each): spiky beats plus baseline drift."""
    rng = np.random.default_rng(seed)
    t = np.arange(int(fs * seconds)) / fs
    beat = sum(np.exp(-((t - c) ** 2) / (2 * 0.012 ** 2)) for c in np.arange(0.4, seconds, 0.85))
    leads = {}
    for k, name in enumerate(("I", "II", "V1", "V2", "V3", "V4", "V5", "V6")):
        sign = 1 if k % 3 else -1
        drift = 40 * np.sin(2 * np.pi * (0.3 + 0.05 * k) * t + rng.uniform(0, 6.28))
        leads[name] = np.round(sign * (150 + 20 * k) * beat + drift)
    return leads


def _base(sop_class: str, modality: str) -> FileDataset:
    meta = FileMetaDataset()
    meta.MediaStorageSOPClassUID = sop_class
    meta.MediaStorageSOPInstanceUID = generate_uid()
    meta.TransferSyntaxUID = ExplicitVRLittleEndian
    ds = FileDataset(None, {}, file_meta=meta, preamble=b"\0" * 128)
    ds.SOPClassUID = sop_class
    ds.SOPInstanceUID = meta.MediaStorageSOPInstanceUID
    ds.StudyInstanceUID = generate_uid()
    ds.SeriesInstanceUID = generate_uid()
    ds.Modality = modality
    ds.PatientID = "SYNTHETIC"
    return ds


def waveform_dicom(leads: dict[str, np.ndarray], fs: int = 500, sensitivity: float = 4.88,
                   unit: str = "uV", label: str = "RHYTHM", median: bool = False,
                   acquisition: str = "20260101120000") -> FileDataset:
    """A 12-lead ECG waveform object holding `leads` (counts) with the given sensitivity."""
    ds = _base(TWELVE_LEAD, "ECG")
    ds.AcquisitionDateTime = acquisition
    groups = []
    if median:   # a short median-beat group first, as MUSE exports have
        groups.append(_group({k: v[: int(1.2 * fs)] for k, v in leads.items()}, fs, sensitivity, unit, "MEDIAN"))
    groups.append(_group(leads, fs, sensitivity, unit, label))
    ds.WaveformSequence = groups
    return ds


def _group(leads, fs, sensitivity, unit, label) -> Dataset:
    names = list(leads)
    data = np.stack([np.asarray(leads[n], dtype=np.int16) for n in names], axis=1)
    group = Dataset()
    group.MultiplexGroupLabel = label
    group.WaveformOriginality = "ORIGINAL"
    group.NumberOfWaveformChannels = len(names)
    group.NumberOfWaveformSamples = data.shape[0]
    group.SamplingFrequency = fs
    group.WaveformBitsAllocated = 16
    group.WaveformSampleInterpretation = "SS"
    channels = []
    for name in names:
        channel = Dataset()
        source = Dataset()
        source.CodeValue = SCP_ECG_CODES[name]
        source.CodingSchemeDesignator = "SCPECG"
        source.CodeMeaning = f"Lead {name}"
        channel.ChannelSourceSequence = [source]
        channel.ChannelSensitivity = sensitivity
        units = Dataset()
        units.CodeValue = unit
        units.CodingSchemeDesignator = "UCUM"
        units.CodeMeaning = unit
        channel.ChannelSensitivityUnitsSequence = [units]
        channel.ChannelSensitivityCorrectionFactor = 1
        channel.ChannelBaseline = 0
        channel.WaveformBitsStored = 16
        channels.append(channel)
    group.ChannelDefinitionSequence = channels
    group.WaveformData = data.tobytes()
    return group


PT_PER_MM = 72 / 25.4
STANDARD_ROWS = (("I", "aVR", "V1", "V4"), ("II", "aVL", "V2", "V5"), ("III", "aVF", "V3", "V6"))


def synthetic_leads_mv(seed: int = 0, fs: int = 500):
    """12 leads (mV) over 10 s; the limb leads obey Einthoven/Goldberger."""
    rng = np.random.default_rng(seed)
    t = np.arange(0, 10.0, 1 / fs)

    def wave():
        out = np.zeros_like(t)
        for f in rng.uniform(0.7, 6.0, size=4):
            out += rng.uniform(0.05, 0.25) * np.sin(2 * np.pi * f * t + rng.uniform(0, 6.28))
        return out

    lead_i, lead_ii = wave(), wave()
    leads = {"I": lead_i, "II": lead_ii, "III": lead_ii - lead_i, "aVR": -(lead_i + lead_ii) / 2,
             "aVL": lead_i - lead_ii / 2, "aVF": lead_ii - lead_i / 2}
    for v in ("V1", "V2", "V3", "V4", "V5", "V6"):
        leads[v] = wave()
    return t, leads


def vector_ecg_pdf(t, leads, fs: int = 500, speed: float = 25.0, gain: float = 10.0) -> bytes:
    """A vector ECG PDF in the standard 3x4 + lead II rhythm layout, drawn the way vendor exports
    are (red millimetre grid, calibration pulses, lead labels, scale text). Trimmed from
    DeepECG_Docker tests/test_pdf_inputs.py (make_ecg_pdf, unrotated page)."""
    from pypdf import PdfWriter
    from pypdf.generic import DecodedStreamObject, DictionaryObject, NameObject

    ops = []
    t0, bases = 60.0, [470.0, 365.0, 260.0, 155.0]
    pt_s, pt_mv = speed * PT_PER_MM, gain * PT_PER_MM

    def line(points, rgb, width):
        ops.append(f"q {rgb[0]} {rgb[1]} {rgb[2]} RG {width} w {points[0][0]:.4f} {points[0][1]:.4f} m "
                   + " ".join(f"{x:.4f} {y:.4f} l" for x, y in points[1:]) + " S Q")

    x_lo, x_hi, y_lo, y_hi = 30.0, 770.0, 100.0, 520.0
    for k in range(int((x_hi - x_lo) / PT_PER_MM) + 1):
        x = x_lo + k * PT_PER_MM
        major = k % 5 == 0
        line([(x, y_lo), (x, y_hi)], (0.883, 0.6, 0.557) if major else (1.0, 0.752, 0.797), 0.24 if major else 0.048)
    for k in range(int((y_hi - y_lo) / PT_PER_MM) + 1):
        y = y_lo + k * PT_PER_MM
        major = k % 5 == 0
        line([(x_lo, y), (x_hi, y)], (0.883, 0.6, 0.557) if major else (1.0, 0.752, 0.797), 0.24 if major else 0.048)
    height = gain * PT_PER_MM
    for b in bases:
        x0 = t0 - 20
        line([(x0, b), (x0 + 1, b), (x0 + 1, b + height), (x0 + 6 * PT_PER_MM, b + height),
              (x0 + 6 * PT_PER_MM, b), (x0 + 7 * PT_PER_MM, b)], (0, 0, 0), 0.48)

    def piece(sig, b, lo, hi):
        sel = (t >= lo - 1e-9) & (t <= hi + 1e-9)
        line([(t0 + tt * pt_s, b + vv * pt_mv) for tt, vv in zip(t[sel], sig[sel])], (0, 0, 0), 0.48)

    for r in range(3):
        for c in range(4):
            for k in range(3):
                lo = c * 2.5 + k
                piece(leads[STANDARD_ROWS[r][c]], bases[r], lo, min(lo + 1.0, (c + 1) * 2.5 - 1 / fs))
    for k in range(10):
        piece(leads["II"], bases[3], k, min(k + 1.0, 10.0 - 1 / fs))

    def text(x, y, s):
        ops.append(f"BT /F1 9 Tf 1 0 0 1 {x:.3f} {y:.3f} Tm ({s}) Tj ET")

    names = {(r, c): STANDARD_ROWS[r][c] for r in range(3) for c in range(4)}
    names[(3, 0)] = "II"
    for (r, c), s in names.items():
        text(t0 + c * 2.5 * pt_s + 2, bases[r] + 0.6 * pt_mv, s)
    text(40, 60, f"{speed:g} mm/s {gain:g} mm/mV 0.05-150 Hz")

    writer = PdfWriter()
    font = writer._add_object(DictionaryObject({NameObject("/Type"): NameObject("/Font"),
                                                NameObject("/Subtype"): NameObject("/Type1"),
                                                NameObject("/BaseFont"): NameObject("/Helvetica")}))
    page = writer.add_blank_page(width=792.0, height=595.0)
    page[NameObject("/Resources")] = DictionaryObject({NameObject("/Font"): DictionaryObject({NameObject("/F1"): font})})
    stream = DecodedStreamObject()
    stream.set_data("\n".join(ops).encode("latin-1"))
    page[NameObject("/Contents")] = writer._add_object(stream)
    buffer = io.BytesIO()
    writer.write(buffer)
    return buffer.getvalue()


def pdf_dicom(pdf_bytes: bytes) -> FileDataset:
    ds = _base(ENCAPSULATED_PDF, "DOC")
    ds.MIMETypeOfEncapsulatedDocument = "application/pdf"
    ds.EncapsulatedDocument = pdf_bytes + (b"\0" if len(pdf_bytes) % 2 else b"")
    ds.EncapsulatedDocumentLength = len(pdf_bytes)
    return ds


def image_dicom(pixels: np.ndarray) -> FileDataset:
    ds = _base(SECONDARY_CAPTURE, "OT")
    ds.Rows, ds.Columns = pixels.shape[:2]
    if pixels.ndim == 3:
        ds.SamplesPerPixel, ds.PhotometricInterpretation, ds.PlanarConfiguration = 3, "RGB", 0
    else:
        ds.SamplesPerPixel, ds.PhotometricInterpretation = 1, "MONOCHROME2"
    ds.BitsAllocated = ds.BitsStored = 8
    ds.HighBit = 7
    ds.PixelRepresentation = 0
    ds.PixelData = np.ascontiguousarray(pixels, dtype=np.uint8).tobytes()
    return ds


def roundtrip(ds: FileDataset) -> pydicom.Dataset:
    """The object as the service receives it: written to bytes and read back."""
    buffer = io.BytesIO()
    ds.save_as(buffer, enforce_file_format=True)
    return pydicom.dcmread(io.BytesIO(buffer.getvalue()))


def dicom_bytes(ds: FileDataset) -> bytes:
    buffer = io.BytesIO()
    ds.save_as(buffer, enforce_file_format=True)
    return buffer.getvalue()
