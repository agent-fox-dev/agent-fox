"""Unit tests for AGENTS.md initialization (Spec 44).

Requirements: 44-REQ-1.1, 44-REQ-1.2, 44-REQ-1.3, 44-REQ-1.E1,
              44-REQ-2.1, 44-REQ-2.2, 44-REQ-2.3,
              44-REQ-3.1, 44-REQ-3.2, 44-REQ-3.3, 44-REQ-3.E1,
              44-REQ-4.1, 44-REQ-4.2, 44-REQ-5.1
"""

from __future__ import annotations

import os
import subprocess
from pathlib import Path

import agentfox
import pytest
from agentfox.workspace.init_project import _ensure_agents_md, _ensure_claude_md_symlink
from click.testing import CliRunner

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

_TEMPLATES_DIR = Path(agentfox.__file__).parent / "_templates"
_AGENTS_MD_TEMPLATE = _TEMPLATES_DIR / "agents_md.md"


# ---------------------------------------------------------------------------
# TS-44-1: Template file exists in package
# ---------------------------------------------------------------------------


class TestTemplateBundled:
    """TS-44-1: Bundled template file exists and is non-empty."""

    def test_template_file_exists(self) -> None:
        """44-REQ-1.1: Template exists at expected package path."""
        assert _AGENTS_MD_TEMPLATE.exists(), f"Template not found at {_AGENTS_MD_TEMPLATE}"

    def test_template_is_nonempty(self) -> None:
        """44-REQ-1.1: Template is non-empty."""
        assert _AGENTS_MD_TEMPLATE.stat().st_size > 0


# ---------------------------------------------------------------------------
# TS-44-2: Template is valid UTF-8
# ---------------------------------------------------------------------------


class TestTemplateEncoding:
    """TS-44-2: Template can be read as UTF-8."""

    def test_template_is_valid_utf8(self) -> None:
        """44-REQ-1.2: Template is valid UTF-8 markdown."""
        content = _AGENTS_MD_TEMPLATE.read_text(encoding="utf-8")
        assert len(content) > 0


# ---------------------------------------------------------------------------
# TS-44-3: Template contains placeholder markers
# ---------------------------------------------------------------------------


class TestTemplatePlaceholders:
    """TS-44-3: Template contains angle-bracket placeholder markers."""

    def test_template_contains_placeholders(self) -> None:
        """44-REQ-1.3: Template has at least one placeholder marker."""
        content = _AGENTS_MD_TEMPLATE.read_text(encoding="utf-8")
        assert "<main_package>" in content or "<test_directory>" in content, (
            "Template must contain at least one placeholder marker"
        )


# ---------------------------------------------------------------------------
# TS-44-4: Creates AGENTS.md when absent
# ---------------------------------------------------------------------------


class TestCreatesAgentsMd:
    """TS-44-4: _ensure_agents_md creates the file when absent."""

    def test_creates_agents_md_when_absent(self, tmp_path: Path) -> None:
        """44-REQ-2.1: Creates AGENTS.md with template content."""
        _ensure_agents_md(tmp_path)

        agents_md = tmp_path / "AGENTS.md"
        assert agents_md.exists()
        template_content = _AGENTS_MD_TEMPLATE.read_text(encoding="utf-8")
        # 371-REQ-3.1: Template variables are substituted at creation time.
        # Resolve spec_root the same way _ensure_agents_md does.
        from agentfox.core.config import load_config

        config_path = tmp_path / ".agent-fox" / "config.toml"
        expected = template_content.replace(
            "{{SPEC_ROOT}}",
            load_config(config_path if config_path.exists() else None).paths.spec_root,
        )
        assert agents_md.read_text(encoding="utf-8") == expected

    def test_creates_returns_created(self, tmp_path: Path) -> None:
        """44-REQ-2.3: Returns 'created' when file is written."""
        result = _ensure_agents_md(tmp_path)
        assert result == "created"


# ---------------------------------------------------------------------------
# TS-44-7: Skips when AGENTS.md exists
# ---------------------------------------------------------------------------


class TestSkipsExistingAgentsMd:
    """TS-44-7: _ensure_agents_md does not overwrite an existing file."""

    def test_skips_when_agents_md_exists(self, tmp_path: Path) -> None:
        """44-REQ-3.1: Existing file content is preserved."""
        agents_md = tmp_path / "AGENTS.md"
        agents_md.write_text("custom content", encoding="utf-8")

        result = _ensure_agents_md(tmp_path)

        assert agents_md.read_text(encoding="utf-8") == "custom content"
        assert result == "skipped"

    def test_skips_returns_skipped(self, tmp_path: Path) -> None:
        """44-REQ-3.3: Returns 'skipped' when file already exists."""
        (tmp_path / "AGENTS.md").write_text("custom content", encoding="utf-8")

        result = _ensure_agents_md(tmp_path)

        assert result == "skipped"


