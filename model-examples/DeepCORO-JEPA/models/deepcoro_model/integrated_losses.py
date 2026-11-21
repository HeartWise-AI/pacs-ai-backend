"""Study-batch supervision; CCC is computed across studies per anatomical group."""
from __future__ import annotations

import torch
from torch import Tensor
import torch.nn.functional as F


def concordance_loss(prediction: Tensor, target: Tensor, eps: float = 1e-8):
    """Mean 1-CCC over nondegenerate anatomical groups, never across anatomy.

    Inputs are (studies, groups). At least two observed, finite study pairs and
    nonzero target variance are needed per group. Constant predictions remain
    eligible: they must receive a learning signal. Missing labels are ignored.
    """
    if prediction.ndim != 2 or target.shape != prediction.shape:
        raise ValueError("CCC expects aligned (studies, anatomical_groups) arrays")
    terms = []
    for group in range(prediction.shape[1]):
        p, y = prediction[:, group], target[:, group]
        valid = torch.isfinite(p) & torch.isfinite(y)
        if int(valid.sum()) < 2:
            continue
        p, y = p[valid], y[valid]
        pc, yc = p-p.mean(), y-y.mean()
        yvar = yc.square().mean()
        if float(yvar.detach()) <= eps:
            continue
        covariance = (pc*yc).mean()
        denominator = pc.square().mean()+yvar+(p.mean()-y.mean()).square()
        terms.append(1-(2*covariance)/(denominator+eps))
    if not terms:
        return torch.nan_to_num(prediction, nan=0., posinf=0., neginf=0.).sum()*0, 0
    return torch.stack(terms).mean(), len(terms)


def study_supervision_loss(severity: Tensor, binary_logits: Tensor, target: Tensor,
                           threshold: Tensor, mode="mae_ccc", ccc_weight=.1,
                           binary_weight=.3, weight: Tensor | None = None,
                           onesided: Tensor | None = None, onesided_max: float = .30,
                           regression_mask: Tensor | None = None):
    """Normalized severity MAE + optional CCC + per-group threshold BCE.

    Bounded accumulation in integrated_study.py holds at most the configured
    number of study graphs before calling this function. Legacy MSE is an
    explicit comparator, not silently mixed with MAE.

    v17 (default off; weight=None and onesided=None run the pre-v17 code path unchanged, bit for bit):
      weight   (studies, groups), >= 0: per-(study, segment) label weight (arm A repaired labels at .5). The regression
               term (mse / mae, and the MAE part of mae_ccc) and the threshold BCE become weighted means over the valid
               entries, sum(w * e) / sum(w). The CCC term stays unweighted and excludes nothing.
      onesided (studies, groups), bool: arm C '0 by convention' treated segments on post-PCI rows: the target is
               "<= onesided_max" (normalized, .30 = 30%), so the regression residual is relu(prediction - onesided_max)
               (no loss at or below 30%). The BCE target stays the usual binary label (0 <= 30% is below every threshold).
    v19 (default None = unchanged): regression_mask (studies, groups) bool: False removes the entry from the % regression
               term (mse / mae and the MAE / CCC parts of mae_ccc), e.g. labels >= 99% under severity_loss_below_99_only;
               the threshold BCE keeps every valid entry.
    """
    if weight is not None or onesided is not None or regression_mask is not None:
        return _study_supervision_loss_v17(severity, binary_logits, target, threshold, mode, ccc_weight, binary_weight,
                                           weight, onesided, onesided_max, regression_mask)
    if severity.ndim != 2 or severity.shape != target.shape or binary_logits.shape != target.shape:
        raise ValueError("Study losses expect matching (studies, groups) arrays")
    valid = torch.isfinite(severity) & torch.isfinite(binary_logits) & torch.isfinite(target)
    if not valid.any():
        raise ValueError("A study batch has no observed finite stenosis labels")
    s, y, logits = severity.float(), target.float(), binary_logits.float()
    mae = F.l1_loss(s[valid], y[valid])
    ccc, n_ccc = concordance_loss(s, y)
    if mode == "mae_ccc":
        regression = mae+ccc_weight*ccc
    elif mode == "mae":
        regression = mae
    elif mode == "mse":
        regression = F.mse_loss(s[valid], y[valid])
    else:
        raise ValueError(f"Unknown severity loss {mode}")
    binary_target = y >= threshold[None]
    bce = F.binary_cross_entropy_with_logits(logits[valid], binary_target[valid].float())
    loss = regression+binary_weight*bce
    return loss, {"mae": float(mae.detach()), "ccc_loss": float(ccc.detach()),
                  "ccc_valid_segments": n_ccc, "bce": float(bce.detach())}


