"""Unit tests for night-shift display integration.

Test Spec: TS-81-6, TS-81-7, TS-81-10, TS-81-11, TS-81-12, TS-81-13,
           TS-81-14, TS-81-15, TS-81-16, TS-81-E4, TS-81-E5, TS-81-E7,
           TS-81-E8
Requirements: 81-REQ-2.1, 81-REQ-2.2, 81-REQ-3.1, 81-REQ-3.2, 81-REQ-3.3,
              81-REQ-3.4, 81-REQ-3.5, 81-REQ-4.1, 81-REQ-4.2, 81-REQ-4.E1
"""

from __future__ import annotations

from io import StringIO
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from afissues.protocol import IssueResult
from agentfox.maintenance.engine import NightShiftEngine
from agentfox.ui.progress import ProgressDisplay


def _make_config(
    *,
    issue_interval: int = 900,
    max_cost: float | None = None,
    max_sessions: int | None = None,
) -> MagicMock:
    config = MagicMock()
    config.night_shift.issue_check_interval = issue_interval
    config.orchestrator.max_cost = max_cost
    config.orchestrator.max_sessions = max_sessions
    return config


def _make_issue(number: int = 42, title: str = "Fix bug") -> IssueResult:
    return IssueResult(
        number=number,
        title=title,
        html_url=f"https://github.com/test/repo/issues/{number}",
        body="Detailed bug description.",
    )


def _make_theme(*, force_terminal: bool = True, width: int = 120) -> tuple:
    """Create an AppTheme with a StringIO-backed console for testing."""
    from agentfox.core.config import ThemeConfig
    from agentfox.ui.display import create_theme
    from rich.console import Console
    from rich.theme import Theme

    _STYLE_ROLES = ("header", "success", "error", "warning", "info", "tool", "muted")
    config = ThemeConfig()
    theme = create_theme(config)
    buf = StringIO()
    rich_theme = Theme({role: getattr(config, role) for role in _STYLE_ROLES})
    theme.console = Console(file=buf, theme=rich_theme, width=width, force_terminal=force_terminal)
    return theme, buf


# ---------------------------------------------------------------------------
# TS-81-10: Phase line emitted on issue check start
# Requirement: 81-REQ-3.1
# ---------------------------------------------------------------------------


class TestPhaseLineIssueCheck:
    """Verify status line emitted when engine starts an issue check."""

    @pytest.mark.asyncio
    async def test_81_phase_line_issue_check(self) -> None:
        """Status line contains 'af:fix' or 'issue' on issue check."""
        lines: list[tuple[str, str]] = []

        config = _make_config()
        platform = AsyncMock()
        platform.list_issues_by_label = AsyncMock(return_value=[])

        engine = NightShiftEngine(
            config=config,
            platform=platform,
            status_callback=lambda text, style: lines.append((text, style)),
        )

        await engine._run_issue_check()

        assert any("af:fix" in line or "issue" in line.lower() for line, _ in lines), (
            f"Expected phase line mentioning af:fix or issue, got: {lines}"
        )


# ---------------------------------------------------------------------------
# TS-81-13: Phase line on successful fix
# Requirement: 81-REQ-3.4
# ---------------------------------------------------------------------------


class TestPhaseLineFixComplete:
    """Verify permanent line emitted on successful fix."""

    @pytest.mark.asyncio
    async def test_81_phase_line_fix_complete(self) -> None:
        """Status line contains issue number on successful fix."""
        lines: list[tuple[str, str]] = []

        config = _make_config()
        platform = AsyncMock()

        engine = NightShiftEngine(
            config=config,
            platform=platform,
            status_callback=lambda text, style: lines.append((text, style)),
        )

        issue = _make_issue(number=42)
        mock_metrics = MagicMock(
            sessions_run=3,
            input_tokens=100,
            output_tokens=50,
            cache_read_input_tokens=0,
            cache_creation_input_tokens=0,
        )

        with patch("agentfox.maintenance.engine.FixPipeline") as MockPipeline:
            mock_instance = AsyncMock()
            mock_instance.process_issue = AsyncMock(return_value=mock_metrics)
            MockPipeline.return_value = mock_instance

            await engine._process_fix(issue)

        assert any("#42" in line for line, _ in lines), f"Expected #42 in phase lines, got: {lines}"


