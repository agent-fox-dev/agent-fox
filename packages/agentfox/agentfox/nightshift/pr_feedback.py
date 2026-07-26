"""PR feedback loop: monitors open PRs and re-runs coder on failures.

Detects CI failures and reviewer change requests on open pull requests,
then iteratively re-runs the coder with failure context injected.

All public and private functions are module-level — no FixPipeline
subclassing.  FixPipeline is used via composition only.

Requirements: 07-REQ-4 through 07-REQ-16
"""

from __future__ import annotations

import logging
import shutil
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Literal

from afissues.labels import LABEL_FIXED, LABEL_PR
from afissues.protocol import (
    CheckResult,
    IssueResult,
    PlatformProtocol,
    ReviewComment,
)

from agentfox.core.config import NightShiftConfig
from agentfox.nightshift.fix_pipeline import (
    PR_TRACKING_PATTERN,  # noqa: F401 — re-exported for pr_feedback namespace
    FixPipeline,
    TriageResult,  # noqa: F401 — re-exported for pr_feedback namespace
    format_tracking_comment,  # noqa: F401 — re-exported for pr_feedback namespace
    parse_tracking_comment,
)
from agentfox.nightshift.spec_builder import sanitise_branch_name  # noqa: F401

if TYPE_CHECKING:
    pass

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Module-level string constants (07-REQ-15.2)
# ---------------------------------------------------------------------------

_FEEDBACK_ITERATION_MESSAGE = (
    "Feedback iteration {attempt} applied by nightshift."
)

_NO_CHANGES_MESSAGE = (
    "Nightshift feedback iteration produced no changes. "
    "The coder session completed but did not modify any files."
)

_RETRY_LIMIT_MESSAGE = (
    "Nightshift retry limit reached for this PR. "
    "Manual intervention is required."
)

_FEEDBACK_COMMIT_MESSAGE = "fix: {issue_title} [nightshift feedback #{attempt}]"


# ---------------------------------------------------------------------------
# Internal result types for CI and review check steps
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class _CICheckResult:
    """Result of CI status evaluation."""

    action: str  # 'skip' | 're_entry' | 'pass_through'
    ci_failures: list[object] = field(default_factory=list)


@dataclass(frozen=True)
class _ReviewCheckResult:
    """Result of review state evaluation."""

    action: str  # 'skip' | 're_entry'
    review_comments: list[object] = field(default_factory=list)


# ---------------------------------------------------------------------------
# process_pr_issue — main entry point (07-REQ-15.4)
# ---------------------------------------------------------------------------


async def process_pr_issue(
    issue: IssueResult,
    config: NightShiftConfig,
    platform: PlatformProtocol,
    pipeline: FixPipeline,
) -> None:
    """Process a single PR issue through the feedback loop.

    Orchestrates the full PR check and feedback re-entry flow for a
    single issue: parse tracking comment, check PR state, check CI/reviews,
    and run feedback iteration if needed.

    Requirements: 07-REQ-4, 07-REQ-5, 07-REQ-6, 07-REQ-7, 07-REQ-15.4
    """
    # Step 1: Parse tracking comment to extract pr_number and attempt
    try:
        comments = await platform.list_issue_comments(issue.number)
    except Exception:
        logger.warning(
            "Skipped issue #%d: failed to list comments.",
            issue.number,
        )
        return None

    pr_number: int | None = None
    attempt: int = 1

    # Find the last comment matching PR_TRACKING_PATTERN
    for comment in reversed(comments):
        parsed = parse_tracking_comment(comment.body)
        if parsed is not None:
            pr_number, attempt = parsed
            break

    if pr_number is None:
        logger.warning(
            "Skipped issue #%d: no valid tracking comment found. "
            "Will retry next cycle.",
            issue.number,
        )
        return None

    # Step 2: Check PR state (merged, closed, open)
    state_result = await _check_pr_state(
        issue=issue,
        pr_number=pr_number,
        platform=platform,
    )
    if state_result is not None:
        # PR was merged or closed — state_result indicates early return
        return None

    # Step 3: Check CI status
    ci_result = await _check_ci_status(
        pr_number=pr_number,
        issue_number=issue.number,
        platform=platform,
    )

    if ci_result.action == "re_entry":
        # CI failure triggers feedback re-entry
        await _run_feedback_iteration(
            issue=issue,
            pr_number=pr_number,
            attempt=attempt,
            trigger="ci",
            ci_failures=ci_result.ci_failures,
            review_comments=[],
            config=config,
            platform=platform,
            pipeline=pipeline,
        )
        return None

    if ci_result.action == "skip":
        # In-progress/queued or ambiguous — wait for next cycle
        return None

    # Step 4: CI passed — check reviews (only if CI passed through)
    review_result = await _check_reviews(
        pr_number=pr_number,
        issue_number=issue.number,
        platform=platform,
    )

    if review_result.action == "re_entry":
        # Reviewer requested changes
        await _run_feedback_iteration(
            issue=issue,
            pr_number=pr_number,
            attempt=attempt,
            trigger="review",
            ci_failures=[],
            review_comments=review_result.review_comments,
            config=config,
            platform=platform,
            pipeline=pipeline,
        )
        return None

    # PR is healthy — skip (awaiting human merge decision)
    return None


