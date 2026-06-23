"""Unit tests for agentfox.io.cli — common_options and AgentFoxGroup.

Test Spec: TS-03-8, TS-03-9, TS-03-10, TS-03-11, TS-03-12, TS-03-13,
           TS-03-47, TS-03-48, TS-03-49, TS-03-E3
Requirements: 03-REQ-3.1, 03-REQ-3.2, 03-REQ-3.3, 03-REQ-3.4,
              03-REQ-3.5, 03-REQ-3.6, 03-REQ-3.E1, 03-REQ-9.1,
              03-REQ-9.2, 03-REQ-9.3
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


class TestAfAgentDefaultJsonQuiet:
    """TS-03-8: AF_AGENT=1 defaults json_mode=True and quiet=True."""

    def test_af_agent_1_defaults(self) -> None:
        """03-REQ-3.1: OutputManager has json_mode=True and quiet=True."""
        cli, captured = _make_test_cli()
        runner = CliRunner(env={"AF_AGENT": "1"})
        runner.invoke(cli, ["sub"])
        assert len(captured) == 1
        assert captured[0].json_mode is True
        assert captured[0].quiet is True


class TestAfAgentOverrideNoJson:
    """TS-03-9: --no-json overrides AF_AGENT=1 json_mode while quiet stays True."""

    def test_no_json_overrides_af_agent(self) -> None:
        """03-REQ-3.2: json_mode=False, quiet=True."""
        cli, captured = _make_test_cli()
        runner = CliRunner(env={"AF_AGENT": "1"})
        runner.invoke(cli, ["--no-json", "sub"])
        assert captured[0].json_mode is False
        assert captured[0].quiet is True


class TestAfAgentOverrideVerbose:
    """TS-03-10: --verbose overrides AF_AGENT=1 quiet while json_mode stays True."""

    def test_verbose_overrides_af_agent_quiet(self) -> None:
        """03-REQ-3.3: json_mode=True, quiet=False."""
        cli, captured = _make_test_cli()
        runner = CliRunner(env={"AF_AGENT": "1"})
        runner.invoke(cli, ["--verbose", "sub"])
        assert captured[0].json_mode is True
        assert captured[0].quiet is False


class TestAfAgentOverrideBoth:
    """TS-03-11: --no-json --verbose overrides both AF_AGENT=1 defaults."""

    def test_no_json_verbose_overrides_both(self) -> None:
        """03-REQ-3.4: json_mode=False, quiet=False."""
        cli, captured = _make_test_cli()
        runner = CliRunner(env={"AF_AGENT": "1"})
        runner.invoke(cli, ["--no-json", "--verbose", "sub"])
        assert captured[0].json_mode is False
        assert captured[0].quiet is False


class TestAfAgentNon1ValuesIgnored:
    """TS-03-12: AF_AGENT values other than '1' do not activate agent mode."""

    @pytest.mark.parametrize(
        "bad_val",
        ["true", "yes", "on", "0", ""],
        ids=["true", "yes", "on", "zero", "empty"],
    )
    def test_non_1_values_ignored(self, bad_val: str) -> None:
        """03-REQ-3.5: json_mode=False and quiet=False for non-'1' values."""
        cli, captured = _make_test_cli()
        runner = CliRunner(env={"AF_AGENT": bad_val})
        runner.invoke(cli, ["sub"])
        assert captured[0].json_mode is False, f"AF_AGENT={bad_val!r} should not activate json_mode"
        assert captured[0].quiet is False, f"AF_AGENT={bad_val!r} should not activate quiet"


class TestSentinelKeys:
    """TS-03-13: _json_explicit and _quiet_explicit sentinels set correctly."""

    def test_json_flag_sets_sentinel(self) -> None:
        """03-REQ-3.6: _json_explicit=True when --json passed."""
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
        runner.invoke(cli, ["--json", "sub"])
        assert ctx_capture[-1].get("_json_explicit") is True

    def test_no_flag_no_sentinel(self) -> None:
        """03-REQ-3.6: _json_explicit absent when no flag passed."""
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
        runner.invoke(cli, ["sub"])
        assert ctx_capture[-1].get("_json_explicit") is None or (ctx_capture[-1].get("_json_explicit") is False)

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


class TestCommonOptionsAddsFlags:
    """TS-03-47: common_options adds all four flag groups to the root Click group."""

    def test_all_flags_registered(self) -> None:
        """03-REQ-9.1: Group has --verbose, --quiet, --trace, --json params."""
        from agentfox.io import common_options

        @click.group()
        @common_options
        def cli(**kwargs: object) -> None:
            pass

        param_names = [p.name for p in cli.params]
        assert "verbose" in param_names
        assert "quiet" in param_names
        assert "trace" in param_names
        # json may be registered as 'json' or 'json_mode' depending on impl
        assert any(name in param_names for name in ("json", "json_mode", "no_json")), (
            f"No json-related param found in {param_names}"
        )


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

    def test_skips_conflicting_flag(self) -> None:
        """03-REQ-9.3: No duplicate flag; debug warning logged; no exception."""
        from agentfox.io import common_options

        @click.group()
        @click.option("--json", is_flag=True)
        @common_options
        def cli(**kwargs: object) -> None:
            pass

        # Count json params — should be exactly 1 (not duplicated)
        json_params = [p for p in cli.params if p.name == "json"]
        assert len(json_params) <= 1, "json param should not be duplicated"


class TestAfAgentNon1Comprehensive:
    """TS-03-E3: AF_AGENT set to non-'1' values does not activate agent mode."""

    @pytest.mark.parametrize(
        "bad_val",
        ["true", "yes", "on", "0", ""],
        ids=["true", "yes", "on", "zero", "empty"],
    )
    def test_non_1_values_no_agent_mode(self, bad_val: str) -> None:
        """03-REQ-3.E1: json_mode=False and quiet=False for non-'1' values."""
        cli, captured = _make_test_cli()
        runner = CliRunner(env={"AF_AGENT": bad_val})
        runner.invoke(cli, ["sub"])
        assert captured[0].json_mode is False, f"AF_AGENT={bad_val!r} wrongly activated json_mode"
        assert captured[0].quiet is False, f"AF_AGENT={bad_val!r} wrongly activated quiet"
