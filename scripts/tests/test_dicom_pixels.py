"""Shared DICOM pixel conversion: canonical behaviour and per-container copies."""

import importlib.util
import re
import unittest
from pathlib import Path

import numpy as np

REPOSITORY_ROOT = Path(__file__).resolve().parents[2]
CANONICAL = REPOSITORY_ROOT / "model-template" / "utils" / "dicom_pixels.py"

# Containers that decode DICOM pixel data themselves and must use the shared helper.
CONVERTING_MODELS = [
    "CATH-EF",
    "CathEF-CLIP",
    "DeepCORO-CTO",
    "DeepCORO-MACE",
    "DeepCORO-SCAD",
    "DeepCORO-SYNTAX",
    "DeepCoro_CLIP/cardio_syntax",
    "DeepCoro_CLIP/generic",
    "DeepCoro_CLIP/procedure_view_classifier",
    "DeepRV",
    "DeepRV-CLIP",
    "ECHO-PRIME/generic",
    "ECHO-PRIME/view_classifier",
    "PanEcho",
    "med_gemma_1.5",
]

# Files that read ``pixel_array`` without the helper, and why that is correct.
EXEMPT_PIXEL_READERS = {
    # CT volume: per-slice min-max of the float stored values; already high-bit safe.
    "BrainGPT_v1/pipeline_script.py",
    # Reads pixel_array only to return the raw buffer; the caller decodes it again.
    "ECHO-PRIME/generic/utils/video_utils.py",
    "ECHO-PRIME/view_classifier/models/utils.py",
    "PanEcho/models/view_classifier_utils.py",
}

spec = importlib.util.spec_from_file_location("dicom_pixels", CANONICAL)
dicom_pixels = importlib.util.module_from_spec(spec)
spec.loader.exec_module(dicom_pixels)


def reference_window(raw: np.ndarray) -> np.ndarray:
    values = raw.astype(np.float32)
    low, high = np.percentile(values, 0.5), np.percentile(values, 99.5)
    return (np.clip((values - low) / max(high - low, 1e-6), 0, 1) * 255).astype(np.uint8)


class DicomPixelHelperTest(unittest.TestCase):
    def setUp(self):
        rng = np.random.default_rng(7)
        self.eight_bit = rng.integers(0, 256, (4, 12, 10), dtype=np.uint8)
        self.twelve_bit = rng.integers(0, 4096, (4, 12, 10), dtype=np.uint16)

    def test_eight_bit_monochrome2_and_colour_are_returned_unchanged(self):
        for interpretation in ("MONOCHROME2", "RGB", "YBR_FULL", "YBR_FULL_422", "PALETTE COLOR", None):
            with self.subTest(interpretation=interpretation):
                result = dicom_pixels.to_display_uint8(self.eight_bit, interpretation)
                self.assertIs(result, self.eight_bit)
                self.assertEqual(result.tobytes(), self.eight_bit.tobytes())

    def test_high_bit_monochrome2_is_windowed_to_uint8(self):
        result = dicom_pixels.to_display_uint8(self.twelve_bit, "MONOCHROME2")
        self.assertEqual(result.dtype, np.uint8)
        np.testing.assert_array_equal(result, reference_window(self.twelve_bit))
        self.assertEqual((int(result.min()), int(result.max())), (0, 255))

    def test_monochrome1_is_inverted_after_windowing(self):
        np.testing.assert_array_equal(
            dicom_pixels.to_display_uint8(self.eight_bit, "MONOCHROME1"), 255 - self.eight_bit
        )
        np.testing.assert_array_equal(
            dicom_pixels.to_display_uint8(self.twelve_bit, "MONOCHROME1"),
            255 - reference_window(self.twelve_bit),
        )

    def test_signed_and_rescaled_values_are_windowed(self):
        signed = self.twelve_bit.astype(np.int16) - 2048
        np.testing.assert_array_equal(
            dicom_pixels.to_display_uint8(signed, "MONOCHROME2"), reference_window(self.twelve_bit)
        )

    def test_constant_image_does_not_divide_by_zero(self):
        flat = np.full((2, 3, 3), 1000, dtype=np.uint16)
        self.assertTrue(np.all(dicom_pixels.to_display_uint8(flat, "MONOCHROME2") == 0))

    def test_high_bit_palette_colour_is_left_to_the_colour_lut(self):
        self.assertIs(dicom_pixels.to_display_uint8(self.twelve_bit, "PALETTE COLOR"), self.twelve_bit)


class DicomPixelCopiesTest(unittest.TestCase):
    def test_every_converting_container_carries_the_canonical_helper(self):
        canonical = CANONICAL.read_text(encoding="utf-8")
        for model in CONVERTING_MODELS:
            with self.subTest(model=model):
                copy = REPOSITORY_ROOT / "model-examples" / model / "utils" / "dicom_pixels.py"
                self.assertTrue(copy.is_file())
                self.assertEqual(canonical, copy.read_text(encoding="utf-8"))

    def test_every_helper_copy_belongs_to_a_listed_container(self):
        copies = sorted((REPOSITORY_ROOT / "model-examples").glob("**/utils/dicom_pixels.py"))
        models = {str(path.parent.parent.relative_to(REPOSITORY_ROOT / "model-examples")) for path in copies}
        self.assertEqual(models, set(CONVERTING_MODELS))

    def test_pixel_array_readers_use_the_helper_or_are_exempt(self):
        pattern = re.compile(r"\.pixel_array\b")
        for path in sorted((REPOSITORY_ROOT / "model-examples").glob("**/*.py")):
            relative = str(path.relative_to(REPOSITORY_ROOT / "model-examples"))
            if "/tests/" in relative or relative.endswith("utils/dicom_pixels.py"):
                continue
            source = path.read_text(encoding="utf-8")
            if not pattern.search(source) or relative in EXEMPT_PIXEL_READERS:
                continue
            with self.subTest(file=relative):
                self.assertIn("utils.dicom_pixels", source)


if __name__ == "__main__":
    unittest.main()
