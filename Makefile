.PHONY: check test test-fast lint format coverage build clean build-containers build-sandbox-container clean-branches install-skills uninstall-skills doc

VERSION    := $(shell git describe --tags 2>/dev/null || echo "0.1.0")
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# The identity of the af binary, set with -ldflags -X on cmd/af's variables.
LDFLAGS    := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)

CONTAINER_REGISTRY ?= quay.io/agentfox

SANDBOX_IMAGE ?= sandbox
SANDBOX_IMAGE_TAG ?= $(VERSION)

DOC_DIR ?= docs/generated

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

# Build every package, and bin/af
build:
	go build ./...
	go build -ldflags "$(LDFLAGS)" -o bin/af ./cmd/af

build-containers: build-sandbox-container

# Build the sandbox container locally.
build-sandbox-container:
	podman build \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD=$(COMMIT) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(CONTAINER_REGISTRY)/$(SANDBOX_IMAGE):$(SANDBOX_IMAGE_TAG) \
		-t $(CONTAINER_REGISTRY)/$(SANDBOX_IMAGE):latest \
		-f containers/sandbox/Containerfile .

clean:
	-rm -f coverage.txt
	-rm -rf bin

clean-branches:
	git checkout main
	@git branch --list 'feature/*' | xargs -r git branch -D
	@git branch --list 'fix/*' | xargs -r git branch -D
	@git branch --list 'impl/*' | xargs -r git branch -D
	@git branch --list 'docs/*' | xargs -r git branch -D
	@git branch --list 'worktree-agent-*' | xargs -r git branch -D

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

doc: ## Generate Go package reference (go doc -all) as text files under build/doc
	@rm -rf $(DOC_DIR)
	@mkdir -p $(DOC_DIR)
	@for m in . ; do \
		( cd $$m && mod=$$(go list -m) && pkgs=$$(go list ./...) && \
		for p in $$(printf '%s\n' "$$pkgs" | grep -v -e /internal/ -e /examples/); do \
			rel=$${p#$$mod}; rel=$${rel#/}; out="$(CURDIR)/$(DOC_DIR)/$${rel:-$$(basename $$mod)}.txt"; \
			mkdir -p "$$(dirname "$$out")" && go doc -all "$$p" > "$$out" || exit 1; \
		done ) || exit 1; \
	done
	@echo "package docs written to $(DOC_DIR)"