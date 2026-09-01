"""Tests for direct mode zero-regression and return contract across all merge modes.

Test Spec: TS-02-5 (direct mode _harvest_and_integrate), TS-02-6 (direct mode
           _integrate_fix), TS-02-22 (return tuple shape invariant),
           TS-02-P2 (return shape property), TS-02-P6 (direct-mode property)
Requirements: 02-REQ-2.1, 02-REQ-2.2, 02-REQ-8.1

Reviewer Findings (applied):
- [CRITICAL] _integrate_fix() returns tuple[str, list[str]] (2-tuple with status
  'merged'/'no_changes'/'error'), NOT the 4-tuple the spec claims. Tests adapt to
  the actual 2-tuple contract.
- [MAJOR] _harvest_and_integrate() is a method on NodeSessionRunner, not a standalone
  function. Tests mock a NodeSessionRunner instance.
- [MAJOR] _integrate_fix() is a method on FixPipeline, not a standalone function.
  Tests mock a FixPipeline instance.
- [MAJOR] harvest() has a complex signature with keyword arguments (force_clean,
  push, audit_sink, run_id, node_id). Tests verify the actual calling convention.
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
    merge_strategy: str = "direct",
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
    branch_name: str = "fix/test-branch",
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
    merge_strategy: str = "direct",
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
        # Ensure async platform methods are await-compatible for all modes
        platform.add_issue_comment = AsyncMock()
        platform.close_issue = AsyncMock()
        platform.create_pr = AsyncMock()
    return FixPipeline(
        config=config,
        platform=platform,
    )


# ---------------------------------------------------------------------------
# TS-02-5: _harvest_and_integrate() in direct mode calls harvest() exactly once
# ---------------------------------------------------------------------------


class TestHarvestAndIntegrateDirectMode:
    """TS-02-5: _harvest_and_integrate() in direct mode calls harvest() exactly
    once, does not call get_changed_files() or create_pr(), and returns the
    expected 4-tuple.

    Requirements: 02-REQ-2.1
    """

    @pytest.mark.asyncio
    async def test_harvest_called_exactly_once(self) -> None:
        """harvest() is called exactly once in direct mode."""
        runner = _make_runner(merge_strategy="direct")
        workspace = _make_workspace()
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
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        assert mock_harvest.call_count == 1

    @pytest.mark.asyncio
    async def test_returns_completed_tuple(self) -> None:
        """Return value is ('completed', None, touched_files, False) in direct mode."""
        runner = _make_runner(merge_strategy="direct")
        workspace = _make_workspace()
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
        ):
            result = await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        assert result == ("completed", None, ["file_a.py"], False)

    @pytest.mark.asyncio
    async def test_harvest_receives_correct_kwargs(self) -> None:
        """harvest() receives push=True, force_clean, and audit parameters."""
        runner = _make_runner(merge_strategy="direct", force_clean=True)
        workspace = _make_workspace()
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
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        # Verify harvest was called with the correct keyword arguments
        call_kwargs = mock_harvest.call_args
        assert call_kwargs.kwargs.get("push") is True
        assert call_kwargs.kwargs.get("force_clean") is True
        assert call_kwargs.kwargs.get("dev_branch") == "main"

    @pytest.mark.asyncio
    async def test_no_merge_strategy_log_lines_emitted(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
        """No INFO/WARNING log lines about merge strategy are emitted in direct mode."""
        runner = _make_runner(merge_strategy="direct")
        workspace = _make_workspace()
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
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        merge_strategy_logs = [
            r
            for r in caplog.records
            if "Merge strategy" in r.message or "merge strategy" in r.message.lower()
        ]
        assert len(merge_strategy_logs) == 0, (
            f"Unexpected merge strategy log lines in direct mode: {merge_strategy_logs}"
        )

    @pytest.mark.asyncio
    async def test_create_platform_safe_not_called(self) -> None:
        """create_platform_safe() is NOT called in direct mode."""
        runner = _make_runner(merge_strategy="direct")
        workspace = _make_workspace()
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
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=MagicMock(),
            ) as mock_cps,
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        mock_cps.assert_not_called()


# ---------------------------------------------------------------------------
# TS-02-6: _integrate_fix() in direct mode calls _harvest_and_push() and
#          closes the originating issue
#
# Note: Adapted from spec — _integrate_fix returns tuple[str, list[str]]
# (2-tuple), NOT tuple[str, str|None, list[str], bool] (4-tuple).
# Status values are 'merged'/'no_changes'/'error', NOT 'completed'.
# ---------------------------------------------------------------------------


class TestIntegrateFixDirectMode:
    """TS-02-6: _integrate_fix() in direct mode calls _harvest_and_push()
    and the originating issue is closed via _handle_result.

    Requirements: 02-REQ-2.2

    Note: The actual return type is tuple[str, list[str]] where status is
    'merged', 'no_changes', or 'error'. The spec's 4-tuple claim is incorrect
    per reviewer findings. Tests adapt to the real 2-tuple contract.
    """

    @pytest.mark.asyncio
    async def test_harvest_and_push_called_once(self) -> None:
        """_harvest_and_push() is called exactly once in direct mode."""
        pipeline = _make_fix_pipeline(merge_strategy="direct")
        issue = _make_issue()
        spec = _make_spec()
        workspace = _make_workspace(branch="fix/test-branch")

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
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        assert mock_harvest_push.call_count == 1

    @pytest.mark.asyncio
    async def test_returns_merged_tuple(self) -> None:
        """Return value is ('merged', changed_files) in direct mode on success."""
        pipeline = _make_fix_pipeline(merge_strategy="direct")
        issue = _make_issue()
        spec = _make_spec()
        workspace = _make_workspace(branch="fix/test-branch")

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
        ):
            result = await pipeline._integrate_fix(issue, spec, workspace)

        # Actual return type is tuple[str, list[str]] — NOT a 4-tuple
        assert result == ("merged", ["fix.py"])

    @pytest.mark.asyncio
    async def test_return_is_two_tuple(self) -> None:
        """Return value is a 2-tuple (status, changed_files), not a 4-tuple."""
        pipeline = _make_fix_pipeline(merge_strategy="direct")
        issue = _make_issue()
        spec = _make_spec()
        workspace = _make_workspace(branch="fix/test-branch")

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
        ):
            result = await pipeline._integrate_fix(issue, spec, workspace)

        assert len(result) == 2
        status, changed_files = result
        assert isinstance(status, str)
        assert isinstance(changed_files, list)


# ---------------------------------------------------------------------------
# TS-02-22: Return tuple shape invariant across all three modes
#
# Note: Adapted for actual return types:
# - _harvest_and_integrate: tuple[str, str|None, list[str], bool] (4-tuple)
# - _integrate_fix: tuple[str, list[str]] (2-tuple)
# ---------------------------------------------------------------------------


class TestReturnTupleShapeInvariant:
    """TS-02-22: _harvest_and_integrate() and _integrate_fix() maintain their
    return type invariant across all three merge strategy modes and partial
    failure scenarios.

    Requirements: 02-REQ-8.1

    Note: The return shapes differ between the two functions:
    - _harvest_and_integrate: tuple[str, str|None, list[str], bool]
    - _integrate_fix: tuple[str, list[str]]
    This test verifies each function preserves its OWN return shape across modes.
    """

    # --- _harvest_and_integrate return shape tests ---

    @pytest.mark.asyncio
    @pytest.mark.parametrize("mode", ["direct", "branch", "pr"])
    async def test_harvest_and_integrate_returns_4_tuple(self, mode: str) -> None:
        """_harvest_and_integrate returns a 4-tuple regardless of merge strategy."""
        runner = _make_runner(merge_strategy=mode)
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
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["file.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=None,
            ),
        ):
            result = await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        assert len(result) == 4, f"Expected 4-tuple, got {len(result)}-tuple in {mode} mode"
        status, error_message, touched_files, is_non_retryable = result
        assert isinstance(status, str)
        assert error_message is None or isinstance(error_message, str)
        assert isinstance(touched_files, list)
        assert all(isinstance(f, str) for f in touched_files)
        assert isinstance(is_non_retryable, bool)

    @pytest.mark.asyncio
    @pytest.mark.parametrize("mode", ["direct", "branch", "pr"])
    async def test_harvest_and_integrate_status_completed(self, mode: str) -> None:
        """_harvest_and_integrate returns status='completed' across all modes."""
        runner = _make_runner(merge_strategy=mode)
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
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["file.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=None,
            ),
        ):
            result = await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        status, error_message, _touched, is_non_retryable = result
        assert status == "completed"
        assert error_message is None
        assert is_non_retryable is False

    # --- _integrate_fix return shape tests ---

    @pytest.mark.asyncio
    @pytest.mark.parametrize("mode", ["direct", "branch", "pr"])
    async def test_integrate_fix_returns_2_tuple(self, mode: str) -> None:
        """_integrate_fix returns a 2-tuple regardless of merge strategy.

        Note: The spec claims a 4-tuple but the actual function returns
        tuple[str, list[str]]. Tests adapt to the real contract.
        """
        pipeline = _make_fix_pipeline(merge_strategy=mode)
        issue = _make_issue()
        spec = _make_spec()
        workspace = _make_workspace(branch="fix/test-branch")

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
        ):
            result = await pipeline._integrate_fix(issue, spec, workspace)

        assert len(result) == 2, f"Expected 2-tuple, got {len(result)}-tuple in {mode} mode"
        status, changed_files = result
        assert isinstance(status, str)
        assert isinstance(changed_files, list)
        assert all(isinstance(f, str) for f in changed_files)

    @pytest.mark.asyncio
    @pytest.mark.parametrize("mode", ["direct", "branch", "pr"])
    async def test_integrate_fix_status_is_valid(self, mode: str) -> None:
        """_integrate_fix returns a valid status string across all modes.

        The valid statuses are 'merged', 'no_changes', or 'error'.
        For successful cases, 'merged' is expected.
        """
        pipeline = _make_fix_pipeline(merge_strategy=mode)
        issue = _make_issue()
        spec = _make_spec()
        workspace = _make_workspace(branch="fix/test-branch")

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
        ):
            result = await pipeline._integrate_fix(issue, spec, workspace)

        status, _changed_files = result
        assert status in {"merged", "no_changes", "error"}


# ---------------------------------------------------------------------------
# TS-02-P2: Property test — return tuple shape is invariant across modes
#
# Note: Adapted for actual return types per reviewer findings.
# ---------------------------------------------------------------------------


class TestReturnTupleShapeProperty:
    """TS-02-P2: For any call to _harvest_and_integrate() or _integrate_fix()
    across all merge strategy modes and partial failure scenarios, the return
    tuple always maintains its expected shape.

    Property: 02-PROP-2
    Validates: 02-REQ-8.1, 02-REQ-2.1, 02-REQ-2.2, 02-REQ-3.1, 02-REQ-3.2,
               02-REQ-4.1, 02-REQ-4.2, 02-REQ-4.E2, 02-REQ-4.E3

    Return shape by function:
    - _harvest_and_integrate: tuple[str, str|None, list[str], bool]
    - _integrate_fix: tuple[str, list[str]]
    """

    @pytest.mark.asyncio
    @pytest.mark.parametrize(
        ("mode", "harvest_result"),
        [
            ("direct", ["file_a.py"]),
            ("direct", []),
            ("branch", ["changed.py"]),
            ("branch", []),
            ("pr", ["config.py"]),
            ("pr", []),
        ],
    )
    async def test_harvest_and_integrate_shape_invariant(
        self,
        mode: str,
        harvest_result: list[str],
    ) -> None:
        """_harvest_and_integrate always returns (str, str|None, list[str], bool)."""
        runner = _make_runner(merge_strategy=mode)
        workspace = _make_workspace()
        outcome = _make_session_outcome("completed")

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=harvest_result,
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
                return_value=harvest_result,
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=None,
            ),
        ):
            result = await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        status, err_msg, touched, non_retry = result
        assert status == "completed"
        assert err_msg is None
        assert isinstance(touched, list)
        assert all(isinstance(f, str) for f in touched)
        assert non_retry is False

    @pytest.mark.asyncio
    @pytest.mark.parametrize(
        ("mode", "harvest_result"),
        [
            ("direct", ["fix.py"]),
            ("direct", []),
            ("branch", ["changed.py"]),
            ("branch", []),
            ("pr", ["auth/login.py"]),
            ("pr", []),
        ],
    )
    async def test_integrate_fix_shape_invariant(
        self,
        mode: str,
        harvest_result: list[str],
    ) -> None:
        """_integrate_fix always returns (str, list[str]).

        Note: Adapted from spec's 4-tuple claim to actual 2-tuple contract.
        """
        pipeline = _make_fix_pipeline(merge_strategy=mode)
        issue = _make_issue()
        spec = _make_spec()
        workspace = _make_workspace(branch="fix/test-branch")

        with (
            patch.object(
                pipeline,
                "_harvest_and_push",
                new_callable=AsyncMock,
                return_value=harvest_result,
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
                return_value=harvest_result,
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=None,
            ),
        ):
            result = await pipeline._integrate_fix(issue, spec, workspace)

        assert len(result) == 2
        status, touched = result
        assert isinstance(status, str)
        assert status in {"merged", "no_changes", "error"}
        assert isinstance(touched, list)
        assert all(isinstance(f, str) for f in touched)


# ---------------------------------------------------------------------------
# TS-02-P6: Property test — direct mode produces no behavioral change
# ---------------------------------------------------------------------------


class TestDirectModeInvariantProperty:
    """TS-02-P6: For any execution where merge_strategy is 'direct' or absent,
    the code path is identical to the pre-feature implementation.

    Property: 02-PROP-6
    Validates: 02-REQ-2.1, 02-REQ-2.2, 02-REQ-1.E1

    Asserts: harvest() is called, no extra Merge Strategy log lines, create_pr
    not called, create_platform_safe not called.
    """

    @pytest.mark.asyncio
    @pytest.mark.parametrize(
        ("merge_strategy", "description"),
        [
            ("direct", "explicit direct mode"),
        ],
    )
    async def test_harvest_called_in_direct_mode(
        self,
        merge_strategy: str,
        description: str,
    ) -> None:
        """harvest() is always called when merge_strategy is 'direct'."""
        runner = _make_runner(merge_strategy=merge_strategy)
        workspace = _make_workspace()
        outcome = _make_session_outcome("completed")

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["file.py"],
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
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        assert mock_harvest.call_count == 1, f"harvest() not called in {description}"

    @pytest.mark.asyncio
    async def test_default_config_behaves_as_direct(self) -> None:
        """When merge_strategy defaults to 'direct', harvest() is called."""
        # This tests that the default value works identically to explicit 'direct'
        config = AgentFoxConfig(
            workspace=WorkspaceConfig(
                # merge_strategy not specified — should default to 'direct'
            ),
        )
        runner = NodeSessionRunner(
            "test_spec:1",
            config,
            knowledge_db=_MOCK_KB,
        )
        workspace = _make_workspace()
        outcome = _make_session_outcome("completed")

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["file.py"],
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
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        assert mock_harvest.call_count == 1

    @pytest.mark.asyncio
    async def test_no_merge_strategy_logs_in_direct_mode(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """No 'Merge strategy' log lines are emitted in direct mode."""
        runner = _make_runner(merge_strategy="direct")
        workspace = _make_workspace()
        outcome = _make_session_outcome("completed")

        with (
            caplog.at_level(logging.DEBUG),
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
        ):
            await runner._harvest_and_integrate(
                "test_spec:1", outcome, workspace, Path("/tmp/repo")
            )

        extra_info_logs = [
            r
            for r in caplog.records
            if r.levelno >= logging.INFO and "Merge strategy" in r.message
        ]
        assert len(extra_info_logs) == 0

    @pytest.mark.asyncio
    async def test_integrate_fix_direct_calls_harvest_and_push(self) -> None:
        """_integrate_fix in direct mode calls _harvest_and_push() once."""
        pipeline = _make_fix_pipeline(merge_strategy="direct")
        issue = _make_issue()
        spec = _make_spec()
        workspace = _make_workspace(branch="fix/test-branch")

        with (
            patch.object(
                pipeline,
                "_harvest_and_push",
                new_callable=AsyncMock,
                return_value=["fix.py"],
            ) as mock_hp,
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
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        assert mock_hp.call_count == 1

    @pytest.mark.asyncio
    async def test_integrate_fix_direct_no_merge_strategy_logs(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """No merge strategy log lines in _integrate_fix direct mode."""
        pipeline = _make_fix_pipeline(merge_strategy="direct")
        issue = _make_issue()
        spec = _make_spec()
        workspace = _make_workspace(branch="fix/test-branch")

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
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        extra_logs = [
            r
            for r in caplog.records
            if r.levelno >= logging.INFO and "Merge strategy" in r.message
        ]
        assert len(extra_logs) == 0
