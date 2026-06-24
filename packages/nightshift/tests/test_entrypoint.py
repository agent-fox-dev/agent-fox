"""Tests for nightshift entry point discoverability.

Test Spec: TS-07-39, TS-07-40, TS-07-8
Requirements: 07-REQ-9.1, 07-REQ-9.2, 07-REQ-2.5
"""

from __future__ import annotations

import shutil
import subprocess
import sys

import pytest


class TestPythonModuleEntryPoint:
    """TS-07-39 / TS-07-8: python -m nightshift --help works.

    Requirements: 07-REQ-9.1, 07-REQ-2.5
    """

    def test_python_m_nightshift_help_exits_zero(self) -> None:
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True, text=True, timeout=30,
        )
        assert result.returncode == 0

    def test_python_m_nightshift_help_contains_version(self) -> None:
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True, text=True, timeout=30,
        )
        assert "--version" in result.stdout


class TestNightShiftScriptEntryPoint:
    """TS-07-39: night-shift --help works when installed.

    Requirements: 07-REQ-9.1
    """

    def test_night_shift_help_exits_zero(self) -> None:
        if shutil.which("night-shift") is None:
            pytest.skip("night-shift not installed as a script entry point")
        result = subprocess.run(
            ["night-shift", "--help"],
            capture_output=True, text=True, timeout=30,
        )
        assert result.returncode == 0

    def test_night_shift_help_contains_version(self) -> None:
        if shutil.which("night-shift") is None:
            pytest.skip("night-shift not installed as a script entry point")
        result = subprocess.run(
            ["night-shift", "--help"],
            capture_output=True, text=True, timeout=30,
        )
        assert "--version" in result.stdout


class TestEntryPointDiscoverability:
    """TS-07-40: Integration test is discoverable by pytest.

    Requirements: 07-REQ-9.2
    """

    def test_this_file_is_discoverable(self) -> None:
        """This test file itself proves discoverability by being collected."""
        assert True


class TestFallbackMechanism:
    """Entry point fallback: python -m works even if script not on PATH.

    Requirements: 07-REQ-2.5
    """

    def test_python_m_is_always_available(self) -> None:
        """python -m nightshift --help always works regardless of PATH."""
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True, text=True, timeout=30,
        )
        assert result.returncode == 0
        has_relevant = (
            "night-shift" in result.stdout.lower()
            or "nightshift" in result.stdout.lower()
            or "--help" in result.stdout
        )
        assert has_relevant
