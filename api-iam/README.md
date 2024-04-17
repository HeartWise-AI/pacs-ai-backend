# PACS IAM API
PACS identity and user access management API using Firebase.

## Local Development

Setup the .env file first
- cp .env.example .env

To bootstrap everything, run:
- make

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

To build, run:
- make run

To run the container:
- make up


- configs/firebase service account private key