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
up-orthanc:
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