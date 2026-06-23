"""Property tests for af agentic CLI migration (PROP-1..5).

Test Spec: TS-04-P1, TS-04-P2, TS-04-P3, TS-04-P4, TS-04-P5
Requirements: 04-REQ-2.1, 04-REQ-3.5, 04-REQ-4.1, 04-REQ-6.4
"""

from __future__ import annotations

import json
import os
import random
from pathlib import Path

import pytest
from hypothesis import given, settings
from hypothesis import strategies as st

_AF_PACKAGE_DIR = Path(__file__).resolve().parents[2] / "af"

# All subcommand files plus package-level files
_SUBCOMMAND_FILES = [
    "code.py",
    "plan.py",
    "standup.py",
    "init.py",
    "nightshift.py",
    "reset.py",
    "findings.py",
]

_ALL_AF_PY_FILES = _SUBCOMMAND_FILES + ["__init__.py", "app.py"]

# Subcommands that can be invoked with --json for property testing
_SUBCOMMANDS = [
    "code",
    "plan",
    "standup",
    "init",
    "night-shift",
    "reset",
    "insights",
]


@pytest.mark.xfail(
    strict=False,
    reason="JSONL streaming and ProgressDisplay not yet wired",
)
class TestProp1StdoutStderrSeparation:
    """TS-04-P1: stdout/stderr separation for JSONL streaming commands.

    For any invocation of af code or af night-shift with --json, every
    stdout line is valid JSON and every stderr line is a valid JSONL
    progress event; no cross-contamination occurs.
    """

    @pytest.mark.parametrize("command", ["code", "night-shift"])
    def test_stdout_stderr_no_cross_contamination(
        self, cli_runner, command: str
    ) -> None:
        """All stdout lines are valid JSON; all stderr lines are JSONL events."""
        from af.app import main
        from click.testing import CliRunner

        runner = CliRunner(mix_stderr=False)
        result = runner.invoke(main, [command, "--json"])

        # All stdout lines must be valid JSON
        for line in result.output.strip().splitlines():
            if line.strip():
                json.loads(line)

        # All stderr lines must be valid JSONL with an 'event' key
        stderr_text = result.stderr if hasattr(result, "stderr") else ""
        for line in stderr_text.strip().splitlines():
            if line.strip():
                obj = json.loads(line)
                assert "event" in obj

        assert result.exit_code == 0


@pytest.mark.xfail(
    strict=False,
    reason="OutputManager migration and --json mode not yet complete",
)
class TestProp4JsonModeValidOutput:
    """TS-04-P4: JSON mode produces valid JSON on stdout.

    For any af command invoked with --json, stdout contains only valid
    JSON text and the process exits with code 0 on success.
    """

    @pytest.mark.parametrize("command", _SUBCOMMANDS)
    def test_json_mode_stdout_is_valid_json(
        self, cli_runner, command: str
    ) -> None:
        """af <cmd> --json produces valid JSON on stdout and exits 0."""
        from af.app import main

        result = cli_runner.invoke(main, [command, "--json"])
        assert result.exit_code == 0
        obj = json.loads(result.output)
        assert isinstance(obj, dict)


class TestProp2NoJsonIoReferences:
    """TS-04-P2: No af.json_io references in any af/ Python file."""

    @pytest.mark.parametrize("filename", _ALL_AF_PY_FILES)
    def test_no_json_io_in_source(self, filename: str) -> None:
        """'af.json_io' does not appear in the given af/ source file."""
        filepath = _AF_PACKAGE_DIR / filename
        if not filepath.exists():
            pytest.skip(f"{filename} does not exist")
        content = filepath.read_text()
        assert "af.json_io" not in content

    def test_json_io_file_absent(self) -> None:
        """af/json_io.py does not exist on disk."""
        assert not os.path.exists(_AF_PACKAGE_DIR / "json_io.py")


@pytest.mark.xfail(
    strict=False,
    reason="OutputManager migration not done; click.echo still used for data",
)
class TestProp3OutputManagerSoleChannel:
    """TS-04-P3: om.emit() is the sole data output channel."""

    @pytest.mark.parametrize("filename", _SUBCOMMAND_FILES)
    def test_no_click_echo_for_data(self, filename: str) -> None:
        """click.echo() is not used for data payloads in the given file."""
        filepath = _AF_PACKAGE_DIR / filename
        content = filepath.read_text()
        assert "click.echo(" not in content, (
            f"{filename} uses click.echo for data output"
        )


@pytest.mark.xfail(reason="format_table not yet implemented in agentfox.io")
class TestProp5FormatTableKeyAlignment:
    """TS-04-P5: format_table JSON dicts always have header-matching keys."""

    @settings(max_examples=50)
    @given(
        headers=st.lists(
            st.text(
                alphabet=st.characters(whitelist_categories=("L", "N")),
                min_size=1,
                max_size=10,
            ),
            min_size=1,
            max_size=10,
            unique=True,
        ),
        num_rows=st.integers(min_value=1, max_value=20),
    )
    def test_all_dicts_have_header_keys(
        self, headers: list[str], num_rows: int
    ) -> None:
        """Every dict in format_table output has exactly the header keys."""
        from agentfox.io import format_table

        rows = []
        for _ in range(num_rows):
            width = random.randint(0, len(headers) + 2)  # noqa: S311
            rows.append([f"v{i}" for i in range(width)])

        result = format_table(headers=headers, rows=rows, json_mode=True)
        assert len(result) == num_rows
        for row_dict in result:
            assert set(row_dict.keys()) == set(headers)
