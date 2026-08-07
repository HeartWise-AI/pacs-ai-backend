import torch
import torchvision
import torch.nn as nn


class EchoPrimeViewClassifier(nn.Module):
    def __init__(self, weights_file_path: str):
        super().__init__()
        self.model = torchvision.models.video.mvit_v2_s()
        self.model.head[-1] = torch.nn.Linear(self.model.head[-1].in_features, 512)
        self.model.head = torch.nn.Sequential(
            self.model.head,
            torch.nn.Linear(512, 20),
        )

        device = "cuda" if torch.cuda.is_available() else "cpu"
        state_dict = torch.load(weights_file_path, map_location=device, weights_only=True)

        if any(key.startswith("module.") for key in state_dict.keys()):
            state_dict = {
                key.replace("module.", ""): value for key, value in state_dict.items()
            }

        self.model.load_state_dict(state_dict)
        self.model.to(device)

        for param in self.model.parameters():
            param.requires_grad = False

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        return self.model(x)
