"""Frozen linear-probe heads on the DeepECG-CLIP DINOv3 encoder, loaded and scored by DeepECG_Docker's code.

The heads are DeepECG_Docker DINOv3 probe folders (HeartWise-AI/DeepECG_Docker models/dinov3_bank.py,
vendored unchanged here as ``models/dinov3_bank.py``):

    <ProbeDirectory>/config.json           encoder checkpoint, preprocessing and feature (dim 1536)
    <ProbeDirectory>/<task>_<clean|aug>/
      config.json                          identical to <ProbeDirectory>/config.json
      head.json                            name, task, outputs, encoder {repo, file, sha256}, classifier
      thresholds.json                      {"thresholds": {output: validation Youden cutoff}}; {} = none
      classifier.safetensors               weight [k, 1536], bias [k], mean [1536], scale [1536], float32

Every check and the probability are the bank's own (``_DeepECG_DINOv3_Bank.load_head`` and
``head_probabilities``): z = (feature - mean) / scale, then sigmoid per output (softmax for a 3-class
head). The bank would also load a second copy of the frozen encoder; this container already computes
the same 1536-d concat[CLS, mean(patch tokens)] of base/epoch_030.pt in its shared trunk
(models/dinov3_heads.py, one pass with the fine-tuned heads), so ``_HeadBank`` runs the bank's
configuration checks and loads heads without that encoder.

Adding a head needs no code change: upload its folder under ``probes/`` in the Hugging Face repository
and bump the pinned model revision (data/model_info.json). Folder names are ``<task>_clean`` or
``<task>_aug`` with a task of ``dinov3_bank.PROBE_OUTPUTS``, whose outputs the head must have.
"""
from __future__ import annotations

import json
import re
from dataclasses import dataclass, field
from pathlib import Path
from types import SimpleNamespace

import torch

from models import dinov3_bank as bank

_KEY = re.compile(r"^[A-Za-z0-9_.-]+$")
REQUIRED_FILES = ("config.json", "head.json", "thresholds.json", "classifier.safetensors")
VARIANTS = ("clean", "aug")
FACT_KEYS = ("decision", "probe", "probe_description", "test_auroc", "test_images", "status", "notes")


@dataclass
class Probe:
    key: str                      # folder name, <task>_<variant>
    name: str                     # head.json name (its identity in the DeepECG_Docker repository)
    task: str
    outputs: list[str]
    thresholds: dict[str, float]  # empty: probabilities only
    activation: str = "sigmoid"
    head: dict = field(default_factory=dict, repr=False)   # the bank's head dict
    facts: dict = field(default_factory=dict)

    def __call__(self, features: torch.Tensor) -> torch.Tensor:
        """[B, 1536] -> [B, k] probabilities, with DeepECG_Docker's ``head_probabilities``."""
        return bank.head_probabilities(features.detach().to("cpu", torch.float32), self.head)


class _HeadBank(bank._DeepECG_DINOv3_Bank):
    """``_DeepECG_DINOv3_Bank`` minus its encoder: the same checks of <ProbeDirectory>/config.json and
    of the encoder SHA-256, then the bank's ``load_head`` unchanged."""

    def __init__(self, directory: Path, encoder_sha256: str):   # noqa: super().__init__ would load an encoder
        directory = Path(directory)
        self.folder = str(directory.parent.resolve())
        self.probes_dir = directory.name
        config_path = self._required(f"{self.probes_dir}/config.json")
        self.config = bank._read_json(config_path)
        checkpoint = self.config.get("checkpoint", {})
        if checkpoint.get("file") != bank.ENCODER_FILE or checkpoint.get("sha256") != bank.ENCODER_SHA256:
            raise RuntimeError(f"{config_path}: encoder checkpoint does not match {bank.ENCODER_FILE} "
                               f"({bank.ENCODER_SHA256})")
        if checkpoint.get("state_dict_key") != bank.ENCODER_STATE_KEY or checkpoint.get("strict") is not True:
            raise RuntimeError(f"{config_path}: unsupported checkpoint loading settings")
        if self.config.get("model_name") != "dinov3_vitb16_clip":
            raise RuntimeError(f"{config_path}: model_name is not dinov3_vitb16_clip")
        if encoder_sha256 != bank.ENCODER_SHA256:
            raise RuntimeError(f"the frozen encoder pinned by models/config.json ({encoder_sha256}) is not "
                               f"the encoder of the probe heads ({bank.ENCODER_SHA256})")
        feature_dim = int(self.config.get("feature", {}).get("dim", 0))
        expected_dim = 2 * int(bank.DINOV3_VITB16["hidden_size"])
        if feature_dim != expected_dim:
            raise RuntimeError(f"{config_path}: feature dim {feature_dim}, the encoder gives {expected_dim}")
        self.encoder_sha256 = encoder_sha256
        self.encoder = SimpleNamespace(feature_dim=feature_dim)
        self.heads = {}


def _task(key: str) -> str:
    task, _, variant = key.rpartition("_")
    if variant not in VARIANTS or task not in bank.PROBE_OUTPUTS:
        raise RuntimeError(f"{key}: probe folders are named <task>_<{'|'.join(VARIANTS)}> with a task of "
                           f"{sorted(bank.PROBE_OUTPUTS)}")
    return task


def discover_probes(directory: Path, encoder_sha256: str, with_weights: bool = True) -> list[Probe]:
    """Every head folder of ``directory`` (none when it is absent), sorted by folder name.
    ``with_weights`` is accepted for callers that only need the registry; the bank always reads
    the (small) classifier tensors to validate them."""
    directory = Path(directory)
    if not directory.is_dir():
        return []
    folders = sorted(p for p in directory.iterdir() if p.is_dir() and not p.name.startswith("."))
    if not folders:
        return []
    loader = _HeadBank(directory, encoder_sha256)
    probes = []
    for folder in folders:
        if not _KEY.fullmatch(folder.name):
            raise RuntimeError(f"{folder.name}: head folder names use letters, digits, '_', '-' and '.'")
        missing = [name for name in REQUIRED_FILES if not (folder / name).is_file()]
        if missing:
            raise RuntimeError(f"{folder.name}: missing {', '.join(missing)}")
        task = _task(folder.name)
        head = loader.load_head(folder.name, bank.PROBE_OUTPUTS[task])
        metadata = json.loads((folder / "head.json").read_text(encoding="utf-8"))
        facts = {k: metadata[k] for k in FACT_KEYS if k in metadata}
        probes.append(Probe(folder.name, head["name"], head["task"], list(head["outputs"]),
                            dict(head["thresholds"]), head["activation"], head, facts))
    return probes
