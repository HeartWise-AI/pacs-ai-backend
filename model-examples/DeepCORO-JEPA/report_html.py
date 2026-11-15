"""HTML report of DeepCORO-JEPA: the 18 segments drawn on a coronary schema (Schéma coronaire), plus the LAD
physiology, lesion thresholds and per-vessel detail. Pure Python (no torch), so it renders from a JSON response.

The schema follows the coronary dominance given in the request metadata (study majority of the angiogram
classifier); without it the tree is drawn right dominant and the report says the dominance was not provided.
"""
from __future__ import annotations

import json
import math
import sys
from html import escape

from display_calibration import CATEGORY_LABELS, calibration, display_category, display_pct

SEGMENT_NAMES = {
    "prox_rca": "Proximal RCA", "mid_rca": "Mid RCA", "dist_rca": "Distal RCA", "pda": "PDA",
    "posterolateral": "Posterolateral", "left_main": "Left main", "prox_lad": "Proximal LAD", "mid_lad": "Mid LAD",
    "dist_lad": "Distal LAD", "D1": "First diagonal", "D2": "Second diagonal", "prox_lcx": "Proximal LCx",
    "mid_lcx": "Mid LCx", "dist_lcx": "Distal LCx", "om1": "First obtuse marginal", "om2": "Second obtuse marginal",
    "bx": "Ramus (bisector)", "lvp": "Posterior left ventricular",
}
SHORT = {
    "prox_rca": "Prox RCA", "mid_rca": "Mid RCA", "dist_rca": "Dist RCA", "pda": "PDA", "posterolateral": "PL",
    "left_main": "Left main", "prox_lad": "Prox LAD", "mid_lad": "Mid LAD", "dist_lad": "Dist LAD", "D1": "D1",
    "D2": "D2", "prox_lcx": "Prox LCx", "mid_lcx": "Mid LCx", "dist_lcx": "Dist LCx", "om1": "OM1", "om2": "OM2",
    "bx": "Ramus", "lvp": "PLV",
}
HIDDEN_SEGMENTS = {"lvp"}  # posterior LV branch: still read by the model and in the JSON, not shown in the report
LESION_NAMES = {"calcif": "calcification", "thrombus": "thrombus", "cto": "chronic total occlusion"}
LESION_TAG = {"calcif": "Calcified", "thrombus": "Thrombus", "cto": "CTO"}
VESSELS = [("Right coronary", ["prox_rca", "mid_rca", "dist_rca", "pda", "posterolateral"]),
           ("Left main and LAD", ["left_main", "prox_lad", "mid_lad", "dist_lad", "D1", "D2", "bx"]),
           ("Circumflex", ["prox_lcx", "mid_lcx", "dist_lcx", "om1", "om2"])]

# Schema geometry (viewBox 0 0 1040 720): cubic Bezier per segment, vessel width, label box top-left.
GEOMETRY = {
    "prox_rca": (((300, 92), (220, 94), (160, 162), (130, 252)), 17, (22, 120)),
    "mid_rca": (((130, 252), (110, 322), (105, 392), (115, 452)), 15, (138, 418)),
    "dist_rca": (((115, 452), (125, 512), (160, 542), (232, 547)), 13, (14, 560)),
    "pda": (((232, 547), (332, 612), (472, 642), (622, 647)), 9, (330, 668)),
    "posterolateral": (((232, 547), (292, 562), (342, 567), (392, 567)), 9, (196, 600)),
    "lvp": (((392, 567), (432, 567), (472, 562), (512, 552)), 7, (530, 560)),
    "left_main": (((420, 92), (450, 94), (475, 97), (500, 102)), 17, (360, 30)),
    "prox_lad": (((500, 102), (580, 112), (640, 132), (700, 172)), 15, (560, 40)),
    "mid_lad": (((700, 172), (770, 222), (820, 292), (850, 382)), 12, (880, 392)),
    "dist_lad": (((850, 382), (880, 472), (880, 562), (850, 642)), 9, (900, 540)),
    "D1": (((652, 138), (720, 160), (790, 190), (850, 237)), 9, (748, 92)),
    "D2": (((790, 262), (840, 282), (890, 312), (925, 355)), 8, (900, 222)),
    "bx": (((506, 108), (570, 162), (630, 222), (680, 282)), 9, (700, 290)),
    "prox_lcx": (((500, 102), (495, 172), (490, 222), (478, 272)), 14, (360, 170)),
    "mid_lcx": (((478, 272), (468, 342), (455, 402), (440, 442)), 12, (336, 340)),
    "dist_lcx": (((440, 442), (425, 477), (405, 502), (380, 517)), 9, (262, 470)),
    "om1": (((486, 200), (540, 240), (580, 290), (600, 352)), 8, (612, 345)),
    "om2": (((466, 352), (505, 387), (530, 422), (545, 472)), 7, (568, 470)),
}
# Left dominance: a small RCA that stops short of the crux; the PDA, posterolateral and posterior LV branches
# come off a long distal circumflex.
GEOMETRY_LEFT = {**GEOMETRY,
    "prox_rca": (((300, 92), (220, 94), (160, 162), (130, 252)), 11, (22, 120)),
    "mid_rca": (((130, 252), (110, 322), (105, 392), (115, 452)), 9, (138, 418)),
    "dist_rca": (((115, 452), (118, 485), (128, 505), (150, 520)), 6, (14, 540)),
    "dist_lcx": (((440, 442), (420, 500), (380, 535), (320, 552)), 11, (292, 474)),
    "pda": (((320, 552), (400, 605), (500, 635), (630, 645)), 9, (452, 668)),
    "posterolateral": (((372, 538), (360, 572), (342, 598), (318, 620)), 7, (210, 640)),
    "lvp": (((320, 552), (285, 560), (255, 570), (225, 584)), 6, (126, 592)),
}
# Unlabelled side branches (acute marginal, posterolateral twigs) so the tree reads like the hand-drawn schema.
# Each with the segment it arises from.
DECOR = [(((128, 300), (180, 330), (230, 370), (262, 402)), 6, "mid_rca"),
         (((300, 566), (330, 585), (350, 600), (372, 612)), 4, "posterolateral"),
         (((440, 566), (468, 585), (488, 600), (505, 612)), 4, "lvp")]
DECOR_LEFT = [(((128, 300), (180, 330), (230, 370), (262, 402)), 4, "mid_rca"),
              (((410, 590), (430, 612), (440, 628), (446, 640)), 4, "pda")]
