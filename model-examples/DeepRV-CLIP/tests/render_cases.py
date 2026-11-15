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
MAIN = ("predictions", "abnormalRV", "probability")  # the stored number the hostile-input tests alter
MAIN_BAD = {"too_low": -0.001, "too_high": 1.5, "too_many_decimals": 0.11512, "other": 0.777}
EXAMPLE = {"Outcome": float32(0.1153)}
EDGE_PROBABILITIES = [0.0, 0.0004, 0.0005, 0.005, 0.115, 0.125, 0.1344, 0.1345, 0.1349, 0.135, 0.1351, 0.1354,
                      0.4944, 0.4949, 0.495, 0.4951, 0.4995, 0.4996, 0.49999, 0.5, 0.5004, 0.5005, 0.96, 0.9949,
                      0.995, 0.9951, 0.9995, 0.9996, 1.0]


def edge_preds() -> list[dict]:
    """As float32, which is what the model outputs (see sweep_preds)."""
    return [{"Outcome": float32(p)} for p in EDGE_PROBABILITIES]


def sweep_preds(rng: random.Random) -> list[dict]:
    """Every float32 nearest to a multiple of 0.0001 (what the model can output), and random sigmoids. Decimal
    multiples of 0.0005 as float64 are left out: the report prints round(p * 100, 1) and the JSON stores
    round(p, 3), which agree for every float32 output but not for a float64 placed exactly on such a tie."""
    return ([{"Outcome": float32(i / 10000.0)} for i in range(10001)]
            + [{"Outcome": sigmoid32(rng)} for _ in range(20000)])


def raw_outputs(rng: random.Random) -> dict:
    return {"Outcome": torch.tensor([[rng.uniform(-8.0, 8.0)]])}
