"""Frozen linear probes on the DeepECG-CLIP DINOv3 encoder, discovered from files.

A probe reads the 1536-d pooled features (concat[CLS, mean(patch tokens)]) of the frozen
pretraining encoder (base/epoch_030.pt, run zo59vdxk), the same features
DeepECG-PNG scripts/probe_report.py fitted its StandardScaler + LogisticRegression heads on.

Adding a probe needs no code change: put a folder under the release's ``probes/`` directory
(next to aco/ and acco_vs_aico/ on the NAS and in the Hugging Face repository), list its files
in ``probes/SHA256SUMS``, and bump the pinned model revision in data/model_info.json:

    probes/
      SHA256SUMS                      sha256sum of every file below, paths relative to probes/
      <probe_id>/head.json            the manifest below
      <probe_id>/classifier.safetensors

head.json:
    {
      "id": "lvef_le_40",                       unique, [a-z0-9_]+
      "task": "binary" | "multilabel" | "multiclass",
      "description": "LVEF <= 40% on the echocardiogram within 30 days",
      "labels": ["LVEF <= 40%"],                one per output row
      "label_ids": ["lvef_le_40"],              optional; default <id> (one row) or <id>.<k>
      "thresholds": [0.123],                    validation Youden per row; null for multiclass
      "group": "lvef",                          optional, for display
      "source": {...}, "metrics": {...}         free-form provenance, returned by model-facts only
    }

classifier.safetensors: ``weight`` [K, 1536] and ``bias`` [K] of the classifier, and the
StandardScaler as ``scaler_mean`` [1536] / ``scaler_scale`` [1536] (omit both when the probe
was fitted on raw features). Output: sigmoid per row (binary, multilabel) or softmax over the
rows (multiclass). convert_probe.py writes this layout from a scripts/probe_report.py joblib.
"""
from __future__ import annotations

import hashlib
import json
import re
from dataclasses import dataclass, field
from pathlib import Path

import torch

FEATURE_DIM = 1536
TASKS = ("binary", "multilabel", "multiclass")
_ID = re.compile(r"^[a-z0-9_]+$")


@dataclass
class ProbeOutput:
    id: str
    label: str
    threshold: float | None


@dataclass
class Probe:
    id: str
    task: str
    description: str
    group: str | None
    outputs: list[ProbeOutput]
    weight: torch.Tensor
    bias: torch.Tensor
    mean: torch.Tensor | None = None
    scale: torch.Tensor | None = None
    facts: dict = field(default_factory=dict)

    def __call__(self, features: torch.Tensor) -> torch.Tensor:
        """[B, 1536] -> [B, K] probabilities."""
        x = features.to(torch.float64)
        if self.mean is not None:
            x = (x - self.mean) / self.scale
        logits = x @ self.weight.T + self.bias
        return torch.softmax(logits, dim=-1) if self.task == "multiclass" else torch.sigmoid(logits)


def read_sha256sums(path: Path) -> dict[str, str]:
    sums = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        if line.strip():
            digest, name = line.split(maxsplit=1)
            sums[name.lstrip("*").strip()] = digest.lower()
    return sums


def verify_probe_files(directory: Path) -> list[Path]:
    """Every file under ``directory`` must be listed in its SHA256SUMS with a matching hash,
    and every listed file must exist. An absent directory holds no probes."""
    if not directory.is_dir():
        return []
    sums_path = directory / "SHA256SUMS"
    if not sums_path.is_file():
        raise ValueError(f"{directory} has no SHA256SUMS")
    sums = read_sha256sums(sums_path)
    present = sorted(p.relative_to(directory).as_posix() for p in directory.rglob("*")
                     if p.is_file() and p.name != "SHA256SUMS" and ".cache" not in p.parts)
    if sorted(sums) != present:
        raise ValueError(f"{sums_path} does not list exactly the files of {directory}")
    for name, expected in sums.items():
        actual = hashlib.sha256((directory / name).read_bytes()).hexdigest()
        if actual != expected:
            raise ValueError(f"SHA-256 mismatch for probes/{name}")
    return [directory / name for name in sums]