# Topology of the drawn tree: the segment each segment arises from (the point is read from the geometry).
PARENT = {"mid_rca": "prox_rca", "dist_rca": "mid_rca", "pda": "dist_rca", "posterolateral": "dist_rca",
          "lvp": "posterolateral", "prox_lad": "left_main", "prox_lcx": "left_main", "bx": "left_main",
          "mid_lad": "prox_lad", "dist_lad": "mid_lad", "D1": "prox_lad", "D2": "mid_lad",
          "mid_lcx": "prox_lcx", "dist_lcx": "mid_lcx", "om1": "prox_lcx", "om2": "mid_lcx"}
PARENT_LEFT = {**PARENT, "pda": "dist_lcx", "posterolateral": "dist_lcx", "lvp": "dist_lcx"}
CTO_AT = 0.5  # the block mark sits at the middle of the occluded segment (the lesion is not localised inside it)
VIEW_W, VIEW_H = 1040, 720


PROBABILITY_DECIMALS = 6  # probabilities in the response; the yes/no calls are taken on these returned values


def shown(p: float, cut: float, decimals: int) -> str:
    """A returned probability as text for the diagnosis and the report: `decimals` decimals, or more when fewer
    would print it on the other side of its threshold (printed with 4 decimals) than the call that was made."""
    for d in range(decimals, PROBABILITY_DECIMALS):
        if (float(f"{p:.{d}f}") > round(cut, 4)) == (p > cut):
            return f"{p:.{d}f}"
    return f"{p:.{PROBABILITY_DECIMALS}f}"


def bez(c, t):
    (x0, y0), (x1, y1), (x2, y2), (x3, y3) = c
    m = 1 - t
    return (m**3 * x0 + 3 * m * m * t * x1 + 3 * m * t * t * x2 + t**3 * x3,
            m**3 * y0 + 3 * m * m * t * y1 + 3 * m * t * t * y2 + t**3 * y3)


def bez_len(c, n=60):
    pts = [bez(c, i / n) for i in range(n + 1)]
    return sum(((x1 - x0) ** 2 + (y1 - y0) ** 2) ** 0.5 for (x0, y0), (x1, y1) in zip(pts, pts[1:]))


def path_d(c):
    (x0, y0), (x1, y1), (x2, y2), (x3, y3) = c
    return f"M{x0},{y0} C{x1},{y1} {x2},{y2} {x3},{y3}"


def split(c, t=0.5):
    """de Casteljau: the two halves of a cubic at t."""
    p0, p1, p2, p3 = c
    lerp = lambda a, b: (a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t)
    a, b, cc = lerp(p0, p1), lerp(p1, p2), lerp(p2, p3)
    d, e = lerp(a, b), lerp(b, cc)
    m = lerp(d, e)
    return (p0, a, d, m), (m, e, cc, p3)


def tangent(c, t):
    (x0, y0), (x1, y1) = bez(c, max(t - .01, 0)), bez(c, min(t + .01, 1))
    n = ((x1 - x0) ** 2 + (y1 - y0) ** 2) ** 0.5 or 1
    return (x1 - x0) / n, (y1 - y0) / n


def displayed_pct(v: dict) -> int:
    """The % shown for a segment: the calibrated display value of display_calibration (0 = normal, 100 for a CTO
    flag); the model estimate `stenosis_pct` stays in the JSON and in the hover text."""
    return display_pct(v)


def is_flagged(v: dict) -> bool:
    return v["significant"] or bool(v.get("cto"))


def severity(pct: float) -> str:
    return "sev-high" if pct >= 70 else "sev-mid" if pct >= 50 else "sev-low" if pct >= 30 else "sev-none"


def pct_text(v: dict) -> str:
    """Displayed value as text: "normal" for 0, otherwise the %."""
    pct = displayed_pct(v)
    return "normal" if pct == 0 else f"{pct}%"


def lesion_tags(v: dict) -> list[str]:
    return [LESION_TAG[k] for k in LESION_NAMES if v.get(k)]


def attach_t(c, point, n=200) -> float:
    """Position (0 to 1) along cubic `c` of its point nearest to `point`: where a branch starting there arises."""
    px, py = point
    return min(range(n + 1), key=lambda i: (bez(c, i / n)[0] - px) ** 2 + (bez(c, i / n)[1] - py) ** 2) / n


def occluded_upstream(segs: dict, left_dominant: bool = False):
    """(source, block): `source(g)` is the CTO-flagged segment upstream of segment `g` (None when flow reaches it),
    `block(parent, point)` the same for a vessel arising from `parent` at `point`. Walks the drawn tree: every
    segment distal to an occlusion, and each branch arising at or beyond the block mark of the occluded segment
    (the mark sits at CTO_AT of its length), is without antegrade flow; branches arising before the mark keep it."""
    geometry, parent = (GEOMETRY_LEFT, PARENT_LEFT) if left_dominant else (GEOMETRY, PARENT)
    cto = {g for g in geometry if g not in HIDDEN_SEGMENTS and (segs.get(g) or {}).get("cto")}
    known = {}

    def block(p, point):
        if p is None:
            return None
        up = source(p)
        if up is None and p in cto and attach_t(geometry[p][0], point) >= CTO_AT:
            up = p
        return up

    def source(g):
        if g not in known:
            known[g] = block(parent.get(g), geometry[g][0][0])
        return known[g]

    return source, block


def clot(x: float, y: float, angle: float, r: float, title: str = "", cls: str = "thrombus") -> str:
    """Thrombus cartoon: a lobulated clot lying along the vessel at (x, y), `r` its half height. The lobes are drawn
    twice, outlined then filled, so the outline runs around the whole clot and it reads on a red or a grey vessel."""
    lobes = [(-1.55, 0.12, 0.72), (-0.62, -0.26, 0.96), (0.36, 0.2, 1.0), (1.22, -0.16, 0.8), (1.86, 0.18, 0.52)]
    circles = lambda cls: "".join(f'<circle class="{cls}" cx="{cx * r:.1f}" cy="{cy * r:.1f}" r="{k * r:.1f}"/>'
                                  for cx, cy, k in lobes)
    return (f'<g class="{cls}" transform="translate({x:.1f} {y:.1f}) rotate({angle:.0f})">'
            + (f"<title>{escape(title)}</title>" if title else "") + circles("thr-edge") + circles("thr-lobe")
            + f'<circle class="thr-shine" cx="{-0.7 * r:.1f}" cy="{-0.55 * r:.1f}" r="{0.22 * r:.1f}"/>'
              f'<circle class="thr-shine" cx="{0.5 * r:.1f}" cy="{-0.2 * r:.1f}" r="{0.17 * r:.1f}"/></g>')


