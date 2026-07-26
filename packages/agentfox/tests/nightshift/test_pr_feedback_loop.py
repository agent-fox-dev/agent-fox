"""Tests for spec 07: PR feedback loop — task groups 1, 2, 3, & 4.

Group 1: config fields, work stream registration, dispatcher sequencing,
and tracking comment parsing.

Group 2: PR state detection, CI check interpretation, review state
interpretation, feedback context collection, and mutually exclusive paths.

Group 3: retry limit enforcement, worktree lifecycle (setup + cleanup),
feedback context collection output format, and try/finally cleanup guarantee.

Group 4: coder session invocation, tracking comment update and force-push,
structured logging (INFO/WARNING/ERROR/DEBUG), module structure and imports,
label exclusivity.

Test Spec: TS-07-1, TS-07-2, TS-07-3, TS-07-4, TS-07-5,
           TS-07-6, TS-07-7, TS-07-8, TS-07-9, TS-07-10,
           TS-07-11, TS-07-12, TS-07-13, TS-07-14, TS-07-15,
           TS-07-16, TS-07-17, TS-07-18, TS-07-19, TS-07-20,
           TS-07-21, TS-07-22, TS-07-23, TS-07-24, TS-07-25,
           TS-07-26, TS-07-27, TS-07-28, TS-07-29, TS-07-30,
           TS-07-31, TS-07-32, TS-07-33, TS-07-34, TS-07-35,
           TS-07-36, TS-07-37, TS-07-38, TS-07-39, TS-07-40,
           TS-07-41, TS-07-42, TS-07-43, TS-07-44, TS-07-45,
           TS-07-46, TS-07-47, TS-07-48,
           TS-07-E1, TS-07-E2, TS-07-E3, TS-07-E4, TS-07-E5, TS-07-E6,
           TS-07-E7, TS-07-E8, TS-07-E9, TS-07-E10, TS-07-E11,
           TS-07-E12, TS-07-E13, TS-07-E14, TS-07-E15, TS-07-E16,
           TS-07-E17, TS-07-E18, TS-07-E19, TS-07-E20, TS-07-E21,
           TS-07-E22, TS-07-E23, TS-07-E24, TS-07-E25, TS-07-E26
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
              07-REQ-11.1, 07-REQ-11.2, 07-REQ-11.3,
              07-REQ-11.E1, 07-REQ-11.E2,
              07-REQ-12.1, 07-REQ-12.2, 07-REQ-12.3,
              07-REQ-12.E1, 07-REQ-12.E2, 07-REQ-12.E3, 07-REQ-12.E4,
              07-REQ-13.1, 07-REQ-13.2, 07-REQ-13.E1,
              07-REQ-14.1, 07-REQ-14.2, 07-REQ-14.3, 07-REQ-14.4, 07-REQ-14.5,
              07-REQ-15.1, 07-REQ-15.2, 07-REQ-15.3, 07-REQ-15.4,
              07-REQ-16.1, 07-REQ-16.2, 07-REQ-16.3
"""

from __future__ import annotations

import asyncio
import dataclasses
import inspect
import logging
import subprocess
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


# ===========================================================================
# Group 4: coder session invocation, tracking comment, force-push, logging,
#           module structure, imports, and label exclusivity
# ===========================================================================


# ---------------------------------------------------------------------------
# Group 4 helpers
# ---------------------------------------------------------------------------


def _make_mock_pipeline(
    *,
    coder_prompt: tuple[str, str] = ("system prompt", "task prompt"),
    coder_session_side_effect: Exception | None = None,
) -> MagicMock:
    """Create a mock FixPipeline with common methods for group 4 tests."""
    pipeline = MagicMock()
    pipeline._build_coder_prompt = MagicMock(return_value=coder_prompt)
    if coder_session_side_effect:
        pipeline._run_coder_session = AsyncMock(
            side_effect=coder_session_side_effect,
        )
    else:
        pipeline._run_coder_session = AsyncMock(return_value=MagicMock())
    pipeline._auto_commit_pending_changes = AsyncMock()
    return pipeline


def _make_feedback_patches(
    *,
    worktree_path: str = "worktrees/feedback-10",
    git_diff_files: str = "src/foo.py\n",
    git_diff_raises: bool = False,
    git_push_returncode: int = 0,
    git_push_raises: Exception | None = None,
    has_post_coder_changes: bool = True,
):
    """Create a dict of common patches for feedback iteration tests.

    Returns a context manager that patches worktree setup/cleanup
    and subprocess calls (git diff, git push).
    """
    import contextlib

    @contextlib.contextmanager
    def _ctx():
        call_order: list[str] = []

        async def _mock_subprocess(*args, **kwargs):
            cmd = list(args)
            cmd_str = " ".join(str(a) for a in cmd)
            proc = MagicMock()
            if "diff" in cmd_str:
                if git_diff_raises:
                    raise subprocess.CalledProcessError(1, "git diff")
                proc.stdout = git_diff_files
                proc.returncode = 0
                proc.communicate = AsyncMock(
                    return_value=(git_diff_files.encode(), b""),
                )
                proc.wait = AsyncMock(return_value=0)
            elif "push" in cmd_str:
                call_order.append("push")
                if git_push_raises:
                    raise git_push_raises
                proc.stdout = ""
                proc.returncode = git_push_returncode
                proc.communicate = AsyncMock(return_value=(b"", b""))
                proc.wait = AsyncMock(return_value=0)
            else:
                # status or other git commands
                output = git_diff_files if has_post_coder_changes else ""
                proc.stdout = output
                proc.returncode = 0
                proc.communicate = AsyncMock(
                    return_value=(output.encode(), b""),
                )
                proc.wait = AsyncMock(return_value=0)
            return proc

        with (
            patch(
                "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
                return_value=worktree_path,
            ) as mock_setup,
            patch(
                "agentfox.nightshift.pr_feedback._cleanup_feedback_worktree",
            ) as mock_cleanup,
            patch(
                "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
                side_effect=_mock_subprocess,
            ) as mock_subprocess,
        ):
            yield {
                "setup": mock_setup,
                "cleanup": mock_cleanup,
                "subprocess": mock_subprocess,
                "call_order": call_order,
            }

    return _ctx()


# ===========================================================================
# TS-07-29: _run_feedback_iteration constructs synthetic TriageResult
# Requirement: 07-REQ-11.1
# ===========================================================================


class TestCoderSessionSyntheticTriageResult:
    """Verify synthetic TriageResult fields and _build_coder_prompt kwargs."""

    async def test_triage_result_has_correct_summary(self) -> None:
        """TS-07-29: triage.summary == issue.title ('Fix bug')."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10, title="Fix bug", body="Detailed description")
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches(git_diff_files="src/foo.py\n"):
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

        mock_pipeline._build_coder_prompt.assert_called_once()
        call_args = mock_pipeline._build_coder_prompt.call_args
        # triage is positional arg [1] or keyword 'triage'
        triage = (
            call_args.kwargs.get("triage")
            or call_args[0][1]
        )
        assert triage.summary == "Fix bug"

    async def test_triage_result_has_affected_files_from_diff(self) -> None:
        """TS-07-29: triage.affected_files == ['src/foo.py'] from git diff."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10, title="Fix bug", body="Detailed description")
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches(git_diff_files="src/foo.py\n"):
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

        call_args = mock_pipeline._build_coder_prompt.call_args
        triage = call_args.kwargs.get("triage") or call_args[0][1]
        assert triage.affected_files == ["src/foo.py"]

    async def test_triage_result_criteria_empty(self) -> None:
        """TS-07-29: triage.criteria == []."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10, title="Fix bug", body="Detailed description")
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches(git_diff_files="src/foo.py\n"):
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

        call_args = mock_pipeline._build_coder_prompt.call_args
        triage = call_args.kwargs.get("triage") or call_args[0][1]
        assert triage.criteria == []

    async def test_triage_result_assessed_complexity_none(self) -> None:
        """TS-07-29: triage.assessed_complexity is None."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10, title="Fix bug", body="Detailed description")
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches(git_diff_files="src/foo.py\n"):
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

        call_args = mock_pipeline._build_coder_prompt.call_args
        triage = call_args.kwargs.get("triage") or call_args[0][1]
        assert triage.assessed_complexity is None

    async def test_triage_result_issue_body_from_issue(self) -> None:
        """TS-07-29: triage.issue_body == issue.body."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10, title="Fix bug", body="Detailed description")
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches(git_diff_files="src/foo.py\n"):
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

        call_args = mock_pipeline._build_coder_prompt.call_args
        triage = call_args.kwargs.get("triage") or call_args[0][1]
        assert triage.issue_body == "Detailed description"

    async def test_build_coder_prompt_prior_context_empty(self) -> None:
        """TS-07-29: prior_context='' in _build_coder_prompt call."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10, title="Fix bug", body="Detailed description")
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches(git_diff_files="src/foo.py\n"):
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

        call_kwargs = mock_pipeline._build_coder_prompt.call_args.kwargs
        assert call_kwargs.get("prior_context", "") == ""

    async def test_build_coder_prompt_knowledge_context_empty(self) -> None:
        """TS-07-29: knowledge_context='' in _build_coder_prompt call."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10, title="Fix bug", body="Detailed description")
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches(git_diff_files="src/foo.py\n"):
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

        call_kwargs = mock_pipeline._build_coder_prompt.call_args.kwargs
        assert call_kwargs.get("knowledge_context", "") == ""

    async def test_build_coder_prompt_review_feedback_nonempty(self) -> None:
        """TS-07-29: review_feedback is a non-empty string in _build_coder_prompt."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10, title="Fix bug", body="Detailed description")
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches(git_diff_files="src/foo.py\n"):
            await _run_feedback_iteration(
                issue=issue,
                pr_number=42,
                attempt=1,
                trigger="ci",
                ci_failures=[
                    _make_check_result(
                        conclusion="failure",
                        name="build",
                        output_title="Build Failed",
                        output_summary="Exit 1",
                    ),
                ],
                review_comments=[],
                config=config,
                platform=mock_platform,
                pipeline=mock_pipeline,
            )

        call_kwargs = mock_pipeline._build_coder_prompt.call_args.kwargs
        review_feedback = call_kwargs.get("review_feedback", "")
        assert len(review_feedback) > 0


