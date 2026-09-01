"""Tests for afissues package scaffold (TS-03-1 through TS-03-5).

Verifies pyproject.toml metadata, workspace membership, py.typed marker,
wheel build configuration, and pytest configuration.

Requirements: 03-REQ-1.1, 03-REQ-1.2, 03-REQ-1.3, 03-REQ-1.4, 03-REQ-1.5
"""

from __future__ import annotations

import tomllib
from pathlib import Path

import pytest

# ── Paths ───────────────────────────────────────────────────────────
# Resolve relative to this test file so the tests work regardless of cwd.
_TESTS_UNIT = Path(__file__).resolve().parent
_AFISSUES_PKG = _TESTS_UNIT.parents[1]  # packages/afissues/
_AFISSUES_SRC = _AFISSUES_PKG / "afissues"


@pytest.fixture()
def pyproject() -> dict:
    """Parse packages/afissues/pyproject.toml."""
    toml_path = _AFISSUES_PKG / "pyproject.toml"
    assert toml_path.exists(), f"pyproject.toml not found at {toml_path}"
    with open(toml_path, "rb") as f:
        return tomllib.load(f)


# ── TS-03-1: pyproject.toml declares correct metadata ──────────────


class TestPyprojectMetadata:
    """TS-03-1: Build backend, name, version, python, and httpx dep."""

    def test_build_backend_is_hatchling(self, pyproject: dict) -> None:
        assert pyproject["build-system"]["build-backend"] == "hatchling.build"

    def test_package_name_is_afissues(self, pyproject: dict) -> None:
        assert pyproject["project"]["name"] == "afissues"

    def test_version_is_4_2_0(self, pyproject: dict) -> None:
        assert pyproject["project"]["version"] == "4.7.0"

    def test_requires_python_ge_312(self, pyproject: dict) -> None:
        assert pyproject["project"]["requires-python"] == ">=3.12"

    def test_httpx_in_dependencies(self, pyproject: dict) -> None:
        deps = pyproject["project"]["dependencies"]
        assert "httpx>=0.27" in deps


# ── TS-03-2: uv workspace auto-registration ────────────────────────


class TestWorkspaceMembership:
    """TS-03-2: afissues is auto-resolved as a workspace member."""

    def test_afissues_version_resolvable(self) -> None:
        import importlib.metadata

        version = importlib.metadata.version("afissues")
        assert "4.7.0" in version


# ── TS-03-3: py.typed marker file ──────────────────────────────────


class TestPyTypedMarker:
    """TS-03-3: py.typed exists in source and installed package."""

    def test_py_typed_in_source_tree(self) -> None:
        assert (_AFISSUES_SRC / "py.typed").exists()

    def test_py_typed_in_installed_package(self) -> None:
        import afissues

        pkg_path = Path(afissues.__file__).parent
        assert (pkg_path / "py.typed").exists()


# ── TS-03-4: wheel build configuration ─────────────────────────────


class TestWheelBuildConfig:
    """TS-03-4: hatch.build.targets.wheel packages includes afissues."""

    def test_hatch_build_packages(self, pyproject: dict) -> None:
        packages = pyproject["tool"]["hatch"]["build"]["targets"]["wheel"]["packages"]
        assert "afissues" in packages


# ── TS-03-5: pytest testpaths configuration ─────────────────────────


class TestPytestConfig:
    """TS-03-5: pytest configured with testpaths = ['tests']."""

    def test_testpaths_equals_tests(self, pyproject: dict) -> None:
        testpaths = pyproject["tool"]["pytest"]["ini_options"]["testpaths"]
        assert testpaths == ["tests"]
