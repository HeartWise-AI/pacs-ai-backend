import os
import tempfile
from pathlib import Path

import torch


def check_miopen():
    """Disable MIOpen on AMD GPUs when it cannot create its lock files (some filesystems reject ':' in names)."""
    if torch.version.hip is None or not torch.backends.cudnn.enabled:
        return torch.backends.cudnn.enabled
    lock_dir = Path(tempfile.gettempdir()) / "miopen-lockfiles"
    probe = lock_dir / f"echoalign_probe_{os.getpid()}_:memory:.lock"
    try:
        lock_dir.mkdir(parents=True, exist_ok=True)
        probe.touch()
        probe.unlink()
        return True
    except OSError:
        torch.backends.cudnn.enabled = False
        print(f"MIOpen cannot create lock files in {lock_dir}; using PyTorch convolutions instead", flush=True)
        return False
