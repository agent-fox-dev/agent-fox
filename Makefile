.DEFAULT_GOAL := help

# MODULES are the repository's Go modules.
MODULES := .
DOC_DIR ?= docs/generated

VERSION    := $(shell git describe --tags 2>/dev/null || echo "0.1.0")
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# The identity of the binaries, set with -ldflags -X on the variables of cmd/af and cmd/agent-fox.
LDFLAGS    := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)

# Where make build writes af and agent-fox. The tests that build the binaries set
# it, so that they do not replace bin/ or write the same files side by side.
BIN_DIR    ?= bin

CONTAINER_REGISTRY ?= quay.io/agentfox

SANDBOX_IMAGE ?= sandbox
SANDBOX_IMAGE_TAG ?= $(VERSION)

# The tools image is the sandbox image with af and Claude Code in it. AGENTKIT_REF is
# the branch or tag of agentkit-go that af is built against.
TOOLS_IMAGE ?= tools
TOOLS_IMAGE_TAG ?= $(VERSION)
AGENTKIT_REF ?= main
CLAUDE_CODE_VERSION ?= latest

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*##"}; {printf "  %-24s %s\n", $$1, $$2}'

.PHONY: test
test: ## Run all Go tests
	@rc=0; for m in $(MODULES); do echo "go test ./... ($$m)"; (cd $$m && go test ./... -count=1) || rc=1; done; exit $$rc

.PHONY: test-short
test-short: ## Run all Go tests with -short, skipping the slow gates
	@rc=0; for m in $(MODULES); do echo "go test -short ./... ($$m)"; (cd $$m && go test -short ./... -count=1) || rc=1; done; exit $$rc

.PHONY: build
build: ## Build all packages, and bin/af and bin/agent-fox
	@rc=0; for m in $(MODULES); do echo "go build ./... ($$m)"; (cd $$m && go build ./...) || rc=1; done; exit $$rc
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/af ./cmd/af
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/agent-fox ./cmd/agent-fox

.PHONY: coverage
coverage: ## Run the tests with a coverage report
	go test ./... -coverprofile=coverage.txt -covermode=atomic
	go tool cover -func=coverage.txt

.PHONY: fmt
fmt: ## gofmt all source files in place
	gofmt -l -w .

.PHONY: vet
vet: ## go vet all modules
	@rc=0; for m in $(MODULES); do echo "go vet ./... ($$m)"; (cd $$m && go vet ./...) || rc=1; done; exit $$rc

.PHONY: lint
lint: ## Run golangci-lint if installed, otherwise skip
	@if command -v golangci-lint >/dev/null 2>&1; then \
		set -e; for m in $(MODULES); do (cd $$m && golangci-lint run ./...); done; \
	else \
		echo "golangci-lint not installed; skipping (see https://golangci-lint.run)"; \
	fi

# check is the fast loop for day-to-day work: it runs the tests with -short, so
# the tests that skip themselves under -short are skipped. Run check-release
# before a release or a merge.
.PHONY: check
check: fmt vet lint test-short ## Run fmt, vet, lint and the short tests - the fast check

.PHONY: check-release
check-release: fmt vet lint test ## Run fmt, vet, lint and the full tests, slow gates included - use before a release

.PHONY: doc
doc: ## Generate Go package reference (go doc -all) as text files under docs/generated
	@rm -rf $(DOC_DIR)
	@mkdir -p $(DOC_DIR)
	@for m in $(MODULES); do \
		( cd $$m && mod=$$(go list -m) && pkgs=$$(go list -f '{{if .GoFiles}}{{.ImportPath}}{{end}}' ./...) && \
		for p in $$(printf '%s\n' "$$pkgs" | grep -v -e /internal/ -e /examples/); do \
			rel=$${p#$$mod}; rel=$${rel#/}; pre=$$m/; [ "$$m" = . ] && pre=; \
			out="$(CURDIR)/$(DOC_DIR)/$$pre$${rel:-$$(basename $$mod)}.txt"; \
			mkdir -p "$$(dirname "$$out")" && go doc -all "$$p" > "$$out" || exit 1; \
		done ) || exit 1; \
	done
	@echo "package docs written to $(DOC_DIR)"

.PHONY: tidy
tidy: ## go mod tidy all modules
	@set -e; for m in $(MODULES); do (cd $$m && go mod tidy); done

# ---------------------------------------------------------------------------
# Repository-specific targets
# ---------------------------------------------------------------------------

.PHONY: clean
clean: ## Remove build output and the coverage profile
	rm -rf bin coverage.txt

.PHONY: build-containers
build-containers: build-sandbox-container build-tools-container ## Build the sandbox image, then the tools image on it

.PHONY: build-sandbox-container
build-sandbox-container: ## Build the sandbox image locally
	podman build \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD=$(COMMIT) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(CONTAINER_REGISTRY)/$(SANDBOX_IMAGE):$(SANDBOX_IMAGE_TAG) \
		-t $(CONTAINER_REGISTRY)/$(SANDBOX_IMAGE):latest \
		-f containers/sandbox/Containerfile .

# The tools image is built on the sandbox image that build-sandbox-container tags.
.PHONY: build-tools-container
build-tools-container: ## Build the tools image locally, on the sandbox image
	podman build \
		--build-arg BASE_IMAGE=$(CONTAINER_REGISTRY)/$(SANDBOX_IMAGE):$(SANDBOX_IMAGE_TAG) \
		--build-arg AGENTKIT_REF=$(AGENTKIT_REF) \
		--build-arg CLAUDE_CODE_VERSION=$(CLAUDE_CODE_VERSION) \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD=$(COMMIT) \
		--build-arg BUILD_TIME=$(BUILD_TIME) \
		-t $(CONTAINER_REGISTRY)/$(TOOLS_IMAGE):$(TOOLS_IMAGE_TAG) \
		-t $(CONTAINER_REGISTRY)/$(TOOLS_IMAGE):latest \
		-f containers/tools/Containerfile .

.PHONY: clean-branches
clean-branches: ## Switch to main and delete the local feature, fix, impl, docs and worktree-agent branches
	git checkout main
	@git branch --list 'feature/*' | xargs -r git branch -D
	@git branch --list 'fix/*' | xargs -r git branch -D
	@git branch --list 'impl/*' | xargs -r git branch -D
	@git branch --list 'docs/*' | xargs -r git branch -D
	@git branch --list 'worktree-agent-*' | xargs -r git branch -D

# The skills of skills/ that a coding agent installs, one file each.

SKILLS_TEMPLATES_DIR := $(CURDIR)/skills
CLAUDE_SKILLS_DIR := $(HOME)/.claude/skills

.PHONY: install-skills
install-skills: ## Copy each skills/af-* file to ~/.claude/skills/<name>/SKILL.md
	@for skill in $(SKILLS_TEMPLATES_DIR)/af-*; do \
		name=$$(basename "$$skill"); \
		target="$(CLAUDE_SKILLS_DIR)/$$name"; \
		mkdir -p "$$target"; \
		cp "$$skill" "$$target/SKILL.md"; \
		echo "installed: $$name -> $$target/SKILL.md"; \
	done

.PHONY: uninstall-skills
uninstall-skills: ## Remove the skills that install-skills copied
	@for skill in $(SKILLS_TEMPLATES_DIR)/af-*; do \
		name=$$(basename "$$skill"); \
		if [ -d "$(CLAUDE_SKILLS_DIR)/$$name" ]; then \
			rm -rf "$(CLAUDE_SKILLS_DIR)/$$name"; \
			echo "removed: $$name"; \
		fi; \
	done
