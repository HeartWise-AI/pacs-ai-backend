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
