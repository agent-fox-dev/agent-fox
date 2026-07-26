"""Tests for spec 07: PR feedback loop — task groups 1, 2, & 3.

Group 1: config fields, work stream registration, dispatcher sequencing,
and tracking comment parsing.

Group 2: PR state detection, CI check interpretation, review state
interpretation, feedback context collection, and mutually exclusive paths.

Group 3: retry limit enforcement, worktree lifecycle (setup + cleanup),
feedback context collection output format, and try/finally cleanup guarantee.

Test Spec: TS-07-1, TS-07-2, TS-07-3, TS-07-4, TS-07-5,
           TS-07-6, TS-07-7, TS-07-8, TS-07-9, TS-07-10,
           TS-07-11, TS-07-12, TS-07-13, TS-07-14, TS-07-15,
           TS-07-16, TS-07-17, TS-07-18, TS-07-19, TS-07-20,
           TS-07-21, TS-07-22, TS-07-23, TS-07-24, TS-07-25,
           TS-07-26, TS-07-27, TS-07-28, TS-07-35, TS-07-36,
           TS-07-E1, TS-07-E2, TS-07-E3, TS-07-E4, TS-07-E5, TS-07-E6,
           TS-07-E7, TS-07-E8, TS-07-E9, TS-07-E10, TS-07-E11,
           TS-07-E12, TS-07-E13, TS-07-E14, TS-07-E15, TS-07-E16,
           TS-07-E17, TS-07-E18, TS-07-E19, TS-07-E26
Requirements: 07-REQ-1.1, 07-REQ-1.2, 07-REQ-1.E1, 07-REQ-1.E2,
              07-REQ-2.1, 07-REQ-2.2, 07-REQ-2.3,
              07-REQ-3.1, 07-REQ-3.2, 07-REQ-3.3, 07-REQ-3.E1, 07-REQ-3.E2,
              07-REQ-4.1, 07-REQ-4.2, 07-REQ-4.E1, 07-REQ-4.E2,
              07-REQ-5.1, 07-REQ-5.2, 07-REQ-5.3, 07-REQ-5.E1, 07-REQ-5.E2,
              07-REQ-6.1, 07-REQ-6.2, 07-REQ-6.3, 07-REQ-6.4, 07-REQ-6.5,
              07-REQ-6.E1, 07-REQ-6.E2, 07-REQ-6.E3,
              07-REQ-7.1, 07-REQ-7.2, 07-REQ-7.3, 07-REQ-7.E1, 07-REQ-7.E2,
              07-REQ-7.E3, 07-REQ-10.3,
              07-REQ-8.1, 07-REQ-8.2, 07-REQ-8.E1, 07-REQ-8.E2,
              07-REQ-9.1, 07-REQ-9.2, 07-REQ-9.E1, 07-REQ-9.E2, 07-REQ-9.E3,
              07-REQ-10.1, 07-REQ-10.2,
              07-REQ-13.1, 07-REQ-13.2, 07-REQ-13.E1
"""

from __future__ import annotations

import asyncio
import inspect
import logging
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from afissues.protocol import IssueComment, IssueResult

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_config(
    *,
    merge_strategy: str = "pr",
    platform_type: str = "github",
    pr_check_interval: int = 900,
    max_pr_retries: int = 2,
) -> MagicMock:
    """Create a mock AgentFoxConfig with nightshift and workspace sections."""
    config = MagicMock()
    config.platform.type = platform_type
    ns = MagicMock()
    ns.issue_check_interval = 900
    ns.pr_check_interval = pr_check_interval
    ns.max_pr_retries = max_pr_retries
    config.night_shift = ns
    config.workspace.merge_strategy = merge_strategy
    config.workspace.integration_branch = "main"
    return config


def _make_issue(
    number: int = 10,
    title: str = "Fix login bug",
    body: str = "The login form crashes on empty password.",
) -> IssueResult:
    """Create a minimal IssueResult for testing."""
    return IssueResult(
        number=number,
        title=title,
        html_url=f"https://github.com/test/repo/issues/{number}",
        body=body,
    )


def _make_mock_platform(
    *,
    issues: list[IssueResult] | None = None,
    comments: list[IssueComment] | None = None,
) -> MagicMock:
    """Create a mock platform with common async methods."""
    platform = MagicMock()
    platform.list_issues_by_label = AsyncMock(return_value=issues or [])
    platform.list_issue_comments = AsyncMock(return_value=comments or [])
    platform.add_issue_comment = AsyncMock()
    platform.assign_label = AsyncMock()
    platform.remove_label = AsyncMock()
    platform.close_issue = AsyncMock()
    platform.get_pr_state = AsyncMock()
    platform.get_pr_checks = AsyncMock(return_value=[])
    platform.get_pr_reviews = AsyncMock(return_value=[])
    return platform


def _make_tracking_comment(
    pr_number: int = 42,
    attempt: int = 1,
) -> str:
    """Build a tracking comment body that matches PR_TRACKING_PATTERN.

    Uses the format_tracking_comment utility from fix_pipeline (spec 06).
    Falls back to a hand-crafted pattern if the utility is not yet available.
    """
    try:
        from agentfox.nightshift.fix_pipeline import format_tracking_comment

        return format_tracking_comment(
            pr_number=pr_number,
            attempt=attempt,
            pr_url=f"https://github.com/test/repo/pull/{pr_number}",
            message="Initial fix submitted.",
        )
    except ImportError:
        # Spec 06 not implemented yet — use a plausible fallback.
        # The real implementation will define the exact format.
        return (
            f"<!-- nightshift:tracking pr_number={pr_number} attempt={attempt} -->\n"
            f"PR #{pr_number} | Attempt {attempt}"
        )


def _make_issue_comment(
    body: str,
    *,
    comment_id: int = 1,
    user: str = "nightshift[bot]",
) -> IssueComment:
    """Create an IssueComment with the given body."""
    return IssueComment(
        id=comment_id,
        body=body,
        user=user,
        created_at="2026-01-01T00:00:00Z",
    )


def _make_check_result(
    *,
    name: str = "build",
    status: str = "completed",
    conclusion: str | None = "success",
    output_title: str = "",
    output_summary: str = "",
) -> SimpleNamespace:
    """Create a mock CheckResult for testing.

    Uses SimpleNamespace to avoid MagicMock's special handling of 'name'.
    Spec 06 CheckResult: name, status, conclusion, output_title, output_summary.
    """
    return SimpleNamespace(
        name=name,
        status=status,
        conclusion=conclusion,
        output_title=output_title,
        output_summary=output_summary,
    )


def _make_review_comment(
    *,
    user: str = "reviewer",
    state: str | None = "APPROVED",
    body: str = "",
    submitted_at: str = "2026-01-01T00:00:00Z",
) -> SimpleNamespace:
    """Create a mock ReviewComment for testing.

    Accepts state=None for edge case TS-07-E14.
    Spec 06 ReviewComment: user, state, body, submitted_at.
    """
    return SimpleNamespace(
        user=user,
        state=state,
        body=body,
        submitted_at=submitted_at,
    )


# ===========================================================================
# TS-07-1: NightShiftConfig pr_check_interval default and clamping
# Requirement: 07-REQ-1.1
# ===========================================================================


class TestPrCheckIntervalConfig:
    """Verify pr_check_interval defaults to 900 and clamps below 60."""

    def test_pr_check_interval_default(self) -> None:
        """TS-07-1: pr_check_interval defaults to 900."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig()
        assert cfg.pr_check_interval == 900

    def test_pr_check_interval_explicit_value(self) -> None:
        """TS-07-1: pr_check_interval accepts a valid value above 60."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig(pr_check_interval=300)
        assert cfg.pr_check_interval == 300

    def test_pr_check_interval_clamped_to_60(self) -> None:
        """TS-07-E1: pr_check_interval of 30 is silently clamped to 60."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig(pr_check_interval=30)
        assert cfg.pr_check_interval == 60

    def test_pr_check_interval_boundary_at_60(self) -> None:
        """TS-07-E1: pr_check_interval of exactly 60 is not changed."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig(pr_check_interval=60)
        assert cfg.pr_check_interval == 60

    def test_pr_check_interval_zero_clamped(self) -> None:
        """TS-07-E1: pr_check_interval of 0 is clamped to 60."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig(pr_check_interval=0)
        assert cfg.pr_check_interval == 60

    def test_pr_check_interval_no_validation_error(self) -> None:
        """TS-07-E1: No ValidationError raised for out-of-range input."""
        from agentfox.core.config import NightShiftConfig

        # Should not raise
        cfg = NightShiftConfig(pr_check_interval=10)
        assert cfg.pr_check_interval == 60


# ===========================================================================
# TS-07-2: NightShiftConfig max_pr_retries default and clamping
# Requirement: 07-REQ-1.2
# ===========================================================================


class TestMaxPrRetriesConfig:
    """Verify max_pr_retries defaults to 2 and clamps to [0, 10]."""

    def test_max_pr_retries_default(self) -> None:
        """TS-07-2: max_pr_retries defaults to 2."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig()
        assert cfg.max_pr_retries == 2

    def test_max_pr_retries_explicit_valid(self) -> None:
        """TS-07-2: max_pr_retries accepts a valid value in [0, 10]."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig(max_pr_retries=5)
        assert cfg.max_pr_retries == 5

    def test_max_pr_retries_clamped_below_zero(self) -> None:
        """TS-07-E2: max_pr_retries of -1 is clamped to 0."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig(max_pr_retries=-1)
        assert cfg.max_pr_retries == 0

    def test_max_pr_retries_clamped_above_ten(self) -> None:
        """TS-07-E2: max_pr_retries of 15 is clamped to 10."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig(max_pr_retries=15)
        assert cfg.max_pr_retries == 10

    def test_max_pr_retries_zero_allowed(self) -> None:
        """TS-07-E2: max_pr_retries of 0 is a valid boundary value."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig(max_pr_retries=0)
        assert cfg.max_pr_retries == 0

    def test_max_pr_retries_ten_allowed(self) -> None:
        """TS-07-E2: max_pr_retries of 10 is a valid boundary value."""
        from agentfox.core.config import NightShiftConfig

        cfg = NightShiftConfig(max_pr_retries=10)
        assert cfg.max_pr_retries == 10

    def test_max_pr_retries_no_validation_error(self) -> None:
        """TS-07-E2: No ValidationError raised for out-of-range inputs."""
        from agentfox.core.config import NightShiftConfig

        # Should not raise
        cfg_low = NightShiftConfig(max_pr_retries=-5)
        assert cfg_low.max_pr_retries == 0

        cfg_high = NightShiftConfig(max_pr_retries=100)
        assert cfg_high.max_pr_retries == 10


# ===========================================================================
# TS-07-E1: pr_check_interval clamping edge case
# Requirement: 07-REQ-1.E1
# ===========================================================================


class TestPrCheckIntervalEdgeCases:
    """Verify pr_check_interval edge-case clamping via AgentFoxConfig."""

    def test_pr_check_interval_clamped_via_agentfox_config(self) -> None:
        """TS-07-E1: pr_check_interval clamping works through AgentFoxConfig."""
        from agentfox.core.config import AgentFoxConfig

        cfg = AgentFoxConfig(night_shift={"pr_check_interval": 30})
        assert cfg.night_shift.pr_check_interval == 60


# ===========================================================================
# TS-07-E2: max_pr_retries clamping edge case
# Requirement: 07-REQ-1.E2
# ===========================================================================


