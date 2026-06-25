"""Tests for worktree path collision fix: role/mode-aware path derivation.

Covers PRD tests 1-7 (TS-09-18 through TS-09-24), property tests
TS-09-P1 through TS-09-P5, edge-case tests TS-09-E1 through TS-09-E4,
and smoke tests TS-09-SMOKE-1 through TS-09-SMOKE-4.

Requirements: 09-REQ-1 through 09-REQ-8
"""

from __future__ import annotations

import asyncio
import dataclasses
import logging
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from agentfox.workspace.worktree import (
    WorkspaceInfo,
    _cleanup_empty_ancestors,
    create_worktree,
    destroy_worktree,
)

# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def repo_root(tmp_path: Path) -> Path:
    """Provide a tmp_path-based repo_root with .agent-fox/worktrees/ structure.

    No real git repository — used with stubbed git CLI only.
    """
    worktrees_root = tmp_path / ".agent-fox" / "worktrees"
    worktrees_root.mkdir(parents=True)
    return tmp_path


@pytest.fixture
def _stub_git(monkeypatch: pytest.MonkeyPatch) -> None:
    """Stub all git CLI subprocess calls so no real git commands execute.

    Patches run_git, create_branch, delete_branch, and branch_used_by_worktree
    in the worktree module to no-op AsyncMocks.
    """
    monkeypatch.setattr(
        "agentfox.workspace.worktree.run_git",
        AsyncMock(return_value=(0, "", "")),
    )
    monkeypatch.setattr(
        "agentfox.workspace.worktree.create_branch",
        AsyncMock(),
    )
    monkeypatch.setattr(
        "agentfox.workspace.worktree.delete_branch",
        AsyncMock(),
    )
    monkeypatch.setattr(
        "agentfox.workspace.worktree.branch_used_by_worktree",
        AsyncMock(return_value=False),
    )


# ---------------------------------------------------------------------------
# PRD Test 1 (TS-09-18): 2-level path without role or mode
# Requirement: 09-REQ-8.1
# ---------------------------------------------------------------------------


class TestPathWithoutMode:
    """TS-09-18, 09-REQ-8.1: create_worktree() without role/mode produces 2-level path."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_path_without_mode(self, repo_root: Path) -> None:
        """PRD test 1: 2-level path and feature/{spec}/{task_group} branch."""
        result = await create_worktree(
            repo_root, "my_spec", 7, base_branch="main",
        )
        expected_path = repo_root / ".agent-fox" / "worktrees" / "my_spec" / "7"
        assert result.path == expected_path
        assert result.branch == "feature/my_spec/7"


# ---------------------------------------------------------------------------
# PRD Test 2 (TS-09-19): 4-level path with role and mode
# Requirement: 09-REQ-8.2
# ---------------------------------------------------------------------------


class TestPathWithRoleAndMode:
    """TS-09-19, 09-REQ-8.2: create_worktree() with role+mode produces 4-level path."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_path_with_role_and_mode(self, repo_root: Path) -> None:
        """PRD test 2: 4-level path for role='reviewer', mode='drift-review'."""
        result = await create_worktree(
            repo_root,
            "08_spec_generation_improvement",
            0,
            base_branch="main",
            role="reviewer",
            mode="drift-review",
        )
        expected_path = (
            repo_root
            / ".agent-fox"
            / "worktrees"
            / "08_spec_generation_improvement"
            / "0"
            / "reviewer"
            / "drift-review"
        )
        assert result.path == expected_path
        assert result.branch == "feature/08_spec_generation_improvement/0/reviewer/drift-review"


# ---------------------------------------------------------------------------
# PRD Test 3 (TS-09-20): Coder node regression — no mode → 2-level path
# Requirement: 09-REQ-8.3
# ---------------------------------------------------------------------------


