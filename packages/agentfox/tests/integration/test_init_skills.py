"""Integration tests for skills symlink / migration behaviour (spec 709).

Requirements: 47-REQ-2.2, 709-AC-3, 709-AC-4, 709-AC-5
"""

from __future__ import annotations

from pathlib import Path

from af.app import main
from click.testing import CliRunner

# ---------------------------------------------------------------------------
# TS-47-2: No skills without flag
# ---------------------------------------------------------------------------


class TestNoSkillsWithoutFlag:
    """47-REQ-2.2: af init does not create skill files (skills come from external repo)."""

    def test_no_skills_on_init(self, cli_runner: CliRunner, tmp_git_repo: Path) -> None:
        """af init does not create .agents/skills/ or a .claude/skills symlink."""
        result = cli_runner.invoke(main, ["init"])

        assert result.exit_code == 0
        skills_dir = tmp_git_repo / ".agents" / "skills"
        assert not skills_dir.exists() or len(list(skills_dir.iterdir())) == 0
        assert not (tmp_git_repo / ".claude" / "skills").is_symlink()


# ---------------------------------------------------------------------------
# 709: CLAUDE.md symlink on init
# ---------------------------------------------------------------------------


class TestClaudeMdSymlinkOnInit:
    """709-AC-4: CLAUDE.md symlink is created by af init."""

    def test_claude_md_symlink_on_fresh_init(self, cli_runner: CliRunner, tmp_git_repo: Path) -> None:
        """Fresh af init creates CLAUDE.md as a symlink to AGENTS.md."""
        result = cli_runner.invoke(main, ["init"])

        assert result.exit_code == 0
        claude_md = tmp_git_repo / "CLAUDE.md"
        assert claude_md.is_symlink()
        assert claude_md.read_text(encoding="utf-8") == (tmp_git_repo / "AGENTS.md").read_text(encoding="utf-8")

    def test_claude_md_symlink_survives_reinit(self, cli_runner: CliRunner, tmp_git_repo: Path) -> None:
        """CLAUDE.md symlink is preserved on re-init."""
        cli_runner.invoke(main, ["init"])
        cli_runner.invoke(main, ["init"])

        assert (tmp_git_repo / "CLAUDE.md").is_symlink()


# ---------------------------------------------------------------------------
# 709-AC-3: Migration from old-style .claude/skills/ directory
# ---------------------------------------------------------------------------


class TestSkillsMigrationOnReinit:
    """709-AC-3: Old .claude/skills/ directory is migrated on re-init."""

    def test_old_skills_migrated(self, cli_runner: CliRunner, tmp_git_repo: Path) -> None:
        """Pre-existing .claude/skills/ dir is migrated to .agents/skills/ on af init."""
        # Simulate old-style install by creating .claude/skills/ as a real dir
        old_skills = tmp_git_repo / ".claude" / "skills" / "af-custom"
        old_skills.mkdir(parents=True)
        (old_skills / "SKILL.md").write_text("custom skill content")

        result = cli_runner.invoke(main, ["init"])

        assert result.exit_code == 0
        # Custom skill migrated
        assert (tmp_git_repo / ".agents" / "skills" / "af-custom" / "SKILL.md").read_text() == "custom skill content"
        # .claude/skills is now a symlink
        assert (tmp_git_repo / ".claude" / "skills").is_symlink()
