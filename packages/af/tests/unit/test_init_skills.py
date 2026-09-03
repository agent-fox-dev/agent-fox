"""Unit tests for the skills symlink helper (spec 709).

Requirements: 709-AC-2, 709-AC-3, 709-AC-6
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import patch

# ---------------------------------------------------------------------------
# 709-AC-2: Skills symlink created
# ---------------------------------------------------------------------------


class TestSkillsSymlinkCreated:
    """709-AC-2: .claude/skills is a symlink to ../.agents/skills."""

    def test_symlink_created_when_agents_skills_exists(self, tmp_path: Path) -> None:
        """_ensure_skills_symlink creates .claude/skills symlink when .agents/skills/ exists."""
        from agentfox.workspace.init_project import _ensure_skills_symlink

        project_root = tmp_path / "project"
        project_root.mkdir()
        (project_root / ".claude").mkdir()
        (project_root / ".agents" / "skills").mkdir(parents=True)

        _ensure_skills_symlink(project_root)

        claude_skills = project_root / ".claude" / "skills"
        assert claude_skills.is_symlink()
        assert claude_skills.resolve() == (project_root / ".agents" / "skills").resolve()

    def test_skills_accessible_via_symlink(self, tmp_path: Path) -> None:
        """Skills placed in .agents/skills/ are accessible via .claude/skills/ symlink."""
        from agentfox.workspace.init_project import _ensure_skills_symlink

        project_root = tmp_path / "project"
        project_root.mkdir()
        (project_root / ".claude").mkdir()

        skill_dir = project_root / ".agents" / "skills" / "af-test"
        skill_dir.mkdir(parents=True)
        (skill_dir / "SKILL.md").write_text("---\nname: af-test\n---\nContent")

        _ensure_skills_symlink(project_root)

        assert (project_root / ".claude" / "skills" / "af-test" / "SKILL.md").exists()

    def test_symlink_idempotent(self, tmp_path: Path) -> None:
        """Calling _ensure_skills_symlink twice does not error."""
        from agentfox.workspace.init_project import _ensure_skills_symlink

        project_root = tmp_path / "project"
        project_root.mkdir()
        (project_root / ".claude").mkdir()
        (project_root / ".agents" / "skills").mkdir(parents=True)

        _ensure_skills_symlink(project_root)
        _ensure_skills_symlink(project_root)

        assert (project_root / ".claude" / "skills").is_symlink()

    def test_no_symlink_without_agents_skills(self, tmp_path: Path) -> None:
        """No symlink created when .agents/skills/ does not exist."""
        from agentfox.workspace.init_project import _ensure_skills_symlink

        project_root = tmp_path / "project"
        project_root.mkdir()

        _ensure_skills_symlink(project_root)

        assert not (project_root / ".claude" / "skills").exists()


# ---------------------------------------------------------------------------
# 709-AC-3: Migration from .claude/skills/ directory
# ---------------------------------------------------------------------------


class TestSkillsMigration:
    """709-AC-3: Existing .claude/skills/ directory is migrated to .agents/skills/."""

    def test_migration_moves_contents(self, tmp_path: Path) -> None:
        """Existing skills in .claude/skills/ are moved to .agents/skills/."""
        from agentfox.workspace.init_project import _ensure_skills_symlink

        project_root = tmp_path / "project"
        project_root.mkdir()

        # Simulate an old-style install
        old_skills = project_root / ".claude" / "skills"
        old_skills.mkdir(parents=True)
        skill_dir = old_skills / "af-old"
        skill_dir.mkdir()
        (skill_dir / "SKILL.md").write_text("old content")

        _ensure_skills_symlink(project_root)

        # Content migrated
        assert (project_root / ".agents" / "skills" / "af-old" / "SKILL.md").exists()
        assert (project_root / ".agents" / "skills" / "af-old" / "SKILL.md").read_text() == "old content"
        # .claude/skills is now a symlink
        assert (project_root / ".claude" / "skills").is_symlink()

    def test_migration_skips_existing_at_destination(self, tmp_path: Path) -> None:
        """Migration does not overwrite existing files at .agents/skills/."""
        from agentfox.workspace.init_project import _ensure_skills_symlink

        project_root = tmp_path / "project"
        project_root.mkdir()

        # Existing in .agents/skills/
        new_skills = project_root / ".agents" / "skills" / "af-shared"
        new_skills.mkdir(parents=True)
        (new_skills / "SKILL.md").write_text("new content")

        # Old copy in .claude/skills/
        old_skills = project_root / ".claude" / "skills" / "af-shared"
        old_skills.mkdir(parents=True)
        (old_skills / "SKILL.md").write_text("old content")

        _ensure_skills_symlink(project_root)

        # New content preserved, not overwritten by old
        assert (project_root / ".agents" / "skills" / "af-shared" / "SKILL.md").read_text() == "new content"


# ---------------------------------------------------------------------------
# 709-AC-6: Symlink failure is a warning, not an error
# ---------------------------------------------------------------------------


class TestSkillsSymlinkFailure:
    """709-AC-6: Symlink creation failure is logged as a warning."""

    def test_symlink_failure_does_not_raise(self, tmp_path: Path) -> None:
        """_ensure_skills_symlink logs a warning on OSError, does not crash."""
        from agentfox.workspace.init_project import _ensure_skills_symlink

        project_root = tmp_path / "project"
        project_root.mkdir()
        (project_root / ".agents" / "skills").mkdir(parents=True)
        (project_root / ".claude").mkdir()

        with patch.object(Path, "symlink_to", side_effect=OSError("no symlinks")):
            _ensure_skills_symlink(project_root)

        assert not (project_root / ".claude" / "skills").exists()