# ---------------------------------------------------------------------------
# _check_pr_state — merged/closed/open detection (07-REQ-5)
# ---------------------------------------------------------------------------


async def _check_pr_state(
    *,
    issue: IssueResult,
    pr_number: int,
    platform: PlatformProtocol,
) -> str | None:
    """Check if PR is merged, closed, or open.

    Returns a string signal ('merged' | 'closed') if the PR is no longer
    open and the issue state has been updated.  Returns ``None`` if the
    PR is still open (caller should continue to CI check).

    Requirements: 07-REQ-5.1, 07-REQ-5.2, 07-REQ-5.3
    """
    try:
        pr_state = await platform.get_pr_state(pr_number)
    except Exception as exc:
        logger.warning(
            "Skipped issue #%d, PR #%d: get_pr_state failed — %s",
            issue.number,
            pr_number,
            exc,
        )
        return "error"

    if pr_state.merged:
        # PR merged — close issue with af:fixed label
        try:
            await platform.assign_label(issue.number, LABEL_FIXED)
            await platform.remove_label(issue.number, LABEL_PR)
            await platform.close_issue(
                issue.number, f"PR #{pr_number} merged."
            )
            logger.info(
                "PR #%d merged for issue #%d. Closed with af:fixed.",
                pr_number,
                issue.number,
            )
        except Exception as exc:
            logger.warning(
                "Skipped issue #%d, PR #%d: mid-sequence error — %s",
                issue.number,
                pr_number,
                exc,
            )
        return "merged"

    if pr_state.state == "closed":
        # PR closed without merge
        try:
            await platform.add_issue_comment(
                issue.number,
                f"PR #{pr_number} was closed without merging. "
                "Removing af:pr label for manual triage.",
            )
            await platform.remove_label(issue.number, LABEL_PR)
            logger.info(
                "PR #%d closed without merge for issue #%d. "
                "Removed af:pr for manual triage.",
                pr_number,
                issue.number,
            )
        except Exception as exc:
            logger.warning(
                "Skipped issue #%d, PR #%d: closed-PR handling error — %s",
                issue.number,
                pr_number,
                exc,
            )
        return "closed"

    # PR is open — continue to CI check
    return None


# ---------------------------------------------------------------------------
# _check_ci_status — CI check interpretation (07-REQ-6)
# ---------------------------------------------------------------------------


async def _check_ci_status(
    *,
    pr_number: int,
    issue_number: int,
    platform: PlatformProtocol,
) -> _CICheckResult:
    """Evaluate CI check results for a PR.

    Returns a ``_CICheckResult`` with action = 'skip', 're_entry', or
    'pass_through'.  API errors return action='skip'.

    Requirements: 07-REQ-6.1 through 07-REQ-6.5, 07-REQ-6.E1–E3
    """
    try:
        checks = await platform.get_pr_checks(pr_number)
    except Exception as exc:
        logger.warning(
            "Skipped issue #%d, PR #%d: get_pr_checks failed — %s",
            issue_number,
            pr_number,
            exc,
        )
        return _CICheckResult(action="skip")

    # Empty checks → treat as all passing
    if not checks:
        return _CICheckResult(action="pass_through")

    # Check for in-progress or queued (wait for completion)
    if any(c.status in ("in_progress", "queued") for c in checks):
        return _CICheckResult(action="skip")

    # Check for failures or timeouts
    failures = [
        c
        for c in checks
        if c.conclusion in ("failure", "timed_out")
    ]
    if failures:
        logger.info(
            "Re-entry triggered for issue #%d, PR #%d: CI failure/timeout.",
            issue_number,
            pr_number,
        )
        return _CICheckResult(action="re_entry", ci_failures=failures)

    # Check for all success
    if all(c.conclusion == "success" for c in checks):
        return _CICheckResult(action="pass_through")

    # Remaining: ambiguous states (cancelled, action_required, stale, None)
    # If none succeeded → ambiguous state warning
    has_success = any(c.conclusion == "success" for c in checks)
    if not has_success:
        logger.warning(
            "Skipped issue #%d, PR #%d: all checks in ambiguous state "
            "(cancelled/action_required/stale).",
            issue_number,
            pr_number,
        )
        return _CICheckResult(action="skip")

    # Mix of success and ambiguous (no failures) → pass through
    return _CICheckResult(action="pass_through")


# ---------------------------------------------------------------------------
# _check_reviews — review state interpretation (07-REQ-7)
# ---------------------------------------------------------------------------


