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


VIEW: dict[str, int] = {}  # clip -> class index the fake classifier returns


class _FakeClassifier:
    """Returns the class index written in each clip's tensor, so every clip gets its own view."""

    def __call__(self, batch):
        import torch

        idx = batch.reshape(batch.shape[0], -1)[:, 0].long()
        return torch.nn.functional.one_hot(idx, num_classes=3).float() * 10


def test_each_clip_of_a_single_series_study_is_classified_on_its_own(monkeypatch):
    import torch

    service_cls = logic.CustomPredictionService
    monkeypatch.setitem(service_cls.models, "view_classifier", _FakeClassifier())
    monkeypatch.setattr(service_cls, "_view_classifier_class_mapping", {"0": "A4C", "1": "Subcostal", "2": "PLAX"}, raising=False)
    monkeypatch.setattr(service_cls, "_prepare_view_classifier_video",
                        lambda self, dicom: (torch.full((3, 2, 4, 4), float(VIEW[dicom.SOPInstanceUID])), "success"))

    series = generate_uid()
    clips = []
    for view in (0, 2, 1):  # A4C, PLAX, then a subcostal clip last, which used to decide for the whole series
        ds = clip(series)
        VIEW[ds.SOPInstanceUID] = view
        clips.append(ds)

    svc = service()
    metadata = svc._generate_view_classifier_metadata(clips)
    assert len(metadata) == 3
    assert [metadata[logic._clip_key(c)]["predicted_class"] for c in clips] == ["A4C", "PLAX", "Subcostal"]
    assert svc._filter_dicoms_with_metadata(clips, metadata) == clips[:2]


def test_a_single_series_study_is_not_dropped_when_its_last_clip_is_off_view(monkeypatch):
    import torch

    service_cls = logic.CustomPredictionService
    monkeypatch.setitem(service_cls.models, "view_classifier", _FakeClassifier())
    monkeypatch.setattr(service_cls, "_view_classifier_class_mapping", {"0": "A4C", "1": "Subcostal", "2": "PLAX"}, raising=False)
    monkeypatch.setattr(service_cls, "_prepare_view_classifier_video",
                        lambda self, dicom: (torch.full((3, 2, 4, 4), float(VIEW[dicom.SOPInstanceUID])), "success"))
    series = generate_uid()
    clips = []
    for view in (0, 0, 2, 1):
        ds = clip(series)
        VIEW[ds.SOPInstanceUID] = view
        clips.append(ds)
    svc = service()
    kept = svc._filter_dicoms_with_metadata(clips, svc._generate_view_classifier_metadata(clips))
    assert kept == clips[:3]
