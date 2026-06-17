"""Tests for project scaffold and package structure."""

from __future__ import annotations

import tomllib
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[3]


class TestProjectStructure:
    """Tests for pyproject.toml and build configuration."""

    def test_agentspec_runtime_deps(self) -> None:
        """agentspec declares required runtime dependencies."""
        agentspec_pyproject = PROJECT_ROOT / "packages" / "agentspec" / "pyproject.toml"
        with open(agentspec_pyproject, "rb") as f:
            agentspec_toml = tomllib.load(f)
        agentspec_deps = " ".join(agentspec_toml.get("project", {}).get("dependencies", [])).lower()
        assert "afspec" in agentspec_deps, "afspec must be an agentspec dependency"
        assert "agentfox" in agentspec_deps, "agentfox must be an agentspec dependency"
        assert "pyyaml" in agentspec_deps, "pyyaml must be an agentspec dependency"

    def test_dev_deps(self) -> None:
        """pyproject.toml declares dev dependencies."""
        pyproject = PROJECT_ROOT / "pyproject.toml"
        with open(pyproject, "rb") as f:
            toml = tomllib.load(f)

        dev_deps: list[str] = []
        dep_groups = toml.get("dependency-groups", {})
        if "dev" in dep_groups:
            dev_deps = dep_groups["dev"]

        deps_lower = " ".join(str(d) for d in dev_deps).lower()
        assert "pytest" in deps_lower, "pytest must be a dev dependency"
        assert "hypothesis" in deps_lower, "hypothesis must be a dev dependency"
        assert "ruff" in deps_lower, "ruff must be a dev dependency"
        assert "mypy" in deps_lower, "mypy must be a dev dependency"

    def test_python_version(self) -> None:
        """pyproject.toml requires Python 3.12+."""
        pyproject = PROJECT_ROOT / "pyproject.toml"
        with open(pyproject, "rb") as f:
            toml = tomllib.load(f)
        requires_python = toml.get("project", {}).get("requires-python", "")
        assert requires_python == ">=3.12", f"requires-python must be '>=3.12', got '{requires_python}'"

    def test_make_check(self) -> None:
        """make check runs linter and tests."""
        makefile = PROJECT_ROOT / "Makefile"
        assert makefile.exists(), "Makefile must exist"
        content = makefile.read_text()
        has_lint = "ruff" in content or "lint" in content
        has_test = "pytest" in content or "test" in content
        assert has_lint, "Makefile check target must reference ruff or lint"
        assert has_test, "Makefile check target must reference pytest or test"

    def test_make_test(self) -> None:
        """make test runs pytest."""
        makefile = PROJECT_ROOT / "Makefile"
        assert makefile.exists(), "Makefile must exist"
        content = makefile.read_text()
        assert "uv run pytest" in content, "Makefile test target must run 'uv run pytest'"


class TestExceptionHierarchy:
    """Tests for agentspec exception classes."""

    def test_agentspec_error_base(self) -> None:
        """AgentSpecError is defined and inherits from Exception."""
        from agentspec.errors import AgentSpecError

        assert issubclass(AgentSpecError, Exception)

    def test_config_error_inherits(self) -> None:
        """ConfigError inherits from AgentSpecError."""
        from agentspec.errors import AgentSpecError, ConfigError

        assert issubclass(ConfigError, AgentSpecError)


class TestEdgeCases:
    """Edge case tests for project scaffold."""

    def test_uv_required(self) -> None:
        """Project documents uv as required installer."""
        readme = PROJECT_ROOT / "packages" / "README.md"
        assert readme.exists(), "README.md must exist"
        content = readme.read_text()
        assert "uv" in content, "README must mention uv as required tool"
