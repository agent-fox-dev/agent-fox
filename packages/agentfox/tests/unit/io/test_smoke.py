"""End-to-end smoke tests for agentfox.io unified terminal IO.

Traces all eight execution paths (TS-03-SMOKE-1 through TS-03-SMOKE-8)
and verifies return value propagation through the call chain.

Test Spec: TS-03-SMOKE-1..8, subtasks 12.1, 12.2, 12.3
Requirements: 03-REQ-2.2, 03-REQ-3.1, 03-REQ-5.4, 03-REQ-6.1,
              03-REQ-7.4, 03-REQ-8.3, 03-REQ-14.1
"""

from __future__ import annotations

import json
import os
from unittest.mock import patch

import click
import pytest
from agentfox.core.errors import AgentFoxError
from click.testing import CliRunner

from tests.unit.io.conftest import (
    capture_stderr,
    capture_stdout,
    mock_non_tty,
    mock_stdin,
)

# ---------------------------------------------------------------------------
# Helper: create a minimal CLI using AgentFoxGroup + common_options
# ---------------------------------------------------------------------------


def _make_cli_with_common_options():
    """Create an AgentFoxGroup CLI with common_options applied."""
    from agentfox.io import AgentFoxGroup, common_options

    @click.group(cls=AgentFoxGroup)
    @common_options
    def cli(**kwargs):
        pass

    return cli


# ---------------------------------------------------------------------------
# TS-03-SMOKE-1: Agent mode invocation with AF_AGENT=1
# PATH-1: AF_AGENT=1 -> json_mode=True, quiet=True -> emit_ok -> JSON stdout
# ---------------------------------------------------------------------------


class TestSmoke1AgentMode:
    """TS-03-SMOKE-1: AF_AGENT=1 causes JSON output with ok=true."""

    def test_agent_mode_json_output(self) -> None:
        """PATH-1: Agent mode produces pretty-printed JSON with ok=true."""
        from agentfox.io import AgentFoxGroup, common_options, emit_ok

        @click.group(cls=AgentFoxGroup)
        @common_options
        def cli(**kwargs):
            pass

        captured: list = []

        @cli.command()
        @click.pass_context
        def sub(ctx):
            om = ctx.obj["output"]
            captured.append(om)
            emit_ok({"result": "done"})

        runner = CliRunner(env={"AF_AGENT": "1"})
        with patch("agentfox.core.logging.setup_logging"):
            result = runner.invoke(cli, ["sub"])

        # json_mode is now per-command, not set at group level by AF_AGENT
        assert captured[0].json_mode is False
        assert captured[0].quiet is True

        # stdout receives pretty-printed JSON with ok=true
        parsed = json.loads(result.output)
        assert parsed["ok"] is True
        assert parsed["result"] == "done"
        assert "  " in result.output  # indent=2

        # exit code 0
        assert result.exit_code == 0


# ---------------------------------------------------------------------------
# TS-03-SMOKE-2: Error in JSON mode producing structured envelope
# PATH-2: --json -> ConfigError -> cli_error_handler -> emit_error -> stdout
# ---------------------------------------------------------------------------


class TestSmoke2JsonError:
    """TS-03-SMOKE-2: Structured JSON error envelope for AgentFoxError."""

    def test_json_error_envelope(self) -> None:
        """PATH-2: AgentFoxError produces structured envelope + exit 1."""

        class ConfigError(AgentFoxError):
            retryable = False

        cli = _make_cli_with_common_options()

        @cli.command()
        @click.option("--json/--no-json", "json_flag", default=None)
        @click.pass_context
        def fail(ctx, json_flag):
            if json_flag:
                ctx.obj["output"].json_mode = True
            raise ConfigError("Config not found")

        runner = CliRunner()
        with patch("agentfox.core.logging.setup_logging"):
            result = runner.invoke(cli, ["fail", "--json"])

        assert result.exit_code == 1

        # stdout receives JSON error envelope
        parsed = json.loads(result.output)
        assert parsed["ok"] is False
        assert parsed["error"]["type"] == "config_error"
        assert parsed["error"]["message"] == "Config not found"
        assert parsed["error"]["retryable"] is False


