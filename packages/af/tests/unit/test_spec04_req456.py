"""Tests for shim deletion, JSON help, and format_table (REQ-4, REQ-5, REQ-6).

Test Spec: TS-04-17, TS-04-18, TS-04-19, TS-04-20, TS-04-21,
           TS-04-22, TS-04-23, TS-04-24, TS-04-25, TS-04-26,
           TS-04-E5, TS-04-E6, TS-04-E7, TS-04-E8
Requirements: 04-REQ-4.1, 04-REQ-4.2, 04-REQ-5.1, 04-REQ-5.2, 04-REQ-5.3,
              04-REQ-6.1, 04-REQ-6.2, 04-REQ-6.3, 04-REQ-6.4, 04-REQ-6.5,
              04-REQ-4.E1, 04-REQ-5.E1, 04-REQ-6.E1, 04-REQ-6.E2

Note: Spec references af/insights.py but actual file is af/findings.py.
"""

from __future__ import annotations

import json
import os
import subprocess
from pathlib import Path

import pytest

_AF_PACKAGE_DIR = Path(__file__).resolve().parents[2] / "af"


# --- REQ-4: Shim deletion ---


class TestJsonIoFileAbsent:
    """TS-04-17: af/json_io.py does not exist on disk."""

    def test_json_io_file_does_not_exist(self) -> None:
        """af/json_io.py must not be present in the af package."""
        assert not os.path.exists(_AF_PACKAGE_DIR / "json_io.py")


class TestNoJsonIoImportsViaGrep:
    """TS-04-18: No 'af.json_io' references in af/ source tree."""

    def test_grep_finds_no_json_io_references(self) -> None:
        """grep -r 'af.json_io' af/ returns no matches."""
        result = subprocess.run(
            ["grep", "-r", "af.json_io", str(_AF_PACKAGE_DIR)],
            capture_output=True,
            text=True,
        )
        assert result.returncode != 0, "grep found af.json_io references"
        assert result.stdout.strip() == ""


class TestImportJsonIoRaises:
    """TS-04-E5: Importing af.json_io raises ModuleNotFoundError."""

    def test_import_raises_module_not_found(self) -> None:
        """import af.json_io raises ModuleNotFoundError."""
        import importlib
        import sys

        # Remove from cache if already imported
        for key in list(sys.modules.keys()):
            if key.startswith("af.json_io"):
                del sys.modules[key]

        with pytest.raises(ModuleNotFoundError, match="json_io"):
            importlib.import_module("af.json_io")


# --- REQ-5: Structured JSON help ---


class TestJsonHelpOutput:
    """TS-04-19: --json --help emits JSON command description."""

    def test_json_help_has_required_fields(self, cli_runner) -> None:
        """af standup --json --help returns JSON with name/description/options/exit_codes."""
        from af.app import main

        result = cli_runner.invoke(main, ["standup", "--json", "--help"])
        assert result.exit_code == 0
        obj = json.loads(result.output)
        assert isinstance(obj.get("name"), str)
        assert isinstance(obj.get("description"), str)
        assert isinstance(obj.get("options"), list)
        assert isinstance(obj.get("exit_codes"), list)


class TestExitCodesInJsonHelp:
    """TS-04-20: exit_codes entries have int code and str description."""

    def test_exit_code_entries_have_correct_types(self, cli_runner) -> None:
        """Each exit_codes entry has integer 'code' and string 'description'."""
        from af.app import main

        result = cli_runner.invoke(main, ["standup", "--json", "--help"])
        obj = json.loads(result.output)
        for ec in obj["exit_codes"]:
            assert isinstance(ec["code"], int)
            assert isinstance(ec["description"], str)


class TestTextHelpWithoutJson:
    """TS-04-21: --help without --json renders Click standard text help."""

    def test_text_help_contains_usage(self, cli_runner) -> None:
        """af standup --help shows standard Click text with 'Usage:'."""
        from af.app import main

        result = cli_runner.invoke(main, ["standup", "--help"])
        assert result.exit_code == 0
        assert "Usage:" in result.output or "usage:" in result.output.lower()
        # Verify it is NOT JSON
        with pytest.raises((json.JSONDecodeError, ValueError)):
            json.loads(result.output)


