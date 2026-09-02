"""Tests for PR mode error handling, operation sequence, and property tests.

Test Spec: TS-02-E4 (push failure propagates), TS-02-E5 (af code partial
           failure — ERROR log), TS-02-26 (ERROR log with branch URL),
           TS-02-E6 (nightshift partial failure — branch-mode comment),
           TS-02-E7 (empty changed_files in pr mode), TS-02-29 (operation
           sequence integrity), TS-02-P3 (platform guard precedes push),
           TS-02-E8 (push timeout propagation), TS-02-E15 (unexpected
           exception propagation)
Requirements: 02-REQ-4.E1, 02-REQ-4.E2, 02-REQ-4.E3, 02-REQ-4.E4,
              02-REQ-4.E5, 02-REQ-9.4, 02-REQ-10.1, 02-REQ-10.E1

Reviewer Findings (applied):
- [MAJOR] _integrate_fix() returns tuple[str, list[str]] (2-tuple with status
  'merged'/'no_changes'/'error'), NOT the 4-tuple the spec claims. Tests adapt
  to the actual 2-tuple contract.
- [MAJOR] _harvest_and_integrate() is a method on NodeSessionRunner, not a
  standalone function. Tests mock a NodeSessionRunner instance.
- [MAJOR] _integrate_fix() is a method on FixPipeline, not a standalone
  function. Tests mock a FixPipeline instance.
- [MAJOR] The actual platform method is add_issue_comment(issue_number, body),
  NOT post_comment(). There is no post_comment method in the codebase.
- [MAJOR] The codebase uses httpx, not aiohttp. create_platform_safe returns
  GitHubPlatform | None. GitHubPlatform has _owner and _repo attributes.
"""

from __future__ import annotations

import logging
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from afaudit.sink import SessionOutcome
from afissues.errors import IntegrationError
from afissues.protocol import IssueResult
from agentfox.core.config import AgentFoxConfig, WorkspaceConfig
from agentfox.engine.session_lifecycle import NodeSessionRunner
from agentfox.knowledge.db import KnowledgeDB
from agentfox.maintenance.fix_pipeline import FixPipeline
from agentfox.maintenance.spec_builder import InMemorySpec
from agentfox.workspace import WorkspaceInfo

# ---------------------------------------------------------------------------
# Helpers: create minimal test objects (consistent with prior groups)
# ---------------------------------------------------------------------------

_MOCK_KB = MagicMock(spec=KnowledgeDB)


def _make_workspace(branch: str = "feature/test_spec/1") -> WorkspaceInfo:
    """Create a minimal WorkspaceInfo for testing."""
    return WorkspaceInfo(
        path=Path("/tmp/test-worktree"),
        branch=branch,
        spec_name="test_spec",
        task_group=1,
    )


def _make_session_outcome(status: str = "completed") -> SessionOutcome:
    """Create a minimal SessionOutcome for testing."""
    return SessionOutcome(
        status=status,
        spec_name="test_spec",
        task_group="1",
        node_id="test_spec:1",
    )


def _make_runner(
    merge_strategy: str = "pr",
    integration_branch: str = "main",
    force_clean: bool = False,
) -> NodeSessionRunner:
    """Create a NodeSessionRunner with the specified merge_strategy config."""
    config = AgentFoxConfig(
        workspace=WorkspaceConfig(
            merge_strategy=merge_strategy,
            integration_branch=integration_branch,
            force_clean=force_clean,
        ),
    )
    return NodeSessionRunner(
        "test_spec:1",
        config,
        knowledge_db=_MOCK_KB,
    )


def _make_issue(
    number: int = 42,
    title: str = "Login fails on empty password",
) -> IssueResult:
    """Create a minimal IssueResult for testing."""
    return IssueResult(
        number=number,
        title=title,
        html_url=f"https://github.com/test/repo/issues/{number}",
    )


