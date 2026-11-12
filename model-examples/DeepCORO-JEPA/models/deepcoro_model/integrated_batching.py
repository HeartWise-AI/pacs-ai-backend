"""Bounded study graphs with per-study-normalized optimizer accumulation."""
from __future__ import annotations

import torch

from integrated_losses import study_supervision_loss


class ViewBoundedStudyBatch:
    """Keep at most max_studies/max_views graphs; CCC stays within each group.

    ``before_study`` releases pending graphs before allocating the next model
    forward. High-view studies become singletons and legitimately skip CCC.
    Each study has equal weight within the fixed optimizer-study budget,
    independent of how the memory budget partitions its graph batches.
    """
    def __init__(self, optimizer, parameters, threshold, *, max_views=12,
                 max_studies=2, optimizer_studies=8, severity_loss="mae_ccc",
                 ccc_weight=.1, binary_weight=.3):
        if min(max_views, max_studies, optimizer_studies) < 1 or max_studies > optimizer_studies:
            raise ValueError("Invalid study/graph accumulation budget")
        self.optimizer, self.parameters, self.threshold = optimizer, list(parameters), threshold
        self.max_views, self.max_studies, self.optimizer_studies = max_views, max_studies, optimizer_studies
        self.severity_loss, self.ccc_weight, self.binary_weight = severity_loss, ccc_weight, binary_weight
        self.pending, self.accumulated, self.records = [], 0, []
        self.optimizer_updates = 0
        optimizer.zero_grad(set_to_none=True)

    def before_study(self, views):
        if views < 1 or views > self.max_views:
            raise ValueError("A study must fit the declared full-view graph budget without extra truncation")
        if self.pending and (sum(x["views"] for x in self.pending)+views > self.max_views):
            self.flush()

    def add(self, severity, binary_logits, target, auxiliary, views, uid=None):
        if sum(x["views"] for x in self.pending)+views > self.max_views:
            raise RuntimeError("Call before_study before allocating a study that exceeds the pending view budget")
        self.pending.append(dict(severity=severity, binary_logits=binary_logits, target=target,
                                 auxiliary=auxiliary, views=views, uid=uid))
        if (len(self.pending) >= self.max_studies or sum(x["views"] for x in self.pending) >= self.max_views
                or self.accumulated+len(self.pending) >= self.optimizer_studies):
            self.flush()

    def _step(self):
        if not self.accumulated:
            return
        if self.accumulated < self.optimizer_studies:
            for parameter in self.parameters:
                if parameter.grad is not None:
                    parameter.grad.mul_(self.optimizer_studies/self.accumulated)
        norm = torch.nn.utils.clip_grad_norm_(self.parameters, 1.)
        self.optimizer.step()
        self.optimizer.zero_grad(set_to_none=True)
        self.optimizer_updates += 1
        self.accumulated = 0
        if self.records:
            self.records[-1]["optimizer_step_applied"] = True
            self.records[-1]["gradient_norm_before_clip"] = float(norm)

    def flush(self):
        if not self.pending:
            return
        pending, self.pending = self.pending, []
        primary, parts = study_supervision_loss(
            torch.stack([x["severity"] for x in pending]),
            torch.stack([x["binary_logits"] for x in pending]),
            torch.stack([x["target"] for x in pending]), self.threshold,
            mode=self.severity_loss, ccc_weight=self.ccc_weight, binary_weight=self.binary_weight)
        auxiliary = torch.stack([x["auxiliary"] for x in pending]).mean()
        loss = primary+auxiliary
        (loss*len(pending)/self.optimizer_studies).backward()
        self.accumulated += len(pending)
        self.records.append({**parts, "loss": float(loss.detach()), "auxiliary": float(auxiliary.detach()),
                             "studies": len(pending), "full_views": sum(x["views"] for x in pending),
                             "study_uids": [x["uid"] for x in pending], "optimizer_step_applied": False})
        if self.accumulated >= self.optimizer_studies:
            self._step()

    def finish(self):
        self.flush()
        self._step()
        return self.records


def mask_retention_loss(model, sample, device, class_weights, amp, *, cldice_weight=.1):
    """Current-trunk mask supervision; cached tokens are used only if frozen."""
    from integrated_model import segmentation_loss
    image = sample["frames"].to(device)[None]
    updating_encoder = any(p.requires_grad for p in model.enc.parameters())
    if hasattr(model, "encode_features"):
        with amp(device):
            pyramid = model.encode_features(image.expand(-1, 16, -1, -1)).temporal_mean(8)
            tokens = pyramid.stacked()
    elif updating_encoder:
        with amp(device):
            tokens = model.encode(image.expand(-1, 16, -1, -1)).reshape(1, 8, -1, model.enc.embed_dim).mean(1)
    else:
        tokens = sample["tokens"].to(device)[None]
    with amp(device):
        logits = model.mask_decoder(tokens, image, (1, image.shape[-2]//16, image.shape[-1]//16))
    teacher = sample["teacher_prob"].to(device)[None] if sample["teacher_prob"].numel() else None
    return segmentation_loss(logits, sample["target"].to(device)[None], class_weights, teacher,
                             sample["agreement"].to(device)[None] if teacher is not None else None,
                             hard_weight=sample["agreement"].to(device)[None], cldice_weight=cldice_weight)
