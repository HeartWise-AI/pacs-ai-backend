import torch

from PIL import Image
from transformers import AutoProcessor, AutoModelForImageTextToText


class MedGemma:
    def __init__(self, model_path: str):
        self.model = AutoModelForImageTextToText.from_pretrained(
            model_path,
            torch_dtype=torch.bfloat16,
            device_map="auto",
            local_files_only=True
        )
        self.processor = AutoProcessor.from_pretrained(model_path, local_files_only=True)

    def generate(
        self,
        prompt: str = "Describe this image",
        image: Image.Image | None = None,
        max_new_tokens: int = 2000
    ) -> str:
        if image is not None:
            content = [
                {"type": "image", "image": image},
                {"type": "text", "text": prompt}
            ]
        else:
            content = [{"type": "text", "text": prompt}]

        messages = [{"role": "user", "content": content}]

        inputs = self.processor.apply_chat_template(
            messages,
            add_generation_prompt=True,
            tokenize=True,
            return_dict=True,
            return_tensors="pt"
        ).to(self.model.device, dtype=torch.bfloat16)

        input_len = inputs["input_ids"].shape[-1]

        with torch.inference_mode():
            generation = self.model.generate(**inputs, max_new_tokens=max_new_tokens, do_sample=False)
            generation = generation[0][input_len:]

        return self.processor.decode(generation, skip_special_tokens=True)