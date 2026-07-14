"""Tests for PR mode happy path and observability.

Test Spec: TS-02-9 (_harvest_and_integrate pr mode success),
           TS-02-10 (_integrate_fix pr mode success),
           TS-02-11 (platform None fallback), TS-02-12 (lazy platform
           validation), TS-02-24 (pr mode INFO log), TS-02-25 (pr mode
           WARNING on None platform), TS-02-28 (PR URL in session summary)
Requirements: 02-REQ-4.1, 02-REQ-4.2, 02-REQ-4.3, 02-REQ-4.4,
              02-REQ-9.2, 02-REQ-9.3, 02-REQ-9.5, 02-REQ-9.6

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
from afissues.protocol import IssueResult
from agentfox.core.config import AgentFoxConfig, WorkspaceConfig
from agentfox.engine.session_lifecycle import NodeSessionRunner
from agentfox.knowledge.db import KnowledgeDB
from agentfox.nightshift.fix_pipeline import FixPipeline
from agentfox.nightshift.spec_builder import InMemorySpec
from agentfox.workspace import WorkspaceInfo

# ---------------------------------------------------------------------------
# Helpers: create minimal test objects
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


def _make_issue(number: int = 42, title: str = "Login fails on empty password") -> IssueResult:
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
    platform = MagicMock()
    platform._owner = owner
    platform._repo = repo
    platform.create_pr = AsyncMock(return_value=create_pr_url)
    platform.add_issue_comment = AsyncMock()
    platform.close_issue = AsyncMock()
    platform.assign_label = AsyncMock()
    return platform


# ---------------------------------------------------------------------------
# TS-02-9: _harvest_and_integrate() in pr mode with a valid platform pushes
#          the branch, calls build_pr_body(), creates the PR with the correct
#          title, emits an INFO log, and returns the expected tuple.
#
# Also covers TS-02-24 (INFO log with PR URL).
#
# Requirements: 02-REQ-4.1, 02-REQ-9.2
# ---------------------------------------------------------------------------


class TestHarvestAndIntegratePrModeSuccess:
    """TS-02-9 / TS-02-24: _harvest_and_integrate() in pr mode with a valid
    platform pushes the branch, creates the PR with the correct title, emits
    an INFO log with the PR URL, and returns the expected 4-tuple.

    Requirements: 02-REQ-4.1, 02-REQ-9.2
    """

    @pytest.mark.asyncio
    async def test_git_push_called_once(self) -> None:
        """git push to origin is called once in pr mode."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ) as mock_push,
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        assert mock_push.call_count == 1, (
            "git push to origin should be called exactly once in pr mode"
        )

    @pytest.mark.asyncio
    async def test_create_pr_called_with_correct_title(self) -> None:
        """platform.create_pr called with title='{spec_name}: {task_group_title}'.

        For af code sessions, the PR title format is:
        '{spec_name}: {task_group_title}'
        """
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        # Verify create_pr was called with the correct title format
        mock_platform.create_pr.assert_called_once()
        call_kwargs = mock_platform.create_pr.call_args
        # Title should follow spec_name: task_group_title pattern
        title = call_kwargs.kwargs.get("title") or (
            call_kwargs.args[0] if call_kwargs.args else None
        )
        assert title is not None, "create_pr must be called with a title"
        # The spec_name for our runner is 'test_spec' from the workspace
        assert "test_spec" in title, (
            f"PR title should contain the spec name; got: {title}"
        )

    @pytest.mark.asyncio
    async def test_create_pr_called_with_correct_head_branch(self) -> None:
        """platform.create_pr called with head=feature_branch."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        mock_platform.create_pr.assert_called_once()
        call_kwargs = mock_platform.create_pr.call_args
        head = call_kwargs.kwargs.get("head")
        assert head == "feat/ms", f"Expected head='feat/ms', got head={head!r}"

    @pytest.mark.asyncio
    async def test_info_log_with_pr_url(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """INFO log 'Pull request created: {url}' is emitted on success (TS-02-24)."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform(
            create_pr_url="https://github.com/owner/repo/pull/1",
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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        info_pr_lines = [
            r
            for r in caplog.records
            if r.levelno == logging.INFO and "Pull request created" in r.message
        ]
        assert len(info_pr_lines) == 1, (
            f"Expected exactly one INFO log about PR creation, "
            f"got {len(info_pr_lines)}: {[r.message for r in info_pr_lines]}"
        )
        assert "https://github.com/owner/repo/pull/1" in info_pr_lines[0].message

    @pytest.mark.asyncio
    async def test_exactly_one_pr_created_info_log(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """Exactly one INFO log about PR creation is emitted (TS-02-24)."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform()

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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        pr_created_logs = [
            r
            for r in caplog.records
            if r.levelno == logging.INFO
            and ("Pull request created" in r.message or "pull request" in r.message.lower())
        ]
        assert len(pr_created_logs) == 1, (
            f"Expected exactly 1 PR-creation INFO log, got {len(pr_created_logs)}: "
            f"{[r.message for r in pr_created_logs]}"
        )

    @pytest.mark.asyncio
    async def test_returns_completed_tuple(self) -> None:
        """Return value is ('completed', None, touched_files, False) in pr mode."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            result = await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        assert result == ("completed", None, ["config.py"], False)

    @pytest.mark.asyncio
    async def test_harvest_not_called_in_pr_mode(self) -> None:
        """harvest() (squash-merge) is NOT called in pr mode -- only push + PR."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform()

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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        assert mock_harvest.call_count == 0, (
            "harvest() (squash-merge) should NOT be called in pr mode"
        )


# ---------------------------------------------------------------------------
# TS-02-10: _integrate_fix() in pr mode with a valid platform creates a PR
#           with the correct nightshift title, does not close the issue, and
#           the PR body contains 'Fixes #N'.
#
# Requirements: 02-REQ-4.2
# ---------------------------------------------------------------------------


class TestIntegrateFixPrModeSuccess:
    """TS-02-10: _integrate_fix() in pr mode with a valid platform creates a
    PR with the correct nightshift title, does NOT close the issue, and the
    PR body contains 'Fixes #N' for GitHub auto-close.

    Requirements: 02-REQ-4.2

    Note: The actual return type is tuple[str, list[str]] (2-tuple), NOT the
    spec's 4-tuple. Tests adapt to the real contract.
    """

    @pytest.mark.asyncio
    async def test_create_pr_called_with_nightshift_title(self) -> None:
        """platform.create_pr called with title='Fix #42: Login fails on empty password'."""
        mock_platform = _make_mock_platform(
            create_pr_url="https://github.com/owner/repo/pull/5",
        )
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        issue = _make_issue(number=42, title="Login fails on empty password")
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

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
                return_value=["auth/login.py"],
            ),
            patch(
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        mock_platform.create_pr.assert_called_once()
        call_kwargs = mock_platform.create_pr.call_args
        title = call_kwargs.kwargs.get("title") or (
            call_kwargs.args[0] if call_kwargs.args else None
        )
        expected_title = "Fix #42: Login fails on empty password"
        assert title == expected_title, (
            f"Expected PR title '{expected_title}', got '{title}'"
        )

    @pytest.mark.asyncio
    async def test_pr_body_contains_fixes_n(self) -> None:
        """PR body contains 'Fixes #42' for GitHub auto-close."""
        mock_platform = _make_mock_platform(
            create_pr_url="https://github.com/owner/repo/pull/5",
        )
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        issue = _make_issue(number=42, title="Login fails on empty password")
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

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
                return_value=["auth/login.py"],
            ),
            patch(
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        mock_platform.create_pr.assert_called_once()
        call_kwargs = mock_platform.create_pr.call_args
        body = call_kwargs.kwargs.get("body") or (
            call_kwargs.args[1] if len(call_kwargs.args) > 1 else ""
        )
        assert "Fixes #42" in body, (
            f"PR body should contain 'Fixes #42' for GitHub auto-close; got body: {body}"
        )

    @pytest.mark.asyncio
    async def test_issue_close_not_called(self) -> None:
        """close_issue is NOT called in pr mode -- PR body has 'Fixes #N' for auto-close."""
        mock_platform = _make_mock_platform(
            create_pr_url="https://github.com/owner/repo/pull/5",
        )
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        issue = _make_issue(number=42, title="Login fails on empty password")
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

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
                return_value=["auth/login.py"],
            ),
            patch(
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        mock_platform.close_issue.assert_not_called()

    @pytest.mark.asyncio
    async def test_returns_valid_tuple(self) -> None:
        """Return value is a 2-tuple with status and changed files list.

        Note: The spec claims a 4-tuple but _integrate_fix returns (str, list[str]).
        """
        mock_platform = _make_mock_platform(
            create_pr_url="https://github.com/owner/repo/pull/5",
        )
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        issue = _make_issue(number=42, title="Login fails on empty password")
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

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
                return_value=["auth/login.py"],
            ),
            patch(
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            result = await pipeline._integrate_fix(issue, spec, workspace)

        assert len(result) == 2, f"Expected 2-tuple, got {len(result)}-tuple"
        status, changed_files = result
        assert isinstance(status, str)
        assert isinstance(changed_files, list)

    @pytest.mark.asyncio
    async def test_harvest_and_push_not_called_in_pr_mode(self) -> None:
        """_harvest_and_push() is NOT called in pr mode -- push + PR replaces it."""
        mock_platform = _make_mock_platform(
            create_pr_url="https://github.com/owner/repo/pull/5",
        )
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        issue = _make_issue(number=42, title="Login fails on empty password")
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

        with (
            patch.object(
                pipeline,
                "_harvest_and_push",
                new_callable=AsyncMock,
                return_value=["auth/login.py"],
            ) as mock_harvest_push,
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
                return_value=["auth/login.py"],
            ),
            patch(
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        assert mock_harvest_push.call_count == 0, (
            "_harvest_and_push() should NOT be called in pr mode"
        )


# ---------------------------------------------------------------------------
# TS-02-11 / TS-02-25: When create_platform_safe() returns None in pr mode,
#                       a WARNING is logged and the system falls back to
#                       branch mode without any branch push.
#
# Requirements: 02-REQ-4.3, 02-REQ-9.3
# ---------------------------------------------------------------------------


class TestPlatformNoneFallback:
    """TS-02-11 / TS-02-25: When create_platform_safe() returns None in pr
    mode, a WARNING is logged and the system falls back to branch mode
    without any branch push.

    Requirements: 02-REQ-4.3, 02-REQ-9.3
    """

    @pytest.mark.asyncio
    async def test_harvest_and_integrate_warning_log(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """WARNING log emitted when create_platform_safe returns None."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
        outcome = _make_session_outcome("completed")

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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=None,
            ),
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        warn_lines = [
            r
            for r in caplog.records
            if r.levelno == logging.WARNING and "falling back" in r.message.lower()
        ]
        assert len(warn_lines) == 1, (
            f"Expected exactly one WARNING about platform fallback, "
            f"got {len(warn_lines)}: {[r.message for r in warn_lines]}"
        )
        assert (
            "platform is not configured" in warn_lines[0].message.lower()
            or "not configured" in warn_lines[0].message.lower()
        )

    @pytest.mark.asyncio
    async def test_harvest_and_integrate_warning_exact_message(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """WARNING message matches the spec exactly."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
        outcome = _make_session_outcome("completed")

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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=None,
            ),
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        expected_msg = (
            "Merge strategy is 'pr' but platform is not configured "
            "— falling back to 'branch' mode."
        )
        warn_lines = [
            r
            for r in caplog.records
            if r.levelno == logging.WARNING
        ]
        warn_messages = [r.message for r in warn_lines]
        assert expected_msg in warn_messages, (
            f"Expected WARNING message '{expected_msg}' not found. "
            f"Warnings emitted: {warn_messages}"
        )

    @pytest.mark.asyncio
    async def test_harvest_and_integrate_no_push_on_none_platform(self) -> None:
        """git push NOT called when create_platform_safe returns None."""
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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=None,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
            ) as mock_push,
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        mock_push.assert_not_called()

    @pytest.mark.asyncio
    async def test_harvest_and_integrate_returns_completed_on_fallback(self) -> None:
        """Return value is ('completed', None, touched_files, False) on fallback."""
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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=None,
            ),
        ):
            result = await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        status, err_msg, _touched, non_retry = result
        assert status == "completed"
        assert err_msg is None
        assert non_retry is False

    @pytest.mark.asyncio
    async def test_integrate_fix_warning_log_on_none_platform(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """_integrate_fix emits WARNING when platform is None in pr mode."""
        mock_platform = MagicMock()
        mock_platform.add_issue_comment = AsyncMock()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        issue = _make_issue(number=42)
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42")
        workspace = _make_workspace(branch="fix/issue-42")

        with (
            caplog.at_level(logging.DEBUG),
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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=None,
            ),
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        warn_lines = [
            r
            for r in caplog.records
            if r.levelno == logging.WARNING and "falling back" in r.message.lower()
        ]
        assert len(warn_lines) == 1, (
            f"Expected exactly one WARNING about platform fallback in _integrate_fix, "
            f"got {len(warn_lines)}: {[r.message for r in warn_lines]}"
        )

    @pytest.mark.asyncio
    async def test_integrate_fix_no_push_on_none_platform(self) -> None:
        """git push NOT called in _integrate_fix when platform is None."""
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
            ) as mock_push,
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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=None,
            ),
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        mock_push.assert_not_called()


# ---------------------------------------------------------------------------
# TS-02-12: Platform availability is validated lazily via
#           create_platform_safe() only at the point of PR creation, not at
#           startup or session start.
#
# Requirements: 02-REQ-4.4
# ---------------------------------------------------------------------------


class TestLazyPlatformValidation:
    """TS-02-12: Platform availability is validated lazily via
    create_platform_safe() only at the point of PR creation, not at startup
    or session start.

    Requirements: 02-REQ-4.4

    Verifies that create_platform_safe is not called during session
    initialization; only at merge strategy execution time inside
    _harvest_and_integrate() or _integrate_fix().
    """

    def test_create_platform_safe_not_called_at_runner_init(self) -> None:
        """create_platform_safe is NOT called when NodeSessionRunner is created."""
        with patch(
            "agentfox.nightshift.platform_factory.create_platform_safe",
            return_value=MagicMock(),
        ) as mock_cps:
            # Creating the runner should not call create_platform_safe
            _make_runner(merge_strategy="pr")

        mock_cps.assert_not_called()

    def test_create_platform_safe_not_called_at_pipeline_init(self) -> None:
        """create_platform_safe is NOT called when FixPipeline is created."""
        with patch(
            "agentfox.nightshift.platform_factory.create_platform_safe",
            return_value=MagicMock(),
        ) as mock_cps:
            # Creating the pipeline should not call create_platform_safe
            _make_fix_pipeline(merge_strategy="pr")

        mock_cps.assert_not_called()

    @pytest.mark.asyncio
    async def test_create_platform_safe_called_during_harvest_and_integrate(self) -> None:
        """create_platform_safe IS called during _harvest_and_integrate() in pr mode."""
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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=None,
            ) as mock_cps,
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        assert mock_cps.call_count == 1, (
            "create_platform_safe should be called exactly once during "
            "_harvest_and_integrate in pr mode"
        )

    @pytest.mark.asyncio
    async def test_create_platform_safe_called_during_integrate_fix(self) -> None:
        """create_platform_safe IS called during _integrate_fix() in pr mode."""
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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=None,
            ) as mock_cps,
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        assert mock_cps.call_count == 1, (
            "create_platform_safe should be called exactly once during "
            "_integrate_fix in pr mode"
        )

    @pytest.mark.asyncio
    async def test_create_platform_safe_not_called_in_direct_mode(self) -> None:
        """create_platform_safe is NOT called in direct mode (lazy check)."""
        runner = _make_runner(merge_strategy="direct")
        workspace = _make_workspace()
        outcome = _make_session_outcome("completed")

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["file.py"],
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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=MagicMock(),
            ) as mock_cps,
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        mock_cps.assert_not_called()


# ---------------------------------------------------------------------------
# TS-02-28: In pr mode after successful PR creation, the af code CLI session
#           summary printed to stdout includes the PR URL.
#
# Requirements: 02-REQ-9.6
#
# Note: The session summary is surfaced through log output and the return
# value structure. A true end-to-end stdout test would require running
# the full CLI, which is beyond unit test scope. This test verifies that
# the PR URL appears in the INFO log output emitted by
# _harvest_and_integrate, which is the mechanism for surfacing the URL in
# the af code session summary output.
# ---------------------------------------------------------------------------


class TestPrModeSessionSummary:
    """TS-02-28: In pr mode after successful PR creation, the session summary
    output includes the PR URL.

    Requirements: 02-REQ-9.6

    Verifies that the PR URL appears in the INFO log output which is
    surfaced in the standard af code session summary. The INFO log is the
    mechanism by which the PR URL reaches stdout.
    """

    @pytest.mark.asyncio
    async def test_pr_url_in_log_output(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """PR URL appears in INFO log output for session summary."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform(
            create_pr_url="https://github.com/owner/repo/pull/1",
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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        all_messages = " ".join(r.message for r in caplog.records)
        assert "https://github.com/owner/repo/pull/1" in all_messages, (
            "PR URL should appear in log output for inclusion in session summary"
        )

    @pytest.mark.asyncio
    async def test_returns_successfully_with_exit_code_zero(self) -> None:
        """_harvest_and_integrate returns a completed tuple (exit code 0 semantics)."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform(
            create_pr_url="https://github.com/owner/repo/pull/1",
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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            result = await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        status, error_message, _touched_files, _non_retryable = result
        assert status == "completed", (
            "PR mode should return 'completed' status (exit code 0)"
        )
        assert error_message is None, (
            "PR mode should return None error message on success"
        )

    @pytest.mark.asyncio
    async def test_pr_url_in_specific_info_log(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """PR URL appears in a specific INFO log line about PR creation."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform(
            create_pr_url="https://github.com/owner/repo/pull/42",
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
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        info_lines = [
            r
            for r in caplog.records
            if r.levelno == logging.INFO
            and "Pull request created" in r.message
        ]
        assert len(info_lines) == 1
        assert "https://github.com/owner/repo/pull/42" in info_lines[0].message
