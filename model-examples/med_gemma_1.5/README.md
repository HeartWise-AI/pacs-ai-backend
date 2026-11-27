> **Version 1.5.2 (torch 2.13.0+cu126): re-validate before re-enabling.**
> 1.5.2 moves torch from 2.6.0 (CUDA 12.4) to 2.13.0 (CUDA 12.6) to close Dependabot security advisories (source-only release; the model is stopped on MHI and not deployed).
> The generated text changed with this torch version. Parity versus 1.5.1 on 12 chest X-ray inputs (2 studies x 6 variants, greedy decoding):
> only **1 of 12** generated reports was identical; teacher-forced over 2,728 tokens the top token changed on 1.10% (max probability difference 0.115, mean 0.0063);
> one mirrored input gained a cardiomegaly finding. torch 2.10.0+cu126 gives the same logits as 2.13.0, so the drift comes from the CUDA 12.4 -> 12.6 libraries.
> The 1.5.1 image was deterministic (12/12 identical on repeat). **Re-validate the outputs clinically before the model is re-enabled.**

Important: Models are stored on huggingface, please refer to [pacs-ai-examples](https://huggingface.co/heartwise/pacs-ai-examples).

To build the docker image and push it to the docker hub, run the following command:

```
docker build --secret id=hf_token,src=./hf_token.txt -t heartwisehub/medgemma_1.5:1.0 .
docker push heartwisehub/medgemma_1.5:1.0
```

To run the docker image locally for testing, run the following command:
```
docker run -p 8000:8000 heartwisehub/medgemma_1.5:1.0
```

If you want to add GPU support, run the following command:
```
docker run -p 8000:8000 --gpus all heartwisehub/medgemma_1.5:1.0
```

To run the docker image locally for debugging with the pacs network, run the following command and then you can attach to the container and debug it:
```
docker run -it --network pacs-net --gpus all --entrypoint /bin/bash heartwisehub/medgemma_1.5:1.0
```