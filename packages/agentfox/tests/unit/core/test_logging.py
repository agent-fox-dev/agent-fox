"""Unit tests for agent-fox logging configuration.

Covers setup_logging() verbosity tiers.

Requirements: 01-REQ-6.1, 01-REQ-6.2, 01-REQ-6.3, 01-REQ-6.E1
"""

from __future__ import annotations

import logging

import pytest

# ---------------------------------------------------------------------------
# setup_logging() verbosity tiers
# ---------------------------------------------------------------------------


class TestSetupLoggingTiers:
    """setup_logging() sets the correct level for each verbosity tier."""

    def _get_agent_fox_level(self) -> int:
        return logging.getLogger("agentfox").level

    def test_default_level_is_warning(self) -> None:
        """No flags → WARNING level."""
        from agentfox.core.logging import setup_logging

        setup_logging(verbose=False, quiet=False)
        assert self._get_agent_fox_level() == logging.WARNING

    def test_verbose_sets_debug(self) -> None:
        """--verbose → DEBUG level."""
        from agentfox.core.logging import setup_logging

        setup_logging(verbose=True, quiet=False)
        assert self._get_agent_fox_level() == logging.DEBUG

    def test_quiet_sets_error(self) -> None:
        """--quiet → ERROR level."""
        from agentfox.core.logging import setup_logging

        setup_logging(verbose=False, quiet=True)
        assert self._get_agent_fox_level() == logging.ERROR

    def test_verbose_wins_over_quiet(self) -> None:
        """--verbose --quiet → DEBUG level (01-REQ-6.E1: most info wins)."""
        from agentfox.core.logging import setup_logging

        setup_logging(verbose=True, quiet=True)
        assert self._get_agent_fox_level() == logging.DEBUG

    def test_setup_logging_rejects_trace_kwarg(self) -> None:
        """setup_logging(trace=True) raises TypeError — trace param removed."""
        from agentfox.core.logging import setup_logging

        with pytest.raises(TypeError):
            setup_logging(trace=True)  # type: ignore[call-arg]


# ---------------------------------------------------------------------------
# TRACE constant removed
# ---------------------------------------------------------------------------


class TestTraceConstantRemoved:
    """TRACE constant no longer exported from agentfox.core.logging."""

    def test_trace_import_raises_import_error(self) -> None:
        """from agentfox.core.logging import TRACE must raise ImportError."""
        with pytest.raises(ImportError):
            from agentfox.core.logging import TRACE  # noqa: F401

    def test_level_5_not_named_trace(self) -> None:
        """logging.getLevelName(5) must NOT return 'TRACE' after module import."""
        import agentfox.core.logging  # noqa: F401

        name = logging.getLevelName(5)
        assert name != "TRACE", f"Expected level 5 not to be 'TRACE', got {name!r}"
