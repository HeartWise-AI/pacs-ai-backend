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


import re

VERSION = "2.0.1"
MAIN = ("predictions", "jctoScore", "predicted")  # the stored number the hostile-input tests alter
MAIN_BAD = {"too_low": -0.01, "too_high": 4.01, "too_many_decimals": 0.723, "other": 3.33}
ARTERIES = ("lad", "rca", "lcx")
COMPONENTS = ("jcto_blunt_stump", "jcto_calcification", "jcto_bending_gt45", "jcto_occlusion_length_gt20")
NOTE = ("\n <div style=\"margin:-8px 0 18px;color:#6c757d;font-size:12px;\">Report rendered from the stored "
        "result: component probabilities are shown as stored, to three decimals.</div>")
COMPONENT_CELL = re.compile(r"(<td style='text-align:right'>)(\d\.\d+)(</td><td style='text-align:center'>)")
EDGE_SCORES = [0.72, 0.0, 0.004, 0.005, 0.244, 0.245, 0.2451, 0.25, 0.2549, 0.255, 0.494, 0.495, 0.4951, 0.4996, 0.5,
               0.504, 1.4949, 1.495, 1.5, 2.4996, 2.5, 3.4996, 3.5, 3.996, 4.0]
EDGE_PROBABILITIES = [0.245, 0.022, 0.0, 0.0004, 0.0005, 0.1344, 0.1345, 0.1349, 0.135, 0.1351, 0.1354, 0.4994,
                      0.4995, 0.4996, 0.49999, 0.5, 0.5004, 0.5005, 0.9995, 0.9996, 1.0]


def preds_of(scores: dict, components: dict) -> dict:
    """Post-processed outputs as _postprocess returns them: the 15 per-artery heads, then the selected artery's
    heads under the generic names (by the model's own _select_artery)."""
    from logic import CustomPredictionService
    raw = {f"jcto_score_{a}": float(scores[a]) for a in ARTERIES}
    raw.update({f"{h}_{a}": float(components[a][i]) for a in ARTERIES for i, h in enumerate(COMPONENTS)})
    return CustomPredictionService()._select_artery(raw)


def _case(index: int, score: float, selected: str) -> dict:
    """`selected` gets `score`; the others get a lower score (or the same one, which the first artery wins)."""
    def probability(offset: int) -> float:
        return EDGE_PROBABILITIES[(index * 5 + offset) % len(EDGE_PROBABILITIES)]
    lower = [max(0.0, score - 0.006), score * 0.5]
    scores = {selected: score}
    for position, artery in enumerate(a for a in ARTERIES if a != selected):
        scores[artery] = lower[position]
    components = {a: [probability(4 * ARTERIES.index(a) + i) for i in range(4)] for a in ARTERIES}
    return preds_of(scores, components)


EXAMPLE = preds_of({"lad": 0.7234, "rca": 0.31, "lcx": 0.0},
                   {"lad": [0.2451, 0.2583, 0.0221, 0.6772], "rca": [0.181, 0.332, 0.025, 0.345],
                    "lcx": [0.146, 0.148, 0.014, 0.214]})


def edge_preds() -> list[dict]:
    """The first cases keep every stored component away from the 0.5 threshold (see the flag test)."""
    calm = [preds_of({"lad": 0.2, "rca": 1.7, "lcx": 1.2}, {a: [0.1349, 0.1351, 0.77, 0.022] for a in ARTERIES}),
            preds_of({"lad": 0.2, "rca": 1.7, "lcx": 2.6}, {a: [0.9, 0.8, 0.7, 0.6] for a in ARTERIES}),
            preds_of({"lad": 0.0, "rca": 0.0, "lcx": 0.0}, {a: [0.0, 0.01, 0.02, 0.03] for a in ARTERIES})]
    return calm + [_case(index, score, ARTERIES[index % 3]) for index, score in enumerate(EDGE_SCORES)]


def sweep_preds(rng: random.Random) -> list[dict]:
    grid = [preds_of({ARTERIES[i % 3]: i / 1000.0, ARTERIES[(i + 1) % 3]: (i * 7 % 4001) / 1000.0,
                      ARTERIES[(i + 2) % 3]: (i * 13 % 4001) / 1000.0},
                     {a: [((i + 37 * (4 * k + j)) * 3 % 10001) / 10000.0 for j in range(4)] for k, a in enumerate(ARTERIES)})
            for i in range(4001)]
    randoms = [preds_of({a: float32(rng.uniform(0.0, 4.0)) for a in ARTERIES},
                        {a: [sigmoid32(rng, 5.0) for _ in range(4)] for a in ARTERIES}) for _ in range(8000)]
    return grid + randoms


def raw_outputs(rng: random.Random) -> dict:
    out = {f"jcto_score_{a}": torch.tensor([[rng.uniform(-1.0, 5.0)]]) for a in ARTERIES}
    out.update({f"{h}_{a}": torch.tensor([[rng.uniform(-6.0, 6.0)]]) for a in ARTERIES for h in COMPONENTS})
    return out


def normalise(html: str) -> str:
    """The report without the stored-result note and with the four component probability cells blanked: the
    only places where a report rendered from a stored result may differ from the live one."""
    html, cells = COMPONENT_CELL.subn(r"\1#\3", html.replace(NOTE, ""))
    assert cells == 4, cells
    return html


def as_stored(live_html: str, preds: dict) -> str:
    """What render-report must answer for the prediction whose live report is `live_html`: the four component
    cells with the three-decimal rounding of the raw probability, and the note under the component table."""
    values = iter(f"{round(float(preds[h]), 3):.3f}" for h in COMPONENTS)
    html, cells = COMPONENT_CELL.subn(lambda m: m.group(1) + next(values) + m.group(3), live_html)
    assert cells == 4 and html.count("</tbody>\n </table>") == 2
    return html.replace("</tbody>\n </table>", "</tbody>\n </table>" + NOTE, 1)