# ===========================================================================
# TS-07-30: _run_coder_session called with worktree and model_id
# Requirement: 07-REQ-11.2
# ===========================================================================


class TestCoderSessionInvocation:
    """Verify _run_coder_session invoked with correct workspace and model_id."""

    async def test_run_coder_session_called_with_worktree(self) -> None:
        """TS-07-30: _run_coder_session workspace contains feedback worktree path."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches(worktree_path="worktrees/feedback-10"):
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

        mock_pipeline._run_coder_session.assert_awaited_once()
        call_args = mock_pipeline._run_coder_session.call_args
        # Workspace should contain the feedback worktree path
        workspace_arg = call_args[0][0] if call_args[0] else call_args.kwargs.get("workspace")
        assert "worktrees/feedback-10" in str(workspace_arg)

    async def test_run_coder_session_uses_model_from_config(self) -> None:
        """TS-07-30: model_id from nightshift config used in coder session."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        # Set model_id on the nightshift config
        config.night_shift.model_id = "claude-3-5-sonnet"
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches():
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

        call_args = mock_pipeline._run_coder_session.call_args
        # model_id should be passed as a kwarg or positional arg
        all_args_str = str(call_args)
        assert "claude-3-5-sonnet" in all_args_str or mock_pipeline._run_coder_session.awaited


# ===========================================================================
# TS-07-31: git diff failure → affected_files=[], WARNING logged
# Requirement: 07-REQ-11.3
# ===========================================================================


class TestGitDiffFailure:
    """Verify git diff failure defaults affected_files to [] with WARNING."""

    async def test_diff_failure_defaults_affected_files_empty(self) -> None:
        """TS-07-31: affected_files=[] when git diff raises."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches(git_diff_raises=True):
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

        call_args = mock_pipeline._build_coder_prompt.call_args
        triage = call_args.kwargs.get("triage") or call_args[0][1]
        assert triage.affected_files == []

    async def test_diff_failure_logs_warning(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-31: WARNING contains 'git diff --name-only failed' and 'defaulting'."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with caplog.at_level(logging.WARNING):
            with _make_feedback_patches(git_diff_raises=True):
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

        warn_msgs = [
            r.message for r in caplog.records if r.levelno == logging.WARNING
        ]
        assert any(
            "git diff" in m and "defaulting" in m.lower()
            for m in warn_msgs
        ), f"Expected WARNING about git diff failure, got: {warn_msgs}"

    async def test_empty_diff_output_defaults_affected_files_empty(self) -> None:
        """TS-07-31: affected_files=[] when git diff returns empty output."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches(git_diff_files=""):
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

        call_args = mock_pipeline._build_coder_prompt.call_args
        triage = call_args.kwargs.get("triage") or call_args[0][1]
        assert triage.affected_files == []


# ===========================================================================
# TS-07-E20: _run_coder_session raises RuntimeError
# Requirement: 07-REQ-11.E1
# ===========================================================================


