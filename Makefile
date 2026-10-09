.PHONY: check test test-fast lint format build json-gen clean build-darwin-arm64 build-linux-arm64 build-linux-amd64

VERSION    := $(shell git describe --tags 2>/dev/null || echo "0.1.0")
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDVARS := \
  -X github.com/agent-fox-dev/agentfox.Version=$(VERSION) \
  -X github.com/agent-fox-dev/agentfox.Commit=$(COMMIT) \
  -X github.com/agent-fox-dev/agentfox.BuildTime=$(BUILD_TIME)

LDFLAGS := -ldflags "$(LDVARS)"

# The Linux release binaries are linked statically so they run on any
# distribution; netgo and osusergo keep name resolution and user lookup in Go,
# so the C library contributes nothing that needs a dynamic loader.
STATIC_LDFLAGS := -tags netgo,osusergo -ldflags "$(LDVARS) -linkmode external -extldflags -static"

CONTAINER_REGISTRY ?= quay.io/agentfox

SANDBOX_IMAGE ?= sandbox
SANDBOX_IMAGE_TAG ?= $(VERSION)
TOOLS_IMAGE ?= tools
TOOLS_IMAGE_TAG ?= $(VERSION)

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

# Generate test coverage
coverage:
	go test ./... -coverprofile=coverage.txt -covermode=atomic
	go tool cover -func=coverage.txt

# Build all CLIs
build:
	CGO_ENABLED=1 go build $(LDFLAGS) -o bin/af ./cmd/af
	CGO_ENABLED=1 go build $(LDFLAGS) -o bin/nightshift ./cmd/nightshift
	CGO_ENABLED=1 go install $(LDFLAGS) ./cmd/spec
	CGO_ENABLED=1 go install $(LDFLAGS) ./cmd/triage
	CGO_ENABLED=1 go install $(LDFLAGS) ./cmd/fix
	CGO_ENABLED=1 go install $(LDFLAGS) ./cmd/impl

# Release builds of the tools, one per platform, into dist/. They build with
# cgo: AgentKit outlines every language but Go with tree-sitter, which is C, and
# a build without cgo outlines Go files only. So each platform needs a C
# compiler for its target. Build each on a matching host, as
# .github/workflows/build-binaries.yaml does, or name a cross compiler with CC:
#
#   make build-linux-amd64 CC="zig cc -target x86_64-linux-gnu"
TOOLS := spec triage fix impl

build-all: build-darwin-arm64 build-linux-arm64 build-linux-amd64

# check-host stops a platform build on a different host when no CC is given,
# where the host's own C compiler would fail on the target's headers.
define check-host
	@if [ "$(origin CC)" = "default" ] && [ "$$(go env GOHOSTOS)/$$(go env GOHOSTARCH)" != "$(1)" ]; then \
		echo "$@ needs a $(1) host, or CC set to a C compiler for $(1)" >&2; exit 1; \
	fi
endef

build-darwin-arm64:
	$(call check-host,darwin/arm64)
	@for tool in $(TOOLS); do \
		CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(DIST_DIR)/$$tool-darwin-arm64 ./cmd/$$tool || exit 1; \
	done

build-linux-arm64:
	$(call check-host,linux/arm64)
	@for tool in $(TOOLS); do \
		CGO_ENABLED=1 GOOS=linux GOARCH=arm64 go build $(STATIC_LDFLAGS) -o $(DIST_DIR)/$$tool-linux-arm64 ./cmd/$$tool || exit 1; \
	done

build-linux-amd64:
	$(call check-host,linux/amd64)
	@for tool in $(TOOLS); do \
		CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build $(STATIC_LDFLAGS) -o $(DIST_DIR)/$$tool-linux-amd64 ./cmd/$$tool || exit 1; \
	done

build-containers: build-sandbox-container build-tools-container

# Build the sandbox container locally.
build-sandbox-container:
	podman build \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD=$(COMMIT) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(CONTAINER_REGISTRY)/$(SANDBOX_IMAGE):$(SANDBOX_IMAGE_TAG) \
		-t $(CONTAINER_REGISTRY)/$(SANDBOX_IMAGE):latest \
		-f containers/sandbox/Containerfile .

build-tools-container:
	podman build \
		--build-context agentkit-go=../agentkit-go \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD=$(COMMIT) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(CONTAINER_REGISTRY)/$(TOOLS_IMAGE):$(TOOLS_IMAGE_TAG) \
		-t $(CONTAINER_REGISTRY)/$(TOOLS_IMAGE):latest \
		-f containers/tools/Containerfile .

clean:
	-rm -f coverage.txt
	-rm -rf bin/af bin/nightshift
	-rm -rf $(DIST_DIR)/*-arm64 $(DIST_DIR)/*-amd64

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

# manage skills

SKILLS_TEMPLATES_DIR := $(CURDIR)/skills
CLAUDE_SKILLS_DIR := $(HOME)/.claude/skills

install-skills:
	@for skill in $(SKILLS_TEMPLATES_DIR)/*; do \
		name=$$(basename "$$skill"); \
		target="$(CLAUDE_SKILLS_DIR)/$$name"; \
		mkdir -p "$$target"; \
		cp "$$skill" "$$target/SKILL.md"; \
		echo "installed: $$name -> $$target/SKILL.md"; \
	done

uninstall-skills:
	@for skill in $(SKILLS_TEMPLATES_DIR)/*; do \
		name=$$(basename "$$skill"); \
		if [ -d "$(CLAUDE_SKILLS_DIR)/$$name" ]; then \
			rm -rf "$(CLAUDE_SKILLS_DIR)/$$name"; \
			echo "removed: $$name"; \
		fi; \
	done