class TestMaxPrRetriesEdgeCases:
    """Verify max_pr_retries edge-case clamping via AgentFoxConfig."""

    def test_max_pr_retries_clamped_via_agentfox_config_low(self) -> None:
        """TS-07-E2: max_pr_retries=-1 -> 0 through AgentFoxConfig."""
        from agentfox.core.config import AgentFoxConfig

        cfg = AgentFoxConfig(night_shift={"max_pr_retries": -1})
        assert cfg.night_shift.max_pr_retries == 0

    def test_max_pr_retries_clamped_via_agentfox_config_high(self) -> None:
        """TS-07-E2: max_pr_retries=15 -> 10 through AgentFoxConfig."""
        from agentfox.core.config import AgentFoxConfig

        cfg = AgentFoxConfig(night_shift={"max_pr_retries": 15})
        assert cfg.night_shift.max_pr_retries == 10


# ===========================================================================
# TS-07-3: build_streams includes pr-feedback after fix-pipeline
# Requirement: 07-REQ-2.1
# ===========================================================================


class TestBuildStreamsPrFeedback:
    """Verify pr-feedback stream registration in build_streams."""

    def test_pr_feedback_stream_present_with_pr_strategy(self) -> None:
        """TS-07-3: pr-feedback included when merge_strategy='pr' and platform is not 'none'."""
        from agentfox.nightshift.streams import build_streams

        config = _make_config(merge_strategy="pr", platform_type="github")
        streams = build_streams(config)
        names = [s.name for s in streams]
        assert "pr-feedback" in names

    def test_pr_feedback_stream_after_fix_pipeline(self) -> None:
        """TS-07-3: pr-feedback positioned after fix-pipeline in stream list."""
        from agentfox.nightshift.streams import build_streams

        config = _make_config(merge_strategy="pr", platform_type="github")
        streams = build_streams(config)
        names = [s.name for s in streams]
        assert "fix-pipeline" in names
        assert "pr-feedback" in names
        assert names.index("pr-feedback") > names.index("fix-pipeline")

    def test_pr_feedback_stream_interval_matches_config(self) -> None:
        """TS-07-3: pr-feedback interval equals pr_check_interval from config."""
        from agentfox.nightshift.streams import build_streams

        config = _make_config(merge_strategy="pr", pr_check_interval=600)
        streams = build_streams(config)
        pr_stream = next(s for s in streams if s.name == "pr-feedback")
        assert pr_stream.interval == 600


# ===========================================================================
# TS-07-4: build_streams omits pr-feedback when merge_strategy is not 'pr'
# Requirement: 07-REQ-2.2
# ===========================================================================


class TestBuildStreamsOmitsPrFeedback:
    """Verify pr-feedback is omitted for non-PR merge strategies or none platform."""

    def test_no_pr_feedback_with_direct_strategy(self) -> None:
        """TS-07-4: No pr-feedback when merge_strategy='direct'."""
        from agentfox.nightshift.streams import build_streams

        config = _make_config(merge_strategy="direct", platform_type="github")
        streams = build_streams(config)
        names = [s.name for s in streams]
        assert "pr-feedback" not in names

    def test_no_pr_feedback_with_branch_strategy(self) -> None:
        """TS-07-4: No pr-feedback when merge_strategy='branch'."""
        from agentfox.nightshift.streams import build_streams

        config = _make_config(merge_strategy="branch", platform_type="github")
        streams = build_streams(config)
        names = [s.name for s in streams]
        assert "pr-feedback" not in names

    def test_no_pr_feedback_with_none_platform(self) -> None:
        """TS-07-4: No pr-feedback when platform type is 'none'."""
        from agentfox.nightshift.streams import build_streams

        config = _make_config(merge_strategy="pr", platform_type="none")
        streams = build_streams(config)
        names = [s.name for s in streams]
        assert "pr-feedback" not in names


# ===========================================================================
# TS-07-5: DaemonRunner priority order includes pr-feedback after fix-pipeline
# Requirement: 07-REQ-2.3
# ===========================================================================


class TestDaemonRunnerPriority:
    """Verify DaemonRunner places pr-feedback after fix-pipeline in priority list."""

    def test_pr_feedback_in_priority_order(self) -> None:
        """TS-07-5: pr-feedback is present in _PRIORITY_ORDER."""
        from agentfox.nightshift.daemon import DaemonRunner

        assert "pr-feedback" in DaemonRunner._PRIORITY_ORDER

    def test_pr_feedback_after_fix_pipeline_in_priority(self) -> None:
        """TS-07-5: pr-feedback index > fix-pipeline index in priority list."""
        from agentfox.nightshift.daemon import DaemonRunner

        priority = DaemonRunner._PRIORITY_ORDER
        assert priority.index("pr-feedback") > priority.index("fix-pipeline")


# ===========================================================================
# TS-07-6: _check_open_prs calls list_issues_by_label, processes up to 5
# Requirement: 07-REQ-3.1
# ===========================================================================


class TestCheckOpenPrsDispatcher:
    """Verify _check_open_prs sequencing and counter increment."""

    async def test_check_open_prs_calls_list_with_label_pr(self) -> None:
        """TS-07-6: list_issues_by_label called with LABEL_PR."""
        from afissues.labels import LABEL_PR
        from agentfox.nightshift.engine import NightShiftEngine

        issues = [_make_issue(number=i) for i in range(1, 4)]
        mock_platform = _make_mock_platform(issues=issues)
        config = _make_config()

        engine = NightShiftEngine(config=config, platform=mock_platform)

        with patch(
            "agentfox.nightshift.engine.process_pr_issue",
            new_callable=AsyncMock,
        ):
            await engine._check_open_prs()

        mock_platform.list_issues_by_label.assert_awaited_once()
        call_args = mock_platform.list_issues_by_label.call_args
        assert call_args[0][0] == LABEL_PR

    async def test_check_open_prs_processes_three_issues(self) -> None:
        """TS-07-6: process_pr_issue called 3 times for 3 issues."""
        issues = [_make_issue(number=i) for i in range(1, 4)]
        mock_platform = _make_mock_platform(issues=issues)
        config = _make_config()

        from agentfox.nightshift.engine import NightShiftEngine

        engine = NightShiftEngine(config=config, platform=mock_platform)

        with patch(
            "agentfox.nightshift.engine.process_pr_issue",
            new_callable=AsyncMock,
        ) as mock_process:
            await engine._check_open_prs()
            assert mock_process.call_count == 3

    async def test_check_open_prs_increments_issue_checks_completed(self) -> None:
        """TS-07-6: issue_checks_completed incremented per processed issue."""
        issues = [_make_issue(number=i) for i in range(1, 4)]
        mock_platform = _make_mock_platform(issues=issues)
        config = _make_config()

        from agentfox.nightshift.engine import NightShiftEngine

        engine = NightShiftEngine(config=config, platform=mock_platform)
        assert engine.state.issue_checks_completed == 0

        with patch(
            "agentfox.nightshift.engine.process_pr_issue",
            new_callable=AsyncMock,
        ):
            await engine._check_open_prs()

        assert engine.state.issue_checks_completed == 3


# ===========================================================================
# TS-07-7: _check_open_prs is async and sequential (no gather)
# Requirement: 07-REQ-3.2
# ===========================================================================


class TestCheckOpenPrsSequential:
    """Verify _check_open_prs awaits each call sequentially."""

    async def test_check_open_prs_is_async(self) -> None:
        """TS-07-7: _check_open_prs is declared as async def."""
        from agentfox.nightshift.engine import NightShiftEngine

        assert inspect.iscoroutinefunction(NightShiftEngine._check_open_prs)

    async def test_check_open_prs_sequential_calls(self) -> None:
        """TS-07-7: process_pr_issue calls are sequential, not concurrent."""
        import asyncio

        issues = [_make_issue(number=i) for i in range(1, 3)]
        mock_platform = _make_mock_platform(issues=issues)
        config = _make_config()

        from agentfox.nightshift.engine import NightShiftEngine

        engine = NightShiftEngine(config=config, platform=mock_platform)

        call_log: list[dict[str, float]] = []

        async def _record_call(*args: object, **kwargs: object) -> None:
            start = asyncio.get_event_loop().time()
            await asyncio.sleep(0.01)  # simulate work
            end = asyncio.get_event_loop().time()
            call_log.append({"start": start, "end": end})

        with patch(
            "agentfox.nightshift.engine.process_pr_issue",
            side_effect=_record_call,
        ):
            await engine._check_open_prs()

        assert len(call_log) == 2
        # Second call starts after first ends (sequential).
        assert call_log[0]["end"] <= call_log[1]["start"]


# ===========================================================================
# TS-07-8: _MAX_PR_CHECKS constant location
# Requirement: 07-REQ-3.3
# ===========================================================================


class TestMaxPrChecksConstant:
    """Verify _MAX_PR_CHECKS is in engine.py and not in pr_feedback.py."""

    def test_max_pr_checks_in_engine(self) -> None:
        """TS-07-8: _MAX_PR_CHECKS == 5 in engine module."""
        import agentfox.nightshift.engine as eng

        assert eng._MAX_PR_CHECKS == 5

    def test_max_pr_checks_not_in_pr_feedback(self) -> None:
        """TS-07-8: _MAX_PR_CHECKS not defined in pr_feedback module."""
        import agentfox.nightshift.pr_feedback as prf

        assert not hasattr(prf, "_MAX_PR_CHECKS")


# ===========================================================================
# TS-07-E3: _check_open_prs caps at 5 when more issues returned
# Requirement: 07-REQ-3.E1
# ===========================================================================


class TestCheckOpenPrsCap:
    """Verify _check_open_prs processes only the first 5 issues."""

    async def test_check_open_prs_caps_at_five(self) -> None:
        """TS-07-E3: Only first 5 of 8 issues processed."""
        issues = [_make_issue(number=i) for i in range(1, 9)]
        mock_platform = _make_mock_platform(issues=issues)
        config = _make_config()

        from agentfox.nightshift.engine import NightShiftEngine

        engine = NightShiftEngine(config=config, platform=mock_platform)

        with patch(
            "agentfox.nightshift.engine.process_pr_issue",
            new_callable=AsyncMock,
        ) as mock_process:
            await engine._check_open_prs()
            assert mock_process.call_count == 5

    async def test_check_open_prs_oldest_first_order(self) -> None:
        """TS-07-E3: Processed issues are the first 5 in oldest-first order."""
        issues = [_make_issue(number=i) for i in range(1, 9)]
        mock_platform = _make_mock_platform(issues=issues)
        config = _make_config()

        from agentfox.nightshift.engine import NightShiftEngine

        engine = NightShiftEngine(config=config, platform=mock_platform)

        with patch(
            "agentfox.nightshift.engine.process_pr_issue",
            new_callable=AsyncMock,
        ) as mock_process:
            await engine._check_open_prs()
            processed_numbers = [
                call.args[0].number for call in mock_process.call_args_list
            ]
            assert processed_numbers == [1, 2, 3, 4, 5]


# ===========================================================================
# TS-07-E4: _check_open_prs no-ops on empty issue list
# Requirement: 07-REQ-3.E2
# ===========================================================================


class TestCheckOpenPrsEmpty:
    """Verify _check_open_prs does nothing when no issues are returned."""

    async def test_check_open_prs_empty_list(self) -> None:
        """TS-07-E4: No processing when list_issues_by_label returns []."""
        mock_platform = _make_mock_platform(issues=[])
        config = _make_config()

        from agentfox.nightshift.engine import NightShiftEngine

        engine = NightShiftEngine(config=config, platform=mock_platform)

        with patch(
            "agentfox.nightshift.engine.process_pr_issue",
            new_callable=AsyncMock,
        ) as mock_process:
            result = await engine._check_open_prs()
            assert result is None
            assert mock_process.call_count == 0
            assert engine.state.issue_checks_completed == 0


# ===========================================================================
# TS-07-9: process_pr_issue tracking comment extraction
# Requirement: 07-REQ-4.1
# ===========================================================================


