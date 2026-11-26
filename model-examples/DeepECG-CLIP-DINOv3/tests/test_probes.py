"""Frozen linear probes are discovered from files, verified, and computed like StandardScaler + LogisticRegression."""
from __future__ import annotations

import sys
from pathlib import Path

import pytest
import torch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
sys.path.insert(0, str(Path(__file__).resolve().parent))

import probe_fixtures as pf  # noqa: E402
from models.probes import discover_probes  # noqa: E402


def test_probes_are_discovered_and_compute_scaled_logistic_outputs(tmp_path):
    pf.standard_probes(tmp_path)
    probes = {p.id: p for p in discover_probes(tmp_path)}
    assert sorted(probes) == ["hcm3", "lvef_le_40", "patterns"]
    assert [o.id for o in probes["patterns"].outputs] == ["patterns.sinus", "patterns.afib", "patterns.lbbb"]
    assert [o.id for o in probes["hcm3"].outputs] == ["hcm3.0", "hcm3.1", "hcm3.2"]

    from safetensors.torch import load_file
    t = load_file(str(tmp_path / "patterns" / "classifier.safetensors"))
    x = torch.randn(2, 1536, dtype=torch.float64)
    expected = torch.sigmoid(((x - t["scaler_mean"]) / t["scaler_scale"]) @ t["weight"].T + t["bias"])
    torch.testing.assert_close(probes["patterns"](x), expected)
    multiclass = probes["hcm3"](x)
    torch.testing.assert_close(multiclass.sum(dim=1), torch.ones(2, dtype=torch.float64))


def test_no_probe_directory_means_no_probes(tmp_path):
    assert discover_probes(tmp_path / "absent") == []


def test_a_changed_or_unlisted_file_fails_verification(tmp_path):
    pf.standard_probes(tmp_path)
    (tmp_path / "lvef_le_40" / "head.json").write_text('{"id": "lvef_le_40"}')
    with pytest.raises(ValueError, match="SHA-256 mismatch"):
        discover_probes(tmp_path)
    pf.write_sums(tmp_path)
    (tmp_path / "extra.txt").write_text("x")
    with pytest.raises(ValueError, match="does not list exactly"):
        discover_probes(tmp_path)


@pytest.mark.parametrize("change,message", [
    ({"id": "other"}, "folder name"),
    ({"task": "regression"}, "task"),
    ({"thresholds": [1.5]}, "probabilities"),
    ({"thresholds": None}, "one entry per label"),
    ({"labels": ["a", "b"]}, "binary probe has one label"),
])
def test_invalid_manifests_are_refused(tmp_path, change, message):
    pf.write_probe(tmp_path, "shd", "binary", ["SHD"], [0.1], **{})
    import json
    manifest = json.loads((tmp_path / "shd" / "head.json").read_text())
    manifest.update(change)
    (tmp_path / "shd" / "head.json").write_text(json.dumps(manifest))
    pf.write_sums(tmp_path)
    with pytest.raises(ValueError, match=message):
        discover_probes(tmp_path)


def test_wrong_weight_shape_is_refused(tmp_path):
    from safetensors.torch import save_file
    pf.write_probe(tmp_path, "shd", "binary", ["SHD"], [0.1])
    save_file({"weight": torch.zeros(1, 512), "bias": torch.zeros(1)}, str(tmp_path / "shd" / "classifier.safetensors"))
    pf.write_sums(tmp_path)
    with pytest.raises(ValueError, match="weight must be"):
        discover_probes(tmp_path)
