"""Tests for _mark_subtasks_done no-diff handling.

Issue #681: _mark_subtasks_done raised WorkspaceError when tasks.json had
no staged diff (already committed by the coder agent). After the fix, it
checks for staged changes and skips the commit silently.

Test Spec: TS-NS-3, TS-NS-4
Requirements: NS-REQ-3, NS-REQ-4
"""

from __future__ import annotations

import logging
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest
from agentfox.core.config import AgentFoxConfig
from agentfox.engine.session_lifecycle import NodeSessionRunner
from agentfox.knowledge.db import KnowledgeDB
from agentfox.workspace import WorkspaceInfo

_MOCK_KB = MagicMock(spec=KnowledgeDB)


class TestMarkSubtasksDoneNoDiff:
    """TS-NS-3: _mark_subtasks_done skips commit when no staged changes."""

    @pytest.mark.asyncio
    async def test_no_commit_when_no_staged_diff(self, tmp_path: Path) -> None:
        """When git diff --cached --quiet returns 0, git commit is NOT called."""
        runner = NodeSessionRunner("spec:1", AgentFoxConfig(), knowledge_db=_MOCK_KB)
        runner._spec_name = "08_api_key_management_ui"
        runner._task_group = 2

        workspace = WorkspaceInfo(
            path=tmp_path,
            spec_name="08_api_key_management_ui",
            task_group=2,
            branch="feature/08_api_key_management_ui/2",
        )

        calls: list[list[str]] = []

        async def mock_run_git(args, cwd, check=True):
            calls.append(args)
            # git diff --cached --quiet returns 0 when no staged changes
            if args[:3] == ["diff", "--cached", "--quiet"]:
                return 0, "", ""
            return 0, "", ""

        # Create a fake spec directory with tasks.json
        spec_dir = tmp_path / "08_api_key_management_ui"
        spec_dir.mkdir()
        (spec_dir / "tasks.json").write_text("{}")

        mock_spec = MagicMock()
        mock_tasks = MagicMock()

        with (
            patch("agentfox.core.config.resolve_spec_root", return_value=tmp_path),
            patch("afspec.load_spec", return_value=mock_spec),
            patch("afspec.mutate.complete_subtask_states", return_value=mock_tasks),
            patch("afspec.save"),
            patch("agentfox.workspace.git.run_git", side_effect=mock_run_git),
        ):
            # Should NOT raise
            await runner._mark_subtasks_done(workspace)

        # Verify git commit was never called
        commit_calls = [c for c in calls if c[0] == "commit"]
        assert len(commit_calls) == 0, f"git commit should not be called, but was: {commit_calls}"

    @pytest.mark.asyncio
    async def test_no_warning_logged_when_no_staged_diff(self, tmp_path: Path, caplog) -> None:
        """No WARNING is logged when there is nothing to commit."""
        runner = NodeSessionRunner("spec:1", AgentFoxConfig(), knowledge_db=_MOCK_KB)
        runner._spec_name = "08_api_key_management_ui"
        runner._task_group = 2

        workspace = WorkspaceInfo(
            path=tmp_path,
            spec_name="08_api_key_management_ui",
            task_group=2,
            branch="feature/08_api_key_management_ui/2",
        )

        async def mock_run_git(args, cwd, check=True):
            if args[:3] == ["diff", "--cached", "--quiet"]:
                return 0, "", ""
            return 0, "", ""

        spec_dir = tmp_path / "08_api_key_management_ui"
        spec_dir.mkdir()
        (spec_dir / "tasks.json").write_text("{}")

        mock_spec = MagicMock()
        mock_tasks = MagicMock()

        with (
            patch("agentfox.core.config.resolve_spec_root", return_value=tmp_path),
            patch("afspec.load_spec", return_value=mock_spec),
            patch("afspec.mutate.complete_subtask_states", return_value=mock_tasks),
            patch("afspec.save"),
            patch("agentfox.workspace.git.run_git", side_effect=mock_run_git),
            caplog.at_level(logging.WARNING),
        ):
            await runner._mark_subtasks_done(workspace)

        warning_records = [r for r in caplog.records if r.levelno >= logging.WARNING]
        subtask_warnings = [r for r in warning_records if "subtasks" in r.message.lower()]
        assert len(subtask_warnings) == 0, f"Unexpected WARNING about subtasks: {subtask_warnings}"

    @pytest.mark.asyncio
    async def test_debug_logged_when_no_staged_diff(self, tmp_path: Path, caplog) -> None:
        """A DEBUG message is logged when skipping commit due to no changes."""
        runner = NodeSessionRunner("spec:1", AgentFoxConfig(), knowledge_db=_MOCK_KB)
        runner._spec_name = "08_api_key_management_ui"
        runner._task_group = 2

        workspace = WorkspaceInfo(
            path=tmp_path,
            spec_name="08_api_key_management_ui",
            task_group=2,
            branch="feature/08_api_key_management_ui/2",
        )

        async def mock_run_git(args, cwd, check=True):
            if args[:3] == ["diff", "--cached", "--quiet"]:
                return 0, "", ""
            return 0, "", ""

        spec_dir = tmp_path / "08_api_key_management_ui"
        spec_dir.mkdir()
        (spec_dir / "tasks.json").write_text("{}")

        mock_spec = MagicMock()
        mock_tasks = MagicMock()

        with (
            patch("agentfox.core.config.resolve_spec_root", return_value=tmp_path),
            patch("afspec.load_spec", return_value=mock_spec),
            patch("afspec.mutate.complete_subtask_states", return_value=mock_tasks),
            patch("afspec.save"),
            patch("agentfox.workspace.git.run_git", side_effect=mock_run_git),
            caplog.at_level(logging.DEBUG),
        ):
            await runner._mark_subtasks_done(workspace)

        debug_records = [r for r in caplog.records if r.levelno == logging.DEBUG and "already up-to-date" in r.message]
        assert len(debug_records) >= 1, "Expected DEBUG log about tasks.json being up-to-date"


