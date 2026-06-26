"""Smoke tests for nightshift CLI (migrated from af).

Test Spec: TS-07-SMOKE-4, TS-07-SMOKE-5, TS-07-11
Requirements: 07-REQ-3.3, 07-REQ-3.4, 07-REQ-4.4
Migrated from: packages/af/tests/integration/test_spec04_smoke.py (07-REQ-8.1)

Note: Daemon-invoking smoke tests (SMOKE-1, SMOKE-2, SMOKE-3) are covered in
test_cli_behavior.py since they require the full daemon mock infrastructure.
"""

from __future__ import annotations

import subprocess
import sys

import pytest


class TestSmoke4VersionOutput:
    """TS-07-SMOKE-4: nightshift --version prints 4.0.0-rc5 and exits 0."""

    def test_version_output(self) -> None:
        """nightshift --version outputs version string and exits 0."""
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--version"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert result.returncode == 0
        assert "4.0.0" in result.stdout


class TestSmoke5CIDiscovery:
    """TS-07-SMOKE-5: make check discovers nightshift tests.

    Requirements: 07-REQ-4.4
    """

    def test_pytest_collects_nightshift_tests(self) -> None:
        """pytest --collect-only packages/nightshift/tests/ discovers tests."""
        result = subprocess.run(
            [sys.executable, "-m", "pytest", "--collect-only", "-q", "packages/nightshift/tests/"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert result.returncode == 0
        assert "test" in result.stdout.lower()


class TestSubcommandContracts:
    """TS-07-11: nightshift --help shows all expected global options.

    Requirements: 07-REQ-3.3
    """

    def test_help_shows_global_options(self, cli_runner) -> None:
        """nightshift --help contains --json, --verbose, --quiet, --version (no --trace)."""
        from nightshift.app import main

        result = cli_runner.invoke(main, ["--help"])
        assert result.exit_code == 0
        help_text = result.output
        assert "--json" in help_text
        assert "--no-json" in help_text
        assert "--verbose" in help_text
        assert "--quiet" in help_text
        assert "--trace" not in help_text, "--trace must be absent after removal"
        assert "--version" in help_text


class TestAgentFoxGroupSmoke:
    """Verify nightshift main is wired via AgentFoxGroup.

    Requirements: 07-REQ-3.11
    """

    def test_main_uses_agentfox_group(self) -> None:
        """nightshift main Click group is AgentFoxGroup instance."""
        from agentfox.io import AgentFoxGroup
        from nightshift.app import main

        assert isinstance(main, AgentFoxGroup)


class TestHelpOutputSubprocess:
    """Verify --help works as subprocess for nightshift.

    Requirements: 07-REQ-3.3
    """

    @pytest.mark.parametrize(
        "flag",
        ["--json", "--no-json", "--verbose", "--quiet", "--version"],
    )
    def test_help_lists_flag(self, flag: str) -> None:
        """nightshift --help output contains expected flag."""
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert result.returncode == 0
        assert flag in result.stdout
