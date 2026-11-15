"""Schema drawing of the lesion flags: calcification band and plates, no antegrade flow distal to a CTO, clot symbol.
Drawing only: the predictions and the JSON response are not changed."""
from __future__ import annotations

import base64
import copy
import json
import re
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

import report_html  # noqa: E402
from report_html import GEOMETRY, GEOMETRY_LEFT, PARENT, PARENT_LEFT, occluded_upstream, render_report, schema_svg  # noqa: E402
from report_input import predictions_of  # noqa: E402

CUTS = {"operating_point": "test", "calcif": 0.2, "thrombus": 0.2, "cto": 0.2}
JPEG = base64.b64encode(b"\xff\xd8\xff\xe0" + b"\x00" * 20).decode("ascii")
JPEG_2 = base64.b64encode(b"\xff\xd8\xff\xe0" + b"\x01" * 20).decode("ascii")


def _segments(**flags) -> dict:
    """All 18 segments, unremarkable, with the given lesion flags: _segments(mid_rca=("cto",))."""
    segs = {}
    for g in GEOMETRY:
        on = flags.get(g, ())
        segs[g] = {"stenosis_pct": 30.0, "prob_significant": 0.01, "prob_threshold": 0.2, "significant": False}
        for k in ("calcif", "thrombus", "cto"):
            segs[g].update({f"{k}_prob": 0.9 if k in on else 0.01, k: k in on})
    return segs


def _crop(segment: str, crop: str = JPEG) -> dict:
    return {"segment": segment, "has_crop": True, "video": 1, "frame": 6, "bag_attention": 0.7,
            "max_full_view_attention": 0.2, "top10pct_token_share": 0.4, "images": {"crop": crop, "frame": JPEG_2}}


def _grey(segs: dict, left: bool) -> set:
    """Segments drawn without antegrade flow over their whole length."""
    source, _ = occluded_upstream(segs, left)
    return {g for g in GEOMETRY if source(g)}


def _paths(svg: str, cls: str) -> list[str]:
    return re.findall(rf'<path class="{cls}" d="([^"]+)"', svg)


# -- topology ----------------------------------------------------------------

def test_the_tree_is_connected_as_drawn():
    for geometry, parent in ((GEOMETRY, PARENT), (GEOMETRY_LEFT, PARENT_LEFT)):
        assert set(parent) | {"prox_rca", "left_main"} == set(geometry)
        for g, p in parent.items():
            (sx, sy), t = geometry[g][0][0], report_html.attach_t(geometry[p][0], geometry[g][0][0])
            px, py = report_html.bez(geometry[p][0], t)
            assert ((sx - px) ** 2 + (sy - py) ** 2) ** 0.5 < 12, (g, p)  # each branch starts on its parent


RIGHT_CASES = [
    ("prox_rca", {"mid_rca", "dist_rca", "pda", "posterolateral", "lvp"}),
    ("mid_rca", {"dist_rca", "pda", "posterolateral", "lvp"}),
    ("dist_rca", {"pda", "posterolateral", "lvp"}),
    ("left_main", {"prox_lad", "mid_lad", "dist_lad", "D1", "D2", "bx", "prox_lcx", "mid_lcx", "dist_lcx", "om1", "om2"}),
    ("prox_lad", {"mid_lad", "dist_lad", "D1", "D2"}),          # D1 arises beyond the block mark
    ("mid_lad", {"dist_lad", "D2"}),                            # D1 arises proximal to it and keeps its flow
    ("prox_lcx", {"mid_lcx", "dist_lcx", "om1", "om2"}),
    ("mid_lcx", {"dist_lcx"}),                                  # OM2 arises before the block mark
    ("D1", set()), ("om1", set()), ("pda", set()), ("bx", set()), ("dist_lad", set()),   # side branch or end vessel
    ("posterolateral", {"lvp"}),                                # only the unlabelled posterior LV branch is distal
]
LEFT_CASES = [
    ("prox_rca", {"mid_rca", "dist_rca"}),
    ("dist_rca", set()),                                        # the small right coronary ends there
    ("mid_lcx", {"dist_lcx", "pda", "posterolateral", "lvp"}),
    ("dist_lcx", {"pda", "posterolateral", "lvp"}),
    ("prox_lcx", {"mid_lcx", "dist_lcx", "om1", "om2", "pda", "posterolateral", "lvp"}),
    ("left_main", set(GEOMETRY) - {"prox_rca", "mid_rca", "dist_rca", "left_main"}),
    ("mid_lad", {"dist_lad", "D2"}), ("D2", set()), ("pda", set()),
]


@pytest.mark.parametrize("left, occluded, distal",
                         [(False, g, d) for g, d in RIGHT_CASES] + [(True, g, d) for g, d in LEFT_CASES])