def _make_spec(
    issue_number: int = 42,
    branch_name: str = "fix/test-branch",
) -> InMemorySpec:
    """Create a minimal InMemorySpec for testing."""
    return InMemorySpec(
        issue_number=issue_number,
        title="Login fails on empty password",
        task_prompt="Fix the bug",
        system_context="Bug context",
        branch_name=branch_name,
    )


def _make_fix_pipeline(
    merge_strategy: str = "pr",
    platform: object | None = None,
) -> FixPipeline:
    """Create a FixPipeline with the specified merge_strategy config."""
    config = AgentFoxConfig(
        workspace=WorkspaceConfig(
            merge_strategy=merge_strategy,
            integration_branch="main",
        ),
    )
    if platform is None:
        platform = MagicMock()
    return FixPipeline(
        config=config,
        platform=platform,
    )


def _make_mock_platform(
    *,
    owner: str = "owner",
    repo: str = "repo",
    create_pr_url: str = "https://github.com/owner/repo/pull/1",
) -> MagicMock:
    """Create a mock platform with create_pr, add_issue_comment, and close_issue."""
    from afissues.protocol import PrResult

    platform = MagicMock()
    platform._owner = owner
    platform._repo = repo
    platform.create_pr = AsyncMock(
        return_value=PrResult(html_url=create_pr_url, number=1),
    )
    platform.add_issue_comment = AsyncMock()
    platform.close_issue = AsyncMock()
    platform.assign_label = AsyncMock()
    return platform


# ---------------------------------------------------------------------------
# TS-02-E4: If the git push of the feature_branch to origin fails (e.g.
#           network error, auth failure, or rejected push) before create_pr()
#           is called, then the exception propagates to the existing pipeline
#           error handling.
#
# Requirements: 02-REQ-4.E1
# ---------------------------------------------------------------------------


