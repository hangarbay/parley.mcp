IMAGE ?= parley.mcp

.PHONY: build test docker

build:
	go build -o parley-mcp ./cmd/parley-mcp

test:
	go vet ./...
	go test ./...

docker:
	docker build -t $(IMAGE):local .
