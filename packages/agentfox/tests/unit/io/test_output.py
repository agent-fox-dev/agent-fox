"""Unit tests for agentfox.io.output — OutputManager and get_output_manager.

Test Spec: TS-03-4, TS-03-5, TS-03-6, TS-03-7, TS-03-14, TS-03-15,
           TS-03-16, TS-03-17, TS-03-18, TS-03-19, TS-03-20, TS-03-21,
           TS-03-22, TS-03-63, TS-03-E2
Requirements: 03-REQ-2.1, 03-REQ-2.2, 03-REQ-2.3, 03-REQ-2.4,
              03-REQ-2.E1, 03-REQ-4.1, 03-REQ-4.2, 03-REQ-4.3,
              03-REQ-4.4, 03-REQ-4.5, 03-REQ-4.6, 03-REQ-4.7,
              03-REQ-4.8, 03-REQ-4.9, 03-REQ-15.1
"""

from __future__ import annotations

import json
import os
from unittest.mock import patch

import click
import pytest
from click.testing import CliRunner

from tests.unit.io.conftest import capture_stderr, capture_stdout


class TestOutputManagerFields:
    """TS-03-4: Verify OutputManager fields with correct types."""

    def test_all_five_fields_accessible(self) -> None:
        """03-REQ-2.1: All five fields are accessible on every OutputManager instance."""
        from agentfox.io import OutputManager
        from rich.console import Console

        om = OutputManager(json_mode=True, quiet=False, verbose=False, trace=False)
        assert isinstance(om.json_mode, bool)
        assert isinstance(om.quiet, bool)
        assert isinstance(om.verbose, bool)
        assert isinstance(om.trace, bool)
        assert isinstance(om.console, Console)

    def test_fields_match_constructor_args(self) -> None:
        """03-REQ-2.1: Fields reflect the values passed to the constructor."""
        from agentfox.io import OutputManager

        om = OutputManager(json_mode=True, quiet=True, verbose=True, trace=True)
        assert om.json_mode is True
        assert om.quiet is True
        assert om.verbose is True
        assert om.trace is True


class TestAgentFoxGroupOutputManagerConstruction:
    """TS-03-5: AgentFoxGroup.invoke() constructs OutputManager at ctx.obj['output']."""

    def test_output_manager_stored_before_subcommand(self) -> None:
        """03-REQ-2.2: ctx.obj['output'] is an OutputManager before any subcommand."""
        from agentfox.io import AgentFoxGroup, OutputManager

        captured: list[OutputManager | None] = []

        @click.group(cls=AgentFoxGroup)
        def cli() -> None:
            pass

        @cli.command()
        @click.pass_context
        def sub(ctx: click.Context) -> None:
            captured.append(ctx.obj.get("output"))

        runner = CliRunner()
        runner.invoke(cli, ["sub"])
        assert len(captured) == 1
        assert isinstance(captured[0], OutputManager)


class TestGetOutputManagerFallback:
    """TS-03-6: get_output_manager() returns fallback defaults when no Click context."""

    def test_fallback_defaults_no_context(self) -> None:
        """03-REQ-2.3: Returns OutputManager with all-False defaults outside Click."""
        from agentfox.io import get_output_manager

        # Ensure no Click context is active
        ctx = click.get_current_context(silent=True)
        assert ctx is None, "Test requires no active Click context"

        om = get_output_manager()
        assert om.json_mode is False
        assert om.quiet is False
        assert om.verbose is False
        assert om.trace is False


