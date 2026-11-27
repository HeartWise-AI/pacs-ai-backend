import numpy as np
import torch
import torch.nn.functional as F

from .encoder import encode_clips, load_clip_encoder
from .pooling import SLOTS, STRUCTURES, load_study_pooling, pool_studies


class EchoAlign:
    def __init__(self, weights="weights/EchoAlign.pt", device=None):
        self.device = torch.device(device or ("cuda" if torch.cuda.is_available() else "cpu"))
        blob = torch.load(weights, map_location="cpu", weights_only=True)
        sd = blob["state_dict"]
        self.encoder = load_clip_encoder(sd, self.device)
        self.pooler = load_study_pooling(sd, self.device)
        self._load_text(blob)
        self.pca = {k: {n: t.numpy() for n, t in blob[f"pca_{k}"].items()}
                    for k in ("study", "clip") if f"pca_{k}" in blob}
        self.structures = list(STRUCTURES)
        self.slots = list(SLOTS)

    def _load_text(self, blob):
        from tokenizers import BertWordPieceTokenizer
        from transformers import BertConfig, BertModel

        vocab = {token: i for i, token in enumerate(blob["vocab"].split("\n"))}
        self.max_text_tokens = int(blob["max_text_tokens"])
        self.tokenizer = BertWordPieceTokenizer(vocab=vocab, lowercase=True)
        self.tokenizer.enable_truncation(self.max_text_tokens)
        self.tokenizer.enable_padding(length=self.max_text_tokens, pad_id=vocab["[PAD]"])
        sd = blob["state_dict"]
        self.text_encoder = BertModel(BertConfig(**blob["text_config"], attn_implementation="eager"))
        self.text_encoder.load_state_dict(
            {k[len("text_enc."):]: v for k, v in sd.items() if k.startswith("text_enc.")}, strict=True)
        self.text_proj = torch.nn.Linear(self.text_encoder.config.hidden_size, 512, bias=False)
        self.text_proj.load_state_dict({"weight": sd["text_proj.weight"]})
        self.text_encoder.to(self.device).eval()
        self.text_proj.to(self.device).eval()

    @torch.inference_mode()
    def encode_clips(self, clips, return_tokens=False):
        clips = torch.as_tensor(clips)
        if clips.dim() == 4:
            clips = clips.unsqueeze(0)
        return encode_clips(self.encoder, clips, return_tokens=return_tokens)

    @staticmethod
    def patch_token_map(tokens, frame, n_frames=16):
        t = torch.as_tensor(tokens)
        t = t.reshape(t.shape[0], 196, n_frames, t.shape[-1])[:, :, int(frame)]
        return t.transpose(1, 2).reshape(t.shape[0], t.shape[-1], 14, 14)

    @torch.inference_mode()
    def encode_study(self, clips):
        x = [torch.as_tensor(v) for v in clips]
        g, c = (np.concatenate(p) for p in zip(*[self.encode_clips(torch.stack(x[i:i + 32])) for i in range(0, len(x), 32)]))
        return pool_studies(self.pooler, g, c, group_index=[0] * len(g))[0]

    @torch.inference_mode()
    def encode_text(self, texts):
        enc = self.tokenizer.encode_batch(list(texts))
        ids = torch.tensor([e.ids for e in enc], device=self.device)
        mask = torch.tensor([e.attention_mask for e in enc], device=self.device)
        types = torch.tensor([e.type_ids for e in enc], device=self.device)
        cls = self.text_encoder(input_ids=ids, attention_mask=mask, token_type_ids=types).last_hidden_state[:, 0]
        return F.normalize(self.text_proj(cls), dim=-1).float().cpu().numpy()

    def pca_embedding(self, vectors, level="study"):
        x = np.asarray(vectors, np.float64)
        single = x.ndim == 2
        x = x[None] if single else x
        if level not in self.pca:
            raise KeyError(f"the weights file has no pca_{level} bases")
        p = self.pca[level]
        out = np.einsum("nsd,skd->nsk", x - p["mean"][None], p["components"]).reshape(len(x), -1)
        return out[0] if single else out

    def zero_shot(self, study_vectors, positive, negative, structure=None):
        def unit(v):
            return v / max(float((v ** 2).sum()) ** 0.5, 1e-12)

        if structure == "mean":
            x = unit(sum(unit(v) for v in study_vectors) / len(study_vectors))
        else:
            x = unit(study_vectors[0 if structure in (None, "global") else self.structures.index(structure) + 1])
        pos, neg = self.encode_text(positive), self.encode_text(negative)
        return float((pos @ x).mean() - (neg @ x).mean())