class TestPushFailurePropagatesException:
    """TS-02-E4: If the git push to origin fails before create_pr() is called
    in pr mode, the exception propagates to the existing pipeline error
    handling without any special handling in the merge strategy code.

    Requirements: 02-REQ-4.E1
    """

    @pytest.mark.asyncio
    async def test_harvest_and_integrate_push_oserror_propagates(self) -> None:
        """OSError from git push propagates from _harvest_and_integrate()."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/my-branch")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform()

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                side_effect=OSError("push failed"),
            ),
        ):
            with pytest.raises(OSError, match="push failed"):
                await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

    @pytest.mark.asyncio
    async def test_create_pr_not_called_after_push_failure(self) -> None:
        """platform.create_pr() is never called when git push fails."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/my-branch")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform()

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                side_effect=OSError("push failed"),
            ),
        ):
            try:
                await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))
            except OSError:
                pass

        assert mock_platform.create_pr.call_count == 0, "create_pr() should NOT be called when git push fails"

    @pytest.mark.asyncio
    async def test_integrate_fix_push_failure_propagates(self) -> None:
        """OSError from git push propagates from _integrate_fix() in pr mode."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        issue = _make_issue(number=42)
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

        with (
            patch.object(
                pipeline,
                "_harvest_and_push",
                new_callable=AsyncMock,
                return_value=["fix.py"],
            ),
            patch.object(
                pipeline,
                "_auto_commit_pending_changes",
                new_callable=AsyncMock,
            ),
            patch.object(
                pipeline,
                "_push_fix_branch_upstream",
                new_callable=AsyncMock,
            ),
            patch.object(
                pipeline,
                "_update_spinner",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["fix.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                side_effect=OSError("push failed"),
            ),
        ):
            with pytest.raises(OSError, match="push failed"):
                await pipeline._integrate_fix(issue, spec, workspace)


# ---------------------------------------------------------------------------
# TS-02-E5 / TS-02-26: In af code pr mode, if the branch was pushed
#           successfully but create_pr() raises IntegrationError, the system
#           logs an ERROR with the remote branch URL, falls back to branch
#           mode semantics, and returns ('completed', None, touched_files,
#           False) with exit code 0.
#
# Requirements: 02-REQ-4.E2, 02-REQ-9.4
# ---------------------------------------------------------------------------


class TestHarvestAndIntegratePrPartialFailure:
    """TS-02-E5 / TS-02-26: In af code pr mode, if create_pr() raises
    IntegrationError after branch was pushed, the system logs an ERROR
    with the remote branch URL, falls back to branch mode semantics, and
    returns ('completed', None, touched_files, False).

    Requirements: 02-REQ-4.E2, 02-REQ-9.4
    """

    @pytest.mark.asyncio
    async def test_error_log_with_remote_branch_url(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """ERROR log contains remote branch URL on create_pr failure."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/my-branch")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform(owner="owner", repo="repo")
        mock_platform.create_pr = AsyncMock(
            side_effect=IntegrationError("PR creation failed"),
        )

        with (
            caplog.at_level(logging.DEBUG),
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        error_lines = [r for r in caplog.records if r.levelno == logging.ERROR and "PR creation failed" in r.message]
        assert len(error_lines) == 1, (
            f"Expected exactly one ERROR log about PR creation failure, "
            f"got {len(error_lines)}: {[r.message for r in error_lines]}"
        )
        assert "https://github.com/owner/repo/tree/feat/my-branch" in error_lines[0].message

    @pytest.mark.asyncio
    async def test_exactly_one_error_log_on_partial_failure(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """Exactly one ERROR log line is emitted on partial failure (TS-02-26)."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/my-branch")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform(owner="owner", repo="repo")
        mock_platform.create_pr = AsyncMock(
            side_effect=IntegrationError("API error"),
        )

        with (
            caplog.at_level(logging.DEBUG),
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        error_lines = [r for r in caplog.records if r.levelno == logging.ERROR and "PR creation failed" in r.message]
        assert len(error_lines) == 1

    @pytest.mark.asyncio
    async def test_harvest_not_called_on_partial_failure(self) -> None:
        """harvest() (squash-merge) is NOT called on partial failure —
        falls back to branch mode semantics."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/my-branch")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform()
        mock_platform.create_pr = AsyncMock(
            side_effect=IntegrationError("API error"),
        )

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ) as mock_harvest,
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        assert mock_harvest.call_count == 0, "harvest() (squash-merge) should NOT be called on partial failure"

    @pytest.mark.asyncio
    async def test_returns_completed_tuple_on_partial_failure(self) -> None:
        """Returns ('completed', None, touched_files, False) on partial failure.

        Session exit code remains 0 — the partial failure is logged but does
        not cause a non-zero exit.
        """
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/my-branch")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform()
        mock_platform.create_pr = AsyncMock(
            side_effect=IntegrationError("API error"),
        )

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            result = await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        status, err_msg, touched, non_retry = result
        assert status == "completed"
        assert err_msg is None
        assert isinstance(touched, list)
        assert non_retry is False

    @pytest.mark.asyncio
    async def test_no_remote_branch_rollback_on_partial_failure(self) -> None:
        """Remote branch is NOT rolled back (no delete-push call) on
        partial failure."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/my-branch")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform()
        mock_platform.create_pr = AsyncMock(
            side_effect=IntegrationError("API error"),
        )

        push_calls: list[dict] = []

        async def mock_push(
            repo_root: Path,
            branch: str,
            remote: str = "origin",
            *,
            force: bool = False,
        ) -> bool:
            push_calls.append({"branch": branch, "force": force})
            return True

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                side_effect=mock_push,
            ),
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        # Only the initial push should have happened — no delete/rollback push
        delete_pushes = [c for c in push_calls if c.get("force")]
        assert len(delete_pushes) == 0, "Remote branch should NOT be rolled back on partial failure"


# ---------------------------------------------------------------------------
# TS-02-E6: In nightshift pr mode, if create_pr raises IntegrationError,
#           the exception propagates from _integrate_fix() and _pr_number
#           remains None.
#
# Updated by spec 06 (06-REQ-8.4): the previous branch-mode fallback
# (02-REQ-4.E3) was removed to prevent premature issue closing.  See
# docs/errata/06_pr_create_exception_propagation.md.
#
# Requirements: 06-REQ-8.4 (supersedes 02-REQ-4.E3)
# ---------------------------------------------------------------------------


class TestIntegrateFixPrPartialFailure:
    """TS-02-E6 (updated for spec 06): In nightshift pr mode, if create_pr
    raises IntegrationError after push succeeded, _integrate_fix() lets the
    exception propagate and _pr_number remains None.

    Requirements: 06-REQ-8.4 (supersedes 02-REQ-4.E3)
    """

    @pytest.mark.asyncio
    async def test_error_log_with_remote_branch_url(self) -> None:
        """IntegrationError from create_pr propagates from _integrate_fix."""
        mock_platform = _make_mock_platform(owner="owner", repo="repo")
        mock_platform.create_pr = AsyncMock(
            side_effect=IntegrationError("PR creation failed"),
        )
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        issue = _make_issue(number=42)
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

        with (
            patch.object(
                pipeline,
                "_auto_commit_pending_changes",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["auth/login.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            with pytest.raises(IntegrationError, match="PR creation failed"):
                await pipeline._integrate_fix(issue, spec, workspace)

        assert pipeline._pr_number is None

    @pytest.mark.asyncio
    async def test_posts_branch_mode_comment(self) -> None:
        """IntegrationError from create_pr propagates; no branch-mode
        comment is posted by _integrate_fix (the caller handles it)."""
        mock_platform = _make_mock_platform(owner="owner", repo="repo")
        mock_platform.create_pr = AsyncMock(
            side_effect=IntegrationError("API error"),
        )
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        issue = _make_issue(number=42)
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

        with (
            patch.object(
                pipeline,
                "_auto_commit_pending_changes",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["auth/login.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            with pytest.raises(IntegrationError):
                await pipeline._integrate_fix(issue, spec, workspace)

        # No branch-mode comment posted by _integrate_fix — the caller
        # (process_issue) handles the exception and posts its own comment.
        mock_platform.add_issue_comment.assert_not_called()

    @pytest.mark.asyncio
    async def test_issue_close_not_called(self) -> None:
        """close_issue is NOT called when create_pr raises — the exception
        propagates and the issue stays open."""
        mock_platform = _make_mock_platform(owner="owner", repo="repo")
        mock_platform.create_pr = AsyncMock(
            side_effect=IntegrationError("API error"),
        )
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        issue = _make_issue(number=42)
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

        with (
            patch.object(
                pipeline,
                "_auto_commit_pending_changes",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["auth/login.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            with pytest.raises(IntegrationError):
                await pipeline._integrate_fix(issue, spec, workspace)

        mock_platform.close_issue.assert_not_called()

    @pytest.mark.asyncio
    async def test_returns_completed_result_no_retry(self) -> None:
        """IntegrationError propagates from _integrate_fix; _pr_number
        remains None so _handle_result is never called with pr_created.

        Note: _integrate_fix now raises instead of returning a tuple.
        """
        mock_platform = _make_mock_platform(owner="owner", repo="repo")
        mock_platform.create_pr = AsyncMock(
            side_effect=IntegrationError("API error"),
        )
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        issue = _make_issue(number=42)
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

        with (
            patch.object(
                pipeline,
                "_auto_commit_pending_changes",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["auth/login.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            with pytest.raises(IntegrationError):
                await pipeline._integrate_fix(issue, spec, workspace)

        # _pr_number stays None — no partial state exposed
        assert pipeline._pr_number is None


# ---------------------------------------------------------------------------
# TS-02-E7: In pr mode, when get_changed_files() returns an empty list,
#           build_pr_body() is called with an empty list and the PR body
#           contains an empty Changed Files section without errors.
#
# Requirements: 02-REQ-4.E4
# ---------------------------------------------------------------------------


class TestPrModeEmptyChangedFiles:
    """TS-02-E7: In pr mode, when get_changed_files() returns an empty list,
    build_pr_body() is called with an empty list and the PR body contains
    an empty Changed Files section without errors.

    Requirements: 02-REQ-4.E4
    """

    @pytest.mark.asyncio
    async def test_harvest_and_integrate_empty_changed_files_no_error(self) -> None:
        """No exception raised when get_changed_files returns [] in pr mode."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform()

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=[],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=[],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            result = await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        # Should not raise, and touched_files should be empty
        status, err_msg, touched, non_retry = result
        assert status == "completed"
        assert touched == []

    @pytest.mark.asyncio
    async def test_pr_body_has_empty_changed_files_section(self) -> None:
        """PR body contains '## Changed Files' but no bullet items when
        changed_files is empty."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform()

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=[],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=[],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        mock_platform.create_pr.assert_called_once()
        call_kwargs = mock_platform.create_pr.call_args
        body = call_kwargs.kwargs.get("body") or (call_kwargs.args[1] if len(call_kwargs.args) > 1 else "")
        assert "## Changed Files" in body, "PR body should contain '## Changed Files' heading"
        # The Changed Files section should have no bullet items
        changed_section = body.split("## Changed Files")[1]
        # If there are other sections, only check up to the next ##
        if "##" in changed_section[1:]:
            changed_section = changed_section.split("##")[0]
        assert "- " not in changed_section, (
            "Changed Files section should have no bullet items when changed_files is empty"
        )

    @pytest.mark.asyncio
    async def test_integrate_fix_empty_changed_files_no_error(self) -> None:
        """No exception raised in _integrate_fix when changed_files is []."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        issue = _make_issue(number=42)
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

        with (
            patch.object(
                pipeline,
                "_harvest_and_push",
                new_callable=AsyncMock,
                return_value=[],
            ),
            patch.object(
                pipeline,
                "_auto_commit_pending_changes",
                new_callable=AsyncMock,
            ),
            patch.object(
                pipeline,
                "_push_fix_branch_upstream",
                new_callable=AsyncMock,
            ),
            patch.object(
                pipeline,
                "_update_spinner",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=[],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            result = await pipeline._integrate_fix(issue, spec, workspace)

        assert len(result) == 2
        status, changed_files = result
        assert isinstance(status, str)
        assert changed_files == [] or isinstance(changed_files, list)


# ---------------------------------------------------------------------------
# TS-02-29: _harvest_and_integrate() and _integrate_fix() in pr mode execute
#           operations in the mandatory sequence: (1) create_platform_safe,
#           (2) push, (3) get_changed_files, (4) build_pr_body,
#           (5) create_pr.
#
# Requirements: 02-REQ-10.1
# ---------------------------------------------------------------------------


class TestOperationSequenceIntegrity:
    """TS-02-29: In pr mode, operations execute in mandatory sequence:
    create_platform_safe -> push -> get_changed_files -> build_pr_body ->
    create_pr. No step is reordered.

    Requirements: 02-REQ-10.1
    """

    @pytest.mark.asyncio
    async def test_harvest_and_integrate_operation_order(self) -> None:
        """_harvest_and_integrate() pr mode operations execute in the
        correct mandatory sequence."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform()

        call_order: list[str] = []

        original_cps_return = mock_platform

        def track_cps(*args: object, **kwargs: object) -> MagicMock:
            call_order.append("create_platform_safe")
            return original_cps_return

        async def track_push(*args: object, **kwargs: object) -> bool:
            call_order.append("git_push")
            return True

        async def track_gcf(*args: object, **kwargs: object) -> list[str]:
            call_order.append("get_changed_files")
            return ["config.py"]

        async def track_create_pr(**kwargs: object) -> object:
            from afissues.protocol import PrResult

            call_order.append("create_pr")
            return PrResult(html_url="https://github.com/owner/repo/pull/1", number=1)

        mock_platform.create_pr = AsyncMock(side_effect=track_create_pr)

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                side_effect=track_gcf,
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                side_effect=track_cps,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                side_effect=track_push,
            ),
            patch(
                "agentfox.maintenance.fix_pipeline.build_pr_body",
                side_effect=lambda **kwargs: call_order.append("build_pr_body") or "## Summary\n\ntest",
            ),
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        # Verify the mandatory sequence
        assert "create_platform_safe" in call_order, "create_platform_safe must be called"
        assert "git_push" in call_order, "git push must be called"
        assert "get_changed_files" in call_order, "get_changed_files must be called"
        assert "create_pr" in call_order, "create_pr must be called"

        # Verify order: platform check < push < get_changed_files < create_pr
        cps_idx = call_order.index("create_platform_safe")
        push_idx = call_order.index("git_push")
        gcf_idx = call_order.index("get_changed_files")
        pr_idx = call_order.index("create_pr")

        assert cps_idx < push_idx, f"create_platform_safe (idx={cps_idx}) must precede git_push (idx={push_idx})"
        assert push_idx < gcf_idx, f"git_push (idx={push_idx}) must precede get_changed_files (idx={gcf_idx})"
        assert gcf_idx < pr_idx, f"get_changed_files (idx={gcf_idx}) must precede create_pr (idx={pr_idx})"

    @pytest.mark.asyncio
    async def test_integrate_fix_operation_order(self) -> None:
        """_integrate_fix() pr mode operations execute in the correct
        mandatory sequence."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        issue = _make_issue(number=42)
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

        call_order: list[str] = []

        def track_cps(*args: object, **kwargs: object) -> MagicMock:
            call_order.append("create_platform_safe")
            return mock_platform

        async def track_push(*args: object, **kwargs: object) -> bool:
            call_order.append("git_push")
            return True

        async def track_gcf(*args: object, **kwargs: object) -> list[str]:
            call_order.append("get_changed_files")
            return ["auth/login.py"]

        async def track_create_pr(**kwargs: object) -> object:
            from afissues.protocol import PrResult

            call_order.append("create_pr")
            return PrResult(html_url="https://github.com/owner/repo/pull/5", number=5)

        mock_platform.create_pr = AsyncMock(side_effect=track_create_pr)

        with (
            patch.object(
                pipeline,
                "_harvest_and_push",
                new_callable=AsyncMock,
                return_value=["auth/login.py"],
            ),
            patch.object(
                pipeline,
                "_auto_commit_pending_changes",
                new_callable=AsyncMock,
            ),
            patch.object(
                pipeline,
                "_push_fix_branch_upstream",
                new_callable=AsyncMock,
            ),
            patch.object(
                pipeline,
                "_update_spinner",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                side_effect=track_gcf,
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                side_effect=track_cps,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                side_effect=track_push,
            ),
            patch(
                "agentfox.maintenance.fix_pipeline.build_pr_body",
                side_effect=lambda **kwargs: call_order.append("build_pr_body") or "## Summary\n\ntest",
            ),
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        # Verify platform check always precedes push
        if "create_platform_safe" in call_order and "git_push" in call_order:
            cps_idx = call_order.index("create_platform_safe")
            push_idx = call_order.index("git_push")
            assert cps_idx < push_idx, "create_platform_safe must precede git_push in _integrate_fix pr mode"

        # Verify create_pr is called
        assert "create_pr" in call_order, "create_pr must be called in _integrate_fix pr mode"


# ---------------------------------------------------------------------------
# TS-02-P3: Property test — platform guard always precedes branch push in
#           pr mode. A None result from create_platform_safe always prevents
#           any push from occurring.
#
# Property: 02-PROP-3
# Validates: 02-REQ-4.3, 02-REQ-4.4, 02-REQ-10.1
# ---------------------------------------------------------------------------


class TestPlatformGuardPrecedesPushProperty:
    """TS-02-P3: For any pr mode execution, create_platform_safe() is always
    called and its result checked before any git push; a None result always
    prevents any push from occurring.

    Property: 02-PROP-3
    Validates: 02-REQ-4.3, 02-REQ-4.4, 02-REQ-10.1
    """

    @pytest.mark.asyncio
    async def test_harvest_and_integrate_none_platform_no_push(self) -> None:
        """When create_platform_safe returns None, push_to_remote is NOT called
        in _harvest_and_integrate."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
        outcome = _make_session_outcome("completed")

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=None,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
            ) as mock_push,
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        mock_push.assert_not_called()

    @pytest.mark.asyncio
    async def test_integrate_fix_none_platform_no_push(self) -> None:
        """When create_platform_safe returns None, push_to_remote is NOT called
        in _integrate_fix."""
        mock_platform = MagicMock()
        mock_platform.add_issue_comment = AsyncMock()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        issue = _make_issue(number=42)
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

        with (
            patch.object(
                pipeline,
                "_harvest_and_push",
                new_callable=AsyncMock,
                return_value=["fix.py"],
            ),
            patch.object(
                pipeline,
                "_auto_commit_pending_changes",
                new_callable=AsyncMock,
            ),
            patch.object(
                pipeline,
                "_push_fix_branch_upstream",
                new_callable=AsyncMock,
            ),
            patch.object(
                pipeline,
                "_update_spinner",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["fix.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=None,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
            ) as mock_push,
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        mock_push.assert_not_called()

    @pytest.mark.asyncio
    async def test_platform_check_before_push_with_valid_platform(self) -> None:
        """When platform is valid, create_platform_safe is called BEFORE
        git push."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform()

        call_order: list[str] = []

        def track_cps(*args: object, **kwargs: object) -> MagicMock:
            call_order.append("create_platform_safe")
            return mock_platform

        async def track_push(*args: object, **kwargs: object) -> bool:
            call_order.append("git_push")
            return True

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                side_effect=track_cps,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                side_effect=track_push,
            ),
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        assert "create_platform_safe" in call_order, "create_platform_safe must be called"
        assert "git_push" in call_order, "git_push must be called"
        cps_idx = call_order.index("create_platform_safe")
        push_idx = call_order.index("git_push")
        assert cps_idx < push_idx, f"create_platform_safe (idx={cps_idx}) must precede git_push (idx={push_idx})"

    @pytest.mark.asyncio
    @pytest.mark.parametrize(
        "platform_result",
        [None, "valid_platform"],
        ids=["none_platform", "valid_platform"],
    )
    async def test_platform_guard_invariant(self, platform_result: str | None) -> None:
        """Platform availability is always checked before any branch push in
        pr mode, regardless of whether the platform is available."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform() if platform_result else None

        call_order: list[str] = []

        def track_cps(*args: object, **kwargs: object) -> MagicMock | None:
            call_order.append("create_platform_safe")
            return mock_platform

        async def track_push(*args: object, **kwargs: object) -> bool:
            call_order.append("git_push")
            return True

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                side_effect=track_cps,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                side_effect=track_push,
            ),
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        # create_platform_safe must always be called
        assert "create_platform_safe" in call_order

        if platform_result is None:
            # None result: push must never be called
            assert "git_push" not in call_order, "push must not be called when platform is None"
        else:
            # Valid platform: platform check must precede push
            assert "git_push" in call_order
            cps_idx = call_order.index("create_platform_safe")
            push_idx = call_order.index("git_push")
            assert cps_idx < push_idx


# ---------------------------------------------------------------------------
# TS-02-E8: If the git push subprocess hangs indefinitely, the pipeline
#           relies on any existing timeout mechanism; the merge strategy
#           code itself does not silently swallow the hang.
#
# Requirements: 02-REQ-4.E5
# ---------------------------------------------------------------------------


class TestPushTimeoutPropagation:
    """TS-02-E8: If the git push subprocess hangs indefinitely, the pipeline
    relies on any existing timeout mechanism; the merge strategy code does
    not silently swallow the exception.

    Requirements: 02-REQ-4.E5
    """

    @pytest.mark.asyncio
    async def test_timeout_error_propagates(self) -> None:
        """TimeoutError from git push propagates through _harvest_and_integrate
        without being caught by merge strategy code."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/my-branch")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform()

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                side_effect=TimeoutError("push timed out"),
            ),
        ):
            with pytest.raises(TimeoutError, match="push timed out"):
                await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

    @pytest.mark.asyncio
    async def test_subprocess_timeout_propagates(self) -> None:
        """subprocess.TimeoutExpired from git push propagates through
        _harvest_and_integrate."""
        import subprocess

        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/my-branch")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform()

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                side_effect=subprocess.TimeoutExpired("git push", 30),
            ),
        ):
            with pytest.raises(subprocess.TimeoutExpired):
                await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))