def calcium_plates(c, w: float) -> str:
    """Calcification cartoon on the vessel wall: pale plates of uneven length lying on both edges of the segment."""
    out = []
    for t, side, length in ((0.08, -1, 7), (0.21, 1, 15), (0.4, 1, 9), (0.53, -1, 17), (0.77, 1, 8), (0.9, -1, 13)):
        x, y = bez(c, t)
        tx, ty = tangent(c, t)
        x, y = x - ty * side * (w / 2 + 1.5), y + tx * side * (w / 2 + 1.5)
        out.append(f'<rect class="calc-plate" x="{-length / 2}" y="-2.7" width="{length}" height="5.4" rx="2.4" '
                   f'transform="translate({x:.1f} {y:.1f}) rotate({math.degrees(math.atan2(ty, tx)):.0f})"/>')
    return "".join(out)


def legend_cartoon(kind: str) -> str:
    """The schema's cartoon of a finding at legend size (same classes, so it follows the palette)."""
    # "key" first in each class list: the schema's own marks keep their exact class attribute
    vessel = lambda x0, x1, cls="vessel", cap="round": (f'<path class="key {cls}" d="M{x0},9 L{x1},9" stroke-width="7" '
                                                        f'style="stroke-linecap:{cap}"/>')
    if kind == "cto":
        body = (vessel(4, 17) + vessel(21, 38, "vessel noflow")
                + '<line class="key cto-mark" x1="19" y1="2" x2="19" y2="16" stroke-width="3.2"/>')
    elif kind == "calcif":
        body = ('<path class="calc-edge" d="M1,9 L41,9" stroke-width="17"/><path class="calc-band" d="M2,9 L40,9" stroke-width="15"/>'
                + vessel(0, 42, cap="butt")
                + '<rect class="calc-plate" x="5" y="2.6" width="11" height="3.8" rx="1.7"/>'
                  '<rect class="calc-plate" x="29" y="2.6" width="6" height="3.8" rx="1.7"/>'
                  '<rect class="calc-plate" x="16" y="11.6" width="12" height="3.8" rx="1.7"/>')
    else:
        body = vessel(4, 38) + clot(20.5, 9, 0, 5.2, cls="key thrombus")
    return f'<svg class="sw-cartoon" viewBox="0 0 42 18" width="42" height="18" aria-hidden="true">{body}</svg>'


