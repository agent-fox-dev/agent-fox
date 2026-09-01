"""PR feedback loop: monitors open PRs and re-runs coder on failures.

Detects CI failures and reviewer change requests on open pull requests,
then iteratively re-runs the coder with failure context injected.

All public and private functions are module-level — no FixPipeline
subclassing.  FixPipeline is used via composition only.

Requirements: 07-REQ-4 through 07-REQ-16
"""

from __future__ import annotations

import asyncio
import logging
import pathlib
import shutil
import subprocess
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
from agentfox.maintenance.fix_pipeline import (
    PR_TRACKING_PATTERN,  # noqa: F401 — re-exported for pr_feedback namespace
    FixPipeline,
    TriageResult,  # noqa: F401 — re-exported for pr_feedback namespace
    format_tracking_comment,  # noqa: F401 — re-exported for pr_feedback namespace
    parse_tracking_comment,
)
from agentfox.maintenance.spec_builder import sanitise_branch_name  # noqa: F401

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
    has_changes: bool | None = None,
) -> None:
    """Orchestrate a single feedback re-entry iteration.

    Checks retry limit, sets up worktree, runs coder, posts tracking
    comment, force-pushes, and cleans up.

    Requirements: 07-REQ-8, 07-REQ-9, 07-REQ-11, 07-REQ-12, 07-REQ-13
    """
    from agentfox.maintenance.spec_builder import (
        InMemorySpec,
        sanitise_branch_name,
    )
    from agentfox.workspace.worktree import WorkspaceInfo

    max_retries = config.night_shift.max_pr_retries

    # Retry limit check (07-REQ-8)
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

    branch = sanitise_branch_name(issue.title, issue.number)
    integration_branch = config.workspace.integration_branch
    new_attempt = attempt + 1

    try:
        # Step 1: Set up worktree (07-REQ-9.1)
        worktree_path = await _setup_feedback_worktree(
            issue=issue, config=config,
        )

        # Step 2: Compute affected_files via git diff (07-REQ-11.3)
        affected_files: list[str] = []
        try:
            diff_proc = await asyncio.create_subprocess_exec(
                "git", "diff", "--name-only", integration_branch, branch,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
            diff_stdout, _ = await diff_proc.communicate()
            if diff_proc.returncode == 0 and diff_stdout:
                affected_files = [
                    f for f in diff_stdout.decode(errors="replace").strip().split("\n")
                    if f
                ]
        except Exception as exc:
            logger.warning(
                "git diff --name-only failed for issue #%d, PR #%d "
                "— defaulting affected_files to []. %s",
                issue.number,
                pr_number,
                exc,
            )

        # Step 3: Collect feedback context (07-REQ-10)
        feedback_text = _collect_feedback(
            trigger=trigger,
            ci_failures=ci_failures,
            review_comments=review_comments,
        )

        # Step 4: Construct synthetic TriageResult (07-REQ-11.1)
        triage = TriageResult(
            summary=issue.title,
            affected_files=affected_files,
            criteria=[],
            assessed_complexity=None,
            issue_body=issue.body,
        )

        # Step 5: Build coder prompt (07-REQ-11.2)
        spec = InMemorySpec(
            issue_number=issue.number,
            title=issue.title,
            task_prompt=f"Fix: {issue.title}",
            system_context=issue.body or "",
            branch_name=branch,
        )

        system_prompt, task_prompt = pipeline._build_coder_prompt(
            spec,
            triage,
            review_feedback=feedback_text,
            prior_context="",
            knowledge_context="",
        )

        # Step 6: Run coder session (07-REQ-11.2)
        workspace = WorkspaceInfo(
            path=pathlib.Path(worktree_path),
            branch=branch,
            spec_name=f"feedback-{issue.number}",
            task_group=1,
        )

        model_id = getattr(config.night_shift, "model_id", None)
        try:
            await pipeline._run_coder_session(
                workspace,
                spec,
                system_prompt,
                task_prompt,
                model_id=model_id,
            )
        except asyncio.CancelledError:
            raise
        except Exception as exc:
            logger.error(
                "Error in feedback iteration for issue #%d, PR #%d: "
                "coder session raised — %s",
                issue.number,
                pr_number,
                exc,
            )
            return None

        # Step 7: Post tracking comment BEFORE push (07-REQ-12.1)
        tracking_comment = format_tracking_comment(
            pr_number=pr_number,
            attempt=new_attempt,
            pr_url="",
            message=_FEEDBACK_ITERATION_MESSAGE.format(attempt=new_attempt),
        )
        try:
            await platform.add_issue_comment(issue.number, tracking_comment)
        except Exception as exc:
            logger.error(
                "Error in feedback iteration for issue #%d, PR #%d: "
                "failed to post tracking comment — %s",
                issue.number,
                pr_number,
                exc,
            )
            return None

        # Step 8: Check for changes after coder session (07-REQ-12.2, 12.3)
        coder_made_changes = True
        if has_changes is not None:
            coder_made_changes = has_changes
        else:
            try:
                status_proc = await asyncio.create_subprocess_exec(
                    "git", "status", "--porcelain",
                    stdout=asyncio.subprocess.PIPE,
                    stderr=asyncio.subprocess.PIPE,
                    cwd=worktree_path,
                )
                status_stdout, _ = await status_proc.communicate()
                if not status_stdout or not status_stdout.strip():
                    coder_made_changes = False
            except Exception:
                pass  # Assume changes exist on error

        if not coder_made_changes:
            # No changes — skip push, post warning comment
            await platform.add_issue_comment(
                issue.number, _NO_CHANGES_MESSAGE,
            )
            logger.warning(
                "Feedback iteration %d for issue #%d, PR #%d: "
                "coder produced no changes.",
                new_attempt,
                issue.number,
                pr_number,
            )
            return None

        # Step 9: Auto-commit pending changes (07-REQ-12.2)
        commit_msg = _FEEDBACK_COMMIT_MESSAGE.format(
            issue_title=issue.title, attempt=new_attempt,
        )
        try:
            await pipeline._auto_commit_pending_changes(commit_msg, workspace)
        except Exception as exc:
            logger.error(
                "Error in feedback iteration for issue #%d, PR #%d: "
                "auto-commit failed — %s",
                issue.number,
                pr_number,
                exc,
            )
            return None

        # Step 10: Force-push (07-REQ-12.2)
        try:
            push_proc = await asyncio.create_subprocess_exec(
                "git", "push", "--force", "origin", branch,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
                cwd=worktree_path,
            )
            await push_proc.communicate()
            if push_proc.returncode != 0:
                raise subprocess.CalledProcessError(
                    push_proc.returncode, f"git push --force origin {branch}",
                )
        except Exception as exc:
            logger.error(
                "Error in feedback iteration for issue #%d, PR #%d: "
                "git push --force failed — %s",
                issue.number,
                pr_number,
                exc,
            )
            return None

        logger.info(
            "Feedback iteration %d complete for issue #%d, PR #%d.",
            new_attempt,
            issue.number,
            pr_number,
        )
        return None

    except asyncio.CancelledError:
        raise
    except Exception as exc:
        logger.error(
            "Error in feedback iteration for issue #%d, PR #%d: %s",
            issue.number,
            pr_number,
            exc,
        )
        return None
    finally:
        # Always cleanup worktree (07-REQ-13.1)
        _cleanup_feedback_worktree(issue.number)


# ---------------------------------------------------------------------------
# _setup_feedback_worktree — git fetch + worktree add (07-REQ-9)
# ---------------------------------------------------------------------------


_GIT_SUBPROCESS_TIMEOUT = 120  # seconds


async def _setup_feedback_worktree(
    *,
    issue: IssueResult,
    config: object,
    worktree_base: str = "worktrees",
) -> str:
    """Set up a feedback worktree for the given issue.

    Derives the fix branch name via ``sanitise_branch_name``, then runs
    ``git fetch origin <branch>`` followed by
    ``git worktree add worktrees/feedback-<issue_number> <branch>``.

    Returns the worktree path string on success; raises an exception on
    fetch or worktree-add failure.

    Requirements: 07-REQ-9.1, 07-REQ-9.E1, 07-REQ-9.E2, 07-REQ-9.E3
    """
    branch = sanitise_branch_name(issue.title, issue.number)
    worktree_path = str(
        pathlib.Path(worktree_base) / f"feedback-{issue.number}"
    )

    # Step 1: git fetch origin <branch>
    try:
        proc = await asyncio.wait_for(
            asyncio.create_subprocess_exec(
                "git", "fetch", "origin", branch,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            ),
            timeout=_GIT_SUBPROCESS_TIMEOUT,
        )
        stdout, stderr = await asyncio.wait_for(
            proc.communicate(),
            timeout=_GIT_SUBPROCESS_TIMEOUT,
        )
    except TimeoutError:
        logger.error(
            "git fetch timed out for issue #%d, branch %s",
            issue.number,
            branch,
        )
        raise

    if proc.returncode != 0:
        error_msg = stderr.decode(errors="replace").strip() if stderr else "unknown error"
        logger.error(
            "git fetch failed for issue #%d, branch %s — %s",
            issue.number,
            branch,
            error_msg,
        )
        raise subprocess.CalledProcessError(
            proc.returncode, f"git fetch origin {branch}",
        )

    # Step 2: git worktree add
    try:
        proc = await asyncio.wait_for(
            asyncio.create_subprocess_exec(
                "git", "worktree", "add", worktree_path, branch,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            ),
            timeout=_GIT_SUBPROCESS_TIMEOUT,
        )
        stdout, stderr = await asyncio.wait_for(
            proc.communicate(),
            timeout=_GIT_SUBPROCESS_TIMEOUT,
        )
    except TimeoutError:
        logger.error(
            "git worktree add timed out for issue #%d, path %s",
            issue.number,
            worktree_path,
        )
        raise

    if proc.returncode != 0:
        error_msg = stderr.decode(errors="replace").strip() if stderr else "unknown error"
        logger.error(
            "git worktree add failed for issue #%d, path %s — %s",
            issue.number,
            worktree_path,
            error_msg,
        )
        raise subprocess.CalledProcessError(
            proc.returncode, f"git worktree add {worktree_path} {branch}",
        )

    return worktree_path


# ---------------------------------------------------------------------------
# _cleanup_feedback_worktree — remove worktree directory (07-REQ-13)
# ---------------------------------------------------------------------------


def _cleanup_feedback_worktree(
    issue_number: int,
    *,
    worktree_base: str = "worktrees",
) -> None:
    """Remove the feedback worktree directory if it exists.

    Silently no-ops if the directory does not exist.  If removal itself
    fails (e.g. permission error), logs at WARNING and does *not*
    re-raise — the finally block must never mask the original exception.

    Requirements: 07-REQ-9.2, 07-REQ-13.1, 07-REQ-13.2, 07-REQ-13.E1
    """
    worktree_path = pathlib.Path(worktree_base) / f"feedback-{issue_number}"
    if not worktree_path.exists():
        logger.debug(
            "Feedback worktree not found for issue #%d — skipping cleanup.",
            issue_number,
        )
        return None

    try:
        shutil.rmtree(worktree_path)
    except Exception as exc:
        logger.warning(
            "Failed to remove feedback worktree for issue #%d at %s — %s",
            issue_number,
            worktree_path,
            exc,
        )
    return None
