"""Unit tests for NightShiftConfig.

Test Spec: TS-61-26, TS-61-E12
Requirements: 61-REQ-9.1, 61-REQ-9.E1
"""

from __future__ import annotations

# ---------------------------------------------------------------------------
# TS-61-26: NightShiftConfig defaults
# Requirement: 61-REQ-9.1
# ---------------------------------------------------------------------------


class TestNightShiftConfigDefaults:
    """Verify default config values."""

    def test_default_issue_check_interval(self) -> None:
        """issue_check_interval defaults to 900."""
        from agent_fox.core.config import NightShiftConfig

        cfg = NightShiftConfig()
        assert cfg.issue_check_interval == 900


# ---------------------------------------------------------------------------
# TS-61-E12: Interval clamped to minimum
# Requirement: 61-REQ-9.E1
# ---------------------------------------------------------------------------


class TestIntervalClamping:
    """Verify that intervals < 60s are clamped to 60."""

    def test_issue_check_interval_clamped(self) -> None:
        """issue_check_interval of 10 is clamped to 60."""
        from agent_fox.core.config import NightShiftConfig

        cfg = NightShiftConfig(issue_check_interval=10)
        assert cfg.issue_check_interval == 60

    def test_interval_at_boundary_not_clamped(self) -> None:
        """An interval of exactly 60 is not changed."""
        from agent_fox.core.config import NightShiftConfig

        cfg = NightShiftConfig(issue_check_interval=60)
        assert cfg.issue_check_interval == 60

    def test_interval_above_minimum_not_clamped(self) -> None:
        """An interval above 60 is not changed."""
        from agent_fox.core.config import NightShiftConfig

        cfg = NightShiftConfig(issue_check_interval=120)
        assert cfg.issue_check_interval == 120
