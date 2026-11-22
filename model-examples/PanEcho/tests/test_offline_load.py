"""Model load must not need network access and must reject an incomplete checkpoint."""

import pickle
import sys
from pathlib import Path

import numpy as np
import pytest
import torch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

from models import models as architecture
from models.pan_echo import PanEcho, Task


@pytest.fixture()
def no_network(monkeypatch):
    def refuse(*args, **kwargs):
        raise AssertionError("model load attempted a download")

    monkeypatch.setattr(torch.hub, "load_state_dict_from_url", refuse)
    monkeypatch.setattr("torchvision.models._api.load_state_dict_from_url", refuse, raising=False)
    import socket

    monkeypatch.setattr(socket.socket, "connect", refuse)


@pytest.fixture(scope="module")
def synthetic_release(tmp_path_factory):
    """Random weights with the released checkpoint's layout, and a two-task list."""
    directory = tmp_path_factory.mktemp("panecho")
    tasks = {
        "EF": {"task_type": "regression", "class_names": np.array(["EF"]), "mean": 55.0},
        "LVSize": {"task_type": "multi-class_classification", "class_names": np.array(["a", "b", "c"]), "mean": np.nan},
    }
    with open(directory / "tasks.pkl", "wb") as output:
        pickle.dump(tasks, output)
    torch.manual_seed(0)
    encoder = architecture.FrameTransformer("convnext_tiny", 8, 4, 0.0, "mean", 16, pretrained_backbone=False)
    task_list = [Task(name, t["task_type"], t["class_names"], t["mean"]) for name, t in tasks.items()]
    reference = architecture.MultiTaskModel(encoder, encoder.encoder.n_features, task_list, 0.25, False)
    weights = {key: value.clone() for key, value in reference.state_dict().items()}
    torch.save({"weights": weights}, directory / "panecho.pt")
    return directory, weights


def test_checkpoint_load_needs_no_download_and_sets_every_backbone_tensor(no_network, synthetic_release):
    directory, weights = synthetic_release

    model = PanEcho(
        model_path=str(directory / "panecho.pt"), task_path=str(directory / "tasks.pkl"), activations=False
    ).model

    state = model.state_dict()
    backbone = [key for key in state if key.startswith("encoder.encoder.")]
    assert len(backbone) > 100
    assert set(state) == set(weights)
    assert all(torch.equal(state[key], weights[key]) for key in state if key != "encoder.time_encoder.pe")


def test_incomplete_checkpoint_is_rejected(no_network, synthetic_release, tmp_path):
    directory, weights = synthetic_release
    incomplete = dict(weights)
    removed = next(key for key in incomplete if key.startswith("encoder.encoder."))
    del incomplete[removed]
    torch.save({"weights": incomplete}, tmp_path / "panecho.pt")

    with pytest.raises(RuntimeError, match="does not match the architecture"):
        PanEcho(model_path=str(tmp_path / "panecho.pt"), task_path=str(directory / "tasks.pkl"))


def test_imagenet_initialisation_is_still_requested_without_the_checkpoint(monkeypatch):
    requested = {}

    def fake_convnext_tiny(weights=None):
        requested["weights"] = weights
        raise RuntimeError("stop after recording the request")

    monkeypatch.setattr(architecture.torchvision.models, "convnext_tiny", fake_convnext_tiny)
    with pytest.raises(RuntimeError, match="stop after recording"):
        architecture.ImageEncoder("convnext_tiny")
    assert requested["weights"] == "IMAGENET1K_V1"

    with pytest.raises(RuntimeError, match="stop after recording"):
        architecture.ImageEncoder("convnext_tiny", pretrained_backbone=False)
    assert requested["weights"] is None
