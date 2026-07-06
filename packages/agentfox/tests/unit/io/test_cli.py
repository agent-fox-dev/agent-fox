"""Unit tests for agentfox.io.cli — common_options and AgentFoxGroup.

Test Spec: TS-03-8, TS-03-10, TS-03-47, TS-03-48, TS-03-49, TS-03-E3
Requirements: 03-REQ-3.1, 03-REQ-3.3, 03-REQ-3.5, 03-REQ-3.E1,
              03-REQ-9.1, 03-REQ-9.2, 03-REQ-9.3
"""

from __future__ import annotations

from typing import Any

import click
import pytest
from click.testing import CliRunner


def _make_test_cli() -> tuple[click.Group, list[Any]]:
    """Create a test CLI with AgentFoxGroup and common_options, returning captured outputs."""
    from agentfox.io import AgentFoxGroup, OutputManager, common_options

    captured: list[OutputManager] = []

    @click.group(cls=AgentFoxGroup)
    @common_options
    def cli(**kwargs: object) -> None:
        pass

    @cli.command()
    @click.pass_context
    def sub(ctx: click.Context) -> None:
        captured.append(ctx.obj["output"])

    return cli, captured


class TestAfAgentDefaultQuiet:
    """TS-03-8: AF_AGENT=1 defaults quiet=True."""

    def test_af_agent_1_defaults(self) -> None:
        """03-REQ-3.1: OutputManager has quiet=True. json_mode is per-command."""
        cli, captured = _make_test_cli()
        runner = CliRunner(env={"AF_AGENT": "1"})
        runner.invoke(cli, ["sub"])
        assert len(captured) == 1
        assert captured[0].quiet is True


class TestAfAgentOverrideVerbose:
    """TS-03-10: --verbose overrides AF_AGENT=1 quiet."""

    def test_verbose_overrides_af_agent_quiet(self) -> None:
        """03-REQ-3.3: quiet=False when --verbose passed."""
        cli, captured = _make_test_cli()
        runner = CliRunner(env={"AF_AGENT": "1"})
        runner.invoke(cli, ["--verbose", "sub"])
        assert captured[0].quiet is False


class TestAfAgentNon1ValuesIgnored:
    """TS-03-12: AF_AGENT values other than '1' do not activate agent mode."""

    @pytest.mark.parametrize(
        "bad_val",
        ["true", "yes", "on", "0", ""],
        ids=["true", "yes", "on", "zero", "empty"],
    )
    def test_non_1_values_ignored(self, bad_val: str) -> None:
        """03-REQ-3.5: quiet=False for non-'1' values."""
        cli, captured = _make_test_cli()
        runner = CliRunner(env={"AF_AGENT": bad_val})
        runner.invoke(cli, ["sub"])
        assert captured[0].quiet is False, f"AF_AGENT={bad_val!r} should not activate quiet"


class TestSentinelKeys:
    """TS-03-13: _quiet_explicit sentinel set correctly."""

    def test_quiet_flag_sets_sentinel(self) -> None:
        """03-REQ-3.6: _quiet_explicit=True when --quiet passed."""
        from agentfox.io import AgentFoxGroup, common_options

        ctx_capture: list[dict] = []

        @click.group(cls=AgentFoxGroup)
        @common_options
        def cli(**kwargs: object) -> None:
            pass

        @cli.command()
        @click.pass_context
        def sub(ctx: click.Context) -> None:
            ctx_capture.append(dict(ctx.obj))

        runner = CliRunner()
        runner.invoke(cli, ["--quiet", "sub"])
        assert ctx_capture[-1].get("_quiet_explicit") is True

    def test_verbose_flag_also_sets_quiet_sentinel(self) -> None:
        """03-REQ-3.6: _quiet_explicit=True when --verbose passed (either flag in pair)."""
        from agentfox.io import AgentFoxGroup, common_options

        ctx_capture: list[dict] = []

        @click.group(cls=AgentFoxGroup)
        @common_options
        def cli(**kwargs: object) -> None:
            pass

        @cli.command()
        @click.pass_context
        def sub(ctx: click.Context) -> None:
            ctx_capture.append(dict(ctx.obj))

        runner = CliRunner()
        runner.invoke(cli, ["--verbose", "sub"])
        assert ctx_capture[-1].get("_quiet_explicit") is True


class TestCommonOptionsAddsFlags:
    """TS-03-47: common_options adds --verbose and --quiet to the root Click group."""

    def test_all_flags_registered(self) -> None:
        """03-REQ-9.1: Group has --verbose and --quiet params."""
        from agentfox.io import common_options

        @click.group()
        @common_options
        def cli(**kwargs: object) -> None:
            pass

        param_names = [p.name for p in cli.params]
        assert "verbose" in param_names
        assert "quiet" in param_names
        assert "trace" not in param_names, "--trace must not be registered after removal"


class TestCommonOptionsRejectsNonGroup:
    """TS-03-48: common_options raises TypeError on Click Command (non-Group)."""

    def test_raises_type_error_on_command(self) -> None:
        """03-REQ-9.2: TypeError raised at decoration time."""
        from agentfox.io import common_options

        with pytest.raises(TypeError) as exc_info:

            @common_options
            @click.command()
            def sub() -> None:
                pass

        assert (
            "root" in str(exc_info.value).lower()
            or "subcommand" in str(exc_info.value).lower()
            or "group" in str(exc_info.value).lower()
        )


class TestCommonOptionsNameCollision:
    """TS-03-49: common_options skips conflicting flags and logs debug warning."""

    def test_skips_conflicting_flag(self, caplog: pytest.LogCaptureFixture) -> None:
        """03-REQ-9.3: No duplicate flag; debug warning logged; no exception."""
        import logging

        from agentfox.io import common_options

        with caplog.at_level(logging.DEBUG):

            @click.group()
            @click.option("--verbose", is_flag=True)
            @common_options
            def cli(**kwargs: object) -> None:
                pass

        verbose_params = [p for p in cli.params if p.name == "verbose"]
        assert len(verbose_params) == 1, f"expected exactly 1 verbose param, got {len(verbose_params)}"

        debug_records = [r for r in caplog.records if r.levelno == logging.DEBUG]
        assert any(
            "verbose" in r.message.lower()
            and (
                "skip" in r.message.lower()
                or "conflict" in r.message.lower()
                or "collision" in r.message.lower()
                or "already" in r.message.lower()
            )
            for r in debug_records
        ), "expected debug log about verbose flag collision"


class TestAfAgentNon1Comprehensive:
    """TS-03-E3: AF_AGENT set to non-'1' values does not activate agent mode."""

    @pytest.mark.parametrize(
        "bad_val",
        ["true", "yes", "on", "0", ""],
        ids=["true", "yes", "on", "zero", "empty"],
    )
    def test_non_1_values_no_agent_mode(self, bad_val: str) -> None:
        """03-REQ-3.E1: quiet=False for non-'1' values."""
        cli, captured = _make_test_cli()
        runner = CliRunner(env={"AF_AGENT": bad_val})
        runner.invoke(cli, ["sub"])
        assert captured[0].quiet is False, f"AF_AGENT={bad_val!r} wrongly activated quiet"
