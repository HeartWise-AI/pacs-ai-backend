default: up-local

.PHONY: up
up:
	docker compose up -d --build

.PHONY: down
down:
	docker compose down

.PHONY: up-local
up-local:
	docker compose up --build

.PHONY: up-api-pacs
up-api-pacs:
	cd api-pacs
	docker compose up --build

.PHONY: up-orthanc
up-orthanc:
	cd orthanc
	docker compose up --build

.PHONY: up-orthanc
up-orthanc:
	cd orthanc
	docker compose up --build

.PHONY: up-redis
up-redis:
	cd redis
	docker compose up --build