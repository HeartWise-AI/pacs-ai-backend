import torch
import torch.nn as nn

class ClassificationHead(nn.Module):
    """Proper classification head for X3D models."""
    def __init__(self, dim_in, num_classes=1):
        super().__init__()
        # Smaller intermediate dimension for stability
        self.fc1 = nn.Conv3d(dim_in, 512, kernel_size=1, stride=1, bias=False)
        self.bn1 = nn.BatchNorm3d(512)
        self.relu = nn.ReLU(inplace=True)
        self.dropout = nn.Dropout3d(0.5)
        self.fc2 = nn.Conv3d(512, num_classes, kernel_size=1, stride=1, bias=True)
        
        # Better initialization for classification
        nn.init.kaiming_normal_(self.fc1.weight, mode='fan_out', nonlinearity='relu')
        nn.init.normal_(self.fc2.weight, mean=0, std=0.01)
        nn.init.constant_(self.fc2.bias, 0)
    
    def forward(self, x):
        # Conv3d expects [B, C, T, H, W]
        x = self.fc1(x)
        x = self.bn1(x)
        x = self.relu(x)
        x = self.dropout(x)
        x = self.fc2(x)
        # Global average pooling
        x = x.mean([2, 3, 4])
        return x

class MultiOutputHead(nn.Module):
    def __init__(
        self, 
        dim_in, 
        head_structure: dict[str, int], 
        head_task: dict[str, str] = None,
    ):
        super().__init__()
        self.heads = nn.ModuleDict(
            {head_name: ClassificationHead(dim_in, num_classes) for head_name, num_classes in head_structure.items()}
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
            dim_in=2048, 
            head_structure=head_structure
        )
        self.model.load_state_dict(torch.load(model_path, map_location=torch.device('cpu')))
        self.model.to("cuda" if torch.cuda.is_available() else "cpu")
        self.model.eval()
        
    def forward(self, x: torch.Tensor) -> torch.Tensor:
        return self.model(x)