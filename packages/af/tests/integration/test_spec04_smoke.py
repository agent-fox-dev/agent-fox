"""Smoke tests for af agentic CLI migration.

Test Spec: TS-04-SMOKE-1..5, TS-04-27, TS-04-28
Requirements: 04-REQ-1.3, 04-REQ-2.5, 04-REQ-3.6, 04-REQ-5.1, 04-REQ-6.2,
              04-REQ-7.1, 04-REQ-7.2
"""

from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path

import pytest

_AF_PACKAGE_DIR = Path(__file__).resolve().parents[2] / "af"

_SUBCOMMANDS = [
    "code",
    "plan",
    "standup",
    "init",
    "night-shift",
    "reset",
    "insights",
]


# --- TS-04-27: Full test suite collectible ---


class TestFullSuiteCollectible:
    """TS-04-27: All spec-04 test files are collectible by pytest."""

    def test_spec04_tests_collect_without_errors(self) -> None:
        """pytest --collect-only on spec04 test files succeeds."""
        tests_dir = Path(__file__).resolve().parents[1]
        spec04_files = sorted(tests_dir.rglob("test_spec04_*.py"))
        assert len(spec04_files) >= 5, (
            f"Expected at least 5 spec04 test files, found {len(spec04_files)}"
        )
        result = subprocess.run(
            ["uv", "run", "pytest", "--collect-only", "-q"]
            + [str(f) for f in spec04_files],
            capture_output=True,
            text=True,
            cwd=str(tests_dir.parent),
        )
        assert result.returncode == 0, (
            f"pytest collect failed:\n{result.stdout}\n{result.stderr}"
        )


# --- TS-04-28: Subcommand contracts ---


class TestSubcommandContracts:
    """TS-04-28: All af subcommands --help returns exit 0."""

    @pytest.mark.parametrize("cmd", _SUBCOMMANDS)
    def test_help_exits_zero(self, cli_runner, cmd: str) -> None:
        """af <cmd> --help exits with code 0."""
        from af.app import main

        result = cli_runner.invoke(main, [cmd, "--help"])
        assert result.exit_code == 0, (
            f"{cmd} --help failed with exit code {result.exit_code}"
        )


# --- Smoke Tests ---


@pytest.mark.xfail(reason="JSONL streaming not yet wired in af code")
class TestSmoke1CodeJsonlStreaming:
    """TS-04-SMOKE-1: af code --json emits JSONL progress + JSON result."""

    def test_code_json_streaming_path(self, cli_runner) -> None:
        """af code --json: stderr has JSONL events, stdout has JSON result."""
        from af.app import main

        result = cli_runner.invoke(main, ["code", "--json"])
        assert result.exit_code == 0
        # stdout is valid JSON
        final = json.loads(result.output)
        assert isinstance(final, dict)


@pytest.mark.xfail(reason="af standup --json not yet migrated to OutputManager")
class TestSmoke2StandupJson:
    """TS-04-SMOKE-2: af standup --json emits structured JSON."""

    def test_standup_json_output(self, cli_runner) -> None:
        """af standup --json returns single JSON object on stdout."""
        from af.app import main

        result = cli_runner.invoke(main, ["standup", "--json"])
        assert result.exit_code == 0
        obj = json.loads(result.output)
        assert isinstance(obj, dict)


@pytest.mark.xfail(reason="JSON help renderer not yet implemented")
class TestSmoke3JsonHelp:
    """TS-04-SMOKE-3: af night-shift --json --help returns JSON help."""

    def test_json_help_for_night_shift(self, cli_runner) -> None:
        """af night-shift --json --help: JSON with command metadata."""
        from af.app import main

        result = cli_runner.invoke(main, ["night-shift", "--json", "--help"])
        assert result.exit_code == 0
        obj = json.loads(result.output)
        assert "name" in obj
        assert "description" in obj
        assert "options" in obj
        assert "exit_codes" in obj


class TestSmoke4ShimRemoval:
    """TS-04-SMOKE-4: af/json_io.py absent, no references remain."""

    def test_json_io_file_absent(self) -> None:
        """af/json_io.py does not exist on disk."""
        assert not os.path.exists(_AF_PACKAGE_DIR / "json_io.py")

    def test_grep_no_json_io_references(self) -> None:
        """grep finds zero af.json_io references in af/."""
        result = subprocess.run(
            ["grep", "-r", "af.json_io", str(_AF_PACKAGE_DIR)],
            capture_output=True,
            text=True,
        )
        assert result.returncode != 0


@pytest.mark.xfail(reason="af standup not yet wired with format_table Rich output")
class TestSmoke5HumanReadableStandup:
    """TS-04-SMOKE-5: af standup (no --json) shows Rich table."""

    def test_standup_text_mode_not_json(self, cli_runner) -> None:
        """af standup without --json: Rich output, not raw JSON."""
        from af.app import main

        result = cli_runner.invoke(main, ["standup"])
        assert result.exit_code == 0
        # Output should NOT be valid JSON
        with pytest.raises((json.JSONDecodeError, ValueError)):
            json.loads(result.output)