def _study_supervision_loss_v17(severity, binary_logits, target, threshold, mode, ccc_weight, binary_weight,
                                weight, onesided, onesided_max, regression_mask=None):
    """Weighted / one-sided variant of study_supervision_loss (see its docstring). Same validity rule and parts."""
    if severity.ndim != 2 or severity.shape != target.shape or binary_logits.shape != target.shape:
        raise ValueError("Study losses expect matching (studies, groups) arrays")
    s, y, logits = severity.float(), target.float(), binary_logits.float()
    w = torch.ones_like(y) if weight is None else weight.to(y).float()
    one = torch.zeros_like(y, dtype=torch.bool) if onesided is None else onesided.to(y.device).bool()
    if w.shape != y.shape or one.shape != y.shape:
        raise ValueError("Label weight / one-sided masks must match (studies, groups)")
    valid = torch.isfinite(s) & torch.isfinite(logits) & torch.isfinite(y) & torch.isfinite(w) & (w > 0)
    if not valid.any():
        raise ValueError("A study batch has no observed finite stenosis labels")
    wv = w[valid]
    wmean = lambda e: (wv*e).sum()/wv.sum()
    if regression_mask is None:
        residual = torch.where(one, torch.relu(s-onesided_max), s-y)[valid]
        mae = wmean(residual.abs())
        ccc, n_ccc = concordance_loss(s, y)  # unweighted, nothing excluded (one-sided rows keep their 0 target here)
        rmean = wmean
    else:  # v19 B: the % regression only on entries the mask keeps (BCE below keeps every valid entry)
        keep = regression_mask.to(y.device).bool()
        if keep.shape != y.shape:
            raise ValueError("regression_mask must match (studies, groups)")
        rvalid = valid & keep
        wr = w[rvalid]
        residual = torch.where(one, torch.relu(s-onesided_max), s-y)[rvalid]
        rmean = (lambda e: (wr*e).sum()/wr.sum()) if bool(rvalid.any()) else (lambda e: s.sum()*0)
        mae = rmean(residual.abs())
        ccc, n_ccc = concordance_loss(s, torch.where(keep, y, torch.full_like(y, float("nan"))))
    if mode == "mae_ccc":
        regression = mae+ccc_weight*ccc
    elif mode == "mae":
        regression = mae
    elif mode == "mse":
        regression = rmean(residual.square())
    else:
        raise ValueError(f"Unknown severity loss {mode}")
    binary_target = y >= threshold[None]
    bce = wmean(F.binary_cross_entropy_with_logits(logits[valid], binary_target[valid].float(), reduction="none"))
    loss = regression+binary_weight*bce
    parts = {"mae": float(mae.detach()), "ccc_loss": float(ccc.detach()),
             "ccc_valid_segments": n_ccc, "bce": float(bce.detach()),
             "label_weight_mean": float(wv.mean().detach()), "onesided_entries": int((one & valid).sum()),
             "onesided_active": int((one & valid & (s > onesided_max)).sum())}
    if regression_mask is not None:
        parts["regression_excluded_entries"] = int((valid & ~keep).sum())
    return loss, parts


# ---------------------------------------------------------------------------------------------------------------------
# v18 (v18_prep/v18_prereg.md section 2; every term is off unless its trainer weight is > 0). Run side: 0 left, 1 right,
# -1 unknown. dominance: the StudyClips (dominance_target, dominance_weight) pair, 1 = left dominant; known when the weight
# is finite and > 0 (as cohorts/curated_v15 build / v17_prep/imaged_mask.py).

def _dominance_left(dominance: Tensor | None):
    """True = left dominant, False = right dominant, None = unknown."""
    if dominance is None or not bool(torch.isfinite(dominance).all()) or float(dominance[1]) <= 0:
        return None
    return float(dominance[0]) >= .5


