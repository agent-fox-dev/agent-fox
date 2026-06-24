"""Tests for test migration from af to nightshift package.

Test Spec: TS-07-34, TS-07-35, TS-07-36, TS-07-37, TS-07-38, TS-07-E9, TS-07-P4
Requirements: 07-REQ-8.1, 07-REQ-8.2, 07-REQ-8.3, 07-REQ-8.4, 07-REQ-8.5
"""

from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

import pytest

# Test files that should be migrated from af to nightshift.
# Note: test_code_dry_run.py is EXCLUDED per reviewer finding -- it tests
# the af CLI's --dry-run flag, not nightshift-specific behavior.
MIGRATED_TEST_FILES = [
    "test_spec04_req3.py",
    "test_spec04_smoke.py",
    "test_spec04_properties.py",
]

NIGHTSHIFT_TESTS_DIR = Path("packages/nightshift/tests")
AF_TESTS_DIR = Path("packages/af/tests")


class TestMigratedTestFilesExist:
    """TS-07-34: Migrated test files are present in nightshift tests.

    Requirements: 07-REQ-8.1
    """

    @pytest.mark.parametrize("test_file", MIGRATED_TEST_FILES)
    def test_migrated_file_exists(self, test_file: str) -> None:
        """Each migrated test file exists somewhere under packages/nightshift/tests/."""
        found = False
        for root, _dirs, files in os.walk(NIGHTSHIFT_TESTS_DIR):
            if test_file in files:
                found = True
                break
        assert found, (
            f"{test_file} not found under {NIGHTSHIFT_TESTS_DIR}"
        )


class TestMigratedTestsPass:
    """TS-07-35: All migrated nightshift tests pass.

    Requirements: 07-REQ-8.2
    """

    @pytest.mark.slow
    def test_nightshift_tests_pass(self) -> None:
        """pytest packages/nightshift/tests/ exits 0 with all tests passing."""
        result = subprocess.run(
            [sys.executable, "-m", "pytest", "packages/nightshift/tests/",
             "-q", "--tb=short"],
            capture_output=True, text=True, timeout=120,
        )
        assert result.returncode == 0, (
            f"Nightshift tests must all pass. Exit code: {result.returncode}\n"
            f"stdout:\n{result.stdout}\n"
            f"stderr:\n{result.stderr}"
        )


class TestAfTestsStillPass:
    """TS-07-36: af tests continue to pass after migration.

    Requirements: 07-REQ-8.3
    """

    @pytest.mark.slow
    def test_af_tests_pass(self) -> None:
        """pytest packages/af/tests/ exits 0 with no regressions."""
        result = subprocess.run(
            [sys.executable, "-m", "pytest", "packages/af/tests/",
             "-q", "--tb=short"],
            capture_output=True, text=True, timeout=120,
        )
        assert result.returncode == 0, (
            f"af tests must all pass after migration. Exit code: {result.returncode}\n"
            f"stdout:\n{result.stdout}\n"
            f"stderr:\n{result.stderr}"
        )


class TestConftestExists:
    """TS-07-37: conftest.py exists for nightshift tests.

    Requirements: 07-REQ-8.4
    """

    def test_nightshift_conftest_exists(self) -> None:
        assert (NIGHTSHIFT_TESTS_DIR / "conftest.py").exists(), (
            "packages/nightshift/tests/conftest.py must exist with shared fixtures"
        )

    def test_af_conftest_intact(self) -> None:
        """af conftest.py was not removed during migration."""
        assert (AF_TESTS_DIR / "conftest.py").exists(), (
            "packages/af/tests/conftest.py must remain intact"
        )


class TestRemovalTestInAfSuite:
    """TS-07-38: af suite includes a test asserting night-shift removal.

    Requirements: 07-REQ-8.5
    """

    def test_removal_test_file_exists(self) -> None:
        """A test file in af tests validates night-shift removal."""
        removal_test = AF_TESTS_DIR / "test_nightshift_removal.py"
        assert removal_test.exists(), (
            "packages/af/tests/test_nightshift_removal.py must exist"
        )


class TestCodeDryRunRemainsInAf:
    """TS-07-E9: test_code_dry_run.py stays in af (not migrated to nightshift).

    Requirements: 07-REQ-8.1, 07-REQ-8.E1
    """

    def test_code_dry_run_not_in_nightshift(self) -> None:
        """test_code_dry_run.py should NOT be in nightshift tests."""
        for root, _dirs, files in os.walk(NIGHTSHIFT_TESTS_DIR):
            assert "test_code_dry_run.py" not in files, (
                "test_code_dry_run.py tests af CLI --dry-run, not nightshift"
            )

    def test_code_dry_run_still_in_af(self) -> None:
        """test_code_dry_run.py should remain in af tests."""
        found = False
        for root, _dirs, files in os.walk(AF_TESTS_DIR):
            if "test_code_dry_run.py" in files:
                found = True
                break
        assert found, "test_code_dry_run.py should remain in af tests"


class TestAfTestsRetainContent:
    """TS-07-P4 / TS-07-E9: af test files retain content after migration.

    Requirements: 07-REQ-8.3, 07-REQ-8.E1
    """

    def test_af_unit_tests_exist(self) -> None:
        """af unit test directory still has test files."""
        unit_dir = AF_TESTS_DIR / "unit"
        if not unit_dir.exists():
            pytest.skip("af/tests/unit/ not found")
        test_files = list(unit_dir.glob("test_*.py"))
        assert len(test_files) > 0, "af unit tests should not be empty"

    def test_af_has_test_files(self) -> None:
        """af tests directory is not empty after migration."""
        all_tests = []
        for root, _dirs, files in os.walk(AF_TESTS_DIR):
            all_tests.extend(
                f for f in files if f.startswith("test_") and f.endswith(".py")
            )
        assert len(all_tests) > 5, (
            f"af tests should have many test files, found only {len(all_tests)}"
        )
