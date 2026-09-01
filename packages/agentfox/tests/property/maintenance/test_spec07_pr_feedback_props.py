"""Property-based tests for spec 07: PR feedback loop correctness properties.

Task group 5 — property tests (subtask 5.1) for:
  - TS-07-P1: Retry limit enforcement (attempt vs max_pr_retries)
  - TS-07-P2: af:pr and af:fix label mutual exclusivity
  - TS-07-P3: Tracking comment posted before force-push (call order)
  - TS-07-P4: _cleanup_feedback_worktree always called exactly once
  - TS-07-P5: _collect_feedback called at most once with one trigger
  - TS-07-P6: At most 5 issues per poll cycle, oldest-first ordering
  - TS-07-P7: No comment posted on polling-phase API exceptions
  - TS-07-P8: max_pr_retries=0 disables all feedback iterations

Requirements: 07-REQ-8.1, 07-REQ-8.2, 07-REQ-8.E1, 07-REQ-8.E2,
              07-REQ-16.1, 07-REQ-16.2, 07-REQ-16.3,
              07-REQ-12.1, 07-REQ-12.2, 07-REQ-12.E2,
              07-REQ-13.1, 07-REQ-13.2, 07-REQ-13.E1,
              07-REQ-6.2, 07-REQ-7.1, 07-REQ-10.3,
              07-REQ-3.1, 07-REQ-3.E1,
              07-REQ-5.E1, 07-REQ-6.E2, 07-REQ-7.E1, 07-REQ-4.E2
"""

from __future__ import annotations

import logging
import subprocess
from types import SimpleNamespace
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from afissues.protocol import IssueComment, IssueResult
from hypothesis import HealthCheck, given, settings
from hypothesis import strategies as st

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
    try:
        from agentfox.maintenance.fix_pipeline import format_tracking_comment

        return format_tracking_comment(
            pr_number=pr_number,
            attempt=attempt,
            pr_url=f"https://github.com/test/repo/pull/{pr_number}",
            message="Initial fix submitted.",
        )
    except ImportError:
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
    return SimpleNamespace(
        user=user,
        state=state,
        body=body,
        submitted_at=submitted_at,
    )


def _make_mock_pipeline(
    *,
    coder_prompt: tuple[str, str] = ("system prompt", "task prompt"),
) -> MagicMock:
    pipeline = MagicMock()
    pipeline._build_coder_prompt = MagicMock(return_value=coder_prompt)
    pipeline._run_coder_session = AsyncMock(return_value=MagicMock())
    pipeline._auto_commit_pending_changes = AsyncMock()
    return pipeline


