"""Write probe heads in the DeepECG_Docker head format (random weights, no patient data)."""
from __future__ import annotations

import json
from pathlib import Path

import torch
from safetensors.torch import save_file

ROOT = Path(__file__).resolve().parents[1]
ENCODER_CONFIG = json.loads((ROOT / "models/encoder_config.json").read_text())
FROZEN_SHA256 = json.loads((ROOT / "models/config.json").read_text())["FrozenEncoder"]["sha256"]
DIM = 1536


def write_head(root: Path, key: str, outputs: list[str], thresholds: dict, seed: int = 0,
               standardize: bool = True, encoder_sha256: str = FROZEN_SHA256) -> dict:
    """One head folder, as DeepECG_Docker publishes them (e.g. heartwise/SigLIP2_SHD)."""
    generator = torch.Generator().manual_seed(seed)
    tensors = {"weight": torch.randn(len(outputs), DIM, generator=generator) * 0.05,
               "bias": torch.randn(len(outputs), generator=generator)}
    shapes = {"weight": [len(outputs), DIM], "bias": [len(outputs)]}
    if standardize:
        tensors["mean"] = torch.randn(DIM, generator=generator)
        tensors["scale"] = torch.rand(DIM, generator=generator) + 0.5
        shapes.update({"mean": [DIM], "scale": [DIM]})
    folder = root / key
    folder.mkdir(parents=True, exist_ok=True)
    save_file(tensors, str(folder / "classifier.safetensors"))
    head = {
        "name": f"heartwise/DINOv3_{key}",
        "task": f"{key} (synthetic test head)",
        "outputs": outputs,
        "encoder": {"repo": "heartwise/deepecg_clip_dinov3_acs", "run": "zo59vdxk",
                    "file": "acs_ft20_dinov3_siglip2_epoch030/base/epoch_030.pt", "sha256": encoder_sha256,
                    "feature": ENCODER_CONFIG["feature"]},
        "classifier": {"file": "classifier.safetensors", "type": "linear", "tensors": shapes, "dtype": "float32",
                       "input_dim": DIM, "standardize": standardize, "activation": "sigmoid"},
        "decision": "binary: probability >= thresholds.json cutoff" if thresholds else "probabilities only",
    }
    (folder / "head.json").write_text(json.dumps(head))
    (folder / "thresholds.json").write_text(json.dumps({"thresholds": thresholds}))
    (folder / "config.json").write_text(json.dumps(ENCODER_CONFIG))
    return tensors


def standard_heads(root: Path) -> None:
    write_head(root, "lvef_le_40", ["LVEF_le40"], {"LVEF_le40": 0.2}, seed=1)
    write_head(root, "patterns", ["Sinusal", "Afib", "Left bundle branch block"], {}, seed=2, standardize=False)
    write_head(root, "shd", ["SHD"], {"SHD": 0.5}, seed=3)
