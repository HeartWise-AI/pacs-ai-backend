import torch
import torch.nn as nn

class ClassificationHead(nn.Module):
    def __init__(self, dim_in, num_classes=1):
        super().__init__()
        self.fc1 = nn.Conv3d(dim_in, 2048, bias=True, kernel_size=1, stride=1)
        self.regress = nn.Linear(2048, num_classes)
        
        # Initialize weights properly for classification
        nn.init.kaiming_normal_(self.fc1.weight, mode='fan_out', nonlinearity='relu')
        nn.init.constant_(self.fc1.bias, 0)
        nn.init.xavier_uniform_(self.regress.weight)
        nn.init.constant_(self.regress.bias, 0)

    def forward(self, x):
        x = self.fc1(x)
        x = F.relu(x)  # Add activation
        x = x.mean([2, 3, 4])
        x = self.regress(x)
        return x

class MultiOutputHead(nn.Module):
    def __init__(
        self, 
        dim_in, 
        head_structure: dict[str, int], 
    ):
        super().__init__()
        self.heads = nn.ModuleDict(
            {head_name: nn.ModuleList([ClassificationHead(dim_in, num_classes)]) for head_name, num_classes in head_structure}
        )
    
class VasoVision(nn.Module):
    def __init__(
        self, 
        model_path: str,
        head_structure: dict[str, int]
    ):
        super(VasoVision, self).__init__()
        self.model = torch.hub.load(
            "facebookresearch/pytorchvideo", 
            "x3d_m", 
            pretrained=True, 
            source="local"
        )
        self.model.blocks[-1] = MultiOutputHead(
            dim_in=192, 
            head_structure=head_structure
        )
        state_dict = torch.load(model_path, map_location=torch.device('cpu'), weights_only=False)['model_state_dict']
        torch.nn.modules.utils.consume_prefix_in_state_dict_if_present(state_dict, 'module.')
        self.model.load_state_dict(state_dict)
        self.model.to("cuda" if torch.cuda.is_available() else "cpu")
        self.model.eval()
        
    def forward(self, x: torch.Tensor) -> torch.Tensor:
        return self.model(x)