"""Tests for branch mode behavior and observability.

Test Spec: TS-02-7 (_harvest_and_integrate branch mode), TS-02-8
           (_integrate_fix branch mode), TS-02-23 (branch mode INFO log),
           TS-02-27 (stdout includes branch name), TS-02-E3 (empty
           changed_files in branch mode)
Requirements: 02-REQ-3.1, 02-REQ-3.2, 02-REQ-3.E1, 02-REQ-9.1, 02-REQ-9.5

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
from agentfox.maintenance.fix_pipeline import FixPipeline
from agentfox.maintenance.spec_builder import InMemorySpec
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
    merge_strategy: str = "branch",
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


def _make_issue(number: int = 42, title: str = "Fix bug") -> IssueResult:
    """Create a minimal IssueResult for testing."""
    return IssueResult(
        number=number,
        title=title,
        html_url=f"https://github.com/test/repo/issues/{number}",
    )


def _make_spec(
    issue_number: int = 42,
    branch_name: str = "fix/issue-42-branch",
) -> InMemorySpec:
    """Create a minimal InMemorySpec for testing."""
    return InMemorySpec(
        issue_number=issue_number,
        title="Fix bug",
        task_prompt="Fix the bug",
        system_context="Bug context",
        branch_name=branch_name,
    )


def _make_fix_pipeline(
    merge_strategy: str = "branch",
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
        # Branch/PR modes await platform methods, so they must be AsyncMock.
        platform.add_issue_comment = AsyncMock()
        platform.close_issue = AsyncMock()
        platform.create_pr = AsyncMock()
    return FixPipeline(
        config=config,
        platform=platform,
    )


# ---------------------------------------------------------------------------
# TS-02-7: _harvest_and_integrate() in branch mode skips harvest(),
#          does not push to origin, calls get_changed_files(), emits the
#          correct INFO log, and returns the expected tuple.
#
# Also covers TS-02-23 (exactly one INFO log with branch name).
#
# Requirements: 02-REQ-3.1, 02-REQ-9.1
# ---------------------------------------------------------------------------


class TestHarvestAndIntegrateBranchMode:
    """TS-02-7 / TS-02-23: _harvest_and_integrate() in branch mode skips
    harvest(), does not push to origin, calls get_changed_files(), emits the
    correct INFO log, and returns the expected 4-tuple.

    Requirements: 02-REQ-3.1, 02-REQ-9.1
    """

    @pytest.mark.asyncio
    async def test_harvest_not_called(self) -> None:
        """harvest() is NOT called in branch mode."""
        runner = _make_runner(merge_strategy="branch")
        workspace = _make_workspace(branch="feat/my-feature")
        outcome = _make_session_outcome("completed")

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["file_a.py"],
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
                return_value=["changed.py"],
            ),
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        assert mock_harvest.call_count == 0, "harvest() should NOT be called in branch mode"

    @pytest.mark.asyncio
    async def test_git_push_not_called(self) -> None:
        """git push to origin is NOT called in branch mode."""
        runner = _make_runner(merge_strategy="branch")
        workspace = _make_workspace(branch="feat/my-feature")
        outcome = _make_session_outcome("completed")

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["file_a.py"],
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
                return_value=["changed.py"],
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
            ) as mock_push,
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        # push_to_remote should not be called in branch mode
        mock_push.assert_not_called()

    @pytest.mark.asyncio
    async def test_get_changed_files_called_once(self) -> None:
        """get_changed_files() is called exactly once in branch mode."""
        runner = _make_runner(merge_strategy="branch")
        workspace = _make_workspace(branch="feat/my-feature")
        outcome = _make_session_outcome("completed")

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["file_a.py"],
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
                return_value=["changed.py"],
            ) as mock_gcf,
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        assert mock_gcf.call_count == 1, "get_changed_files() should be called exactly once in branch mode"

    @pytest.mark.asyncio
    async def test_returns_completed_tuple(self) -> None:
        """Return value is ('completed', None, touched_files, False) in branch mode."""
        runner = _make_runner(merge_strategy="branch")
        workspace = _make_workspace(branch="feat/my-feature")
        outcome = _make_session_outcome("completed")

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["file_a.py"],
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
                return_value=["changed.py"],
            ),
        ):
            result = await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        assert result == ("completed", None, ["changed.py"], False)

    @pytest.mark.asyncio
    async def test_info_log_emitted_with_branch_name(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """INFO log line emitted: "Merge strategy is 'branch' -- feature branch
        'feat/my-feature' kept locally." (TS-02-23).
        """
        runner = _make_runner(merge_strategy="branch")
        workspace = _make_workspace(branch="feat/my-feature")
        outcome = _make_session_outcome("completed")

        with (
            caplog.at_level(logging.DEBUG),
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["file_a.py"],
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
                return_value=["changed.py"],
            ),
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        info_lines = [r for r in caplog.records if r.levelno == logging.INFO and "Merge strategy" in r.message]
        assert len(info_lines) == 1, (
            f"Expected exactly one INFO log about merge strategy, "
            f"got {len(info_lines)}: {[r.message for r in info_lines]}"
        )
        expected_msg = "Merge strategy is 'branch' — feature branch 'feat/my-feature' kept locally."
        assert info_lines[0].message == expected_msg

    @pytest.mark.asyncio
    async def test_exactly_one_merge_strategy_log(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """Exactly one merge strategy log line is emitted in branch mode (TS-02-23)."""
        runner = _make_runner(merge_strategy="branch")
        workspace = _make_workspace(branch="feat/my-feature")
        outcome = _make_session_outcome("completed")

        with (
            caplog.at_level(logging.DEBUG),
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["file_a.py"],
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
                return_value=["changed.py"],
            ),
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        merge_strategy_logs = [
            r for r in caplog.records if "Merge strategy" in r.message or "merge strategy" in r.message.lower()
        ]
        assert len(merge_strategy_logs) == 1, (
            f"Expected exactly one merge strategy log line, "
            f"got {len(merge_strategy_logs)}: "
            f"{[r.message for r in merge_strategy_logs]}"
        )


# ---------------------------------------------------------------------------
# TS-02-8: _integrate_fix() in branch mode skips harvest(), does not push
#          the feature branch, does not close the issue, and posts the exact
#          branch-mode comment.
#
# Requirements: 02-REQ-3.2
#
# Reviewer findings applied:
# - add_issue_comment(issue_number, body) is the real method, not post_comment
# - _integrate_fix returns tuple[str, list[str]], not a 4-tuple
# - _integrate_fix is a method on FixPipeline
# ---------------------------------------------------------------------------


class TestIntegrateFixBranchMode:
    """TS-02-8: _integrate_fix() in branch mode skips harvest(), does not
    push the feature branch, does NOT close the originating issue, and posts
    the exact branch-mode comment via add_issue_comment().

    Requirements: 02-REQ-3.2

    Note: The actual return type is tuple[str, list[str]] (2-tuple), NOT the
    spec's 4-tuple. Tests adapt to the real contract.
    """

    @pytest.mark.asyncio
    async def test_harvest_and_push_not_called(self) -> None:
        """_harvest_and_push() is NOT called in branch mode."""
        pipeline = _make_fix_pipeline(merge_strategy="branch")
        issue = _make_issue(number=42)
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42-branch")
        workspace = _make_workspace(branch="fix/issue-42-branch")

        with (
            patch.object(
                pipeline,
                "_harvest_and_push",
                new_callable=AsyncMock,
                return_value=["fix.py"],
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
                return_value=["fix.py"],
            ),
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        assert mock_harvest_push.call_count == 0, "_harvest_and_push() should NOT be called in branch mode"

    @pytest.mark.asyncio
    async def test_git_push_not_called(self) -> None:
        """git push to origin is NOT called in branch mode."""
        pipeline = _make_fix_pipeline(merge_strategy="branch")
        issue = _make_issue(number=42)
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42-branch")
        workspace = _make_workspace(branch="fix/issue-42-branch")

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
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        mock_push.assert_not_called()

    @pytest.mark.asyncio
    async def test_issue_close_not_called(self) -> None:
        """close_issue is NOT called in branch mode."""
        mock_platform = MagicMock()
        mock_platform.close_issue = AsyncMock()
        mock_platform.add_issue_comment = AsyncMock()
        pipeline = _make_fix_pipeline(merge_strategy="branch", platform=mock_platform)
        issue = _make_issue(number=42)
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42-branch")
        workspace = _make_workspace(branch="fix/issue-42-branch")

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
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        mock_platform.close_issue.assert_not_called()

    @pytest.mark.asyncio
    async def test_posts_exact_branch_mode_comment(self) -> None:
        """add_issue_comment is called with the exact branch-mode comment text.

        Expected text:
        'Fix branch created: `fix/issue-42-branch`. Merge strategy is set to
        `branch` -- please review and merge manually.'
        """
        mock_platform = MagicMock()
        mock_platform.add_issue_comment = AsyncMock()
        mock_platform.close_issue = AsyncMock()
        pipeline = _make_fix_pipeline(merge_strategy="branch", platform=mock_platform)
        issue = _make_issue(number=42)
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42-branch")
        workspace = _make_workspace(branch="fix/issue-42-branch")

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
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        expected_comment = (
            "Fix branch created: `fix/issue-42-branch`. "
            "Merge strategy is set to `branch` "
            "— please review and merge manually."
        )
        # The comment should be posted via add_issue_comment or _post_comment
        # (which internally calls add_issue_comment)
        comment_calls = mock_platform.add_issue_comment.call_args_list
        found = False
        for c in comment_calls:
            # add_issue_comment(issue_number, body) -- positional or keyword
            args, kwargs = c
            body_text = args[1] if len(args) > 1 else kwargs.get("body", "")
            if expected_comment in body_text:
                found = True
                break
        assert found, f"Expected branch-mode comment not found in add_issue_comment calls. Calls made: {comment_calls}"

    @pytest.mark.asyncio
    async def test_returns_valid_tuple(self) -> None:
        """Return value is a 2-tuple with valid status and file list.

        Note: The spec says ('completed', None, touched_files, False) but
        the actual return type is tuple[str, list[str]]. We adapt.
        """
        mock_platform = MagicMock()
        mock_platform.add_issue_comment = AsyncMock()
        pipeline = _make_fix_pipeline(merge_strategy="branch", platform=mock_platform)
        issue = _make_issue(number=42)
        spec = _make_spec(issue_number=42, branch_name="fix/issue-42-branch")
        workspace = _make_workspace(branch="fix/issue-42-branch")

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
        ):
            result = await pipeline._integrate_fix(issue, spec, workspace)

        assert len(result) == 2, f"Expected 2-tuple, got {len(result)}-tuple"
        status, changed_files = result
        assert isinstance(status, str)
        assert isinstance(changed_files, list)
        # In branch mode, the status should not be 'error'
        assert status != "error", "Branch mode should not return 'error' status"

    @pytest.mark.asyncio
    async def test_comment_uses_correct_issue_number(self) -> None:
        """The branch-mode comment is posted to the correct issue number."""
        mock_platform = MagicMock()
        mock_platform.add_issue_comment = AsyncMock()
        pipeline = _make_fix_pipeline(merge_strategy="branch", platform=mock_platform)
        issue = _make_issue(number=99)
        spec = _make_spec(issue_number=99, branch_name="fix/issue-99-branch")
        workspace = _make_workspace(branch="fix/issue-99-branch")

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
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        # Check that add_issue_comment was called with the correct issue number
        comment_calls = mock_platform.add_issue_comment.call_args_list
        branch_mode_calls = [
            c
            for c in comment_calls
            if "Merge strategy" in str(c) or "merge manually" in str(c) or "branch" in str(c).lower()
        ]
        for c in branch_mode_calls:
            args, kwargs = c
            issue_num = args[0] if args else kwargs.get("issue_number")
            assert issue_num == 99, f"Expected issue number 99, got {issue_num}"


# ---------------------------------------------------------------------------
# TS-02-27: In branch mode, the af code CLI session summary printed to stdout
#           includes the feature branch name.
#
# Requirements: 02-REQ-9.5
#
# Note: The session summary is propagated through _harvest_and_integrate's
# return value and log output. This test verifies that the branch name is
# present in the INFO log emitted by _harvest_and_integrate, which the CLI
# can surface in its stdout output. A true end-to-end stdout test would
# require running the full CLI, which is beyond unit test scope.
# ---------------------------------------------------------------------------


class TestBranchModeSessionSummary:
    """TS-02-27: In branch mode, the session summary output includes the
    feature branch name.

    Requirements: 02-REQ-9.5

    Verifies that the branch name appears in the INFO log output which is
    surfaced in the standard af code session summary. The INFO log is the
    mechanism by which the branch name reaches stdout.
    """

    @pytest.mark.asyncio
    async def test_branch_name_in_log_output(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """Branch name 'feat/my-feature' appears in INFO log output."""
        runner = _make_runner(merge_strategy="branch")
        workspace = _make_workspace(branch="feat/my-feature")
        outcome = _make_session_outcome("completed")

        with (
            caplog.at_level(logging.DEBUG),
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["file_a.py"],
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
                return_value=["changed.py"],
            ),
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        # The branch name should appear somewhere in log output
        all_messages = " ".join(r.message for r in caplog.records)
        assert "feat/my-feature" in all_messages, (
            "Branch name 'feat/my-feature' should appear in log output "
            "for inclusion in the session summary printed to stdout"
        )

    @pytest.mark.asyncio
    async def test_returns_successfully_with_exit_code_zero(self) -> None:
        """_harvest_and_integrate returns a completed tuple (exit code 0 semantics)."""
        runner = _make_runner(merge_strategy="branch")
        workspace = _make_workspace(branch="feat/my-feature")
        outcome = _make_session_outcome("completed")

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["file_a.py"],
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
                return_value=["changed.py"],
            ),
        ):
            result = await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        status, error_message, _touched_files, _non_retryable = result
        assert status == "completed", "Branch mode should return 'completed' status (exit code 0)"
        assert error_message is None, "Branch mode should return None error message"

    @pytest.mark.asyncio
    async def test_branch_name_in_info_log_for_different_branch(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """Branch name substitution works for different branch names."""
        runner = _make_runner(merge_strategy="branch")
        workspace = _make_workspace(branch="feature/02_merge_strategy/6")
        outcome = _make_session_outcome("completed")

        with (
            caplog.at_level(logging.DEBUG),
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["file_a.py"],
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
                return_value=["changed.py"],
            ),
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        info_lines = [r for r in caplog.records if r.levelno == logging.INFO and "Merge strategy" in r.message]
        assert len(info_lines) == 1
        assert "feature/02_merge_strategy/6" in info_lines[0].message


# ---------------------------------------------------------------------------
# TS-02-E3: In branch mode, _harvest_and_integrate() proceeds normally when
#           get_changed_files() returns an empty list.
#
# Requirements: 02-REQ-3.E1
# ---------------------------------------------------------------------------


class TestBranchModeEmptyChangedFiles:
    """TS-02-E3: In branch mode, _harvest_and_integrate() proceeds normally
    when get_changed_files() returns an empty list, returning
    ('completed', None, [], False).

    Requirements: 02-REQ-3.E1
    """

    @pytest.mark.asyncio
    async def test_empty_changed_files_returns_completed(self) -> None:
        """Returns ('completed', None, [], False) with empty changed files."""
        runner = _make_runner(merge_strategy="branch")
        workspace = _make_workspace(branch="feat/x")
        outcome = _make_session_outcome("completed")

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
        ):
            result = await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        assert result == ("completed", None, [], False)

    @pytest.mark.asyncio
    async def test_empty_changed_files_no_error_log(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """No error log is emitted when changed files list is empty."""
        runner = _make_runner(merge_strategy="branch")
        workspace = _make_workspace(branch="feat/x")
        outcome = _make_session_outcome("completed")

        with (
            caplog.at_level(logging.DEBUG),
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
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        error_logs = [r for r in caplog.records if r.levelno >= logging.ERROR]
        assert len(error_logs) == 0, (
            f"No error logs expected in branch mode with empty changed files, got: {[r.message for r in error_logs]}"
        )

    @pytest.mark.asyncio
    async def test_empty_changed_files_no_exception(self) -> None:
        """No exception is raised when changed files list is empty."""
        runner = _make_runner(merge_strategy="branch")
        workspace = _make_workspace(branch="feat/x")
        outcome = _make_session_outcome("completed")

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
        ):
            # Should not raise any exception
            result = await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        # Basic sanity check on result shape
        assert len(result) == 4
        status, err_msg, touched, non_retry = result
        assert status == "completed"
        assert err_msg is None
        assert touched == []
        assert non_retry is False

    @pytest.mark.asyncio
    async def test_empty_changed_files_still_emits_info_log(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """INFO log about branch mode is still emitted even with empty changed files."""
        runner = _make_runner(merge_strategy="branch")
        workspace = _make_workspace(branch="feat/x")
        outcome = _make_session_outcome("completed")

        with (
            caplog.at_level(logging.DEBUG),
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
        ):
            await runner._harvest_and_integrate("test_spec:1", outcome, workspace, Path("/tmp/repo"))

        info_lines = [r for r in caplog.records if r.levelno == logging.INFO and "Merge strategy" in r.message]
        assert len(info_lines) == 1, "INFO log about branch mode should be emitted even with empty changed files"
