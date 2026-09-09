.PHONY: check test test-fast lint format build json-gen clean build-darwin-arm64 build-linux-arm64 build-linux-amd64

VERSION    := $(shell git describe --tags 2>/dev/null || echo "0.1.0")
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -ldflags "\
  -X github.com/agent-fox-dev/agentfox.Version=$(VERSION) \
  -X github.com/agent-fox-dev/agentfox.Commit=$(COMMIT) \
  -X github.com/agent-fox-dev/agentfox.BuildTime=$(BUILD_TIME)"

CONTAINER_REGISTRY ?= quay.io/agentfox

SCHEMAS_DIR := $(CURDIR)/afspec/schemas
DIST_DIR    := $(CURDIR)/dist

# Run lint + all tests
check: lint test

# Run all tests
test:
	go test ./... -count=1

# Run the fast tests only
test-fast:
	go test ./... -short -count=1

# Run linter
lint:
	test -z "$$(gofmt -l .)"
	go vet ./...

format:
	gofmt -w .

# Build all CLIs
build:
	CGO_ENABLED=1 go build $(LDFLAGS) -o bin/af ./cmd/af
	CGO_ENABLED=1 go build $(LDFLAGS) -o bin/nightshift ./cmd/nightshift
	CGO_ENABLED=1 go install $(LDFLAGS) ./cmd/spec
	CGO_ENABLED=1 go install $(LDFLAGS) ./cmd/issue
	CGO_ENABLED=1 go install $(LDFLAGS) ./cmd/fix

# Cross-platform static builds of the three tools
TOOLS := spec issue fix

build-all: build-darwin-arm64 build-linux-arm64 build-linux-amd64

build-darwin-arm64:
	@for tool in $(TOOLS); do \
		CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(DIST_DIR)/$$tool-darwin-arm64 ./cmd/$$tool; \
	done

build-linux-arm64:
	@for tool in $(TOOLS); do \
		CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(DIST_DIR)/$$tool-linux-arm64 ./cmd/$$tool; \
	done

build-linux-amd64:
	@for tool in $(TOOLS); do \
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(DIST_DIR)/$$tool-linux-amd64 ./cmd/$$tool; \
	done

clean:
	rm -rf $(DIST_DIR) bin/af bin/nightshift bin/spec bin/issue bin/fix

# Regenerate the Go artifact types from the bundled JSON Schemas.
# The canonical schemas live in the agent-fox-dev/spec repository; the copies
# under afspec/schemas/ are what the library compiles and embeds.
# --only-models: structural checking belongs to Validate(), which runs the
# compiled schemas. Generated UnmarshalJSON methods would duplicate it and
# would also reject a scaffold that `spec new` must be able to write and read.
json-gen:
	go install github.com/atombender/go-jsonschema@latest
	go-jsonschema --only-models -p afspec $(SCHEMAS_DIR)/requirements.v2.json  > $(CURDIR)/afspec/requirements.v2.go
	go-jsonschema --only-models -p afspec $(SCHEMAS_DIR)/test_spec.v2.json     > $(CURDIR)/afspec/test_spec.v2.go
	go-jsonschema --only-models -p afspec $(SCHEMAS_DIR)/tasks.v2.json         > $(CURDIR)/afspec/tasks.v2.go
	go-jsonschema --only-models -p afspec $(SCHEMAS_DIR)/prd-frontmatter.v2.json > $(CURDIR)/afspec/prd-frontmatter.v2.go
