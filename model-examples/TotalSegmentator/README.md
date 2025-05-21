To build the docker image and push it to the docker hub, run the following command:

```sh
docker build -t cacoool/pacs-ai-segmentation:0.5.0 .
docker push cacoool/pacs-ai-segmentation:0.5.0
```

To run the docker image locally for testing, run the following command:

```sh
docker run -p 8000:8000 --gpus all --shm-size 2g cacoool/pacs-ai-segmentation:0.5.0
```

To run the docker image locally for debugging with the pacs network, run the following command and then you can attach to the container and debug it:

```sh
docker run -it --network pacs-net --gpus all --shm-size 2g --entrypoint /bin/bash cacoool/pacs-ai-segmentation:0.5.0
```