class TestCoderRegression:
    """TS-09-20, 09-REQ-8.3: Coder node without mode produces pre-fix 2-level path."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_coder_regression(self, repo_root: Path) -> None:
        """PRD test 3: role='coder', mode=None → 2-level path, 'coder' not in path."""
        result = await create_worktree(
            repo_root,
            "coder_spec",
            2,
            base_branch="main",
            role="coder",
            mode=None,
        )
        expected_path = repo_root / ".agent-fox" / "worktrees" / "coder_spec" / "2"
        assert result.path == expected_path
        assert result.branch == "feature/coder_spec/2"
        # Confirm no extra role/mode segments
        assert "coder" not in str(result.path)


# ---------------------------------------------------------------------------
# PRD Test 4 (TS-09-21): Concurrent dispatch — distinct paths (CI blocking)
# Requirement: 09-REQ-8.4
#
# This test MUST be a blocking required check in the PR pytest pipeline
# per 09-REQ-8.4. It is marked with @pytest.mark.ci_required to signal
# to CI configuration that this test must block merge on failure.
# ---------------------------------------------------------------------------


class TestConcurrentDistinctPaths:
    """TS-09-21, 09-REQ-8.4: Concurrent create_worktree() with different modes."""

    @pytest.mark.asyncio
    @pytest.mark.ci_required
    @pytest.mark.usefixtures("_stub_git")
    async def test_concurrent_distinct_paths(self, repo_root: Path) -> None:
        """PRD test 4: two concurrent calls produce non-equal, non-colliding paths.

        This test is a CI blocking required check per 09-REQ-8.4.
        """
        result1, result2 = await asyncio.gather(
            create_worktree(
                repo_root,
                "08_spec_generation_improvement",
                0,
                base_branch="main",
                role="reviewer",
                mode="pre-review",
            ),
            create_worktree(
                repo_root,
                "08_spec_generation_improvement",
                0,
                base_branch="main",
                role="reviewer",
                mode="drift-review",
            ),
        )
        assert result1.path != result2.path
        assert result1.branch != result2.branch
        assert "pre-review" in str(result1.path)
        assert "drift-review" in str(result2.path)


# ---------------------------------------------------------------------------
# PRD Test 5 (TS-09-22): Empty-string role/mode treated as None
# Requirement: 09-REQ-8.5
# ---------------------------------------------------------------------------


class TestEmptyStringTreatedAsNone:
    """TS-09-22, 09-REQ-8.5: role='' and mode='' normalised to None → 2-level path."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_empty_string_treated_as_none(self, repo_root: Path) -> None:
        """PRD test 5: empty strings → 2-level path identical to all-None."""
        result = await create_worktree(
            repo_root,
            "test_spec",
            0,
            base_branch="main",
            role="",
            mode="",
        )
        expected_path = repo_root / ".agent-fox" / "worktrees" / "test_spec" / "0"
        assert result.path == expected_path
        assert result.branch == "feature/test_spec/0"
        assert result.role is None
        assert result.mode is None


# ---------------------------------------------------------------------------
# PRD Test 6 (TS-09-23): role present, mode absent → 2-level, no WARNING
# Requirement: 09-REQ-8.6
# ---------------------------------------------------------------------------


class TestRolePresentModeAbsentSilent:
    """TS-09-23, 09-REQ-8.6: role='reviewer', mode=None → 2-level path, no WARNING."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_role_present_mode_absent_silent(
        self, repo_root: Path, caplog: pytest.LogCaptureFixture
    ) -> None:
        """PRD test 6: role present but mode absent → silent fallback to 2-level."""
        with caplog.at_level(logging.WARNING):
            result = await create_worktree(
                repo_root,
                "test_spec",
                0,
                base_branch="main",
                role="reviewer",
                mode=None,
            )
        expected_path = repo_root / ".agent-fox" / "worktrees" / "test_spec" / "0"
        assert result.path == expected_path
        assert result.branch == "feature/test_spec/0"
        # No WARNING-level log should be emitted
        warning_records = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_records) == 0, f"Unexpected WARNING logs: {warning_records}"


# ---------------------------------------------------------------------------
# PRD Test 7 (TS-09-24): mode present, role absent → WARNING + 'unknown'
# Requirement: 09-REQ-8.7
# ---------------------------------------------------------------------------


class TestModePresentRoleAbsentWarning:
    """TS-09-24, 09-REQ-8.7: role=None, mode='drift-review' → WARNING + 'unknown'."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_mode_present_role_absent_warning(
        self, repo_root: Path, caplog: pytest.LogCaptureFixture
    ) -> None:
        """PRD test 7: mode set, role absent → 'unknown' as role segment + WARNING."""
        with caplog.at_level(logging.WARNING):
            result = await create_worktree(
                repo_root,
                "08_spec_generation_improvement",
                0,
                base_branch="main",
                role=None,
                mode="drift-review",
            )
        expected_path = (
            repo_root
            / ".agent-fox"
            / "worktrees"
            / "08_spec_generation_improvement"
            / "0"
            / "unknown"
            / "drift-review"
        )
        assert result.path == expected_path
        assert "unknown" in result.branch
        assert result.role == "unknown"
        assert result.mode == "drift-review"
        # At least one WARNING containing mode, spec_name, and task_group
        warning_records = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_records) >= 1, "Expected at least one WARNING log"
        warning_text = warning_records[0].message
        assert "drift-review" in warning_text
        assert "08_spec_generation_improvement" in warning_text
        assert "0" in warning_text


# ---------------------------------------------------------------------------
# TS-09-1: create_worktree() accepts role and mode kwargs
# Requirement: 09-REQ-1.1
# ---------------------------------------------------------------------------


class TestCreateWorktreeSignature:
    """TS-09-1: create_worktree() callable with role and mode kwargs."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_accepts_role_and_mode_kwargs(self, repo_root: Path) -> None:
        """create_worktree() is callable with role and mode and returns WorkspaceInfo."""
        result = await create_worktree(
            repo_root,
            "test_spec",
            0,
            base_branch="main",
            role="reviewer",
            mode="pre-review",
        )
        assert result is not None
        assert isinstance(result, WorkspaceInfo)


# ---------------------------------------------------------------------------
# TS-09-2: Empty-string normalisation
# Requirement: 09-REQ-1.2
# ---------------------------------------------------------------------------


class TestEmptyStringNormalisation:
    """TS-09-2: Empty-string role/mode normalised to None."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_empty_strings_normalised_to_none(self, repo_root: Path) -> None:
        """role='' and mode='' produce WorkspaceInfo with role=None, mode=None."""
        result = await create_worktree(
            repo_root,
            "test_spec",
            1,
            base_branch="main",
            role="",
            mode="",
        )
        assert result.role is None
        assert result.mode is None
        expected_path = repo_root / ".agent-fox" / "worktrees" / "test_spec" / "1"
        assert result.path == expected_path


