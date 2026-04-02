include .env

default: run-dev

.PHONY:	install
install:
	go mod tidy
	go mod vendor

.PHONY:	lint
lint:
	golangci-lint run

.PHONY:	build
build:
	mkdir -p bin
	go build -o bin/api-pacs \
	    cmd/main.go

.PHONY:	build-dev
build-dev:
	mkdir -p bin
	go build -race -o bin/api-pacs \
	    cmd/main.go

.PHONY:	test
test:
	go test -race -v -p 1 ./...

.PHONY:	run
run:	build
	./bin/api-pacs

.PHONY:	run-dev
run-dev:	build-dev
	./bin/api-pacs

.PHONY:	migrate-schema
migrate-schema:
	mkdir -p infrastructures/database/postgresql/migrations
	migrate create -ext sql -dir infrastructures/database/postgresql/migrations -seq ${NAME}

.PHONY: migrate-up
migrate-up: 
	migrate -path infrastructures/database/postgresql/migrations -database "postgresql://${POSTGRES_DB_USERNAME}:${POSTGRES_DB_PASSWORD}@${POSTGRES_DB_HOST}:${POSTGRES_DB_PORT}/${POSTGRES_DB_DATABASE}?sslmode=disable" -verbose up ${STEPS}

.PHONY: migrate-down
migrate-down:
	migrate -path infrastructures/database/postgresql/migrations -database "postgresql://${POSTGRES_DB_USERNAME}:${POSTGRES_DB_PASSWORD}@${POSTGRES_DB_HOST}:${POSTGRES_DB_PORT}/${POSTGRES_DB_DATABASE}?sslmode=disable" -verbose down ${STEPS}

.PHONY: migrate-version
migrate-version:
	migrate -path infrastructures/database/postgresql/migrations -database "postgresql://${POSTGRES_DB_USERNAME}:${POSTGRES_DB_PASSWORD}@${POSTGRES_DB_HOST}:${POSTGRES_DB_PORT}/${POSTGRES_DB_DATABASE}?sslmode=disable" version

.PHONY: migrate-force
migrate-force:
	migrate -path infrastructures/database/postgresql/migrations -database "postgresql://${POSTGRES_DB_USERNAME}:${POSTGRES_DB_PASSWORD}@${POSTGRES_DB_HOST}:${POSTGRES_DB_PORT}/${POSTGRES_DB_DATABASE}?sslmode=disable" force ${STEPS}
