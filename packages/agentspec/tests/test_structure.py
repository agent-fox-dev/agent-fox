"""Structural tests for the agentspec library package.

Verifies file existence, pyproject.toml content, and package structure
after the monorepo restructure.

Test Spec Entries: TS-10-1, TS-10-2, TS-10-14, TS-10-15, TS-10-E7
"""

from __future__ import annotations

import tomllib
from pathlib import Path

import pytest

REPO_ROOT = Path(__file__).resolve().parents[3]
SPECLIB_PKG = REPO_ROOT / "packages" / "agentspec"


# ---------------------------------------------------------------------------
# TS-10-1: Library Package Directory Structure
# Requirement: 10-REQ-1.3
# ---------------------------------------------------------------------------

_EXPECTED_MODULES = [
    "__init__.py",
    "agent.py",
    "campaign.py",
    "config.py",
    "errors.py",
    "prompts.py",
    "session.py",
    "tools.py",
]


@pytest.mark.parametrize("module", _EXPECTED_MODULES)
def test_ts10_1_library_module_exists(module: str) -> None:
    """TS-10-1: Each required library module exists at packages/agentspec/agentspec/."""
    path = SPECLIB_PKG / "agentspec" / module
    assert path.exists(), f"Expected module not found: {path}"


# ---------------------------------------------------------------------------
# TS-10-2: Library pyproject.toml Dependencies
# Requirement: 10-REQ-1.2
# ---------------------------------------------------------------------------


def _load_agentspec_pyproject() -> dict:
    """Load and parse packages/agentspec/pyproject.toml."""
    pyproject_path = SPECLIB_PKG / "pyproject.toml"
    assert pyproject_path.exists(), f"pyproject.toml not found at {pyproject_path}"
    with open(pyproject_path, "rb") as f:
        return tomllib.load(f)


def test_ts10_2_dependencies_include_required_libs() -> None:
    """TS-10-2: agentspec dependencies include afspec, agentfox, pyyaml."""
    config = _load_agentspec_pyproject()
    deps = config["project"]["dependencies"]

    assert any("afspec" in d for d in deps), "afspec not in dependencies"
    assert any("agentfox" in d for d in deps), "agentfox not in dependencies"
    assert any("pyyaml" in d.lower() for d in deps), "pyyaml not in dependencies"


def test_ts10_2_no_cli_dependencies() -> None:
    """TS-10-2: agentspec dependencies must NOT include click or rich."""
    config = _load_agentspec_pyproject()
    deps = config["project"]["dependencies"]

    assert not any("click" in d.lower() for d in deps), "click should not be in agentspec dependencies"
    assert not any("rich" in d.lower() for d in deps), "rich should not be in agentspec dependencies"


# ---------------------------------------------------------------------------
# TS-10-14: Library Package Has afspec Path Dependency
# Requirement: 10-REQ-1.4
# ---------------------------------------------------------------------------


def test_ts10_14_afspec_path_dependency() -> None:
    """TS-10-14: agentspec declares afspec as a workspace dependency.

    In a uv workspace, member packages reference each other via
    ``{ workspace = true }`` rather than direct path references.
    The root pyproject.toml resolves the actual paths. See
    ``docs/errata/10_workspace_dependency_syntax.md``.
    """
    config = _load_agentspec_pyproject()
    sources = config["tool"]["uv"]["sources"]

    assert "afspec" in sources, "afspec not in uv sources"
    assert sources["afspec"].get("workspace") is True, (
        f"afspec should be a workspace dependency, got {sources['afspec']}"
    )


# ---------------------------------------------------------------------------
# TS-10-15: Library Dev Dependencies
# Requirement: 10-REQ-1.5
# ---------------------------------------------------------------------------


def test_ts10_15_dev_dependencies() -> None:
    """TS-10-15: agentspec has dev optional-dependencies with test/lint tools."""
    config = _load_agentspec_pyproject()
    dev_deps = config["project"]["optional-dependencies"]["dev"]

    assert any("pytest" in d for d in dev_deps), "pytest not in dev deps"
    assert any("ruff" in d for d in dev_deps), "ruff not in dev deps"
    assert any("mypy" in d for d in dev_deps), "mypy not in dev deps"


# ---------------------------------------------------------------------------
# TS-10-E7: Cross-Package Test Isolation
# Requirement: 10-REQ-4.E1
# ---------------------------------------------------------------------------


def test_ts10_e7_no_spec_cli_imports_in_agentspec_tests() -> None:
    """TS-10-E7: No agentspec test file imports from spec_cli.

    Checks actual Python import statements (lines starting with
    ``from spec_cli`` or ``import spec_cli``), not arbitrary string
    occurrences — assertion messages and comments are excluded.
    """
    import re

    test_dir = SPECLIB_PKG / "tests"
    assert test_dir.exists(), f"Test directory not found: {test_dir}"

    import_re = re.compile(r"^\s*(?:from\s+spec_cli\b|import\s+spec_cli\b)")
    for test_file in test_dir.glob("*.py"):
        for lineno, line in enumerate(test_file.read_text().splitlines(), start=1):
            assert not import_re.match(line), f"{test_file.name}:{lineno} imports from spec_cli: {line.strip()!r}"
