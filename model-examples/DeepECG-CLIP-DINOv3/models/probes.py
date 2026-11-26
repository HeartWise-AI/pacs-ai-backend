"""Frozen linear-probe heads on the DeepECG-CLIP DINOv3 encoder, in the DeepECG_Docker head format.

Each head is one folder with exactly the files of a DeepECG_Docker classifier repository
(HeartWise-AI/DeepECG_Docker models/siglip2_wrapper.py, ``_DeepECG_SigLIP2_Bank.load_head``;
e.g. heartwise/SigLIP2_SHD), so the same folder serves both pipelines:

    <ProbeDirectory>/<head key>/
      config.json              identical to models/encoder_config.json (encoder + preprocessing)
      head.json                name, task, outputs, label, encoder {repo, run, file, sha256, feature},
                               classifier {file, type "linear", tensors, dtype "float32",
                               input_dim 1536, standardize, activation "sigmoid"}, decision, source
      thresholds.json          {"thresholds": {output: cutoff}}; {} for a probability-only head
      classifier.safetensors   weight [k, 1536], bias [k] (+ mean [1536], scale [1536] when
                               standardize is true), float32

p = sigmoid(((feature - mean) / scale) @ weight.T + bias), one per output, where feature is the
1536-d concat[CLS, mean(patch tokens)] of the frozen pretraining encoder (base/epoch_030.pt),
whose SHA-256 head.json must name. The checks below are DeepECG_Docker's, with the encoder and
its feature size of this model. A scripts/probe_report.py probe (StandardScaler +
LogisticRegression) maps directly: weight = coef_, bias = intercept_, mean = mean_, scale = scale_.

Adding a head needs no code change: upload its folder under ``probes/`` in the Hugging Face
repository and bump the pinned model revision (data/model_info.json).
"""
from __future__ import annotations

import json
import re
from dataclasses import dataclass, field
from pathlib import Path

import torch

_KEY = re.compile(r"^[A-Za-z0-9_.-]+$")
REQUIRED_FILES = ("config.json", "head.json", "thresholds.json", "classifier.safetensors")


@dataclass
class Probe:
    key: str                      # folder name
    name: str                     # head.json name (the repository of the DeepECG_Docker twin)
    task: str
    outputs: list[str]
    thresholds: dict[str, float]  # empty: probabilities only
    tensors: dict[str, torch.Tensor] = field(default_factory=dict)
    standardize: bool = False
    facts: dict = field(default_factory=dict)

    def __call__(self, features: torch.Tensor) -> torch.Tensor:
        """[B, 1536] -> [B, k] probabilities, as DeepECG_Docker ``probabilities``."""
        x = features.detach().to("cpu", torch.float32)
        t = self.tensors
        if self.standardize:
            x = (x - t["mean"]) / t["scale"]
        return torch.sigmoid(x @ t["weight"].T + t["bias"])


def _read_json(path: Path) -> dict:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except Exception as e:
        raise RuntimeError(f"cannot read {path}: {type(e).__name__}: {e}") from e
    if not isinstance(value, dict):
        raise RuntimeError(f"{path}: expected a JSON object")
    return value


