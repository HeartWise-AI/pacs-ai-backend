import torch
import torchvision
import torch.nn as nn

class EchoPrimeViewClassifier(nn.Module):
    def __init__(self, weights_file_path: str):
        super(EchoPrimeViewClassifier, self).__init__()
        self.model = torchvision.models.video.mvit_v2_s()
        self.model.head[-1] = torch.nn.Linear(self.model.head[-1].in_features, 512)
        self.model.head = torch.nn.Sequential(
            self.model.head,  
            torch.nn.Linear(512, 20)  
        )
        
        device = "cuda" if torch.cuda.is_available() else "cpu"
        state_dict = torch.load(weights_file_path, map_location=device)
        
        # Handle weights saved with DataParallel (have 'module.' prefix)
        if any(k.startswith('module.') for k in state_dict.keys()):
            state_dict = {k.replace('module.', ''): v for k, v in state_dict.items()}
        
        self.model.load_state_dict(state_dict)
        self.model.to(device)

        for param in self.model.parameters():
            param.requires_grad = False

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        return self.model(x)