def schema_svg(segs: dict, cuts: dict, left_dominant: bool = False) -> str:
    bands, vessels, plates, plaques, clots, pins, labels = [], [], [], [], [], [], []
    boxes, flagged_thrombus = [], []  # label boxes; segments whose clot is placed once every label is known
    geometry, decor = (GEOMETRY_LEFT, DECOR_LEFT) if left_dominant else (GEOMETRY, DECOR)
    source, block = occluded_upstream(segs, left_dominant)
    no_flow = lambda up: f"<title>No antegrade flow shown: distal to the CTO flag of {escape(SEGMENT_NAMES[up])}</title>"
    for c, w, parent in decor:
        up = block(parent, c[0])
        vessels.append(f'<path class="vessel noflow" d="{path_d(c)}" stroke-width="{w}">{no_flow(up)}</path>' if up
                       else f'<path class="vessel" d="{path_d(c)}" stroke-width="{w}"/>')
    for g, (c, w, (lx, ly)) in geometry.items():
        v = None if g in HIDDEN_SEGMENTS else segs.get(g)
        d = path_d(c)
        L = bez_len(c)
        up = source(g)
        grey = " noflow" if up else ""
        if v is not None and v.get("calcif"):
            # Calcification: a grey band behind the vessel, wider than it, over the whole segment, with pale
            # calcium plates along the vessel wall.
            bands.append(f'<g class="calc"><title>{escape(SEGMENT_NAMES[g])}: calcification flag</title>'
                         f'<path class="calc-edge" d="{d}" stroke-width="{w + 23}"/>'
                         f'<path class="calc-band" d="{d}" stroke-width="{w + 19}"/></g>')
            plates.append(f'<g class="calc-plates">{calcium_plates(c, w)}</g>')
        if v is not None and v.get("cto"):
            # Occluded: contrast stops at the block; no antegrade flow is drawn beyond it (grey vessel).
            near, far = split(c, CTO_AT)
            mx, my = bez(c, CTO_AT)
            tx, ty = tangent(c, CTO_AT)
            nx, ny, h = -ty, tx, w / 2 + 7
            vessels.append(f'<path class="vessel{grey}" d="{path_d(near)}" stroke-width="{w}">{no_flow(up) if up else ""}</path>'
                           f'<path class="vessel noflow" d="{path_d(far)}" stroke-width="{w}">{no_flow(up or g)}</path>')
            plaques.append(f'<line class="cto-mark" x1="{mx - nx * h:.1f}" y1="{my - ny * h:.1f}" '
                           f'x2="{mx + nx * h:.1f}" y2="{my + ny * h:.1f}" stroke-width="5"/>')
        elif up:
            vessels.append(f'<path class="vessel noflow" d="{d}" stroke-width="{w}">{no_flow(up)}</path>')
        else:
            vessels.append(f'<path class="vessel" d="{d}" stroke-width="{w}"/>')
        if v is None:
            continue
        pct, flagged = displayed_pct(v), is_flagged(v)
        # Plaque: an amber wall around a lumen narrowed in proportion to the estimated stenosis.
        if pct >= 15 and not v.get("cto"):
            lumen = max(w * (1 - pct / 100), 1.2)
            dash = f'stroke-dasharray="{L * .18:.1f} {L * 2:.1f}" stroke-dashoffset="{-L * .41:.1f}"'
            plaques.append(f'<path class="plaque" d="{d}" stroke-width="{w + 3}" {dash}/>'
                           f'<path class="lumen{grey}" d="{d}" stroke-width="{lumen:.1f}" {dash}/>')
        if v.get("thrombus"):
            flagged_thrombus.append((g, c, w, bool(v.get("cto"))))
        mx, my = bez(c, 0.5)
        tags = lesion_tags(v)
        line1 = f"{SHORT[g]} {pct}%" if pct else f"{SHORT[g]} normal"
        line2 = (f"P {shown(v['prob_significant'], v['prob_threshold'], 3)} > {v['prob_threshold']:.3f}"
                 if v["significant"] else "")
        line3 = " · ".join(f"CTO · est. {v['stenosis_pct']:.0f}%" if t == "CTO" else t for t in tags)
        lines = [s for s in (line1, line2, line3) if s]
        bw = max(len(s) * (7.6 if i == 0 else 7.0) for i, s in enumerate(lines)) + 18
        bh = 12 + 17 * len(lines)
        cls = "lbl flag" if flagged else ("lbl tagged" if tags else "lbl")
        cx, cy = min(max(mx, lx), lx + bw), min(max(my, ly), ly + bh)
        boxes.append((lx, ly, lx + bw, ly + bh))
        pins.append(f'<line class="leader" x1="{cx:.0f}" y1="{cy:.0f}" x2="{mx:.0f}" y2="{my:.0f}"/>'
                    f'<circle class="pin" cx="{mx:.0f}" cy="{my:.0f}" r="3.4"/>')
        tip = (f"{SEGMENT_NAMES[g]}: shown {pct_text(v)} ({CATEGORY_LABELS[display_category(v)]}); "
               f"{'occluded (CTO flag), ' if v.get('cto') else ''}model estimate "
               f"{v['stenosis_pct']:.1f}%, P(significant) "
               f"{shown(v['prob_significant'], v['prob_threshold'], 3)} vs threshold {v['prob_threshold']:.4f}"
               f" ({'flagged' if flagged else 'not flagged'})"
               + "".join(f"; P({LESION_NAMES[k]}) {shown(v[k + '_prob'], cuts[k], 2)}" for k in LESION_NAMES
                         if k + "_prob" in v and k in cuts)
               + (f"; no antegrade flow shown (distal to the CTO flag of {SEGMENT_NAMES[up]})" if up else ""))
        text = "".join(f'<text x="{lx + 9}" y="{ly + 21 + 17 * i}" class="{"t1" if i == 0 else "t2"}">{escape(s)}</text>'
                       for i, s in enumerate(lines))
        labels.append(f'<g class="{cls}"><title>{escape(tip)}</title>'
                      f'<rect x="{lx}" y="{ly}" width="{bw:.0f}" height="{bh}" rx="3"/>{text}</g>')
    for g, c, w, cto in flagged_thrombus:
        # Clot in the lumen: just proximal to a CTO block, otherwise beside the plaque; moved along the segment
        # when a label box would hide it there.
        r = max(w * .6, 8)
        places = (0.3, 0.18, 0.4, 0.08) if cto else (0.68, 0.32, 0.84, 0.16, 0.5, 0.94, 0.06)
        hidden = lambda t, pad: any(x0 - pad < bez(c, t)[0] < x1 + pad and y0 - pad < bez(c, t)[1] < y1 + pad
                                    for x0, y0, x1, y1 in boxes)
        t = next((t for pad in (r, 0) for t in places if not hidden(t, pad)), places[0])  # clear, else half visible
        px, py = bez(c, t)
        tx, ty = tangent(c, t)
        angle = math.degrees(math.atan2(ty, tx))
        if abs(angle) > 90:  # same clot, not upside down
            angle -= 180
        clots.append(clot(px, py, angle, r, f"{SEGMENT_NAMES[g]}: thrombus flag"))
    return (f'<svg class="schema" viewBox="0 0 {VIEW_W} {VIEW_H}" role="img" '
            f'aria-label="{"Left" if left_dominant else "Right"}-dominant coronary schema with the estimated '
            'stenosis of each segment; calcified segments on a grey band with calcium plates, vessels distal to a '
            'chronic total occlusion in grey, a clot symbol on segments flagged for thrombus">'
            '<text x="300" y="80" class="ostium" text-anchor="middle">RCA ostium</text>'
            '<text x="420" y="80" class="ostium" text-anchor="middle">LM ostium</text>'
            f'<g class="calc-bands">{"".join(bands)}</g>'
            f'<g class="vessels">{"".join(vessels)}</g><g>{"".join(plates)}{"".join(plaques)}{"".join(clots)}</g>'
            f'<g>{"".join(pins)}</g><g>{"".join(labels)}</g></svg>')


def prob_bar(p: float, cut: float, hit: bool) -> str:
    # Square-root scale so small thresholds (0.02-0.2) stay readable next to probabilities near 1.
    sx = lambda x: f"{(max(x, 0) ** 0.5) * 100:.1f}%"
    return (f'<span class="bar{" hit" if hit else ""}"><span class="fill" style="width:{sx(p)}"></span>'
            f'<span class="cut" style="left:{sx(cut)}"></span></span>')


def detail_rows(segs: dict, cuts: dict) -> str:
    out = []
    for vessel, keys in VESSELS:
        rows = []
        for g in keys:
            v = segs.get(g)
            if v is None or g in HIDDEN_SEGMENTS:
                continue
            pct = displayed_pct(v)
            model = f'<span class="muted small"> est. {v["stenosis_pct"]:.0f}%</span>' if v.get("cto") else ""
            shown_pct = f"{pct}%" if pct else '<span class="normal">0%</span>'
            title = escape(f"{CATEGORY_LABELS[display_category(v)]}; model estimate {v['stenosis_pct']:.1f}%")
            tags = "".join(f'<span class="chip warn">{t}</span>' for t in lesion_tags(v))
            sub = " · ".join(f"{'CTO' if k == 'cto' else LESION_NAMES[k].capitalize()} {shown(v[k + '_prob'], cuts[k], 2)}"
                             for k in LESION_NAMES if k + "_prob" in v and k in cuts)
            rows.append(
                f'<li class="seg{" is-flag" if is_flagged(v) else ""}">'
                f'<span class="sname">{SEGMENT_NAMES[g]}{tags}</span>'
                f'<span class="pct {severity(pct)}" title="{title}">{shown_pct}{model}</span>'
                f'<span class="pcol">{prob_bar(v["prob_significant"], v["prob_threshold"], v["significant"])}'
                f'<span class="pnum">P {shown(v["prob_significant"], v["prob_threshold"], 3)}'
                f'<span class="muted"> / {v["prob_threshold"]:.4f}</span></span></span>'
                f'<span class="lesions muted">{sub}</span></li>')
        out.append(f'<section class="vessel-group"><h3>{vessel}</h3><ul>{"".join(rows)}</ul></section>')
    return "".join(out)


