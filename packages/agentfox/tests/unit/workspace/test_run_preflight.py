"""Tests for run-level workspace pre-flight check.

Verifies that run_preflight_workspace_check correctly:
- Prunes stale worktree entries
- Detects stale lock files
- Tests git credential availability
- Cleans up stale worktree directories (issue #629)
- Returns structured results
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import AsyncMock, patch

import pytest
from agentfox.workspace.health import (
    WorkspacePreflightResult,
    cleanup_stale_worktrees,
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


# ---------------------------------------------------------------------------
# Stale worktree cleanup (issue #629)
# ---------------------------------------------------------------------------


class TestCleanupStaleWorktrees:
    """cleanup_stale_worktrees removes leftover worktree directories at startup."""

    @pytest.mark.asyncio
    async def test_no_worktrees_dir_is_noop(self, tmp_path: Path) -> None:
        """No .agent-fox/worktrees/ directory → 0 removed, no errors."""
        count = await cleanup_stale_worktrees(tmp_path)
        assert count == 0

    @pytest.mark.asyncio
    async def test_empty_worktrees_dir_is_noop(self, tmp_path: Path) -> None:
        """Empty .agent-fox/worktrees/ → 0 removed."""
        (tmp_path / ".agent-fox" / "worktrees").mkdir(parents=True)
        count = await cleanup_stale_worktrees(tmp_path)
        assert count == 0

    @pytest.mark.asyncio
    async def test_removes_stale_worktree_directories(self, tmp_path: Path) -> None:
        """Stale worktree directories are removed via git worktree remove --force."""
        worktrees_root = tmp_path / ".agent-fox" / "worktrees"
        wt1 = worktrees_root / "spec_a" / "1"
        wt2 = worktrees_root / "spec_b" / "2"
        wt1.mkdir(parents=True)
        wt2.mkdir(parents=True)
        (wt1 / "some_file.py").touch()
        (wt2 / "other_file.py").touch()

        git_calls: list[list[str]] = []

        async def mock_git(args, cwd, check=True, timeout=None):
            git_calls.append(args)
            if args[:2] == ["worktree", "list"]:
                return (
                    0,
                    f"worktree {wt1}\nbranch refs/heads/feature/spec_a/1\n\n"
                    f"worktree {wt2}\nbranch refs/heads/feature/spec_b/2\n\n",
                    "",
                )
            return (0, "", "")

        with patch("agentfox.workspace.health.run_git", side_effect=mock_git):
            count = await cleanup_stale_worktrees(tmp_path)

        assert count == 2
        remove_calls = [c for c in git_calls if c[:2] == ["worktree", "remove"]]
        assert len(remove_calls) == 2

    @pytest.mark.asyncio
    async def test_fallback_rmtree_when_git_remove_fails(self, tmp_path: Path) -> None:
        """When git worktree remove fails, fall back to shutil.rmtree."""
        worktrees_root = tmp_path / ".agent-fox" / "worktrees"
        wt = worktrees_root / "spec_x" / "1"
        wt.mkdir(parents=True)
        (wt / "file.py").touch()

        async def mock_git(args, cwd, check=True, timeout=None):
            if args[:2] == ["worktree", "list"]:
                return (0, "", "")
            if args[:2] == ["worktree", "remove"]:
                return (1, "", "error: failed")
            return (0, "", "")

        with patch("agentfox.workspace.health.run_git", side_effect=mock_git):
            count = await cleanup_stale_worktrees(tmp_path)

        assert count == 1
        assert not wt.exists()

    @pytest.mark.asyncio
    async def test_cleans_empty_parent_dirs(self, tmp_path: Path) -> None:
        """After removal, empty parent dirs like spec_name/ are cleaned up."""
        worktrees_root = tmp_path / ".agent-fox" / "worktrees"
        wt = worktrees_root / "spec_y" / "3"
        wt.mkdir(parents=True)
        (wt / "code.py").touch()

        async def mock_git(args, cwd, check=True, timeout=None):
            if args[:2] == ["worktree", "list"]:
                return (0, "", "")
            return (0, "", "")

        with patch("agentfox.workspace.health.run_git", side_effect=mock_git):
            count = await cleanup_stale_worktrees(tmp_path)

        assert count == 1
        assert not (worktrees_root / "spec_y").exists()

    @pytest.mark.asyncio
    async def test_never_raises_on_git_failure(self, tmp_path: Path) -> None:
        """cleanup_stale_worktrees never raises even when git commands fail."""
        worktrees_root = tmp_path / ".agent-fox" / "worktrees"
        wt = worktrees_root / "spec_z" / "1"
        wt.mkdir(parents=True)

        with patch("agentfox.workspace.health.run_git", new_callable=AsyncMock) as mock_git:
            mock_git.side_effect = Exception("total failure")
            count = await cleanup_stale_worktrees(tmp_path)

        assert isinstance(count, int)

    @pytest.mark.asyncio
    async def test_four_level_paths_cleaned(self, tmp_path: Path) -> None:
        """4-level worktree paths (spec/group/role/mode) are also cleaned."""
        worktrees_root = tmp_path / ".agent-fox" / "worktrees"
        wt = worktrees_root / "spec_r" / "2" / "reviewer" / "audit-review"
        wt.mkdir(parents=True)
        (wt / "test.py").touch()

        async def mock_git(args, cwd, check=True, timeout=None):
            if args[:2] == ["worktree", "list"]:
                return (0, f"worktree {wt}\nbranch refs/heads/feature/spec_r/2/reviewer/audit-review\n\n", "")
            return (0, "", "")

        with patch("agentfox.workspace.health.run_git", side_effect=mock_git):
            count = await cleanup_stale_worktrees(tmp_path)

        assert count == 1
        assert not wt.exists()

    @pytest.mark.asyncio
    async def test_preflight_calls_cleanup(self, tmp_path: Path) -> None:
        """run_preflight_workspace_check invokes cleanup_stale_worktrees."""
        git_dir = tmp_path / ".git"
        git_dir.mkdir()
        worktrees_root = tmp_path / ".agent-fox" / "worktrees"
        wt = worktrees_root / "spec_p" / "1"
        wt.mkdir(parents=True)
        (wt / "f.py").touch()

        async def mock_git(args, cwd, check=True, timeout=None):
            if args[:2] == ["worktree", "list"]:
                return (0, "", "")
            return (0, "", "")

        with patch("agentfox.workspace.health.run_git", side_effect=mock_git):
            result = await run_preflight_workspace_check(tmp_path)

        assert result.stale_worktrees_removed >= 1
        assert not wt.exists()
