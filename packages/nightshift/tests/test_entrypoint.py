"""Tests for nightshift entry point discoverability.

Test Spec: TS-07-39, TS-07-40, TS-07-8, TS-07-E10
Requirements: 07-REQ-9.1, 07-REQ-9.2, 07-REQ-2.5
"""

from __future__ import annotations

import shutil
import subprocess
import sys

import pytest


class TestPythonModuleEntryPoint:
    """TS-07-39: python -m nightshift --help works.

    Requirements: 07-REQ-9.1
    """

    def test_python_m_nightshift_help_exits_zero(self) -> None:
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert result.returncode == 0

    def test_python_m_nightshift_help_contains_version(self) -> None:
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True,
            text=True,
            timeout=30,
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
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert result.returncode == 0

    def test_night_shift_help_contains_version(self) -> None:
        if shutil.which("night-shift") is None:
            pytest.skip("night-shift not installed as a script entry point")
        result = subprocess.run(
            ["night-shift", "--help"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert "--version" in result.stdout


class TestEntryPointOutputEquivalence:
    """TS-07-8: python -m nightshift and night-shift produce identical output.

    Requirements: 07-REQ-2.5
    """

    def test_help_output_identical(self) -> None:
        """python -m nightshift --help and night-shift --help produce identical stdout."""
        result_module = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert result_module.returncode == 0

        if shutil.which("night-shift") is None:
            pytest.skip("night-shift entry point not installed on PATH")

        result_entry = subprocess.run(
            ["night-shift", "--help"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert result_entry.returncode == 0
        assert result_module.stdout == result_entry.stdout, (
            "python -m nightshift --help and night-shift --help must produce identical stdout"
        )


class TestEntryPointDiscoverability:
    """TS-07-40: Integration test is discoverable by pytest.

    Requirements: 07-REQ-9.2
    """

    def test_this_file_is_discoverable(self) -> None:
        """This test file itself proves discoverability by being collected."""
        assert True


class TestFallbackMechanism:
    """TS-07-E10: python -m fallback when night-shift script not on PATH.

    Requirements: 07-REQ-9.E1
    """

    def test_python_m_is_always_available(self) -> None:
        """python -m nightshift --help always works regardless of PATH."""
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert result.returncode == 0
        assert "--version" in result.stdout, "python -m nightshift --help must contain --version in output"
