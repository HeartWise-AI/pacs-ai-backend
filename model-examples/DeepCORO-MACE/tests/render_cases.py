"""Model-specific inputs of test_render_report.py: raw model outputs around every place where the JSON output
rounds what the HTML report reads unrounded."""
from __future__ import annotations

import json
import random
from pathlib import Path

import torch

ROOT = Path(__file__).resolve().parents[1]
CLASS_MAPPING = json.loads((ROOT / "models" / "class_mapping.json").read_text(encoding="utf-8"))


def sigmoid32(rng: random.Random, spread: float = 9.0) -> float:
    """A probability as the model produces it: a float32 sigmoid read back as a Python float."""
    return float(torch.sigmoid(torch.tensor(rng.uniform(-spread, spread), dtype=torch.float32)))


def float32(value: float) -> float:
    return float(torch.tensor(value, dtype=torch.float32))


VERSION = "1.0.1"
MAIN = ("predictions", "primary", "compositeMace", "probability")  # the stored number the hostile-input tests alter
MAIN_BAD = {"too_low": -0.001, "too_high": 1.5, "too_many_decimals": 0.85812, "other": 0.777}
HEADS = list(CLASS_MAPPING)
EXAMPLE = {head: round(0.06 + 0.09 * index, 4) for index, head in enumerate(HEADS)}  # none on the 0.5 threshold
EDGE_PROBABILITIES = [0.0, 0.0004, 0.0005, 0.1344, 0.1345, 0.135, 0.4994, 0.4995, 0.4996, 0.49999, 0.5, 0.5004,
                      0.5005, 0.858, 0.9995, 0.9996, 1.0]


def edge_preds() -> list[dict]:
    same = [{head: p for head in HEADS} for p in EDGE_PROBABILITIES]
    shifted = [{head: EDGE_PROBABILITIES[(start + index) % len(EDGE_PROBABILITIES)] for index, head in enumerate(HEADS)}
               for start in range(len(EDGE_PROBABILITIES))]
    return same + shifted


def sweep_preds(rng: random.Random) -> list[dict]:
    grid = [{head: (i + 37 * index) % 10001 / 10000.0 for index, head in enumerate(HEADS)} for i in range(10001)]
    return grid + [{head: sigmoid32(rng) for head in HEADS} for _ in range(10000)]


def raw_outputs(rng: random.Random) -> dict:
    return {head: torch.tensor([[rng.uniform(-8.0, 8.0)]]) for head in HEADS}