# ---------------------------------------------------------------------------
# TS-09-3: WorkspaceInfo.role and .mode reflect normalised inputs
# Requirement: 09-REQ-1.3
# ---------------------------------------------------------------------------


class TestWorkspaceInfoRoleModeFields:
    """TS-09-3: WorkspaceInfo.role and .mode reflect normalised effective values."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_role_and_mode_set_on_workspace_info(self, repo_root: Path) -> None:
        """WorkspaceInfo carries normalised role and mode."""
        result = await create_worktree(
            repo_root,
            "test_spec",
            2,
            base_branch="main",
            role="reviewer",
            mode="drift-review",
        )
        assert result.role == "reviewer"
        assert result.mode == "drift-review"


# ---------------------------------------------------------------------------
# TS-09-4: effective_mode is None → 2-level path
# Requirement: 09-REQ-2.1
# ---------------------------------------------------------------------------


class TestModeNone2LevelPath:
    """TS-09-4: effective_mode=None → 2-level path and branch."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_mode_none_produces_2level_path(self, repo_root: Path) -> None:
        """role=None, mode=None → worktrees_root/spec/task_group."""
        result = await create_worktree(
            repo_root, "my_spec", 3, base_branch="main", role=None, mode=None,
        )
        assert result.path == repo_root / ".agent-fox" / "worktrees" / "my_spec" / "3"
        assert result.branch == "feature/my_spec/3"


# ---------------------------------------------------------------------------
# TS-09-5: Both effective_role and effective_mode set → 4-level path
# Requirement: 09-REQ-2.2
# ---------------------------------------------------------------------------


class TestBothRoleAndMode4LevelPath:
    """TS-09-5: Both role and mode set → 4-level path and branch."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_both_set_produces_4level_path(self, repo_root: Path) -> None:
        """role='reviewer', mode='drift-review' → 4-level path."""
        result = await create_worktree(
            repo_root,
            "08_spec_generation_improvement",
            0,
            base_branch="main",
            role="reviewer",
            mode="drift-review",
        )
        expected = (
            repo_root / ".agent-fox" / "worktrees"
            / "08_spec_generation_improvement" / "0" / "reviewer" / "drift-review"
        )
        assert result.path == expected
        assert result.branch == "feature/08_spec_generation_improvement/0/reviewer/drift-review"


# ---------------------------------------------------------------------------
# TS-09-6: role set, mode=None → silent fallback to 2-level
# Requirement: 09-REQ-2.3
# ---------------------------------------------------------------------------


class TestRoleSetModeNoneSilentFallback:
    """TS-09-6: role set, mode=None → 2-level path, no WARNING."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_role_set_mode_none_silent_fallback(
        self, repo_root: Path, caplog: pytest.LogCaptureFixture
    ) -> None:
        """role='reviewer', mode=None → 2-level path without WARNING."""
        with caplog.at_level(logging.WARNING):
            result = await create_worktree(
                repo_root, "test_spec", 0, base_branch="main",
                role="reviewer", mode=None,
            )
        assert result.path == repo_root / ".agent-fox" / "worktrees" / "test_spec" / "0"
        assert result.branch == "feature/test_spec/0"
        assert not [r for r in caplog.records if r.levelno >= logging.WARNING]


# ---------------------------------------------------------------------------
# TS-09-7: mode set, role=None → WARNING + 'unknown' role
# Requirement: 09-REQ-2.4
# ---------------------------------------------------------------------------