def physiology_card(p: dict) -> str:
    def row(k, label, cutoff):
        hit = p[f"{k}_abnormal"]
        prob, thr = p[f"{k}_abnormal_prob"], p[f"{k}_abnormal_threshold"]
        return (f'<div class="phys-row"><div class="phys-head"><span class="phys-name">{label}</span>'
                f'<span class="call {"bad" if hit else "ok"}">{"Abnormal" if hit else "Normal"}</span></div>'
                f'<div class="phys-value">{p[k]:.2f}<span class="muted"> estimated</span></div>'
                f'{prob_bar(prob, thr, hit)}'
                f'<div class="muted small">P({label} ≤ {cutoff}) {shown(prob, thr, 2)}, threshold {thr:.4f}</div></div>')
    return (f'<section class="card"><h2>LAD physiology</h2>{row("ffr", "FFR", "0.80")}{row("ifr", "iFR", "0.89")}'
            f'<p class="muted small">Operating point: {escape(p["operating_point"])}. Test AUROC about 0.69; '
            f'does not replace invasive FFR/iFR.</p></section>')


CSS = """
/* Layout: header + verdict strip, then the coronary schema beside a narrow side column, then per-vessel detail. */
:root{
  --paper:#f7f6f4; --card:#ffffff; --ink:#1d1f24; --muted:#636872; --line:#dcdad6;
  --vessel:#7a1712; --vessel-edge:#3b0907; --plaque:#e8a521; --label:#e4e2df; --label-ink:#1d1f24;
  --flag:#1c3fae; --flag-ink:#ffffff; --thrombus:#5a2a8c; --ok:#1f7a4d; --warn:#b4570a; --bad:#b3261e;
  --sev-low:#8a6d1f; --sev-mid:#b4570a; --sev-high:#b3261e; --bar:#cfd3da;
  --noflow:#747983; --noflow-edge:#3f434b; --calc:#c9ccd2; --calc-edge:#70757f; --calc-plate:#ffffff;
  --thr-edge:#ffffff; --thr-shine:#b79be0;
  --font:system-ui,-apple-system,"Segoe UI",Roboto,"Helvetica Neue",Arial,sans-serif;
  --font-cond:"Arial Narrow","Roboto Condensed","Helvetica Neue",Arial,sans-serif;
}
@media (prefers-color-scheme: dark){:root:not([data-theme="light"]){
  color-scheme:dark; --paper:#15161a; --card:#1e2026; --ink:#ebecef; --muted:#9aa0aa; --line:#33363e;
  --vessel:#a3281f; --vessel-edge:#d0574c; --label:#2e3139; --label-ink:#ebecef; --flag:#3d63d8; --thrombus:#b48ce8;
  --ok:#4cc38a; --warn:#f0a14a; --bad:#ff7b72; --sev-low:#d9b44a; --sev-mid:#f0a14a; --sev-high:#ff7b72; --bar:#3a3e47;
  --noflow:#676d79; --noflow-edge:#a2a7b1; --calc:#8a8f9a; --calc-edge:#d5d8de; --calc-plate:#f4f5f7;
  --thr-edge:#15161a; --thr-shine:#eadcff;}}
:root[data-theme="dark"]{
  color-scheme:dark; --paper:#15161a; --card:#1e2026; --ink:#ebecef; --muted:#9aa0aa; --line:#33363e;
  --vessel:#a3281f; --vessel-edge:#d0574c; --label:#2e3139; --label-ink:#ebecef; --flag:#3d63d8; --thrombus:#b48ce8;
  --ok:#4cc38a; --warn:#f0a14a; --bad:#ff7b72; --sev-low:#d9b44a; --sev-mid:#f0a14a; --sev-high:#ff7b72; --bar:#3a3e47;
  --noflow:#676d79; --noflow-edge:#a2a7b1; --calc:#8a8f9a; --calc-edge:#d5d8de; --calc-plate:#f4f5f7;
  --thr-edge:#15161a; --thr-shine:#eadcff;}
*{box-sizing:border-box}
body{margin:0;background:var(--paper);color:var(--ink);font:14px/1.5 var(--font);
  padding-left:max(16px,env(safe-area-inset-left,0px));padding-right:max(16px,env(safe-area-inset-right,0px))}
.wrap{max-width:1180px;margin:0 auto;padding-block:24px 40px;display:grid;gap:20px}
header{display:flex;flex-wrap:wrap;justify-content:space-between;align-items:flex-end;gap:12px;
  border-bottom:2px solid var(--ink);padding-bottom:10px}
.eyebrow{font:600 11px/1 var(--font);letter-spacing:.14em;text-transform:uppercase;color:var(--muted)}
h1{margin:6px 0 0;font:700 24px/1.15 var(--font);letter-spacing:-.01em;text-wrap:balance}
h2{margin:0 0 12px;font:700 13px/1.2 var(--font);letter-spacing:.12em;text-transform:uppercase}
h3{margin:0 0 6px;font:600 12px/1.2 var(--font);letter-spacing:.1em;text-transform:uppercase;color:var(--muted)}
.meta{display:flex;flex-wrap:wrap;gap:6px 18px;color:var(--muted);font-size:12.5px;font-variant-numeric:tabular-nums}
.meta b{color:var(--ink);font-weight:600}
.research{border:1px solid var(--bad);color:var(--bad);border-radius:3px;padding:2px 7px;font-weight:600;font-size:11.5px;
  letter-spacing:.06em;text-transform:uppercase}
.verdict{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:12px}
.vcell{background:var(--card);border:1px solid var(--line);border-radius:4px;padding:12px 14px;display:grid;gap:4px;align-content:start}
.vcell .k{font-size:11.5px;letter-spacing:.08em;text-transform:uppercase;color:var(--muted);font-weight:600}
.vcell .v{font-size:15px;font-weight:600}
.vcell.alert{border-left:4px solid var(--flag)}
.vcell.warnc{border-left:4px solid var(--warn)}
.main{display:grid;grid-template-columns:minmax(0,1fr) 300px;gap:20px;align-items:start}
@media (max-width:900px){.main{grid-template-columns:1fr}}
.card{background:var(--card);border:1px solid var(--line);border-radius:4px;padding:16px}
.schema-card{padding:16px 16px 8px}
.schema-title{display:flex;justify-content:space-between;align-items:baseline;flex-wrap:wrap;gap:8px;
  border-bottom:1px solid var(--ink);padding-bottom:6px;margin-bottom:8px}
.schema-title h2{margin:0;font-variant:small-caps;letter-spacing:.08em;text-transform:none;font-size:16px}
.scroll{overflow-x:auto}
.schema{display:block;width:100%;min-width:640px;height:auto}
.vessel{fill:none;stroke:var(--vessel);stroke-linecap:round;stroke-linejoin:round;
  paint-order:stroke;filter:drop-shadow(0 0 .6px var(--vessel-edge))}
.plaque{fill:none;stroke:var(--plaque);stroke-linecap:butt}
.lumen{fill:none;stroke:var(--vessel);stroke-linecap:butt}
.cto-mark{stroke:var(--ink);stroke-linecap:round}
.vessel.noflow{stroke:var(--noflow);filter:drop-shadow(0 0 .6px var(--noflow-edge))}
.lumen.noflow{stroke:var(--noflow)}
.calc path{fill:none;stroke-linecap:butt}
.calc-edge{stroke:var(--calc-edge)}
.calc-band{stroke:var(--calc)}
.calc-plate{fill:var(--calc-plate);stroke:var(--calc-edge);stroke-width:1}
.thr-edge{fill:var(--thr-edge);stroke:var(--thr-edge);stroke-width:3.4}
.thr-lobe{fill:var(--thrombus)}
.thr-shine{fill:var(--thr-shine)}
.leader{stroke:var(--muted);stroke-width:1;stroke-dasharray:3 3}
.pin{fill:var(--card);stroke:var(--muted);stroke-width:1.2}
.lbl rect{fill:var(--label);stroke:none}
.lbl text{fill:var(--label-ink);font-family:var(--font-cond)}
.lbl .t1{font-size:14px;font-weight:700}
.lbl .t2{font-size:12px}
.lbl.flag rect{fill:var(--flag)}
.lbl.flag text{fill:var(--flag-ink)}
.lbl.tagged rect{stroke:var(--warn);stroke-width:1.5}
.ostium{fill:var(--muted);font-size:11px;letter-spacing:.06em;text-transform:uppercase}
.legend{display:flex;flex-wrap:wrap;gap:6px 16px;font-size:12px;color:var(--muted);padding:6px 2px 4px}
.legend span{display:inline-flex;align-items:center;gap:6px}
.sw{display:inline-block;width:18px;height:12px;border-radius:2px}
.sw.flag{background:var(--flag)} .sw.lbl{background:var(--label)} .sw.plq{background:linear-gradient(var(--plaque) 0 30%,var(--vessel) 30% 70%,var(--plaque) 70%)}
.sw-cartoon{display:inline-block;flex:none;overflow:visible}
.side{display:grid;gap:16px}
.phys-row{display:grid;gap:5px;padding-block:10px;border-top:1px solid var(--line)}
.phys-row:first-of-type{border-top:0;padding-top:0}
.phys-head{display:flex;justify-content:space-between;align-items:center}
.phys-name{font-weight:700}
.phys-value{font-size:22px;font-weight:700;font-variant-numeric:tabular-nums}
.phys-value .muted{font-size:12px;font-weight:400;margin-left:6px}
.call{font-size:11.5px;font-weight:700;letter-spacing:.06em;text-transform:uppercase;padding:2px 8px;border-radius:999px;border:1px solid}
.call.ok{color:var(--ok)} .call.bad{color:var(--bad)}
.muted{color:var(--muted)} .small{font-size:12px;margin:0}
.bar{position:relative;display:block;height:6px;background:var(--bar);border-radius:3px;overflow:visible}
.bar .fill{position:absolute;inset:0 auto 0 0;background:var(--muted);border-radius:3px}
.bar.hit .fill{background:var(--flag)}
.bar .cut{position:absolute;top:-4px;bottom:-4px;width:2px;background:var(--ink);transform:translateX(-1px)}
.thresholds dl{display:grid;grid-template-columns:1fr auto;gap:4px 12px;margin:0;font-variant-numeric:tabular-nums}
.thresholds dt{color:var(--muted)} .thresholds dd{margin:0;font-weight:600}
.details{display:grid;grid-template-columns:repeat(auto-fit,minmax(330px,1fr));gap:16px}
.vessel-group ul{list-style:none;margin:0;padding:0}
.seg{display:grid;grid-template-columns:minmax(0,1fr) 48px 140px;grid-template-areas:"n p b" "l l l";gap:2px 12px;
  align-items:center;padding:8px 6px;border-top:1px solid var(--line)}
.seg.is-flag{background:color-mix(in srgb,var(--flag) 9%,transparent);box-shadow:inset 3px 0 0 var(--flag)}
.sname{grid-area:n;font-weight:600;display:flex;flex-wrap:wrap;gap:6px;align-items:center}
.pct{grid-area:p;text-align:right;font-weight:700;font-variant-numeric:tabular-nums}
.pcol{grid-area:b;display:grid;gap:3px}
.pnum{font-size:11.5px;font-variant-numeric:tabular-nums}
.lesions{grid-area:l;font-size:11.5px;font-variant-numeric:tabular-nums}
.sev-none{color:var(--muted)} .pct .normal{color:var(--ok)} .sev-low{color:var(--sev-low)} .sev-mid{color:var(--sev-mid)} .sev-high{color:var(--sev-high)}
.chip{font-size:10.5px;font-weight:700;letter-spacing:.05em;text-transform:uppercase;padding:1px 6px;border-radius:3px}
.chip.warn{background:var(--warn);color:#fff}
.addendum{display:grid;gap:14px}
.addendum h2{margin:0}
.steps{margin:0;padding-left:1.3em;display:grid;gap:8px;max-width:95ch}
.addendum-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(280px,1fr));gap:16px 24px}
.addendum-grid p{margin:0}
.addendum dl{display:grid;grid-template-columns:1fr auto;gap:4px 12px;margin:0 0 8px;font-variant-numeric:tabular-nums}
.addendum dt{color:var(--muted)} .addendum dd{margin:0;font-weight:600;text-align:right}
.explain{display:grid;gap:10px}
.explain h2{margin:0}
.xgrid{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,420px),1fr));gap:16px}
.xcard{margin:0;display:grid;gap:8px;align-content:start;border-top:1px solid var(--line);padding-top:10px}
.xcard h3{margin:0;color:var(--ink);font-size:13px;display:flex;gap:10px;align-items:baseline}
.ximgs{display:grid;grid-template-columns:1fr 1fr;gap:8px}
.ximgs img{width:100%;height:auto;border-radius:3px;background:#000}
.disclaimer{font-size:12.5px;color:var(--muted);border-top:1px solid var(--line);padding-top:12px;max-width:80ch}
"""


