IMAGE ?= parley.mcp
CONTAINER ?= parley

.PHONY: build test integ docker pull restart update

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

# Pull the published image. The CI workflow pushes hangarbay/parley.mcp:latest
# and :sha on every commit to main, so this picks up the newest build.
pull:
	docker pull hangarbay/parley.mcp:latest

# Stop and remove any existing container, then start a fresh one from the newly
# pulled image. The server runs over stdio, so it needs -i to keep stdin open.
restart:
	docker rm -f $(CONTAINER) 2>/dev/null || true
	docker run -d -i --name $(CONTAINER) hangarbay/parley.mcp:latest

# Pull the newest image and restart the container on it. This is the usual
# redeploy cycle after a push: build on CI, then make update.
update: pull restart
	@echo "parley.mcp container $(CONTAINER) restarted on hangarbay/parley.mcp:latest"
