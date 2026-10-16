from models.pan_echo import PanEcho


def init_model():
    return PanEcho(
        model_path='./models/panecho.pt',
        task_path='./models/tasks.pkl',
        pretrained=True,
        image_encoder_only=False,
        backbone_only=False,
    )
    
if __name__ == "__main__":
    init_model()
