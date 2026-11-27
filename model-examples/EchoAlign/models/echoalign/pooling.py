import numpy as np
import torch
import torch.nn as nn
import torch.nn.functional as F

STRUCTURES = [
    "left_ventricle", "right_ventricle", "left_atrium", "right_atrium",
    "aortic_valve", "mitral_valve", "tricuspid_valve", "pulmonic_valve",
    "pericardium", "aortic_root", "aortic_arch", "pulmonary_artery",
]
SLOTS = ["global"] + STRUCTURES


class StableStudyPooling(nn.Module):
    def __init__(self, dim_in=512, dim_out=512, num_structures=12, heads=4, layers=2,
                 dropout=0.0, num_views=10, num_dicom_types=4):
        super().__init__()
        self.K = int(num_structures)
        self.heads = int(heads)
        self.slots = 1 + self.K
        self.view_emb = nn.Embedding(int(num_views), dim_in) if num_views else None
        self.dtype_emb = nn.Embedding(int(num_dicom_types), dim_in)
        self.slot_emb = nn.Embedding(self.slots, dim_in)
        enc_layer = nn.TransformerEncoderLayer(
            d_model=dim_in, nhead=self.heads, dim_feedforward=dim_in * 2,
            dropout=dropout, batch_first=True, norm_first=True,
        )
        self.encoder = nn.TransformerEncoder(enc_layer, num_layers=int(layers))
        self.queries = nn.Parameter(torch.zeros(self.slots, dim_in))
        self.pool_attn = nn.MultiheadAttention(dim_in, self.heads, batch_first=True)
        self.norm = nn.LayerNorm(dim_in)
        self.proj = nn.Linear(dim_in, dim_out, bias=False)

    def _forward_impl(self, img_embeds, concept_tokens, group_index, view_ids,
                      dicom_type_ids):
        device = img_embeds.device
        M, D = img_embeds.shape
        slots = self.slots
        S = int(group_index.max().item()) + 1

        counts = torch.bincount(group_index, minlength=S)
        Vmax = int(counts.max().item())
        order = torch.argsort(group_index, stable=True)
        starts = torch.cumsum(counts, 0) - counts
        pos_in_group = torch.arange(M, device=device) - starts[group_index[order]]
        cell = torch.empty(M, dtype=torch.long, device=device)
        cell[order] = group_index[order] * Vmax + pos_in_group

        clip_valid = torch.zeros(S * Vmax, dtype=torch.bool, device=device)
        clip_valid[cell] = True

        tok = torch.zeros(S * Vmax, slots, D, device=device, dtype=img_embeds.dtype)
        cond = self.dtype_emb(dicom_type_ids)
        if self.view_emb is not None:
            cond = cond + self.view_emb(view_ids)
        tok[cell, 0] = img_embeds + cond
        tok[cell, 1:] = concept_tokens + cond.unsqueeze(1)
        tok = tok + self.slot_emb.weight.unsqueeze(0).to(tok.dtype)
        bank = tok.reshape(S, Vmax * slots, D)
        tok_valid = clip_valid.unsqueeze(1).expand(-1, slots).reshape(S, Vmax * slots)

        bank = self.encoder(bank, src_key_padding_mask=~tok_valid)
        bank = torch.nan_to_num(bank, nan=0.0)

        slot_id = torch.arange(slots, device=device).repeat(Vmax)
        q_id = torch.arange(slots, device=device)
        same_slot = slot_id.unsqueeze(0) == q_id.unsqueeze(1)

        allow = same_slot.unsqueeze(0) & tok_valid.unsqueeze(1)
        attn_mask = (~allow).repeat_interleave(self.heads, dim=0)
        q = self.queries.unsqueeze(0).expand(S, -1, -1).to(bank.dtype)
        pooled, _ = self.pool_attn(q, bank, bank, attn_mask=attn_mask, need_weights=False)
        out = F.normalize(self.proj(self.norm(pooled)), dim=-1)
        out = torch.nan_to_num(out, nan=0.0)
        return out[:, 0], out[:, 1:]

    def forward(self, *args, **kwargs):
        # Both flags are needed (the fused path bypasses SDPA); the MHA flag is process-wide: one pooler per worker.
        from torch.nn.attention import SDPBackend, sdpa_kernel
        previous = torch.backends.mha.get_fastpath_enabled()
        try:
            torch.backends.mha.set_fastpath_enabled(False)
            with sdpa_kernel([SDPBackend.MATH]):
                return self._forward_impl(*args, **kwargs)
        finally:
            torch.backends.mha.set_fastpath_enabled(previous)


def load_study_pooling(state_dict, device="cpu"):
    if any(k.startswith("study_pool.") for k in state_dict):
        sd = {k[len("study_pool."):]: v for k, v in state_dict.items() if k.startswith("study_pool.")}
    else:
        sd = dict(state_dict)
    model = StableStudyPooling(dropout=0.0, num_views=sd["view_emb.weight"].shape[0] if "view_emb.weight" in sd else 0,
                               num_dicom_types=sd["dtype_emb.weight"].shape[0])
    model.load_state_dict(sd, strict=True)
    return model.to(device).eval()


@torch.inference_mode()
def pool_studies(model, global_vecs, concept_vecs, group_index, view_ids=None,
                 dicom_type_ids=None, clip_budget=4096):
    device = next(model.parameters()).device
    group_index = np.asarray(group_index, np.int64)
    M = len(group_index)
    if M == 0:
        raise ValueError("no clips to pool")
    view_ids = np.zeros(M, np.int64) if view_ids is None else np.asarray(view_ids, np.int64)
    dicom_type_ids = np.zeros(M, np.int64) if dicom_type_ids is None else np.asarray(dicom_type_ids, np.int64)
    S = int(group_index.max()) + 1
    counts = np.bincount(group_index, minlength=S)
    if (counts == 0).any():
        raise ValueError("every study index in [0, S) needs at least one clip")
    order = np.argsort(group_index, kind="stable")
    bounds = np.r_[0, np.cumsum(counts)]
    out = np.empty((S, 13, 512), np.float32)
    rank = np.argsort(counts, kind="stable")[::-1]
    at = 0
    while at < S:
        take = max(1, clip_budget // int(counts[rank[at]]))
        batch = rank[at:at + take]
        at += take
        rows = np.concatenate([order[bounds[j]:bounds[j + 1]] for j in batch])
        gid = np.repeat(np.arange(len(batch)), counts[batch]).astype(np.int64)
        args = [np.asarray(global_vecs[rows], np.float32), np.asarray(concept_vecs[rows], np.float32),
                gid, view_ids[rows], dicom_type_ids[rows]]
        g, s = model(*[torch.from_numpy(a).to(device) for a in args])
        out[batch, 0] = g.float().cpu().numpy()
        out[batch, 1:] = s.float().cpu().numpy()
    return out