# ---------------------------------------------------------------------------
# TS-03-SMOKE-3: Error in human mode producing plain-text stderr message
# PATH-3: No flags, no AF_AGENT -> ValueError -> plain text stderr -> exit 1
# ---------------------------------------------------------------------------


class TestSmoke3HumanError:
    """TS-03-SMOKE-3: Plain-text stderr error in human mode."""

    def test_human_mode_error(self) -> None:
        """PATH-3: Exception in human mode writes plain text to stderr."""
        cli = _make_cli_with_common_options()

        @cli.command()
        def fail():
            raise ValueError("something broke")

        runner = CliRunner()
        with patch("agentfox.core.logging.setup_logging"):
            result = runner.invoke(cli, ["fail"])

        assert result.exit_code == 1
        # In CliRunner with default settings, stderr is mixed into output.
        # cli_error_handler writes "Error: ..." to stderr via click.echo(err=True).
        # The combined output should contain the error text.
        assert "something broke" in result.output
        # Verify the output is NOT a JSON error envelope (human mode)
        try:
            parsed = json.loads(result.output.strip())
            assert not (parsed.get("ok") is False and "error" in parsed), (
                "Should not emit JSON error envelope in human mode"
            )
        except (json.JSONDecodeError, ValueError):
            pass  # Expected: stdout is not JSON -- correct behavior


# ---------------------------------------------------------------------------
# TS-03-SMOKE-4: StatusSpinner in non-TTY CI environment
# PATH-4: non-TTY -> plain text stderr -> update() + log()
# ---------------------------------------------------------------------------


class TestSmoke4SpinnerNonTTY:
    """TS-03-SMOKE-4: StatusSpinner in non-TTY prints plain text to stderr."""

    def test_non_tty_spinner(self) -> None:
        """PATH-4: update() and log() produce plain text lines on stderr."""
        from agentfox.io import StatusSpinner

        with mock_non_tty():
            with capture_stderr() as err:
                with StatusSpinner("Processing...", quiet=False, theme=None) as s:
                    s.update("Step 1 complete")
                    s.log("Detail logged")

        output = err.getvalue()
        assert "Step 1 complete" in output
        assert "Detail logged" in output


# ---------------------------------------------------------------------------
# TS-03-SMOKE-6: read_stdin from piped JSON input
# PATH-6: piped JSON -> read_stdin() -> parsed dict
# ---------------------------------------------------------------------------


class TestSmoke6ReadStdin:
    """TS-03-SMOKE-6: read_stdin() consumes piped JSON input."""

    def test_piped_json_input(self) -> None:
        """PATH-6: stdin pipe with JSON returns parsed dict."""
        from agentfox.io import read_stdin

        with mock_stdin(b'{"key": "val"}'):
            result = read_stdin()

        assert result == {"key": "val"}


# ---------------------------------------------------------------------------
# TS-03-SMOKE-7: --version eager option before OutputManager
# PATH-7: af --version -> click.echo -> SystemExit(0) -> exit 0
# ---------------------------------------------------------------------------


class TestSmoke7VersionOption:
    """TS-03-SMOKE-7: --version uses click.echo() before OutputManager."""

    def test_version_exits_cleanly(self) -> None:
        """PATH-7: --version outputs version string and exits 0."""
        from af.app import main as af_main

        runner = CliRunner()
        result = runner.invoke(af_main, ["--version"])

        assert result.exit_code == 0
        assert len(result.output.strip()) > 0  # version string present
        # No AttributeError or KeyError in exception chain
        assert result.exception is None or isinstance(result.exception, SystemExit)


# ---------------------------------------------------------------------------
# TS-03-SMOKE-8: emit_error with state parameter
# PATH-8: emit_error(exc, state='executing') -> JSON with top-level 'state'
# ---------------------------------------------------------------------------


