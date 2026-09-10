IMAGE ?= parley.mcp

.PHONY: build test integ docker

build:
	go build -o parley-mcp ./cmd/parley-mcp

test:
	go vet ./...
	go test ./...

# Live end-to-end check of both providers over the MCP surface. Needs network
# access; the Gemini case also needs Firefox.
integ:
	go test -tags=integration -count=1 -timeout 5m -v ./cmd/parley-mcp/

docker:
	docker build -t $(IMAGE):local .