class TestJsonHelpNoExitCodesDecorator:
    """TS-04-E6: Command without @exit_codes still renders valid JSON help."""

    def test_empty_exit_codes_for_undecorated_command(self, cli_runner) -> None:
        """A command without @exit_codes returns exit_codes=[]."""
        import click
        from agentfox.io import AgentFoxGroup

        @click.group(cls=AgentFoxGroup)
        def test_cli() -> None:
            pass

        # Explicitly name the command to ensure stable CLI invocation
        # regardless of Click's function-name-to-command-name derivation.
        @test_cli.command("no-exit-codes-cmd")
        def no_exit_codes_cmd() -> None:
            """A command without exit codes."""

        result = cli_runner.invoke(test_cli, ["no-exit-codes-cmd", "--json", "--help"])
        assert result.exit_code == 0
        obj = json.loads(result.output)
        assert obj["exit_codes"] == []


# --- REQ-6: format_table ---


class TestFormatTableBasic:
    """TS-04-22: format_table basic happy path."""

    def test_json_mode_returns_list_of_dicts(self) -> None:
        """format_table with json_mode=True returns list of dicts."""
        from agentfox.io import format_table

        result = format_table(
            headers=["Name", "Status"],
            rows=[["Alice", "Done"]],
            json_mode=True,
        )
        assert result == [{"Name": "Alice", "Status": "Done"}]

    def test_text_mode_returns_renderable(self) -> None:
        """format_table with json_mode=False returns a non-empty renderable."""
        from agentfox.io import format_table

        result = format_table(
            headers=["Name", "Status"],
            rows=[["Alice", "Done"]],
            json_mode=False,
        )
        assert result is not None
        assert str(result) != ""


class TestFormatTableJsonModeKeys:
    """TS-04-25: format_table JSON mode returns dicts with exact header keys."""

    def test_keys_match_headers_exactly(self) -> None:
        """Every dict has exactly the same keys as the headers list."""
        from agentfox.io import format_table

        result = format_table(
            headers=["A", "B", "C"],
            rows=[["x", "y", "z"], ["1", "2", "3"]],
            json_mode=True,
        )
        assert len(result) == 2
        assert result[0] == {"A": "x", "B": "y", "C": "z"}
        assert result[1] == {"A": "1", "B": "2", "C": "3"}
        for row_dict in result:
            assert set(row_dict.keys()) == {"A", "B", "C"}


class TestFormatTableTextModeRich:
    """TS-04-26: format_table text mode returns Rich Table or renderable."""

    def test_text_mode_returns_rich_compatible(self) -> None:
        """Result is a Rich Table, str, or has __rich_console__."""
        from agentfox.io import format_table

        result = format_table(
            headers=["Name", "Value"],
            rows=[["foo", "bar"]],
            json_mode=False,
        )
        from rich.table import Table

        assert isinstance(result, (Table, str)) or hasattr(result, "__rich_console__")
        assert result is not None


class TestStandupUsesFormatTable:
    """TS-04-23: af/standup.py uses format_table from agentfox.io."""

    def test_standup_imports_format_table(self) -> None:
        """af/standup.py imports format_table from agentfox.io."""
        content = (_AF_PACKAGE_DIR / "standup.py").read_text()
        assert "from agentfox.io" in content or "from agentfox.io.output" in content
        assert "format_table(" in content


class TestInsightsUsesFormatTable:
    """TS-04-24: af/findings.py (insights) uses format_table from agentfox.io."""

    def test_findings_imports_format_table(self) -> None:
        """af/findings.py imports format_table from agentfox.io."""
        content = (_AF_PACKAGE_DIR / "findings.py").read_text()
        assert "from agentfox.io" in content or "from agentfox.io.output" in content
        assert "format_table(" in content


class TestFormatTableEmptyRows:
    """TS-04-E7: format_table with empty rows returns empty structure."""

    def test_json_mode_returns_empty_list(self) -> None:
        """Empty rows with json_mode=True returns []."""
        from agentfox.io import format_table

        result = format_table(headers=["A", "B"], rows=[], json_mode=True)
        assert result == []

    def test_text_mode_returns_empty_table(self) -> None:
        """Empty rows with json_mode=False returns non-None table."""
        from agentfox.io import format_table

        result = format_table(headers=["A", "B"], rows=[], json_mode=False)
        assert result is not None


class TestFormatTableShortRow:
    """TS-04-E8: format_table pads short rows."""

    def test_json_mode_pads_with_none(self) -> None:
        """Short row padded with None in JSON mode."""
        from agentfox.io import format_table

        result = format_table(
            headers=["A", "B", "C"],
            rows=[["only_one"]],
            json_mode=True,
        )
        assert len(result) == 1
        assert result[0]["A"] == "only_one"
        assert result[0]["B"] is None
        assert result[0]["C"] is None

    def test_text_mode_pads_without_exception(self) -> None:
        """Short row in text mode does not raise."""
        from agentfox.io import format_table

        result = format_table(
            headers=["A", "B", "C"],
            rows=[["only_one"]],
            json_mode=False,
        )
        assert result is not None