class TestModeSetRoleNoneWarning:
    """TS-09-7: mode set, role=None → WARNING + 'unknown' substitution."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_mode_set_role_none_warning_and_unknown(
        self, repo_root: Path, caplog: pytest.LogCaptureFixture
    ) -> None:
        """role=None, mode='drift-review' → 'unknown' role segment + WARNING."""
        with caplog.at_level(logging.WARNING):
            result = await create_worktree(
                repo_root, "08_spec_generation_improvement", 0,
                base_branch="main", role=None, mode="drift-review",
            )
        expected = (
            repo_root / ".agent-fox" / "worktrees"
            / "08_spec_generation_improvement" / "0" / "unknown" / "drift-review"
        )
        assert result.path == expected
        assert result.branch == "feature/08_spec_generation_improvement/0/unknown/drift-review"
        assert result.role == "unknown"
        warning_records = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_records) >= 1
        msg = warning_records[0].message
        assert "drift-review" in msg
        assert "08_spec_generation_improvement" in msg
        assert "0" in msg


# ---------------------------------------------------------------------------
# TS-09-8: WorkspaceInfo new fields — frozen dataclass with role and mode
# Requirement: 09-REQ-3.1
# ---------------------------------------------------------------------------


class TestWorkspaceInfoNewFields:
    """TS-09-8: WorkspaceInfo declares role and mode as optional frozen fields."""

    def test_workspace_info_new_fields(self) -> None:
        """WorkspaceInfo can be constructed with role and mode; is frozen."""
        info = WorkspaceInfo(
            path=Path("/tmp/worktrees/spec/0"),
            branch="feature/spec/0",
            spec_name="spec",
            task_group=0,
            role="reviewer",
            mode="pre-review",
        )
        assert info.role == "reviewer"
        assert info.mode == "pre-review"
        # Frozen check
        with pytest.raises(dataclasses.FrozenInstanceError):
            info.role = "other"  # type: ignore[misc]


# ---------------------------------------------------------------------------
# TS-09-9: WorkspaceInfo backward compatibility
# Requirement: 09-REQ-3.2
# ---------------------------------------------------------------------------


class TestWorkspaceInfoBackwardCompat:
    """TS-09-9: WorkspaceInfo without role/mode defaults both to None."""

    def test_workspace_info_backward_compat(self) -> None:
        """Existing callers that omit role and mode still work."""
        info = WorkspaceInfo(
            path=Path("/tmp/worktrees/spec/0"),
            branch="feature/spec/0",
            spec_name="spec",
            task_group=0,
        )
        assert info.role is None
        assert info.mode is None


# ---------------------------------------------------------------------------
# TS-09-10: WorkspaceInfo no serialization methods
# Requirement: 09-REQ-3.3
# ---------------------------------------------------------------------------


class TestWorkspaceInfoNoSerialization:
    """TS-09-10: WorkspaceInfo is in-memory only — no persistence methods."""

    def test_workspace_info_no_serialization(self) -> None:
        """WorkspaceInfo has no to_json, serialize, or to_dict methods."""
        assert dataclasses.is_dataclass(WorkspaceInfo)
        assert not hasattr(WorkspaceInfo, "to_json")
        assert not hasattr(WorkspaceInfo, "serialize")
        assert not hasattr(WorkspaceInfo, "to_dict")


# ---------------------------------------------------------------------------
# TS-09-11: NodeSessionRunner._setup_workspace() passes role and mode
# Requirement: 09-REQ-4.1
# ---------------------------------------------------------------------------


class TestSetupWorkspacePassesRoleMode:
    """TS-09-11: _setup_workspace() forwards archetype and mode to create_worktree()."""

    @pytest.mark.asyncio
    async def test_setup_workspace_passes_role_mode(self, tmp_path: Path) -> None:
        """create_worktree() is called with role=self._archetype and mode=self._mode."""
        from agentfox.core.config import AgentFoxConfig
        from agentfox.engine.session_lifecycle import NodeSessionRunner
        from agentfox.knowledge.db import KnowledgeDB

        mock_kb = MagicMock(spec=KnowledgeDB)
        config = AgentFoxConfig()
        runner = NodeSessionRunner(
            "test_spec:0",
            config,
            archetype="reviewer",
            mode="pre-review",
            knowledge_db=mock_kb,
        )

        fake_workspace = WorkspaceInfo(
            path=tmp_path / "ws",
            branch="feature/test_spec/0",
            spec_name="test_spec",
            task_group=0,
        )
        mock_create = AsyncMock(return_value=fake_workspace)

        with (
            patch(
                "agentfox.engine.session_lifecycle.create_worktree",
                mock_create,
            ),
            patch(
                "agentfox.engine.session_lifecycle.ensure_integration_branch",
                AsyncMock(),
            ),
        ):
            await runner._setup_workspace(tmp_path, "test_spec:0")

        call_kwargs = mock_create.call_args.kwargs
        assert call_kwargs["role"] == "reviewer"
        assert call_kwargs["mode"] == "pre-review"


# ---------------------------------------------------------------------------
# TS-09-12: _setup_workspace() passes empty-string mode as-is
# Requirement: 09-REQ-4.2
# ---------------------------------------------------------------------------


class TestSetupWorkspacePassesEmptyStringModeAsIs:
    """TS-09-12: _setup_workspace() passes self._mode='' without pre-normalisation."""

    @pytest.mark.asyncio
    async def test_setup_workspace_passes_empty_string_mode_as_is(self, tmp_path: Path) -> None:
        """create_worktree() receives mode='' at the call site, not mode=None."""
        from agentfox.core.config import AgentFoxConfig
        from agentfox.engine.session_lifecycle import NodeSessionRunner
        from agentfox.knowledge.db import KnowledgeDB

        mock_kb = MagicMock(spec=KnowledgeDB)
        config = AgentFoxConfig()
        runner = NodeSessionRunner(
            "test_spec:0",
            config,
            archetype="coder",
            mode="",
            knowledge_db=mock_kb,
        )

        fake_workspace = WorkspaceInfo(
            path=tmp_path / "ws",
            branch="feature/test_spec/0",
            spec_name="test_spec",
            task_group=0,
        )
        mock_create = AsyncMock(return_value=fake_workspace)

        with (
            patch(
                "agentfox.engine.session_lifecycle.create_worktree",
                mock_create,
            ),
            patch(
                "agentfox.engine.session_lifecycle.ensure_integration_branch",
                AsyncMock(),
            ),
        ):
            await runner._setup_workspace(tmp_path, "test_spec:0")

        call_kwargs = mock_create.call_args.kwargs
        # Passed as-is; normalisation happens inside create_worktree, not here
        assert call_kwargs["mode"] == ""


# ---------------------------------------------------------------------------
# TS-09-13: Coder backward compatibility — pre-fix 2-level path
# Requirement: 09-REQ-5.1
# ---------------------------------------------------------------------------


class TestCoderBackwardCompat:
    """TS-09-13: Without role/mode, path and branch identical to pre-fix."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_coder_backward_compat(self, repo_root: Path) -> None:
        """Calling without role/mode → same 2-level path as pre-fix."""
        result = await create_worktree(
            repo_root, "coder_spec", 5, base_branch="main",
        )
        assert result.path == repo_root / ".agent-fox" / "worktrees" / "coder_spec" / "5"
        assert result.branch == "feature/coder_spec/5"
        assert result.role is None
        assert result.mode is None