ADDENDUM = """<section class="card addendum" id="method"><h2>Addendum: how this reading is made</h2>
<ol class="steps">
<li><b>Input.</b> 1 to 12 diagnostic left and right coronary contrast runs of the study (no PCI, stent or
ventriculography runs), 16 frames each, every second frame, centred on peak opacification.</li>
<li><b>Pass 1, segmentation.</b> One EchoJEPA ViT-L video encoder reads every run at 224 px. A mask decoder predicts
the model's own 26-class SYNTAX segment masks on every frame, with no manual tracing. The 26 classes are grouped
into the report segments (for example PDA = SYNTAX 4 and 19, posterolateral = 20 to 23).</li>
<li><b>Masking.</b> Each segment's features are pooled inside its own mask, so a segment is read from the pixels
the model assigned to it. On 10 test studies, 32% to 86% of the attention inside a crop fell on the segment's
mask.</li>
<li><b>Pass 2, tracked crops.</b> From those masks the model cuts up to 4 crops per segment at 320 px that follow
the segment across frames, and reads them with the same encoder. The encoder runs at most twice per study.</li>
<li><b>Readout.</b> Attention pooling over videos and crops, one bag per segment, gives the % stenosis,
P(significant) and the calcification, thrombus and CTO probabilities. A separate head on the left main, LAD and
diagonal features gives LAD FFR and iFR.</li>
</ol>
<div class="addendum-grid">
<div><h3>Lesion localization</h3><p>The model reliably finds the right segment; finding the lesion inside the
segment is not shown. On an external expert-annotated set (ARCADE, 300 images, 386 stenosis boxes) its attention
peak fell inside the expert box 53% of the time, against 50% for the centre of the segment mask. The marks on the
schema are therefore placed at the middle of each segment, not at the lesion's position on the angiogram.</p></div>
<div><h3>Not in this report</h3><p>The masks, crops and attention computed for this study are used internally but
not returned by this version, so they cannot be shown on the angiogram yet. The posterior LV branch is read but not
shown. Dominance comes from a separate angiogram classifier (majority of the diagnostic videos sent).</p></div>
<div><h3>Held-out performance (MHI test set, 3,194 studies)</h3><dl>
<dt>Stenosis, macro AUROC (18 segments)</dt><dd>0.885 (DeepCORO-CLIP 0.850)</dd>
<dt>Segment flags: sensitivity / specificity</dt><dd>0.80 / 0.80</dd>
<dt>Segment flags: PPV / NPV</dt><dd>0.25 / 0.98</dd>
<dt>LAD FFR/iFR, AUROC</dt><dd>0.69</dd>
<dt>Calcification / thrombus / CTO flags: sensitivity</dt><dd>0.19 / 0.51 / 0.37</dd>
<dt>Same flags: PPV</dt><dd>0.27 / 0.10 / 0.21</dd>
</dl><p class="muted small">Distal and branch segments agree less with the report (Pearson r 0.20 to 0.48).
Trained on 22,515 MHI studies; thresholds come from the MHI validation split and have not been validated at
another site.</p></div>
</div>
<p class="muted small">Sources: DeepCORO-JEPA model documentation (container README, model card and the DeepCORO
model and experiment pages, v6 to v13).</p>
</section>"""


