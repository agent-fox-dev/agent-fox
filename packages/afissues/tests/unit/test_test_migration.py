"""Tests for test migration structure (TS-03-31 through TS-03-34).

Verifies that platform test files are relocated to packages/afissues/tests/,
conftest.py references only afissues imports, property tests are relocated,
and the old agentfox platform test directory is deleted.

Requirements: 03-REQ-9.1, 03-REQ-9.2, 03-REQ-9.3, 03-REQ-9.4

Drift errata:
  - 03-REQ-9.1 / TS-03-31: The spec claims "10 unit test files".  The original
    directory had 9 files (see errata §3).  Specs 04 (GitLab) and 05 (Gitea)
    subsequently added test_gitlab.py, test_gitlab_group3.py, and test_gitea.py,
    bringing the current total to 12.  Tests below use >= 9 as the minimum
    bound for core platform tests.
  - 03-REQ-9.2: conftest.py fixture ``platform_config`` imports from
    ``agentfox.core.config`` (not afissues) and is unused by all test files.
    It can be dropped or left with its original import during relocation.
"""

from __future__ import annotations

import glob
import os
from pathlib import Path

import pytest

# ── Paths ───────────────────────────────────────────────────────────
_WORKSPACE_ROOT = Path(__file__).resolve().parents[4]
_AFISSUES_TESTS = Path(__file__).resolve().parents[1]  # packages/afissues/tests/
_AFISSUES_UNIT = _AFISSUES_TESTS / "unit"
_AFISSUES_PROPERTY = _AFISSUES_TESTS / "property"
_OLD_PLATFORM_DIR = _WORKSPACE_ROOT / "packages" / "agentfox" / "tests" / "unit" / "platform"


# ── TS-03-31: Test directory structure ───────────────────────────────


class TestMigrationDirectoryStructure:
    """TS-03-31: packages/afissues/tests/ has the expected file layout.

    Drift: spec says 10 unit test files; actual count is >= 9 (core platform
    tests) and currently 12 after specs 04/05 additions.
    """

    def test_unit_test_files_exist(self) -> None:
        """At least 9 core platform unit test files are present."""
        unit_tests = glob.glob(str(_AFISSUES_UNIT / "test_*.py"))
        # Exclude spec-03 test files (test_scaffold, test_protocol, etc.) from
        # the count — we only care about the relocated platform tests here.
        relocated = [
            f
            for f in unit_tests
            if Path(f).name
            not in {
                "test_scaffold.py",
                "test_protocol.py",
                "test_github.py",
                "test_labels.py",
                "test_errors.py",
                "test_init_reexports.py",
                "test_migration.py",
                "test_test_migration.py",
                "test_footprint.py",
                "test_docs.py",
                "test_validation.py",
            }
        ]
        assert len(relocated) >= 9, (
            f"Expected >= 9 relocated platform test files, got {len(relocated)}: "
            f"{[Path(f).name for f in relocated]}"
        )

    def test_conftest_exists(self) -> None:
        """packages/afissues/tests/unit/conftest.py exists."""
        assert (_AFISSUES_UNIT / "conftest.py").exists(), (
            "conftest.py must be relocated to packages/afissues/tests/unit/"
        )

    def test_property_test_exists(self) -> None:
        """packages/afissues/tests/property/test_overhaul_props.py exists."""
        assert (_AFISSUES_PROPERTY / "test_overhaul_props.py").exists(), (
            "test_overhaul_props.py must be relocated to packages/afissues/tests/property/"
        )


# ── TS-03-32: conftest.py references only afissues imports ──────────


class TestConftestImports:
    """TS-03-32: Relocated conftest.py uses only afissues imports."""

    def test_no_agentfox_platform_in_conftest(self) -> None:
        """conftest.py must not reference agentfox.platform."""
        conftest_path = _AFISSUES_UNIT / "conftest.py"
        if not conftest_path.exists():
            pytest.skip("conftest.py not yet relocated")
        content = conftest_path.read_text()
        assert "agentfox.platform" not in content, (
            "conftest.py still references agentfox.platform"
        )

    def test_conftest_references_afissues_or_is_empty(self) -> None:
        """conftest.py references afissues or is minimal (empty/no imports)."""
        conftest_path = _AFISSUES_UNIT / "conftest.py"
        if not conftest_path.exists():
            pytest.skip("conftest.py not yet relocated")
        content = conftest_path.read_text()
        # The fixture imports from agentfox.core.config which stays in agentfox.
        # Either the file imports afissues, or the unused fixture was removed.
        has_afissues_ref = "afissues" in content
        is_minimal = content.strip() == "" or "import" not in content
        assert has_afissues_ref or is_minimal, (
            "conftest.py should reference afissues or be minimal after relocation"
        )


# ── TS-03-33: test_overhaul_props.py imports from afissues ──────────


class TestPropertyTestImports:
    """TS-03-33: Relocated test_overhaul_props.py uses afissues imports."""

    def test_no_agentfox_platform_in_property_test(self) -> None:
        """Property test must not reference agentfox.platform."""
        prop_path = _AFISSUES_PROPERTY / "test_overhaul_props.py"
        if not prop_path.exists():
            pytest.skip("test_overhaul_props.py not yet relocated")
        content = prop_path.read_text()
        assert "agentfox.platform" not in content, (
            "test_overhaul_props.py still references agentfox.platform"
        )

    def test_property_test_imports_afissues(self) -> None:
        """Property test imports from afissues."""
        prop_path = _AFISSUES_PROPERTY / "test_overhaul_props.py"
        if not prop_path.exists():
            pytest.skip("test_overhaul_props.py not yet relocated")
        content = prop_path.read_text()
        assert "afissues" in content, (
            "test_overhaul_props.py must import from afissues"
        )


# ── TS-03-34: Old platform test directory deleted ────────────────────


class TestOldPlatformDirDeleted:
    """TS-03-34: packages/agentfox/tests/unit/platform/ is removed."""

    def test_old_platform_dir_does_not_exist(self) -> None:
        """The old platform test directory must be deleted after relocation."""
        assert not os.path.exists(_OLD_PLATFORM_DIR), (
            f"Old platform test directory still exists: {_OLD_PLATFORM_DIR}"
        )

    def test_no_test_files_remain(self) -> None:
        """No test files remain under the old platform directory."""
        if not os.path.exists(_OLD_PLATFORM_DIR):
            return  # Directory deleted — test passes
        test_files = glob.glob(str(_OLD_PLATFORM_DIR / "test_*.py"))
        assert len(test_files) == 0, (
            f"Test files remain in deleted directory: {[Path(f).name for f in test_files]}"
        )

    def test_no_conftest_remains(self) -> None:
        """No conftest.py remains under the old platform directory."""
        if not os.path.exists(_OLD_PLATFORM_DIR):
            return  # Directory deleted — test passes
        assert not (Path(_OLD_PLATFORM_DIR) / "conftest.py").exists(), (
            "conftest.py remains in deleted platform directory"
        )