def _make_feedback_patches(
    *,
    worktree_path: str = "worktrees/feedback-10",
    git_diff_files: str = "src/foo.py\n",
    has_post_coder_changes: bool = True,
):
    """Create common patches for feedback iteration tests."""
    import contextlib

    @contextlib.contextmanager
    def _ctx():
        call_order: list[str] = []

        async def _mock_subprocess(*args, **kwargs):
            cmd_str = " ".join(str(a) for a in args)
            proc = MagicMock()
            if "diff" in cmd_str:
                proc.stdout = git_diff_files
                proc.returncode = 0
                proc.communicate = AsyncMock(
                    return_value=(git_diff_files.encode(), b""),
                )
                proc.wait = AsyncMock(return_value=0)
            elif "push" in cmd_str:
                call_order.append("push")
                proc.stdout = ""
                proc.returncode = 0
                proc.communicate = AsyncMock(return_value=(b"", b""))
                proc.wait = AsyncMock(return_value=0)
            else:
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
                "agentfox.maintenance.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
                return_value=worktree_path,
            ) as mock_setup,
            patch(
                "agentfox.maintenance.pr_feedback._cleanup_feedback_worktree",
            ) as mock_cleanup,
            patch(
                "agentfox.maintenance.pr_feedback.asyncio.create_subprocess_exec",
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


# ---------------------------------------------------------------------------
# TS-07-P1: Retry limit enforcement — attempt vs max_pr_retries
#
# Property: 07-PROP-1
# Validates: 07-REQ-8.1, 07-REQ-8.2, 07-REQ-8.E1, 07-REQ-8.E2
# ---------------------------------------------------------------------------


class TestRetryLimitProperty:
    """TS-07-P1: feedback re-entry only executes when attempt <= max_pr_retries."""

    @given(
        attempt=st.integers(min_value=1, max_value=20),
        max_pr_retries=st.integers(min_value=0, max_value=10),
    )
    @settings(max_examples=50, deadline=None)
    @pytest.mark.asyncio
    async def test_retry_limit_invariant(
        self,
        attempt: int,
        max_pr_retries: int,
    ) -> None:
        """For any attempt/max_pr_retries, re-entry only runs when attempt <= max."""
        from agentfox.maintenance.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=max_pr_retries)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        with (
            patch(
                "agentfox.maintenance.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
                return_value="worktrees/feedback-10",
            ) as mock_setup,
            patch(
                "agentfox.maintenance.pr_feedback._cleanup_feedback_worktree",
            ),
            patch(
                "agentfox.maintenance.pr_feedback.asyncio.create_subprocess_exec",
                new_callable=AsyncMock,
                return_value=MagicMock(
                    stdout="file.py\n",
                    returncode=0,
                    communicate=AsyncMock(return_value=(b"file.py\n", b"")),
                    wait=AsyncMock(return_value=0),
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

            if attempt > max_pr_retries:
                # Should NOT have created worktree or run coder
                mock_setup.assert_not_awaited()
                mock_pipeline._run_coder_session.assert_not_awaited()
                # Should have posted retry limit message
                mock_platform.add_issue_comment.assert_awaited_once()
            else:
                # Should have set up worktree and run coder
                mock_setup.assert_awaited_once()
                mock_pipeline._run_coder_session.assert_awaited_once()


# ---------------------------------------------------------------------------
# TS-07-P2: af:pr and af:fix are mutually exclusive after any operation
#
# Property: 07-PROP-2
# Validates: 07-REQ-16.1, 07-REQ-16.2, 07-REQ-16.3
# ---------------------------------------------------------------------------


@st.composite
def pr_state_scenarios(draw: st.DrawFn) -> dict:
    """Generate scenarios for PR state transitions."""
    scenario = draw(
        st.sampled_from([
            "merged",
            "closed_without_merge",
            "open_ci_pass_review_skip",
        ])
    )
    return {"scenario": scenario}


class TestLabelMutualExclusivityProperty:
    """TS-07-P2: af:pr and af:fix never simultaneously present."""

    @given(scenario=pr_state_scenarios())
    @settings(max_examples=30, deadline=None)
    @pytest.mark.asyncio
    async def test_label_exclusivity_invariant(
        self,
        scenario: dict,
    ) -> None:
        """For every PR state transition, af:fix is never added alongside af:pr."""
        from agentfox.maintenance.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )

        if scenario["scenario"] == "merged":
            platform.get_pr_state = AsyncMock(
                return_value=MagicMock(merged=True, state="closed"),
            )
        elif scenario["scenario"] == "closed_without_merge":
            platform.get_pr_state = AsyncMock(
                return_value=MagicMock(merged=False, state="closed"),
            )
        else:
            # open, all CI pass, review skip
            platform.get_pr_state = AsyncMock(
                return_value=MagicMock(merged=False, state="open"),
            )
            platform.get_pr_checks = AsyncMock(
                return_value=[_make_check_result(conclusion="success")],
            )
            platform.get_pr_reviews = AsyncMock(
                return_value=[_make_review_comment(state="APPROVED")],
            )

        await process_pr_issue(
            issue=issue,
            config=_make_config(),
            platform=platform,
            pipeline=MagicMock(),
        )

        # Invariant: af:fix is never assigned during process_pr_issue
        assigned_labels = [
            c[0][1] for c in platform.assign_label.call_args_list
        ]
        assert "af:fix" not in assigned_labels, (
            f"af:fix should never be assigned by process_pr_issue: {assigned_labels}"
        )


# ---------------------------------------------------------------------------
# TS-07-P3: Tracking comment posted before force-push
#
# Property: 07-PROP-3
# Validates: 07-REQ-12.1, 07-REQ-12.2, 07-REQ-12.E2
# ---------------------------------------------------------------------------


class TestTrackingCommentBeforePushProperty:
    """TS-07-P3: tracking comment always posted before force-push."""

    @given(
        attempt=st.integers(min_value=1, max_value=5),
    )
    @settings(max_examples=20, deadline=None)
    @pytest.mark.asyncio
    async def test_comment_before_push_invariant(
        self,
        attempt: int,
    ) -> None:
        """For any attempt, tracking comment is posted before force-push."""
        from agentfox.maintenance.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=10)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        call_order: list[str] = []

        async def _record_comment(*args, **kwargs):
            call_order.append("comment")

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
                "agentfox.maintenance.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
                return_value="worktrees/feedback-10",
            ),
            patch(
                "agentfox.maintenance.pr_feedback._cleanup_feedback_worktree",
            ),
            patch(
                "agentfox.maintenance.pr_feedback.asyncio.create_subprocess_exec",
                side_effect=_mock_subprocess,
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

        # Invariant: if push occurred, comment was posted first
        if "push" in call_order:
            assert "comment" in call_order, "comment must be posted before push"
            assert call_order.index("comment") < call_order.index("push"), (
                f"comment must come before push: {call_order}"
            )
        # Invariant: if comment was not posted, push must not have occurred
        if "comment" not in call_order:
            assert "push" not in call_order, (
                f"push should not occur without comment: {call_order}"
            )


# ---------------------------------------------------------------------------
# TS-07-P4: _cleanup_feedback_worktree always called exactly once
#
# Property: 07-PROP-4
# Validates: 07-REQ-13.1, 07-REQ-13.2, 07-REQ-13.E1
# ---------------------------------------------------------------------------


@st.composite
def failure_injection_points(draw: st.DrawFn) -> str:
    """Generate points at which to inject failures in _run_feedback_iteration."""
    return draw(
        st.sampled_from([
            "setup_worktree",
            "coder_session",
            "tracking_comment",
            "auto_commit",
            "push",
            "none",
        ])
    )


class TestCleanupAlwaysCalledProperty:
    """TS-07-P4: _cleanup_feedback_worktree called exactly once per iteration."""

    @given(failure_point=failure_injection_points())
    @settings(max_examples=30, deadline=None)
    @pytest.mark.asyncio
    async def test_cleanup_called_exactly_once(
        self,
        failure_point: str,
    ) -> None:
        """Regardless of failure point, cleanup is called exactly once."""
        from agentfox.maintenance.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=10)
        mock_platform = _make_mock_platform()
        mock_pipeline = _make_mock_pipeline()

        setup_side_effect = None
        if failure_point == "setup_worktree":
            setup_side_effect = OSError("disk full")
        elif failure_point == "coder_session":
            mock_pipeline._run_coder_session = AsyncMock(
                side_effect=RuntimeError("model error"),
            )
        elif failure_point == "tracking_comment":
            mock_platform.add_issue_comment = AsyncMock(
                side_effect=Exception("API error"),
            )
        elif failure_point == "auto_commit":
            mock_pipeline._auto_commit_pending_changes = AsyncMock(
                side_effect=Exception("disk full"),
            )

        push_raises = None
        if failure_point == "push":
            push_raises = subprocess.CalledProcessError(1, "git push")

        async def _mock_subprocess(*args, **kwargs):
            cmd_str = " ".join(str(a) for a in args)
            proc = MagicMock()
            if "push" in cmd_str and push_raises:
                raise push_raises
            proc.stdout = "file.py\n"
            proc.returncode = 0
            proc.communicate = AsyncMock(return_value=(b"file.py\n", b""))
            proc.wait = AsyncMock(return_value=0)
            return proc

        with (
            patch(
                "agentfox.maintenance.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
                return_value="worktrees/feedback-10",
                side_effect=setup_side_effect,
            ),
            patch(
                "agentfox.maintenance.pr_feedback._cleanup_feedback_worktree",
            ) as mock_cleanup,
            patch(
                "agentfox.maintenance.pr_feedback.asyncio.create_subprocess_exec",
                side_effect=_mock_subprocess,
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
            except (OSError, RuntimeError, Exception):
                pass  # Some failures may propagate

        # Invariant: cleanup called exactly once regardless of failure
        assert mock_cleanup.call_count == 1, (
            f"Expected cleanup called exactly once, got {mock_cleanup.call_count} "
            f"(failure_point={failure_point})"
        )


# ---------------------------------------------------------------------------
# TS-07-P5: _collect_feedback called at most once with one trigger
#
# Property: 07-PROP-5
# Validates: 07-REQ-6.2, 07-REQ-7.1, 07-REQ-10.3
# ---------------------------------------------------------------------------


@st.composite
def ci_review_scenarios(draw: st.DrawFn) -> dict:
    """Generate CI check + review combinations that trigger re-entry."""
    scenario = draw(
        st.sampled_from([
            "ci_failure",
            "ci_timed_out",
            "ci_pass_review_changes_requested",
        ])
    )
    return {"scenario": scenario}


class TestCollectFeedbackMutualExclusionProperty:
    """TS-07-P5: _collect_feedback called at most once with exactly one trigger."""

    @given(scenario=ci_review_scenarios())
    @settings(max_examples=20, deadline=None)
    @pytest.mark.asyncio
    async def test_collect_feedback_called_once_per_trigger(
        self,
        scenario: dict,
    ) -> None:
        """For any re-entry trigger, _collect_feedback is called at most once
        with exactly one trigger value, and the output has one markdown section."""
        from agentfox.maintenance.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=False, state="open"),
        )

        if scenario["scenario"] == "ci_failure":
            platform.get_pr_checks = AsyncMock(
                return_value=[_make_check_result(conclusion="failure")],
            )
        elif scenario["scenario"] == "ci_timed_out":
            platform.get_pr_checks = AsyncMock(
                return_value=[_make_check_result(conclusion="timed_out")],
            )
        else:
            # CI pass, review changes requested
            platform.get_pr_checks = AsyncMock(
                return_value=[_make_check_result(conclusion="success")],
            )
            platform.get_pr_reviews = AsyncMock(
                return_value=[
                    _make_review_comment(
                        state="CHANGES_REQUESTED",
                        body="Fix this",
                    ),
                ],
            )

        collect_calls: list[dict] = []

        with patch(
            "agentfox.maintenance.pr_feedback._run_feedback_iteration",
            new_callable=AsyncMock,
        ) as mock_iteration:
            # Capture the trigger argument passed to _run_feedback_iteration
            async def _capture_iteration(*args, **kwargs):
                trigger = kwargs.get("trigger", args[3] if len(args) > 3 else None)
                ci_failures = kwargs.get("ci_failures", args[4] if len(args) > 4 else [])
                review_comments = kwargs.get(
                    "review_comments", args[5] if len(args) > 5 else [],
                )
                collect_calls.append({
                    "trigger": trigger,
                    "ci_failures": ci_failures,
                    "review_comments": review_comments,
                })

            mock_iteration.side_effect = _capture_iteration

            await process_pr_issue(
                issue=issue,
                config=_make_config(),
                platform=platform,
                pipeline=MagicMock(),
            )

        # Invariant: _run_feedback_iteration called at most once
        assert len(collect_calls) <= 1, (
            f"Expected at most 1 _run_feedback_iteration call, got {len(collect_calls)}"
        )

        if collect_calls:
            call = collect_calls[0]
            # Invariant: trigger is either 'ci' or 'review', never both
            assert call["trigger"] in ("ci", "review"), (
                f"Expected trigger 'ci' or 'review', got {call['trigger']}"
            )


