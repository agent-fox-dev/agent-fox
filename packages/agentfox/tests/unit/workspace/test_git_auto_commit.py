"""Unit tests for auto_commit_worktree() in workspace/git.py.

Test Spec: TS-NS-4
Requirements: NS-REQ-4.1
"""

from __future__ import annotations

import logging
from pathlib import Path
from unittest.mock import patch

import pytest
from agentfox.workspace.git import auto_commit_worktree

# ---------------------------------------------------------------------------
# TS-NS-4a: dirty worktree — stages and commits, returns True
# ---------------------------------------------------------------------------


class TestAutoCommitDirtyWorktree:
    """TS-NS-4: auto_commit_worktree stages and commits when worktree is dirty.

    Requirement: NS-REQ-4.1
    """

    async def test_returns_true_on_dirty_worktree(self, tmp_path: Path) -> None:
        """Returns True when there are uncommitted changes."""
        calls: list[list[str]] = []

        async def mock_run_git(args, cwd, check=True):
            calls.append(args)
            if args[0] == "status":
                return 0, " M some_file.py\n", ""
            return 0, "", ""

        with patch("agentfox.workspace.git.run_git", side_effect=mock_run_git):
            result = await auto_commit_worktree(tmp_path)

        assert result is True

    async def test_runs_add_then_commit(self, tmp_path: Path) -> None:
        """Runs git add -A then git commit in that order."""
        calls: list[list[str]] = []

        async def mock_run_git(args, cwd, check=True):
            calls.append(args)
            if args[0] == "status":
                return 0, " M some_file.py\n", ""
            return 0, "", ""

        with patch("agentfox.workspace.git.run_git", side_effect=mock_run_git):
            await auto_commit_worktree(tmp_path, message="fix: test commit")

        status_calls = [c for c in calls if c[0] == "status"]
        add_calls = [c for c in calls if c[0] == "add"]
        commit_calls = [c for c in calls if c[0] == "commit"]

        assert len(status_calls) == 1
        assert len(add_calls) == 1
        assert add_calls[0] == ["add", "-A"]
        assert len(commit_calls) == 1
        assert "-m" in commit_calls[0]
        assert "fix: test commit" in commit_calls[0]

        # add must precede commit
        add_idx = calls.index(add_calls[0])
        commit_idx = calls.index(commit_calls[0])
        assert add_idx < commit_idx

    async def test_uses_custom_message(self, tmp_path: Path) -> None:
        """Passes the custom commit message to git commit."""
        committed_messages: list[str] = []

        async def mock_run_git(args, cwd, check=True):
            if args[0] == "status":
                return 0, "?? newfile.txt\n", ""
            if args[0] == "commit":
                idx = args.index("-m")
                committed_messages.append(args[idx + 1])
            return 0, "", ""

        with patch("agentfox.workspace.git.run_git", side_effect=mock_run_git):
            await auto_commit_worktree(tmp_path, message="chore: my custom message")

        assert committed_messages == ["chore: my custom message"]


# ---------------------------------------------------------------------------
# TS-NS-4b: clean worktree — no add/commit, returns False
# ---------------------------------------------------------------------------


class TestAutoCommitCleanWorktree:
    """TS-NS-4: auto_commit_worktree is a no-op when the worktree is clean.

    Requirement: NS-REQ-2.1, NS-REQ-4.1
    """

    async def test_returns_false_on_clean_worktree(self, tmp_path: Path) -> None:
        """Returns False when git status --porcelain has no output."""
        calls: list[list[str]] = []

        async def mock_run_git(args, cwd, check=True):
            calls.append(args)
            return 0, "", ""

        with patch("agentfox.workspace.git.run_git", side_effect=mock_run_git):
            result = await auto_commit_worktree(tmp_path)

        assert result is False

    async def test_does_not_call_add_or_commit_on_clean(self, tmp_path: Path) -> None:
        """git add -A and git commit are NOT called when the worktree is clean."""
        calls: list[list[str]] = []

        async def mock_run_git(args, cwd, check=True):
            calls.append(args)
            return 0, "", ""

        with patch("agentfox.workspace.git.run_git", side_effect=mock_run_git):
            await auto_commit_worktree(tmp_path)

        subcommands = [c[0] for c in calls]
        assert "add" not in subcommands
        assert "commit" not in subcommands


# ---------------------------------------------------------------------------
# TS-NS-4c: empty commit (all changes gitignored) — does not raise, returns False
# ---------------------------------------------------------------------------


class TestAutoCommitEmptyCommit:
    """TS-NS-4: auto_commit_worktree handles empty commit gracefully.

    Requirement: NS-REQ-3.1, NS-REQ-4.1
    """

    async def test_returns_false_on_empty_commit(self, tmp_path: Path) -> None:
        """Returns False when git commit exits non-zero with nothing-to-commit."""

        async def mock_run_git(args, cwd, check=True):
            if args[0] == "status":
                return 0, " M something\n", ""
            if args[0] == "add":
                return 0, "", ""
            if args[0] == "commit":
                return 1, "", "nothing to commit, working tree clean"
            return 0, "", ""

        with patch("agentfox.workspace.git.run_git", side_effect=mock_run_git):
            result = await auto_commit_worktree(tmp_path)

        assert result is False

    async def test_does_not_raise_on_empty_commit(self, tmp_path: Path) -> None:
        """No exception is propagated when git commit returns non-zero."""

        async def mock_run_git(args, cwd, check=True):
            if args[0] == "status":
                return 0, " M something\n", ""
            if args[0] == "add":
                return 0, "", ""
            if args[0] == "commit":
                return 1, "", "nothing to commit"
            return 0, "", ""

        with patch("agentfox.workspace.git.run_git", side_effect=mock_run_git):
            # Must not raise
            await auto_commit_worktree(tmp_path)

    async def test_logs_warning_on_empty_commit(self, tmp_path: Path, caplog: pytest.LogCaptureFixture) -> None:
        """A WARNING is logged when git commit fails."""

        async def mock_run_git(args, cwd, check=True):
            if args[0] == "status":
                return 0, "?? ignored_file.log\n", ""
            if args[0] == "add":
                return 0, "", ""
            if args[0] == "commit":
                return 1, "", "nothing to commit"
            return 0, "", ""

        with patch("agentfox.workspace.git.run_git", side_effect=mock_run_git):
            with caplog.at_level(logging.WARNING, logger="agentfox.workspace.git"):
                await auto_commit_worktree(tmp_path)

        assert any("warning" in r.levelname.lower() or r.levelno >= logging.WARNING for r in caplog.records)