def calibration_note() -> str:
    t = calibration()
    xs, ys = t["knots"]["estimate_pct"], t["knots"]["display_pct"]
    knots = ", ".join(f"{x:g}→{y:g}" for x, y in zip(xs, ys))
    return escape(f"Shown %: the model estimate is pulled toward the mean, so it is rescaled for display "
                  f"({t['version']}). Estimates below {t['zero_below_pct']:g}% are shown as 0% (normal); above, the "
                  f"estimate is mapped through {knots} so the shown values follow the angiographic categories "
                  f"<30, 30-49, 50-69 and ≥70%. A segment flagged for significant stenosis is never shown as normal; "
                  f"a CTO flag is shown as 100%. The flags come from the probability, not from the shown %. "
                  f"Fitted on {t['provenance']['dataset_short']}.")


def explain_section(items: list[dict], segs: dict) -> str:
    """Where the model read each flagged segment: the crop with the highest attention, at pixel level."""
    if not items:
        return ""
    cards = []
    for it in items:
        g = it["segment"]
        v = segs.get(g, {})
        head = (f'<h3>{escape(SEGMENT_NAMES.get(g, g))} <span class="pct {severity(displayed_pct(v)) if v else ""}">'
                f'{pct_text(v)}</span></h3>' if v else f"<h3>{escape(g)}</h3>")
        if not it.get("has_crop"):
            cards.append(f'<figure class="xcard none">{head}<p class="muted small">No tracked crop for this segment '
                         f'(its mask was too small in every video); the reading comes from the full views only.</p></figure>')
            continue
        img = it.get("images")
        view = it.get("max_full_view_attention")
        share = (f"This crop carried {it['bag_attention'] * 100:.0f}% of the segment's attention"
                 + (f" (best full view {view * 100:.0f}%)." if view is not None else "."))
        pictures = (f'<div class="ximgs">'
                    f'<img src="data:image/jpeg;base64,{img["crop"]}" alt="Tracked crop of {escape(SEGMENT_NAMES.get(g, g))} '
                    f'with the model attention overlaid" width="320" height="320">'
                    f'<img src="data:image/jpeg;base64,{img["frame"]}" alt="Full frame with the crop box"></div>'
                    if img else '<p class="muted small">The images were not stored with this result '
                                '(results before version 1.3.0 kept only the metadata).</p>')
        cards.append(
            f'<figure class="xcard">{head}{pictures}<figcaption class="muted small">Video {it["video"] + 1}, frame {it["frame"] + 1} of 16. {share} '
            f'The top 10% of crop pixels hold {it["top10pct_token_share"] * 100:.0f}% of the attention; + marks the peak.'
            f'</figcaption></figure>')
    return (f'<section class="card explain"><h2>Where the model looked</h2>'
            f'<p class="muted small">For each flagged segment: the tracked crop that weighed most in its reading, with '
            f'the attention the model applied inside it (bright = more weight), and the full frame with the crop box. '
            f'Attention shows where the segment was read, not a measured lesion; its peak fell inside the expert '
            f'lesion box about half the time on an external set.</p><div class="xgrid">{"".join(cards)}</div></section>')


