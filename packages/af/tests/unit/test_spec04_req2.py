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
    "nightshift.py",
    "reset.py",
    "findings.py",
]

_AF_PACKAGE_DIR = Path(__file__).resolve().parents[2] / "af"


class TestSubcommandsUseOutputManager:
    """TS-04-4: Each af subcommand retrieves OutputManager."""

    @pytest.mark.xfail(
        strict=False,
        reason="OutputManager migration not yet done",
    )
    @pytest.mark.parametrize("filename", _SUBCOMMAND_FILES)
    def test_uses_output_manager(self, filename: str) -> None:
        """Subcommand file uses ctx.obj['output'] and om.emit()."""
        filepath = _AF_PACKAGE_DIR / filename
        content = filepath.read_text()
        has_output_retrieval = (
            "ctx.obj['output']" in content or 'ctx.obj["output"]' in content
        )
        has_om_emit = "om.emit(" in content or "output.emit(" in content
        assert has_output_retrieval, f"{filename} missing ctx.obj['output'] retrieval"
        assert has_om_emit, f"{filename} missing om.emit() call"

    @pytest.mark.xfail(
        strict=False,
        reason="Not all files migrated yet; some use console.print",
    )
    @pytest.mark.parametrize("filename", _SUBCOMMAND_FILES)
    def test_no_click_echo_data_output(self, filename: str) -> None:
        """Subcommand file does not use click.echo() for data output."""
        filepath = _AF_PACKAGE_DIR / filename
        content = filepath.read_text()
        assert "click.echo(" not in content, (
            f"{filename} still uses click.echo() for data output"
        )


class TestNoJsonIoImports:
    """TS-04-5: No af subcommand imports from af.json_io."""

    @pytest.mark.xfail(
        strict=False,
        reason="af.json_io shim not yet deleted; some files clean",
    )
    @pytest.mark.parametrize("filename", _SUBCOMMAND_FILES)
    def test_no_json_io_import(self, filename: str) -> None:
        """Subcommand file has no import from af.json_io."""
        filepath = _AF_PACKAGE_DIR / filename
        content = filepath.read_text()
        assert "af.json_io" not in content, (
            f"{filename} still imports from af.json_io"
        )
        assert "from af import json_io" not in content, (
            f"{filename} still imports json_io from af"
        )


@pytest.mark.xfail(reason="OutputManager not yet implemented in agentfox.io")
class TestOutputManagerTextMode:
    """TS-04-6: OutputManager renders human-readable text."""

    def test_emit_produces_human_readable_output(self) -> None:
        """om.emit() with json_mode=False produces non-JSON output."""
        import io

        from agentfox.io import OutputManager

        buf = io.StringIO()
        om = OutputManager(json_mode=False, stdout=buf)
        om.emit({"key": "value"})
        output = buf.getvalue()
        assert len(output.strip()) > 0
        assert '{"key"' not in output


@pytest.mark.xfail(reason="OutputManager not yet implemented in agentfox.io")
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


@pytest.mark.xfail(reason="af standup not yet migrated to OutputManager")
class TestStandupJsonOutput:
    """TS-04-8: af standup --json emits structured JSON."""

    def test_standup_json_exits_zero_with_valid_json(self, cli_runner) -> None:
        """af standup --json returns exit 0 and valid JSON."""
        from af.app import main

        result = cli_runner.invoke(main, ["standup", "--json"])
        assert result.exit_code == 0
        obj = json.loads(result.output)
        assert isinstance(obj, dict)


@pytest.mark.xfail(reason="af init not yet migrated to OutputManager")
class TestInitJsonOutput:
    """TS-04-9: af init --json emits structured JSON."""

    def test_init_json_exits_zero_with_valid_json(self, cli_runner) -> None:
        """af init --json returns exit 0 and valid JSON."""
        from af.app import main

        result = cli_runner.invoke(main, ["init", "--json"])
        assert result.exit_code == 0
        obj = json.loads(result.output)
        assert isinstance(obj, dict)


@pytest.mark.xfail(reason="RuntimeError guard for missing OutputManager not implemented")
class TestMissingOutputManagerRaises:
    """TS-04-E2: RuntimeError when ctx.obj['output'] is missing."""

    def test_standup_with_empty_obj_raises(self, cli_runner) -> None:
        """af standup with obj={} raises RuntimeError."""
        from af.app import main

        result = cli_runner.invoke(main, ["standup"], obj={})
        assert result.exit_code != 0
        assert result.exception is not None
        exc_str = str(result.exception)
        assert "output" in exc_str.lower() or "OutputManager" in exc_str
