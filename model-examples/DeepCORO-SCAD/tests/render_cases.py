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


VERSION = "1.0.2"
MAIN = ("predictions", "scad", "probability")  # the stored number the hostile-input tests alter
MAIN_BAD = {"too_low": -0.0001, "too_high": 1.5, "too_many_decimals": 0.449912, "other": 0.7771}
THRESHOLD = float(CLASS_MAPPING["scad"]["threshold"])
EDGE_PROBABILITIES = [0.0, 0.00004, 0.00005, 0.0004, 0.05234, 0.05235, 0.05236, 0.0524, THRESHOLD - 1e-8, THRESHOLD,
                      0.05241, 0.05244, 0.05245, 0.0525, 0.4494, 0.44944, 0.44945, 0.44946, 0.4495, 0.44951, 0.4499,
                      0.45, 0.99994, 0.99995, 0.99996, 1.0]


def _preds(probability: float) -> dict:
    return {"scad": probability, "cad": 0.31, "other": 0.07, "stenosis": 0.42}


EXAMPLE = _preds(0.44991)


def edge_preds() -> list[dict]:
    return [_preds(p) for p in EDGE_PROBABILITIES]


def sweep_preds(rng: random.Random) -> list[dict]:
    return ([_preds(i / 100000.0) for i in range(0, 100001, 3)] + [_preds(i / 10000.0) for i in range(10001)]
            + [_preds(sigmoid32(rng)) for _ in range(20000)])


def raw_outputs(rng: random.Random) -> dict:
    return {head: torch.tensor([[rng.uniform(-8.0, 8.0) if head != "stenosis" else rng.uniform(-0.5, 1.5)]])
            for head in ("scad", "cad", "other", "stenosis")}
