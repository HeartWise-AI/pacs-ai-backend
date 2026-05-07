Important: Models are stored on huggingface, please refer to [pacs-ai-examples](https://huggingface.co/heartwise/pacs-ai-examples).

To build the docker image and push it to the docker hub, run the following command:

```
docker build -t heartwisehub/pacs-ai-pan-echo:1.0 .
docker push heartwisehub/pacs-ai-pan-echo:1.0
```

To build with the optional ECHO-PRIME view-classifier weights included, enable the build arg and pass the Hugging Face token as a BuildKit secret:

```bash
DOCKER_BUILDKIT=1 docker build \
  --build-arg DOWNLOAD_VIEW_CLASSIFIER_WEIGHTS=true \
  --secret id=hf_token,src=/path/to/hf_token.txt \
  -t heartwisehub/pacs-ai-pan-echo:1.0 .
```

To run the docker image locally for testing, run the following command:
```
docker run -p 8000:8000 heartwisehub/pacs-ai-pan-echo:1.0
```

If you want to add GPU support, run the following command:
```
docker run -p 8000:8000 --gpus all heartwisehub/pacs-ai-pan-echo:1.0
```

To run the docker image locally for debugging with the pacs network, run the following command and then you can attach to the container and debug it:
```
docker run -it --network pacs-net --gpus all --entrypoint /bin/bash heartwisehub/pacs-ai-pan-echo:1.0
```

`PanEcho` can now optionally run the `ECHO-PRIME` view classifier internally before inference. The classifier will be active when `best_model_pretrained_echoprime_updated.pth` exists under [weights](/home/jdelfrate/pacs-ai-backend/model-examples/PanEcho/weights), either because it was downloaded at build time or placed there manually.
