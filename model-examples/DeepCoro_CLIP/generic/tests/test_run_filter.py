"""Run selection rules (no weights needed): only diagnostic L/R coronary contrast runs are scored."""

import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
pytest.importorskip("torch")

from models.run_filter import assign_status, keep_mask  # noqa: E402


def run(structure, contrast="yes", stent="absent"):
    return {"main_structure": structure, "contrast_agent": contrast, "stent_presence": stent, "dominance": "right_dominant"}


def test_post_pci_runs_of_the_stented_artery_are_dropped_but_other_arteries_kept():
    runs = [run("Left Coronary"), run("Right Coronary"), run("Left Coronary", stent="present"),
            run("Left Coronary"), run("Right Coronary")]
    times = [1.0, 2.0, 3.0, 4.0, 5.0]
    assert assign_status(runs, times) == ["diagnostic", "diagnostic", "PCI", "POST_PCI", "diagnostic"]
    assert keep_mask(runs, times) == [True, True, False, False, True]


def test_status_follows_series_time_not_upload_order():
    runs = [run("Left Coronary"), run("Left Coronary", stent="present")]
    assert keep_mask(runs, [10.0, 5.0]) == [False, False]  # the stented run came first in time


def test_non_coronary_non_contrast_and_unreadable_runs_are_dropped():
    runs = [run("LV"), run("Right Coronary", contrast="no"), None, run("Graft"), run("Right Coronary")]
    assert keep_mask(runs, [1.0, 2.0, 3.0, 4.0, 5.0]) == [False, False, False, False, True]


def test_run_filter_is_pinned():
    spec = json.loads((ROOT / "models/run_filter.json").read_text(encoding="utf-8"))
    assert spec["repository"] == "heartwise/VasoVision"
    assert len(spec["revision"]) == 40 and len(spec["weightsSha256"]) == 64
    dockerfile = (ROOT / "Dockerfile").read_text(encoding="utf-8")
    assert "python download_run_filter.py --token-file /run/secrets/hf_token" in dockerfile
