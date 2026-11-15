"""Synthetic classifier contract tests; no weights, GPU or patient data."""

import asyncio
import hashlib
import sys
from pathlib import Path
from types import SimpleNamespace

import pytest
import torch
from pydicom.dataset import Dataset

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from logic import CustomPredictionService
from utils.diagnostic_contract import verified_preprocessing_contract


def prediction(uid, timestamp, stent="absent", main="Left Coronary", contrast="yes"):
    return {"sop_instance_uid": uid, "series_time": timestamp, "stent_presence": stent,
            "main_structure": main, "contrast_agent": contrast}


def test_complete_study_chronology_accepts_times_without_fraction():
    service = CustomPredictionService()
    rows = [prediction("3", "12:02:00"), prediction("1", "12:00:00"),
            prediction("2", "12:01:00.123", "present"),
            prediction("4", "12:03:00", main="Right Coronary")]
    result = service._assign_procedure_status(rows)
    assert [r["sop_instance_uid"] for r in result] == ["1", "2", "3", "4"]
    assert [r["status"] for r in result] == ["diagnostic", "PCI", "POST_PCI", "diagnostic"]


@pytest.mark.parametrize("timestamp", [None, "", "12:00", "120000", "25:00:00"])
def test_incomplete_or_invalid_time_does_not_become_diagnostic(timestamp):
    with pytest.raises(ValueError):
        CustomPredictionService()._assign_procedure_status([prediction("1", timestamp)])


def test_ambiguous_tied_pci_timing_fails_closed():
    with pytest.raises(ValueError, match="Ambiguous PCI timing"):
        CustomPredictionService()._assign_procedure_status([
            prediction("1", "12:00:00", "present"), prediction("2", "12:00:00")])


def test_forward_batch_is_one_and_repeated_series_preserves_sop_identity(monkeypatch):
    service = CustomPredictionService()
    monkeypatch.setattr(torch.cuda, "is_available", lambda: False)
    monkeypatch.setattr(CustomPredictionService, "config", SimpleNamespace(
        head_structure=SimpleNamespace(main_structure=2, contrast_agent=1, stent_presence=1)), raising=False)
    monkeypatch.setattr(CustomPredictionService, "class_mapping", {
        "main_structure": {0: "Left Coronary", 1: "Right Coronary"},
        "contrast_agent": {0: "no", 1: "yes"},
        "stent_presence": {0: "absent", 1: "present"}}, raising=False)
    calls = []

    def model(batch):
        calls.append(tuple(batch.shape))
        return {"main_structure": torch.tensor([[2., 0.]]),
                "contrast_agent": torch.tensor([[0.]]),
                "stent_presence": torch.tensor([[-1.]])}

    monkeypatch.setattr(CustomPredictionService, "models", {"vaso_vision": model})
    monkeypatch.setattr(service, "_process_dicom", lambda _: torch.zeros(3, 2, 4, 4))
    dicoms = []
    for i in range(3):
        d = Dataset()
        d.SeriesInstanceUID, d.SOPInstanceUID, d.SeriesTime = "1.2.3", f"1.2.3.{i+1}", f"12000{i}"
        dicoms.append(d)
    results = service._run_inference(dicoms, ["1.2.3"] * 3)
    assert calls == [(1, 3, 2, 4, 4)] * 3
    assert [r["sop_instance_uid"] for r in results] == ["1.2.3.1", "1.2.3.2", "1.2.3.3"]
    assert all(r["series_instance_uid"] == r["dicom_name"] == "1.2.3" for r in results)
    assert all(r["contrast_agent"] == "yes" for r in results), "binary threshold remains >= 0.5"