def test_segments_distal_to_a_cto_are_grey_and_the_others_are_not(left, occluded, distal):
    segs = _segments(**{occluded: ("cto",)})
    assert _grey(segs, left) == distal
    svg = schema_svg(segs, CUTS, left_dominant=left)
    geometry = GEOMETRY_LEFT if left else GEOMETRY
    grey, red = _paths(svg, "vessel noflow"), _paths(svg, "vessel")
    for g, (c, _w, _label) in geometry.items():
        d = report_html.path_d(c)
        if g == occluded:  # red up to the block mark, grey beyond it
            near, far = report_html.split(c, report_html.CTO_AT)
            assert report_html.path_d(near) in red and report_html.path_d(far) in grey and d not in red + grey
        elif g in distal:
            assert d in grey and d not in red, g
        else:
            assert d in red and d not in grey, g
    assert svg.count('class="cto-mark"') == 1
    name = report_html.SEGMENT_NAMES[occluded]
    assert svg.count(f"no antegrade flow shown (distal to the CTO flag of {name})") == len(distal - {"lvp"})  # hover text of each labelled segment


def test_without_a_cto_nothing_is_grey():
    for left in (False, True):
        svg = schema_svg(_segments(mid_lad=("calcif", "thrombus")), CUTS, left_dominant=left)
        assert "noflow" not in svg and "antegrade" not in svg


def test_unlabelled_twigs_follow_the_vessel_they_arise_from():
    acute_marginal = report_html.path_d(report_html.DECOR[0][0])  # arises from the mid RCA before its block mark
    svg = schema_svg(_segments(mid_rca=("cto",)), CUTS)
    assert acute_marginal in _paths(svg, "vessel") and acute_marginal not in _paths(svg, "vessel noflow")
    svg = schema_svg(_segments(prox_rca=("cto",)), CUTS)
    assert acute_marginal in _paths(svg, "vessel noflow")
    assert all(report_html.path_d(c) in _paths(svg, "vessel noflow") for c, _w, _p in report_html.DECOR)


def test_a_flagged_segment_distal_to_a_cto_keeps_its_label_and_marks():
    segs = _segments(prox_rca=("cto",), mid_rca=("cto",), dist_rca=("calcif", "thrombus"))
    segs["pda"].update(stenosis_pct=60.0, prob_significant=0.9, significant=True)
    svg = schema_svg(segs, CUTS)
    assert svg.count('class="cto-mark"') == 2 and svg.count('class="thrombus"') == 1 and svg.count('class="calc"') == 1
    assert svg.count('class="thr-lobe"') == 5 and svg.count('class="calc-plate"') == 6
    assert f"Dist RCA {report_html.displayed_pct(segs['dist_rca'])}%" in svg and "Calcified · Thrombus" in svg and '<g class="lbl flag">' in svg
    # the plaques of the distal RCA, PDA and posterolateral stay, their lumen in the grey of the vessel
    assert svg.count('class="lumen noflow"') == 3 and svg.count('class="plaque"') == 15
    # the occluded mid RCA is grey on both sides of its own mark: it lies beyond the proximal occlusion
    near, far = report_html.split(GEOMETRY["mid_rca"][0], report_html.CTO_AT)
    assert {report_html.path_d(near), report_html.path_d(far)} <= set(_paths(svg, "vessel noflow"))


def test_the_hidden_posterior_lv_branch_does_not_block_anything():
    segs = _segments(lvp=("cto", "calcif", "thrombus"))
    svg = schema_svg({g: v for g, v in segs.items() if g != "lvp"}, CUTS)
    assert "noflow" not in svg and "cto-mark" not in svg and 'class="calc"' not in svg
    assert "noflow" not in schema_svg(segs, CUTS)  # also when the caller passes it


# -- calcification -------------------------------------------------------------

@pytest.mark.parametrize("left", [False, True])
def test_a_calcified_segment_gets_a_grey_band_wider_than_the_vessel(left):
    svg = schema_svg(_segments(mid_rca=("calcif",), dist_lcx=("calcif",)), CUTS, left_dominant=left)
    geometry = GEOMETRY_LEFT if left else GEOMETRY
    bands = re.findall(r'<g class="calc"><title>([^<]+)</title><path class="calc-edge" d="([^"]+)" stroke-width="([\d.]+)"/>'
                       r'<path class="calc-band" d="([^"]+)" stroke-width="([\d.]+)"/></g>', svg)
    assert [b[0] for b in bands] == ["Mid RCA: calcification flag", "Distal LCx: calcification flag"]
    for (_title, d_edge, w_edge, d_band, w_band), g in zip(bands, ("mid_rca", "dist_lcx")):
        c, w, _label = geometry[g]
        assert d_edge == d_band == report_html.path_d(c) and float(w_edge) > float(w_band) >= w + 12
    assert svg.index('class="calc-bands"') < svg.index('class="vessels"')  # behind the vessel
    # calcium plates on the vessel wall, drawn over the vessels
    assert svg.count('<g class="calc-plates">') == 2 and svg.count('class="calc-plate"') == 12
    assert svg.index('class="vessels"') < svg.index('class="calc-plates"')
    assert svg.count("Calcified</text>") == 2  # the label tag stays


