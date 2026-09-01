"""Shared fixtures for agentfox test suite."""

from __future__ import annotations

import logging
import os
import subprocess
from collections.abc import Generator
from pathlib import Path

import duckdb
import pytest
from hypothesis import settings

settings.register_profile("ci", deadline=None)
settings.load_profile("ci")

_SLOW_DIRS = ("/property/", "/integration/", "/spec/", "/maintenance/")
_SLOW_SUFFIXES = ("_props.py", "_properties.py")
_SLOW_FILES = ("test_orchestrator.py", "test_block_budget.py", "test_knowledge_pruning.py")


def pytest_collection_modifyitems(config: pytest.Config, items: list[pytest.Item]) -> None:
    """Auto-mark tests as slow based on file path patterns.

    Used by ``make test-fast`` (``pytest -m 'not slow'``) to skip
    property tests, integration tests, orchestrator loop tests, and
    other tests that take >1 s each.
    """
    slow = pytest.mark.slow
    for item in items:
        path = str(item.fspath)
        if any(d in path for d in _SLOW_DIRS):
            item.add_marker(slow)
        elif any(path.endswith(s) for s in _SLOW_SUFFIXES):
            item.add_marker(slow)
        elif any(path.endswith(f) for f in _SLOW_FILES):
            item.add_marker(slow)


from agentfox.knowledge.db import KnowledgeDB  # noqa: E402
from agentfox.knowledge.migrations import apply_pending_migrations  # noqa: E402

from tests.unit.knowledge.conftest import SCHEMA_DDL  # noqa: E402


@pytest.fixture(autouse=True)
def _reset_agent_fox_logger() -> Generator[None, None, None]:
    """Reset the agentfox logger after each test."""
    yield
    agent_logger = logging.getLogger("agentfox")
    agent_logger.setLevel(logging.NOTSET)
    agent_logger.handlers.clear()


@pytest.fixture
def knowledge_conn() -> Generator[duckdb.DuckDBPyConnection, None, None]:
    """Provide a fresh in-memory DuckDB with all migrations applied."""
    conn = duckdb.connect(":memory:")
    conn.execute(SCHEMA_DDL)
    apply_pending_migrations(conn)
    yield conn
    conn.close()


@pytest.fixture
def knowledge_db(
    knowledge_conn: duckdb.DuckDBPyConnection,
) -> Generator[KnowledgeDB, None, None]:
    """Provide a KnowledgeDB wrapper around in-memory DuckDB."""
    db = KnowledgeDB.__new__(KnowledgeDB)
    db._conn = knowledge_conn
    yield db


@pytest.fixture
def cli_runner():
    """Provide a Click CLI test runner."""
    from click.testing import CliRunner

    return CliRunner()


@pytest.fixture
def tmp_git_repo(tmp_path: Path) -> Generator[Path, None, None]:
    """Create a temporary git repository for integration tests."""
    repo = tmp_path / "repo"
    repo.mkdir()

    subprocess.run(["git", "init"], cwd=repo, check=True, capture_output=True)
    subprocess.run(
        ["git", "config", "user.email", "test@test.com"],
        cwd=repo,
        check=True,
        capture_output=True,
    )
    subprocess.run(
        ["git", "config", "user.name", "Test"],
        cwd=repo,
        check=True,
        capture_output=True,
    )

    readme = repo / "README.md"
    readme.write_text("# Test repo\n")
    subprocess.run(["git", "add", "."], cwd=repo, check=True, capture_output=True)
    subprocess.run(
        ["git", "commit", "-m", "initial"],
        cwd=repo,
        check=True,
        capture_output=True,
    )

    original_dir = os.getcwd()
    os.chdir(repo)
    yield repo
    os.chdir(original_dir)
