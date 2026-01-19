Important: Models are stored on huggingface, please refer to [pacs-ai-examples](https://huggingface.co/heartwise/pacs-ai-examples).

To build the docker image and push it to the docker hub, run the following command:

```
docker build --build-arg HF_API_KEY=API_KEY_HERE -t cacoool/pacs-ai-deeprv:1.0.0 .
docker push cacoool/pacs-ai-deeprv:1.0.0
```

To run the docker image locally for testing, run the following command:
```
docker run -p 8000:8000 cacoool/pacs-ai-deeprv:1.0.0
```

If you want to add GPU support, run the following command:
```
docker run -p 8000:8000 --gpus all cacoool/pacs-ai-deeprv:1.0.0
```

To run the docker image locally for debugging with the pacs network, run the following command and then you can attach to the container and debug it:
```
docker run -it --network pacs-net --gpus all --entrypoint /bin/bash cacoool/pacs-ai-deeprv:1.0
```