def test_no_band_without_a_calcification_flag():
    svg = schema_svg(_segments(mid_rca=("cto", "thrombus")), CUTS)
    assert 'class="calc"' not in svg and "calc-plate" not in svg


# -- thrombus -------------------------------------------------------------------

CLOT = re.compile(r'<g class="thrombus" transform="translate\(([\d.]+) ([\d.]+)\) rotate\((-?\d+)\)"><title>([^<]+)</title>'
                  r'((?:<circle class="thr-edge"[^>]+/>)+)((?:<circle class="thr-lobe"[^>]+/>)+)')


def _near(point, c, t) -> bool:
    x, y = report_html.bez(c, t)
    return abs(float(point[0]) - x) < 0.1 and abs(float(point[1]) - y) < 0.1


@pytest.mark.parametrize("left", [False, True])
@pytest.mark.parametrize("flags, at", [
    (("thrombus",), 0.68),
    (("thrombus", "calcif"), 0.68),
    (("thrombus", "cto"), 0.3),             # just proximal to the block mark
    (("thrombus", "cto", "calcif"), 0.3),
])
def test_a_thrombus_flag_draws_a_clot_in_the_lumen(left, flags, at):
    svg = schema_svg(_segments(dist_rca=flags), CUTS, left_dominant=left)
    clots = CLOT.findall(svg)
    assert len(clots) == svg.count('class="thrombus"') == 1
    x, y, _angle, title, edge, lobes = clots[0]
    c, w, _label = (GEOMETRY_LEFT if left else GEOMETRY)["dist_rca"]
    assert title == "Distal RCA: thrombus flag" and _near((x, y), c, at)
    assert ("cto" in flags) == (at < report_html.CTO_AT)
    # a lobulated shape, outlined: the same lobes drawn as outline then as fill, at least as high as the vessel
    radii = [float(r) for r in re.findall(r' r="([\d.]+)"', lobes)]
    assert edge.count("<circle") == lobes.count("<circle") == 5 and len(set(radii)) > 3 and 2 * max(radii) >= max(w, 13)
    assert svg.count('class="cto-mark"') == ("cto" in flags) and svg.count('class="calc"') == ("calcif" in flags)
    assert "Thrombus" in svg  # the label tag stays
    # over the vessel, the calcium plates and the plaque; under the labels
    assert svg.index('class="vessels"') < svg.index('class="thrombus"') < svg.index('class="lbl')
    if "calcif" in flags:
        assert svg.index('class="calc-plates"') < svg.index('class="thrombus"')


def test_no_clot_without_a_thrombus_flag():
    svg = schema_svg(_segments(mid_lad=("cto", "calcif")), CUTS)
    assert 'class="thrombus"' not in svg and "thr-lobe" not in svg


def test_one_clot_per_flagged_segment_never_upside_down():
    flags = {g: ("thrombus",) for g in GEOMETRY if g != "lvp"}
    for left in (False, True):
        clots = CLOT.findall(schema_svg(_segments(**flags), CUTS, left_dominant=left))
        assert len(clots) == 17 and all(-90 <= int(c[2]) <= 90 for c in clots)
        assert len({c[3] for c in clots}) == 17


@pytest.mark.parametrize("left", [False, True])
def test_a_clot_is_moved_along_its_segment_when_a_label_would_hide_it(left):
    flags = {g: ("thrombus", "calcif") for g in GEOMETRY if g != "lvp"}  # two-line labels, the widest case
    svg = schema_svg(_segments(**flags), CUTS, left_dominant=left)
    labels = [tuple(map(float, m)) for m in re.findall(r'<rect x="([\d.]+)" y="([\d.]+)" width="([\d.]+)" height="([\d.]+)" rx="3"/><text', svg)]
    clots = CLOT.findall(svg)
    assert len(labels) == len(clots) == 17
    for x, y, *_ in clots:
        assert not any(lx < float(x) < lx + lw and ly < float(y) < ly + lh for lx, ly, lw, lh in labels), "clot under a label"