class TestAgentFoxGroupNonDictCtxObj:
    """TS-03-7: AgentFoxGroup logs debug warning when ctx.obj is non-dict."""

    def test_non_dict_ctx_obj_does_not_crash(self, caplog: pytest.LogCaptureFixture) -> None:
        """03-REQ-2.4: CLI does not crash when ctx.obj is pre-set to non-dict."""
        import logging

        from agentfox.io import AgentFoxGroup

        @click.group(cls=AgentFoxGroup)
        @click.pass_context
        def cli(ctx: click.Context) -> None:
            ctx.obj = "not_a_dict"

        @cli.command()
        def sub() -> None:
            pass

        with caplog.at_level(logging.DEBUG):
            runner = CliRunner()
            result = runner.invoke(cli, ["sub"])

        # 1. Must not crash
        assert result.exit_code in (0, 1)

        # 2. MUST verify a debug-level warning was logged about the non-dict ctx.obj
        debug_records = [r for r in caplog.records if r.levelno == logging.DEBUG]
        assert any("dict" in r.message.lower() or "ctx.obj" in r.message.lower() for r in debug_records), (
            "expected debug log mentioning non-dict ctx.obj"
        )


class TestEmitJsonWritesWhenJsonMode:
    """TS-03-14: emit_json() writes pretty-printed JSON with indent=2 when json_mode=True."""

    def test_emit_json_writes_indent2(self) -> None:
        """03-REQ-4.1: JSON string with 2-space indentation to stdout."""
        from agentfox.io import OutputManager

        om = OutputManager(json_mode=True, quiet=False, verbose=False, trace=False)
        with capture_stdout() as out:
            om.emit_json({"key": "value"})
        output = out.getvalue()
        parsed = json.loads(output)
        assert parsed == {"key": "value"}
        assert "  " in output  # indent=2 produces two-space indentation


class TestEmitJsonNoop:
    """TS-03-15: emit_json() is a no-op when json_mode=False."""

    def test_emit_json_noop_when_not_json_mode(self) -> None:
        """03-REQ-4.2: Nothing is written to stdout."""
        from agentfox.io import OutputManager

        om = OutputManager(json_mode=False, quiet=False, verbose=False, trace=False)
        with capture_stdout() as out:
            om.emit_json({"key": "value"})
        assert out.getvalue() == ""


class TestEmitHumanWritesWhenNotJsonMode:
    """TS-03-16: emit_human() writes plain text to stdout when json_mode=False."""

    def test_emit_human_writes_text(self) -> None:
        """03-REQ-4.3: Plain text string is written to stdout."""
        from agentfox.io import OutputManager

        om = OutputManager(json_mode=False, quiet=False, verbose=False, trace=False)
        with capture_stdout() as out:
            om.emit_human("Hello world")
        assert "Hello world" in out.getvalue()


class TestEmitHumanNoop:
    """TS-03-17: emit_human() is a no-op when json_mode=True."""

    def test_emit_human_noop_when_json_mode(self) -> None:
        """03-REQ-4.4: Nothing is written to stdout."""
        from agentfox.io import OutputManager

        om = OutputManager(json_mode=True, quiet=False, verbose=False, trace=False)
        with capture_stdout() as out:
            om.emit_human("Hello world")
        assert out.getvalue() == ""


class TestEmitDispatchJsonMode:
    """TS-03-18: emit() writes JSON and does not call human_fn when json_mode=True."""

    def test_emit_json_mode_writes_json_ignores_human_fn(self) -> None:
        """03-REQ-4.5: JSON is written; human_fn is not called."""
        from agentfox.io import OutputManager

        called: list[bool] = []

        def human_fn() -> None:
            called.append(True)

        om = OutputManager(json_mode=True, quiet=False, verbose=False, trace=False)
        with capture_stdout() as out:
            om.emit({"key": "value"}, human_fn=human_fn)
        assert json.loads(out.getvalue()) == {"key": "value"}
        assert len(called) == 0


class TestEmitDispatchHumanMode:
    """TS-03-19: emit() calls human_fn exactly once when json_mode=False."""

    def test_emit_human_mode_calls_human_fn(self) -> None:
        """03-REQ-4.6: human_fn is called exactly once; no JSON written."""
        from agentfox.io import OutputManager

        called: list[bool] = []

        def human_fn() -> None:
            called.append(True)

        om = OutputManager(json_mode=False, quiet=False, verbose=False, trace=False)
        with capture_stdout() as out:
            om.emit({"key": "value"}, human_fn=human_fn)
        assert len(called) == 1
        assert out.getvalue() == ""


