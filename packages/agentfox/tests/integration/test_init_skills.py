"""Integration tests for skill installation via init --skills (Spec 47).

Requirements: 47-REQ-2.1, 47-REQ-2.2, 47-REQ-2.4, 47-REQ-2.5,
              47-REQ-3.1, 47-REQ-3.2, 47-REQ-4.1, 47-REQ-4.2
Test Spec: TS-47-1 through TS-47-7
"""

from __future__ import annotations

from pathlib import Path

import agentfox
from af.app import main
from click.testing import CliRunner

# Path to bundled skill templates (for verification)
_SKILLS_DIR = Path(agentfox.__file__).parent / "_templates" / "skills"


def _bundled_skill_names() -> set[str]:
    """Return the set of bundled skill template names."""
    return {f.name for f in _SKILLS_DIR.iterdir() if f.is_file() and not f.name.startswith(".")}


# ---------------------------------------------------------------------------
# TS-47-1: Skills installed to correct paths
# ---------------------------------------------------------------------------


class TestSkillsInstalledToCorrectPaths:
    """TS-47-1: init --skills creates SKILL.md files at correct paths.

    Requirements: 47-REQ-2.1, 47-REQ-4.1
    """

    def test_skills_installed_to_correct_paths(self, cli_runner: CliRunner, tmp_git_repo: Path) -> None:
        """Each bundled skill produces .agents/skills/{name}/SKILL.md."""
        result = cli_runner.invoke(main, ["init", "--skills"])

        assert result.exit_code == 0
        for name in _bundled_skill_names():
            skill_path = tmp_git_repo / ".agents" / "skills" / name / "SKILL.md"
            assert skill_path.exists(), f"Missing skill: {name}"

    def test_claude_skills_symlink_created(self, cli_runner: CliRunner, tmp_git_repo: Path) -> None:
        """709-AC-2: .claude/skills is a symlink after init --skills."""
        cli_runner.invoke(main, ["init", "--skills"])

        claude_skills = tmp_git_repo / ".claude" / "skills"
        assert claude_skills.is_symlink()
        for name in _bundled_skill_names():
            assert (claude_skills / name / "SKILL.md").exists(), (
                f"Skill {name} not accessible via .claude/skills symlink"
            )


# ---------------------------------------------------------------------------
# TS-47-2: No skills without flag
# ---------------------------------------------------------------------------


class TestNoSkillsWithoutFlag:
    """TS-47-2: init without --skills does not create skill files.

    Requirement: 47-REQ-2.2
    """

    def test_no_skills_without_flag(self, cli_runner: CliRunner, tmp_git_repo: Path) -> None:
        """No .agents/skills/ directory created without --skills."""
        result = cli_runner.invoke(main, ["init"])

        assert result.exit_code == 0
        skills_dir = tmp_git_repo / ".agents" / "skills"
        assert not skills_dir.exists() or len(list(skills_dir.iterdir())) == 0
        assert not (tmp_git_repo / ".claude" / "skills").is_symlink()


# ---------------------------------------------------------------------------
# TS-47-3: Skills overwrite on re-run
# ---------------------------------------------------------------------------


class TestSkillsOverwriteOnRerun:
    """TS-47-3: Re-running init --skills overwrites existing skill files.

    Requirement: 47-REQ-2.4
    """

    def test_skills_overwrite_on_rerun(self, cli_runner: CliRunner, tmp_git_repo: Path) -> None:
        """Modified skill file is overwritten with bundled version."""
        # First install
        cli_runner.invoke(main, ["init", "--skills"])

        # Pick the first skill and modify it
        first_name = sorted(_bundled_skill_names())[0]
        skill_path = tmp_git_repo / ".agents" / "skills" / first_name / "SKILL.md"
        skill_path.write_text("modified content")

        # Re-install
        cli_runner.invoke(main, ["init", "--skills"])

        # Should be overwritten with bundled content (after template substitution)
        bundled_content = (_SKILLS_DIR / first_name).read_text()
        # Template variables are substituted at install time (371-REQ-3.1)
        from agentfox.core.config import AgentFoxConfig

        expected = bundled_content.replace("{{SPEC_ROOT}}", AgentFoxConfig().paths.spec_root)
        assert skill_path.read_text() == expected
        assert skill_path.read_text() != "modified content"


# ---------------------------------------------------------------------------
# TS-47-4: Output reports skill count
# ---------------------------------------------------------------------------


class TestOutputReportsSkillCount:
    """TS-47-4: Human-readable output mentions number of skills installed.

    Requirement: 47-REQ-2.5
    """

    def test_output_reports_skill_count(self, cli_runner: CliRunner, tmp_git_repo: Path) -> None:
        """Output contains 'installed' and the skill count number."""
        result = cli_runner.invoke(main, ["init", "--skills"])

        expected_count = len(_bundled_skill_names())
        assert "installed" in result.output.lower()
        assert str(expected_count) in result.output


# ---------------------------------------------------------------------------
# TS-47-7: Skills work on re-init
# ---------------------------------------------------------------------------


class TestSkillsWorkOnReinit:
    """TS-47-7: --skills works on re-init of already-initialized project.

    Requirement: 47-REQ-4.2
    """

    def test_skills_work_on_reinit(self, cli_runner: CliRunner, tmp_git_repo: Path) -> None:
        """Re-init with --skills installs skills and reports already initialized."""
        # First init without skills
        cli_runner.invoke(main, ["init"])

        # Re-init with skills
        result = cli_runner.invoke(main, ["init", "--skills"])

        assert result.exit_code == 0
        assert (tmp_git_repo / ".agents" / "skills" / "af-spec" / "SKILL.md").exists()
        assert (tmp_git_repo / ".claude" / "skills").is_symlink()


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
        """Pre-existing .claude/skills/ dir is migrated to .agents/skills/."""
        # Simulate old-style install by creating .claude/skills/ as a real dir
        old_skills = tmp_git_repo / ".claude" / "skills" / "af-custom"
        old_skills.mkdir(parents=True)
        (old_skills / "SKILL.md").write_text("custom skill content")

        result = cli_runner.invoke(main, ["init", "--skills"])

        assert result.exit_code == 0
        # Custom skill migrated
        assert (tmp_git_repo / ".agents" / "skills" / "af-custom" / "SKILL.md").read_text() == "custom skill content"
        # .claude/skills is now a symlink
        assert (tmp_git_repo / ".claude" / "skills").is_symlink()
