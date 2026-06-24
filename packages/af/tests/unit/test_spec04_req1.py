"""Tests for AgentFoxGroup wiring in af/app.py (REQ-1).

Test Spec: TS-04-1, TS-04-2, TS-04-3, TS-04-E1
Requirements: 04-REQ-1.1, 04-REQ-1.2, 04-REQ-1.3, 04-REQ-1.E1
"""

from __future__ import annotations

import json
import sys

import click
import pytest
from click.testing import CliRunner


class TestAppUsesAgentFoxGroup:
    """TS-04-1: af/app.py declares top-level group with cls=AgentFoxGroup."""

    def test_cli_is_instance_of_agent_fox_group(self) -> None:
        """The top-level CLI group is an instance of AgentFoxGroup."""
        from af.app import main
        from agentfox.io import AgentFoxGroup

        assert isinstance(main, AgentFoxGroup)


class TestNoLegacySymbols:
    """TS-04-2: af module no longer exports BannerGroup or handle_agent_fox_errors."""

    def test_af_module_no_banner_group(self) -> None:
        """BannerGroup is not an attribute of the af module.

        BannerGroup was removed from af/app.py (04-REQ-1.2); it was
        never exported from af/__init__.py.
        """
        import af

        assert not hasattr(af, "BannerGroup")

    def test_af_module_no_handle_agent_fox_errors(self) -> None:
        """handle_agent_fox_errors is not an attribute of the af module."""
        import af

        assert not hasattr(af, "handle_agent_fox_errors")


class TestErrorEnvelope:
    """TS-04-3: AgentFoxGroup catches unhandled exceptions.

    AgentFoxGroup emits JSON error envelopes in agent mode (AF_AGENT=1).
    We register a test subcommand that raises RuntimeError and verify
    the error handling path.
    """

    def test_error_envelope_nonzero_exit(self, cli_runner) -> None:
        """Process exits with non-zero exit code on unhandled exception."""
        from af.app import main

        result = cli_runner.invoke(main, ["failing-command"])
        assert result.exit_code != 0

    def test_error_envelope_json_on_stderr(self) -> None:
        """Agent mode emits a valid JSON error envelope with 'error' field.

        In agent mode (AF_AGENT=1), AgentFoxGroup catches unhandled
        exceptions and emits a structured JSON error envelope to stdout.
        """
        from agentfox.io import AgentFoxGroup

        @click.group(cls=AgentFoxGroup)
        def test_cli() -> None:
            pass

        @test_cli.command()
        def boom() -> None:
            raise RuntimeError("test kaboom")

        runner = CliRunner()
        result = runner.invoke(test_cli, ["boom"], env={"AF_AGENT": "1"})
        assert result.exit_code != 0
        error_envelope = json.loads(result.output)
        assert "error" in error_envelope


class TestImportErrorWhenAgentFoxIOMissing:
    """TS-04-E1: ImportError if AgentFoxGroup cannot be imported."""

    def test_import_error_references_agentfox_io(self) -> None:
        """Blocking agentfox.io and reloading af.app raises ImportError."""
        import importlib

        saved = sys.modules.get("agentfox.io")
        sys.modules["agentfox.io"] = None  # type: ignore[assignment]
        try:
            import af.app

            with pytest.raises(ImportError, match="agentfox.io"):
                importlib.reload(af.app)
        finally:
            if saved is not None:
                sys.modules["agentfox.io"] = saved
            else:
                sys.modules.pop("agentfox.io", None)
