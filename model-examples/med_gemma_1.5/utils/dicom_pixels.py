"""Shared conversion of decoded DICOM pixel data to 8-bit display values.

Canonical copy: ``model-template/utils/dicom_pixels.py``. Each inference container keeps an
identical copy in its own ``utils/`` directory (containers are self-contained);
``scripts/tests/test_dicom_pixels.py`` fails if a copy drifts.

The models were trained on 8-bit videos. ``pixel_array`` from 10/12/16-bit DICOMs is
``uint16`` and was previously written straight to OpenCV video writers or cast with
``astype(np.uint8)``, which corrupts or wraps the values, and MONOCHROME1 (inverted
grayscale) input was not inverted. This module applies the conversion used by the
DeepCORO-SCAD training intake:

* data that is not already ``uint8`` is windowed to the clip-level 0.5-99.5 percentile range
  and scaled to ``uint8``. A positive linear Modality LUT (RescaleSlope/Intercept) does not
  change the result, so stored values are used directly;
* MONOCHROME1 data is inverted so that higher values are brighter, as in MONOCHROME2.

``uint8`` input in any other photometric interpretation (MONOCHROME2, RGB, YBR_*, PALETTE
COLOR) is returned unchanged, as the same array object, so existing 8-bit behaviour is
byte-identical. Not for CT/MR models that rely on Hounsfield units.
"""

from __future__ import annotations

from typing import Any, Optional

import numpy as np

LOW_PERCENTILE = 0.5
HIGH_PERCENTILE = 99.5
_RESCALED_INTERPRETATIONS = {"MONOCHROME1", "MONOCHROME2", "RGB", None}


def to_display_uint8(pixels: np.ndarray, photometric_interpretation: Optional[str] = None) -> np.ndarray:
    """Return ``pixels`` as 8-bit display values (see module docstring)."""
    interpretation = str(photometric_interpretation).strip().upper() if photometric_interpretation else None
    if pixels.dtype != np.uint8 and interpretation in _RESCALED_INTERPRETATIONS:
        values = pixels.astype(np.float32)
        low = float(np.percentile(values, LOW_PERCENTILE))
        high = float(np.percentile(values, HIGH_PERCENTILE))
        pixels = (np.clip((values - low) / max(high - low, 1e-6), 0, 1) * 255).astype(np.uint8)
    if interpretation == "MONOCHROME1" and pixels.dtype == np.uint8:
        pixels = 255 - pixels
    return pixels


def dicom_display_pixels(dicom: Any) -> np.ndarray:
    """Decode ``dicom.pixel_array`` and convert it with :func:`to_display_uint8`."""
    return to_display_uint8(dicom.pixel_array, getattr(dicom, "PhotometricInterpretation", None))