def dominance_text(dom: dict | None) -> tuple[str, str]:
    """(value, detail) for the summary strip."""
    if not dom:
        return "Not provided", "Schema drawn right dominant (the usual anatomy)."
    votes = f"{dom['left_votes'] if dom['call'] == 'left' else dom['right_votes']} of {dom['video_count']} diagnostic videos"
    how = (f"majority, {votes}" if dom["method"] == "majority"
           else f"tie ({dom['left_votes']}/{dom['right_votes']}) broken by mean P(left) {dom['mean_left_probability']:.2f}")
    return f"{dom['call'].capitalize()} dominant", f"Angiogram classifier, {how}."


def render_report(preds: dict, model_version: str, recommendation: str) -> str:
    segs = {g: v for g, v in preds["segments"].items() if g not in HIDDEN_SEGMENTS}
    cuts = preds.get("lesion_thresholds", {})
    dom = preds.get("coronary_dominance")
    flagged = [g for g, v in segs.items() if is_flagged(v)]
    dom_value, dom_detail = dominance_text(dom)
    verdict = [
        ("alert" if flagged else "", "Significant stenosis flagged",
         ", ".join(f"{SEGMENT_NAMES[g]} ({pct_text(segs[g])}{', occluded' if segs[g].get('cto') else ''})"
                   for g in flagged) or "None", ""),
        ("", "Coronary dominance", dom_value, dom_detail),
    ]
    for k, label in LESION_NAMES.items():
        if k in cuts:
            hit = [g for g, v in segs.items() if v.get(k)]
            verdict.append(("warnc" if hit else "", label.capitalize(),
                            ", ".join(f"{SEGMENT_NAMES[g]} (P {shown(segs[g][k + '_prob'], cuts[k], 2)})" for g in hit) or "None flagged", ""))
    phys = preds.get("lad_physiology")
    if phys:
        call = lambda k: "abnormal" if phys[f"{k}_abnormal"] else "normal"
        verdict.append(("alert" if phys["ffr_abnormal"] or phys["ifr_abnormal"] else "", "LAD physiology",
                        f"FFR {call('ffr')} · iFR {call('ifr')}", ""))
    verdict_html = "".join(f'<div class="vcell {c}"><span class="k">{k}</span><span class="v">{escape(v)}</span>'
                           + (f'<span class="muted small">{escape(d)}</span>' if d else "") + "</div>"
                           for c, k, v, d in verdict)
    thr = ""
    if cuts:
        thr = ('<section class="card thresholds"><h2>Lesion flag thresholds</h2><dl>'
               + "".join(f"<dt>{LESION_NAMES[k].capitalize()}</dt><dd>&gt; {cuts[k]:.4f}</dd>" for k in LESION_NAMES if k in cuts)
               + f'</dl><p class="muted small" style="margin-top:8px">Operating point {escape(cuts["operating_point"])}: '
               'few false alarms, misses many of these findings.</p></section>')
    n = preds["n_videos"]
    left = bool(dom and dom["call"] == "left")
    drawn = ("Left dominant" if left else "Right dominant") + ("" if dom else " (dominance not provided)")
    return f"""<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>DeepCORO-JEPA Coronary Report</title>
<style>{CSS}</style></head><body>
<div class="wrap">
<header>
  <div><div class="eyebrow">AI coronary angiography reading</div>
  <h1>DeepCORO-JEPA {escape(model_version)}</h1></div>
  <div class="meta"><span><b>{n}</b> video{'' if n == 1 else 's'}</span><span><b>{preds['encoder_passes']}</b> encoder passes</span>
  <span><b>{len(segs)}</b> segments shown</span><span class="research">Research use only</span></div>
</header>
<div class="verdict">{verdict_html}</div>
<div class="main">
  <section class="card schema-card">
    <div class="schema-title"><h2>Schéma coronaire</h2><span class="muted small">{drawn}. Estimated % diameter stenosis per segment, calibrated for display (see the note under the segment detail); hover a label for the model estimate and the probabilities.</span></div>
    <div class="scroll">{schema_svg(segs, cuts, left_dominant=left)}</div>
    <div class="legend"><span><i class="sw flag"></i>Flagged: P(significant) above its segment threshold, or CTO</span>
      <span><i class="sw lbl"></i>Not flagged</span><span><i class="sw plq"></i>Plaque, lumen narrowed to the shown % (≥15%)</span>
      <span>{legend_cartoon("cto")}Occluded (CTO flag): reported as 100%; no antegrade flow shown distal to it (grey vessel)</span>
      <span>{legend_cartoon("calcif")}Calcification flag: grey band with calcium plates along the segment</span>
      <span>{legend_cartoon("thrombus")}Thrombus flag: clot in the lumen</span></div>
  </section>
  <aside class="side">{physiology_card(phys) if phys else ''}{thr}</aside>
</div>
{explain_section(preds.get("explainability") or [], segs)}
<section class="card"><h2>Segment detail</h2>
  <p class="muted small" style="margin-bottom:10px">Bar: P(significant stenosis) on a square-root scale; the black tick is the segment's validation threshold. Significant means ≥70% (≥50% for left main).</p>
  <p class="muted small" style="margin-bottom:10px">{calibration_note()}</p>
  <div class="details">{detail_rows(segs, cuts)}</div></section>
<p class="disclaimer">{escape(recommendation)}</p>
{ADDENDUM}
</div></body></html>"""


if __name__ == "__main__":
    # python3 report_html.py docs/examples/predict_response_json.json docs/examples/predict_report.html [model_version]
    # Same report as HTML-mode predict and /inference/render-report for that response (model_version "v13-cal1").
    data = json.load(open(sys.argv[1]))["data"]
    out = render_report(data["predictions"], sys.argv[3] if len(sys.argv) > 3 else "v13-cal1", data["modelRecommendations"]["en"])
    open(sys.argv[2], "w").write(out + "\n")