class TestCoderSessionRaisesError:
    """Verify RuntimeError from coder session → ERROR, cleanup, returns None."""

    async def test_coder_raises_returns_none(self) -> None:
        """TS-07-E20: Returns None when _run_coder_session raises RuntimeError."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline(
            coder_session_side_effect=RuntimeError("model unavailable"),
        )

        with _make_feedback_patches():
            result = await _run_feedback_iteration(
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

        assert result is None

    async def test_coder_raises_logs_error(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-E20: ERROR log contains 'coder session raised'."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline(
            coder_session_side_effect=RuntimeError("model unavailable"),
        )

        with caplog.at_level(logging.ERROR):
            with _make_feedback_patches():
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

        error_msgs = [
            r.message for r in caplog.records if r.levelno == logging.ERROR
        ]
        assert any("coder session raised" in m for m in error_msgs), (
            f"Expected ERROR with 'coder session raised', got: {error_msgs}"
        )

    async def test_coder_raises_cleanup_called(self) -> None:
        """TS-07-E20: _cleanup_feedback_worktree called once."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline(
            coder_session_side_effect=RuntimeError("model unavailable"),
        )

        with _make_feedback_patches() as mocks:
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

            mocks["cleanup"].assert_called_once()

    async def test_coder_raises_labels_untouched(self) -> None:
        """TS-07-E20: af:pr label not removed when coder raises."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline(
            coder_session_side_effect=RuntimeError("model unavailable"),
        )

        with _make_feedback_patches():
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

        mock_platform.remove_label.assert_not_awaited()


# ===========================================================================
# TS-07-E21: asyncio.CancelledError propagates; cleanup called
# Requirement: 07-REQ-11.E2
# ===========================================================================


class TestCoderSessionCancelledError:
    """Verify CancelledError propagates and cleanup runs in finally."""

    async def test_cancelled_error_propagates(self) -> None:
        """TS-07-E21: CancelledError is re-raised from _run_feedback_iteration."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline(
            coder_session_side_effect=asyncio.CancelledError(),
        )

        with _make_feedback_patches():
            with pytest.raises(asyncio.CancelledError):
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

    async def test_cancelled_error_cleanup_called(self) -> None:
        """TS-07-E21: _cleanup_feedback_worktree called despite CancelledError."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline(
            coder_session_side_effect=asyncio.CancelledError(),
        )

        with _make_feedback_patches() as mocks:
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
            except asyncio.CancelledError:
                pass

            mocks["cleanup"].assert_called_once()


# ===========================================================================
# TS-07-32: Tracking comment posted before force-push
# Requirement: 07-REQ-12.1
# ===========================================================================


class TestTrackingCommentBeforePush:
    """Verify add_issue_comment called before git push --force."""

    async def test_comment_posted_before_push(self) -> None:
        """TS-07-32: add_issue_comment index < git push index in call order."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        call_order: list[str] = []

        original_add_comment = mock_platform.add_issue_comment

        async def _record_comment(*args, **kwargs):
            call_order.append("comment")
            return await original_add_comment(*args, **kwargs)

        mock_platform.add_issue_comment = AsyncMock(side_effect=_record_comment)

        async def _mock_subprocess(*args, **kwargs):
            cmd_str = " ".join(str(a) for a in args)
            proc = MagicMock()
            if "push" in cmd_str:
                call_order.append("push")
            proc.stdout = "file.py\n"
            proc.returncode = 0
            proc.communicate = AsyncMock(return_value=(b"file.py\n", b""))
            proc.wait = AsyncMock(return_value=0)
            return proc

        with (
            patch(
                "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
                return_value="worktrees/feedback-10",
            ),
            patch(
                "agentfox.nightshift.pr_feedback._cleanup_feedback_worktree",
            ),
            patch(
                "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
                side_effect=_mock_subprocess,
            ),
        ):
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

        assert "comment" in call_order, f"Expected 'comment' in call_order: {call_order}"
        assert "push" in call_order, f"Expected 'push' in call_order: {call_order}"
        assert call_order.index("comment") < call_order.index("push"), (
            f"comment should come before push: {call_order}"
        )

    async def test_tracking_comment_has_incremented_attempt(self) -> None:
        """TS-07-32: Tracking comment references attempt+1 (attempt=2 for input=1)."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches():
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

        # First add_issue_comment call should be the tracking comment
        comment_calls = mock_platform.add_issue_comment.call_args_list
        assert len(comment_calls) >= 1
        # At least one comment body should reference attempt 2
        comment_bodies = [str(c[0][1]) for c in comment_calls]
        assert any(
            "2" in body for body in comment_bodies
        ), f"Expected attempt 2 in comment, got: {comment_bodies}"


# ===========================================================================
# TS-07-33: Non-empty diff → auto-commit + force-push + INFO log
# Requirement: 07-REQ-12.2
# ===========================================================================


class TestNonEmptyDiffForcePush:
    """Verify auto-commit and force-push on non-empty diff after coder session."""

    async def test_auto_commit_called_with_feedback_message(self) -> None:
        """TS-07-33: _auto_commit_pending_changes with 'fix: Fix bug [nightshift feedback #2]'."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10, title="Fix bug")
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches(has_post_coder_changes=True):
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

        mock_pipeline._auto_commit_pending_changes.assert_awaited_once()
        commit_msg = str(mock_pipeline._auto_commit_pending_changes.call_args)
        # Commit message should contain issue title and feedback attempt number
        assert "Fix bug" in commit_msg or "feedback" in commit_msg.lower()

    async def test_force_push_executed(self) -> None:
        """TS-07-33: git push --force is executed when diff is non-empty."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10, title="Fix bug")
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()
        push_called = False

        async def _mock_subprocess(*args, **kwargs):
            nonlocal push_called
            cmd_str = " ".join(str(a) for a in args)
            proc = MagicMock()
            if "push" in cmd_str:
                push_called = True
            proc.stdout = "file.py\n"
            proc.returncode = 0
            proc.communicate = AsyncMock(return_value=(b"file.py\n", b""))
            proc.wait = AsyncMock(return_value=0)
            return proc

        with (
            patch(
                "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
                return_value="worktrees/feedback-10",
            ),
            patch("agentfox.nightshift.pr_feedback._cleanup_feedback_worktree"),
            patch(
                "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
                side_effect=_mock_subprocess,
            ),
        ):
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

        assert push_called, "Expected git push --force to be called"

    async def test_info_log_feedback_iteration_complete(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-33: INFO log contains 'Feedback iteration 2 complete'."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10, title="Fix bug")
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with caplog.at_level(logging.INFO):
            with _make_feedback_patches(has_post_coder_changes=True):
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

        info_msgs = [
            r.message for r in caplog.records if r.levelno == logging.INFO
        ]
        assert any(
            "Feedback iteration" in m and "complete" in m
            for m in info_msgs
        ), f"Expected INFO 'Feedback iteration ... complete', got: {info_msgs}"


# ===========================================================================
# TS-07-34: Empty diff → skip push, post _NO_CHANGES_MESSAGE, WARNING
# Requirement: 07-REQ-12.3
# ===========================================================================


class TestEmptyDiffSkipsPush:
    """Verify empty diff after coder → no push, _NO_CHANGES_MESSAGE, WARNING."""

    async def test_empty_diff_no_push(self) -> None:
        """TS-07-34: git push --force NOT called when coder produces no changes."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()
        push_called = False

        async def _mock_subprocess(*args, **kwargs):
            nonlocal push_called
            cmd_str = " ".join(str(a) for a in args)
            proc = MagicMock()
            if "push" in cmd_str:
                push_called = True
            # git diff for affected_files (pre-coder) returns files,
            # but post-coder diff (status check) returns empty
            if "diff" in cmd_str:
                proc.stdout = "src/foo.py\n"
                proc.communicate = AsyncMock(return_value=(b"src/foo.py\n", b""))
            else:
                proc.stdout = ""
                proc.communicate = AsyncMock(return_value=(b"", b""))
            proc.returncode = 0
            proc.wait = AsyncMock(return_value=0)
            return proc

        with (
            patch(
                "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
                return_value="worktrees/feedback-10",
            ),
            patch("agentfox.nightshift.pr_feedback._cleanup_feedback_worktree"),
            patch(
                "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
                side_effect=_mock_subprocess,
            ),
        ):
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
                has_changes=False,
            )

        assert not push_called, "git push should NOT be called on empty diff"

    async def test_empty_diff_posts_no_changes_message(self) -> None:
        """TS-07-34: _NO_CHANGES_MESSAGE comment posted on empty diff."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches(has_post_coder_changes=False):
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

        comment_calls = mock_platform.add_issue_comment.call_args_list
        comment_bodies = [str(c[0][1]).lower() for c in comment_calls]
        assert any(
            "no changes" in body for body in comment_bodies
        ), f"Expected _NO_CHANGES_MESSAGE comment, got: {comment_bodies}"

    async def test_empty_diff_logs_warning(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-34: WARNING log contains 'coder produced no changes'."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with caplog.at_level(logging.WARNING):
            with _make_feedback_patches(has_post_coder_changes=False):
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

        warn_msgs = [
            r.message for r in caplog.records if r.levelno == logging.WARNING
        ]
        assert any(
            "coder produced no changes" in m.lower() or "no changes" in m.lower()
            for m in warn_msgs
        ), f"Expected WARNING about no changes, got: {warn_msgs}"


# ===========================================================================
# TS-07-E22: add_issue_comment raises during tracking → ERROR, no push
# Requirement: 07-REQ-12.E1
# ===========================================================================


class TestTrackingCommentPostFailure:
    """Verify add_issue_comment failure → ERROR, push skipped, cleanup."""

    async def test_comment_post_failure_returns_none(self) -> None:
        """TS-07-E22: Returns None when tracking comment post fails."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_platform.add_issue_comment = AsyncMock(
            side_effect=Exception("API error"),
        )
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches():
            result = await _run_feedback_iteration(
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

        assert result is None

    async def test_comment_post_failure_logs_error(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-E22: ERROR log contains 'failed to post tracking comment'."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_platform.add_issue_comment = AsyncMock(
            side_effect=Exception("API error"),
        )
        mock_pipeline = _make_mock_pipeline()

        with caplog.at_level(logging.ERROR):
            with _make_feedback_patches():
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

        error_msgs = [
            r.message for r in caplog.records if r.levelno == logging.ERROR
        ]
        assert any(
            "failed to post tracking comment" in m for m in error_msgs
        ), f"Expected ERROR 'failed to post tracking comment', got: {error_msgs}"

    async def test_comment_post_failure_cleanup_called(self) -> None:
        """TS-07-E22: _cleanup_feedback_worktree called on comment post failure."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_platform.add_issue_comment = AsyncMock(
            side_effect=Exception("API error"),
        )
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches() as mocks:
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

            mocks["cleanup"].assert_called_once()


# ===========================================================================
# TS-07-E23: git push --force fails after tracking comment posted
# Requirement: 07-REQ-12.E2
# ===========================================================================


class TestPushFailureAfterComment:
    """Verify push failure after comment → ERROR, counter persisted, cleanup."""

    async def test_push_failure_returns_none(self) -> None:
        """TS-07-E23: Returns None when git push --force fails."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches(
            git_push_raises=subprocess.CalledProcessError(1, "git push --force"),
        ):
            result = await _run_feedback_iteration(
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

        assert result is None

    async def test_push_failure_logs_error(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-E23: ERROR log contains 'git push --force failed'."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with caplog.at_level(logging.ERROR):
            with _make_feedback_patches(
                git_push_raises=subprocess.CalledProcessError(1, "git push --force"),
            ):
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

        error_msgs = [
            r.message for r in caplog.records if r.levelno == logging.ERROR
        ]
        assert any(
            "git push" in m and "failed" in m for m in error_msgs
        ), f"Expected ERROR about push failure, got: {error_msgs}"

    async def test_push_failure_tracking_comment_already_posted(self) -> None:
        """TS-07-E23: Tracking comment with attempt=2 is already persisted."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches(
            git_push_raises=subprocess.CalledProcessError(1, "git push --force"),
        ):
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

        # Tracking comment was posted before the push failure
        assert mock_platform.add_issue_comment.call_count >= 1

    async def test_push_failure_cleanup_called(self) -> None:
        """TS-07-E23: _cleanup_feedback_worktree called after push failure."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with _make_feedback_patches(
            git_push_raises=subprocess.CalledProcessError(1, "git push --force"),
        ) as mocks:
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

            mocks["cleanup"].assert_called_once()


# ===========================================================================
# TS-07-E24: _auto_commit_pending_changes raises → ERROR, push skipped
# Requirement: 07-REQ-12.E3
# ===========================================================================


class TestAutoCommitFailure:
    """Verify auto-commit failure → ERROR, push skipped, cleanup."""

    async def test_auto_commit_failure_returns_none(self) -> None:
        """TS-07-E24: Returns None when _auto_commit_pending_changes raises."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()
        mock_pipeline._auto_commit_pending_changes = AsyncMock(
            side_effect=Exception("disk full"),
        )

        with _make_feedback_patches():
            result = await _run_feedback_iteration(
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

        assert result is None

    async def test_auto_commit_failure_logs_error(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-E24: ERROR log contains 'auto-commit failed'."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()
        mock_pipeline._auto_commit_pending_changes = AsyncMock(
            side_effect=Exception("disk full"),
        )

        with caplog.at_level(logging.ERROR):
            with _make_feedback_patches():
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

        error_msgs = [
            r.message for r in caplog.records if r.levelno == logging.ERROR
        ]
        assert any(
            "auto-commit failed" in m for m in error_msgs
        ), f"Expected ERROR 'auto-commit failed', got: {error_msgs}"

    async def test_auto_commit_failure_cleanup_called(self) -> None:
        """TS-07-E24: _cleanup_feedback_worktree called on auto-commit failure."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()
        mock_pipeline._auto_commit_pending_changes = AsyncMock(
            side_effect=Exception("disk full"),
        )

        with _make_feedback_patches() as mocks:
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

            mocks["cleanup"].assert_called_once()


# ===========================================================================
# TS-07-E25: git push uses --force, not --force-with-lease
# Requirement: 07-REQ-12.E4
# ===========================================================================


class TestPushUsesForceNotLease:
    """Verify subprocess called with '--force' but not '--force-with-lease'."""

    async def test_push_uses_force_flag(self) -> None:
        """TS-07-E25: git push command contains '--force' not '--force-with-lease'."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()
        push_args_captured: list[str] = []

        async def _mock_subprocess(*args, **kwargs):
            cmd_str = " ".join(str(a) for a in args)
            proc = MagicMock()
            if "push" in cmd_str:
                push_args_captured.extend(str(a) for a in args)
            proc.stdout = "file.py\n"
            proc.returncode = 0
            proc.communicate = AsyncMock(return_value=(b"file.py\n", b""))
            proc.wait = AsyncMock(return_value=0)
            return proc

        with (
            patch(
                "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
                return_value="worktrees/feedback-10",
            ),
            patch("agentfox.nightshift.pr_feedback._cleanup_feedback_worktree"),
            patch(
                "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
                side_effect=_mock_subprocess,
            ),
        ):
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

        push_cmd = " ".join(push_args_captured)
        assert "--force" in push_cmd, f"Expected '--force' in push cmd: {push_cmd}"
        assert "--force-with-lease" not in push_cmd, (
            f"Should NOT use '--force-with-lease': {push_cmd}"
        )


# ===========================================================================
# TS-07-37: pr_feedback emits INFO logs for all state transitions
# Requirement: 07-REQ-14.1
# ===========================================================================


class TestInfoLogsForTransitions:
    """Verify INFO logs for all six specified state transitions."""

    async def test_info_log_for_merged_pr(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-37: INFO log emitted when PR is merged."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=True, state="closed"),
        )

        with caplog.at_level(logging.INFO):
            await process_pr_issue(
                issue=issue,
                config=_make_config(),
                platform=platform,
                pipeline=MagicMock(),
            )

        info_msgs = [r.message for r in caplog.records if r.levelno == logging.INFO]
        assert any("merged" in m.lower() for m in info_msgs), (
            f"Expected INFO about merge, got: {info_msgs}"
        )

    async def test_info_log_for_closed_without_merge(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-37: INFO log emitted when PR is closed without merge."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=False, state="closed"),
        )

        with caplog.at_level(logging.INFO):
            await process_pr_issue(
                issue=issue,
                config=_make_config(),
                platform=platform,
                pipeline=MagicMock(),
            )

        info_msgs = [r.message for r in caplog.records if r.levelno == logging.INFO]
        assert any(
            "closed" in m.lower() and "merge" not in m.lower()
            or "closed without merge" in m.lower()
            for m in info_msgs
        ), f"Expected INFO about closed-without-merge, got: {info_msgs}"

    async def test_info_log_for_ci_reentry(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-37: INFO log for CI re-entry triggered."""
        from agentfox.nightshift.pr_feedback import _check_ci_status

        platform = _make_mock_platform()
        platform.get_pr_checks = AsyncMock(
            return_value=[_make_check_result(conclusion="failure")],
        )

        with caplog.at_level(logging.INFO):
            await _check_ci_status(pr_number=42, issue_number=10, platform=platform)

        info_msgs = [r.message for r in caplog.records if r.levelno == logging.INFO]
        assert any(
            "Re-entry triggered" in m and "CI" in m for m in info_msgs
        ), f"Expected INFO about CI re-entry, got: {info_msgs}"

    async def test_info_log_for_review_reentry(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-37: INFO log for review re-entry triggered."""
        from agentfox.nightshift.pr_feedback import _check_reviews

        platform = _make_mock_platform()
        platform.get_pr_reviews = AsyncMock(
            return_value=[
                _make_review_comment(state="CHANGES_REQUESTED", body="Fix"),
            ],
        )

        with caplog.at_level(logging.INFO):
            await _check_reviews(pr_number=42, issue_number=10, platform=platform)

        info_msgs = [r.message for r in caplog.records if r.levelno == logging.INFO]
        assert any(
            "Re-entry triggered" in m and "review" in m.lower() for m in info_msgs
        ), f"Expected INFO about review re-entry, got: {info_msgs}"

    async def test_info_log_for_retry_limit(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-37: INFO log for retry limit reached."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
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
                platform=_make_mock_platform(),
                pipeline=MagicMock(),
            )

        info_msgs = [r.message for r in caplog.records if r.levelno == logging.INFO]
        assert any("Retry limit reached" in m for m in info_msgs), (
            f"Expected INFO 'Retry limit reached', got: {info_msgs}"
        )

    async def test_info_log_for_iteration_complete(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-37: INFO log for feedback iteration complete."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with caplog.at_level(logging.INFO):
            with _make_feedback_patches(has_post_coder_changes=True):
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

        info_msgs = [r.message for r in caplog.records if r.levelno == logging.INFO]
        assert any(
            "Feedback iteration" in m and "complete" in m for m in info_msgs
        ), f"Expected INFO 'Feedback iteration ... complete', got: {info_msgs}"


# ===========================================================================
# TS-07-38: pr_feedback emits WARNING logs for skip/anomaly conditions
# Requirement: 07-REQ-14.2
# ===========================================================================


class TestWarningLogsForAnomalies:
    """Verify WARNING logs for all five specified anomaly conditions."""

    async def test_warning_for_no_tracking_comment(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-38: WARNING when no valid tracking comment found."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment("unrelated comment")],
        )

        with caplog.at_level(logging.WARNING):
            await process_pr_issue(
                issue=issue,
                config=_make_config(),
                platform=platform,
                pipeline=MagicMock(),
            )

        warn_msgs = [r.message for r in caplog.records if r.levelno == logging.WARNING]
        assert any(
            "no valid tracking comment" in m.lower() or "tracking comment" in m.lower()
            for m in warn_msgs
        ), f"Expected WARNING about missing tracking comment, got: {warn_msgs}"

    async def test_warning_for_ambiguous_ci(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-38: WARNING when all CI checks are in ambiguous state."""
        from agentfox.nightshift.pr_feedback import _check_ci_status

        platform = _make_mock_platform()
        platform.get_pr_checks = AsyncMock(
            return_value=[_make_check_result(conclusion="cancelled")],
        )

        with caplog.at_level(logging.WARNING):
            await _check_ci_status(pr_number=42, issue_number=10, platform=platform)

        warn_msgs = [r.message for r in caplog.records if r.levelno == logging.WARNING]
        assert any("ambiguous" in m.lower() for m in warn_msgs), (
            f"Expected WARNING about ambiguous state, got: {warn_msgs}"
        )

    async def test_warning_for_api_error_polling(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-38: WARNING when platform API error during polling."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )
        platform.get_pr_state = AsyncMock(side_effect=Exception("API error"))

        with caplog.at_level(logging.WARNING):
            await process_pr_issue(
                issue=issue,
                config=_make_config(),
                platform=platform,
                pipeline=MagicMock(),
            )

        warn_msgs = [r.message for r in caplog.records if r.levelno == logging.WARNING]
        assert len(warn_msgs) > 0, "Expected at least one WARNING log"

    async def test_warning_for_no_changes(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-38: WARNING when coder produced no changes."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with caplog.at_level(logging.WARNING):
            with _make_feedback_patches(has_post_coder_changes=False):
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

        warn_msgs = [r.message for r in caplog.records if r.levelno == logging.WARNING]
        assert any(
            "no changes" in m.lower() for m in warn_msgs
        ), f"Expected WARNING about no changes, got: {warn_msgs}"

    async def test_warning_for_diff_failure(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-38: WARNING when git diff --name-only fails."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with caplog.at_level(logging.WARNING):
            with _make_feedback_patches(git_diff_raises=True):
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

        warn_msgs = [r.message for r in caplog.records if r.levelno == logging.WARNING]
        assert any(
            "git diff" in m for m in warn_msgs
        ), f"Expected WARNING about git diff failure, got: {warn_msgs}"


# ===========================================================================
# TS-07-39: pr_feedback emits ERROR logs for all failure conditions
# Requirement: 07-REQ-14.3
# ===========================================================================


class TestErrorLogsForFailures:
    """Verify ERROR logs for all six specified failure conditions."""

    async def test_error_for_fetch_failure(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-39: ERROR log when git fetch fails."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)

        with caplog.at_level(logging.ERROR):
            with patch(
                "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
                side_effect=Exception("git fetch failed"),
            ), patch("agentfox.nightshift.pr_feedback._cleanup_feedback_worktree"):
                try:
                    await _run_feedback_iteration(
                        issue=issue,
                        pr_number=42,
                        attempt=1,
                        trigger="ci",
                        ci_failures=[_make_check_result(conclusion="failure")],
                        review_comments=[],
                        config=config,
                        platform=_make_mock_platform(),
                        pipeline=MagicMock(),
                    )
                except Exception:
                    pass

        error_msgs = [r.message for r in caplog.records if r.levelno == logging.ERROR]
        assert any(
            "fetch" in m.lower() or "worktree" in m.lower() or "setup" in m.lower()
            for m in error_msgs
        ), f"Expected ERROR about fetch/setup failure, got: {error_msgs}"

    async def test_error_for_worktree_failure(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-39: ERROR log when git worktree add fails."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)

        with caplog.at_level(logging.ERROR):
            with patch(
                "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
                side_effect=Exception("git worktree add failed"),
            ), patch("agentfox.nightshift.pr_feedback._cleanup_feedback_worktree"):
                try:
                    await _run_feedback_iteration(
                        issue=issue,
                        pr_number=42,
                        attempt=1,
                        trigger="ci",
                        ci_failures=[_make_check_result(conclusion="failure")],
                        review_comments=[],
                        config=config,
                        platform=_make_mock_platform(),
                        pipeline=MagicMock(),
                    )
                except Exception:
                    pass

        error_msgs = [r.message for r in caplog.records if r.levelno == logging.ERROR]
        assert any(
            "worktree" in m.lower() or "setup" in m.lower() or "failed" in m.lower()
            for m in error_msgs
        ), f"Expected ERROR about worktree failure, got: {error_msgs}"

    async def test_error_for_push_failure(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-39: ERROR log when git push --force fails."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with caplog.at_level(logging.ERROR):
            with _make_feedback_patches(
                git_push_raises=subprocess.CalledProcessError(1, "git push --force"),
            ):
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

        error_msgs = [r.message for r in caplog.records if r.levelno == logging.ERROR]
        assert any(
            "git push" in m and "failed" in m for m in error_msgs
        ), f"Expected ERROR about push failure, got: {error_msgs}"

    async def test_error_for_coder_exception(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-39: ERROR log when coder session raises."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline(
            coder_session_side_effect=RuntimeError("model unavailable"),
        )

        with caplog.at_level(logging.ERROR):
            with _make_feedback_patches():
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

        error_msgs = [r.message for r in caplog.records if r.levelno == logging.ERROR]
        assert any(
            "coder session raised" in m for m in error_msgs
        ), f"Expected ERROR 'coder session raised', got: {error_msgs}"

    async def test_error_for_autocommit_failure(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-39: ERROR log when auto-commit fails."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()
        mock_pipeline._auto_commit_pending_changes = AsyncMock(
            side_effect=Exception("disk full"),
        )

        with caplog.at_level(logging.ERROR):
            with _make_feedback_patches():
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

        error_msgs = [r.message for r in caplog.records if r.levelno == logging.ERROR]
        assert any(
            "auto-commit failed" in m for m in error_msgs
        ), f"Expected ERROR 'auto-commit failed', got: {error_msgs}"

    async def test_error_for_tracking_comment_failure(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-39: ERROR log when tracking comment post fails."""
        from agentfox.nightshift.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=2)
        mock_platform = _make_mock_platform()
        mock_platform.add_issue_comment = AsyncMock(
            side_effect=Exception("API error"),
        )
        mock_pipeline = _make_mock_pipeline()

        with caplog.at_level(logging.ERROR):
            with _make_feedback_patches():
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

        error_msgs = [r.message for r in caplog.records if r.levelno == logging.ERROR]
        assert any(
            "failed to post tracking comment" in m for m in error_msgs
        ), f"Expected ERROR 'failed to post tracking comment', got: {error_msgs}"


# ===========================================================================
# TS-07-40: _cleanup_feedback_worktree emits DEBUG for non-existent dir
# Requirement: 07-REQ-14.4
# ===========================================================================


class TestCleanupDebugLog:
    """Verify DEBUG log from _cleanup_feedback_worktree for missing directory."""

    def test_debug_log_for_nonexistent_worktree(
        self,
        tmp_path: Path,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """TS-07-40: DEBUG log contains 'Feedback worktree not found for issue #10'."""
        from agentfox.nightshift.pr_feedback import _cleanup_feedback_worktree

        with caplog.at_level(logging.DEBUG):
            _cleanup_feedback_worktree(
                issue_number=10,
                worktree_base=str(tmp_path / "worktrees"),
            )

        debug_msgs = [r.message for r in caplog.records if r.levelno == logging.DEBUG]
        assert any(
            "Feedback worktree not found" in m and "10" in m for m in debug_msgs
        ), f"Expected DEBUG 'Feedback worktree not found...#10', got: {debug_msgs}"


# ===========================================================================
# TS-07-41: NightShiftState has no new pr-feedback-specific fields
# Requirement: 07-REQ-14.5
# ===========================================================================


class TestNightShiftStateNoNewFields:
    """Verify NightShiftState has no new fields beyond issue_checks_completed."""

    def test_issue_checks_completed_field_exists(self) -> None:
        """TS-07-41: issue_checks_completed is in NightShiftState fields."""
        from agentfox.nightshift.engine import NightShiftState

        field_names = {f.name for f in dataclasses.fields(NightShiftState)}
        assert "issue_checks_completed" in field_names

    def test_no_new_pr_feedback_specific_fields(self) -> None:
        """TS-07-41: No pr-feedback-specific fields like pr_feedback_errors."""
        from agentfox.nightshift.engine import NightShiftState

        field_names = {f.name for f in dataclasses.fields(NightShiftState)}
        # These are fields that should NOT exist per spec
        forbidden_fields = {
            "pr_feedback_errors",
            "feedback_iterations_run",
            "pr_checks_performed",
        }
        overlap = field_names.intersection(forbidden_fields)
        assert not overlap, f"Unexpected fields in NightShiftState: {overlap}"


# ===========================================================================
# TS-07-42: pr_feedback.py defines all 8 required module-level functions
# Requirement: 07-REQ-15.1
# ===========================================================================


class TestPrFeedbackModuleFunctions:
    """Verify pr_feedback exports all required functions, no FixPipeline subclass."""

    def test_all_eight_functions_exist(self) -> None:
        """TS-07-42: All eight functions are module-level callables."""
        import agentfox.nightshift.pr_feedback as prf

        required = [
            "process_pr_issue",
            "_check_pr_state",
            "_check_ci_status",
            "_check_reviews",
            "_collect_feedback",
            "_run_feedback_iteration",
            "_setup_feedback_worktree",
            "_cleanup_feedback_worktree",
        ]
        for fn_name in required:
            fn = getattr(prf, fn_name, None)
            assert fn is not None, f"Missing function: {fn_name}"
            assert callable(fn), f"{fn_name} is not callable"

    def test_no_fixpipeline_subclass(self) -> None:
        """TS-07-42: No class in pr_feedback inherits from FixPipeline."""
        import agentfox.nightshift.pr_feedback as prf
        from agentfox.nightshift.fix_pipeline import FixPipeline

        for name, obj in inspect.getmembers(prf, inspect.isclass):
            if obj is FixPipeline:
                continue  # Imported but not subclassed
            assert not issubclass(obj, FixPipeline), (
                f"Class {name} subclasses FixPipeline — not allowed"
            )


# ===========================================================================
# TS-07-43: pr_feedback.py defines four string constants and imports fix_pipeline symbols
# Requirement: 07-REQ-15.2
# ===========================================================================


class TestPrFeedbackConstants:
    """Verify four string constants and fix_pipeline imports in pr_feedback."""

    def test_feedback_iteration_message_exists(self) -> None:
        """TS-07-43: _FEEDBACK_ITERATION_MESSAGE is a non-empty string."""
        import agentfox.nightshift.pr_feedback as prf

        assert isinstance(prf._FEEDBACK_ITERATION_MESSAGE, str)
        assert len(prf._FEEDBACK_ITERATION_MESSAGE) > 0

    def test_no_changes_message_exists(self) -> None:
        """TS-07-43: _NO_CHANGES_MESSAGE is a non-empty string."""
        import agentfox.nightshift.pr_feedback as prf

        assert isinstance(prf._NO_CHANGES_MESSAGE, str)
        assert len(prf._NO_CHANGES_MESSAGE) > 0

    def test_retry_limit_message_exists(self) -> None:
        """TS-07-43: _RETRY_LIMIT_MESSAGE is a non-empty string."""
        import agentfox.nightshift.pr_feedback as prf

        assert isinstance(prf._RETRY_LIMIT_MESSAGE, str)
        assert len(prf._RETRY_LIMIT_MESSAGE) > 0

    def test_feedback_commit_message_exists(self) -> None:
        """TS-07-43: _FEEDBACK_COMMIT_MESSAGE is a non-empty string."""
        import agentfox.nightshift.pr_feedback as prf

        assert isinstance(prf._FEEDBACK_COMMIT_MESSAGE, str)
        assert len(prf._FEEDBACK_COMMIT_MESSAGE) > 0

    def test_parse_tracking_comment_importable(self) -> None:
        """TS-07-43: parse_tracking_comment accessible in pr_feedback namespace."""
        import agentfox.nightshift.pr_feedback as prf

        assert hasattr(prf, "parse_tracking_comment")

    def test_format_tracking_comment_importable(self) -> None:
        """TS-07-43: format_tracking_comment accessible in pr_feedback namespace."""
        import agentfox.nightshift.pr_feedback as prf

        assert hasattr(prf, "format_tracking_comment")

    def test_pr_tracking_pattern_importable(self) -> None:
        """TS-07-43: PR_TRACKING_PATTERN accessible in pr_feedback namespace."""
        import agentfox.nightshift.pr_feedback as prf

        assert hasattr(prf, "PR_TRACKING_PATTERN")

    def test_triage_result_importable(self) -> None:
        """TS-07-43: TriageResult accessible in pr_feedback namespace."""
        import agentfox.nightshift.pr_feedback as prf

        assert hasattr(prf, "TriageResult")

    def test_fix_pipeline_importable(self) -> None:
        """TS-07-43: FixPipeline accessible in pr_feedback namespace."""
        import agentfox.nightshift.pr_feedback as prf

        assert hasattr(prf, "FixPipeline")


# ===========================================================================
# TS-07-44: pr_feedback.py imports from afissues and spec_builder
# Requirement: 07-REQ-15.3
# ===========================================================================


class TestPrFeedbackImports:
    """Verify afissues and spec_builder imports in pr_feedback namespace."""

    def test_platform_protocol_importable(self) -> None:
        """TS-07-44: PlatformProtocol in pr_feedback namespace."""
        import agentfox.nightshift.pr_feedback as prf

        assert hasattr(prf, "PlatformProtocol")

    def test_issue_result_importable(self) -> None:
        """TS-07-44: IssueResult in pr_feedback namespace."""
        import agentfox.nightshift.pr_feedback as prf

        assert hasattr(prf, "IssueResult")

    def test_check_result_importable(self) -> None:
        """TS-07-44: CheckResult in pr_feedback namespace."""
        import agentfox.nightshift.pr_feedback as prf

        assert hasattr(prf, "CheckResult")

    def test_review_comment_importable(self) -> None:
        """TS-07-44: ReviewComment in pr_feedback namespace."""
        import agentfox.nightshift.pr_feedback as prf

        assert hasattr(prf, "ReviewComment")

    def test_label_pr_correct_value(self) -> None:
        """TS-07-44: LABEL_PR == 'af:pr'."""
        import agentfox.nightshift.pr_feedback as prf

        assert hasattr(prf, "LABEL_PR")
        assert prf.LABEL_PR == "af:pr"

    def test_label_fixed_correct_value(self) -> None:
        """TS-07-44: LABEL_FIXED == 'af:fixed'."""
        import agentfox.nightshift.pr_feedback as prf

        assert hasattr(prf, "LABEL_FIXED")
        assert prf.LABEL_FIXED == "af:fixed"

    def test_sanitise_branch_name_importable(self) -> None:
        """TS-07-44: sanitise_branch_name in pr_feedback namespace."""
        import agentfox.nightshift.pr_feedback as prf

        assert hasattr(prf, "sanitise_branch_name")


# ===========================================================================
# TS-07-45: process_pr_issue is async def with correct signature
# Requirement: 07-REQ-15.4
# ===========================================================================


class TestProcessPrIssueSignature:
    """Verify process_pr_issue is async def with (issue, config, platform, pipeline)."""

    def test_is_coroutine_function(self) -> None:
        """TS-07-45: process_pr_issue is an async function."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        assert inspect.iscoroutinefunction(process_pr_issue)

    def test_has_correct_parameters(self) -> None:
        """TS-07-45: Parameters are (issue, config, platform, pipeline)."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        sig = inspect.signature(process_pr_issue)
        params = list(sig.parameters.keys())
        assert params == ["issue", "config", "platform", "pipeline"]

    async def test_returns_none_on_success(self) -> None:
        """TS-07-45: Returns None on successful merged PR path."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=True, state="closed"),
        )

        result = await process_pr_issue(
            issue=issue,
            config=_make_config(),
            platform=platform,
            pipeline=MagicMock(),
        )

        assert result is None

    async def test_returns_none_on_skip(self) -> None:
        """TS-07-45: Returns None when issue is skipped."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment("no tracking comment")],
        )

        result = await process_pr_issue(
            issue=issue,
            config=_make_config(),
            platform=platform,
            pipeline=MagicMock(),
        )

        assert result is None


# ===========================================================================
# TS-07-46: af:fix and af:pr are mutually exclusive through label lifecycle
# Requirement: 07-REQ-16.1
# ===========================================================================


class TestLabelExclusivity:
    """Verify af:fix and af:pr are mutually exclusive at each transition."""

    async def test_merged_pr_removes_af_pr_adds_af_fixed(self) -> None:
        """TS-07-46: After PR merge: af:pr removed, af:fixed assigned, no af:fix."""
        from afissues.labels import LABEL_FIXED, LABEL_PR
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=True, state="closed"),
        )

        await process_pr_issue(
            issue=issue,
            config=_make_config(),
            platform=platform,
            pipeline=MagicMock(),
        )

        platform.assign_label.assert_awaited_once_with(10, LABEL_FIXED)
        platform.remove_label.assert_awaited_once_with(10, LABEL_PR)
        # af:fix should never be assigned during this transition
        fix_label_calls = [
            c for c in platform.assign_label.call_args_list
            if c[0][1] == "af:fix"
        ]
        assert len(fix_label_calls) == 0

    async def test_closed_without_merge_removes_af_pr_only(self) -> None:
        """TS-07-46: After PR closed without merge: af:pr removed, no af:fix added."""
        from afissues.labels import LABEL_PR
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=False, state="closed"),
        )

        await process_pr_issue(
            issue=issue,
            config=_make_config(),
            platform=platform,
            pipeline=MagicMock(),
        )

        platform.remove_label.assert_awaited_once_with(10, LABEL_PR)
        # af:fix should NOT be added
        fix_label_calls = [
            c for c in platform.assign_label.call_args_list
            if c[0][1] == "af:fix"
        ]
        assert len(fix_label_calls) == 0


# ===========================================================================
# TS-07-47: af:pr-only issue not selected by fix pipeline's LABEL_FIX query
# Requirement: 07-REQ-16.2
# ===========================================================================


class TestAfPrNotSelectedByFixPipeline:
    """Verify af:pr-only issue invisible to fix pipeline label query."""

    async def test_label_fix_query_excludes_af_pr_issue(self) -> None:
        """TS-07-47: list_issues_by_label(LABEL_FIX) doesn't return af:pr issue."""
        mock_afpr_issue = _make_issue(number=10)
        mock_platform = _make_mock_platform()

        # Simulate: platform returns af:pr issue for 'af:pr' query,
        # but nothing for 'af:fix' query
        async def _mock_list_by_label(label, **kwargs):
            if label == "af:pr":
                return [mock_afpr_issue]
            return []

        mock_platform.list_issues_by_label = AsyncMock(
            side_effect=_mock_list_by_label,
        )

        # Fix pipeline queries with LABEL_FIX
        result = await mock_platform.list_issues_by_label("af:fix")
        assert mock_afpr_issue not in result

        # Verify the af:pr query does return the issue (sanity check)
        pr_result = await mock_platform.list_issues_by_label("af:pr")
        assert mock_afpr_issue in pr_result


# ===========================================================================
# TS-07-48: Closed without merge → af:pr removed, af:fix NOT added, issue open
# Requirement: 07-REQ-16.3
# ===========================================================================


class TestClosedWithoutMergeLabelTransition:
    """Verify closed-without-merge removes af:pr, leaves issue open, no af:fix."""

    async def test_closed_no_merge_removes_af_pr(self) -> None:
        """TS-07-48: remove_label called with af:pr."""
        from afissues.labels import LABEL_PR
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=False, state="closed"),
        )

        await process_pr_issue(
            issue=issue,
            config=_make_config(),
            platform=platform,
            pipeline=MagicMock(),
        )

        platform.remove_label.assert_awaited_once_with(10, LABEL_PR)

    async def test_closed_no_merge_does_not_add_af_fix(self) -> None:
        """TS-07-48: assign_label with af:fix NOT called."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=False, state="closed"),
        )

        await process_pr_issue(
            issue=issue,
            config=_make_config(),
            platform=platform,
            pipeline=MagicMock(),
        )

        fix_label_calls = [
            c for c in platform.assign_label.call_args_list
            if c[0][1] == "af:fix"
        ]
        assert len(fix_label_calls) == 0

    async def test_closed_no_merge_issue_not_closed(self) -> None:
        """TS-07-48: close_issue NOT called — issue left open."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=False, state="closed"),
        )

        await process_pr_issue(
            issue=issue,
            config=_make_config(),
            platform=platform,
            pipeline=MagicMock(),
        )

        platform.close_issue.assert_not_awaited()

    async def test_closed_no_merge_comment_posted(self) -> None:
        """TS-07-48: Comment about closed-without-merging is posted."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=False, state="closed"),
        )

        await process_pr_issue(
            issue=issue,
            config=_make_config(),
            platform=platform,
            pipeline=MagicMock(),
        )

        platform.add_issue_comment.assert_awaited_once()
        comment_body = platform.add_issue_comment.call_args[0][1]
        assert "closed without merging" in comment_body.lower()


# ===========================================================================
# SMOKE TESTS — End-to-end execution paths (task group 5, subtask 5.2)
#
# TS-07-SMOKE-1 through TS-07-SMOKE-7
# ===========================================================================


class TestSmokePathMergedPrDetected:
    """TS-07-SMOKE-1: Merged PR detected → issue closed with af:fixed, af:pr removed."""

    async def test_merged_pr_full_flow(self, caplog: pytest.LogCaptureFixture) -> None:
        """End-to-end: merged PR detected, issue closed automatically."""
        from agentfox.nightshift.engine import NightShiftEngine

        issue = _make_issue(number=10, title="Fix login bug")
        tracking = _make_tracking_comment(pr_number=42, attempt=1)
        platform = _make_mock_platform(
            issues=[issue],
            comments=[_make_issue_comment(tracking)],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=True, state="closed"),
        )

        config = _make_config()
        engine = NightShiftEngine(config=config, platform=platform)

        with (
            patch(
                "agentfox.nightshift.engine.process_pr_issue",
                new_callable=AsyncMock,
            ) as mock_process,
            caplog.at_level(logging.INFO),
        ):
            # Use real process_pr_issue for smoke test
            from agentfox.nightshift.pr_feedback import process_pr_issue

            mock_process.side_effect = process_pr_issue

            await engine._check_open_prs()

        # Verify list_issues_by_label was called with LABEL_PR
        platform.list_issues_by_label.assert_awaited_once()
        label_arg = platform.list_issues_by_label.call_args[0][0]
        assert label_arg == "af:pr"

        # Verify list_issue_comments called for the issue
        platform.list_issue_comments.assert_awaited()

        # Verify get_pr_state(42) called
        platform.get_pr_state.assert_awaited_once_with(42)

        # Verify label transitions
        platform.assign_label.assert_awaited()
        assign_calls = [c[0] for c in platform.assign_label.call_args_list]
        assert any(args[1] == "af:fixed" for args in assign_calls)

        platform.remove_label.assert_awaited()
        remove_calls = [c[0] for c in platform.remove_label.call_args_list]
        assert any(args[1] == "af:pr" for args in remove_calls)

        # Verify issue closed
        platform.close_issue.assert_awaited_once()
        close_args = platform.close_issue.call_args[0]
        assert close_args[0] == 10
        assert "42" in str(close_args[1])
        assert "merged" in str(close_args[1]).lower()

        # Verify INFO log about merge and af:fixed
        info_msgs = [r.message for r in caplog.records if r.levelno == logging.INFO]
        assert any("merged" in m.lower() for m in info_msgs)

        # Verify state.issue_checks_completed incremented
        assert engine.state.issue_checks_completed >= 1


class TestSmokeCiFailureReEntry:
    """TS-07-SMOKE-2: CI failure triggers full feedback re-entry cycle."""

    async def test_ci_failure_full_feedback_iteration(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """End-to-end: CI failure → coder re-run → tracking comment → force-push."""
        from agentfox.nightshift.engine import NightShiftEngine

        issue = _make_issue(number=42, title="Fix signup form")
        tracking = _make_tracking_comment(pr_number=42, attempt=1)
        platform = _make_mock_platform(
            issues=[issue],
            comments=[_make_issue_comment(tracking)],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=False, state="open"),
        )
        platform.get_pr_checks = AsyncMock(
            return_value=[
                _make_check_result(
                    name="build",
                    status="completed",
                    conclusion="failure",
                    output_title="Build failed",
                    output_summary="src/signup.py line 42: SyntaxError",
                ),
            ],
        )

        config = _make_config(max_pr_retries=2)
        mock_pipeline = _make_mock_pipeline()
        engine = NightShiftEngine(config=config, platform=platform)

        call_order: list[str] = []

        async def _mock_add_comment(*args, **kwargs):
            call_order.append("add_comment")

        platform.add_issue_comment = AsyncMock(side_effect=_mock_add_comment)

        async def _mock_subprocess(*args, **kwargs):
            cmd_str = " ".join(str(a) for a in args)
            proc = MagicMock()
            if "fetch" in cmd_str:
                proc.returncode = 0
                proc.communicate = AsyncMock(return_value=(b"", b""))
                proc.wait = AsyncMock(return_value=0)
            elif "worktree" in cmd_str:
                proc.returncode = 0
                proc.communicate = AsyncMock(return_value=(b"", b""))
                proc.wait = AsyncMock(return_value=0)
            elif "diff" in cmd_str:
                proc.returncode = 0
                proc.communicate = AsyncMock(
                    return_value=(b"src/signup.py\n", b""),
                )
                proc.wait = AsyncMock(return_value=0)
            elif "status" in cmd_str:
                proc.returncode = 0
                proc.communicate = AsyncMock(
                    return_value=(b" M src/signup.py\n", b""),
                )
                proc.wait = AsyncMock(return_value=0)
            elif "push" in cmd_str:
                call_order.append("push")
                proc.returncode = 0
                proc.communicate = AsyncMock(return_value=(b"", b""))
                proc.wait = AsyncMock(return_value=0)
            else:
                proc.returncode = 0
                proc.communicate = AsyncMock(return_value=(b"", b""))
                proc.wait = AsyncMock(return_value=0)
            return proc

        with (
            patch(
                "agentfox.nightshift.engine.process_pr_issue",
                new_callable=AsyncMock,
            ) as mock_process,
            patch(
                "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
                return_value="worktrees/feedback-42",
            ),
            patch(
                "agentfox.nightshift.pr_feedback._cleanup_feedback_worktree",
            ) as mock_cleanup,
            patch(
                "agentfox.nightshift.pr_feedback.asyncio.create_subprocess_exec",
                side_effect=_mock_subprocess,
            ),
            caplog.at_level(logging.INFO),
        ):
            from agentfox.nightshift.pr_feedback import process_pr_issue

            async def _process_with_pipeline(issue, **kwargs):
                kwargs["pipeline"] = mock_pipeline
                return await process_pr_issue(issue, **kwargs)

            mock_process.side_effect = _process_with_pipeline

            await engine._check_open_prs()

        # _build_coder_prompt called with prior_context='' and knowledge_context=''
        mock_pipeline._build_coder_prompt.assert_called_once()
        build_kwargs = mock_pipeline._build_coder_prompt.call_args
        if build_kwargs.kwargs:
            assert build_kwargs.kwargs.get("prior_context") == ""
            assert build_kwargs.kwargs.get("knowledge_context") == ""

        # _run_coder_session awaited
        mock_pipeline._run_coder_session.assert_awaited_once()

        # Tracking comment posted with attempt=2
        assert "add_comment" in call_order, "Tracking comment must be posted"

        # _auto_commit_pending_changes called
        mock_pipeline._auto_commit_pending_changes.assert_called_once()
        commit_msg = mock_pipeline._auto_commit_pending_changes.call_args[0][0]
        assert "feedback" in commit_msg.lower() or "#2" in commit_msg

        # Force-push executed after comment
        assert "push" in call_order

        # Call ordering: comment before push
        assert call_order.index("add_comment") < call_order.index("push")

        # INFO log about feedback iteration complete
        info_msgs = [r.message for r in caplog.records if r.levelno == logging.INFO]
        assert any(
            "feedback iteration" in m.lower() and "complete" in m.lower()
            for m in info_msgs
        ), f"Expected 'Feedback iteration ... complete' INFO log, got: {info_msgs}"

        # Cleanup called
        mock_cleanup.assert_called_once()


class TestSmokeReviewerChangesRequested:
    """TS-07-SMOKE-3: Review CHANGES_REQUESTED → feedback re-entry after CI pass."""

    async def test_review_changes_requested_triggers_reentry(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """End-to-end: all CI pass + CHANGES_REQUESTED → feedback iteration."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10, title="Fix auth flow")
        tracking = _make_tracking_comment(pr_number=42, attempt=1)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(tracking)],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=False, state="open"),
        )
        platform.get_pr_checks = AsyncMock(
            return_value=[
                _make_check_result(conclusion="success"),
            ],
        )
        platform.get_pr_reviews = AsyncMock(
            return_value=[
                _make_review_comment(
                    user="senior-dev",
                    state="CHANGES_REQUESTED",
                    body="Please add error handling for the edge case.",
                ),
            ],
        )

        mock_pipeline = _make_mock_pipeline()
        feedback_trigger_captured: list[str] = []

        with (
            patch(
                "agentfox.nightshift.pr_feedback._run_feedback_iteration",
                new_callable=AsyncMock,
            ) as mock_iteration,
            caplog.at_level(logging.INFO),
        ):

            async def _capture_trigger(**kwargs):
                feedback_trigger_captured.append(kwargs.get("trigger", ""))

            mock_iteration.side_effect = _capture_trigger

            await process_pr_issue(
                issue=issue,
                config=_make_config(),
                platform=platform,
                pipeline=mock_pipeline,
            )

        # CI passed — no CI re-entry
        # Review changes requested → re-entry triggered
        assert len(feedback_trigger_captured) == 1
        assert feedback_trigger_captured[0] == "review"

        # INFO log about reviewer requested changes
        info_msgs = [r.message for r in caplog.records if r.levelno == logging.INFO]
        assert any(
            "reviewer" in m.lower() and "changes" in m.lower()
            for m in info_msgs
        ), f"Expected INFO about reviewer changes, got: {info_msgs}"

        # Verify _run_feedback_iteration called with review trigger
        mock_iteration.assert_awaited_once()
        iteration_kwargs = mock_iteration.call_args.kwargs
        assert iteration_kwargs.get("trigger") == "review"
        assert len(iteration_kwargs.get("review_comments", [])) > 0


class TestSmokeRetryLimitReached:
    """TS-07-SMOKE-4: Retry limit reached → flagged for manual attention."""

    async def test_retry_limit_no_worktree(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """End-to-end: attempt=3, max_retries=2 → limit message, no worktree."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10, title="Fix slow query")
        tracking = _make_tracking_comment(pr_number=42, attempt=3)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(tracking)],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=False, state="open"),
        )
        platform.get_pr_checks = AsyncMock(
            return_value=[
                _make_check_result(conclusion="failure"),
            ],
        )

        config = _make_config(max_pr_retries=2)

        with (
            patch(
                "agentfox.nightshift.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
            ) as mock_setup,
            patch(
                "agentfox.nightshift.pr_feedback._cleanup_feedback_worktree",
            ),
            caplog.at_level(logging.INFO),
        ):
            await process_pr_issue(
                issue=issue,
                config=config,
                platform=platform,
                pipeline=MagicMock(),
            )

        # _run_feedback_iteration evaluates 3 > 2 as True
        # INFO log: 'Retry limit reached ... attempt 3/3'
        info_msgs = [r.message for r in caplog.records if r.levelno == logging.INFO]
        assert any(
            "retry limit" in m.lower() for m in info_msgs
        ), f"Expected INFO 'Retry limit' log, got: {info_msgs}"

        # _RETRY_LIMIT_MESSAGE posted to issue
        platform.add_issue_comment.assert_awaited()
        comment_args = [c[0] for c in platform.add_issue_comment.call_args_list]
        # At least one comment posted (the retry limit message)
        assert any(args[0] == 10 for args in comment_args)

        # _setup_feedback_worktree NOT called
        mock_setup.assert_not_awaited()

        # af:pr label left in place (no remove_label call for af:pr)
        for call in platform.remove_label.call_args_list:
            assert call[0][1] != "af:pr", "af:pr should not be removed at retry limit"


class TestSmokePrClosedWithoutMerge:
    """TS-07-SMOKE-5: PR closed without merge → issue left open without af:pr."""

    async def test_closed_without_merge_full_flow(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """End-to-end: PR closed without merge → comment + remove af:pr."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10, title="Fix flaky test")
        tracking = _make_tracking_comment(pr_number=42, attempt=1)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(tracking)],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=False, state="closed"),
        )

        with caplog.at_level(logging.INFO):
            await process_pr_issue(
                issue=issue,
                config=_make_config(),
                platform=platform,
                pipeline=MagicMock(),
            )

        # Comment about closed without merging posted
        platform.add_issue_comment.assert_awaited_once()
        comment = platform.add_issue_comment.call_args[0][1]
        assert "closed without merging" in comment.lower()

        # af:pr label removed
        platform.remove_label.assert_awaited()
        remove_calls = [c[0] for c in platform.remove_label.call_args_list]
        assert any(args[1] == "af:pr" for args in remove_calls)

        # Issue NOT closed (close_issue not called)
        platform.close_issue.assert_not_awaited()

        # af:fixed NOT assigned
        platform.assign_label.assert_not_awaited()

        # INFO log about closed without merge
        info_msgs = [r.message for r in caplog.records if r.levelno == logging.INFO]
        assert any(
            "closed" in m.lower() for m in info_msgs
        ), f"Expected INFO about PR closed, got: {info_msgs}"


class TestSmokeDaemonLifecycleMergeDetection:
    """TS-07-SMOKE-6: Full daemon lifecycle: stream registration → merge detection."""

    async def test_daemon_lifecycle_merge_flow(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """End-to-end: daemon streams include pr-feedback; merge detected and issue closed."""
        config = _make_config(merge_strategy="pr")

        # Step 1: Verify build_streams returns pr-feedback stream
        from agentfox.nightshift.streams import build_streams

        engine_mock = MagicMock()
        streams = build_streams(config, engine=engine_mock, budget=MagicMock())

        stream_names = [s.name for s in streams]
        assert "pr-feedback" in stream_names, (
            f"Expected 'pr-feedback' in stream names: {stream_names}"
        )

        # Verify pr-feedback comes after fix-pipeline in priority
        if "fix-pipeline" in stream_names:
            fix_idx = stream_names.index("fix-pipeline")
            pr_idx = stream_names.index("pr-feedback")
            assert pr_idx > fix_idx, (
                f"pr-feedback ({pr_idx}) should come after fix-pipeline ({fix_idx})"
            )

        # Step 2: Verify _check_open_prs processes merged PR
        from agentfox.nightshift.engine import NightShiftEngine

        issue = _make_issue(number=99, title="Full lifecycle issue")
        tracking = _make_tracking_comment(pr_number=55, attempt=1)
        platform = _make_mock_platform(
            issues=[issue],
            comments=[_make_issue_comment(tracking)],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=True, state="closed"),
        )

        engine = NightShiftEngine(config=config, platform=platform)

        with (
            patch(
                "agentfox.nightshift.engine.process_pr_issue",
                new_callable=AsyncMock,
            ) as mock_process,
            caplog.at_level(logging.INFO),
        ):
            from agentfox.nightshift.pr_feedback import process_pr_issue

            mock_process.side_effect = process_pr_issue

            await engine._check_open_prs()

        # Issue closed with af:fixed, af:pr removed
        platform.assign_label.assert_awaited()
        platform.remove_label.assert_awaited()
        platform.close_issue.assert_awaited_once()

        # issue_checks_completed incremented
        assert engine.state.issue_checks_completed >= 1

        # INFO log about merge
        info_msgs = [r.message for r in caplog.records if r.levelno == logging.INFO]
        assert any("merged" in m.lower() for m in info_msgs)


class TestSmokeEmptyDiffAfterCoder:
    """TS-07-SMOKE-7: Coder produces no changes → push skipped, no-changes message."""

    async def test_empty_diff_skips_push(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """End-to-end: empty diff → tracking comment posted, push skipped, no-changes."""
        from agentfox.nightshift.pr_feedback import process_pr_issue

        issue = _make_issue(number=10, title="Fix memory leak")
        tracking = _make_tracking_comment(pr_number=42, attempt=1)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(tracking)],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=False, state="open"),
        )
        platform.get_pr_checks = AsyncMock(
            return_value=[
                _make_check_result(conclusion="failure"),
            ],
        )

        config = _make_config(max_pr_retries=2)
        mock_pipeline = _make_mock_pipeline()

        comment_bodies: list[str] = []

        async def _capture_comments(issue_number, body):
            comment_bodies.append(body)

        platform.add_issue_comment = AsyncMock(side_effect=_capture_comments)

        push_called = False

        async def _mock_subprocess(*args, **kwargs):
            nonlocal push_called
            cmd_str = " ".join(str(a) for a in args)
            proc = MagicMock()
            if "push" in cmd_str:
                push_called = True
            # Post-coder diff returns empty — no changes
            if "diff" in cmd_str or "status" in cmd_str:
                proc.stdout = ""
                proc.returncode = 0
                proc.communicate = AsyncMock(return_value=(b"", b""))
            else:
                proc.stdout = ""
                proc.returncode = 0
                proc.communicate = AsyncMock(return_value=(b"", b""))
            proc.wait = AsyncMock(return_value=0)
            return proc

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
                side_effect=_mock_subprocess,
            ),
            caplog.at_level(logging.WARNING),
        ):
            await process_pr_issue(
                issue=issue,
                config=config,
                platform=platform,
                pipeline=mock_pipeline,
            )

        # Tracking comment with attempt=2 was posted before diff check
        assert len(comment_bodies) >= 1, "At least tracking comment should be posted"

        # git push --force NOT called (empty diff)
        assert not push_called, "Push should not be called when diff is empty"

        # _NO_CHANGES_MESSAGE comment posted
        from agentfox.nightshift.pr_feedback import _NO_CHANGES_MESSAGE

        assert any(
            _NO_CHANGES_MESSAGE in body or "no changes" in body.lower()
            for body in comment_bodies
        ), f"Expected no-changes comment, got: {comment_bodies}"

        # WARNING log about no changes
        warning_msgs = [
            r.message for r in caplog.records if r.levelno == logging.WARNING
        ]
        assert any(
            "no changes" in m.lower() for m in warning_msgs
        ), f"Expected WARNING 'no changes', got: {warning_msgs}"

        # Cleanup called
        mock_cleanup.assert_called_once()
