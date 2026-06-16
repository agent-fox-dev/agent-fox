"""Shared fixtures for af CLI test suite."""

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
