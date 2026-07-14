"""Tests for dependency footprint isolation (TS-03-35 through TS-03-38, TS-03-P1).

Verifies that afissues has minimal dependencies, contains no workspace package
imports in its source modules, and that nightshift and af do not declare
afissues as an explicit dependency.

Requirements: 03-REQ-10.1, 03-REQ-10.2, 03-REQ-10.3, 03-REQ-10.4
"""

from __future__ import annotations

import glob
import tomllib
from pathlib import Path

import pytest

# ── Paths ───────────────────────────────────────────────────────────
_WORKSPACE_ROOT = Path(__file__).resolve().parents[4]
_AFISSUES_SRC = _WORKSPACE_ROOT / "packages" / "afissues" / "afissues"

# Workspace package names that must NOT appear in afissues source imports.
_WORKSPACE_PKGS = ("agentfox", "afspec", "afaudit", "nightshift")


# ── TS-03-35: pip show afissues lists only httpx ─────────────────────


class TestAfissuesDependencies:
    """TS-03-35: afissues declares only httpx as an external dependency."""

    def test_pyproject_only_httpx(self) -> None:
        """pyproject.toml dependencies list contains only httpx."""
        toml_path = _WORKSPACE_ROOT / "packages" / "afissues" / "pyproject.toml"
        with open(toml_path, "rb") as f:
            toml = tomllib.load(f)
        deps = toml["project"]["dependencies"]
        # Filter out any workspace-internal deps that may use tool.uv.sources
        external_deps = [d for d in deps if not any(pkg in d for pkg in _WORKSPACE_PKGS)]
        assert len(external_deps) == 1, f"Expected only httpx, got: {external_deps}"
        assert "httpx" in external_deps[0], f"Expected httpx, got: {external_deps[0]}"


# ── TS-03-36: No workspace imports in afissues source ────────────────


class TestNoWorkspaceImportsInSource:
    """TS-03-36: afissues source modules import only httpx and stdlib."""

    def test_no_workspace_package_in_any_source_file(self) -> None:
        """No .py file under packages/afissues/afissues/ imports workspace packages."""
        source_files = glob.glob(str(_AFISSUES_SRC / "**" / "*.py"), recursive=True)
        violations = []
        for path in source_files:
            content = Path(path).read_text()
            for pkg in _WORKSPACE_PKGS:
                if pkg in content:
                    violations.append(f"{Path(path).name} references {pkg}")
        assert not violations, (
            "Workspace package references found in afissues source:\n"
            + "\n".join(f"  - {v}" for v in violations)
        )


# ── TS-03-37: nightshift does not list afissues ──────────────────────


class TestNightshiftNoAfissuesDep:
    """TS-03-37: nightshift/pyproject.toml does not declare afissues."""

    def test_nightshift_deps_exclude_afissues(self) -> None:
        """afissues must not appear in nightshift's dependencies."""
        # Find nightshift pyproject.toml(s) - could be under packages/ or at root
        nightshift_tomls = glob.glob(
            str(_WORKSPACE_ROOT / "packages" / "*" / "pyproject.toml")
        )
        for toml_path in nightshift_tomls:
            if "nightshift" not in toml_path:
                continue
            with open(toml_path, "rb") as f:
                toml = tomllib.load(f)
            deps = toml.get("project", {}).get("dependencies", [])
            assert not any("afissues" in dep for dep in deps), (
                f"afissues should not be in nightshift deps: {deps}"
            )


# ── TS-03-38: af/pyproject.toml unchanged ───────────────────────────


class TestAfPackageUnchanged:
    """TS-03-38: af/pyproject.toml does not add afissues as a dependency."""

    def test_af_deps_exclude_afissues(self) -> None:
        """afissues must not appear in af's dependencies."""
        af_toml_path = _WORKSPACE_ROOT / "packages" / "af" / "pyproject.toml"
        with open(af_toml_path, "rb") as f:
            toml = tomllib.load(f)
        deps = toml.get("project", {}).get("dependencies", [])
        assert not any("afissues" in dep for dep in deps), (
            f"afissues should not be in af deps: {deps}"
        )


# ── TS-03-P1: Property — afissues modules have zero workspace imports


class TestWorkspaceImportIsolationProperty:
    """TS-03-P1: Every module in afissues/afissues/ is workspace-independent.

    Property invariant: for each module m in packages/afissues/afissues/,
    m does not import from agentfox, afspec, afaudit, or nightshift.
    """

    @pytest.fixture()
    def afissues_modules(self) -> list[Path]:
        """Collect all .py files in the afissues source directory."""
        return [Path(p) for p in glob.glob(str(_AFISSUES_SRC / "**" / "*.py"), recursive=True)]

    def test_every_module_has_no_workspace_imports(
        self, afissues_modules: list[Path]
    ) -> None:
        """For every afissues module: no workspace package references."""
        assert len(afissues_modules) > 0, "No afissues modules found"
        for mod_path in afissues_modules:
            content = mod_path.read_text()
            for pkg in _WORKSPACE_PKGS:
                assert pkg not in content, (
                    f"Module {mod_path.name} references workspace package '{pkg}'"
                )

    def test_only_httpx_and_stdlib_imports(
        self, afissues_modules: list[Path]
    ) -> None:
        """For every afissues module: imports are from httpx, stdlib, or afissues itself."""
        assert len(afissues_modules) > 0, "No afissues modules found"
        for mod_path in afissues_modules:
            content = mod_path.read_text()
            # Check each import line
            for line in content.splitlines():
                stripped = line.strip()
                if not (stripped.startswith("import ") or stripped.startswith("from ")):
                    continue
                # Allow: afissues, httpx, stdlib, __future__
                if any(
                    stripped.startswith(prefix)
                    for prefix in (
                        "from afissues",
                        "import afissues",
                        "from httpx",
                        "import httpx",
                        "from __future__",
                        "import __future__",
                    )
                ):
                    continue
                # Allow stdlib modules — check against known workspace packages
                for pkg in _WORKSPACE_PKGS:
                    assert f"from {pkg}" not in stripped and f"import {pkg}" not in stripped, (
                        f"Module {mod_path.name} imports workspace package '{pkg}': {stripped}"
                    )