# ---------------------------------------------------------------------------
# TS-81-14: Phase line on failed fix
# Requirement: 81-REQ-3.5
# ---------------------------------------------------------------------------


class TestPhaseLineFixFailed:
    """Verify permanent line emitted on failed fix."""

    @pytest.mark.asyncio
    async def test_81_phase_line_fix_failed(self) -> None:
        """Status line contains issue number and 'failed' on fix failure."""
        lines: list[tuple[str, str]] = []

        config = _make_config()
        platform = AsyncMock()

        engine = NightShiftEngine(
            config=config,
            platform=platform,
            status_callback=lambda text, style: lines.append((text, style)),
        )

        issue = _make_issue(number=42)

        with patch("agentfox.maintenance.engine.FixPipeline") as MockPipeline:
            mock_instance = AsyncMock()
            mock_instance.process_issue = AsyncMock(side_effect=RuntimeError("boom"))
            MockPipeline.return_value = mock_instance

            await engine._process_fix(issue)

        assert any("#42" in line and "fail" in line.lower() for line, _ in lines), (
            f"Expected #42 and 'failed' in phase lines, got: {lines}"
        )


# ---------------------------------------------------------------------------
# TS-81-E7: Quiet mode suppresses phase lines
# Requirement: 81-REQ-3.E1
# ---------------------------------------------------------------------------


class TestPhaseLineQuiet:
    """Verify phase lines suppressed in quiet mode."""

    @pytest.mark.asyncio
    async def test_81_phase_lines_quiet(self) -> None:
        """No output when ProgressDisplay is in quiet mode."""
        theme, buf = _make_theme()
        progress = ProgressDisplay(theme, quiet=True)
        progress.start()

        config = _make_config()
        platform = AsyncMock()
        platform.list_issues_by_label = AsyncMock(return_value=[])

        engine = NightShiftEngine(
            config=config,
            platform=platform,
            status_callback=progress.print_status,
        )

        await engine._run_issue_check()

        progress.stop()
        assert buf.getvalue() == "", f"Expected no output in quiet mode, got: {buf.getvalue()!r}"


# ---------------------------------------------------------------------------
# TS-81-P8: Phase line emission completeness (property test)
# Validates: 81-REQ-3.1, 81-REQ-3.2, 81-REQ-3.3, 81-REQ-3.4, 81-REQ-3.5
# ---------------------------------------------------------------------------


class TestPropPhaseLineEmission:
    """Every phase transition produces exactly one status line."""

    @pytest.mark.asyncio
    async def test_81_prop_issue_check_emits_one_line(self) -> None:
        """Issue check emits exactly one phase line on entry."""
        lines: list[tuple[str, str]] = []

        config = _make_config()
        platform = AsyncMock()
        platform.list_issues_by_label = AsyncMock(return_value=[])

        engine = NightShiftEngine(
            config=config,
            platform=platform,
            status_callback=lambda text, style: lines.append((text, style)),
        )

        await engine._run_issue_check()

        # Exactly one phase line for issue check entry
        issue_lines = [(t, s) for t, s in lines if "af:fix" in t or "issue" in t.lower()]
        assert len(issue_lines) == 1

    @pytest.mark.asyncio
    async def test_81_prop_fix_emits_start_and_result(self) -> None:
        """Fix processing emits start and completion/failure lines."""
        lines: list[tuple[str, str]] = []

        config = _make_config()
        platform = AsyncMock()

        engine = NightShiftEngine(
            config=config,
            platform=platform,
            status_callback=lambda text, style: lines.append((text, style)),
        )

        issue = _make_issue(number=77)
        mock_metrics = MagicMock(
            sessions_run=3,
            input_tokens=100,
            output_tokens=50,
            cache_read_input_tokens=0,
            cache_creation_input_tokens=0,
        )

        with patch("agentfox.maintenance.engine.FixPipeline") as MockPipeline:
            mock_instance = AsyncMock()
            mock_instance.process_issue = AsyncMock(return_value=mock_metrics)
            MockPipeline.return_value = mock_instance

            await engine._process_fix(issue)

        start_lines = [(t, s) for t, s in lines if "#77" in t and "Fixing" in t]
        result_lines = [(t, s) for t, s in lines if "#77" in t and ("fixed" in t.lower() or "failed" in t.lower())]
        assert len(start_lines) == 1
        assert len(result_lines) == 1