def read_manifest(path: Path) -> dict:
    manifest = json.loads(path.read_text(encoding="utf-8"))
    probe_id = manifest.get("id")
    if not isinstance(probe_id, str) or not _ID.fullmatch(probe_id) or probe_id != path.parent.name:
        raise ValueError(f"{path}: id must be [a-z0-9_]+ and equal its folder name")
    if manifest.get("task") not in TASKS:
        raise ValueError(f"{path}: task must be one of {TASKS}")
    labels = manifest.get("labels")
    if not isinstance(labels, list) or not labels or not all(isinstance(x, str) and x for x in labels):
        raise ValueError(f"{path}: labels must be a non-empty list of strings")
    if manifest["task"] == "binary" and len(labels) != 1:
        raise ValueError(f"{path}: a binary probe has one label")
    thresholds = manifest.get("thresholds")
    if manifest["task"] == "multiclass":
        thresholds = thresholds or [None] * len(labels)
    if not isinstance(thresholds, list) or len(thresholds) != len(labels):
        raise ValueError(f"{path}: thresholds must have one entry per label")
    for t in thresholds:
        if t is not None and not (isinstance(t, (int, float)) and 0.0 <= float(t) <= 1.0):
            raise ValueError(f"{path}: thresholds must be probabilities")
        if t is None and manifest["task"] != "multiclass":
            raise ValueError(f"{path}: binary and multilabel probes need a threshold per label")
    label_ids = manifest.get("label_ids") or (
        [probe_id] if len(labels) == 1 else [f"{probe_id}.{k}" for k in range(len(labels))])
    if len(label_ids) != len(labels) or len(set(label_ids)) != len(label_ids):
        raise ValueError(f"{path}: label_ids must be unique, one per label")
    manifest["thresholds"], manifest["label_ids"] = thresholds, label_ids
    return manifest


def load_probe(directory: Path, with_weights: bool = True) -> Probe:
    manifest = read_manifest(directory / "head.json")
    outputs = [ProbeOutput(i, label, None if t is None else float(t))
               for i, label, t in zip(manifest["label_ids"], manifest["labels"], manifest["thresholds"])]
    weight = bias = mean = scale = None
    if with_weights:
        from safetensors.torch import load_file

        tensors = load_file(str(directory / "classifier.safetensors"))
        weight = tensors["weight"].to(torch.float64)
        bias = tensors["bias"].to(torch.float64)
        if tuple(weight.shape) != (len(outputs), FEATURE_DIM) or tuple(bias.shape) != (len(outputs),):
            raise ValueError(f"{directory.name}: weight must be [{len(outputs)}, {FEATURE_DIM}] and bias [{len(outputs)}]")
        if ("scaler_mean" in tensors) != ("scaler_scale" in tensors):
            raise ValueError(f"{directory.name}: scaler_mean and scaler_scale go together")
        if "scaler_mean" in tensors:
            mean = tensors["scaler_mean"].to(torch.float64)
            scale = tensors["scaler_scale"].to(torch.float64)
            if mean.shape != (FEATURE_DIM,) or scale.shape != (FEATURE_DIM,) or not bool((scale > 0).all()):
                raise ValueError(f"{directory.name}: the scaler must be two positive-scale [{FEATURE_DIM}] vectors")
    return Probe(manifest["id"], manifest["task"], str(manifest.get("description") or ""),
                 manifest.get("group"), outputs, weight, bias, mean, scale,
                 {k: manifest[k] for k in ("source", "metrics") if k in manifest})


def discover_probes(directory: Path, with_weights: bool = True) -> list[Probe]:
    """The probes of ``directory``, sorted by id. Every file is checked against SHA256SUMS."""
    if with_weights:
        verify_probe_files(directory)
    if not directory.is_dir():
        return []
    return [load_probe(path.parent, with_weights) for path in sorted(directory.glob("*/head.json"))]
