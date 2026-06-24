"""Shared fixtures for nightshift CLI test suite.

Fixture migration notes (07-REQ-8.4 / TS-07-37):
  - _reset_agent_fox_logger: shared with af tests, COPIED here
  - cli_runner: shared with af tests, COPIED here
  - cli_runner_separated: shared with af tests, COPIED here
  - hypothesis CI profile: shared with af tests, COPIED here
"""

from __future__ import annotations

import logging
from collections.abc import Generator

import pytest
from click.testing import CliRunner
from hypothesis import settings

settings.register_profile("ci", deadline=None)
settings.load_profile("ci")


@pytest.fixture(autouse=True)
def _reset_agent_fox_logger() -> Generator[None, None, None]:
    """Reset the agentfox logger after each test."""
    yield
    agent_logger = logging.getLogger("agentfox")
    agent_logger.setLevel(logging.NOTSET)
    agent_logger.handlers.clear()


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