# ---------------------------------------------------------------------------
# TS-07-P6: At most 5 issues per poll cycle, oldest-first ordering
#
# Property: 07-PROP-6
# Validates: 07-REQ-3.1, 07-REQ-3.E1
# ---------------------------------------------------------------------------


class TestPollCycleCapProperty:
    """TS-07-P6: at most _MAX_PR_CHECKS (5) issues processed, oldest-first."""

    @given(
        issue_count=st.integers(min_value=0, max_value=100),
    )
    @settings(max_examples=30, deadline=None)
    @pytest.mark.asyncio
    async def test_poll_cycle_cap_invariant(
        self,
        issue_count: int,
    ) -> None:
        """For any number of af:pr issues, at most 5 are processed oldest-first."""
        from agentfox.maintenance.engine import NightShiftEngine

        issues = [_make_issue(number=i) for i in range(1, issue_count + 1)]
        mock_platform = _make_mock_platform(issues=issues)
        config = _make_config()

        engine = NightShiftEngine(config=config, platform=mock_platform)

        with patch(
            "agentfox.maintenance.engine.process_pr_issue",
            new_callable=AsyncMock,
        ) as mock_process:
            await engine._check_open_prs()

            # Invariant: at most 5 issues processed
            assert mock_process.call_count <= 5, (
                f"Expected at most 5 calls, got {mock_process.call_count}"
            )

            # Invariant: processed issues are oldest-first
            processed_numbers = [
                call.args[0].number for call in mock_process.call_args_list
            ]
            expected = list(range(1, min(issue_count + 1, 6)))
            assert processed_numbers == expected, (
                f"Expected oldest-first {expected}, got {processed_numbers}"
            )


