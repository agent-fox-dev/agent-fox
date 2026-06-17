"""Tests for project scaffold and package structure.

Test Spec Entries: TS-01-1 through TS-01-6, TS-01-16, TS-01-17, TS-01-E7.
"""

from __future__ import annotations

import tomllib
from pathlib import Path

PROJECT_ROOT = Path(__file__).resolve().parents[3]


class TestProjectStructure:
    """Tests for pyproject.toml and build configuration."""

    def test_ts01_1_package_installable(self) -> None:
        """TS-01-1: Package declares spec CLI entry point.

        Requirement: 01-REQ-1.1
        Superseded by spec 10 (monorepo restructure): the CLI entry point
        moved from root pyproject.toml (af-spec) to packages/spec-cli/
        pyproject.toml (spec). This test verifies the new location.
        """
        pyproject = PROJECT_ROOT / "packages" / "spec-cli" / "pyproject.toml"
        assert pyproject.exists(), "spec-cli pyproject.toml must exist"
        with open(pyproject, "rb") as f:
            toml = tomllib.load(f)
        scripts = toml.get("project", {}).get("scripts", {})
        assert "spec" in scripts, "spec CLI entry point must be declared"

    def test_ts01_2_runtime_deps(self) -> None:
        """TS-01-2: Packages declare required runtime dependencies.

        Requirement: 01-REQ-1.2
        Superseded by spec 10 (monorepo restructure): dependencies are now
        split across packages. agentspec declares afspec, anthropic, pyyaml;
        spec-cli declares agentspec, click, rich.
        """
        # Check agentspec dependencies
        agentspec_pyproject = PROJECT_ROOT / "packages" / "agentspec" / "pyproject.toml"
        with open(agentspec_pyproject, "rb") as f:
            agentspec_toml = tomllib.load(f)
        agentspec_deps = " ".join(agentspec_toml.get("project", {}).get("dependencies", [])).lower()
        assert "afspec" in agentspec_deps, "afspec must be a agentspec dependency"
        assert "anthropic" in agentspec_deps, "anthropic must be a agentspec dependency"
        assert "pyyaml" in agentspec_deps, "pyyaml must be a agentspec dependency"

        # Check spec-cli dependencies
        cli_pyproject = PROJECT_ROOT / "packages" / "spec-cli" / "pyproject.toml"
        with open(cli_pyproject, "rb") as f:
            cli_toml = tomllib.load(f)
        cli_deps = " ".join(cli_toml.get("project", {}).get("dependencies", [])).lower()
        assert "click" in cli_deps, "click must be a spec-cli dependency"

    def test_ts01_3_dev_deps(self) -> None:
        """TS-01-3: pyproject.toml declares dev dependencies.

        Requirement: 01-REQ-1.3
        Dev dependencies: pytest, hypothesis, ruff, mypy
        """
        pyproject = PROJECT_ROOT / "pyproject.toml"
        with open(pyproject, "rb") as f:
            toml = tomllib.load(f)

        # Check both possible locations for dev deps
        dev_deps: list[str] = []
        optional_deps = toml.get("project", {}).get("optional-dependencies", {})
        if "dev" in optional_deps:
            dev_deps = optional_deps["dev"]
        dep_groups = toml.get("dependency-groups", {})
        if "dev" in dep_groups:
            dev_deps = dep_groups["dev"]

        deps_lower = " ".join(str(d) for d in dev_deps).lower()
        assert "pytest" in deps_lower, "pytest must be a dev dependency"
        assert "hypothesis" in deps_lower, "hypothesis must be a dev dependency"
        assert "ruff" in deps_lower, "ruff must be a dev dependency"
        assert "mypy" in deps_lower, "mypy must be a dev dependency"

    def test_ts01_4_python_version(self) -> None:
        """TS-01-4: pyproject.toml requires Python 3.14+.

        Requirement: 01-REQ-1.4
        """
        pyproject = PROJECT_ROOT / "pyproject.toml"
        with open(pyproject, "rb") as f:
            toml = tomllib.load(f)
        requires_python = toml.get("project", {}).get("requires-python", "")
        assert requires_python == ">=3.14", f"requires-python must be '>=3.14', got '{requires_python}'"

    def test_ts01_5_make_check(self) -> None:
        """TS-01-5: make check runs linter and tests.

        Requirement: 01-REQ-1.5
        """
        makefile = PROJECT_ROOT / "Makefile"
        assert makefile.exists(), "Makefile must exist"
        content = makefile.read_text()
        has_lint = "ruff" in content or "lint" in content
        has_test = "pytest" in content or "test" in content
        assert has_lint, "Makefile check target must reference ruff or lint"
        assert has_test, "Makefile check target must reference pytest or test"

    def test_ts01_6_make_test(self) -> None:
        """TS-01-6: make test runs pytest.

        Requirement: 01-REQ-1.6
        """
        makefile = PROJECT_ROOT / "Makefile"
        assert makefile.exists(), "Makefile must exist"
        content = makefile.read_text()
        assert "uv run pytest" in content, "Makefile test target must run 'uv run pytest'"


class TestExceptionHierarchy:
    """Tests for agentspec exception classes."""

    def test_ts01_16_agentspec_error_base(self) -> None:
        """TS-01-16: AgentSpecError is defined and inherits from Exception.

        Requirement: 01-REQ-4.1
        """
        from agentspec.errors import AgentSpecError

        assert issubclass(AgentSpecError, Exception)

    def test_ts01_17_config_error_inherits(self) -> None:
        """TS-01-17: ConfigError inherits from AgentSpecError.

        Requirement: 01-REQ-4.2
        """
        from agentspec.errors import AgentSpecError, ConfigError

        assert issubclass(ConfigError, AgentSpecError)


class TestEdgeCases:
    """Edge case tests for project scaffold."""

    def test_ts01_e7_uv_required(self) -> None:
        """TS-01-E7: Project documents uv as required installer.

        Requirement: 01-REQ-1.E1
        Verifies that README.md mentions uv and does not include bare
        ``pip install`` instructions (``uv pip install`` is acceptable).
        """
        readme = PROJECT_ROOT / "packages" / "README.md"
        assert readme.exists(), "README.md must exist"
        content = readme.read_text()
        assert "uv" in content, "README must mention uv as required tool"
        # Ensure no bare pip install instructions (uv pip install is OK)
        for line in content.split("\n"):
            if "pip install" in line and "uv pip install" not in line:
                msg = f"README should not have bare pip install instructions: {line}"
                raise AssertionError(msg)
