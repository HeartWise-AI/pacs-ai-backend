Important: Models are stored on huggingface, please refer to [pacs-ai-examples](https://huggingface.co/heartwise/pacs-ai-examples).

To build the docker image and push it to the docker hub, run the following command:

```
docker build --secret id=hf_token,src=./hf_token.txt -t heartwisehub/pacs-ai-deeprv:1.0 .
docker push heartwisehub/pacs-ai-deeprv:1.0
```

To run the docker image locally for testing, run the following command:
```
docker run -p 8000:8000 heartwisehub/pacs-ai-deeprv:1.0
```

If you want to add GPU support, run the following command:
```
docker run -p 8000:8000 --gpus all heartwisehub/pacs-ai-deeprv:1.0
```

To run the docker image locally for debugging with the pacs network, run the following command and then you can attach to the container and debug it:
```
docker run -it --network pacs-net --gpus all --entrypoint /bin/bash heartwisehub/pacs-ai-deeprv:1.0
```

## Changelog

- 1.0.2: security patch: PyTorch 2.13.0 (CUDA 12.6 build), torchvision 0.28.0, closing the open Dependabot PyTorch advisories (incl. critical GHSA-53q9-r3pm-6pq6 and high GHSA-63cw-57p8-fm3p). Unpinned Python dependencies are now pinned to the versions of the previous image. Same weights, preprocessing, thresholds and report; predictions checked against the previous image on synthetic and real XA studies.