class TestEmitDispatchNoopWhenNoHumanFn:
    """TS-03-20: emit() is a silent no-op when json_mode=False and human_fn is None."""

    def test_emit_noop_no_human_fn(self) -> None:
        """03-REQ-4.7: No output produced; no exception raised."""
        from agentfox.io import OutputManager

        om = OutputManager(json_mode=False, quiet=False, verbose=False, trace=False)
        with capture_stdout() as out:
            om.emit({"key": "value"}, human_fn=None)  # should not raise
        assert out.getvalue() == ""


class TestBannerSuppression:
    """TS-03-21: banner() writes to stderr only when both json_mode=False and quiet=False."""

    @pytest.mark.parametrize(
        ("json_mode", "quiet", "expect_output"),
        [
            (False, False, True),
            (True, False, False),
            (False, True, False),
            (True, True, False),
        ],
        ids=["human-verbose", "json-verbose", "human-quiet", "json-quiet"],
    )
    def test_banner_suppression_rules(self, json_mode: bool, quiet: bool, expect_output: bool) -> None:
        """03-REQ-4.8: Banner written to stderr only for (json_mode=False, quiet=False)."""
        from agentfox.io import OutputManager

        om = OutputManager(json_mode=json_mode, quiet=quiet, verbose=False, trace=False)
        with capture_stderr() as err:
            om.banner()
        if expect_output:
            assert len(err.getvalue()) > 0
        else:
            assert len(err.getvalue()) == 0


class TestStatusSuppression:
    """TS-03-22: status() writes to stderr when quiet=False; suppressed when quiet=True."""

    def test_status_suppressed_when_quiet(self) -> None:
        """03-REQ-4.9: No output when quiet=True."""
        from agentfox.io import OutputManager

        om = OutputManager(json_mode=False, quiet=True, verbose=False, trace=False)
        with capture_stderr() as err:
            om.status("Processing...")
        assert err.getvalue() == ""

    def test_status_writes_when_not_quiet(self) -> None:
        """03-REQ-4.9: Message written to stderr when quiet=False."""
        from agentfox.io import OutputManager

        om = OutputManager(json_mode=False, quiet=False, verbose=False, trace=False)
        with capture_stderr() as err:
            om.status("Processing...")
        assert "Processing..." in err.getvalue()


class TestGetOutputManagerFallbackIgnoresAfAgent:
    """TS-03-E2: get_output_manager() fallback returns json_mode=False even with AF_AGENT=1."""

    def test_fallback_ignores_af_agent(self) -> None:
        """03-REQ-2.E1: AF_AGENT env var is NOT consulted in the fallback path."""
        from agentfox.io import get_output_manager

        with patch.dict(os.environ, {"AF_AGENT": "1"}):
            # Ensure no Click context is active
            ctx = click.get_current_context(silent=True)
            assert ctx is None, "Test requires no active Click context"

            om = get_output_manager()
            assert om.json_mode is False
            assert om.quiet is False
            assert om.verbose is False
            assert om.trace is False


class TestSetupLoggingCalledByAgentFoxGroup:
    """TS-03-63: AgentFoxGroup.invoke() calls setup_logging() with correct resolved flags."""

    def test_setup_logging_called_once_with_correct_args(self) -> None:
        """03-REQ-15.1: setup_logging called exactly once with resolved flag values."""
        from agentfox.io import AgentFoxGroup, common_options

        calls: list[dict] = []

        @click.group(cls=AgentFoxGroup)
        @common_options
        def cli(**kwargs: object) -> None:
            pass

        @cli.command()
        def sub() -> None:
            pass

        with patch(
            "agentfox.core.logging.setup_logging",
            side_effect=lambda **kw: calls.append(kw),
        ):
            runner = CliRunner()
            runner.invoke(cli, ["--verbose", "sub"])

        assert len(calls) == 1
        assert calls[0].get("verbose") is True
        assert calls[0].get("quiet") is False
        assert calls[0].get("trace") is False
