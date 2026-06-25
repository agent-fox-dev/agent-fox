"""Shared fixtures for nightshift CLI test suite.

Fixture migration notes (07-REQ-8.4 / TS-07-37):
  - _reset_agent_fox_logger: shared with af tests, COPIED here
  - cli_runner: shared with af tests, COPIED here
  - cli_runner_separated: shared with af tests, COPIED here
  - hypothesis CI profile: shared with af tests, COPIED here
"""

from __future__ import annotations

import json as json_mod
import logging
from collections.abc import Generator
from unittest.mock import MagicMock, patch

import click
import pytest
from click.testing import CliRunner
from hypothesis import settings

settings.register_profile("ci", deadline=None)
settings.load_profile("ci")


def _make_mock_config() -> MagicMock:
    """Create a minimal mock config for nightshift CLI tests."""
    config = MagicMock()
    config.theme = None
    config.orchestrator.max_cost = 10.0
    config.knowledge = MagicMock()
    config.night_shift = MagicMock()
    config.night_shift.issue_check_interval = 900
    config.night_shift.push_fix_branch = False
    config.platform.type = "github"
    return config


@pytest.fixture(autouse=True)
def _reset_agent_fox_logger() -> Generator[None, None, None]:
    """Reset the agentfox logger after each test."""
    yield
    agent_logger = logging.getLogger("agentfox")
    agent_logger.setLevel(logging.NOTSET)
    agent_logger.handlers.clear()


@pytest.fixture(autouse=True)
def _mock_config_loading():
    """Mock config loading so tests don't need a real config file.

    The standalone nightshift CLI calls ``load_config()`` on startup.
    Without mocking, tests that invoke the CLI via CliRunner would fail
    trying to read ``.agent-fox/config.toml`` from the filesystem.
    """
    with patch("nightshift.app.load_config", return_value=_make_mock_config()):
        yield


@pytest.fixture(autouse=True)
def _mock_daemon():
    """Mock the daemon so CLI tests don't block on ``asyncio.run(runner.run())``.

    ``_run_daemon()`` starts the real daemon loop which blocks indefinitely.
    This fixture replaces it with a mock that simulates the essential output
    (startup message, summary stats, JSONL events) without starting the daemon.
    """

    def _fake_run_daemon(ctx, om, config):  # noqa: ARG001
        click.echo("Night-shift daemon starting. Press Ctrl-C to stop gracefully.")
        if om.json_mode:
            # Emit JSONL: one JSON object per line (not pretty-printed).
            click.echo(json_mod.dumps({"status": "stopped", "issues_fixed": 0, "total_cost": 0.0}))
        else:
            click.echo("Night-shift stopped. Issues fixed: 0, Total cost: $0.0000")

    with patch("nightshift.app._run_daemon", side_effect=_fake_run_daemon):
        yield


@pytest.fixture
def cli_runner() -> CliRunner:
    """Provide a Click CLI test runner."""
    return CliRunner()


@pytest.fixture
def cli_runner_separated() -> CliRunner:
    """Provide a Click CLI test runner with separated stdout/stderr.

    Uses ``mix_stderr=False`` so that ``result.output`` captures stdout
    and ``result.stderr`` captures stderr independently.
    """
    return CliRunner(mix_stderr=False)