class TestSmoke8EmitErrorWithState:
    """TS-03-SMOKE-8: emit_error() with state parameter."""

    def test_emit_error_with_state(self) -> None:
        """PATH-8: emit_error(exc, state='executing') includes 'state' field."""
        from agentfox.io import emit_error

        agentspec_errors = pytest.importorskip("agentspec.errors")
        exc = agentspec_errors.AgentError("rate limited", retryable=True)

        with capture_stdout() as out:
            emit_error(exc, state="executing")

        parsed = json.loads(out.getvalue())
        assert parsed["ok"] is False
        assert parsed["state"] == "executing"
        assert isinstance(parsed["error"]["retryable"], bool)
        # Output is valid JSON parseable by json.loads()
        assert parsed["error"]["message"] == "rate limited"

    def test_emit_error_without_agentspec_with_state(self) -> None:
        """PATH-8 fallback: emit_error works with any exception + state."""
        from agentfox.io import emit_error

        exc = ValueError("something failed")
        with capture_stdout() as out:
            emit_error(exc, state="planning")

        parsed = json.loads(out.getvalue())
        assert parsed["ok"] is False
        assert parsed["state"] == "planning"
        assert parsed["error"]["type"] == "internal_error"


# ---------------------------------------------------------------------------
# 12.2: Return value propagation through the call chain
# ---------------------------------------------------------------------------


class TestCallChainPropagation:
    """12.2: Verify return value propagation through the call chain."""

    def test_emit_error_matches_error_envelope(self) -> None:
        """emit_error() calls error_envelope() and serializes the same dict."""
        from agentfox.io import emit_error, error_envelope

        exc = RuntimeError("chain test")
        expected = error_envelope(exc, state="testing")

        with capture_stdout() as out:
            emit_error(exc, state="testing")

        actual = json.loads(out.getvalue())
        assert actual == expected

    def test_cli_error_handler_passes_exc_unmodified(self) -> None:
        """cli_error_handler receives exc and passes it to emit_error."""
        from agentfox.io.errors import cli_error_handler

        from tests.unit.io.conftest import make_mock_context

        ctx = make_mock_context(json_mode=True)
        exc = RuntimeError("unmodified exception")

        with capture_stdout() as out:
            cli_error_handler(ctx, exc)

        parsed = json.loads(out.getvalue())
        assert parsed["error"]["message"] == "unmodified exception"
        assert parsed["error"]["type"] == "internal_error"
        assert parsed["error"]["detail"] == "RuntimeError"


# ---------------------------------------------------------------------------
# 12.4: Stub and dead-code audit (programmatic checks)
# ---------------------------------------------------------------------------


class TestStubAudit:
    """12.4: Verify no stub markers remain in agentfox/io/ files."""

    def test_no_stub_markers_in_io_package(self) -> None:
        """No raise NotImplementedError, # TODO, # STUB, # FIXME in io/."""
        import agentfox.io

        io_dir = os.path.dirname(agentfox.io.__file__)
        stub_markers = [
            "raise NotImplementedError",
            "# TODO",
            "# STUB",
            "# FIXME",
        ]

        for fname in os.listdir(io_dir):
            if not fname.endswith(".py"):
                continue
            fpath = os.path.join(io_dir, fname)
            with open(fpath) as f:
                source = f.read()
            for marker in stub_markers:
                assert marker not in source, f"Stub marker '{marker}' found in {fname}"

    def test_all_public_symbols_fully_implemented(self) -> None:
        """Every public symbol in __all__ is a real object (not None)."""
        import agentfox.io

        for sym_name in agentfox.io.__all__:
            sym = getattr(agentfox.io, sym_name)
            assert sym is not None, f"{sym_name} is None"
            # Verify callable symbols are actually callable
            if sym_name not in (
                "OutputManager",
                "StatusSpinner",
                "AgentFoxGroup",
                "ProgressDisplay",
            ):
                assert callable(sym), f"{sym_name} is not callable"


class TestAfAppWiring:
    """Verify af/app.py uses AgentFoxGroup."""

    def test_af_app_uses_agent_fox_group(self) -> None:
        """af/app.py root group uses cls=AgentFoxGroup."""
        from af.app import main as af_main
        from agentfox.io import AgentFoxGroup

        assert isinstance(af_main, AgentFoxGroup) or (type(af_main).__name__ == "AgentFoxGroup")