# ---------------------------------------------------------------------------
# TS-44-10: Created regardless of CLAUDE.md presence
# ---------------------------------------------------------------------------


class TestClaudeMdIndependence:
    """TS-44-10, TS-44-11: AGENTS.md creation is independent of CLAUDE.md."""

    def test_created_regardless_of_claude_md(self, tmp_path: Path) -> None:
        """44-REQ-4.1: AGENTS.md is created even when CLAUDE.md exists."""
        (tmp_path / "CLAUDE.md").write_text("# Instructions", encoding="utf-8")

        result = _ensure_agents_md(tmp_path)

        assert (tmp_path / "AGENTS.md").exists()
        assert result == "created"

    def test_created_when_claude_md_absent(self, tmp_path: Path) -> None:
        """44-REQ-4.2: AGENTS.md is created when CLAUDE.md does not exist."""
        result = _ensure_agents_md(tmp_path)

        assert (tmp_path / "AGENTS.md").exists()
        assert result == "created"


# ---------------------------------------------------------------------------
# TS-44-E1: Missing template file raises FileNotFoundError
# ---------------------------------------------------------------------------


class TestMissingTemplate:
    """TS-44-E1: Clear error when template is missing."""

    def test_missing_template_raises_error(self, tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
        """44-REQ-1.E1: FileNotFoundError raised when template is missing."""
        import agentfox.workspace.init_project as init_mod

        monkeypatch.setattr(
            init_mod,
            "_AGENTS_MD_TEMPLATE",
            Path("/nonexistent/path/agents_md.md"),
        )

        with pytest.raises(FileNotFoundError):
            _ensure_agents_md(tmp_path)


# ---------------------------------------------------------------------------
# TS-44-E2: Empty existing AGENTS.md is not overwritten
# ---------------------------------------------------------------------------


class TestEmptyAgentsMd:
    """TS-44-E2: Empty AGENTS.md is not overwritten (existence check only)."""

    def test_empty_agents_md_not_overwritten(self, tmp_path: Path) -> None:
        """44-REQ-3.E1: Empty file is preserved — existence check, not content."""
        agents_md = tmp_path / "AGENTS.md"
        agents_md.write_text("", encoding="utf-8")

        result = _ensure_agents_md(tmp_path)

        assert agents_md.read_text(encoding="utf-8") == ""
        assert result == "skipped"


# ---------------------------------------------------------------------------
# 709-AC-4: CLAUDE.md symlink created when AGENTS.md is created
# ---------------------------------------------------------------------------


class TestClaudeMdSymlinkCreated:
    """709-AC-4: CLAUDE.md -> AGENTS.md symlink is created."""

    def test_symlink_created(self, tmp_path: Path) -> None:
        """CLAUDE.md is a symlink to AGENTS.md after _ensure_claude_md_symlink."""
        _ensure_agents_md(tmp_path)
        _ensure_claude_md_symlink(tmp_path)

        claude_md = tmp_path / "CLAUDE.md"
        assert claude_md.is_symlink()
        assert os.readlink(str(claude_md)) == "AGENTS.md"

    def test_symlink_content_matches(self, tmp_path: Path) -> None:
        """Reading CLAUDE.md via symlink yields same content as AGENTS.md."""
        _ensure_agents_md(tmp_path)
        _ensure_claude_md_symlink(tmp_path)

        agents_content = (tmp_path / "AGENTS.md").read_text(encoding="utf-8")
        claude_content = (tmp_path / "CLAUDE.md").read_text(encoding="utf-8")
        assert claude_content == agents_content

    def test_symlink_idempotent(self, tmp_path: Path) -> None:
        """Calling _ensure_claude_md_symlink twice does not error."""
        _ensure_agents_md(tmp_path)
        _ensure_claude_md_symlink(tmp_path)
        _ensure_claude_md_symlink(tmp_path)

        assert (tmp_path / "CLAUDE.md").is_symlink()


# ---------------------------------------------------------------------------
# 709-AC-5: Existing CLAUDE.md file is not overwritten
# ---------------------------------------------------------------------------


class TestClaudeMdExistingFileUntouched:
    """709-AC-5: Existing CLAUDE.md regular file is left untouched."""

    def test_existing_file_preserved(self, tmp_path: Path) -> None:
        """CLAUDE.md as a regular file is not replaced with a symlink."""
        (tmp_path / "CLAUDE.md").write_text("custom instructions", encoding="utf-8")
        _ensure_agents_md(tmp_path)
        _ensure_claude_md_symlink(tmp_path)

        claude_md = tmp_path / "CLAUDE.md"
        assert not claude_md.is_symlink()
        assert claude_md.read_text(encoding="utf-8") == "custom instructions"

    def test_existing_symlink_preserved(self, tmp_path: Path) -> None:
        """CLAUDE.md as an existing symlink is left as-is."""
        _ensure_agents_md(tmp_path)
        claude_md = tmp_path / "CLAUDE.md"
        claude_md.symlink_to(Path("AGENTS.md"))
        _ensure_claude_md_symlink(tmp_path)

        assert claude_md.is_symlink()


# ---------------------------------------------------------------------------
# 709: No CLAUDE.md symlink when AGENTS.md absent
# ---------------------------------------------------------------------------


class TestClaudeMdSymlinkNoAgentsMd:
    """No CLAUDE.md symlink when AGENTS.md does not exist."""

    def test_no_symlink_without_agents_md(self, tmp_path: Path) -> None:
        """CLAUDE.md is not created when AGENTS.md does not exist."""
        _ensure_claude_md_symlink(tmp_path)

        assert not (tmp_path / "CLAUDE.md").exists()


# ---------------------------------------------------------------------------
# Integration: TS-44-5 Display creation message
# ---------------------------------------------------------------------------


class TestInitDisplaysMessage:
    """TS-44-5: init_cmd displays 'Created AGENTS.md.' when file is created."""

    def test_init_creates_agents_md_message(self, cli_runner: CliRunner, tmp_git_repo: Path) -> None:
        """44-REQ-2.2: stdout contains 'Created AGENTS.md.' on fresh init."""
        from af.app import main

        result = cli_runner.invoke(main, ["init"])

        assert result.exit_code == 0
        assert "Created AGENTS.md." in result.output


# ---------------------------------------------------------------------------
# ---------------------------------------------------------------------------
# Integration: TS-44-8 Silent skip — no message on re-init
# ---------------------------------------------------------------------------


class TestInitSilentSkip:
    """TS-44-8: No AGENTS.md message on re-init when file already exists."""

    def test_init_silent_skip(self, cli_runner: CliRunner, tmp_git_repo: Path) -> None:
        """44-REQ-3.2: stdout does not mention AGENTS.md on skip."""
        from af.app import main

        (tmp_git_repo / "AGENTS.md").write_text("existing", encoding="utf-8")

        result = cli_runner.invoke(main, ["init"])

        assert result.exit_code == 0
        assert "AGENTS.md" not in result.output


# ---------------------------------------------------------------------------
# Integration: TS-44-12 AGENTS.md not added to .gitignore
# ---------------------------------------------------------------------------


class TestAgentsMdNotInGitignore:
    """TS-44-12: init does not add AGENTS.md to .gitignore."""

    def test_agents_md_not_in_gitignore(self, cli_runner: CliRunner, tmp_git_repo: Path) -> None:
        """44-REQ-5.1: .gitignore does not contain AGENTS.md."""
        from af.app import main

        result = cli_runner.invoke(main, ["init"])

        assert result.exit_code == 0
        gitignore_path = tmp_git_repo / ".gitignore"
        if gitignore_path.exists():
            gitignore = gitignore_path.read_text()
            assert "AGENTS.md" not in gitignore


# ---------------------------------------------------------------------------
# Fixtures (local, for integration tests that need cli_runner + tmp_git_repo)
# ---------------------------------------------------------------------------


@pytest.fixture
def cli_runner() -> CliRunner:
    """Provide a Click CLI test runner."""
    return CliRunner()


@pytest.fixture
def tmp_git_repo(tmp_path: Path):
    """Create a temporary git repository for integration tests."""
    repo = tmp_path / "repo"
    repo.mkdir()

    subprocess.run(["git", "init"], cwd=repo, check=True, capture_output=True)
    subprocess.run(
        ["git", "config", "user.email", "test@test.com"],
        cwd=repo,
        check=True,
        capture_output=True,
    )
    subprocess.run(
        ["git", "config", "user.name", "Test"],
        cwd=repo,
        check=True,
        capture_output=True,
    )

    readme = repo / "README.md"
    readme.write_text("# Test repo\n")
    subprocess.run(["git", "add", "."], cwd=repo, check=True, capture_output=True)
    subprocess.run(
        ["git", "commit", "-m", "initial"],
        cwd=repo,
        check=True,
        capture_output=True,
    )

    original_dir = os.getcwd()
    os.chdir(repo)
    yield repo
    os.chdir(original_dir)
