.PHONY: clean test test-fast test-unit test-property test-integration test-deepagents lint format check clean-branches install-skills uninstall-skills

clean:
	find . -type d -name __pycache__ -exec rm -rf {} + 2>/dev/null || true
	find . -type f -name '*.pyc' -delete 2>/dev/null || true
	find . -type f -name '*.pyo' -delete 2>/dev/null || true
	rm -rf .pytest_cache/ *.egg-info/ dist/ .ruff_cache/ .mypy_cache/ .hypothesis/
	rm -rf packages/*/.pytest_cache packages/*/.mypy_cache packages/*/.ruff_cache
	rm -rf packages/*/build packages/*/dist packages/*/*.egg-info

test:
	uv run pytest -q

test-fast:
	uv run pytest -m "not slow" -q

test-unit:
	uv run pytest packages/agentfox/tests/unit/ packages/af/tests/unit/ -q

test-property:
	uv run pytest packages/agentfox/tests/property/ packages/af/tests/property/ -q

test-integration:
	uv run pytest packages/agentfox/tests/integration/ packages/af/tests/integration/ -q

test-deepagents:
	uv pip install '.[deepagents]' && uv run pytest packages/agentfox/tests/unit/session/backends/test_deepagents.py -q

lint:
	uv run ruff check packages/ && uv run ruff format --check packages/

format:
	uv run ruff format packages/

check: lint test

check-all: clear lint test test-unit test-property test-integration

clean-branches:
	@git branch --list 'feature/*' | xargs -r git branch -D
	@git branch --list 'fix/*' | xargs -r git branch -D
	@git branch --list 'refactor/*' | xargs -r git branch -D

