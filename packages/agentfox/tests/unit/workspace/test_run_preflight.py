"""Tests for run-level workspace pre-flight check.

Verifies that run_preflight_workspace_check correctly:
- Prunes stale worktree entries
- Detects stale lock files
- Tests git credential availability
- Returns structured results
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import AsyncMock, patch

import pytest

from agentfox.workspace.health import (
    WorkspacePreflightResult,
    run_preflight_workspace_check,
)


class TestWorkspacePreflightResult:
    """WorkspacePreflightResult dataclass defaults."""

    def test_defaults(self) -> None:
        result = WorkspacePreflightResult()
        assert result.push_available is True
        assert result.issues_found == []
        assert result.worktrees_pruned is False
        assert result.stale_locks_found == []


class TestRunPreflightWorkspaceCheck:
    """run_preflight_workspace_check integration."""

    @pytest.mark.asyncio
    async def test_prune_succeeds(self, tmp_path: Path) -> None:
        git_dir = tmp_path / ".git"
        git_dir.mkdir()

        with patch("agentfox.workspace.health.run_git", new_callable=AsyncMock) as mock_git:
            mock_git.return_value = (0, "", "")

            result = await run_preflight_workspace_check(tmp_path)

        assert result.worktrees_pruned is True
        prune_call = mock_git.call_args_list[0]
        assert prune_call[0][0] == ["worktree", "prune"]

    @pytest.mark.asyncio
    async def test_prune_failure_logged(self, tmp_path: Path) -> None:
        git_dir = tmp_path / ".git"
        git_dir.mkdir()

        with patch("agentfox.workspace.health.run_git", new_callable=AsyncMock) as mock_git:
            mock_git.return_value = (1, "", "error: could not prune")

            result = await run_preflight_workspace_check(tmp_path)

        assert result.worktrees_pruned is False
        assert any("prune failed" in issue for issue in result.issues_found)

    @pytest.mark.asyncio
    async def test_stale_lock_detected(self, tmp_path: Path) -> None:
        git_dir = tmp_path / ".git"
        git_dir.mkdir()
        lock_file = git_dir / "index.lock"
        lock_file.touch()
        import os
        import time

        old_time = time.time() - 7200
        os.utime(lock_file, (old_time, old_time))

        with patch("agentfox.workspace.health.run_git", new_callable=AsyncMock) as mock_git:
            mock_git.return_value = (0, "", "")

            result = await run_preflight_workspace_check(tmp_path)

        assert "index.lock" in result.stale_locks_found

    @pytest.mark.asyncio
    async def test_credential_failure_disables_push(self, tmp_path: Path) -> None:
        git_dir = tmp_path / ".git"
        git_dir.mkdir()

        call_count = 0

        async def mock_git(args, cwd, check=True, timeout=None):
            nonlocal call_count
            call_count += 1
            if args[0] == "worktree":
                return (0, "", "")
            if args[0] == "ls-remote":
                return (128, "", "fatal: could not read Username: terminal prompts disabled")
            return (0, "", "")

        with patch("agentfox.workspace.health.run_git", side_effect=mock_git):
            result = await run_preflight_workspace_check(tmp_path)

        assert result.push_available is False
        assert any("credentials unavailable" in issue for issue in result.issues_found)

    @pytest.mark.asyncio
    async def test_credential_success(self, tmp_path: Path) -> None:
        git_dir = tmp_path / ".git"
        git_dir.mkdir()

        with patch("agentfox.workspace.health.run_git", new_callable=AsyncMock) as mock_git:
            mock_git.return_value = (0, "", "")

            result = await run_preflight_workspace_check(tmp_path)

        assert result.push_available is True

    @pytest.mark.asyncio
    async def test_all_checks_best_effort(self, tmp_path: Path) -> None:
        """Pre-flight never raises, even if all checks fail."""
        git_dir = tmp_path / ".git"
        git_dir.mkdir()

        with patch("agentfox.workspace.health.run_git", new_callable=AsyncMock) as mock_git:
            mock_git.side_effect = Exception("subprocess failed")

            result = await run_preflight_workspace_check(tmp_path)

        assert isinstance(result, WorkspacePreflightResult)
