"""Unit tests for the pre-harvest auto-commit sweep in fix_pipeline.py.

Test Spec: TS-NS-1, TS-NS-2, TS-NS-3, TS-NS-5
Requirements: NS-REQ-1.1, NS-REQ-2.1, NS-REQ-3.1, NS-REQ-5.1
"""

from __future__ import annotations

import logging
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from agentfox.workspace import WorkspaceInfo


def _make_workspace(path: Path | None = None) -> WorkspaceInfo:
    return WorkspaceInfo(
        path=path or Path("/tmp/mock-worktree"),
        branch="fix/614-test",
        spec_name="fix-issue-614",
        task_group=0,
    )


# ---------------------------------------------------------------------------
# TS-NS-1: dirty worktree — auto-commit runs before harvest
# ---------------------------------------------------------------------------


class TestAutoCommitDirtyBeforeHarvest:
    """TS-NS-1: _auto_commit_pending_changes commits dirty worktree before harvest.

    Requirement: NS-REQ-1.1
    """

    async def test_auto_commit_called_on_dirty_worktree(self, tmp_path: Path) -> None:
        """auto_commit_worktree is called with the worktree path."""
        from agentfox.nightshift.fix_pipeline import NightShiftFixPipeline

        workspace = _make_workspace(tmp_path)
        committed_paths: list[Path] = []

        async def mock_auto_commit(worktree_path, message=None):
            committed_paths.append(worktree_path)
            return True

        pipeline = NightShiftFixPipeline.__new__(NightShiftFixPipeline)
        with patch(
            "agentfox.workspace.git.auto_commit_worktree",
            side_effect=mock_auto_commit,
        ):
            await pipeline._auto_commit_pending_changes(workspace)

        assert committed_paths == [tmp_path]

    async def test_auto_commit_returns_true_logs_info(
        self, tmp_path: Path, caplog: pytest.LogCaptureFixture
    ) -> None:
        """When auto_commit_worktree returns True, an INFO is logged."""
        from agentfox.nightshift.fix_pipeline import NightShiftFixPipeline

        workspace = _make_workspace(tmp_path)
        pipeline = NightShiftFixPipeline.__new__(NightShiftFixPipeline)

        with patch(
            "agentfox.workspace.git.auto_commit_worktree",
            new_callable=AsyncMock,
            return_value=True,
        ):
            with caplog.at_level(logging.INFO, logger="agentfox.nightshift.fix_pipeline"):
                await pipeline._auto_commit_pending_changes(workspace)

        assert any(r.levelno >= logging.INFO for r in caplog.records)


# ---------------------------------------------------------------------------
# TS-NS-2: clean worktree — no-op, no warning logged
# ---------------------------------------------------------------------------


class TestAutoCommitCleanWorktreeNoOp:
    """TS-NS-2: _auto_commit_pending_changes is a no-op when worktree is clean.

    Requirement: NS-REQ-2.1
    """

    async def test_no_warning_on_clean_worktree(
        self, tmp_path: Path, caplog: pytest.LogCaptureFixture
    ) -> None:
        """No WARNING is logged when auto_commit_worktree returns False (clean)."""
        from agentfox.nightshift.fix_pipeline import NightShiftFixPipeline

        workspace = _make_workspace(tmp_path)
        pipeline = NightShiftFixPipeline.__new__(NightShiftFixPipeline)

        with patch(
            "agentfox.workspace.git.auto_commit_worktree",
            new_callable=AsyncMock,
            return_value=False,
        ):
            with caplog.at_level(logging.WARNING, logger="agentfox.nightshift.fix_pipeline"):
                await pipeline._auto_commit_pending_changes(workspace)

        warnings = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert not warnings, f"Expected no warnings, got: {[r.message for r in warnings]}"


# ---------------------------------------------------------------------------
# TS-NS-3: auto-commit failure (empty commit) does not block harvest
# ---------------------------------------------------------------------------


class TestAutoCommitFailureDoesNotBlock:
    """TS-NS-3: _auto_commit_pending_changes swallows exceptions from auto_commit_worktree.

    Requirement: NS-REQ-3.1
    """

    async def test_does_not_raise_on_exception(self, tmp_path: Path) -> None:
        """_auto_commit_pending_changes does not propagate exceptions."""
        from agentfox.nightshift.fix_pipeline import NightShiftFixPipeline

        workspace = _make_workspace(tmp_path)
        pipeline = NightShiftFixPipeline.__new__(NightShiftFixPipeline)

        async def mock_auto_commit_raises(*args, **kwargs):
            raise RuntimeError("git commit failed: nothing to commit")

        with patch(
            "agentfox.workspace.git.auto_commit_worktree",
            side_effect=mock_auto_commit_raises,
        ):
            # Must not raise
            await pipeline._auto_commit_pending_changes(workspace)

    async def test_logs_warning_on_exception(
        self, tmp_path: Path, caplog: pytest.LogCaptureFixture
    ) -> None:
        """_auto_commit_pending_changes logs a WARNING when auto_commit_worktree raises."""
        from agentfox.nightshift.fix_pipeline import NightShiftFixPipeline

        workspace = _make_workspace(tmp_path)
        pipeline = NightShiftFixPipeline.__new__(NightShiftFixPipeline)

        async def mock_auto_commit_raises(*args, **kwargs):
            raise RuntimeError("git commit failed")

        with patch(
            "agentfox.workspace.git.auto_commit_worktree",
            side_effect=mock_auto_commit_raises,
        ):
            with caplog.at_level(logging.WARNING, logger="agentfox.nightshift.fix_pipeline"):
                await pipeline._auto_commit_pending_changes(workspace)

        assert any(r.levelno >= logging.WARNING for r in caplog.records)


# ---------------------------------------------------------------------------
# TS-NS-5: auto-commit is called after both coder AND reviewer sessions
# ---------------------------------------------------------------------------


class TestAutoCommitAfterCoderAndReviewer:
    """TS-NS-5: _auto_commit_pending_changes is called after _coder_review_loop().

    Requirement: NS-REQ-5.1

    This test validates the call ordering in the fix pipeline flow:
    _coder_review_loop → _auto_commit_pending_changes → _harvest_and_push.
    We do this by inspecting the source of fix_pipeline.py for the correct
    call order (structural / static test).
    """

    def test_auto_commit_called_after_coder_review_loop(self) -> None:
        """_auto_commit_pending_changes appears after _coder_review_loop in source."""
        import inspect

        from agentfox.nightshift import fix_pipeline

        source = inspect.getsource(fix_pipeline)

        loop_pos = source.find("_coder_review_loop(")
        auto_commit_pos = source.find("_auto_commit_pending_changes(")
        harvest_pos = source.find("_harvest_and_push(")

        assert loop_pos != -1, "_coder_review_loop not found in source"
        assert auto_commit_pos != -1, "_auto_commit_pending_changes not found in source"
        assert harvest_pos != -1, "_harvest_and_push not found in source"

        assert loop_pos < auto_commit_pos < harvest_pos, (
            "_auto_commit_pending_changes must appear between "
            "_coder_review_loop and _harvest_and_push in the source"
        )
