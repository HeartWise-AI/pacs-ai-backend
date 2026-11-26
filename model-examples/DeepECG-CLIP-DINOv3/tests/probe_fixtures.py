"""Write probe folders in the layout models/probes.py reads (random weights, no patient data)."""
from __future__ import annotations

import hashlib
import json
from pathlib import Path

import torch
from safetensors.torch import save_file


def write_probe(root: Path, probe_id: str, task: str, labels: list[str], thresholds, seed: int = 0,
                scaler: bool = True, **extra) -> dict:
    generator = torch.Generator().manual_seed(seed)
    tensors = {"weight": torch.randn(len(labels), 1536, generator=generator, dtype=torch.float64) * 0.05,
               "bias": torch.randn(len(labels), generator=generator, dtype=torch.float64)}
    if scaler:
        tensors["scaler_mean"] = torch.randn(1536, generator=generator, dtype=torch.float64)
        tensors["scaler_scale"] = torch.rand(1536, generator=generator, dtype=torch.float64) + 0.5
    folder = root / probe_id
    folder.mkdir(parents=True, exist_ok=True)
    save_file(tensors, str(folder / "classifier.safetensors"))
    manifest = {"id": probe_id, "task": task, "description": f"{probe_id} probe", "labels": labels,
                "thresholds": thresholds, **extra}
    (folder / "head.json").write_text(json.dumps(manifest))
    return tensors


def write_sums(root: Path) -> None:
    lines = [f"{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.relative_to(root).as_posix()}"
             for p in sorted(root.rglob("*")) if p.is_file() and p.name != "SHA256SUMS"]
    (root / "SHA256SUMS").write_text("\n".join(lines) + "\n")


def standard_probes(root: Path) -> None:
    write_probe(root, "lvef_le_40", "binary", ["LVEF <= 40%"], [0.2], seed=1, group="lvef")
    write_probe(root, "patterns", "multilabel", ["Sinus rhythm", "Atrial fibrillation", "LBBB"], [0.5, 0.3, 0.1],
                seed=2, label_ids=["patterns.sinus", "patterns.afib", "patterns.lbbb"])
    write_probe(root, "hcm3", "multiclass", ["No HCM", "Obstructive HCM", "Non-obstructive HCM"], None, seed=3)
    write_sums(root)
