.PHONY: clean test test-fast test-unit test-property test-integration lint format check clean-branches install-skills uninstall-skills

clean:
	find . -type d -name __pycache__ -exec rm -rf {} + 2>/dev/null || true
	find . -type f -name '*.pyc' -delete 2>/dev/null || true
	find . -type f -name '*.pyo' -delete 2>/dev/null || true
	rm -rf .pytest_cache/ *.egg-info/ dist/ .ruff_cache/ .mypy_cache/ .hypothesis/
	rm -rf packages/*/.pytest_cache packages/*/.mypy_cache packages/*/.ruff_cache
	rm -rf packages/*/build packages/*/dist packages/*/*.egg-info

test: clean
	uv run pytest -q

test-fast:
	uv run pytest -m "not slow" -q

test-unit:
	uv run pytest packages/agentfox/tests/unit/ packages/af/tests/unit/ -q

test-property:
	uv run pytest packages/agentfox/tests/property/ packages/af/tests/property/ -q

test-integration:
	uv run pytest packages/agentfox/tests/integration/ packages/af/tests/integration/ -q

lint:
	uv run ruff check packages/ && uv run ruff format --check packages/

format:
	uv run ruff format packages/

check: lint test

check-all: lint test test-unit test-property test-integration

clean-branches:
	@git branch --list 'feature/*' | xargs -r git branch -D
	@git branch --list 'fix/*' | xargs -r git branch -D

SKILLS_TEMPLATES_DIR := $(CURDIR)/packages/agentfox/agentfox/_templates/skills
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