def visibility_targets(groups, sides: Tensor, dominance: Tensor | None = None) -> Tensor:
    """(groups,) float: 1 if the bag has a run of the segment's side, 0 if not, NaN if not knowable (no run of that side
    but unknown-side runs exist). Side: RCA groups right; PDA / PL right unless the study is known left dominant (then
    left); every other group left (prereg 2A / 2C)."""
    from integrated_model import RIGHT_SIDE_GROUPS, EITHER_SIDE_GROUPS
    sides = sides.reshape(-1)
    present = {0: bool((sides == 0).any()), 1: bool((sides == 1).any())}
    unknown = bool((sides < 0).any())
    left_dom = _dominance_left(dominance) is True
    out = []
    for g in groups:
        side = 1 if g in RIGHT_SIDE_GROUPS or (g in EITHER_SIDE_GROUPS and not left_dom) else 0
        out.append(1. if present[side] else (float("nan") if unknown else 0.))
    return torch.tensor(out)


def visibility_loss(logits: Tensor, target: Tensor) -> Tensor:
    known = torch.isfinite(target)
    if not bool(known.any()):
        return logits.float().sum() * 0
    return F.binary_cross_entropy_with_logits(logits.float()[known], target.to(logits.device).float()[known])


def negative_mask_classes(side: int, dominance_left):
    """Mask classes of the OTHER side for a run of this side (prereg 2D). Left run: RCA classes, plus PDA / PL classes when
    the study is known right dominant. Right run: classes of every left group except lvp (unclear anatomy; never pushed).
    Left-dominant PDA / PL classes are never pushed down on right runs."""
    from integrated_model import SEGMENT_CLASSES, RIGHT_SIDE_GROUPS, EITHER_SIDE_GROUPS
    if side == 0:
        groups = RIGHT_SIDE_GROUPS + (EITHER_SIDE_GROUPS if dominance_left is False else ())
    elif side == 1:
        groups = tuple(g for g in SEGMENT_CLASSES if g not in RIGHT_SIDE_GROUPS + EITHER_SIDE_GROUPS + ("lvp",))
    else:
        return ()
    return tuple(sorted({c for g in groups for c in SEGMENT_CLASSES[g]}))


def negative_mask_loss(mask_logits: Tensor, sides: Tensor, dominance: Tensor | None = None,
                       frame_stride: int = 1) -> tuple[Tensor, dict]:
    """Per known-side run, BCE of the summed other-side class probability toward 0 on every pixel:
    -log(1 - P_other) = logsumexp(all classes) - logsumexp(classes that are not other-side). Mean over pixels of each run,
    then over runs with a known side. mask_logits: (V,T,C,H,W) decoder logits (softmax over C)."""
    from torch.utils.checkpoint import checkpoint
    dom = _dominance_left(dominance)
    terms, other_prob = [], []

    def run_term(x, keep):  # recomputed in the backward pass (memory only: no float32 copies of the logits are kept)
        z = x.float()
        return (torch.logsumexp(z, 1) - torch.logsumexp(z[:, keep], 1)).mean()

    for v, side in enumerate(sides.reshape(-1).tolist()):
        cls = negative_mask_classes(int(side), dom)
        if not cls:
            continue
        x = mask_logits[v, ::frame_stride]
        keep = torch.ones(x.shape[1], dtype=torch.bool, device=x.device)
        keep[list(cls)] = False
        terms.append(checkpoint(run_term, x, keep, use_reentrant=False) if x.requires_grad else run_term(x, keep))
        with torch.no_grad():
            z = x.float()
            other_prob.append(float((1 - torch.exp(torch.logsumexp(z[:, keep], 1) - torch.logsumexp(z, 1))).mean()))
            del z
    if not terms:
        return mask_logits.float().sum() * 0, {"negative_mask_runs": 0}
    return torch.stack(terms).mean(), {"negative_mask_runs": len(terms), "other_side_mask_prob": sum(other_prob) / len(other_prob)}


def v18_auxiliary(result: dict, sample: dict, groups, *, visibility_weight=0., negative_mask_weight=0.,
                  negative_mask_frame_stride=1) -> tuple[Tensor | None, dict]:
    """Sum of the enabled v18 auxiliary terms for one study (None when all weights are 0). The CTO collateral path has no
    term of its own: it is trained by the existing CTO lesion / lesion-distillation BCE on the CTO logit."""
    total, parts = None, {}
    if visibility_weight and "visibility_logits" in result:
        target = visibility_targets(groups, sample["side"], sample.get("dominance"))
        term = visibility_loss(result["visibility_logits"], target)
        total = visibility_weight * term
        parts["visibility_loss"] = float(term.detach())
    if negative_mask_weight:
        term, extra = negative_mask_loss(result["mask_logits"], sample["side"], sample.get("dominance"), negative_mask_frame_stride)
        total = negative_mask_weight * term if total is None else total + negative_mask_weight * term
        parts.update(negative_mask_loss=float(term.detach()), **extra)
    return total, parts