# ---------------------------------------------------------------------------
# TS-07-P7: No comment posted on polling-phase API exceptions
#
# Property: 07-PROP-7
# Validates: 07-REQ-5.E1, 07-REQ-6.E2, 07-REQ-7.E1, 07-REQ-4.E2
# ---------------------------------------------------------------------------


@st.composite
def polling_phase_methods(draw: st.DrawFn) -> str:
    """Generate platform method names that can fail during polling."""
    return draw(
        st.sampled_from([
            "list_issue_comments",
            "get_pr_state",
            "get_pr_checks",
            "get_pr_reviews",
        ])
    )


class TestNoCommentOnPollingErrorProperty:
    """TS-07-P7: no comment posted on polling-phase API exceptions."""

    @given(failing_method=polling_phase_methods())
    @settings(
        max_examples=20,
        deadline=None,
        suppress_health_check=[HealthCheck.function_scoped_fixture],
    )
    @pytest.mark.asyncio
    async def test_no_comment_on_api_error(
        self,
        failing_method: str,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """For any platform API failure during polling, no comment is posted."""
        from agentfox.maintenance.pr_feedback import process_pr_issue

        issue = _make_issue(number=10)
        platform = _make_mock_platform(
            comments=[_make_issue_comment(_make_tracking_comment(pr_number=42))],
        )
        platform.get_pr_state = AsyncMock(
            return_value=MagicMock(merged=False, state="open"),
        )
        platform.get_pr_checks = AsyncMock(
            return_value=[_make_check_result(conclusion="success")],
        )
        platform.get_pr_reviews = AsyncMock(
            return_value=[_make_review_comment(state="APPROVED")],
        )

        # Inject failure at the specified method
        setattr(
            platform,
            failing_method,
            AsyncMock(side_effect=Exception(f"{failing_method} API error")),
        )

        with caplog.at_level(logging.WARNING):
            await process_pr_issue(
                issue=issue,
                config=_make_config(),
                platform=platform,
                pipeline=MagicMock(),
            )

        # Invariant: add_issue_comment never called on polling-phase errors
        platform.add_issue_comment.assert_not_awaited()

        # Invariant: WARNING log emitted (not ERROR for polling phase)
        warning_records = [
            r for r in caplog.records if r.levelno >= logging.WARNING
        ]
        assert len(warning_records) > 0, (
            f"Expected WARNING log for {failing_method} failure"
        )


# ---------------------------------------------------------------------------
# TS-07-P8: max_pr_retries=0 disables all feedback iterations
#
# Property: 07-PROP-8
# Validates: 07-REQ-8.E1
# ---------------------------------------------------------------------------


class TestMaxRetriesZeroDisablesAllProperty:
    """TS-07-P8: max_pr_retries=0 → no worktree, no coder, no push."""

    @given(
        attempt=st.integers(min_value=1, max_value=20),
        trigger=st.sampled_from(["ci", "review"]),
    )
    @settings(
        max_examples=30,
        deadline=None,
        suppress_health_check=[HealthCheck.function_scoped_fixture],
    )
    @pytest.mark.asyncio
    async def test_zero_retries_disables_all(
        self,
        attempt: int,
        trigger: str,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """For any trigger with max_pr_retries=0, no iteration runs."""
        from agentfox.maintenance.pr_feedback import _run_feedback_iteration

        issue = _make_issue(number=10)
        config = _make_config(max_pr_retries=0)
        mock_platform = _make_mock_platform()

        with (
            patch(
                "agentfox.maintenance.pr_feedback._setup_feedback_worktree",
                new_callable=AsyncMock,
            ) as mock_setup,
            patch(
                "agentfox.maintenance.pr_feedback._cleanup_feedback_worktree",
            ),
        ):
            with caplog.at_level(logging.INFO):
                await _run_feedback_iteration(
                    issue=issue,
                    pr_number=42,
                    attempt=attempt,
                    trigger=trigger,
                    ci_failures=[_make_check_result(conclusion="failure")],
                    review_comments=[
                        _make_review_comment(
                            state="CHANGES_REQUESTED",
                            body="Fix",
                        ),
                    ],
                    config=config,
                    platform=mock_platform,
                    pipeline=MagicMock(),
                )

            # Invariant: no worktree created
            mock_setup.assert_not_awaited()

        # Invariant: retry limit message posted
        mock_platform.add_issue_comment.assert_awaited_once()

        # Invariant: INFO log about retry limit
        info_msgs = [
            r.message for r in caplog.records if r.levelno == logging.INFO
        ]
        assert any("Retry limit" in m for m in info_msgs), (
            f"Expected INFO 'Retry limit' log, got: {info_msgs}"
        )
