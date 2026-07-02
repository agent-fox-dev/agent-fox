"""Tests for nightshift workspace integration.

Test Spec: TS-07-21, TS-07-22, TS-07-23, TS-07-24, TS-07-E6
Requirements: 07-REQ-4.1, 07-REQ-4.2, 07-REQ-4.3, 07-REQ-4.4, 07-REQ-4.E1
"""

from __future__ import annotations

import fnmatch
import subprocess
import sys
import tomllib
from importlib.metadata import version as get_version
from pathlib import Path

import pytest


def _load_root_toml() -> dict:
    """Load the root pyproject.toml."""
    path = Path("pyproject.toml")
    with path.open("rb") as f:
        return tomllib.load(f)


class TestWorkspaceMember:
    """TS-07-21: nightshift is a workspace member.

    Requirements: 07-REQ-4.1
    """

    def test_nightshift_in_workspace_members(self) -> None:
        """packages/nightshift is covered by a workspace member glob."""
        config = _load_root_toml()
        members = config["tool"]["uv"]["workspace"]["members"]
        matched = any(fnmatch.fnmatch("packages/nightshift", pattern) for pattern in members)
        assert matched, f"packages/nightshift not matched by any workspace member pattern: {members}"


class TestWorkspaceDependency:
    """TS-07-22: nightshift is a root dependency with workspace source.

    Requirements: 07-REQ-4.2
    """

    def test_nightshift_in_root_dependencies(self) -> None:
        config = _load_root_toml()
        deps = config["project"]["dependencies"]
        _ns_ver = get_version("nightshift")
        assert any("nightshift" in d and _ns_ver in d for d in deps), (
            f"nightshift>={_ns_ver} not found in root dependencies: {deps}"
        )

    def test_nightshift_workspace_source(self) -> None:
        config = _load_root_toml()
        sources = config["tool"]["uv"]["sources"]
        assert sources.get("nightshift", {}).get("workspace") is True, "nightshift must be a workspace source"


class TestTestpaths:
    """TS-07-23: nightshift tests in root testpaths.

    Requirements: 07-REQ-4.3
    """

    def test_nightshift_tests_in_testpaths(self) -> None:
        config = _load_root_toml()
        testpaths = config["tool"]["pytest"]["ini_options"]["testpaths"]
        matched = any("nightshift" in tp for tp in testpaths)
        assert matched, f"packages/nightshift/tests not in testpaths: {testpaths}"


class TestMakeCheckDiscovery:
    """TS-07-24: make check discovers nightshift tests.

    Requirements: 07-REQ-4.4
    """

    def test_testpaths_entry_is_valid_directory(self) -> None:
        """The nightshift testpaths entry points to an existing directory."""
        config = _load_root_toml()
        testpaths = config["tool"]["pytest"]["ini_options"]["testpaths"]
        ns_paths = [tp for tp in testpaths if "nightshift" in tp]
        assert len(ns_paths) > 0
        for tp in ns_paths:
            assert Path(tp).exists(), f"testpath {tp} does not exist"


class TestTestpathsOmissionGuard:
    """TS-07-E6: Omitting nightshift from testpaths causes zero tests collected.

    Requirements: 07-REQ-4.E1

    Verifies that running pytest against only other testpaths (without
    nightshift) does NOT discover nightshift tests -- proving the testpaths
    entry is necessary.
    """

    def test_guard_nightshift_in_testpaths(self) -> None:
        """If nightshift tests are missing from testpaths, CI would miss them."""
        config = _load_root_toml()
        testpaths = config["tool"]["pytest"]["ini_options"]["testpaths"]
        assert any("nightshift" in tp for tp in testpaths), (
            "nightshift tests missing from testpaths -- CI would not run them"
        )

    @pytest.mark.timeout(60)
    def test_omission_means_zero_nightshift_tests(self) -> None:
        """Without nightshift in testpaths, pytest collects no nightshift tests.

        Simulates the omission by running pytest --collect-only on only the
        non-nightshift testpaths and verifying nightshift tests are absent.
        """
        config = _load_root_toml()
        testpaths = config["tool"]["pytest"]["ini_options"]["testpaths"]
        other_paths = [tp for tp in testpaths if "nightshift" not in tp]
        if not other_paths:
            # All testpaths are nightshift -- cannot simulate omission
            return

        result = subprocess.run(
            [sys.executable, "-m", "pytest", "--collect-only", "-q", *other_paths],
            capture_output=True,
            text=True,
            timeout=60,
        )
        # Check that no test files from packages/nightshift/tests/ are collected.
        # We check for the path prefix, not the bare word "nightshift", because
        # other packages may have tests whose names or IDs reference nightshift.
        assert "packages/nightshift/tests/" not in result.stdout, (
            "Nightshift test files should NOT be discovered when nightshift is omitted from testpaths"
        )
