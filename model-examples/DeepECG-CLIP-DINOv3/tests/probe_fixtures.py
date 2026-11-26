"""Write probe heads in the DeepECG_Docker DINOv3 bank format (random weights, no patient data)."""
from __future__ import annotations

import json
import sys
from pathlib import Path

import torch
from safetensors.torch import save_file

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from models import dinov3_bank as bank  # noqa: E402

ENCODER_CONFIG = json.loads((ROOT / "models/encoder_config.json").read_text())
FROZEN_SHA256 = json.loads((ROOT / "models/config.json").read_text())["FrozenEncoder"]["sha256"]
PROBE_CONFIG = json.loads((Path(__file__).parent / "probe_config.json").read_text())   # the release's probes/config.json
DIM = 1536
PATTERNS = [f"Pattern {i:02d}" for i in range(77)]   # stand-ins: the bank checks 77 unique names


def probe_config(encoder_sha256: str = FROZEN_SHA256) -> dict:
    config = json.loads(json.dumps(PROBE_CONFIG))
    config["checkpoint"]["sha256"] = encoder_sha256
    return config


def write_head(root: Path, key: str, outputs: list[str], thresholds: dict, seed: int = 0,
               standardize: bool = True, encoder_sha256: str = FROZEN_SHA256,
               activation: str = "sigmoid") -> dict:
    """One head folder, as the DeepECG_Docker DINOv3 bank reads them (and <root>/config.json)."""
    generator = torch.Generator().manual_seed(seed)
    tensors = {"weight": torch.randn(len(outputs), DIM, generator=generator) * 0.05,
               "bias": torch.randn(len(outputs), generator=generator)}
    shapes = {"weight": [len(outputs), DIM], "bias": [len(outputs)]}
    if standardize:
        tensors["mean"] = torch.randn(DIM, generator=generator)
        tensors["scale"] = torch.rand(DIM, generator=generator) + 0.5
        shapes.update({"mean": [DIM], "scale": [DIM]})
    root.mkdir(parents=True, exist_ok=True)
    config = probe_config(encoder_sha256)
    (root / "config.json").write_text(json.dumps(config))
    folder = root / key
    folder.mkdir(parents=True, exist_ok=True)
    save_file(tensors, str(folder / "classifier.safetensors"))
    head = {
        "name": f"{bank.REPO_ID}/probes/{key}",
        "task": f"{key} (synthetic test head)",
        "outputs": outputs,
        "encoder": {"repo": bank.REPO_ID, "run": "zo59vdxk", "file": bank.ENCODER_FILE, "sha256": encoder_sha256},
        "classifier": {"file": "classifier.safetensors", "type": "linear", "tensors": shapes, "dtype": "float32",
                       "input_dim": DIM, "standardize": standardize, "activation": activation},
        "decision": "binary: probability >= thresholds.json cutoff" if thresholds else "probabilities only",
    }
    (folder / "head.json").write_text(json.dumps(head))
    (folder / "thresholds.json").write_text(json.dumps({"thresholds": thresholds}))
    (folder / "config.json").write_text(json.dumps(config))
    return tensors


def standard_heads(root: Path) -> None:
    write_head(root, "ecg77_clean", PATTERNS, {}, seed=2, standardize=False)
    write_head(root, "lvef_le40_clean", ["LVEF<=40"], {"LVEF<=40": 0.2}, seed=1)
    write_head(root, "shd_clean", ["SHD"], {"SHD": 0.5}, seed=3)
