"""Tests for afaudit package scaffold structure.

TS-01-1: pyproject.toml metadata
TS-01-2: Flat layout with all required module files
TS-01-3: Zero third-party runtime dependencies
TS-01-E1: Dependency isolation check
"""

from __future__ import annotations

import subprocess
import sys
import tomllib
from pathlib import Path

WORKSPACE_ROOT = Path(__file__).resolve().parents[3]
AFAUDIT_PKG = WORKSPACE_ROOT / "packages" / "afaudit"
AFAUDIT_SRC = AFAUDIT_PKG / "afaudit"


class TestPyprojectToml:
    """TS-01-1: pyproject.toml declares correct metadata.

    Requirement: 01-REQ-1.1
    """

    def _load_toml(self) -> dict:
        with open(AFAUDIT_PKG / "pyproject.toml", "rb") as f:
            return tomllib.load(f)

    def test_version_is_4_0_2(self) -> None:
        """Version must be 4.3.3."""
        toml = self._load_toml()
        assert toml["project"]["version"] == "4.3.3"

    def test_requires_python(self) -> None:
        """requires-python must be >=3.12."""
        toml = self._load_toml()
        assert toml["project"]["requires-python"] == ">=3.12"

    def test_build_backend_is_hatchling(self) -> None:
        """Build backend must be hatchling.build."""
        toml = self._load_toml()
        assert toml["build-system"]["build-backend"] == "hatchling.build"

    def test_dependencies_is_empty_list(self) -> None:
        """[project.dependencies] must be an empty list."""
        toml = self._load_toml()
        assert toml["project"]["dependencies"] == []

    def test_no_optional_dependencies(self) -> None:
        """No [project.optional-dependencies] section allowed."""
        toml = self._load_toml()
        assert "optional-dependencies" not in toml["project"]


class TestFilesystemLayout:
    """TS-01-2: Flat layout with all required modules.

    Requirement: 01-REQ-1.2
    """

    def test_afaudit_package_dir_exists(self) -> None:
        """packages/afaudit/afaudit/ must exist (flat layout)."""
        assert AFAUDIT_SRC.is_dir()

    def test_no_src_directory(self) -> None:
        """packages/afaudit/src/ must NOT exist (no src/ layout)."""
        assert not (AFAUDIT_PKG / "src").exists()

    def test_tests_directory_exists(self) -> None:
        """packages/afaudit/tests/ must exist."""
        assert (AFAUDIT_PKG / "tests").is_dir()

    def test_all_module_files_exist(self) -> None:
        """All eight required module files must be present."""
        required_files = [
            "__init__.py",
            "events.py",
            "sink.py",
            "trace.py",
            "postmortem.py",
            "cleanup.py",
            "emit.py",
            "constants.py",
        ]
        for fname in required_files:
            assert (AFAUDIT_SRC / fname).exists(), f"Missing module: {fname}"


class TestZeroDependencies:
    """TS-01-3: Zero third-party runtime dependencies.

    Requirement: 01-REQ-1.3
    """

    def test_pip_show_requires_is_empty(self) -> None:
        """afaudit has no third-party runtime dependencies.

        Uses importlib.metadata (stdlib) for environments where pip may
        not be installed (e.g. uv-managed virtualenvs).  Falls back to
        ``pip show`` if metadata lookup fails.
        """
        import importlib.metadata

        try:
            dist = importlib.metadata.distribution("afaudit")
            requires = dist.requires
            # requires is None or a list of strings (PEP 508 markers).
            # Filter out extras; only check mandatory (no marker or no extra).
            if requires:
                mandatory = [r for r in requires if "extra ==" not in r and "extra==" not in r]
                assert mandatory == [], f"Expected no mandatory deps, got: {mandatory}"
        except importlib.metadata.PackageNotFoundError:
            # Fall back to pip show
            result = subprocess.run(
                [sys.executable, "-m", "pip", "show", "afaudit"],
                capture_output=True,
                text=True,
            )
            assert result.returncode == 0, f"pip show afaudit failed: {result.stderr}"
            for line in result.stdout.splitlines():
                if line.startswith("Requires:"):
                    requires_value = line.split(":", 1)[1].strip()
                    assert requires_value == "", f"Expected empty Requires, got: {requires_value!r}"
                    return


class TestDependencyIsolation:
    """TS-01-E1: Dependency isolation check.

    Requirement: 01-REQ-1.E1
    """

    def test_no_third_party_in_dependencies(self) -> None:
        """[project.dependencies] must be empty — catches accidental additions."""
        with open(AFAUDIT_PKG / "pyproject.toml", "rb") as f:
            toml = tomllib.load(f)
        deps = toml["project"].get("dependencies", [])
        assert deps == [], f"Third-party dependency found in afaudit: {deps}"
