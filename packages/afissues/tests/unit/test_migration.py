"""Tests for platform directory deletion and import migration (TS-03-23 through TS-03-30, TS-03-E7).

Verifies that agentfox/platform/ has been deleted, no agentfox.platform
imports remain anywhere, source and test files are migrated to afissues,
dependency wiring is correct, and stale imports raise ModuleNotFoundError.

Requirements: 03-REQ-7.1, 03-REQ-7.2, 03-REQ-8.1, 03-REQ-8.2, 03-REQ-8.3,
              03-REQ-8.4, 03-REQ-8.5, 03-REQ-8.6, 03-REQ-8.E1
"""

from __future__ import annotations

import glob
import os
import tomllib
from pathlib import Path

import pytest

# ── Paths ───────────────────────────────────────────────────────────
_WORKSPACE_ROOT = Path(__file__).resolve().parents[4]
_AGENTFOX_SRC = _WORKSPACE_ROOT / "packages" / "agentfox" / "agentfox"
_AGENTFOX_TESTS = _WORKSPACE_ROOT / "packages" / "agentfox" / "tests"
_PLATFORM_DIR = _AGENTFOX_SRC / "platform"


# ── TS-03-23: Platform directory no longer exists ─────────────────────


class TestPlatformDirectoryDeleted:
    """TS-03-23: packages/agentfox/agentfox/platform/ does not exist."""

    def test_platform_dir_does_not_exist(self) -> None:
        """The platform directory must be completely removed."""
        assert not os.path.exists(_PLATFORM_DIR), (
            f"Directory should not exist: {_PLATFORM_DIR}"
        )

    def test_no_py_files_remain(self) -> None:
        """No .py files remain under the old platform directory."""
        if not _PLATFORM_DIR.exists():
            return  # Already deleted — pass
        py_files = list(_PLATFORM_DIR.glob("**/*.py"))
        assert len(py_files) == 0, (
            f"Python files remain: {[f.name for f in py_files]}"
        )

    def test_no_init_py_remains(self) -> None:
        """No __init__.py or shim remains in the platform directory."""
        if not _PLATFORM_DIR.exists():
            return  # Already deleted — pass
        assert not (_PLATFORM_DIR / "__init__.py").exists(), (
            "__init__.py still exists in deleted platform directory"
        )


# ── TS-03-24: No agentfox.platform imports in workspace ──────────────


class TestNoAgentfoxPlatformImports:
    """TS-03-24: grep for agentfox.platform in packages/ returns zero matches."""

    def test_no_agentfox_platform_in_any_file(self) -> None:
        """No .py file under packages/ contains 'agentfox.platform' in imports."""
        all_py = glob.glob(
            str(_WORKSPACE_ROOT / "packages" / "**" / "*.py"), recursive=True
        )
        violations = []
        for path in all_py:
            content = Path(path).read_text()
            for i, line in enumerate(content.splitlines(), 1):
                stripped = line.strip()
                if "agentfox.platform" in stripped and (
                    stripped.startswith("from ") or stripped.startswith("import ")
                ):
                    rel = Path(path).relative_to(_WORKSPACE_ROOT)
                    violations.append(f"{rel}:{i}: {stripped}")
        assert not violations, (
            f"Found {len(violations)} stale agentfox.platform import(s):\n"
            + "\n".join(f"  - {v}" for v in violations)
        )


# ── TS-03-25: 12 agentfox source files migrated ──────────────────────


class TestSourceFileMigration:
    """TS-03-25: All agentfox source files import from afissues, not agentfox.platform."""

    def test_no_agentfox_platform_in_source(self) -> None:
        """No .py source under packages/agentfox/agentfox/ references agentfox.platform."""
        sources = glob.glob(str(_AGENTFOX_SRC / "**" / "*.py"), recursive=True)
        violations = []
        for path in sources:
            content = Path(path).read_text()
            if "agentfox.platform" in content:
                rel = Path(path).relative_to(_WORKSPACE_ROOT)
                violations.append(str(rel))
        assert not violations, (
            f"Stale agentfox.platform references in source files: {violations}"
        )


# ── TS-03-26: 44 agentfox test files migrated ────────────────────────


class TestTestFileMigration:
    """TS-03-26: All agentfox test files import from afissues, not agentfox.platform."""

    def test_no_agentfox_platform_in_tests(self) -> None:
        """No .py test under packages/agentfox/tests/ references agentfox.platform."""
        test_files = glob.glob(str(_AGENTFOX_TESTS / "**" / "*.py"), recursive=True)
        violations = []
        for path in test_files:
            content = Path(path).read_text()
            if "agentfox.platform" in content:
                rel = Path(path).relative_to(_WORKSPACE_ROOT)
                violations.append(str(rel))
        assert not violations, (
            f"Stale agentfox.platform references in test files: {violations}"
        )


# ── TS-03-27: af test_init_labels.py imports from afissues.labels ─────


