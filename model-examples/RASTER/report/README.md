To build the docker image and push it to the docker hub, run the following command:

```sh
docker build -t guillaumeth/raster_pacs-ai:report_0.2.0 .
docker push guillaumeth/raster_pacs-ai:report_0.2.0
```

To run the docker image locally for testing, run the following command:

```sh
docker run -p 8000:8000 --gpus all --shm-size 2g guillaumeth/raster_pacs-ai:report_0.2.0
```

To run the docker image locally for debugging with the pacs network, run the following command and then you can attach to the container and debug it:

```sh
docker run -it --network pacs-net --gpus all --shm-size 2g --entrypoint /bin/bash guillaumeth/raster_pacs-ai:report_0.2.0
```