async def _check_reviews(
    *,
    pr_number: int,
    issue_number: int,
    platform: PlatformProtocol,
) -> _ReviewCheckResult:
    """Evaluate review state for a PR.

    Returns a ``_ReviewCheckResult`` with action = 'skip' or 're_entry'.
    API errors return action='skip'.

    Requirements: 07-REQ-7.1 through 07-REQ-7.3, 07-REQ-7.E1–E3
    """
    try:
        reviews = await platform.get_pr_reviews(pr_number)
    except Exception as exc:
        logger.warning(
            "Skipped issue #%d, PR #%d: get_pr_reviews failed — %s",
            issue_number,
            pr_number,
            exc,
        )
        return _ReviewCheckResult(action="skip")

    # Filter out DISMISSED reviews and reviews with null state
    active_reviews = [
        r for r in reviews
        if r.state is not None and r.state != "DISMISSED"
    ]

    if not active_reviews:
        return _ReviewCheckResult(action="skip")

    # Check the latest active review
    latest = active_reviews[-1]
    if latest.state == "CHANGES_REQUESTED":
        logger.info(
            "Re-entry triggered for issue #%d, PR #%d: "
            "reviewer requested changes.",
            issue_number,
            pr_number,
        )
        return _ReviewCheckResult(
            action="re_entry",
            review_comments=active_reviews,
        )

    # APPROVED, COMMENTED, or other non-triggering state
    return _ReviewCheckResult(action="skip")


# ---------------------------------------------------------------------------
# _collect_feedback — feedback context collection (07-REQ-10)
# ---------------------------------------------------------------------------


def _collect_feedback(
    *,
    trigger: Literal["ci", "review"],
    ci_failures: list[CheckResult],
    review_comments: list[ReviewComment],
) -> str:
    """Format CI failures or review comments as structured markdown.

    Produces exactly one section — ``## CI Failures`` or
    ``## Review Feedback`` — depending on the trigger.  The two sections
    are never combined in a single output.

    Requirements: 07-REQ-10.1, 07-REQ-10.2, 07-REQ-10.3
    """
    if trigger == "ci":
        lines = ["## CI Failures\n"]
        for check in ci_failures:
            lines.append(f"### {check.name}\n")
            if check.output_title:
                lines.append(f"**Title:** {check.output_title}\n")
            if check.output_summary:
                lines.append(f"**Summary:** {check.output_summary}\n")
            lines.append("")
        return "\n".join(lines)

    # trigger == 'review'
    lines = ["## Review Feedback\n"]
    for review in review_comments:
        lines.append(f"### Review by {review.user}\n")
        lines.append(f"**State:** {review.state}\n")
        if review.body:
            lines.append(f"{review.body}\n")
        lines.append("")
    return "\n".join(lines)


# ---------------------------------------------------------------------------
# _run_feedback_iteration — full feedback re-entry sequence (07-REQ-8, 11, 12)
# ---------------------------------------------------------------------------


async def _run_feedback_iteration(
    *,
    issue: IssueResult,
    pr_number: int,
    attempt: int,
    trigger: Literal["ci", "review"],
    ci_failures: list[object],
    review_comments: list[object],
    config: object,
    platform: PlatformProtocol,
    pipeline: FixPipeline,
) -> None:
    """Orchestrate a single feedback re-entry iteration.

    Checks retry limit, sets up worktree, runs coder, posts tracking
    comment, force-pushes, and cleans up.

    Requirements: 07-REQ-8, 07-REQ-9, 07-REQ-11, 07-REQ-12, 07-REQ-13
    """
    max_retries = config.night_shift.max_pr_retries

    # Retry limit check
    if attempt > max_retries:
        logger.info(
            "Retry limit reached for issue #%d, PR #%d "
            "(attempt %d/%d). Needs manual attention.",
            issue.number,
            pr_number,
            attempt,
            max_retries + 1,
        )
        await platform.add_issue_comment(issue.number, _RETRY_LIMIT_MESSAGE)
        return None

    raise NotImplementedError(
        "_run_feedback_iteration: worktree/coder/push not yet implemented"
    )


# ---------------------------------------------------------------------------
# _setup_feedback_worktree — git fetch + worktree add (07-REQ-9)
# ---------------------------------------------------------------------------


async def _setup_feedback_worktree(
    issue_number: int,
    branch: str,
    *,
    worktree_base: str = "worktrees",
) -> str:
    """Set up a feedback worktree for the given issue.

    Runs ``git fetch origin <branch>`` then
    ``git worktree add worktrees/feedback-<issue_number> <branch>``.

    Requirements: 07-REQ-9.1
    """
    raise NotImplementedError(
        "_setup_feedback_worktree not yet implemented"
    )


# ---------------------------------------------------------------------------
# _cleanup_feedback_worktree — remove worktree directory (07-REQ-13)
# ---------------------------------------------------------------------------


def _cleanup_feedback_worktree(
    issue_number: int,
    *,
    worktree_base: str = "worktrees",
) -> None:
    """Remove the feedback worktree directory if it exists.

    Silently no-ops if the directory does not exist.

    Requirements: 07-REQ-13.1, 07-REQ-13.2
    """
    import pathlib

    worktree_path = pathlib.Path(worktree_base) / f"feedback-{issue_number}"
    if worktree_path.exists():
        shutil.rmtree(worktree_path)
    else:
        logger.debug(
            "Feedback worktree not found for issue #%d — skipping cleanup.",
            issue_number,
        )
