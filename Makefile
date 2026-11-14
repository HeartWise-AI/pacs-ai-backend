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

# Production never rebuilds on "up": a rebuild from whatever source is checked
# out on the host can silently replace a tested image under the same tag.
# Images that do not exist yet are still built. Rebuild on purpose with
# "make build-prod". study-service is deployed with
# cardio-agent/study-service/scripts/deploy.sh (one image for the API and all
# of its workers, set by STUDY_SERVICE_IMAGE in .env).
.PHONY: up-prod
up-prod:
	docker compose up -d

.PHONY: build-prod
build-prod:
	docker compose build

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

DEMO_XA_ENV_FILE ?= scripts/.env.demo-xa
PYTHON ?= .venv/bin/python

.PHONY: demo-xa-reingest-dry-run
demo-xa-reingest-dry-run:
	$(PYTHON) scripts/demo_xa_reingest.py --env-file "$(DEMO_XA_ENV_FILE)"

.PHONY: demo-xa-reingest
demo-xa-reingest:
ifeq ($(EXECUTE),1)
ifeq ($(KEEP_PREVIOUS),1)
	$(PYTHON) scripts/demo_xa_reingest.py --env-file "$(DEMO_XA_ENV_FILE)" --execute
else
	$(PYTHON) scripts/demo_xa_reingest.py --env-file "$(DEMO_XA_ENV_FILE)" --execute --prune-previous --allow-database-cleanup
endif
else
	$(PYTHON) scripts/demo_xa_reingest.py --env-file "$(DEMO_XA_ENV_FILE)"
endif
