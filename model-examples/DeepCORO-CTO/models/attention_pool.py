import torch
import torch.nn as nn
import torch.nn.functional as F


class AttentionPool(nn.Module):
    """
    Attention-based pooling mechanism inspired by CLIP.

    Uses a learnable query vector to attend over all input tokens,
    producing a single aggregated representation.
    """

    def __init__(
        self,
        embed_dim: int,
        num_heads: int = 8,
        output_dim: int = None,
        dropout: float = 0.0,
    ):
        super().__init__()

        self.embed_dim = embed_dim
        self.num_heads = num_heads
        self.output_dim = output_dim or embed_dim

        # Learnable query vector for attention
        self.query = nn.Parameter(torch.randn(1, 1, embed_dim))
        nn.init.trunc_normal_(self.query, std=0.02)

        # Multi-head attention
        self.attn = nn.MultiheadAttention(
            embed_dim=embed_dim,
            num_heads=num_heads,
            dropout=dropout,
            batch_first=True
        )

        # Layer normalization
        self.norm = nn.LayerNorm(embed_dim)

        # Output projection if needed
        if self.output_dim != embed_dim:
            self.proj = nn.Linear(embed_dim, self.output_dim)
        else:
            self.proj = nn.Identity()

    def forward(self, x: torch.Tensor, mask: torch.Tensor = None) -> torch.Tensor:
        B, N, D = x.shape
        assert D == self.embed_dim, f"Input dim {D} != expected {self.embed_dim}"

        query = self.query.expand(B, -1, -1)

        key_padding_mask = None
        if mask is not None:
            key_padding_mask = mask

        attn_out, attn_weights = self.attn(
            query=query,
            key=x,
            value=x,
            key_padding_mask=key_padding_mask
        )

        attn_out = self.norm(attn_out)
        out = self.proj(attn_out).squeeze(1)

        return out


class AttentionPoolWithCLS(nn.Module):
    """
    Attention pooling with an optional CLS token.

    This variant prepends a learnable CLS token to the sequence
    before applying self-attention, then uses the CLS token output
    as the aggregated representation.
    """

    def __init__(
        self,
        embed_dim: int,
        num_heads: int = 8,
        num_layers: int = 1,
        output_dim: int = None,
        dropout: float = 0.0,
    ):
        super().__init__()

        self.embed_dim = embed_dim
        self.num_heads = num_heads
        self.num_layers = num_layers
        self.output_dim = output_dim or embed_dim

        # Learnable CLS token
        self.cls_token = nn.Parameter(torch.zeros(1, 1, embed_dim))
        nn.init.trunc_normal_(self.cls_token, std=0.02)

        # Transformer layers
        self.transformer = nn.TransformerEncoder(
            nn.TransformerEncoderLayer(
                d_model=embed_dim,
                nhead=num_heads,
                dropout=dropout,
                batch_first=True
            ),
            num_layers=num_layers
        )

        # Final layer norm
        self.norm = nn.LayerNorm(embed_dim)

        # Output projection if needed
        if self.output_dim != embed_dim:
            self.proj = nn.Linear(embed_dim, self.output_dim)
        else:
            self.proj = nn.Identity()

    def forward(self, x: torch.Tensor, mask: torch.Tensor = None) -> torch.Tensor:
        B, N, D = x.shape
        assert D == self.embed_dim, f"Input dim {D} != expected {self.embed_dim}"

        cls_tokens = self.cls_token.expand(B, -1, -1)
        x = torch.cat([cls_tokens, x], dim=1)

        if mask is not None:
            cls_mask = torch.zeros(B, 1, dtype=mask.dtype, device=mask.device)
            mask = torch.cat([cls_mask, mask], dim=1)

        x = self.transformer(x, src_key_padding_mask=mask)

        cls_output = x[:, 0]
        cls_output = self.norm(cls_output)
        out = self.proj(cls_output)

        return out
