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


# ---------------------------------------------------------------------------
# Issue #694: Consolidation tests
# ---------------------------------------------------------------------------


class TestSinglePruneDuringPreflight:
    """TS-NS-3: git worktree prune is called exactly once per preflight."""

    @pytest.mark.asyncio
    async def test_prune_called_exactly_once(self, tmp_path: Path) -> None:
        """git worktree prune is called exactly once across the entire
        run_preflight_workspace_check invocation (issue #694)."""
        git_dir = tmp_path / ".git"
        git_dir.mkdir()

        prune_calls: list[list[str]] = []

        async def mock_git(args, cwd, check=True, timeout=None):
            if args == ["worktree", "prune"]:
                prune_calls.append(args)
            return (0, "", "")

        with patch("agentfox.workspace.health.run_git", side_effect=mock_git):
            result = await run_preflight_workspace_check(tmp_path)

        assert len(prune_calls) == 1, (
            f"Expected exactly 1 worktree prune call, got {len(prune_calls)}"
        )
        assert result.worktrees_pruned is True


class TestNoShutilRmtreeInHealth:
    """TS-NS-1: health.py uses _safe_rmtree, not raw shutil.rmtree."""

    def test_no_shutil_rmtree_import(self) -> None:
        """health.py does not import or call shutil.rmtree (issue #694)."""
        import inspect

        from agentfox.workspace import health

        source = inspect.getsource(health)
        assert "shutil.rmtree" not in source, (
            "health.py must not use shutil.rmtree; use _safe_rmtree instead"
        )
        assert "import shutil" not in source, (
            "health.py must not import shutil after consolidation"
        )


class TestCleanupStaleWorktreeSymlinkProtection:
    """TS-NS-1: cleanup_stale_worktrees uses _safe_rmtree for CWE-59 safety."""

    @pytest.mark.asyncio
    async def test_symlink_target_not_deleted(self, tmp_path: Path) -> None:
        """A symlink inside a stale worktree dir must not cause deletion
        of the external target file (CWE-59, issue #694)."""
        # External file that must survive
        external_file = tmp_path / "external.txt"
        external_file.write_text("keep me")

        # Create a stale worktree directory with a symlink inside
        worktrees_root = tmp_path / ".agent-fox" / "worktrees"
        wt = worktrees_root / "spec_sym" / "1"
        wt.mkdir(parents=True)
        (wt / "link").symlink_to(external_file)

        async def mock_git(args, cwd, check=True, timeout=None):
            if args[:2] == ["worktree", "list"]:
                return (0, "", "")
            return (0, "", "")

        with patch("agentfox.workspace.health.run_git", side_effect=mock_git):
            count = await cleanup_stale_worktrees(tmp_path)

        assert count == 1
        assert not wt.exists(), "Stale worktree should be removed"
        assert external_file.exists(), "External file must survive symlink cleanup"
        assert external_file.read_text() == "keep me"


class TestCleanupPreservesNonEmptyAncestors:
    """TS-NS-2: cleanup_stale_worktrees delegates to _cleanup_empty_ancestors."""

    @pytest.mark.asyncio
    async def test_non_empty_spec_dir_preserved(self, tmp_path: Path) -> None:
        """When a spec dir has two task dirs and only one is removed,
        the spec dir is preserved (issue #694).

        We patch _safe_rmtree so that only wt_stale is actually removed,
        simulating wt_keep being locked/in-use.  The ancestor cleanup
        must preserve spec_mixed/ because it still contains wt_keep.
        """
        from agentfox.workspace.worktree import _safe_rmtree as real_safe_rmtree

        worktrees_root = tmp_path / ".agent-fox" / "worktrees"
        # Two task dirs under the same spec
        wt_stale = worktrees_root / "spec_mixed" / "1"
        wt_stale.mkdir(parents=True)
        (wt_stale / "code.py").touch()

        wt_keep = worktrees_root / "spec_mixed" / "2"
        wt_keep.mkdir(parents=True)
        (wt_keep / "code.py").touch()

        async def mock_git(args, cwd, check=True, timeout=None):
            if args[:2] == ["worktree", "list"]:
                # Only wt_stale is registered; wt_keep is not.
                return (0, f"worktree {wt_stale}\nbranch refs/heads/feature/spec_mixed/1\n\n", "")
            if args[:2] == ["worktree", "remove"]:
                # Simulate successful removal via git
                import shutil as _shutil
                target = Path(args[3])
                if target.exists():
                    _shutil.rmtree(target)
                return (0, "", "")
            return (0, "", "")

        def selective_safe_rmtree(path: Path) -> None:
            """Only remove wt_stale; wt_keep simulates being locked."""
            if path == wt_keep or wt_keep in path.parents:
                return  # simulate removal failure (dir stays)
            real_safe_rmtree(path)

        with (
            patch("agentfox.workspace.health.run_git", side_effect=mock_git),
            patch("agentfox.workspace.health._safe_rmtree", side_effect=selective_safe_rmtree),
        ):
            count = await cleanup_stale_worktrees(tmp_path)

        assert count == 1
        assert not wt_stale.exists(), "Stale worktree should be removed"
        # spec_mixed/ should be preserved because wt_keep (task 2) is still there
        spec_dir = worktrees_root / "spec_mixed"
        assert spec_dir.exists(), "Non-empty spec dir must be preserved"
        assert wt_keep.exists(), "Sibling worktree must not be touched"
