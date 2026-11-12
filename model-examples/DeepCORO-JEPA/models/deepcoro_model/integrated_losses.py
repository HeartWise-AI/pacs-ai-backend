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
                           binary_weight=.3):
    """Normalized severity MAE + optional CCC + per-group threshold BCE.

    Bounded accumulation in integrated_study.py holds at most the configured
    number of study graphs before calling this function. Legacy MSE is an
    explicit comparator, not silently mixed with MAE.
    """
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
