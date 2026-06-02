"""Unit tests for concrete stream implementations (fix-pipeline only).

Test Spec: TS-85-18, TS-85-21
Requirements: 85-REQ-6.1, 85-REQ-7.1
"""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_config(
    platform_type: str = "github",
) -> MagicMock:
    """Create a mock config."""
    config = MagicMock()
    config.platform.type = platform_type
    ns = MagicMock()
    ns.issue_check_interval = 900
    config.night_shift = ns
    return config


# ---------------------------------------------------------------------------
# TS-85-18: CLI --no-fixes disables fix-pipeline
# Requirement: 85-REQ-6.1
# ---------------------------------------------------------------------------


class TestCliNoFixesFlag:
    """Verify --no-fixes flag disables fix-pipeline stream."""

    def test_no_fixes_disables_fix_pipeline(self) -> None:
        """--no-fixes disables fix pipeline."""
        from agent_fox.nightshift.streams import build_streams

        config = _make_config()
        streams = build_streams(config, no_fixes=True)
        assert len(streams) == 1
        assert streams[0].name == "fix-pipeline"
        assert streams[0].enabled is False

    def test_default_enables_fix_pipeline(self) -> None:
        """Default (no flags) enables fix pipeline."""
        from agent_fox.nightshift.streams import build_streams

        config = _make_config()
        streams = build_streams(config, no_fixes=False)
        assert len(streams) == 1
        assert streams[0].name == "fix-pipeline"
        assert streams[0].enabled is True


# ---------------------------------------------------------------------------
# TS-85-21: Platform none disables fix-pipeline
# Requirement: 85-REQ-7.1
# ---------------------------------------------------------------------------


class TestPlatformDegradation:
    """Verify platform.type='none' disables fix-pipeline stream."""

    def test_fix_pipeline_disabled_when_no_platform(self) -> None:
        """Fix pipeline disabled when platform is none."""
        from agent_fox.nightshift.streams import build_streams

        config = _make_config(platform_type="none")
        streams = build_streams(config)
        assert len(streams) == 1
        assert streams[0].name == "fix-pipeline"
        assert streams[0].enabled is False


# ---------------------------------------------------------------------------
# TS-85-28: EngineWorkStream delegates to engine
# ---------------------------------------------------------------------------


class TestEngineWorkStreamDelegation:
    """Verify EngineWorkStream delegates run_once to the engine."""

    async def test_engine_work_stream_calls_drain_issues(self) -> None:
        """EngineWorkStream.run_once calls engine method."""
        from agent_fox.nightshift.daemon import SharedBudget
        from agent_fox.nightshift.streams import EngineWorkStream

        mock_engine = MagicMock()
        mock_engine._drain_issues = AsyncMock(return_value=False)
        mock_engine.state.total_cost = 0.0

        budget = SharedBudget(max_cost=None)
        stream = EngineWorkStream(
            stream_name="fix-pipeline",
            engine=mock_engine,
            method_name="_drain_issues",
            budget=budget,
            enabled=True,
            interval=900,
        )
        await stream.run_once()
        mock_engine._drain_issues.assert_awaited_once()
