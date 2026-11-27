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


VERSION = "6.0.2"
MAIN = ("predictions", "syntax", "value")  # the stored number the hostile-input tests alter
MAIN_BAD = {"too_low": -0.1, "too_high": 1000.1, "too_many_decimals": 25.74, "other": 44.4}
THRESHOLD = CLASS_MAPPING["syntax_category"]["threshold_binary_ge23"]
EDGE_SCORES = [0.0, 0.04, 0.05, 0.44, 0.45, 0.46, 0.49, 0.5, 0.54, 0.55, 16.94, 16.95, 16.96, 16.97, THRESHOLD - 1e-6,
               THRESHOLD, 16.98, 17.0, 17.04, 22.94, 22.95, 22.96, 22.999, 23.0, 23.04, 32.94, 32.95, 32.96, 33.0,
               55.55, 99.96, 140.0]
SEVERITY = [[0.0, 0.25, 0.568, 0.182], [0.9996, 0.0004, 0.0, 0.0], [0.25, 0.25, 0.25, 0.25], [0.1235, 0.3765, 0.4995, 0.0005]]


def _preds(score: float, left: float, right: float, severity: list[float], argmax: int | None = None) -> dict:
    return {"syntax": score, "syntax_left": left, "syntax_right": right, "syntax_category": list(severity),
            "syntax_category_argmax": severity.index(max(severity)) if argmax is None else argmax,
            "syntax_left_category": [0.7, 0.2, 0.1, 0.0], "syntax_left_category_argmax": 0,
            "syntax_right_category": [0.6, 0.3, 0.1, 0.0], "syntax_right_category_argmax": 0}


EXAMPLE = _preds(25.71, 22.18, 2.63, SEVERITY[0])


def edge_preds() -> list[dict]:
    cases = [_preds(score, EDGE_SCORES[(index + 3) % len(EDGE_SCORES)], EDGE_SCORES[(index + 7) % len(EDGE_SCORES)],
                    SEVERITY[index % len(SEVERITY)]) for index, score in enumerate(EDGE_SCORES)]
    cases += [_preds(12.3, 4.5, 6.7, SEVERITY[1], argmax=index) for index in range(4)]
    return cases


def sweep_preds(rng: random.Random) -> list[dict]:
    grid = [_preds(i / 100.0, (i * 7 % 6000) / 100.0, (i * 13 % 4000) / 100.0, SEVERITY[i % len(SEVERITY)])
            for i in range(6001)]
    randoms = []
    for _ in range(10000):
        severity = torch.softmax(torch.tensor([rng.uniform(-4, 4) for _ in range(4)]), dim=-1)
        randoms.append(_preds(float32(rng.uniform(0.0, 70.0)), float32(rng.uniform(0.0, 50.0)),
                              float32(rng.uniform(0.0, 30.0)), [float(p) for p in severity]))
    return grid + randoms


def raw_outputs(rng: random.Random) -> dict:
    out = {head: torch.tensor([[rng.uniform(-3.0, 60.0)]]) for head in ("syntax", "syntax_left", "syntax_right")}
    for head in ("syntax_category", "syntax_left_category", "syntax_right_category"):
        out[head] = torch.tensor([[rng.uniform(-4.0, 4.0) for _ in range(4)]])
    return out