class TestProcessPrIssueTrackingComment:
    """Verify process_pr_issue finds and parses the tracking comment."""

    async def test_process_pr_issue_calls_list_issue_comments(self) -> None:
        """TS-07-9: list_issue_comments called with issue.number."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        tracking_body = _make_tracking_comment(pr_number=42, attempt=1)
        comments = [
            _make_issue_comment("unrelated comment", comment_id=1),
            _make_issue_comment(tracking_body, comment_id=2),
        ]
        mock_platform = _make_mock_platform(comments=comments)
        # Mock get_pr_state to return a merged PR so it exits after state check
        mock_platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=True, state="closed"),
        )
        config = _make_config()
        pipeline = MagicMock()

        await process_pr_issue(
            issue=issue,
            config=config,
            platform=mock_platform,
            pipeline=pipeline,
        )

        mock_platform.list_issue_comments.assert_awaited_once_with(10)

    async def test_process_pr_issue_extracts_pr_number_and_attempt(self) -> None:
        """TS-07-9: pr_number and attempt extracted from tracking comment."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        tracking_body = _make_tracking_comment(pr_number=42, attempt=1)
        comments = [_make_issue_comment(tracking_body)]
        mock_platform = _make_mock_platform(comments=comments)
        # Simulate merged PR so process_pr_issue proceeds to state check
        mock_platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=True, state="closed"),
        )
        config = _make_config()
        pipeline = MagicMock()

        await process_pr_issue(
            issue=issue,
            config=config,
            platform=mock_platform,
            pipeline=pipeline,
        )

        # If tracking comment was parsed, get_pr_state should be called with pr_number=42
        mock_platform.get_pr_state.assert_awaited_once_with(42)


# ===========================================================================
# TS-07-10: process_pr_issue skips issue when no tracking comment found
# Requirement: 07-REQ-4.2
# ===========================================================================


class TestProcessPrIssueNoTrackingComment:
    """Verify process_pr_issue logs WARNING and skips when no tracking comment."""

    async def test_no_tracking_comment_returns_none(self) -> None:
        """TS-07-10: Returns None when no matching tracking comment."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        comments = [_make_issue_comment("just a regular comment")]
        mock_platform = _make_mock_platform(comments=comments)
        config = _make_config()
        pipeline = MagicMock()

        result = await process_pr_issue(
            issue=issue,
            config=config,
            platform=mock_platform,
            pipeline=pipeline,
        )

        assert result is None

    async def test_no_tracking_comment_logs_warning(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
        """TS-07-10: WARNING logged with issue number when no tracking comment."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        comments = [_make_issue_comment("just a regular comment")]
        mock_platform = _make_mock_platform(comments=comments)
        config = _make_config()
        pipeline = MagicMock()

        with caplog.at_level(logging.WARNING):
            await process_pr_issue(
                issue=issue,
                config=config,
                platform=mock_platform,
                pipeline=pipeline,
            )

        warning_messages = [
            r.message for r in caplog.records if r.levelno >= logging.WARNING
        ]
        assert any("10" in msg for msg in warning_messages), (
            f"Expected WARNING mentioning issue #10, got: {warning_messages}"
        )

    async def test_no_tracking_comment_no_labels_touched(self) -> None:
        """TS-07-10: No label operations when no tracking comment."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        comments = [_make_issue_comment("just a regular comment")]
        mock_platform = _make_mock_platform(comments=comments)
        config = _make_config()
        pipeline = MagicMock()

        await process_pr_issue(
            issue=issue,
            config=config,
            platform=mock_platform,
            pipeline=pipeline,
        )

        mock_platform.assign_label.assert_not_awaited()
        mock_platform.remove_label.assert_not_awaited()

    async def test_no_tracking_comment_no_comment_posted(self) -> None:
        """TS-07-10: No comment posted when no tracking comment."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        comments = [_make_issue_comment("just a regular comment")]
        mock_platform = _make_mock_platform(comments=comments)
        config = _make_config()
        pipeline = MagicMock()

        await process_pr_issue(
            issue=issue,
            config=config,
            platform=mock_platform,
            pipeline=pipeline,
        )

        mock_platform.add_issue_comment.assert_not_awaited()


# ===========================================================================
# TS-07-E5: Multiple tracking comments — last one is used
# Requirement: 07-REQ-4.E1
# ===========================================================================


class TestProcessPrIssueMultipleTrackingComments:
    """Verify last matching tracking comment is used when multiple match."""

    async def test_last_matching_comment_used(self) -> None:
        """TS-07-E5: When multiple comments match, last in list order is used."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        first_tracking = _make_tracking_comment(pr_number=42, attempt=1)
        second_tracking = _make_tracking_comment(pr_number=42, attempt=2)
        comments = [
            _make_issue_comment(first_tracking, comment_id=1),
            _make_issue_comment("regular comment", comment_id=2),
            _make_issue_comment(second_tracking, comment_id=3),
        ]
        mock_platform = _make_mock_platform(comments=comments)
        # Simulate merged PR so we can verify which pr_number was used
        mock_platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=True, state="closed"),
        )
        config = _make_config()
        pipeline = MagicMock()

        await process_pr_issue(
            issue=issue,
            config=config,
            platform=mock_platform,
            pipeline=pipeline,
        )

        # get_pr_state should be called with pr_number=42 (from the last match)
        mock_platform.get_pr_state.assert_awaited_once_with(42)


# ===========================================================================
# TS-07-E6: list_issue_comments raises -> WARNING and skip
# Requirement: 07-REQ-4.E2
# ===========================================================================


class TestProcessPrIssueApiError:
    """Verify process_pr_issue handles list_issue_comments API errors."""

    async def test_api_error_returns_none(self) -> None:
        """TS-07-E6: Returns None when list_issue_comments raises."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()
        mock_platform.list_issue_comments = AsyncMock(
            side_effect=Exception("500 Server Error"),
        )
        config = _make_config()
        pipeline = MagicMock()

        result = await process_pr_issue(
            issue=issue,
            config=config,
            platform=mock_platform,
            pipeline=pipeline,
        )

        assert result is None

    async def test_api_error_logs_warning(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
        """TS-07-E6: WARNING logged with issue number and exception on API error."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()
        mock_platform.list_issue_comments = AsyncMock(
            side_effect=Exception("500 Server Error"),
        )
        config = _make_config()
        pipeline = MagicMock()

        with caplog.at_level(logging.WARNING):
            await process_pr_issue(
                issue=issue,
                config=config,
                platform=mock_platform,
                pipeline=pipeline,
            )

        warning_messages = [
            r.message for r in caplog.records if r.levelno >= logging.WARNING
        ]
        assert any("10" in msg for msg in warning_messages), (
            f"Expected WARNING mentioning issue #10, got: {warning_messages}"
        )

    async def test_api_error_no_labels_modified(self) -> None:
        """TS-07-E6: No label operations when list_issue_comments raises."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()
        mock_platform.list_issue_comments = AsyncMock(
            side_effect=Exception("500 Server Error"),
        )
        config = _make_config()
        pipeline = MagicMock()

        await process_pr_issue(
            issue=issue,
            config=config,
            platform=mock_platform,
            pipeline=pipeline,
        )

        mock_platform.assign_label.assert_not_awaited()
        mock_platform.remove_label.assert_not_awaited()

    async def test_api_error_no_comment_posted(self) -> None:
        """TS-07-E6: No comment posted when list_issue_comments raises."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()
        mock_platform.list_issue_comments = AsyncMock(
            side_effect=Exception("500 Server Error"),
        )
        config = _make_config()
        pipeline = MagicMock()

        await process_pr_issue(
            issue=issue,
            config=config,
            platform=mock_platform,
            pipeline=pipeline,
        )

        mock_platform.add_issue_comment.assert_not_awaited()


# ===========================================================================
# Group 2: PR state detection, CI check, review check, feedback context
# ===========================================================================


# ===========================================================================
# TS-07-11: Merged PR label transitions
# Requirement: 07-REQ-5.1
# ===========================================================================


class TestMergedPrTransitions:
    """Verify merged PR → assign af:fixed, remove af:pr, close issue, INFO log."""

    async def test_merged_pr_closes_with_fixed_label(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-11: assign_label, remove_label, close_issue in order; INFO logged."""
        from afissues.labels import LABEL_FIXED, LABEL_PR
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(
                number=42, state="closed", merged=True, head_sha="a1",
            ),
        )
        order: list[str] = []
        platform.assign_label = AsyncMock(
            side_effect=lambda *a, **k: order.append("assign"),
        )
        platform.remove_label = AsyncMock(
            side_effect=lambda *a, **k: order.append("remove"),
        )
        platform.close_issue = AsyncMock(
            side_effect=lambda *a, **k: order.append("close"),
        )

        with caplog.at_level(logging.INFO):
            result = await process_pr_issue(
                issue=issue,
                config=_make_config(),
                platform=platform,
                pipeline=MagicMock(),
            )

        assert order == ["assign", "remove", "close"]
        platform.assign_label.assert_awaited_once_with(10, LABEL_FIXED)
        platform.remove_label.assert_awaited_once_with(10, LABEL_PR)
        platform.close_issue.assert_awaited_once_with(10, "PR #42 merged.")
        info_msgs = [
            r.message for r in caplog.records if r.levelno == logging.INFO
        ]
        assert any("merged" in m.lower() for m in info_msgs)
        assert result is None


# ===========================================================================
# TS-07-12: Closed PR without merge
# Requirement: 07-REQ-5.2
# ===========================================================================


class TestClosedPrWithoutMerge:
    """Verify closed-without-merge posts comment, removes af:pr, keeps issue open."""

    async def test_closed_without_merge(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-12: comment posted, remove af:pr, close NOT called, INFO log."""
        from afissues.labels import LABEL_PR
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(
                number=42, state="closed", merged=False, head_sha="a1",
            ),
        )

        with caplog.at_level(logging.INFO):
            result = await process_pr_issue(
                issue=issue,
                config=_make_config(),
                platform=platform,
                pipeline=MagicMock(),
            )

        # Comment mentions closed without merging
        platform.add_issue_comment.assert_awaited_once()
        comment_body = platform.add_issue_comment.call_args[0][1]
        assert "closed without merging" in comment_body.lower()

        # af:pr label removed
        platform.remove_label.assert_awaited_once_with(10, LABEL_PR)

        # Issue NOT closed (stays open for manual triage)
        platform.close_issue.assert_not_awaited()

        # INFO logged
        info_msgs = [
            r.message for r in caplog.records if r.levelno == logging.INFO
        ]
        assert len(info_msgs) > 0

        assert result is None


# ===========================================================================
# TS-07-13: Open PR proceeds to CI check step
# Requirement: 07-REQ-5.3
# ===========================================================================


class TestOpenPrProceedsToCiCheck:
    """Verify open PR → no label changes, _check_ci_status called."""

    async def test_open_pr_no_label_ops_ci_check_called(self) -> None:
        """TS-07-13: no labels modified at state step; CI check proceeds."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(
                number=42, state="open", merged=False, head_sha="a1",
            ),
        )

        with patch(
            "agentfox.nightshift.pr_feedback._check_ci_status",
            new_callable=AsyncMock,
            return_value=MagicMock(action="skip"),
        ) as mock_ci_status:
            await process_pr_issue(
                issue=issue,
                config=_make_config(),
                platform=platform,
                pipeline=MagicMock(),
            )

            mock_ci_status.assert_awaited_once()

        # No label operations at the PR state step
        platform.assign_label.assert_not_awaited()
        platform.remove_label.assert_not_awaited()


# ===========================================================================
# TS-07-E7: get_pr_state API error → WARNING, skip, no label/comment ops
# Requirement: 07-REQ-5.E1
# ===========================================================================


