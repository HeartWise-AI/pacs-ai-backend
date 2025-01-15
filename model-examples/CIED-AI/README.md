To build the docker image and push it to the docker hub, run the following command:

```
docker build -t cacoool/cied-ai:1.0 .
docker push cacoool/cied-ai:1.0
```

To run the docker image locally for testing, run the following command:
```
docker run -p 8000:8000 cacoool/cied-ai:1.0
```

If you want to add GPU support, run the following command:
```
docker run -p 8000:8000 --gpus all cacoool/cied-ai:1.0
```

To run the docker image locally for debugging with the pacs network, run the following command and then you can attach to the container and debug it:
```
docker run -it --network pacs-net --gpus all --entrypoint /bin/bash cacoool/cied-ai:1.0
```

