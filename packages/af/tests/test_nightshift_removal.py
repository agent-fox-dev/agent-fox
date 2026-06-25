"""Tests for night-shift removal from the af CLI.

Test Spec: TS-07-1, TS-07-2, TS-07-3, TS-07-E1, TS-07-38, TS-07-SMOKE-6
Requirements: 07-REQ-1.1, 07-REQ-1.2, 07-REQ-1.3, 07-REQ-1.E1, 07-REQ-8.5
"""

from __future__ import annotations

import os

from click.testing import CliRunner


class TestNightShiftNotRegistered:
    """TS-07-1: af CLI does not register a night-shift subcommand.

    Requirements: 07-REQ-1.1
    """

    def test_night_shift_not_in_cli_commands(self) -> None:
        """The af Click group commands dict does not contain 'night-shift'."""
        from af.app import main

        assert "night-shift" not in main.commands, "night-shift must be removed from af CLI commands"


class TestNightShiftFileDeleted:
    """TS-07-2: packages/af/af/nightshift.py does not exist.

    Requirements: 07-REQ-1.2
    """

    def test_nightshift_py_does_not_exist(self) -> None:
        """The file packages/af/af/nightshift.py must be deleted."""
        assert not os.path.exists("packages/af/af/nightshift.py"), (
            "packages/af/af/nightshift.py must be deleted with no deprecation stub"
        )


class TestNightShiftGuard:
    """TS-07-E1: Guard test -- if nightshift.py still exists, CI fails.

    Requirements: 07-REQ-1.E1
    """

    def test_guard_nightshift_file_absent(self) -> None:
        """Guard: nightshift.py must be absent for CI to pass."""
        assert not os.path.exists("packages/af/af/nightshift.py"), (
            "nightshift.py must be deleted; this guard test failing means CI would catch the incomplete removal"
        )


class TestAfNightShiftUnrecognized:
    """TS-07-3 / TS-07-SMOKE-6: af night-shift returns unrecognized-command error.

    Requirements: 07-REQ-1.3, 07-REQ-8.5
    """

    def test_af_night_shift_returns_nonzero(self, cli_runner: CliRunner) -> None:
        """Invoking 'af night-shift' returns a non-zero exit code."""
        from af.app import main

        result = cli_runner.invoke(main, ["night-shift"])
        assert result.exit_code != 0

    def test_af_night_shift_error_message(self, cli_runner: CliRunner) -> None:
        """Invoking 'af night-shift' produces an unrecognized-command error."""
        from af.app import main

        result = cli_runner.invoke(main, ["night-shift"])
        output = (result.output or "").lower()
        assert "no such command" in output or "error" in output
