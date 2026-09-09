.PHONY: check test lint build

VERSION    := $(shell git describe --tags 2>/dev/null || echo "0.1.0")
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -ldflags "\
  -X github.com/agent-fox-dev/agentfox.Version=$(VERSION) \
  -X github.com/agent-fox-dev/agentfox.Commit=$(COMMIT) \
  -X github.com/agent-fox-dev/agentfox.BuildTime=$(BUILD_TIME)"

CONTAINER_REGISTRY ?= quay.io/agentfox

# Run lint + all tests
check: lint test

# Run all tests
test:
	go test ./... -count=1

# Run linter
lint:
	go vet ./...

# Build all CLIs
build:
	CGO_ENABLED=1 go build $(LDFLAGS) -o bin/af ./cmd/af
	CGO_ENABLED=1 go build $(LDFLAGS) -o bin/nightshift ./cmd/nightshift