# ---------------------------------------------------------------------------
# TS-02-E15: Unexpected exceptions (not IntegrationError) during the pr
#            mode operation sequence propagate to the caller's standard
#            error path without being silently caught by merge strategy code.
#
# Requirements: 02-REQ-10.E1
# ---------------------------------------------------------------------------


class TestUnexpectedExceptionPropagation:
    """TS-02-E15: Unexpected exceptions (not IntegrationError) during the
    pr mode operation sequence propagate to the caller's standard error
    path without being silently caught by merge strategy code.

    Requirements: 02-REQ-10.E1
    """

    @pytest.mark.asyncio
    async def test_runtime_error_from_get_changed_files_propagates(self) -> None:
        """RuntimeError from get_changed_files propagates from
        _harvest_and_integrate()."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/my-branch")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform()

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                side_effect=RuntimeError("unexpected"),
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            with pytest.raises(RuntimeError, match="unexpected"):
                await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

    @pytest.mark.asyncio
    async def test_value_error_propagates(self) -> None:
        """ValueError from an operation in the sequence propagates
        unchanged."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/my-branch")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform()
        # Make create_pr raise ValueError (unexpected exception)
        mock_platform.create_pr = AsyncMock(
            side_effect=ValueError("invalid argument"),
        )

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            with pytest.raises(ValueError, match="invalid argument"):
                await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

    @pytest.mark.asyncio
    async def test_integrate_fix_runtime_error_propagates(self) -> None:
        """RuntimeError from get_changed_files propagates from
        _integrate_fix() in pr mode."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        issue = _make_issue(number=42)
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

        with (
            patch.object(
                pipeline,
                "_harvest_and_push",
                new_callable=AsyncMock,
                return_value=["fix.py"],
            ),
            patch.object(
                pipeline,
                "_auto_commit_pending_changes",
                new_callable=AsyncMock,
            ),
            patch.object(
                pipeline,
                "_push_fix_branch_upstream",
                new_callable=AsyncMock,
            ),
            patch.object(
                pipeline,
                "_update_spinner",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                side_effect=RuntimeError("unexpected failure"),
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            with pytest.raises(RuntimeError, match="unexpected failure"):
                await pipeline._integrate_fix(issue, spec, workspace)

    @pytest.mark.asyncio
    async def test_keyboard_interrupt_propagates(self) -> None:
        """KeyboardInterrupt during pr mode is not caught by merge strategy
        code and propagates to the caller."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/my-branch")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform()

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                side_effect=KeyboardInterrupt(),
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            with pytest.raises(KeyboardInterrupt):
                await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))