class TestAfLabelTestMigration:
    """TS-03-27: test_init_labels.py imports from afissues.labels; af/pyproject.toml unchanged."""

    def test_test_init_labels_imports_afissues(self) -> None:
        """test_init_labels.py references afissues.labels."""
        test_path = _WORKSPACE_ROOT / "packages" / "af" / "tests" / "unit" / "test_init_labels.py"
        if not test_path.exists():
            pytest.skip("test_init_labels.py not found")
        content = test_path.read_text()
        assert "afissues.labels" in content, (
            "test_init_labels.py should import from afissues.labels"
        )

    def test_test_init_labels_no_agentfox_platform(self) -> None:
        """test_init_labels.py does not reference agentfox.platform."""
        test_path = _WORKSPACE_ROOT / "packages" / "af" / "tests" / "unit" / "test_init_labels.py"
        if not test_path.exists():
            pytest.skip("test_init_labels.py not found")
        content = test_path.read_text()
        assert "agentfox.platform" not in content, (
            "test_init_labels.py still references agentfox.platform"
        )

    def test_af_pyproject_no_afissues_dep(self) -> None:
        """af/pyproject.toml does not directly list afissues in dependencies."""
        toml_path = _WORKSPACE_ROOT / "packages" / "af" / "pyproject.toml"
        with open(toml_path, "rb") as f:
            toml = tomllib.load(f)
        deps = toml.get("project", {}).get("dependencies", [])
        assert not any("afissues" in dep for dep in deps), (
            "af/pyproject.toml should not directly depend on afissues"
        )


# ── TS-03-28: agentfox pyproject.toml has afissues dependency ─────────


class TestAgentfoxDependency:
    """TS-03-28: agentfox pyproject.toml declares afissues as a dependency."""

    def test_afissues_in_agentfox_deps(self) -> None:
        """afissues appears in agentfox's dependency list."""
        toml_path = _WORKSPACE_ROOT / "packages" / "agentfox" / "pyproject.toml"
        with open(toml_path, "rb") as f:
            toml = tomllib.load(f)
        deps = toml["project"]["dependencies"]
        assert any("afissues" in dep for dep in deps), (
            "afissues not found in agentfox dependencies"
        )


# ── TS-03-29: platform_factory imports from afissues ──────────────────


class TestPlatformFactoryMigration:
    """TS-03-29: platform_factory uses afissues imports, no agentfox.platform."""

    def test_no_agentfox_platform_in_platform_factory(self) -> None:
        """platform_factory.py has no agentfox.platform import statements."""
        factory_path = _AGENTFOX_SRC / "nightshift" / "platform_factory.py"
        if not factory_path.exists():
            pytest.skip("platform_factory.py not found")
        source = factory_path.read_text()
        assert "agentfox.platform" not in source, (
            "platform_factory.py still references agentfox.platform"
        )

    def test_platform_factory_imports_afissues(self) -> None:
        """platform_factory.py imports from afissues."""
        factory_path = _AGENTFOX_SRC / "nightshift" / "platform_factory.py"
        if not factory_path.exists():
            pytest.skip("platform_factory.py not found")
        source = factory_path.read_text()
        assert "afissues" in source, (
            "platform_factory.py does not import from afissues"
        )

    def test_platform_factory_module_imports(self) -> None:
        """platform_factory module loads without ImportError."""
        import agentfox.nightshift.platform_factory  # noqa: F401


# ── TS-03-30: Root pyproject.toml includes afissues test path ─────────


class TestRootTestpaths:
    """TS-03-30: Root pyproject.toml testpaths includes packages/afissues/tests."""

    def test_afissues_in_testpaths(self) -> None:
        """testpaths in root pyproject.toml includes afissues tests."""
        toml_path = _WORKSPACE_ROOT / "pyproject.toml"
        with open(toml_path, "rb") as f:
            toml = tomllib.load(f)
        testpaths = toml["tool"]["pytest"]["ini_options"]["testpaths"]
        assert any("afissues" in p for p in testpaths), (
            f"packages/afissues/tests not in testpaths: {testpaths}"
        )

    def test_pytest_discovers_afissues_tests(self) -> None:
        """Verify afissues test files can be discovered by checking path existence."""
        test_dir = _WORKSPACE_ROOT / "packages" / "afissues" / "tests"
        assert test_dir.exists(), "packages/afissues/tests/ directory does not exist"
        test_files = list(test_dir.rglob("test_*.py"))
        assert len(test_files) > 0, "No test files found under packages/afissues/tests/"


# ── TS-03-E7: Stale import raises ModuleNotFoundError ─────────────────


class TestStaleImportError:
    """TS-03-E7: Stale agentfox.platform import raises ModuleNotFoundError."""

    def test_stale_import_raises_module_not_found(self) -> None:
        """A stale 'from agentfox.platform.protocol import ...' raises ModuleNotFoundError."""
        if _PLATFORM_DIR.exists():
            pytest.skip("Platform directory not yet deleted")

        with pytest.raises(ModuleNotFoundError):
            exec("from agentfox.platform.protocol import PlatformProtocol")  # noqa: S102

    def test_stale_import_error_mentions_agentfox(self) -> None:
        """ModuleNotFoundError message references agentfox."""
        if _PLATFORM_DIR.exists():
            pytest.skip("Platform directory not yet deleted")

        with pytest.raises(ModuleNotFoundError, match=r"agentfox"):
            exec("from agentfox.platform.labels import LabelSpec")  # noqa: S102