# ---------------------------------------------------------------------------
# TS-09-14: No additional branch-name sanitization beyond spec_name validation
# Requirement: 09-REQ-5.2
# ---------------------------------------------------------------------------


class TestNoAdditionalSanitization:
    """TS-09-14: No extra sanitization beyond existing spec_name regex."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_no_additional_sanitization(self, repo_root: Path) -> None:
        """Valid spec_name with role/mode: branch preserves segments verbatim."""
        result = await create_worktree(
            repo_root,
            "valid-spec_name",
            0,
            base_branch="main",
            role="reviewer",
            mode="drift-review",
        )
        assert result.branch == "feature/valid-spec_name/0/reviewer/drift-review"

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_invalid_spec_name_still_raises(self, repo_root: Path) -> None:
        """Invalid spec_name raises validation error regardless of role/mode."""
        from agentfox.core.errors import WorkspaceError

        with pytest.raises(WorkspaceError, match="Invalid spec name"):
            await create_worktree(
                repo_root, "invalid spec!", 0, base_branch="main",
            )


# ---------------------------------------------------------------------------
# TS-09-15: destroy_worktree uses workspace.path directly
# Requirement: 09-REQ-6.1
# ---------------------------------------------------------------------------


class TestDestroyWorktreeUsesFullPath:
    """TS-09-15: destroy_worktree uses the exact path from WorkspaceInfo."""

    @pytest.mark.asyncio
    async def test_destroy_worktree_uses_full_path(self, tmp_path: Path) -> None:
        """git worktree remove is called with the exact 4-level path."""
        four_level_path = (
            tmp_path / ".agent-fox" / "worktrees" / "spec" / "0"
            / "reviewer" / "drift-review"
        )
        four_level_path.mkdir(parents=True)

        workspace = WorkspaceInfo(
            path=four_level_path,
            branch="feature/spec/0/reviewer/drift-review",
            spec_name="spec",
            task_group=0,
            role="reviewer",
            mode="drift-review",
        )

        git_commands: list[list[str]] = []

        async def capture_run_git(args: list[str], **kwargs: object) -> tuple[int, str, str]:
            git_commands.append(args)
            return (0, "", "")

        with (
            patch("agentfox.workspace.worktree.run_git", side_effect=capture_run_git),
            patch("agentfox.workspace.worktree.delete_branch", AsyncMock()),
            patch("agentfox.workspace.worktree.branch_used_by_worktree", AsyncMock(return_value=False)),
            patch("agentfox.workspace.worktree._cleanup_empty_ancestors"),
        ):
            await destroy_worktree(tmp_path, workspace)

        # Check that git worktree remove --force was called with the 4-level path
        remove_calls = [
            c for c in git_commands
            if len(c) >= 3 and c[0] == "worktree" and c[1] == "remove"
        ]
        assert len(remove_calls) >= 1
        assert str(four_level_path) in remove_calls[0]


# ---------------------------------------------------------------------------
# TS-09-16: _cleanup_empty_ancestors handles 4-level depth
# Requirement: 09-REQ-6.2
# ---------------------------------------------------------------------------


class TestCleanupEmptyAncestors4Level:
    """TS-09-16: _cleanup_empty_ancestors walks up from 4-level leaf to worktrees_root."""

    def test_cleanup_empty_ancestors_4level(self, tmp_path: Path) -> None:
        """All empty intermediate dirs removed; worktrees_root remains."""
        worktrees_root = tmp_path / ".agent-fox" / "worktrees"
        leaf = worktrees_root / "spec" / "0" / "reviewer" / "drift-review"
        leaf.mkdir(parents=True)

        _cleanup_empty_ancestors(leaf, worktrees_root)

        assert not leaf.exists()
        assert not (worktrees_root / "spec" / "0" / "reviewer").exists()
        assert not (worktrees_root / "spec" / "0").exists()
        assert not (worktrees_root / "spec").exists()
        assert worktrees_root.exists()


# ---------------------------------------------------------------------------
# TS-09-17: Stale worktree cleanup before creation (4-level path)
# Requirement: 09-REQ-7.1
# ---------------------------------------------------------------------------


class TestStaleWorktreeCleanupBeforeAdd:
    """TS-09-17: Pre-existing 4-level path triggers 'git worktree remove --force'."""

    @pytest.mark.asyncio
    async def test_stale_worktree_cleanup_before_add(self, repo_root: Path) -> None:
        """'git worktree remove --force' precedes 'git worktree add' for stale path."""
        stale_path = (
            repo_root / ".agent-fox" / "worktrees" / "spec" / "0"
            / "reviewer" / "drift-review"
        )
        stale_path.mkdir(parents=True)

        git_commands: list[list[str]] = []

        async def capture_run_git(args: list[str], **kwargs: object) -> tuple[int, str, str]:
            git_commands.append(list(args))
            # Simulate successful removal so the path no longer exists
            if args[:2] == ["worktree", "remove"]:
                import shutil
                path_str = args[-1]
                target = Path(path_str)
                if target.exists():
                    shutil.rmtree(target)
            return (0, "", "")

        with (
            patch("agentfox.workspace.worktree.run_git", side_effect=capture_run_git),
            patch("agentfox.workspace.worktree.create_branch", AsyncMock()),
            patch("agentfox.workspace.worktree.delete_branch", AsyncMock()),
            patch("agentfox.workspace.worktree.branch_used_by_worktree", AsyncMock(return_value=False)),
        ):
            await create_worktree(
                repo_root, "spec", 0, base_branch="main",
                role="reviewer", mode="drift-review",
            )

        remove_calls = [
            c for c in git_commands
            if len(c) >= 3 and c[0] == "worktree" and c[1] == "remove" and "--force" in c
        ]
        add_calls = [
            c for c in git_commands
            if len(c) >= 3 and c[0] == "worktree" and c[1] == "add"
        ]
        assert len(remove_calls) >= 1, "Expected 'git worktree remove --force' call"
        assert len(add_calls) >= 1, "Expected 'git worktree add' call"
        # Remove must come before add
        first_remove_idx = git_commands.index(remove_calls[0])
        first_add_idx = git_commands.index(add_calls[0])
        assert first_remove_idx < first_add_idx


# ===========================================================================
# Edge-case tests
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-09-E1: Empty string for role/mode is None, not literal empty segment
# Requirement: 09-REQ-1.E1
# ---------------------------------------------------------------------------


class TestEdgeCaseEmptyStringNotSegment:
    """TS-09-E1: Empty strings normalised to None — no empty path segment."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_edge_empty_string_not_segment(self, repo_root: Path) -> None:
        """role='' and mode='' → None; no empty segment in path."""
        result = await create_worktree(
            repo_root, "test_spec", 0, base_branch="main", role="", mode="",
        )
        assert result.role is None
        assert result.mode is None
        assert "" not in result.path.parts  # no empty path segment
        assert result.path == repo_root / ".agent-fox" / "worktrees" / "test_spec" / "0"


