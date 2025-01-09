To build the docker image and push it to the docker hub, run the following command:

```
docker build -t cacoool/cath-ef:1.4 .
docker push cacoool/cath-ef:1.4
```

To run the docker image locally for testing, run the following command:
```
docker run -p 8000:8000 cacoool/cath-ef:1.4
```

If you want to add GPU support, run the following command:
```
docker run -p 8000:8000 --gpus all cacoool/cath-ef:1.4
```

To run the docker image locally for debugging with the pacs network, run the following command and then you can attach to the container and debug it:
```
docker run -it --network pacs-net --gpus all --entrypoint /bin/bash cacoool/cath-ef:1.4
```