class TestMarkSubtasksDoneWithChanges:
    """TS-NS-4: _mark_subtasks_done commits when staged changes exist."""

    @pytest.mark.asyncio
    async def test_commit_called_when_staged_diff_exists(self, tmp_path: Path) -> None:
        """When git diff --cached --quiet returns 1 (changes), git commit IS called."""
        runner = NodeSessionRunner("spec:1", AgentFoxConfig(), knowledge_db=_MOCK_KB)
        runner._spec_name = "08_api_key_management_ui"
        runner._task_group = 2

        workspace = WorkspaceInfo(
            path=tmp_path,
            spec_name="08_api_key_management_ui",
            task_group=2,
            branch="feature/08_api_key_management_ui/2",
        )

        calls: list[list[str]] = []

        async def mock_run_git(args, cwd, check=True):
            calls.append(args)
            # git diff --cached --quiet returns 1 when there ARE staged changes
            if args[:3] == ["diff", "--cached", "--quiet"]:
                return 1, "", ""
            return 0, "", ""

        spec_dir = tmp_path / "08_api_key_management_ui"
        spec_dir.mkdir()
        (spec_dir / "tasks.json").write_text("{}")

        mock_spec = MagicMock()
        mock_tasks = MagicMock()

        with (
            patch("agentfox.core.config.resolve_spec_root", return_value=tmp_path),
            patch("afspec.load_spec", return_value=mock_spec),
            patch("afspec.mutate.complete_subtask_states", return_value=mock_tasks),
            patch("afspec.save"),
            patch("agentfox.workspace.git.run_git", side_effect=mock_run_git),
        ):
            await runner._mark_subtasks_done(workspace)

        # Verify git commit was called exactly once
        commit_calls = [c for c in calls if c[0] == "commit"]
        assert len(commit_calls) == 1, f"Expected exactly 1 commit call, got {len(commit_calls)}: {commit_calls}"

        # Verify the commit message format
        commit_args = commit_calls[0]
        assert "-m" in commit_args
        msg_idx = commit_args.index("-m") + 1
        assert "chore: mark task group 2 subtasks done" in commit_args[msg_idx]
