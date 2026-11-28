import torch
import torch.nn as nn
import torch.nn.functional as F

IMAGENET_MEAN = (0.485, 0.456, 0.406)
IMAGENET_STD = (0.229, 0.224, 0.225)


class ConceptQueryPooling(nn.Module):
    def __init__(self, dim_in=768, dim_out=512, num_queries=12, heads=8):
        super().__init__()
        self.queries = nn.Parameter(torch.zeros(num_queries, dim_in))
        self.attn = nn.MultiheadAttention(dim_in, heads, batch_first=True)
        self.norm = nn.LayerNorm(dim_in)
        self.proj = nn.Linear(dim_in, dim_out, bias=False)

    def forward(self, tokens):
        q = self.queries.unsqueeze(0).expand(tokens.size(0), -1, -1)
        # need_weights=True keeps the same (non-fused) attention path as training.
        pooled, _ = self.attn(q, tokens, tokens, need_weights=True, average_attn_weights=True)
        return F.normalize(self.proj(self.norm(pooled)), dim=-1)


class ClipEncoder(nn.Module):
    def __init__(self):
        super().__init__()
        from transformers import TimesformerConfig, TimesformerModel
        self.backbone = TimesformerModel(TimesformerConfig(num_frames=8, image_size=224))
        self.image_proj = nn.Linear(768, 512, bias=False)
        self.concept_pool = ConceptQueryPooling()
        self.register_buffer("tgt_mean", torch.tensor(IMAGENET_MEAN).view(1, 1, 3, 1, 1), persistent=False)
        self.register_buffer("tgt_std", torch.tensor(IMAGENET_STD).view(1, 1, 3, 1, 1), persistent=False)

    def forward(self, x, return_tokens=False):
        # The loader emits (pixel/255 - 0.5)/0.5; the backbone was trained on ImageNet statistics.
        x = ((x * 0.5 + 0.5) - self.tgt_mean.to(x.dtype)) / self.tgt_std.to(x.dtype)
        # The stored config says 8 frames; without this, 16 input frames are attended as two halves.
        self.backbone.config.num_frames = int(x.shape[1])
        h = self.backbone(pixel_values=x).last_hidden_state
        global_vec = F.normalize(self.image_proj(h[:, 0]), dim=-1)
        concept = self.concept_pool(h[:, 1:])
        if return_tokens:
            return global_vec, concept, h[:, 1:]
        return global_vec, concept


def load_clip_encoder(state_dict, device="cpu"):
    sd = {}
    for k, v in state_dict.items():
        if k.startswith("vision.backbone."):
            sd["backbone." + k[len("vision.backbone."):]] = v
        elif k.startswith("image_proj.") or k.startswith("concept_pool."):
            sd[k] = v
    model = ClipEncoder()
    missing, unexpected = model.load_state_dict(sd, strict=False)
    # Hugging Face Transformers registers a non-persistent position_ids buffer in some versions; nothing else may differ.
    missing = [m for m in missing if not m.endswith("position_ids")]
    if missing or unexpected:
        raise RuntimeError(f"encoder weights do not match: missing={missing} unexpected={unexpected}")
    return model.to(device).eval()


@torch.inference_mode()
def encode_clips(model, pixel_values, autocast=True, return_tokens=False):
    device = next(model.parameters()).device
    pv = pixel_values.to(device, non_blocking=True)
    use_bf16 = autocast and device.type == "cuda"
    with torch.autocast(device_type="cuda", dtype=torch.bfloat16, enabled=use_bf16):
        out = model(pv, return_tokens=return_tokens)
    return tuple(o.float().cpu().numpy() for o in out)
