"""The view filter decides per clip, also when every clip of the study shares one series.

Synthetic headers only (no PHI). Loads logic.py without the model weights.
"""

import sys
from pathlib import Path

import pytest
from pydicom.dataset import Dataset
from pydicom.uid import generate_uid

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

logic = pytest.importorskip("logic")


def clip(series: str) -> Dataset:
    ds = Dataset()
    ds.SeriesInstanceUID = series
    ds.SOPInstanceUID = generate_uid()
    return ds


def service():
    return logic.CustomPredictionService.__new__(logic.CustomPredictionService)


def test_clips_sharing_a_series_are_filtered_by_their_own_view():
    series = generate_uid()
    a4c, subcostal, plax = clip(series), clip(series), clip(series)
    metadata = {
        logic._clip_key(a4c): {"predicted_class": "A4C"},
        logic._clip_key(subcostal): {"predicted_class": "Subcostal"},
        logic._clip_key(plax): {"predicted_class": "PLAX"},
    }
    kept = service()._filter_dicoms_with_metadata([a4c, subcostal, plax], metadata)
    assert kept == [a4c, plax]


def test_request_metadata_keyed_by_series_still_applies():
    one, other = clip(generate_uid()), clip(generate_uid())
    metadata = {str(one.SeriesInstanceUID): {"predicted_class": "A2C"}, str(other.SeriesInstanceUID): {"predicted_class": "Doppler"}}
    assert service()._filter_dicoms_with_metadata([one, other], metadata) == [one]


def test_clip_key_falls_back_to_the_series_without_an_instance_uid():
    ds = Dataset()
    ds.SeriesInstanceUID = "1.2.3"
    assert logic._clip_key(ds) == "1.2.3"
