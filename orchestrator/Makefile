.PHONY: build test lint format

build:
	go build ./...

test:
	go test ./...

lint:
	golangci-lint run ./...

format:
	gofmt -w .