def test_dominance_returns_label_and_left_probability(monkeypatch):
    service = CustomPredictionService()
    monkeypatch.setattr(torch.cuda, "is_available", lambda: False)
    monkeypatch.setattr(CustomPredictionService, "config", SimpleNamespace(head_structure=SimpleNamespace(
        main_structure=2, contrast_agent=1, stent_presence=1, dominance=1)), raising=False)
    monkeypatch.setattr(CustomPredictionService, "class_mapping", {
        "main_structure": {0: "Left Coronary", 1: "Right Coronary"}, "contrast_agent": {0: "no", 1: "yes"},
        "stent_presence": {0: "absent", 1: "present"},
        "dominance": {0: "right_dominant", 1: "left_dominant"}}, raising=False)
    logits = iter([-2.0, 0.0])  # P(left) 0.119 -> right; P(left) 0.5 -> left (threshold unchanged at >= 0.5)
    monkeypatch.setattr(CustomPredictionService, "models", {"vaso_vision": lambda batch: {
        "main_structure": torch.tensor([[2., 0.]]), "contrast_agent": torch.tensor([[1.]]),
        "stent_presence": torch.tensor([[-1.]]), "dominance": torch.tensor([[next(logits)]])}})
    monkeypatch.setattr(service, "_process_dicom", lambda _: torch.zeros(3, 2, 4, 4))
    dicoms = []
    for i in range(2):
        d = Dataset()
        d.SeriesInstanceUID, d.SOPInstanceUID, d.SeriesTime = "1.2.3", f"1.2.3.{i+1}", f"12000{i}"
        dicoms.append(d)
    results = service._run_inference(dicoms, ["1.2.3"] * 2)
    assert [r["dominance"] for r in results] == ["right_dominant", "left_dominant"]
    assert [r["dominance_left_probability"] for r in results] == [0.119203, 0.5]


def test_invalid_input_cannot_return_a_partial_success():
    with pytest.raises(ValueError, match="valid, uniquely identified"):
        asyncio.run(CustomPredictionService()._handle_json_output(
            SimpleNamespace(seriesInstanceImages={0: {0: "invalid-base64"}})))


def test_unknown_head_labels_cannot_be_assigned_diagnostic_status():
    row = prediction("1", "12:00:00")
    row["stent_presence"] = None
    with pytest.raises(ValueError, match="complete procedure label"):
        CustomPredictionService()._assign_procedure_status([row])


def test_loaded_model_is_not_loaded_again(monkeypatch):
    monkeypatch.setattr(CustomPredictionService, "is_initialized", True)
    CustomPredictionService().load_model(None)


def test_artifact_contract_requires_actual_hashes_and_pinned_source(tmp_path):
    models = tmp_path / "models"
    models.mkdir()
    (models / "weights.pt").write_bytes(b"synthetic weights")
    (models / "mapping.json").write_bytes(b"synthetic mapping")
    contract = {"schemaVersion": "angiogram-procedure-view-sop-v1",
                "weightsSha256": hashlib.sha256(b"synthetic weights").hexdigest(),
                "classMappingSha256": hashlib.sha256(b"synthetic mapping").hexdigest()}
    verified = verified_preprocessing_contract(tmp_path, contract, "weights.pt", "mapping.json", "a" * 40)
    assert verified["sourceRevision"] == "a" * 40
    (models / "weights.pt").write_bytes(b"changed")
    with pytest.raises(ValueError, match="does not match"):
        verified_preprocessing_contract(tmp_path, contract, "weights.pt", "mapping.json", "a" * 40)
    with pytest.raises(ValueError, match="source revision"):
        verified_preprocessing_contract(tmp_path, contract, "weights.pt", "mapping.json", None)


def test_json_response_keeps_labels_as_text_and_the_dominance_probability_as_a_number():
    from utils.http_utils import JsonPredictionResponse
    row = {**prediction("1", "12:00:00"), "status": "diagnostic", "dominance": "left_dominant",
           "dominance_left_probability": 0.814933}
    out = JsonPredictionResponse(predictions=[row]).model_dump()["predictions"][0]
    assert out["dominance_left_probability"] == 0.814933 and out["dominance"] == "left_dominant"
    assert out["series_time"] == "12:00:00" and isinstance(out["main_structure"], str)