def test_the_schema_embeds_no_image_even_when_the_result_has_crops():
    preds = _preds()
    assert "explained" not in schema_svg.__code__.co_varnames
    html = render_report(preds, "v13-cal1", "x")
    schema = html[html.index('<svg class="schema"'):html.index("</svg>", html.index('<svg class="schema"'))]
    assert "<image" not in schema and "data:image" not in schema and "thr-thumb" not in html and "thr-leader" not in html
    # the crops stay in the explanation section, lower on the page
    assert html.index('class="card explain"') > html.index('<svg class="schema"')
    assert html.count("data:image/jpeg;base64,") == 4


# -- whole report ----------------------------------------------------------------

def _preds() -> dict:
    segs = {g: v for g, v in _segments(mid_rca=("cto",), prox_lad=("calcif",), mid_lad=("thrombus",), om1=("thrombus",)).items()}
    return {"n_videos": 3, "encoder_passes": 2, "segments": segs, "lesion_thresholds": dict(CUTS),
            "explainability": [_crop("mid_lad"), _crop("mid_rca", JPEG_2)]}


def test_the_report_carries_the_three_cartoons_their_legend_and_no_script():
    preds = _preds()
    stored = json.loads(json.dumps(preds))
    html = render_report(predictions_of({"predictions": stored}), "v13-cal1", "x")
    assert stored == preds  # rendering reads the result, it does not change it
    assert html.count('class="calc"') == 1 and html.count('class="thrombus"') == 2 and html.count('class="cto-mark"') == 1
    # far half of the mid RCA, distal RCA, PDA, posterolateral, posterior LV branch and their 2 unlabelled twigs
    assert html.count('class="vessel noflow"') == 1 + 4 + 2
    legend = html[html.index('<div class="legend">'):html.index("</section>", html.index('<div class="legend">'))]
    assert "no antegrade flow shown distal to it (grey vessel)" in legend
    assert "Calcification flag: grey band with calcium plates along the segment" in legend
    assert "Thrombus flag: clot in the lumen" in legend
    # the legend shows the same cartoons at small size: block and grey vessel, band and plates, clot
    assert legend.count('<svg class="sw-cartoon"') == 3 and legend.count('aria-hidden="true"') == 3
    assert 'class="key cto-mark"' in legend and 'class="key vessel noflow"' in legend
    assert 'class="calc-band"' in legend and legend.count('class="calc-plate"') == 3
    assert legend.count('class="key thrombus"') == 1 and legend.count('class="thr-lobe"') == 5
    assert "<script" not in html.lower() and "http://" not in html and "https://" not in html and "url(" not in html
    assert "<image" not in html and html.count("data:image/jpeg;base64,") == 4  # the two explanation cards only
    for token in ("--noflow:", "--calc:", "--calc-edge:", "--calc-plate:", "--thr-edge:"):
        assert html.count(token) == 3  # light palette, dark by preference, dark by data-theme


def test_the_live_html_answer_equals_render_report_with_the_three_cartoons(monkeypatch):
    from fastapi.testclient import TestClient

    import test_render_report as shared
    from logic import CustomPredictionService

    preds = shared._prediction()
    index = [it["segment"] for it in preds["explainability"]].index("mid_rca")
    preds["segments"]["mid_rca"].update(thrombus=True, calcif=True)  # CTO, thrombus and calcification, with a stored crop
    monkeypatch.setattr(CustomPredictionService, "is_initialized", True)
    monkeypatch.setattr(CustomPredictionService, "load_model", lambda self, config: None)
    monkeypatch.setattr(CustomPredictionService, "_predict", lambda self, request: copy.deepcopy(preds))
    import os
    cwd = os.getcwd()
    os.chdir(ROOT)
    try:
        import main
    finally:
        os.chdir(cwd)
    client = TestClient(main.app)
    stored = client.post("/inference/predict", json={"outputMode": "JSON"}).json()
    live = client.post("/inference/predict", json={"outputMode": "HTML"}).json()
    assert stored["data"]["predictions"] == json.loads(json.dumps(CustomPredictionService.public(preds)))  # JSON unchanged
    shared._no_model(monkeypatch)
    again = client.post("/inference/render-report", json={"result": json.loads(json.dumps(stored["data"]))}).json()
    assert again["data"]["htmlBase64"] == live["data"]["htmlBase64"]
    html = base64.b64decode(live["data"]["htmlBase64"]).decode("utf-8")
    crop = preds["explainability"][index]["images"]["crop"]
    assert html.count(crop) == 1 and "<image" not in html  # the crop only in the explanation section
    assert html.count('class="calc"') == 1 and html.count('class="thrombus"') == 2 and "calc-plates" in html
    assert 'class="vessel noflow"' in html
