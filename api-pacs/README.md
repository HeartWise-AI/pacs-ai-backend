# PACS API

PACS identity and user access management API using Firebase with multi-tenancy support.

## Local Development

Setup the .env file first

- cp .env.example .env

Setup firebase admin configs

- You need to paste firebase admin configs `pacs-ai-firebase-admin.json` in `configs/firebase` and change `FIREBASE_CONFIG_FILE_PATH` in .env to proceed.

To bootstrap everything, run:

- `make` <---- build and run the Go executable locally

- `make up-local` <---- spins a container via Docker

The command above will install, build, and run the binary

For manual install:

- make install

For lint:

- make lint

Just ensure you installed golangci-lint.

To test:

- make test

For manual build:

- make build
- NOTE: the output for this is in bin/

## Docker Build

To run the container in local environment:

- make up-local