# ---------------------------------------------------------------------------
# TS-09-E2: Both empty strings → identical to all-None (2-level)
# Requirement: 09-REQ-2.E1
# ---------------------------------------------------------------------------


class TestEdgeCaseBothEmptyIdenticalToNone:
    """TS-09-E2: role='', mode='' → same result as role=None, mode=None."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_edge_both_empty_identical_to_none(self, repo_root: Path) -> None:
        """Empty-string pair produces identical path and branch to None pair."""
        result_empty = await create_worktree(
            repo_root, "test_spec", 0, base_branch="main", role="", mode="",
        )
        result_none = await create_worktree(
            repo_root, "test_spec", 0, base_branch="main", role=None, mode=None,
        )
        assert result_empty.path == result_none.path
        assert result_empty.branch == result_none.branch
        assert result_empty.path == repo_root / ".agent-fox" / "worktrees" / "test_spec" / "0"


# ---------------------------------------------------------------------------
# TS-09-E3: Concurrent calls with different modes → distinct paths
# Requirement: 09-REQ-2.E2
# ---------------------------------------------------------------------------


class TestEdgeCaseConcurrentDistinctModes:
    """TS-09-E3: Concurrent calls with different (role, mode) pairs → distinct paths."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_edge_concurrent_distinct_modes(self, repo_root: Path) -> None:
        """Two concurrent calls with different modes produce non-colliding paths."""
        r1, r2 = await asyncio.gather(
            create_worktree(
                repo_root, "08_spec_generation_improvement", 0,
                base_branch="main", role="reviewer", mode="pre-review",
            ),
            create_worktree(
                repo_root, "08_spec_generation_improvement", 0,
                base_branch="main", role="reviewer", mode="drift-review",
            ),
        )
        assert r1.path != r2.path
        assert r1.branch != r2.branch
        assert "pre-review" in str(r1.path)
        assert "drift-review" in str(r2.path)


# ---------------------------------------------------------------------------
# TS-09-E4: NodeSessionRunner._mode='' → passes as-is → 2-level path
# Requirement: 09-REQ-4.E1
# ---------------------------------------------------------------------------