def load_probe(folder: Path, encoder_config: dict, encoder_sha256: str, with_weights: bool = True) -> Probe:
    """Load and validate one head folder (DeepECG_Docker ``load_head`` checks)."""
    missing = [name for name in REQUIRED_FILES if not (folder / name).is_file()]
    if missing:
        raise RuntimeError(f"{folder.name}: missing {', '.join(missing)}")
    if not _KEY.fullmatch(folder.name):
        raise RuntimeError(f"{folder.name}: head folder names use letters, digits, '_', '-' and '.'")
    if _read_json(folder / "config.json") != encoder_config:
        raise RuntimeError(f"{folder.name}/config.json: head configuration differs from models/encoder_config.json")
    metadata = _read_json(folder / "head.json")
    outputs = metadata.get("outputs")
    if not isinstance(outputs, list) or not outputs or not all(isinstance(v, str) and v for v in outputs):
        raise RuntimeError(f"{folder.name}/head.json: outputs must be a non-empty string list")
    if len(set(outputs)) != len(outputs):
        raise RuntimeError(f"{folder.name}/head.json: outputs must be unique")
    if not isinstance(metadata.get("name"), str) or not metadata["name"]:
        raise RuntimeError(f"{folder.name}/head.json: name must be a non-empty string")
    encoder = metadata.get("encoder", {})
    if encoder.get("sha256") != encoder_sha256:
        raise RuntimeError(f"{folder.name}/head.json: classifier does not match the pinned frozen encoder")

    classifier = metadata.get("classifier", {})
    dim = 2 * int(encoder_config["vision_config"]["hidden_size"])
    standardize = classifier.get("standardize") is True
    expected = {"weight": (len(outputs), dim), "bias": (len(outputs),)}
    if standardize:
        expected.update({"mean": (dim,), "scale": (dim,)})
    declared = {name: tuple(shape) for name, shape in classifier.get("tensors", {}).items() if isinstance(shape, list)}
    if (classifier.get("file") != "classifier.safetensors"
            or classifier.get("type") != "linear"
            or classifier.get("input_dim") != dim
            or classifier.get("dtype") != "float32"
            or classifier.get("activation") != "sigmoid"
            or declared != expected):
        raise RuntimeError(f"{folder.name}/head.json: unsupported or inconsistent classifier metadata")

    document = _read_json(folder / "thresholds.json")
    raw = document.get("thresholds")
    if not isinstance(raw, dict):
        raise RuntimeError(f"{folder.name}/thresholds.json: thresholds must be an object")
    try:
        thresholds = {name: float(value) for name, value in raw.items()}
    except (TypeError, ValueError) as e:
        raise RuntimeError(f"{folder.name}/thresholds.json: thresholds must be numeric") from e
    if thresholds and (set(thresholds) != set(outputs) or any(not 0 <= v <= 1 for v in thresholds.values())):
        raise RuntimeError(f"{folder.name}/thresholds.json: expected one [0, 1] cutoff per output, or none")

    tensors: dict[str, torch.Tensor] = {}
    if with_weights:
        from safetensors.torch import load_file

        try:
            tensors = load_file(str(folder / "classifier.safetensors"))
        except Exception as e:
            raise RuntimeError(f"{folder.name}/classifier.safetensors: {type(e).__name__}: {e}") from e
        if set(tensors) != set(expected):
            raise RuntimeError(f"{folder.name}/classifier.safetensors: tensors {sorted(tensors)}, expected {sorted(expected)}")
        wrong = {n: tuple(tensors[n].shape) for n, s in expected.items() if tuple(tensors[n].shape) != s}
        if wrong:
            raise RuntimeError(f"{folder.name}/classifier.safetensors: tensor shapes {wrong}, expected {expected}")
        if any(t.dtype != torch.float32 for t in tensors.values()):
            raise RuntimeError(f"{folder.name}/classifier.safetensors: tensors must be float32")
        if standardize and not bool((tensors["scale"] > 0).all()):
            raise RuntimeError(f"{folder.name}/classifier.safetensors: scale must be positive")
    facts = {k: metadata[k] for k in ("label", "decision", "source") if k in metadata}
    return Probe(folder.name, metadata["name"], str(metadata.get("task") or ""), outputs, thresholds,
                 tensors, standardize, facts)


def discover_probes(directory: Path, encoder_config: dict, encoder_sha256: str,
                    with_weights: bool = True) -> list[Probe]:
    """Every head folder of ``directory`` (none when it is absent), sorted by folder name."""
    if not directory.is_dir():
        return []
    folders = sorted(p for p in directory.iterdir() if p.is_dir() and not p.name.startswith("."))
    return [load_probe(folder, encoder_config, encoder_sha256, with_weights) for folder in folders]
