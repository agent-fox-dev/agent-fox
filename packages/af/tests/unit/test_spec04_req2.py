"""Tests for OutputManager migration in af subcommands (REQ-2).

Test Spec: TS-04-4, TS-04-5, TS-04-6, TS-04-7, TS-04-8, TS-04-9, TS-04-E2
Requirements: 04-REQ-2.1, 04-REQ-2.2, 04-REQ-2.3, 04-REQ-2.4,
              04-REQ-2.5, 04-REQ-2.6, 04-REQ-2.E1

Note: Spec references af/insights.py and af/night_shift.py but the actual
filenames are af/findings.py and af/nightshift.py (skeptic findings).
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

# Correct filenames per codebase reality (skeptic findings)
_SUBCOMMAND_FILES = [
    "code.py",
    "plan.py",
    "standup.py",
    "init.py",
    "reset.py",
    "findings.py",
]

_AF_PACKAGE_DIR = Path(__file__).resolve().parents[2] / "af"


class TestSubcommandsUseOutputManager:
    """TS-04-4: Each af subcommand retrieves OutputManager."""

    @pytest.mark.parametrize("filename", _SUBCOMMAND_FILES)
    def test_uses_output_manager(self, filename: str) -> None:
        """Subcommand file uses ctx.obj['output'] (directly or via helper) and om.emit()."""
        filepath = _AF_PACKAGE_DIR / filename
        content = filepath.read_text()
        has_output_retrieval = (
            "ctx.obj['output']" in content or 'ctx.obj["output"]' in content or "get_output_manager" in content
        )
        has_om_emit = "om.emit(" in content or "output.emit(" in content
        assert has_output_retrieval, f"{filename} missing OutputManager retrieval"
        assert has_om_emit, f"{filename} missing om.emit() call"

    @pytest.mark.parametrize("filename", _SUBCOMMAND_FILES)
    def test_no_click_echo_json_data_output(self, filename: str) -> None:
        """Subcommand does not use click.echo() to emit structured JSON data.

        Data payloads (JSON dicts) must go through om.emit(), not
        click.echo(json.dumps(...)). Text-mode human output and
        stderr error messages via click.echo() are acceptable per
        04-REQ-2.1 (which only requires data output through om.emit).

        Verifies that click.echo is never used to serialize JSON data
        directly — patterns like ``click.echo(json.dumps(...))``.
        """
        import re

        filepath = _AF_PACKAGE_DIR / filename
        content = filepath.read_text()

        # click.echo(json.dumps(...)) would bypass OutputManager for data
        assert "click.echo(json.dumps(" not in content, (
            f"{filename} uses click.echo(json.dumps(...)) — data should go through om.emit()"
        )
        # click.echo with emit_json/emit_ok/emit_line patterns (legacy shim)
        assert "click.echo(emit" not in content, (
            f"{filename} uses click.echo(emit*(...)) — data should go through om.emit()"
        )
        # Verify all click.echo calls are either err=True or text-mode output
        # by checking no click.echo call directly outputs a dict/json structure
        echo_calls = re.findall(r"click\.echo\(([^)]+)\)", content)
        for call_arg in echo_calls:
            assert "json.dumps" not in call_arg, f"{filename} has click.echo with json.dumps in args"


class TestNoJsonIoImports:
    """TS-04-5: No af subcommand imports from af.json_io."""

    @pytest.mark.parametrize("filename", _SUBCOMMAND_FILES)
    def test_no_json_io_import(self, filename: str) -> None:
        """Subcommand file has no import from af.json_io."""
        filepath = _AF_PACKAGE_DIR / filename
        content = filepath.read_text()
        assert "af.json_io" not in content, f"{filename} still imports from af.json_io"
        assert "from af import json_io" not in content, f"{filename} still imports json_io from af"


class TestOutputManagerTextMode:
    """TS-04-6: OutputManager renders human-readable text."""

    def test_emit_produces_human_readable_output(self) -> None:
        """om.emit() with json_mode=False produces non-JSON output via human_fn."""
        import io

        from agentfox.io import OutputManager

        buf = io.StringIO()
        om = OutputManager(json_mode=False, stdout=buf)
        # emit() dispatches: json_mode → emit_json, else → human_fn.
        # In text mode a human_fn callback produces the readable output.
        om.emit({"key": "value"}, human_fn=lambda: om.emit_human("key: value"))
        output = buf.getvalue()
        assert len(output.strip()) > 0
        assert '{"key"' not in output

    def test_emit_human_writes_to_stdout(self) -> None:
        """emit_human() writes plain text to stdout when json_mode=False."""
        import io

        from agentfox.io import OutputManager

        buf = io.StringIO()
        om = OutputManager(json_mode=False, stdout=buf)
        om.emit_human("hello world")
        output = buf.getvalue()
        assert "hello world" in output


class TestOutputManagerJsonMode:
    """TS-04-7: OutputManager renders valid JSON."""

    def test_emit_produces_valid_json(self) -> None:
        """om.emit() with json_mode=True produces valid JSON on stdout."""
        import io

        from agentfox.io import OutputManager

        buf = io.StringIO()
        om = OutputManager(json_mode=True, stdout=buf)
        om.emit({"key": "value"})
        output = buf.getvalue().strip()
        obj = json.loads(output)
        assert obj == {"key": "value"}


class TestStandupJsonOutput:
    """TS-04-8: af standup --json emits structured JSON."""

    def test_standup_json_exits_zero_with_valid_json(self, cli_runner) -> None:
        """af standup --json returns exit 0 and valid JSON.

        Note: --json is a group-level flag, so it must precede the subcommand.
        """
        from af.app import main

        result = cli_runner.invoke(main, ["--json", "standup"])
        assert result.exit_code == 0
        obj = json.loads(result.output)
        assert isinstance(obj, dict)


class TestInitJsonOutput:
    """TS-04-9: af init --json emits structured JSON."""

    def test_init_json_exits_zero_with_valid_json(self, cli_runner) -> None:
        """af init --json returns exit 0 and valid JSON.

        Note: --json is a group-level flag, so it must precede the subcommand.
        """
        from af.app import main

        result = cli_runner.invoke(main, ["--json", "init"])
        assert result.exit_code == 0
        obj = json.loads(result.output)
        assert isinstance(obj, dict)


class TestMissingOutputManagerRaises:
    """TS-04-E2: Verify OutputManager guard behavior.

    When ctx.obj is None or lacks the 'output' key,
    get_output_manager creates a fallback OutputManager (json_mode=False)
    for backward compatibility with tests that invoke subcommands
    directly without the group callback (04-REQ-7.1).
    """

    def test_fallback_created_when_output_key_missing(self) -> None:
        """get_output_manager creates a fallback when 'output' key is absent.

        When ctx.obj exists but lacks 'output', a default OutputManager
        is created with json_mode=False and stored back in ctx.obj.
        """
        import click
        from af import get_output_manager
        from agentfox.io import OutputManager

        ctx = click.Context(click.Command("test"), obj={})
        om = get_output_manager(ctx)
        assert isinstance(om, OutputManager)
        assert om.json_mode is False
        assert ctx.obj["output"] is om

    def test_fallback_created_when_ctx_obj_is_none(self) -> None:
        """get_output_manager creates a fallback when ctx.obj is None.

        Even when ctx.obj is None, a default OutputManager is created
        so existing tests and direct subcommand invocations still work.
        """
        import click
        from af import get_output_manager
        from agentfox.io import OutputManager

        ctx = click.Context(click.Command("test"))
        assert ctx.obj is None  # precondition
        om = get_output_manager(ctx)
        assert isinstance(om, OutputManager)
        assert om.json_mode is False

    def test_returns_existing_output_manager(self) -> None:
        """get_output_manager returns existing OutputManager from ctx.obj."""
        import click
        from af import get_output_manager
        from agentfox.io import OutputManager

        existing_om = OutputManager(json_mode=True)
        ctx = click.Context(click.Command("test"), obj={"output": existing_om})
        om = get_output_manager(ctx)
        assert om is existing_om
        assert om.json_mode is True