class TestEdgeCaseEmptyStringModeSentinel:
    """TS-09-E4: Empty-string sentinel on NodeSessionRunner produces 2-level path."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_empty_string_mode_sentinel(self, repo_root: Path) -> None:
        """mode='' normalised to None inside create_worktree → 2-level path."""
        result = await create_worktree(
            repo_root, "test_spec", 0, base_branch="main",
            role="coder", mode="",
        )
        assert result.mode is None
        assert result.path == repo_root / ".agent-fox" / "worktrees" / "test_spec" / "0"


# ===========================================================================
# Property tests
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-09-P1: Distinct (role, mode) pairs produce distinct paths
# Validates: 09-REQ-2.2, 09-REQ-2.E2
# ---------------------------------------------------------------------------


class TestPropertyDistinctPairsDistinctPaths:
    """TS-09-P1: Different (role, mode) pairs → different paths and branches."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    @pytest.mark.parametrize(
        "role1,mode1,role2,mode2",
        [
            ("reviewer", "pre-review", "reviewer", "drift-review"),
            ("coder", "fast", "coder", "thorough"),
            ("reviewer", "pre-review", "coder", "pre-review"),
            ("analyst", "mode-a", "analyst", "mode-b"),
        ],
    )
    async def test_property_distinct_pairs_distinct_paths(
        self,
        repo_root: Path,
        role1: str,
        mode1: str,
        role2: str,
        mode2: str,
    ) -> None:
        """Parametrized: distinct (role, mode) → distinct path and branch."""
        r1 = await create_worktree(
            repo_root, "spec", 0, base_branch="main", role=role1, mode=mode1,
        )
        r2 = await create_worktree(
            repo_root, "spec", 0, base_branch="main", role=role2, mode=mode2,
        )
        assert r1.path != r2.path
        assert r1.branch != r2.branch


# ---------------------------------------------------------------------------
# TS-09-P2: None/empty combos all produce 2-level path
# Validates: 09-REQ-1.2, 09-REQ-1.E1, 09-REQ-2.E1
# ---------------------------------------------------------------------------


class TestPropertyEmptyNoneCombos2Level:
    """TS-09-P2: All None/'' combos produce the same 2-level path."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    @pytest.mark.parametrize(
        "role,mode",
        [
            (None, None),
            ("", None),
            (None, ""),
            ("", ""),
        ],
    )
    async def test_property_empty_none_combos_2level(
        self, repo_root: Path, role: str | None, mode: str | None,
    ) -> None:
        """All None/'' combos → 2-level path with role=None, mode=None."""
        result = await create_worktree(
            repo_root, "spec", 0, base_branch="main", role=role, mode=mode,
        )
        assert result.path == repo_root / ".agent-fox" / "worktrees" / "spec" / "0"
        assert result.branch == "feature/spec/0"
        assert result.role is None
        assert result.mode is None


# ---------------------------------------------------------------------------
# TS-09-P3: Any role with mode=None/'' → 2-level path
# Validates: 09-REQ-2.1, 09-REQ-2.3, 09-REQ-5.1
# ---------------------------------------------------------------------------


class TestPropertyAnyRoleModeAbsent2Level:
    """TS-09-P3: Any role with mode absent → always 2-level path."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    @pytest.mark.parametrize(
        "role,mode",
        [
            (None, None),
            ("", None),
            ("coder", None),
            ("reviewer", None),
            ("some-random-role", None),
            (None, ""),
            ("", ""),
            ("coder", ""),
            ("reviewer", ""),
        ],
    )
    async def test_property_any_role_mode_absent_2level(
        self, repo_root: Path, role: str | None, mode: str | None,
    ) -> None:
        """Arbitrary role with mode=None or '' → 2-level path."""
        result = await create_worktree(
            repo_root, "spec", 1, base_branch="main", role=role, mode=mode,
        )
        assert result.path == repo_root / ".agent-fox" / "worktrees" / "spec" / "1"
        assert result.branch == "feature/spec/1"


# ---------------------------------------------------------------------------
# TS-09-P4: WorkspaceInfo.role and .mode reflect normalised values
# Validates: 09-REQ-1.3, 09-REQ-3.1
# ---------------------------------------------------------------------------


class TestPropertyNormalisedRoleModeFields:
    """TS-09-P4: WorkspaceInfo fields always match normalised effective values."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    @pytest.mark.parametrize(
        "role,mode,expected_role,expected_mode",
        [
            (None, None, None, None),
            ("", "", None, None),
            ("reviewer", "drift-review", "reviewer", "drift-review"),
            (None, "drift-review", "unknown", "drift-review"),
            ("reviewer", None, None, None),
            ("", "drift-review", "unknown", "drift-review"),
        ],
    )
    async def test_property_normalised_role_mode_fields(
        self,
        repo_root: Path,
        role: str | None,
        mode: str | None,
        expected_role: str | None,
        expected_mode: str | None,
    ) -> None:
        """WorkspaceInfo.role and .mode match normalised expectations."""
        result = await create_worktree(
            repo_root, "spec", 0, base_branch="main", role=role, mode=mode,
        )
        assert result.role == expected_role
        assert result.mode == expected_mode


# ---------------------------------------------------------------------------
# TS-09-P5: WorkspaceInfo backward compatibility — defaults to None
# Validates: 09-REQ-3.2
# ---------------------------------------------------------------------------


class TestPropertyWorkspaceInfoDefaultsNone:
    """TS-09-P5: WorkspaceInfo without role/mode always defaults to None."""

    @pytest.mark.parametrize(
        "spec_name,task_group",
        [
            ("spec_a", 0),
            ("spec_b", 1),
            ("spec-c", 99),
        ],
    )
    def test_property_workspace_info_defaults_none(
        self, spec_name: str, task_group: int,
    ) -> None:
        """WorkspaceInfo constructed without role/mode → both None."""
        info = WorkspaceInfo(
            path=Path(f"/tmp/worktrees/{spec_name}/{task_group}"),
            branch=f"feature/{spec_name}/{task_group}",
            spec_name=spec_name,
            task_group=task_group,
        )
        assert info.role is None
        assert info.mode is None


# ===========================================================================
# Smoke tests
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-09-SMOKE-1: Concurrent reviewer node dispatch — distinct paths
# Execution path: 09-PATH-1
# ---------------------------------------------------------------------------


class TestSmokeConcurrentReviewerDispatch:
    """SMOKE-1: Two reviewer nodes with different modes → distinct 4-level paths."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_smoke_concurrent_reviewer_dispatch(self, repo_root: Path) -> None:
        """Two concurrent create_worktree calls succeed with distinct paths."""
        result1, result2 = await asyncio.gather(
            create_worktree(
                repo_root, "spec", 0, base_branch="main",
                role="reviewer", mode="pre-review",
            ),
            create_worktree(
                repo_root, "spec", 0, base_branch="main",
                role="reviewer", mode="drift-review",
            ),
        )
        assert result1.path != result2.path
        assert result1.branch != result2.branch
        assert "reviewer/pre-review" in str(result1.path)
        assert "reviewer/drift-review" in str(result2.path)
        # No exit-code-128 raised — implicit by not raising