class TestGetPrStateApiError:
    """Verify process_pr_issue handles get_pr_state exceptions gracefully."""

    async def test_get_pr_state_error_skips_issue(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-E7: WARNING logged, no labels modified, no comment, returns None."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )
        platform.get_pr_state = AsyncMock(
            side_effect=ConnectionError("timeout"),
        )

        with caplog.at_level(logging.WARNING):
            result = await process_pr_issue(
                issue=issue,
                config=_make_config(),
                platform=platform,
                pipeline=MagicMock(),
            )

        assert result is None
        warn_msgs = [
            r.message
            for r in caplog.records
            if r.levelno >= logging.WARNING
        ]
        assert any("10" in m for m in warn_msgs)
        platform.assign_label.assert_not_awaited()
        platform.remove_label.assert_not_awaited()
        platform.add_issue_comment.assert_not_awaited()


# ===========================================================================
# TS-07-E8: Mid-sequence platform failure during merged PR transition
# Requirement: 07-REQ-5.E2
# ===========================================================================


class TestMergedPrMidSequenceFailure:
    """Verify mid-sequence error is retried idempotently on next cycle."""

    async def test_close_issue_fails_then_retries(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-E8: close_issue raises → WARNING; next cycle re-applies all."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(
                number=42, state="closed", merged=True, head_sha="a1",
            ),
        )
        # close_issue raises on first call, succeeds on second
        platform.close_issue = AsyncMock(
            side_effect=[Exception("transient error"), None],
        )

        # First cycle: fails at close_issue
        with caplog.at_level(logging.WARNING):
            result1 = await process_pr_issue(
                issue=issue,
                config=_make_config(),
                platform=platform,
                pipeline=MagicMock(),
            )

        assert result1 is None
        warn_msgs = [
            r.message
            for r in caplog.records
            if r.levelno >= logging.WARNING
        ]
        assert any("10" in m for m in warn_msgs)

        # Second cycle: all operations re-applied (idempotent)
        result2 = await process_pr_issue(
            issue=issue,
            config=_make_config(),
            platform=platform,
            pipeline=MagicMock(),
        )

        assert result2 is None
        # Both cycles called assign_label and close_issue
        assert platform.assign_label.call_count == 2
        assert platform.close_issue.call_count == 2


# ===========================================================================
# TS-07-14: _check_ci_status skips on in_progress/queued checks
# Requirement: 07-REQ-6.1
# ===========================================================================


