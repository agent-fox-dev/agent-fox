"""Unit tests for skill installation (Spec 47).

Tests the _install_skills() function in isolation.

Requirements: 47-REQ-1.E1, 47-REQ-2.E1, 47-REQ-2.E2
Test Spec: TS-47-E1, TS-47-E2, TS-47-E3
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import patch

import pytest

# ---------------------------------------------------------------------------
# TS-47-E1: Unreadable template skipped
# ---------------------------------------------------------------------------


class TestUnreadableTemplateSkipped:
    """TS-47-E1: An unreadable template file is skipped with a warning."""

    def test_unreadable_template_skipped(self, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
        """47-REQ-1.E1: Unreadable template is skipped; valid ones installed."""
        from agentfox.workspace.init_project import _install_skills

        # Create a fake _SKILLS_DIR with one valid and one unreadable file
        fake_skills = tmp_path / "fake_skills"
        fake_skills.mkdir()

        valid = fake_skills / "af-valid"
        valid.write_text("---\nname: af-valid\ndescription: Valid.\n---\nContent")

        unreadable = fake_skills / "af-broken"
        unreadable.write_text("content")
        unreadable.chmod(0o000)

        import agentfox.workspace.init_project as init_mod

        monkeypatch.setattr(init_mod, "_SKILLS_DIR", fake_skills)

        project_root = tmp_path / "project"
        project_root.mkdir()

        count = _install_skills(project_root)

        # Valid skill installed, broken one skipped
        assert count == 1
        assert (project_root / ".agents" / "skills" / "af-valid" / "SKILL.md").exists()
        assert not (project_root / ".agents" / "skills" / "af-broken" / "SKILL.md").exists()

        # Cleanup permissions so tmp_path can be removed
        unreadable.chmod(0o644)

    def test_unreadable_count_excludes_skipped(self, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
        """47-REQ-1.E1: Return count excludes skipped skills."""
        from agentfox.workspace.init_project import _install_skills

        fake_skills = tmp_path / "fake_skills"
        fake_skills.mkdir()

        # Two valid, one unreadable
        (fake_skills / "af-one").write_text("---\nname: af-one\ndescription: One.\n---\nContent")
        (fake_skills / "af-two").write_text("---\nname: af-two\ndescription: Two.\n---\nContent")
        broken = fake_skills / "af-broken"
        broken.write_text("content")
        broken.chmod(0o000)

        import agentfox.workspace.init_project as init_mod

        monkeypatch.setattr(init_mod, "_SKILLS_DIR", fake_skills)

        project_root = tmp_path / "project"
        project_root.mkdir()

        count = _install_skills(project_root)
        assert count == 2

        broken.chmod(0o644)


# ---------------------------------------------------------------------------
# TS-47-E3: Permission error creating skills directory
# ---------------------------------------------------------------------------


class TestPermissionErrorHandled:
    """TS-47-E3: Permission error creating .agents/skills/ is handled."""

    def test_permission_error_returns_zero(self, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
        """47-REQ-2.E2: Cannot create .agents/skills/ returns 0, no crash."""
        from agentfox.workspace.init_project import _install_skills

        # Set up a valid skills dir with one template
        fake_skills = tmp_path / "fake_skills"
        fake_skills.mkdir()
        (fake_skills / "af-test").write_text("---\nname: af-test\ndescription: Test.\n---\nContent")

        import agentfox.workspace.init_project as init_mod

        monkeypatch.setattr(init_mod, "_SKILLS_DIR", fake_skills)

        project_root = tmp_path / "project"
        project_root.mkdir()

        # Make .agents read-only so skills/ can't be created
        agents_dir = project_root / ".agents"
        agents_dir.mkdir()
        agents_dir.chmod(0o444)

        count = _install_skills(project_root)
        assert count == 0

        # Cleanup permissions
        agents_dir.chmod(0o755)


# ---------------------------------------------------------------------------
# 709-AC-2: Skills symlink created
# ---------------------------------------------------------------------------


class TestSkillsSymlinkCreated:
    """709-AC-2: .claude/skills is a symlink to ../.agents/skills after install."""

    def test_symlink_created_after_install(self, tmp_path: Path) -> None:
        """After _install_skills + _ensure_skills_symlink, .claude/skills is a symlink."""
        from agentfox.workspace.init_project import _ensure_skills_symlink, _install_skills

        project_root = tmp_path / "project"
        project_root.mkdir()
        (project_root / ".claude").mkdir()

        _install_skills(project_root)
        _ensure_skills_symlink(project_root)

        claude_skills = project_root / ".claude" / "skills"
        assert claude_skills.is_symlink()
        assert claude_skills.resolve() == (project_root / ".agents" / "skills").resolve()

    def test_skills_accessible_via_symlink(self, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
        """Skills installed to .agents/skills/ are accessible via .claude/skills/ symlink."""
        from agentfox.workspace.init_project import _ensure_skills_symlink, _install_skills

        fake_skills = tmp_path / "fake_skills"
        fake_skills.mkdir()
        (fake_skills / "af-test").write_text("---\nname: af-test\ndescription: Test.\n---\nContent")

        import agentfox.workspace.init_project as init_mod

        monkeypatch.setattr(init_mod, "_SKILLS_DIR", fake_skills)

        project_root = tmp_path / "project"
        project_root.mkdir()
        (project_root / ".claude").mkdir()

        _install_skills(project_root)
        _ensure_skills_symlink(project_root)

        assert (project_root / ".claude" / "skills" / "af-test" / "SKILL.md").exists()

    def test_symlink_idempotent(self, tmp_path: Path) -> None:
        """Calling _ensure_skills_symlink twice does not error."""
        from agentfox.workspace.init_project import _ensure_skills_symlink, _install_skills

        project_root = tmp_path / "project"
        project_root.mkdir()
        (project_root / ".claude").mkdir()

        _install_skills(project_root)
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
