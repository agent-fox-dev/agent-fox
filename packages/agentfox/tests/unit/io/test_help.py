"""Unit tests for agentfox.io.help — exit_codes decorator.

Test Spec: TS-03-50, TS-03-51, TS-03-52, TS-03-53
Requirements: 03-REQ-10.1, 03-REQ-10.2, 03-REQ-10.3, 03-REQ-10.4
"""

from __future__ import annotations

import json

import click
import pytest
from click.testing import CliRunner


class TestExitCodesDecorator:
    """TS-03-50: exit_codes sets command.exit_codes on the Command object."""

    def test_exit_codes_sets_attribute(self) -> None:
        """03-REQ-10.1: command.exit_codes is set to the provided mapping."""
        from agentfox.io import exit_codes

        @exit_codes(**{"0": "completed", "1": "error"})
        @click.command()
        def my_cmd() -> None:
            pass

        assert hasattr(my_cmd, "exit_codes")
        assert my_cmd.exit_codes == {"0": "completed", "1": "error"}
        assert isinstance(my_cmd, click.Command)


class TestExitCodesWrongOrder:
    """TS-03-51: exit_codes raises TypeError when applied below @click.command."""

    def test_raises_type_error_below_click_command(self) -> None:
        """03-REQ-10.2: TypeError at decoration time when receiving raw function."""
        from agentfox.io import exit_codes

        with pytest.raises(TypeError) as exc_info:

            @click.command()
            @exit_codes(**{"0": "done"})
            def my_cmd() -> None:
                pass

        error_msg = str(exc_info.value).lower()
        assert "click.command" in error_msg or "plain function" in error_msg or ("command" in error_msg)


class TestExitCodesDoubleApplication:
    """TS-03-52: Second exit_codes application overwrites first with no merge."""

    def test_second_overwrites_first(self) -> None:
        """03-REQ-10.3: command.exit_codes contains only the second mapping."""
        from agentfox.io import exit_codes

        @exit_codes(**{"0": "second", "2": "stalled"})
        @exit_codes(**{"0": "first"})
        @click.command()
        def my_cmd() -> None:
            pass

        assert my_cmd.exit_codes == {"0": "second", "2": "stalled"}
        assert "first" not in str(my_cmd.exit_codes)


class TestHelpOutputUnchanged:
    """TS-03-53: --help output is standard Click text, not JSON, in Spec 03."""

    def test_help_is_standard_text(self) -> None:
        """03-REQ-10.4: Click's standard human-readable text help output."""
        from agentfox.io import AgentFoxGroup, common_options

        @click.group(cls=AgentFoxGroup)
        @common_options
        def cli(**kwargs: object) -> None:
            pass

        runner = CliRunner()
        result = runner.invoke(cli, ["--help"])
        assert result.exit_code == 0
        assert "Usage" in result.output or "Options" in result.output
        # Must not be valid JSON
        with pytest.raises((json.JSONDecodeError, ValueError)):
            json.loads(result.output)