# ---------------------------------------------------------------------------
# TS-09-SMOKE-2: Coder node — 2-level path (backward compat)
# Execution path: 09-PATH-2
# ---------------------------------------------------------------------------


class TestSmokeCoderNode2Level:
    """SMOKE-2: Coder node with no mode → 2-level path, no role/mode segments."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_smoke_coder_node_2level(self, repo_root: Path) -> None:
        """Coder node call produces 2-level path with no role/mode segments."""
        result = await create_worktree(
            repo_root, "spec", 0, base_branch="main",
            role="coder", mode=None,
        )
        assert result.path == repo_root / ".agent-fox" / "worktrees" / "spec" / "0"
        assert result.branch == "feature/spec/0"
        assert result.role is None
        assert result.mode is None
        # No 'reviewer' or 'coder' segment in path
        path_str = str(result.path)
        assert "reviewer" not in path_str
        assert "coder" not in path_str


# ---------------------------------------------------------------------------
# TS-09-SMOKE-3: Mode-set, role-absent → WARNING + 'unknown'
# Execution path: 09-PATH-3
# ---------------------------------------------------------------------------


class TestSmokeModeSetRoleAbsentFallback:
    """SMOKE-3: mode='drift-review', role=None → WARNING + 'unknown' role segment."""

    @pytest.mark.asyncio
    @pytest.mark.usefixtures("_stub_git")
    async def test_smoke_mode_set_role_absent_fallback(
        self, repo_root: Path, caplog: pytest.LogCaptureFixture,
    ) -> None:
        """WARNING emitted and 'unknown' used as role segment."""
        with caplog.at_level(logging.WARNING):
            result = await create_worktree(
                repo_root, "spec", 0, base_branch="main",
                role=None, mode="drift-review",
            )
        assert "unknown/drift-review" in str(result.path)
        assert result.role == "unknown"
        assert result.mode == "drift-review"
        # WARNING emitted
        warning_records = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_records) >= 1
        assert "drift-review" in warning_records[0].message


# ---------------------------------------------------------------------------
# TS-09-SMOKE-4: Stale worktree pre-creation cleanup at 4-level path
# Execution path: 09-PATH-4
# ---------------------------------------------------------------------------


class TestSmokeStaleWorktreeCleanup4Level:
    """SMOKE-4: Pre-existing 4-level dir → remove --force before add."""

    @pytest.mark.asyncio
    async def test_smoke_stale_worktree_cleanup_4level(self, repo_root: Path) -> None:
        """Stale 4-level directory triggers git worktree remove --force before add."""
        stale_path = (
            repo_root / ".agent-fox" / "worktrees" / "spec" / "0"
            / "reviewer" / "drift-review"
        )
        stale_path.mkdir(parents=True)

        git_commands: list[list[str]] = []

        async def capture_run_git(args: list[str], **kwargs: object) -> tuple[int, str, str]:
            git_commands.append(list(args))
            if args[:2] == ["worktree", "remove"]:
                import shutil
                path_str = args[-1]
                target = Path(path_str)
                if target.exists():
                    shutil.rmtree(target)
            return (0, "", "")

        with (
            patch("agentfox.workspace.worktree.run_git", side_effect=capture_run_git),
            patch("agentfox.workspace.worktree.create_branch", AsyncMock()),
            patch("agentfox.workspace.worktree.delete_branch", AsyncMock()),
            patch("agentfox.workspace.worktree.branch_used_by_worktree", AsyncMock(return_value=False)),
        ):
            result = await create_worktree(
                repo_root, "spec", 0, base_branch="main",
                role="reviewer", mode="drift-review",
            )

        # git worktree remove --force issued before git worktree add
        remove_calls = [c for c in git_commands if c[:2] == ["worktree", "remove"]]
        add_calls = [c for c in git_commands if c[:2] == ["worktree", "add"]]
        assert len(remove_calls) >= 1
        assert len(add_calls) >= 1
        assert git_commands.index(remove_calls[0]) < git_commands.index(add_calls[0])
        assert result.path == stale_path
