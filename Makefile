default: up-local

.PHONY: up
up:
	docker compose -f docker-compose-dev.yml up -d --build

.PHONY: down
down:
	docker compose -f docker-compose-dev.yml down

.PHONY: up-local
up-local:
	docker compose -f docker-compose-dev.yml up --build

.PHONY: up-prod
up-prod:
	docker compose up -d --build

.PHONY: down-prod
down-prod:
	docker compose down

.PHONY: up-api-pacs
up-api-pacs:
	cd api-pacs
	docker compose -f docker-compose-dev.yml up --build

.PHONY: up-orthanc
up-orthanc:
	cd orthanc
	docker compose -f docker-compose-dev.yml up --build

.PHONY: up-orthanc-pacs
up-orthanc-pacs:
	cd orthanc-pacs
	docker compose -f docker-compose-dev.yml up --build

.PHONY: up-redis
up-redis:
	cd redis
	docker compose -f docker-compose-dev.yml up --build

.PHONY: up-elasticsearch
up-elasticsearch:
	cd elasticsearch
	docker compose -f docker-compose-dev.yml up --build

.PHONY: up-torchserve
up-torchserve:
	cd torchserve
	docker compose -f docker-compose-dev.yml up --build

DEMO_XA_ENV_FILE ?= scripts/.env.demo-xa
PYTHON ?= .venv/bin/python

.PHONY: demo-xa-reingest-dry-run
demo-xa-reingest-dry-run:
	$(PYTHON) scripts/demo_xa_reingest.py --env-file "$(DEMO_XA_ENV_FILE)"

.PHONY: demo-xa-reingest
demo-xa-reingest:
ifeq ($(KEEP_PREVIOUS),1)
	$(PYTHON) scripts/demo_xa_reingest.py --env-file "$(DEMO_XA_ENV_FILE)" --execute
else
	$(PYTHON) scripts/demo_xa_reingest.py --env-file "$(DEMO_XA_ENV_FILE)" --execute --prune-previous --allow-database-cleanup
endif