# ---------------------------------------------------------------------------------------------------------------------
# v19 (v19_prep/v19_prereg_DRAFT.md section 2; every term is off unless its trainer weight is > 0).
OCCLUDED_LABEL = .985  # "label >= 99%": labels are integer percent / 100; .985 avoids float equality at .99


def imaged_targets(groups, sides: Tensor | None, dominance: Tensor | None = None) -> Tensor:
    """(groups,) bool, goal.md accounting (v17_prep/imaged_mask.py): a segment is imaged if its side (RCA right; PDA / PL right
    unless the study is known left dominant; everything else left) has a run in the bag. Bags with both known sides or with
    neither keep every segment (as imaged_mask.build); sides None (no side lookup) = every segment imaged."""
    from integrated_model import RIGHT_SIDE_GROUPS, EITHER_SIDE_GROUPS
    if sides is None:
        return torch.ones(len(groups), dtype=torch.bool)
    sides = sides.reshape(-1)
    hl, hr = bool((sides == 0).any()), bool((sides == 1).any())
    if hl == hr:
        return torch.ones(len(groups), dtype=torch.bool)
    left_dom = _dominance_left(dominance) is True
    return torch.tensor([hr if (g in RIGHT_SIDE_GROUPS or (g in EITHER_SIDE_GROUPS and not left_dom)) else hl for g in groups])


def occluded_targets(severity: Tensor) -> Tensor:
    """(groups,) float: 1 if label >= 99%, 0 if < 99%, NaN if unlabelled (severity normalized 0-1)."""
    y = severity.float()
    return torch.where(torch.isfinite(y), (y >= OCCLUDED_LABEL).float(), torch.full_like(y, float("nan")))


def focal_bce(logits: Tensor, target: Tensor, gamma: float = 2.0) -> Tensor:
    """Mean focal binary cross-entropy (Lin et al. 2017, no alpha); gamma 0 = plain BCE."""
    z, y = logits.float(), target.float()
    bce = F.binary_cross_entropy_with_logits(z, y, reduction="none")
    if gamma:
        p = z.sigmoid()
        pt = p * y + (1 - p) * (1 - y)
        bce = (1 - pt).pow(gamma) * bce
    return bce.mean()


def v19_auxiliary(result: dict, sample: dict, groups, *, occluded_weight=0., occluded_focal_gamma=2.,
                  cto_le_occluded_weight=0.) -> tuple[Tensor | None, dict]:
    """Sum of the enabled v19 terms for one study (None when both weights are 0).
    A: focal BCE of the occluded logit vs label >= 99% on labelled, imaged segments (imaged_targets).
    C: hinge mean_g relu(P(CTO)_g - P(occluded)_g) over every segment, P(occluded) detached (the CTO head is tied to the
       occluded output, not the reverse; the occluded head is trained only by A)."""
    total, parts = None, {}
    if occluded_weight and "occluded_logits" in result:
        logits = result["occluded_logits"]
        target = occluded_targets(sample["severity"]).to(logits.device)
        imaged = imaged_targets(groups, sample.get("side"), sample.get("dominance")).to(logits.device)
        keep = torch.isfinite(target) & imaged
        if bool(keep.any()):
            term = focal_bce(logits[keep], target[keep], occluded_focal_gamma)
            total = occluded_weight * term
            parts.update(occluded_loss=float(term.detach()), occluded_segments=int(keep.sum()),
                         occluded_positives=int(target[keep].sum()))
    if cto_le_occluded_weight and "occluded_probability" in result and "lesion_cto_probability" in result:
        term = torch.relu(result["lesion_cto_probability"].float() - result["occluded_probability"].detach().float()).mean()
        total = cto_le_occluded_weight * term if total is None else total + cto_le_occluded_weight * term
        parts["cto_le_occluded_hinge"] = float(term.detach())
    return total, parts
