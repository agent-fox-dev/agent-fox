"""Harvester tests.

Test Spec: TS-03-10 (squash merge), TS-03-11 (diverged squash merge),
           TS-03-E5 (no commits), TS-03-E6 (unresolvable conflict)
Requirements: 03-REQ-7.1 through 03-REQ-7.E2,
              45-REQ-4.1, 45-REQ-6.1
"""

from __future__ import annotations

import logging
import subprocess
from pathlib import Path
from unittest.mock import AsyncMock, patch

import pytest
from agentfox.core.errors import IntegrationError
from agentfox.workspace import create_worktree
from agentfox.workspace.git import run_git as _real_run_git
from agentfox.workspace.harvest import _clean_conflicting_untracked, harvest

from .conftest import add_commit_to_branch


class TestHarvesterSquashMerge:
    """TS-03-10: Harvester merges changes via squash merge."""

    @pytest.mark.asyncio
    async def test_squash_merge_succeeds(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """Harvesting a feature branch with commits squash-merges into develop."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")
        add_commit_to_branch(ws.path, "new_file.py", "print('hello')\n")

        files = await harvest(tmp_worktree_repo, ws, dev_branch="develop")
        assert "new_file.py" in files

    @pytest.mark.asyncio
    async def test_squash_produces_single_commit(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """After harvest, develop has exactly one new commit (squash)."""
        develop_tip_before = subprocess.run(
            ["git", "rev-parse", "develop"],
            cwd=tmp_worktree_repo,
            capture_output=True,
            text=True,
            check=True,
        ).stdout.strip()

        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")
        add_commit_to_branch(ws.path, "file_a.py", "a\n")
        add_commit_to_branch(ws.path, "file_b.py", "b\n")

        await harvest(tmp_worktree_repo, ws, dev_branch="develop")

        # Count commits on develop since the tip before harvest
        result = subprocess.run(
            ["git", "rev-list", "--count", f"{develop_tip_before}..develop"],
            cwd=tmp_worktree_repo,
            capture_output=True,
            text=True,
            check=True,
        )
        assert int(result.stdout.strip()) == 1, "Squash merge should produce exactly one commit"


class TestSquashCommitMessage:
    """Squash merge uses the feature branch tip commit's message, not SQUASH_MSG."""

    @pytest.mark.asyncio
    async def test_squash_uses_tip_commit_message(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """The squash commit title should be the feature branch tip's subject,
        not 'Squashed commit of the following:'."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")
        add_commit_to_branch(
            ws.path,
            "new_file.py",
            "print('hello')\n",
            message="feat: add greeting module",
        )

        await harvest(tmp_worktree_repo, ws, dev_branch="develop")

        result = subprocess.run(
            ["git", "log", "-1", "--format=%s", "develop"],
            cwd=tmp_worktree_repo,
            capture_output=True,
            text=True,
            check=True,
        )
        subject = result.stdout.strip()
        assert subject == "feat: add greeting module"
        assert "Squashed commit" not in subject

    @pytest.mark.asyncio
    async def test_squash_multi_commit_uses_tip_subject(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """Multi-commit branch uses the tip commit's subject as the title
        and includes earlier commit subjects in the body."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")
        add_commit_to_branch(
            ws.path,
            "file_a.py",
            "a\n",
            message="feat: add module A",
        )
        add_commit_to_branch(
            ws.path,
            "file_b.py",
            "b\n",
            message="feat: add module B",
        )

        await harvest(tmp_worktree_repo, ws, dev_branch="develop")

        result = subprocess.run(
            ["git", "log", "-1", "--format=%s", "develop"],
            cwd=tmp_worktree_repo,
            capture_output=True,
            text=True,
            check=True,
        )
        subject = result.stdout.strip()
        assert subject == "feat: add module B"
        assert "Squashed commit" not in subject

        body_result = subprocess.run(
            ["git", "log", "-1", "--format=%b", "develop"],
            cwd=tmp_worktree_repo,
            capture_output=True,
            text=True,
            check=True,
        )
        body = body_result.stdout.strip()
        assert "- feat: add module A" in body

    @pytest.mark.asyncio
    async def test_squash_no_author_date_lines(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """Squash commit message must not contain Author: or Date: metadata."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")
        add_commit_to_branch(
            ws.path,
            "new_file.py",
            "content\n",
            message="fix: resolve edge case",
        )

        await harvest(tmp_worktree_repo, ws, dev_branch="develop")

        result = subprocess.run(
            ["git", "log", "-1", "--format=%B", "develop"],
            cwd=tmp_worktree_repo,
            capture_output=True,
            text=True,
            check=True,
        )
        full_msg = result.stdout
        assert "Author:" not in full_msg
        assert "Date:" not in full_msg


    @pytest.mark.asyncio
    async def test_squash_skips_housekeeping_tip(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """When the tip commit is an orchestrator housekeeping commit,
        the squash message should use the last substantive commit instead."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")
        add_commit_to_branch(
            ws.path,
            "impl.py",
            "implementation\n",
            message="feat: implement the feature\n\nDetailed description of the changes\nmade in this commit.",
        )
        add_commit_to_branch(
            ws.path,
            "tasks.json",
            '{"done": true}\n',
            message="chore: mark task group 1 subtasks done",
        )

        await harvest(tmp_worktree_repo, ws, dev_branch="develop")

        result = subprocess.run(
            ["git", "log", "-1", "--format=%s", "develop"],
            cwd=tmp_worktree_repo,
            capture_output=True,
            text=True,
            check=True,
        )
        subject = result.stdout.strip()
        assert subject == "feat: implement the feature"

        body_result = subprocess.run(
            ["git", "log", "-1", "--format=%b", "develop"],
            cwd=tmp_worktree_repo,
            capture_output=True,
            text=True,
            check=True,
        )
        body = body_result.stdout.strip()
        assert "Detailed description" in body
        assert "- chore: mark task group 1 subtasks done" in body


class TestHarvesterDivergedSquashMerge:
    """TS-03-11: Harvester squash-merges diverged branches."""

    @pytest.mark.asyncio
    async def test_diverged_merge_succeeds(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """When develop has diverged, harvester squash-merges successfully."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")

        # Add a commit on the feature branch (different file)
        add_commit_to_branch(
            ws.path,
            "feature_file.py",
            "feature content\n",
        )

        # Add a commit on develop (different file, no conflict)
        subprocess.run(
            ["git", "checkout", "develop"],
            cwd=tmp_worktree_repo,
            check=True,
            capture_output=True,
        )
        add_commit_to_branch(
            tmp_worktree_repo,
            "other_file.py",
            "develop content\n",
        )

        files = await harvest(tmp_worktree_repo, ws, dev_branch="develop")
        assert "feature_file.py" in files


class TestHarvesterMergeFallback:
    """Squash merge with identical content on both branches."""

    @pytest.mark.asyncio
    async def test_cherry_pick_conflict_falls_back_to_merge(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """When both branches have the same file with the same content,
        squash merge produces no new changes (no-op commit)."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")

        # Add a file on the feature branch
        add_commit_to_branch(
            ws.path,
            "tests/test_scaffold.py",
            "def test_scaffold(): pass\n",
        )

        # Add the SAME file with SAME content on develop (simulates
        # a prior session's merge containing the same change)
        subprocess.run(
            ["git", "checkout", "develop"],
            cwd=tmp_worktree_repo,
            check=True,
            capture_output=True,
        )
        add_commit_to_branch(
            tmp_worktree_repo,
            "tests/test_scaffold.py",
            "def test_scaffold(): pass\n",
        )

        # Harvest should succeed — no IntegrationError raised
        files = await harvest(tmp_worktree_repo, ws, dev_branch="develop")
        assert isinstance(files, list)


class TestHarvesterNoCommits:
    """TS-03-E5: Harvester with no new commits is no-op."""

    @pytest.mark.asyncio
    async def test_no_commits_returns_empty_list(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """Harvesting a branch with no new commits returns an empty list."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")
        # Don't add any commits
        files = await harvest(tmp_worktree_repo, ws, dev_branch="develop")
        assert files == []

    @pytest.mark.asyncio
    async def test_no_commits_leaves_develop_unchanged(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """Harvesting with no new commits does not change develop."""
        develop_tip_before = subprocess.run(
            ["git", "rev-parse", "develop"],
            cwd=tmp_worktree_repo,
            capture_output=True,
            text=True,
            check=True,
        ).stdout.strip()
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")

        await harvest(tmp_worktree_repo, ws, dev_branch="develop")

        develop_tip_after = subprocess.run(
            ["git", "rev-parse", "develop"],
            cwd=tmp_worktree_repo,
            capture_output=True,
            text=True,
            check=True,
        ).stdout.strip()
        assert develop_tip_after == develop_tip_before


class TestHarvesterConflictAutoResolve:
    """Harvester delegates conflict resolution to the merge agent.

    Previously these tests verified -X theirs auto-resolution. With the
    removal of blind strategy options (45-REQ-6.1), conflicts that cannot
    be resolved deterministically are delegated to the merge agent. When
    the agent fails, harvest raises IntegrationError.
    """

    @pytest.mark.asyncio
    async def test_add_add_conflict_raises_without_agent(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """When both branches add the same file with different content,
        the harvester delegates to the merge agent. When the agent fails,
        this raises IntegrationError (45-REQ-4.E1)."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")

        # Add a file on the feature branch
        add_commit_to_branch(
            ws.path,
            "shared.py",
            "feature content\n",
        )

        # Add the SAME file with DIFFERENT content on develop
        subprocess.run(
            ["git", "checkout", "develop"],
            cwd=tmp_worktree_repo,
            check=True,
            capture_output=True,
        )
        add_commit_to_branch(
            tmp_worktree_repo,
            "shared.py",
            "develop content\n",
        )

        # With the merge agent mocked to fail, harvest should raise
        with patch(
            "agentfox.workspace.harvest.run_merge_agent",
            new_callable=AsyncMock,
            return_value=False,
        ):
            with pytest.raises(IntegrationError, match="(?i)agent"):
                await harvest(tmp_worktree_repo, ws, dev_branch="develop")

    @pytest.mark.asyncio
    async def test_parallel_add_add_multiple_files(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """Simulates parallel sessions creating overlapping files —
        the exact scenario from issue #84. When the merge agent fails,
        raises IntegrationError."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")

        # Feature branch creates several files (simulating a task group)
        add_commit_to_branch(ws.path, "Makefile", "feature-makefile\n")
        add_commit_to_branch(ws.path, "go.mod", "feature-gomod\n")

        # Meanwhile, develop got the same files from another session
        subprocess.run(
            ["git", "checkout", "develop"],
            cwd=tmp_worktree_repo,
            check=True,
            capture_output=True,
        )
        add_commit_to_branch(tmp_worktree_repo, "Makefile", "develop-makefile\n")
        add_commit_to_branch(tmp_worktree_repo, "go.mod", "develop-gomod\n")

        # With the merge agent mocked to fail, harvest should raise
        with patch(
            "agentfox.workspace.harvest.run_merge_agent",
            new_callable=AsyncMock,
            return_value=False,
        ):
            with pytest.raises(IntegrationError, match="(?i)agent"):
                await harvest(tmp_worktree_repo, ws, dev_branch="develop")

    @pytest.mark.asyncio
    async def test_auto_resolve_preserves_non_conflicting_develop_changes(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """Non-conflicting changes from develop are preserved only when merge
        succeeds. With conflicting files, the merge agent is needed.
        When the agent fails, raises IntegrationError."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")

        # Feature branch creates one file
        add_commit_to_branch(ws.path, "shared.py", "feature content\n")

        # Develop creates the same file AND a different file
        subprocess.run(
            ["git", "checkout", "develop"],
            cwd=tmp_worktree_repo,
            check=True,
            capture_output=True,
        )
        add_commit_to_branch(tmp_worktree_repo, "shared.py", "develop content\n")
        add_commit_to_branch(tmp_worktree_repo, "other.py", "other content\n")

        # With the merge agent mocked to fail, harvest should raise
        with patch(
            "agentfox.workspace.harvest.run_merge_agent",
            new_callable=AsyncMock,
            return_value=False,
        ):
            with pytest.raises(IntegrationError, match="(?i)agent"):
                await harvest(tmp_worktree_repo, ws, dev_branch="develop")


class TestCleanConflictingUntracked:
    """Issue #546: safe-delete logic for untracked files during merge.

    AC-1: Matching content → delete with WARNING log, merge restores file.
    AC-2: Divergent content → IntegrationError, file preserved.
    AC-3: git show failure → file preserved, WARNING logged.
    AC-4: Log at WARNING level, full file list (no truncation).
    AC-5: No conflicts → no-op (covered by existing TestHarvesterSquashMerge).
    """

    @pytest.mark.asyncio
    async def test_matching_content_removed_with_warning(
        self,
        tmp_worktree_repo: Path,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """AC-1: Untracked file whose content matches the branch is deleted
        (with a WARNING), and the file is restored after the squash merge."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")
        file_content = "print('hello')\n"
        add_commit_to_branch(ws.path, "new_file.py", file_content)

        # Place the *same* content as an untracked file on develop.
        untracked = tmp_worktree_repo / "new_file.py"
        untracked.write_text(file_content)

        with caplog.at_level(logging.WARNING, logger="agentfox.workspace.harvest"):
            files = await harvest(tmp_worktree_repo, ws, dev_branch="develop")

        # File should appear in the changed-files list.
        assert "new_file.py" in files

        # File should be present in the working tree after the merge.
        assert (tmp_worktree_repo / "new_file.py").exists()

        # A WARNING-level log entry must mention both "Removing" and the path.
        warning_messages = [r.message for r in caplog.records if r.levelno == logging.WARNING]
        assert any("Removing" in msg and "new_file.py" in msg for msg in warning_messages), (
            f"Expected WARNING about 'Removing new_file.py'; got: {warning_messages}"
        )

    @pytest.mark.asyncio
    async def test_divergent_content_raises_integration_error(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """AC-2: Untracked file with different content raises IntegrationError.

        After issue #724, the finally block runs ``git clean -fd`` which
        removes the divergent file.  The IntegrationError still propagates
        so the orchestrator can track the failure, but the working tree is
        left clean (no orphan files blocking subsequent dispatch cycles).
        """
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")
        add_commit_to_branch(ws.path, "new_file.py", "branch content\n")

        # Place a *different* file as untracked on develop.
        untracked = tmp_worktree_repo / "new_file.py"
        original_content = "local divergent content\n"
        untracked.write_text(original_content)

        with pytest.raises(IntegrationError, match="new_file.py"):
            await harvest(tmp_worktree_repo, ws, dev_branch="develop")

        # After issue #724 the finally-block git clean removes the
        # divergent file, leaving a clean working tree.
        assert not untracked.exists(), (
            "Divergent file should be removed by finally-block git clean (issue #724)"
        )

    @pytest.mark.asyncio
    async def test_git_show_failure_preserves_file(
        self,
        tmp_worktree_repo: Path,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """AC-3: When git show returns non-zero, the untracked file is
        preserved and a WARNING is logged."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")
        add_commit_to_branch(ws.path, "new_file.py", "branch content\n")

        untracked = tmp_worktree_repo / "new_file.py"
        untracked.write_text("local content\n")

        # Selective mock: 'show' always fails; all other subcommands use real run_git.
        async def selective_run_git(cmd_args, **kwargs):
            if cmd_args and cmd_args[0] == "show":
                return 128, "", "fatal: bad revision"
            return await _real_run_git(cmd_args, **kwargs)

        with caplog.at_level(logging.WARNING, logger="agentfox.workspace.harvest"):
            with patch(
                "agentfox.workspace.harvest.run_git",
                side_effect=selective_run_git,
            ):
                # Should not raise — unverifiable means conservatively skip.
                await _clean_conflicting_untracked(tmp_worktree_repo, ws.branch)

        # File must still exist unchanged.
        assert untracked.exists()
        assert untracked.read_text() == "local content\n"

        # A WARNING-level log entry must mention the inability to verify.
        warning_messages = [r.message for r in caplog.records if r.levelno == logging.WARNING]
        assert any("Cannot verify" in msg and "new_file.py" in msg for msg in warning_messages), (
            f"Expected WARNING about 'Cannot verify new_file.py'; got: {warning_messages}"
        )

    @pytest.mark.asyncio
    async def test_warning_lists_all_files_without_truncation(
        self,
        tmp_worktree_repo: Path,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """AC-4: WARNING message contains every removed file, not just the first 5."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")

        filenames = [f"file_{i}.py" for i in range(7)]
        content = "matching content\n"
        for name in filenames:
            add_commit_to_branch(ws.path, name, content)
            (tmp_worktree_repo / name).write_text(content)

        with caplog.at_level(logging.WARNING, logger="agentfox.workspace.harvest"):
            await harvest(tmp_worktree_repo, ws, dev_branch="develop")

        warning_messages = [r.message for r in caplog.records if r.levelno == logging.WARNING]
        # Every filename must appear in at least one WARNING message.
        for name in filenames:
            assert any(name in msg for msg in warning_messages), (
                f"Expected '{name}' in WARNING messages; got: {warning_messages}"
            )


# ---------------------------------------------------------------------------
# TS-118-6, TS-118-7, TS-118-9, TS-118-17: Git stack hardening tests
# Requirements: 118-REQ-2.3, 118-REQ-3.1, 118-REQ-3.E1, 118-REQ-8.1
# ---------------------------------------------------------------------------


class TestForceCleanDuringHarvest:
    """TS-118-6: harvest with force_clean=True removes divergent files.

    AC-2: Before removing divergent files, they are backed up to
    .agent-fox/conflicts/<branch-slug>/.

    Requirements: 118-REQ-2.3
    """

    @pytest.mark.asyncio
    async def test_force_clean_harvest_removes_divergent(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """Harvest with force_clean=True removes divergent untracked files
        and proceeds with the merge without raising IntegrationError."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")
        add_commit_to_branch(ws.path, "new_file.py", "branch content\n")

        # Place a *different* file as untracked on develop (divergent).
        untracked = tmp_worktree_repo / "new_file.py"
        untracked.write_text("local divergent content\n")

        # With force_clean=True, harvest should succeed (no IntegrationError)
        files = await harvest(tmp_worktree_repo, ws, dev_branch="develop", force_clean=True)
        assert len(files) > 0

    @pytest.mark.asyncio
    async def test_force_clean_backs_up_divergent_file(
        self,
        tmp_worktree_repo: Path,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """AC-2: When force_clean=True, divergent untracked files are backed
        up to .agent-fox/conflicts/<branch-slug>/ before removal.

        Asserts (a) original path is removed from working directory,
        (b) a backup file exists under .agent-fox/conflicts/,
        (c) a WARNING log referencing the backup path is emitted.
        """
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")
        add_commit_to_branch(ws.path, "new_file.py", "branch content\n")

        # Place divergent content as untracked on develop
        untracked = tmp_worktree_repo / "new_file.py"
        divergent_content = "local divergent content\n"
        untracked.write_text(divergent_content)

        with caplog.at_level(logging.WARNING, logger="agentfox.workspace.harvest"):
            files = await harvest(tmp_worktree_repo, ws, dev_branch="develop", force_clean=True)

        # (a) A backup file exists under .agent-fox/conflicts/ — this is the key
        # check: the divergent content was preserved before the file was overwritten.
        conflicts_root = tmp_worktree_repo / ".agent-fox" / "conflicts"
        assert conflicts_root.exists(), ".agent-fox/conflicts/ should be created"
        backup_files = list(conflicts_root.rglob("new_file.py"))
        assert backup_files, "Backup of new_file.py should exist under .agent-fox/conflicts/"
        # The backup preserves the divergent content (not the branch content)
        assert backup_files[0].read_text() == divergent_content, "Backup should contain the original divergent content"

        # (b) A WARNING log references the backup path
        warning_messages = [r.message for r in caplog.records if r.levelno == logging.WARNING]
        assert any("conflicts" in msg for msg in warning_messages), (
            f"Expected WARNING mentioning backup location; got: {warning_messages}"
        )

        # Merge should have proceeded — branch content should be in working tree
        assert len(files) > 0
        # After merge, the file contains the branch content (not the divergent content)
        assert untracked.read_text() == "branch content\n", "After merge, file should contain branch content"


class TestNonRetryableErrorOnDivergent:
    """TS-118-7: _clean_conflicting_untracked raises IntegrationError(retryable=True).

    AC-1: Divergent untracked file errors must be retryable so the engine
    can retry the task rather than permanently blocking it.

    Requirements: 118-REQ-3.1
    """

    @pytest.mark.asyncio
    async def test_divergent_untracked_raises_nonretryable(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """When divergent untracked files are found, _clean_conflicting_untracked
        raises IntegrationError with retryable=True (AC-1: must be retryable so the
        engine can retry instead of permanently blocking)."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")
        add_commit_to_branch(ws.path, "new_file.py", "branch content\n")

        # Place divergent content as untracked on develop
        untracked = tmp_worktree_repo / "new_file.py"
        untracked.write_text("local divergent content\n")

        with pytest.raises(IntegrationError) as exc_info:
            await _clean_conflicting_untracked(tmp_worktree_repo, ws.branch)

        # AC-1: must be retryable=True so the orchestrator can retry
        assert exc_info.value.retryable is True


class TestMergeConflictRemainsRetryable:
    """TS-118-9: merge conflict errors remain retryable.

    Requirements: 118-REQ-3.E1
    """

    @pytest.mark.asyncio
    async def test_merge_conflict_retryable(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """Harvest failures from merge conflicts (not untracked files) produce
        retryable errors — retryable defaults to True."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")

        # Add a file on the feature branch
        add_commit_to_branch(ws.path, "shared.py", "feature content\n")

        # Add the SAME file with DIFFERENT content on develop
        subprocess.run(
            ["git", "checkout", "develop"],
            cwd=tmp_worktree_repo,
            check=True,
            capture_output=True,
        )
        add_commit_to_branch(tmp_worktree_repo, "shared.py", "develop content\n")

        # With the merge agent mocked to fail, harvest should raise retryable
        with patch(
            "agentfox.workspace.harvest.run_merge_agent",
            new_callable=AsyncMock,
            return_value=False,
        ):
            with pytest.raises(IntegrationError) as exc_info:
                await harvest(tmp_worktree_repo, ws, dev_branch="develop")
            assert exc_info.value.retryable is True


class TestHarvestErrorDiagnostics:
    """TS-118-17: harvest error messages include remediation hints.

    Requirements: 118-REQ-8.1
    """

    @pytest.mark.asyncio
    async def test_error_message_includes_remediation(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """Error message from divergent untracked file IntegrationError contains
        file path, 'git clean', and '--force-clean'."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")
        add_commit_to_branch(ws.path, "src/foo.py", "branch content\n")

        # Create a directory and divergent file
        (tmp_worktree_repo / "src").mkdir(exist_ok=True)
        (tmp_worktree_repo / "src" / "foo.py").write_text("divergent\n")

        with pytest.raises(IntegrationError) as exc_info:
            await _clean_conflicting_untracked(tmp_worktree_repo, ws.branch)

        msg = str(exc_info.value)
        assert "src/foo.py" in msg
        assert "git clean" in msg
        assert "--force-clean" in msg


class TestCleanConflictingUntrackedSymlinkEscape:
    """AC-2 (issue #579): _clean_conflicting_untracked skips symlink escapes.

    A symlink inside repo_root that points to a directory outside the repo
    must not allow deletion of files in that external directory.
    """

    @pytest.mark.asyncio
    async def test_symlink_target_not_deleted(
        self,
        tmp_path: Path,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """_clean_conflicting_untracked does not delete via symlink escape."""
        # Set up an external directory with a target file
        outside_dir = tmp_path / "outside_dir"
        outside_dir.mkdir()
        target_file = outside_dir / "target.txt"
        target_file.write_text("must survive\n")

        # Set up a minimal git repo (needs HEAD to satisfy run_git)
        repo_root = tmp_path / "repo"
        repo_root.mkdir()
        import subprocess

        subprocess.run(["git", "init"], cwd=repo_root, check=True, capture_output=True)
        subprocess.run(
            ["git", "config", "user.email", "t@t.com"],
            cwd=repo_root,
            check=True,
            capture_output=True,
        )
        subprocess.run(
            ["git", "config", "user.name", "T"],
            cwd=repo_root,
            check=True,
            capture_output=True,
        )
        (repo_root / "README.md").write_text("init\n")
        subprocess.run(
            ["git", "add", "."],
            cwd=repo_root,
            check=True,
            capture_output=True,
        )
        subprocess.run(
            ["git", "commit", "-m", "init"],
            cwd=repo_root,
            check=True,
            capture_output=True,
        )

        # Create a symlink inside repo_root pointing to outside_dir
        slink = repo_root / "slink"
        slink.symlink_to(outside_dir)

        # Mock run_git: ls-files returns the symlink-escaped path,
        # and diff returns it as an incoming file from the feature branch.
        async def mock_run_git(args, **kwargs):
            if args[0] == "ls-files":
                return (0, "slink/target.txt\n", "")
            if args[0] == "diff":
                return (0, "slink/target.txt\n", "")
            # git show — return matching content so it'd be "safe" if allowed
            if args[0] == "show":
                return (0, "must survive\n", "")
            return (0, "", "")

        with caplog.at_level(logging.WARNING, logger="agentfox.workspace.harvest"):
            with patch(
                "agentfox.workspace.harvest.run_git",
                side_effect=mock_run_git,
            ):
                await _clean_conflicting_untracked(repo_root, "feature")

        # The external file must NOT have been deleted
        assert target_file.exists(), "symlink target must not be deleted"

        # A WARNING must have been logged about skipping the path
        messages = " ".join(r.message for r in caplog.records)
        assert "outside repo root" in messages or "skipping" in messages.lower()


# ---------------------------------------------------------------------------
# AC-5: Shared-directory campaign scenarios (issue #600)
# ---------------------------------------------------------------------------


class TestSharedDirectoryCampaign:
    """AC-5: Two specs writing to the same directory must not cascade-block.

    Scenario: Spec A creates test_alpha.py and merges. Spec B (worktree
    created before Spec A merged) creates test_beta.py. An orphan untracked
    copy of test_alpha.py exists in the main working directory from Spec A's
    CWD leak, with divergent content.

    Sub-scenario 1: test_alpha.py is NOT in spec B's incoming changes
      → harvest should succeed (test_alpha.py is not a conflict).

    Sub-scenario 2: test_alpha.py IS in spec B's incoming changes (divergent)
      → harvest raises a retryable IntegrationError (.retryable=True).
    """

    @pytest.mark.asyncio
    async def test_orphan_file_not_in_incoming_harvest_succeeds(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """AC-5 sub-scenario 1: An orphan untracked file that is NOT in the
        feature branch's incoming changes does not block harvest.

        Spec B's branch only touches test_beta.py; the orphan test_alpha.py
        is not in the incoming set, so it is not a conflict.
        """
        # Create feature branch that only touches test_beta.py
        ws = await create_worktree(tmp_worktree_repo, "spec_b", 1, base_branch="develop")
        add_commit_to_branch(ws.path, "packages/foo/tests/test_beta.py", "def test_beta(): pass\n")

        # Place an orphan untracked copy of test_alpha.py in main repo
        # (simulating CWD leak from spec A). It is NOT in ws.branch's changes.
        orphan = tmp_worktree_repo / "packages" / "foo" / "tests" / "test_alpha.py"
        orphan.parent.mkdir(parents=True, exist_ok=True)
        orphan.write_text("orphan divergent content from spec A CWD leak\n")

        # Harvest should succeed: test_alpha.py is not in the incoming set
        files = await harvest(tmp_worktree_repo, ws, dev_branch="develop")
        assert "packages/foo/tests/test_beta.py" in files

    @pytest.mark.asyncio
    async def test_orphan_file_in_incoming_raises_retryable(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """AC-5 sub-scenario 2: An orphan untracked file that IS in the
        feature branch's incoming changes raises a retryable IntegrationError.

        This allows the engine to retry the task on the next cycle (e.g. after
        the workspace is cleaned) rather than permanently blocking it.
        """
        # Create feature branch that touches BOTH test_alpha.py (divergent)
        # and test_beta.py
        ws = await create_worktree(tmp_worktree_repo, "spec_b", 1, base_branch="develop")
        add_commit_to_branch(
            ws.path,
            "packages/foo/tests/test_alpha.py",
            "branch version of alpha\n",
        )
        add_commit_to_branch(
            ws.path,
            "packages/foo/tests/test_beta.py",
            "def test_beta(): pass\n",
        )

        # Place a divergent orphan copy of test_alpha.py in main repo
        orphan = tmp_worktree_repo / "packages" / "foo" / "tests" / "test_alpha.py"
        orphan.parent.mkdir(parents=True, exist_ok=True)
        orphan.write_text("orphan divergent content from spec A CWD leak\n")

        # Harvest should raise a retryable IntegrationError so the orchestrator
        # can retry (AC-1: retryable=True) rather than cascade-blocking.
        with pytest.raises(IntegrationError) as exc_info:
            await harvest(tmp_worktree_repo, ws, dev_branch="develop")

        assert exc_info.value.retryable is True, "Divergent untracked file error must be retryable=True (AC-1/AC-5)"


# ---------------------------------------------------------------------------
# Issue #724: Post-merge git clean runs on all failure paths
# ---------------------------------------------------------------------------


class TestHarvestCleanupOnFailure:
    """Issue #724: git clean runs in the finally block on all exit paths.

    AC-1: Merge agent failure → git clean runs before exception propagates.
    AC-2: _clean_conflicting_untracked raises → git clean runs in finally.
    AC-3: git commit fails → working tree is cleaned.
    AC-4: finally cleanup does not interfere with merge lock release.
    AC-5: Successful harvest path is unaffected by the try/finally refactor.
    """

    @pytest.mark.asyncio
    async def test_git_clean_runs_after_merge_agent_failure(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """AC-1: When the merge agent fails, git clean runs before the
        IntegrationError propagates, leaving no orphan untracked files."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")

        # Create a conflicting file on both branches
        add_commit_to_branch(ws.path, "shared.py", "feature content\n")

        subprocess.run(
            ["git", "checkout", "develop"],
            cwd=tmp_worktree_repo,
            check=True,
            capture_output=True,
        )
        add_commit_to_branch(tmp_worktree_repo, "shared.py", "develop content\n")

        # Also add an untracked file to the repo to verify it gets cleaned
        orphan = tmp_worktree_repo / "orphan_artifact.py"
        orphan.write_text("leftover from previous session\n")

        with patch(
            "agentfox.workspace.harvest.run_merge_agent",
            new_callable=AsyncMock,
            return_value=False,
        ):
            with pytest.raises(IntegrationError):
                await harvest(tmp_worktree_repo, ws, dev_branch="develop")

        # The orphan file should have been cleaned up by the finally block
        assert not orphan.exists(), (
            "Orphan untracked file should be removed by finally-block git clean"
        )

        # Verify no unexpected untracked files remain (excluding .agent-fox)
        result = subprocess.run(
            ["git", "ls-files", "--others", "--exclude-standard"],
            cwd=tmp_worktree_repo,
            capture_output=True,
            text=True,
            check=True,
        )
        untracked = [
            f for f in result.stdout.strip().splitlines()
            if f and not f.startswith(".agent-fox")
        ]
        assert untracked == [], f"Unexpected untracked files after failed harvest: {untracked}"

    @pytest.mark.asyncio
    async def test_git_clean_runs_when_clean_conflicting_raises(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """AC-2: When _clean_conflicting_untracked raises IntegrationError
        (divergent files), the finally block still cleans the working tree."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")
        add_commit_to_branch(ws.path, "new_file.py", "branch content\n")

        # Place a divergent file to trigger IntegrationError
        untracked = tmp_worktree_repo / "new_file.py"
        untracked.write_text("local divergent content\n")

        # Also place an unrelated orphan file
        orphan = tmp_worktree_repo / "orphan_from_prior_session.txt"
        orphan.write_text("should be cleaned\n")

        with pytest.raises(IntegrationError, match="new_file.py"):
            await harvest(tmp_worktree_repo, ws, dev_branch="develop")

        # The divergent file is preserved by _clean_conflicting_untracked's
        # own logic (it raises before deleting), but the orphan should be
        # cleaned by the finally block's git clean.
        assert not orphan.exists(), (
            "Orphan file should be removed by finally-block git clean"
        )

    @pytest.mark.asyncio
    async def test_git_clean_runs_when_commit_fails(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """AC-3: When git commit raises after a successful squash merge,
        the finally block resets the index and cleans untracked files."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")
        add_commit_to_branch(ws.path, "new_file.py", "print('hello')\n")

        # Make run_git raise on 'commit' but pass through everything else
        original_run_git = _real_run_git

        async def fail_on_commit(cmd_args, **kwargs):
            if cmd_args and cmd_args[0] == "commit":
                from agentfox.core.errors import WorkspaceError
                raise WorkspaceError("Simulated commit failure")
            return await original_run_git(cmd_args, **kwargs)

        with patch(
            "agentfox.workspace.harvest.run_git",
            side_effect=fail_on_commit,
        ):
            with pytest.raises(Exception, match="commit failure"):
                await harvest(tmp_worktree_repo, ws, dev_branch="develop")

        # After the finally block, the index should be clean (no staged changes)
        result = subprocess.run(
            ["git", "diff", "--cached", "--quiet"],
            cwd=tmp_worktree_repo,
            capture_output=True,
        )
        assert result.returncode == 0, "Index should be clean after finally-block reset"

        # No unexpected untracked files
        result = subprocess.run(
            ["git", "ls-files", "--others", "--exclude-standard"],
            cwd=tmp_worktree_repo,
            capture_output=True,
            text=True,
            check=True,
        )
        untracked = [
            f for f in result.stdout.strip().splitlines()
            if f and not f.startswith(".agent-fox")
        ]
        assert untracked == [], f"Unexpected untracked files after commit failure: {untracked}"

    @pytest.mark.asyncio
    async def test_cleanup_does_not_interfere_with_lock_release(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """AC-4: After a harvest failure with finally cleanup, the merge lock
        is released and a subsequent harvest can acquire it."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")

        # Create a conflicting file to trigger merge agent failure
        add_commit_to_branch(ws.path, "shared.py", "feature content\n")

        subprocess.run(
            ["git", "checkout", "develop"],
            cwd=tmp_worktree_repo,
            check=True,
            capture_output=True,
        )
        add_commit_to_branch(tmp_worktree_repo, "shared.py", "develop content\n")

        # First harvest fails with merge agent failure
        with patch(
            "agentfox.workspace.harvest.run_merge_agent",
            new_callable=AsyncMock,
            return_value=False,
        ):
            with pytest.raises(IntegrationError):
                await harvest(tmp_worktree_repo, ws, dev_branch="develop")

        # A second harvest call should be able to acquire the lock
        # (not deadlock or timeout). We create a new workspace to avoid
        # "no new commits" short-circuit.
        ws2 = await create_worktree(tmp_worktree_repo, "test_spec2", 2, base_branch="develop")
        add_commit_to_branch(ws2.path, "another_file.py", "content\n")

        files = await harvest(tmp_worktree_repo, ws2, dev_branch="develop")
        assert "another_file.py" in files, "Lock should be released after failed harvest"

    @pytest.mark.asyncio
    async def test_successful_harvest_unaffected_by_refactor(
        self,
        tmp_worktree_repo: Path,
    ) -> None:
        """AC-5: The try/finally refactor does not change behavior on success.
        Working tree is clean and changed files are returned."""
        ws = await create_worktree(tmp_worktree_repo, "test_spec", 1, base_branch="develop")
        add_commit_to_branch(ws.path, "new_file.py", "print('hello')\n")

        files = await harvest(tmp_worktree_repo, ws, dev_branch="develop")

        assert "new_file.py" in files

        # Working tree should be clean (excluding .agent-fox/ which is
        # protected by --exclude .agent-fox in git clean)
        result = subprocess.run(
            ["git", "status", "--porcelain"],
            cwd=tmp_worktree_repo,
            capture_output=True,
            text=True,
            check=True,
        )
        dirty = [
            line for line in result.stdout.strip().splitlines()
            if line and not line.endswith(".agent-fox/")
        ]
        assert dirty == [], f"Working tree should be clean after successful harvest, got: {dirty}"
