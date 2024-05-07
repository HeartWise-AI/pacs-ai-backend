# PACS AI Backend

Repository for PACS AI backend and configurations setup.

## Local Development

> Important! <br><br>
> docker compose version should be `>= v2.24.7` <br>
> Before starting, run `docker compose version` and check the version.

1. Setup `api-pacs/.env`

Reach out to Robert Avram to get the latest env contents.

2. Setup firebase admin configs for `api-pacs`

You need to paste firebase admin configs `pacs-ai-firebase-admin.json` in `configs/firebase` and change `FIREBASE_CONFIG_FILE_PATH` in .env to proceed.

Reach out to Robert Avram to get the configs.

3. Setup `redis/.env`

4. To run everything locally, run `make up-local`

5. To run it asynchronously, run `make up`

You can also opt to launch selected services. Below are the supported commands:

- `make up-api-pacs` to run only the api-pacs container

- `make up-orthanc` to run only the orthanc containers

- `make up-redis` to run only the redis container

## Demo

- api-pacs (Go API) is accessible via http://localhost/docs

- Orthanc container is accessible via http://localhost/orthanc/ui/app/

- Orthanc OpenAPI Docs is accessible via http://localhost/docs/orthanc

Maintainers: Karl from Nuxify, Andrea from Nuxify