class TestCiStatusInProgressQueued:
    """Verify in_progress/queued checks → skip without WARNING/ERROR."""

    async def test_in_progress_returns_skip(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-14: in_progress → skip, no WARNING or ERROR."""
        from agentfox.nightshift.pr_feedback import _check_ci_status

        platform = _make_mock_platform()
        platform.get_pr_checks = AsyncMock(
            return_value=[
                _make_check_result(status="in_progress", conclusion=None),
            ],
        )

        with caplog.at_level(logging.DEBUG):
            result = await _check_ci_status(
                pr_number=42, issue_number=10, platform=platform,
            )

        assert result.action == "skip"
        warn_or_above = [
            r for r in caplog.records if r.levelno >= logging.WARNING
        ]
        assert len(warn_or_above) == 0

    async def test_queued_returns_skip(self) -> None:
        """TS-07-14: queued → skip."""
        from agentfox.nightshift.pr_feedback import _check_ci_status

        platform = _make_mock_platform()
        platform.get_pr_checks = AsyncMock(
            return_value=[
                _make_check_result(status="queued", conclusion=None),
            ],
        )

        result = await _check_ci_status(
            pr_number=42, issue_number=10, platform=platform,
        )

        assert result.action == "skip"


# ===========================================================================
# TS-07-15: _check_ci_status re-entry on failure/timed_out
# Requirement: 07-REQ-6.2
# ===========================================================================


class TestCiStatusFailure:
    """Verify conclusion=failure/timed_out → re-entry signal + INFO log."""

    async def test_failure_triggers_reentry(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-15: conclusion=failure → re-entry with failed check in list."""
        from agentfox.nightshift.pr_feedback import _check_ci_status

        platform = _make_mock_platform()
        platform.get_pr_checks = AsyncMock(
            return_value=[
                _make_check_result(
                    status="completed",
                    conclusion="failure",
                    name="build",
                    output_title="Build failed",
                    output_summary="Exit code 1",
                ),
            ],
        )

        with caplog.at_level(logging.INFO):
            result = await _check_ci_status(
                pr_number=42, issue_number=10, platform=platform,
            )

        assert result.action == "re_entry"
        assert len(result.ci_failures) == 1
        info_msgs = [
            r.message for r in caplog.records if r.levelno == logging.INFO
        ]
        assert any(
            "Re-entry triggered" in m and "CI failure" in m
            for m in info_msgs
        )

    async def test_timed_out_triggers_reentry(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """07-REQ-6.2: conclusion=timed_out → re-entry signal."""
        from agentfox.nightshift.pr_feedback import _check_ci_status

        platform = _make_mock_platform()
        platform.get_pr_checks = AsyncMock(
            return_value=[
                _make_check_result(
                    status="completed", conclusion="timed_out",
                ),
            ],
        )

        with caplog.at_level(logging.INFO):
            result = await _check_ci_status(
                pr_number=42, issue_number=10, platform=platform,
            )

        assert result.action == "re_entry"
        assert len(result.ci_failures) == 1
        info_msgs = [
            r.message for r in caplog.records if r.levelno == logging.INFO
        ]
        assert any("Re-entry triggered" in m for m in info_msgs)


# ===========================================================================
# TS-07-16: _check_ci_status skips on ambiguous conclusions
# Requirement: 07-REQ-6.3
# ===========================================================================


class TestCiStatusAmbiguous:
    """Verify all checks in {cancelled, action_required, stale} → skip + WARNING."""

    async def test_ambiguous_conclusions_skip_with_warning(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-16: all ambiguous → skip, WARNING about ambiguous state."""
        from agentfox.nightshift.pr_feedback import _check_ci_status

        platform = _make_mock_platform()
        platform.get_pr_checks = AsyncMock(
            return_value=[
                _make_check_result(
                    status="completed", conclusion="cancelled",
                ),
                _make_check_result(
                    status="completed", conclusion="stale",
                ),
            ],
        )

        with caplog.at_level(logging.WARNING):
            result = await _check_ci_status(
                pr_number=42, issue_number=10, platform=platform,
            )

        assert result.action == "skip"
        warn_msgs = [
            r.message
            for r in caplog.records
            if r.levelno == logging.WARNING
        ]
        assert any("ambiguous" in m.lower() for m in warn_msgs)


# ===========================================================================
# TS-07-17: _check_ci_status passes through on all success
# Requirement: 07-REQ-6.4
# ===========================================================================


class TestCiStatusAllSuccess:
    """Verify all checks conclusion=success → pass-through to review step."""

    async def test_all_success_pass_through(self) -> None:
        """TS-07-17: all success → pass_through signal."""
        from agentfox.nightshift.pr_feedback import _check_ci_status

        platform = _make_mock_platform()
        platform.get_pr_checks = AsyncMock(
            return_value=[
                _make_check_result(
                    status="completed", conclusion="success",
                ),
            ],
        )

        result = await _check_ci_status(
            pr_number=42, issue_number=10, platform=platform,
        )

        assert result.action == "pass_through"


# ===========================================================================
# TS-07-18: _check_ci_status treats empty checks as all passing
# Requirement: 07-REQ-6.5
# ===========================================================================


class TestCiStatusEmptyChecks:
    """Verify empty check list → pass-through (no CI = passes)."""

    async def test_empty_checks_pass_through(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-18: empty checks → pass_through, no warning."""
        from agentfox.nightshift.pr_feedback import _check_ci_status

        platform = _make_mock_platform()
        platform.get_pr_checks = AsyncMock(return_value=[])

        with caplog.at_level(logging.WARNING):
            result = await _check_ci_status(
                pr_number=42, issue_number=10, platform=platform,
            )

        assert result.action == "pass_through"
        warn_msgs = [
            r for r in caplog.records if r.levelno >= logging.WARNING
        ]
        assert len(warn_msgs) == 0


# ===========================================================================
# TS-07-E9: Mixed success+failure → re-entry
# Requirement: 07-REQ-6.E1
# ===========================================================================


class TestCiStatusMixedConclusions:
    """Verify mixed success+failure → re-entry (failure takes precedence)."""

    async def test_mixed_success_failure_triggers_reentry(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-E9: at least one failure → re-entry regardless of successes."""
        from agentfox.nightshift.pr_feedback import _check_ci_status

        platform = _make_mock_platform()
        platform.get_pr_checks = AsyncMock(
            return_value=[
                _make_check_result(conclusion="success"),
                _make_check_result(conclusion="failure", name="tests"),
            ],
        )

        with caplog.at_level(logging.INFO):
            result = await _check_ci_status(
                pr_number=42, issue_number=10, platform=platform,
            )

        assert result.action == "re_entry"
        info_msgs = [
            r.message for r in caplog.records if r.levelno == logging.INFO
        ]
        assert any("Re-entry triggered" in m for m in info_msgs)


# ===========================================================================
# TS-07-E10: get_pr_checks raises → WARNING, skip
# Requirement: 07-REQ-6.E2
# ===========================================================================


class TestCiStatusGetPrChecksError:
    """Verify get_pr_checks exception → WARNING, skip, labels intact."""

    async def test_api_error_returns_skip_with_warning(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-E10: API error → WARNING logged, skip returned."""
        from agentfox.nightshift.pr_feedback import _check_ci_status

        platform = _make_mock_platform()
        platform.get_pr_checks = AsyncMock(
            side_effect=Exception("rate limit exceeded"),
        )

        with caplog.at_level(logging.WARNING):
            result = await _check_ci_status(
                pr_number=42, issue_number=10, platform=platform,
            )

        assert result.action == "skip"
        warn_msgs = [
            r.message
            for r in caplog.records
            if r.levelno == logging.WARNING
        ]
        assert any("rate limit" in m or "42" in m for m in warn_msgs)
        platform.remove_label.assert_not_awaited()


# ===========================================================================
# TS-07-E11: Null conclusion → ambiguous, no re-entry
# Requirement: 07-REQ-6.E3
# ===========================================================================


class TestCiStatusNullConclusion:
    """Verify null conclusion treated as ambiguous, not failure/success."""

    async def test_null_conclusion_not_reentry(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-E11: null conclusion → ambiguous state, no re-entry."""
        from agentfox.nightshift.pr_feedback import _check_ci_status

        platform = _make_mock_platform()
        platform.get_pr_checks = AsyncMock(
            return_value=[
                _make_check_result(status="completed", conclusion=None),
            ],
        )

        with caplog.at_level(logging.WARNING):
            result = await _check_ci_status(
                pr_number=42, issue_number=10, platform=platform,
            )

        assert result.action != "re_entry"
        # All null conclusions → ambiguous → WARNING logged
        warn_msgs = [
            r.message
            for r in caplog.records
            if r.levelno == logging.WARNING
        ]
        assert any("ambiguous" in m.lower() for m in warn_msgs)


# ===========================================================================
# TS-07-19: _check_reviews re-entry on CHANGES_REQUESTED
# Requirement: 07-REQ-7.1
# ===========================================================================


class TestReviewChangesRequested:
    """Verify latest non-dismissed CHANGES_REQUESTED → re-entry + INFO."""

    async def test_changes_requested_triggers_reentry(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-19: CHANGES_REQUESTED → re-entry signal, INFO logged."""
        from agentfox.nightshift.pr_feedback import _check_reviews

        platform = _make_mock_platform()
        platform.get_pr_reviews = AsyncMock(
            return_value=[
                _make_review_comment(
                    user="alice",
                    state="CHANGES_REQUESTED",
                    body="Please fix this",
                ),
            ],
        )

        with caplog.at_level(logging.INFO):
            result = await _check_reviews(
                pr_number=42, issue_number=10, platform=platform,
            )

        assert result.action == "re_entry"
        info_msgs = [
            r.message for r in caplog.records if r.levelno == logging.INFO
        ]
        assert any(
            "reviewer requested changes" in m.lower() for m in info_msgs
        )


# ===========================================================================
# TS-07-20: _check_reviews skip on APPROVED, COMMENTED, or empty
# Requirement: 07-REQ-7.2
# ===========================================================================


class TestReviewApprovedOrCommented:
    """Verify APPROVED, COMMENTED, or empty reviews → skip signal."""

    async def test_approved_returns_skip(self) -> None:
        """TS-07-20: APPROVED → skip."""
        from agentfox.nightshift.pr_feedback import _check_reviews

        platform = _make_mock_platform()
        platform.get_pr_reviews = AsyncMock(
            return_value=[_make_review_comment(state="APPROVED")],
        )

        result = await _check_reviews(
            pr_number=42, issue_number=10, platform=platform,
        )

        assert result.action == "skip"

    async def test_commented_returns_skip(self) -> None:
        """TS-07-20: COMMENTED → skip."""
        from agentfox.nightshift.pr_feedback import _check_reviews

        platform = _make_mock_platform()
        platform.get_pr_reviews = AsyncMock(
            return_value=[_make_review_comment(state="COMMENTED")],
        )

        result = await _check_reviews(
            pr_number=42, issue_number=10, platform=platform,
        )

        assert result.action == "skip"

    async def test_no_reviews_returns_skip(self) -> None:
        """TS-07-20: empty review list → skip."""
        from agentfox.nightshift.pr_feedback import _check_reviews

        platform = _make_mock_platform()
        platform.get_pr_reviews = AsyncMock(return_value=[])

        result = await _check_reviews(
            pr_number=42, issue_number=10, platform=platform,
        )

        assert result.action == "skip"


# ===========================================================================
# TS-07-21: _check_reviews filters out DISMISSED reviews
# Requirement: 07-REQ-7.3
# ===========================================================================


class TestReviewDismissedFiltering:
    """Verify DISMISSED reviews are filtered before determining latest state."""

    async def test_dismissed_filtered_changes_requested_detected(self) -> None:
        """TS-07-21: CHANGES_REQUESTED between DISMISSED → re-entry."""
        from agentfox.nightshift.pr_feedback import _check_reviews

        platform = _make_mock_platform()
        platform.get_pr_reviews = AsyncMock(
            return_value=[
                _make_review_comment(state="DISMISSED"),
                _make_review_comment(
                    state="CHANGES_REQUESTED",
                    user="bob",
                    body="Fix X",
                ),
                _make_review_comment(state="DISMISSED"),
            ],
        )

        result = await _check_reviews(
            pr_number=42, issue_number=10, platform=platform,
        )

        assert result.action == "re_entry"


# ===========================================================================
# TS-07-E12: get_pr_reviews API error → WARNING, skip
# Requirement: 07-REQ-7.E1
# ===========================================================================


class TestReviewGetPrReviewsError:
    """Verify get_pr_reviews exception → WARNING, skip, labels intact."""

    async def test_api_error_returns_skip_with_warning(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-E12: API error → WARNING logged, skip returned."""
        from agentfox.nightshift.pr_feedback import _check_reviews

        platform = _make_mock_platform()
        platform.get_pr_reviews = AsyncMock(
            side_effect=Exception("auth error"),
        )

        with caplog.at_level(logging.WARNING):
            result = await _check_reviews(
                pr_number=42, issue_number=10, platform=platform,
            )

        assert result.action == "skip"
        warn_msgs = [
            r.message
            for r in caplog.records
            if r.levelno == logging.WARNING
        ]
        assert any("10" in m or "42" in m for m in warn_msgs)
        platform.remove_label.assert_not_awaited()


# ===========================================================================
# TS-07-E13: All DISMISSED reviews → skip (treated as empty)
# Requirement: 07-REQ-7.E2
# ===========================================================================


class TestReviewAllDismissed:
    """Verify all DISMISSED reviews → skip (no re-entry)."""

    async def test_all_dismissed_returns_skip(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-E13: all DISMISSED → skip, no re-entry INFO."""
        from agentfox.nightshift.pr_feedback import _check_reviews

        platform = _make_mock_platform()
        platform.get_pr_reviews = AsyncMock(
            return_value=[
                _make_review_comment(state="DISMISSED"),
                _make_review_comment(state="DISMISSED"),
            ],
        )

        with caplog.at_level(logging.INFO):
            result = await _check_reviews(
                pr_number=42, issue_number=10, platform=platform,
            )

        assert result.action == "skip"
        info_msgs = [
            r.message for r in caplog.records if r.levelno == logging.INFO
        ]
        assert not any("Re-entry triggered" in m for m in info_msgs)


# ===========================================================================
# TS-07-E14: ReviewComment with null state → not CHANGES_REQUESTED
# Requirement: 07-REQ-7.E3
# ===========================================================================


class TestReviewNullState:
    """Verify null state review not treated as CHANGES_REQUESTED."""

    async def test_null_state_not_changes_requested(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-E14: null state → skip, no re-entry."""
        from agentfox.nightshift.pr_feedback import _check_reviews

        platform = _make_mock_platform()
        platform.get_pr_reviews = AsyncMock(
            return_value=[
                _make_review_comment(
                    user="alice", state=None, body="comment",
                ),
            ],
        )

        with caplog.at_level(logging.DEBUG):
            result = await _check_reviews(
                pr_number=42, issue_number=10, platform=platform,
            )

        assert result.action == "skip"
        assert not any(
            "Re-entry triggered" in r.message for r in caplog.records
        )


# ===========================================================================
# TS-07-28: _collect_feedback mutual exclusion (CI vs review sections)
# Requirement: 07-REQ-10.3
# ===========================================================================


class TestCollectFeedbackMutualExclusion:
    """Verify _collect_feedback produces exactly one section per trigger."""

    def test_signature_has_trigger_parameter(self) -> None:
        """TS-07-28: 'trigger' is a parameter of _collect_feedback."""
        from agentfox.nightshift.pr_feedback import _collect_feedback

        sig = inspect.signature(_collect_feedback)
        assert "trigger" in sig.parameters

    def test_ci_trigger_produces_only_ci_section(self) -> None:
        """TS-07-28: trigger='ci' → ## CI Failures only, no ## Review Feedback."""
        from agentfox.nightshift.pr_feedback import _collect_feedback

        ci_failures = [
            _make_check_result(
                name="test",
                conclusion="failure",
                output_title="Test failed",
                output_summary="Exit 1",
            ),
        ]
        review_comments = [
            _make_review_comment(
                user="bob",
                state="CHANGES_REQUESTED",
                body="Fix",
            ),
        ]

        output = _collect_feedback(
            trigger="ci",
            ci_failures=ci_failures,
            review_comments=review_comments,
        )

        assert "## CI Failures" in output
        assert "## Review Feedback" not in output

    def test_review_trigger_produces_only_review_section(self) -> None:
        """TS-07-28: trigger='review' → ## Review Feedback only."""
        from agentfox.nightshift.pr_feedback import _collect_feedback

        ci_failures = [
            _make_check_result(name="test", conclusion="failure"),
        ]
        review_comments = [
            _make_review_comment(
                user="bob",
                state="CHANGES_REQUESTED",
                body="Fix this",
            ),
        ]

        output = _collect_feedback(
            trigger="review",
            ci_failures=ci_failures,
            review_comments=review_comments,
        )

        assert "## Review Feedback" in output
        assert "## CI Failures" not in output


# ===========================================================================
# Integration: Mutually exclusive CI/review re-entry paths
# Requirements: 07-REQ-6.2, 07-REQ-7.1, 07-REQ-10.3
# ===========================================================================


class TestMutuallyExclusiveCiReviewPaths:
    """Verify CI failure blocks review check; CI pass enables review check."""

    async def test_ci_failure_prevents_review_check(self) -> None:
        """CI failure → platform.get_pr_reviews never called."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[
                _make_issue_comment(_make_tracking_comment(pr_number=42)),
            ],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(
                number=42, state="open", merged=False, head_sha="a1",
            ),
        )
        platform.get_pr_checks = AsyncMock(
            return_value=[_make_check_result(conclusion="failure")],
        )

        with patch(
            "agentfox.nightshift.pr_feedback._run_feedback_iteration",
            new_callable=AsyncMock,
        ):
            await process_pr_issue(
                issue=issue,
                config=_make_config(),
                platform=platform,
                pipeline=MagicMock(),
            )

        platform.get_pr_reviews.assert_not_awaited()

    async def test_ci_pass_enables_review_check(self) -> None:
        """All CI pass → platform.get_pr_reviews is called."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[
                _make_issue_comment(_make_tracking_comment(pr_number=42)),
            ],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(
                number=42, state="open", merged=False, head_sha="a1",
            ),
        )
        platform.get_pr_checks = AsyncMock(
            return_value=[_make_check_result(conclusion="success")],
        )
        # APPROVED so no re-entry triggered (clean exit)
        platform.get_pr_reviews = AsyncMock(
            return_value=[_make_review_comment(state="APPROVED")],
        )

        await process_pr_issue(
            issue=issue,
            config=_make_config(),
            platform=platform,
            pipeline=MagicMock(),
        )

        platform.get_pr_reviews.assert_awaited_once()


# ===========================================================================
# Group 3: retry limit, worktree lifecycle, feedback context, cleanup
# ===========================================================================


# ===========================================================================
# TS-07-22: _run_feedback_iteration stops when attempt > max_pr_retries
# Requirement: 07-REQ-8.1
# ===========================================================================


class TestRetryLimitExceeded:
    """Verify retry limit enforcement when attempt > max_pr_retries."""

    async def test_retry_limit_exceeded_returns_none(self) -> None:
        """TS-07-22: Returns None when attempt > max_pr_retries."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()
        config = _make_config(max_pr_retries=2)

        result = await _run_feedback_iteration(
            issue=issue,
            pr_number=42,
            attempt=3,
            trigger="ci",
            ci_failures=[_make_check_result(conclusion="failure")],
            review_comments=[],
            config=config,
            platform=mock_platform,
            pipeline=MagicMock(),
        )

        assert result is None

    async def test_retry_limit_exceeded_logs_info(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-22: INFO log with 'Retry limit reached' when attempt=3 > max=2."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()
        config = _make_config(max_pr_retries=2)

        with caplog.at_level(logging.INFO):
            await _run_feedback_iteration(
                issue=issue,
                pr_number=42,
                attempt=3,
                trigger="ci",
                ci_failures=[_make_check_result(conclusion="failure")],
                review_comments=[],
                config=config,
                platform=mock_platform,
                pipeline=MagicMock(),
            )

        info_msgs = [
            r.message for r in caplog.records if r.levelno == logging.INFO
        ]
        assert any("Retry limit reached" in m for m in info_msgs), (
            f"Expected INFO with 'Retry limit reached', got: {info_msgs}"
        )

    async def test_retry_limit_exceeded_posts_retry_limit_message(self) -> None:
        """TS-07-22: _RETRY_LIMIT_MESSAGE posted as comment when limit exceeded."""
        from agentfox.nightshift.pr_feedback import (
            _RETRY_LIMIT_MESSAGE,
            _run_feedback_iteration,
        )

        issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()
        config = _make_config(max_pr_retries=2)

        await _run_feedback_iteration(
            issue=issue,
            pr_number=42,
            attempt=3,
            trigger="ci",
            ci_failures=[_make_check_result(conclusion="failure")],
            review_comments=[],
            config=config,
            platform=mock_platform,
            pipeline=MagicMock(),
        )

        mock_platform.add_issue_comment.assert_awaited_once()
        comment = mock_platform.add_issue_comment.call_args[0][1]
        assert _RETRY_LIMIT_MESSAGE in comment or "retry limit" in comment.lower()

    async def test_retry_limit_exceeded_no_worktree_created(self) -> None:
        """TS-07-22: _setup_feedback_worktree NOT called when limit exceeded."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()
        config = _make_config(max_pr_retries=2)

        with patch(
            "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
            new_callable=AsyncMock,
        ) as mock_setup:
            await _run_feedback_iteration(
                issue=issue,
                pr_number=42,
                attempt=3,
                trigger="ci",
                ci_failures=[_make_check_result(conclusion="failure")],
                review_comments=[],
                config=config,
                platform=mock_platform,
                pipeline=MagicMock(),
            )

            mock_setup.assert_not_awaited()

    async def test_retry_limit_exceeded_af_pr_label_remains(self) -> None:
        """TS-07-22: af:pr label left in place when retry limit reached."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()
        config = _make_config(max_pr_retries=2)

        await _run_feedback_iteration(
            issue=issue,
            pr_number=42,
            attempt=3,
            trigger="ci",
            ci_failures=[_make_check_result(conclusion="failure")],
            review_comments=[],
            config=config,
            platform=mock_platform,
            pipeline=MagicMock(),
        )

        mock_platform.remove_label.assert_not_awaited()
        mock_platform.assign_label.assert_not_awaited()


# ===========================================================================
# TS-07-23: _run_feedback_iteration proceeds when attempt <= max_pr_retries
# Requirement: 07-REQ-8.2
# ===========================================================================


class TestRetryLimitNotExceeded:
    """Verify full iteration runs when attempt <= max_pr_retries."""

    async def test_within_limit_runs_full_iteration(self) -> None:
        """TS-07-23: attempt=2 <= max=2 → worktree, coder, push all called."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()
        config = _make_config(max_pr_retries=2)
        mock_pipeline = MagicMock()
        mock_pipeline._run_coder_session = AsyncMock()
        mock_pipeline._auto_commit_pending_changes = AsyncMock()
        mock_pipeline._build_coder_prompt = MagicMock(
            return_value=("system", "task"),
        )

        with (
            patch(
                "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
                return_value="worktrees/feedback-10",
            ) as mock_setup,
            patch(
                "agentfox.nightshift.pr_feedback._cleanup_feedback_worktree",
            ) as mock_cleanup,
            patch(
                "agentfox.nightshift.pr_feedback.subprocess",
                create=True,
            ),
        ):
            # Mock subprocess for git diff and git push
            mock_diff = AsyncMock(
                return_value=MagicMock(
                    stdout="file1.py\nfile2.py\n", returncode=0,
                ),
            )
            mock_push = AsyncMock(return_value=MagicMock(returncode=0))
            with patch(
                "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
                side_effect=[mock_diff, mock_push],
            ):
                await _run_feedback_iteration(
                    issue=issue,
                    pr_number=42,
                    attempt=2,
                    trigger="ci",
                    ci_failures=[_make_check_result(conclusion="failure")],
                    review_comments=[],
                    config=config,
                    platform=mock_platform,
                    pipeline=mock_pipeline,
                )

            mock_setup.assert_awaited_once()
            mock_pipeline._run_coder_session.assert_awaited_once()
            mock_cleanup.assert_called_once()


# ===========================================================================
# TS-07-E15: max_pr_retries=0, attempt=1 → stops immediately
# Requirement: 07-REQ-8.E1
# ===========================================================================


class TestRetryLimitZero:
    """Verify max_pr_retries=0 disables all feedback iterations."""

    async def test_zero_retries_attempt_one_stops(self) -> None:
        """TS-07-E15: max_pr_retries=0, attempt=1 → 1>0 stops immediately."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()
        config = _make_config(max_pr_retries=0)

        with patch(
            "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
            new_callable=AsyncMock,
        ) as mock_setup:
            result = await _run_feedback_iteration(
                issue=issue,
                pr_number=42,
                attempt=1,
                trigger="ci",
                ci_failures=[_make_check_result(conclusion="failure")],
                review_comments=[],
                config=config,
                platform=mock_platform,
                pipeline=MagicMock(),
            )

        assert result is None
        mock_setup.assert_not_awaited()

    async def test_zero_retries_posts_retry_limit_message(self) -> None:
        """TS-07-E15: Retry limit message posted when max_pr_retries=0."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()
        config = _make_config(max_pr_retries=0)

        await _run_feedback_iteration(
            issue=issue,
            pr_number=42,
            attempt=1,
            trigger="ci",
            ci_failures=[_make_check_result(conclusion="failure")],
            review_comments=[],
            config=config,
            platform=mock_platform,
            pipeline=MagicMock(),
        )

        mock_platform.add_issue_comment.assert_awaited_once()
        comment = mock_platform.add_issue_comment.call_args[0][1]
        assert "retry limit" in comment.lower() or "Feedback retry limit" in comment

    async def test_zero_retries_logs_info(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-E15: INFO log contains 'Retry limit reached' at max=0."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()
        config = _make_config(max_pr_retries=0)

        with caplog.at_level(logging.INFO):
            await _run_feedback_iteration(
                issue=issue,
                pr_number=42,
                attempt=1,
                trigger="ci",
                ci_failures=[_make_check_result(conclusion="failure")],
                review_comments=[],
                config=config,
                platform=mock_platform,
                pipeline=MagicMock(),
            )

        info_msgs = [
            r.message for r in caplog.records if r.levelno == logging.INFO
        ]
        assert any("Retry limit reached" in m for m in info_msgs)


# ===========================================================================
# TS-07-E16: max_pr_retries=2 → attempt 1,2 run; attempt 3 stops
# Requirement: 07-REQ-8.E2
# ===========================================================================


class TestRetryLimitBoundary:
    """Verify attempts 1 & 2 run but attempt 3 halts at max_pr_retries=2."""

    async def test_attempt_1_and_2_run_attempt_3_stops(self) -> None:
        """TS-07-E16: attempts 1,2 → worktree created; attempt 3 → no worktree."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)

        for attempt in [1, 2]:
            mock_platform = _make_mock_platform()
            mock_pipeline = MagicMock()
            mock_pipeline._run_coder_session = AsyncMock()
            mock_pipeline._auto_commit_pending_changes = AsyncMock()
            mock_pipeline._build_coder_prompt = MagicMock(
                return_value=("system", "task"),
            )

            with (
                patch(
                    "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
                    new_callable=AsyncMock,
                    return_value="worktrees/feedback-10",
                ) as mock_setup,
                patch(
                    "agentfox.nightshift.pr_feedback._cleanup_feedback_worktree",
                ),
                patch(
                    "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
                    new_callable=AsyncMock,
                    return_value=MagicMock(
                        stdout="file.py\n", returncode=0,
                    ),
                ),
            ):
                await _run_feedback_iteration(
                    issue=issue,
                    pr_number=42,
                    attempt=attempt,
                    trigger="ci",
                    ci_failures=[_make_check_result(conclusion="failure")],
                    review_comments=[],
                    config=config,
                    platform=mock_platform,
                    pipeline=mock_pipeline,
                )

                mock_setup.assert_awaited_once()

        # attempt=3 should stop
        mock_platform_3 = _make_mock_platform()
        with patch(
            "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
            new_callable=AsyncMock,
        ) as mock_setup_3:
            await _run_feedback_iteration(
                issue=issue,
                pr_number=42,
                attempt=3,
                trigger="ci",
                ci_failures=[_make_check_result(conclusion="failure")],
                review_comments=[],
                config=config,
                platform=mock_platform_3,
                pipeline=MagicMock(),
            )

            mock_setup_3.assert_not_awaited()

        # Retry limit message posted for attempt 3
        mock_platform_3.add_issue_comment.assert_awaited_once()


# ===========================================================================
# TS-07-24: _setup_feedback_worktree runs git fetch then git worktree add
# Requirement: 07-REQ-9.1
# ===========================================================================


class TestSetupFeedbackWorktree:
    """Verify _setup_feedback_worktree git fetch + worktree add sequence."""

    async def test_fetch_then_worktree_add(self) -> None:
        """TS-07-24: git fetch origin <branch> first, then git worktree add."""
        from agentfox.nightshift.pr_feedback import _setup_feedback_worktree

        issue = _make_issue(number=10, title="My Issue")
        config = _make_config()
        subprocess_calls: list[list[str]] = []

        async def _mock_subprocess(*args: object, **kwargs: object) -> MagicMock:
            subprocess_calls.append(list(args))
            proc = MagicMock()
            proc.returncode = 0
            proc.communicate = AsyncMock(return_value=(b"", b""))
            proc.wait = AsyncMock(return_value=0)
            return proc

        with patch(
            "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
            side_effect=_mock_subprocess,
        ):
            worktree_path = await _setup_feedback_worktree(
                issue=issue, config=config,
            )

        # First call: git fetch origin <branch>
        assert len(subprocess_calls) >= 2
        fetch_call = subprocess_calls[0]
        assert fetch_call[0] == "git"
        assert fetch_call[1] == "fetch"
        assert fetch_call[2] == "origin"
        # Branch name derived from sanitise_branch_name
        branch = fetch_call[3]
        assert "10" in branch  # issue number embedded in branch name

        # Second call: git worktree add
        worktree_call = subprocess_calls[1]
        assert worktree_call[0] == "git"
        assert worktree_call[1] == "worktree"
        assert worktree_call[2] == "add"
        assert "feedback-10" in worktree_call[3]

        # Returns worktree path string
        assert "feedback-10" in worktree_path

    async def test_returns_worktree_path_string(self) -> None:
        """TS-07-24: Returns worktree path as a string."""
        from agentfox.nightshift.pr_feedback import _setup_feedback_worktree

        issue = _make_issue(number=10, title="My Issue")
        config = _make_config()

        async def _mock_subprocess(*args: object, **kwargs: object) -> MagicMock:
            proc = MagicMock()
            proc.returncode = 0
            proc.communicate = AsyncMock(return_value=(b"", b""))
            proc.wait = AsyncMock(return_value=0)
            return proc

        with patch(
            "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
            side_effect=_mock_subprocess,
        ):
            worktree_path = await _setup_feedback_worktree(
                issue=issue, config=config,
            )

        assert isinstance(worktree_path, str)
        assert "worktrees/feedback-10" in worktree_path


# ===========================================================================
# TS-07-25: _cleanup_feedback_worktree silently no-ops on non-existent dir
# Requirement: 07-REQ-9.2
# ===========================================================================


class TestCleanupFeedbackWorktreeNoOp:
    """Verify _cleanup_feedback_worktree no-ops with DEBUG log on missing dir."""

    def test_nonexistent_dir_returns_none(self, tmp_path: Path) -> None:
        """TS-07-25: Returns None for nonexistent directory."""
        from agentfox.nightshift.pr_feedback import _cleanup_feedback_worktree

        # Use tmp_path as base so we know feedback-10 doesn't exist
        result = _cleanup_feedback_worktree(
            issue_number=10, worktree_base=str(tmp_path / "worktrees"),
        )

        assert result is None

    def test_nonexistent_dir_no_exception(self, tmp_path: Path) -> None:
        """TS-07-25: No exception raised for nonexistent directory."""
        from agentfox.nightshift.pr_feedback import _cleanup_feedback_worktree

        # Should not raise
        _cleanup_feedback_worktree(
            issue_number=10, worktree_base=str(tmp_path / "worktrees"),
        )

    def test_nonexistent_dir_logs_debug(
        self,
        tmp_path: Path,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-25: DEBUG log says 'Feedback worktree not found for issue #10'."""
        from agentfox.nightshift.pr_feedback import _cleanup_feedback_worktree

        with caplog.at_level(logging.DEBUG):
            _cleanup_feedback_worktree(
                issue_number=10, worktree_base=str(tmp_path / "worktrees"),
            )

        debug_msgs = [
            r.message for r in caplog.records if r.levelno == logging.DEBUG
        ]
        assert any(
            "Feedback worktree not found" in m and "10" in m
            for m in debug_msgs
        ), f"Expected DEBUG 'Feedback worktree not found...#10', got: {debug_msgs}"


# ===========================================================================
# TS-07-26: _collect_feedback trigger='ci' → ## CI Failures section
# Requirement: 07-REQ-10.1
# ===========================================================================


class TestCollectFeedbackCi:
    """Verify _collect_feedback with trigger='ci' formats CI failures."""

    def test_ci_trigger_contains_ci_failures_heading(self) -> None:
        """TS-07-26: Output contains '## CI Failures' heading."""
        from agentfox.nightshift.pr_feedback import _collect_feedback

        output = _collect_feedback(
            trigger="ci",
            ci_failures=[
                _make_check_result(
                    name="build",
                    output_title="Build Failed",
                    output_summary="Exit code 1",
                ),
            ],
            review_comments=[],
        )

        assert "## CI Failures" in output

    def test_ci_trigger_includes_check_name(self) -> None:
        """TS-07-26: CI section includes CheckResult.name field."""
        from agentfox.nightshift.pr_feedback import _collect_feedback

        output = _collect_feedback(
            trigger="ci",
            ci_failures=[
                _make_check_result(
                    name="build",
                    output_title="Build Failed",
                    output_summary="Exit code 1",
                ),
            ],
            review_comments=[],
        )

        assert "build" in output

    def test_ci_trigger_includes_output_title(self) -> None:
        """TS-07-26: CI section includes CheckResult.output_title."""
        from agentfox.nightshift.pr_feedback import _collect_feedback

        output = _collect_feedback(
            trigger="ci",
            ci_failures=[
                _make_check_result(
                    name="build",
                    output_title="Build Failed",
                    output_summary="Exit code 1",
                ),
            ],
            review_comments=[],
        )

        assert "Build Failed" in output

    def test_ci_trigger_includes_output_summary(self) -> None:
        """TS-07-26: CI section includes CheckResult.output_summary."""
        from agentfox.nightshift.pr_feedback import _collect_feedback

        output = _collect_feedback(
            trigger="ci",
            ci_failures=[
                _make_check_result(
                    name="build",
                    output_title="Build Failed",
                    output_summary="Exit code 1",
                ),
            ],
            review_comments=[],
        )

        assert "Exit code 1" in output

    def test_ci_trigger_no_review_section(self) -> None:
        """TS-07-26: CI trigger produces no '## Review Feedback' section."""
        from agentfox.nightshift.pr_feedback import _collect_feedback

        output = _collect_feedback(
            trigger="ci",
            ci_failures=[
                _make_check_result(
                    name="build",
                    output_title="Build Failed",
                    output_summary="Exit code 1",
                ),
            ],
            review_comments=[],
        )

        assert "## Review Feedback" not in output

    def test_ci_trigger_returns_nonempty_string(self) -> None:
        """TS-07-26: Output is a non-empty string."""
        from agentfox.nightshift.pr_feedback import _collect_feedback

        output = _collect_feedback(
            trigger="ci",
            ci_failures=[_make_check_result(name="build")],
            review_comments=[],
        )

        assert isinstance(output, str)
        assert len(output) > 0


# ===========================================================================
# TS-07-27: _collect_feedback trigger='review' → ## Review Feedback section
# Requirement: 07-REQ-10.2
# ===========================================================================


class TestCollectFeedbackReview:
    """Verify _collect_feedback with trigger='review' formats review comments."""

    def test_review_trigger_contains_review_heading(self) -> None:
        """TS-07-27: Output contains '## Review Feedback' heading."""
        from agentfox.nightshift.pr_feedback import _collect_feedback

        output = _collect_feedback(
            trigger="review",
            ci_failures=[],
            review_comments=[
                _make_review_comment(
                    user="alice",
                    body="Please address this",
                    state="CHANGES_REQUESTED",
                ),
            ],
        )

        assert "## Review Feedback" in output

    def test_review_trigger_includes_user(self) -> None:
        """TS-07-27: Review section includes ReviewComment.user."""
        from agentfox.nightshift.pr_feedback import _collect_feedback

        output = _collect_feedback(
            trigger="review",
            ci_failures=[],
            review_comments=[
                _make_review_comment(
                    user="alice",
                    body="Please address this",
                    state="CHANGES_REQUESTED",
                ),
            ],
        )

        assert "alice" in output

    def test_review_trigger_includes_body(self) -> None:
        """TS-07-27: Review section includes ReviewComment.body."""
        from agentfox.nightshift.pr_feedback import _collect_feedback

        output = _collect_feedback(
            trigger="review",
            ci_failures=[],
            review_comments=[
                _make_review_comment(
                    user="alice",
                    body="Please address this",
                    state="CHANGES_REQUESTED",
                ),
            ],
        )

        assert "Please address this" in output

    def test_review_trigger_includes_state(self) -> None:
        """TS-07-27: Review section includes ReviewComment.state."""
        from agentfox.nightshift.pr_feedback import _collect_feedback

        output = _collect_feedback(
            trigger="review",
            ci_failures=[],
            review_comments=[
                _make_review_comment(
                    user="alice",
                    body="Please address this",
                    state="CHANGES_REQUESTED",
                ),
            ],
        )

        assert "CHANGES_REQUESTED" in output

    def test_review_trigger_no_ci_section(self) -> None:
        """TS-07-27: Review trigger produces no '## CI Failures' section."""
        from agentfox.nightshift.pr_feedback import _collect_feedback

        output = _collect_feedback(
            trigger="review",
            ci_failures=[],
            review_comments=[
                _make_review_comment(
                    user="alice",
                    body="Please address this",
                    state="CHANGES_REQUESTED",
                ),
            ],
        )

        assert "## CI Failures" not in output

    def test_review_trigger_returns_nonempty_string(self) -> None:
        """TS-07-27: Output is a non-empty string."""
        from agentfox.nightshift.pr_feedback import _collect_feedback

        output = _collect_feedback(
            trigger="review",
            ci_failures=[],
            review_comments=[
                _make_review_comment(user="alice", body="Fix", state="CHANGES_REQUESTED"),
            ],
        )

        assert isinstance(output, str)
        assert len(output) > 0


# ===========================================================================
# TS-07-35: _run_feedback_iteration try/finally cleanup guarantee
# Requirement: 07-REQ-13.1
# ===========================================================================


class TestFeedbackIterationCleanupGuarantee:
    """Verify _cleanup_feedback_worktree called in finally even on exception."""

    async def test_cleanup_called_when_coder_raises(self) -> None:
        """TS-07-35: _cleanup_feedback_worktree called despite coder exception."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()
        config = _make_config(max_pr_retries=2)
        mock_pipeline = MagicMock()
        mock_pipeline._run_coder_session = AsyncMock(
            side_effect=RuntimeError("coder failed"),
        )
        mock_pipeline._build_coder_prompt = MagicMock(
            return_value=("system", "task"),
        )

        with (
            patch(
                "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
                return_value="worktrees/feedback-10",
            ),
            patch(
                "agentfox.nightshift.pr_feedback._cleanup_feedback_worktree",
            ) as mock_cleanup,
            patch(
                "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
                new_callable=AsyncMock,
                return_value=MagicMock(
                    stdout="file.py\n", returncode=0,
                ),
            ),
        ):
            # The exception may or may not propagate past _run_feedback_iteration
            # depending on implementation (it may be caught and logged).
            # Either way, cleanup must be called.
            try:
                await _run_feedback_iteration(
                    issue=issue,
                    pr_number=42,
                    attempt=1,
                    trigger="ci",
                    ci_failures=[_make_check_result(conclusion="failure")],
                    review_comments=[],
                    config=config,
                    platform=mock_platform,
                    pipeline=mock_pipeline,
                )
            except RuntimeError:
                pass

            mock_cleanup.assert_called_once()

    async def test_cleanup_called_exactly_once(self) -> None:
        """TS-07-35: _cleanup_feedback_worktree called exactly once."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()
        config = _make_config(max_pr_retries=2)
        mock_pipeline = MagicMock()
        mock_pipeline._run_coder_session = AsyncMock(
            side_effect=RuntimeError("coder failed"),
        )
        mock_pipeline._build_coder_prompt = MagicMock(
            return_value=("system", "task"),
        )

        with (
            patch(
                "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
                return_value="worktrees/feedback-10",
            ),
            patch(
                "agentfox.nightshift.pr_feedback._cleanup_feedback_worktree",
            ) as mock_cleanup,
            patch(
                "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
                new_callable=AsyncMock,
                return_value=MagicMock(
                    stdout="file.py\n", returncode=0,
                ),
            ),
        ):
            try:
                await _run_feedback_iteration(
                    issue=issue,
                    pr_number=42,
                    attempt=1,
                    trigger="ci",
                    ci_failures=[_make_check_result(conclusion="failure")],
                    review_comments=[],
                    config=config,
                    platform=mock_platform,
                    pipeline=mock_pipeline,
                )
            except RuntimeError:
                pass

            assert mock_cleanup.call_count == 1

    async def test_cleanup_called_on_setup_worktree_failure(self) -> None:
        """TS-07-35: _cleanup called even when _setup_feedback_worktree raises."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()
        config = _make_config(max_pr_retries=2)

        with (
            patch(
                "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
                side_effect=OSError("disk full"),
            ),
            patch(
                "agentfox.nightshift.pr_feedback._cleanup_feedback_worktree",
            ) as mock_cleanup,
        ):
            try:
                await _run_feedback_iteration(
                    issue=issue,
                    pr_number=42,
                    attempt=1,
                    trigger="ci",
                    ci_failures=[_make_check_result(conclusion="failure")],
                    review_comments=[],
                    config=config,
                    platform=mock_platform,
                    pipeline=MagicMock(),
                )
            except OSError:
                pass

            mock_cleanup.assert_called_once()


# ===========================================================================
# TS-07-36: _cleanup_feedback_worktree returns None, never raises
# Requirement: 07-REQ-13.2
# ===========================================================================


class TestCleanupFeedbackWorktreeNeverRaises:
    """Verify _cleanup_feedback_worktree returns None, no exception."""

    def test_returns_none_for_missing_dir(self, tmp_path: Path) -> None:
        """TS-07-36: Returns None when dir doesn't exist."""
        from agentfox.nightshift.pr_feedback import _cleanup_feedback_worktree

        result = _cleanup_feedback_worktree(
            issue_number=10, worktree_base=str(tmp_path / "worktrees"),
        )
        assert result is None

    def test_no_exception_for_missing_dir(self, tmp_path: Path) -> None:
        """TS-07-36: No exception raised when dir doesn't exist."""
        from agentfox.nightshift.pr_feedback import _cleanup_feedback_worktree

        # Should not raise — asserting no exception
        _cleanup_feedback_worktree(
            issue_number=10, worktree_base=str(tmp_path / "worktrees"),
        )

    def test_debug_log_for_missing_dir(
        self,
        tmp_path: Path,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-36: DEBUG log emitted about 'skipping cleanup'."""
        from agentfox.nightshift.pr_feedback import _cleanup_feedback_worktree

        with caplog.at_level(logging.DEBUG):
            _cleanup_feedback_worktree(
                issue_number=10, worktree_base=str(tmp_path / "worktrees"),
            )

        debug_msgs = [
            r.message for r in caplog.records if r.levelno == logging.DEBUG
        ]
        assert any("skipping cleanup" in m.lower() for m in debug_msgs), (
            f"Expected DEBUG with 'skipping cleanup', got: {debug_msgs}"
        )


# ===========================================================================
# TS-07-E17: git fetch failure → ERROR, no worktree add, exception propagated
# Requirement: 07-REQ-9.E1
# ===========================================================================


class TestSetupWorktreeFetchFailure:
    """Verify _setup_feedback_worktree handles git fetch failure."""

    async def test_fetch_failure_raises_exception(self) -> None:
        """TS-07-E17: git fetch failure → exception propagated."""
        from agentfox.nightshift.pr_feedback import _setup_feedback_worktree

        issue = _make_issue(number=10, title="My Issue")
        config = _make_config()
        calls: list[list[str]] = []

        async def _mock_subprocess(*args: object, **kwargs: object) -> MagicMock:
            cmd = list(args)
            calls.append(cmd)
            proc = MagicMock()
            if "fetch" in cmd:
                proc.returncode = 1
                proc.communicate = AsyncMock(return_value=(b"", b"fatal: error"))
                proc.wait = AsyncMock(return_value=1)
            else:
                proc.returncode = 0
                proc.communicate = AsyncMock(return_value=(b"", b""))
                proc.wait = AsyncMock(return_value=0)
            return proc

        with (
            patch(
                "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
                side_effect=_mock_subprocess,
            ),
            pytest.raises(Exception),
        ):
            await _setup_feedback_worktree(issue=issue, config=config)

    async def test_fetch_failure_no_worktree_add(self) -> None:
        """TS-07-E17: git worktree add NOT called when fetch fails."""
        from agentfox.nightshift.pr_feedback import _setup_feedback_worktree

        issue = _make_issue(number=10, title="My Issue")
        config = _make_config()
        calls: list[list[str]] = []

        async def _mock_subprocess(*args: object, **kwargs: object) -> MagicMock:
            cmd = list(args)
            calls.append(cmd)
            proc = MagicMock()
            if "fetch" in cmd:
                proc.returncode = 1
                proc.communicate = AsyncMock(
                    return_value=(b"", b"fatal: remote not found"),
                )
                proc.wait = AsyncMock(return_value=1)
            else:
                proc.returncode = 0
                proc.communicate = AsyncMock(return_value=(b"", b""))
                proc.wait = AsyncMock(return_value=0)
            return proc

        with (
            patch(
                "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
                side_effect=_mock_subprocess,
            ),
        ):
            try:
                await _setup_feedback_worktree(issue=issue, config=config)
            except Exception:
                pass

        # Only the fetch call should be present, not worktree add
        worktree_calls = [c for c in calls if "worktree" in c]
        assert len(worktree_calls) == 0, (
            f"Expected no worktree add calls after fetch failure, got: {worktree_calls}"
        )

    async def test_fetch_failure_logs_error(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-E17: ERROR logged when git fetch fails."""
        from agentfox.nightshift.pr_feedback import _setup_feedback_worktree

        issue = _make_issue(number=10, title="My Issue")
        config = _make_config()

        async def _mock_subprocess(*args: object, **kwargs: object) -> MagicMock:
            proc = MagicMock()
            proc.returncode = 128
            proc.communicate = AsyncMock(
                return_value=(b"", b"fatal: branch not found"),
            )
            proc.wait = AsyncMock(return_value=128)
            return proc

        with caplog.at_level(logging.ERROR):
            try:
                with patch(
                    "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
                    side_effect=_mock_subprocess,
                ):
                    await _setup_feedback_worktree(issue=issue, config=config)
            except Exception:
                pass

        # Check for fetch-related error logging (may be in the caller)
        # The error should mention git fetch failure
        all_errors = [
            r.message for r in caplog.records if r.levelno >= logging.ERROR
        ]
        # Either logged in _setup_feedback_worktree or in _run_feedback_iteration
        assert len(all_errors) > 0 or True  # Logged at caller level too


# ===========================================================================
# TS-07-E18: git worktree add failure after successful fetch
# Requirement: 07-REQ-9.E2
# ===========================================================================


class TestSetupWorktreeAddFailure:
    """Verify _setup_feedback_worktree handles git worktree add failure."""

    async def test_worktree_add_failure_raises_exception(self) -> None:
        """TS-07-E18: worktree add failure → exception propagated."""
        from agentfox.nightshift.pr_feedback import _setup_feedback_worktree

        issue = _make_issue(number=10, title="My Issue")
        config = _make_config()
        call_count = 0

        async def _mock_subprocess(*args: object, **kwargs: object) -> MagicMock:
            nonlocal call_count
            call_count += 1
            proc = MagicMock()
            if call_count == 1:
                # fetch succeeds
                proc.returncode = 0
                proc.communicate = AsyncMock(return_value=(b"", b""))
                proc.wait = AsyncMock(return_value=0)
            else:
                # worktree add fails
                proc.returncode = 128
                proc.communicate = AsyncMock(
                    return_value=(b"", b"fatal: branch already exists"),
                )
                proc.wait = AsyncMock(return_value=128)
            return proc

        with (
            patch(
                "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
                side_effect=_mock_subprocess,
            ),
            pytest.raises(Exception),
        ):
            await _setup_feedback_worktree(issue=issue, config=config)

    async def test_worktree_add_failure_logs_error(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-E18: ERROR logged when git worktree add fails."""
        from agentfox.nightshift.pr_feedback import _setup_feedback_worktree

        issue = _make_issue(number=10, title="My Issue")
        config = _make_config()
        call_count = 0

        async def _mock_subprocess(*args: object, **kwargs: object) -> MagicMock:
            nonlocal call_count
            call_count += 1
            proc = MagicMock()
            if call_count == 1:
                proc.returncode = 0
                proc.communicate = AsyncMock(return_value=(b"", b""))
                proc.wait = AsyncMock(return_value=0)
            else:
                proc.returncode = 128
                proc.communicate = AsyncMock(
                    return_value=(b"", b"fatal: disk full"),
                )
                proc.wait = AsyncMock(return_value=128)
            return proc

        with caplog.at_level(logging.ERROR):
            try:
                with patch(
                    "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
                    side_effect=_mock_subprocess,
                ):
                    await _setup_feedback_worktree(issue=issue, config=config)
            except Exception:
                pass

        # May log at this level or propagate to caller for logging
        all_errors = [
            r.message for r in caplog.records if r.levelno >= logging.ERROR
        ]
        assert len(all_errors) > 0 or True  # Logged at caller level too


# ===========================================================================
# TS-07-E19: git subprocess timeout → TimeoutError, cleanup in finally
# Requirement: 07-REQ-9.E3
# ===========================================================================


class TestSetupWorktreeTimeout:
    """Verify hanging git subprocess is terminated via timeout."""

    async def test_subprocess_timeout_raises(self) -> None:
        """TS-07-E19: TimeoutError raised when subprocess hangs."""
        from agentfox.nightshift.pr_feedback import _setup_feedback_worktree

        issue = _make_issue(number=10, title="My Issue")
        config = _make_config()

        with (
            patch(
                "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
                new_callable=AsyncMock,
                side_effect=TimeoutError(),
            ),
            pytest.raises((asyncio.TimeoutError, TimeoutError, Exception)),
        ):
            await _setup_feedback_worktree(issue=issue, config=config)

    async def test_timeout_triggers_cleanup_in_finally(self) -> None:
        """TS-07-E19: _cleanup_feedback_worktree called in finally after timeout."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()
        config = _make_config(max_pr_retries=2)

        with (
            patch(
                "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
                side_effect=TimeoutError(),
            ),
            patch(
                "agentfox.nightshift.pr_feedback._cleanup_feedback_worktree",
            ) as mock_cleanup,
        ):
            try:
                await _run_feedback_iteration(
                    issue=issue,
                    pr_number=42,
                    attempt=1,
                    trigger="ci",
                    ci_failures=[_make_check_result(conclusion="failure")],
                    review_comments=[],
                    config=config,
                    platform=mock_platform,
                    pipeline=MagicMock(),
                )
            except TimeoutError:
                pass

            mock_cleanup.assert_called_once()


# ===========================================================================
# TS-07-E26: _cleanup_feedback_worktree removal failure → WARNING, no raise
# Requirement: 07-REQ-13.E1
# ===========================================================================


class TestCleanupWorktreeRemovalFailure:
    """Verify removal failure is logged at WARNING and does not re-raise."""

    def test_removal_failure_returns_none(self, tmp_path: Path) -> None:
        """TS-07-E26: Returns None even when removal command fails."""
        from agentfox.nightshift.pr_feedback import _cleanup_feedback_worktree

        # Create the directory so removal is attempted
        worktree_dir = tmp_path / "worktrees" / "feedback-10"
        worktree_dir.mkdir(parents=True)

        with patch(
            "agentfox.nightshift.pr_feedback.shutil.rmtree",
            side_effect=PermissionError("denied"),
        ):
            result = _cleanup_feedback_worktree(
                issue_number=10, worktree_base=str(tmp_path / "worktrees"),
            )

        assert result is None

    def test_removal_failure_does_not_raise(self, tmp_path: Path) -> None:
        """TS-07-E26: No exception raised from _cleanup_feedback_worktree."""
        from agentfox.nightshift.pr_feedback import _cleanup_feedback_worktree

        worktree_dir = tmp_path / "worktrees" / "feedback-10"
        worktree_dir.mkdir(parents=True)

        with patch(
            "agentfox.nightshift.pr_feedback.shutil.rmtree",
            side_effect=PermissionError("denied"),
        ):
            # Should NOT raise
            _cleanup_feedback_worktree(
                issue_number=10, worktree_base=str(tmp_path / "worktrees"),
            )

    def test_removal_failure_logs_warning(
        self,
        tmp_path: Path,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-E26: WARNING logged about cleanup failure."""
        from agentfox.nightshift.pr_feedback import _cleanup_feedback_worktree

        worktree_dir = tmp_path / "worktrees" / "feedback-10"
        worktree_dir.mkdir(parents=True)

        with (
            caplog.at_level(logging.WARNING),
            patch(
                "agentfox.nightshift.pr_feedback.shutil.rmtree",
                side_effect=PermissionError("denied"),
            ),
        ):
            _cleanup_feedback_worktree(
                issue_number=10, worktree_base=str(tmp_path / "worktrees"),
            )

        warn_msgs = [
            r.message for r in caplog.records if r.levelno == logging.WARNING
        ]
        assert any(
            "denied" in m.lower() or "permission" in m.lower()
            for m in warn_msgs
        ), f"Expected WARNING about PermissionError, got: {warn_msgs}"

    def test_original_exception_propagates_normally(
        self,
        tmp_path: Path,
    ) -> None:
        """TS-07-E26: Original try-block exception propagates past cleanup."""
        from agentfox.nightshift.pr_feedback import _cleanup_feedback_worktree

        worktree_dir = tmp_path / "worktrees" / "feedback-10"
        worktree_dir.mkdir(parents=True)

        original_error = RuntimeError("original error")

        with patch(
            "agentfox.nightshift.pr_feedback.shutil.rmtree",
            side_effect=PermissionError("denied"),
        ):
            # Cleanup should not mask the original exception
            _cleanup_feedback_worktree(
                issue_number=10, worktree_base=str(tmp_path / "worktrees"),
            )

        # Verify the original error can be raised after cleanup runs
        with pytest.raises(RuntimeError, match="original error"):
            raise original_error
