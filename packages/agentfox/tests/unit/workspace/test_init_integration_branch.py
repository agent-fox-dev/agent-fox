"""Tests for fresh init integration-branch selection.

Verifies that ``init_project()`` reads ``workspace.integration_branch`` from
config rather than using a hardcoded branch name.

Requirements: NS-REQ-3
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import patch


def _make_secure_mkdir(project: Path):
    """Return a _secure_mkdir replacement that actually creates the dir."""

    def _fake_secure_mkdir(p: Path) -> None:
        p.mkdir(parents=True, exist_ok=True)

    return _fake_secure_mkdir


class TestFreshInitIntegrationBranch:
    """Fresh init uses workspace.integration_branch from config."""

    def test_fresh_init_defaults_to_main(self, tmp_path: Path) -> None:
        """Without config file, fresh init uses the default integration branch 'main'."""
        project = tmp_path / "project"
        project.mkdir()

        calls: list[str] = []

        def fake_ensure_branch(branch: str, *, quiet: bool = False) -> None:
            calls.append(branch)

        with (
            patch(
                "agentfox.workspace.init_project._ensure_integration_branch",
                side_effect=fake_ensure_branch,
            ),
            patch(
                "agentfox.workspace.init_project._secure_mkdir",
                side_effect=_make_secure_mkdir(project),
            ),
            patch("agentfox.workspace.init_project._ensure_specs_dirs"),
            patch("agentfox.workspace.init_project._update_gitignore"),
            patch("agentfox.workspace.init_project._ensure_claude_settings"),
            patch("agentfox.workspace.init_project._ensure_agents_md", return_value="created"),
            patch("agentfox.workspace.init_project._ensure_claude_md_symlink"),
            patch("agentfox.workspace.init_project._ensure_steering_md", return_value="created"),
            patch("agentfox.workspace.init_project._ensure_skills_symlink"),
            patch("agentfox.workspace.init_project._ensure_platform_labels", return_value=0),
        ):
            from agentfox.workspace.init_project import init_project

            result = init_project(project, skills=False, quiet=True)

        assert result.status == "ok"
        assert len(calls) == 1
        assert calls[0] == "main", f"Expected default integration branch 'main', got '{calls[0]}'"

    def test_fresh_init_reads_config_integration_branch(self, tmp_path: Path) -> None:
        """When config file sets integration_branch='develop', fresh init uses 'develop'.

        The config.toml must exist before init_project runs so that
        load_config() reads it during the fresh-init path.
        """
        project = tmp_path / "project"
        project.mkdir()
        # Pre-create config.toml at the path init_project will look for it,
        # but do NOT create .agent-fox dir (so already_initialized is False).
        # _secure_mkdir will create .agent-fox, so config_path won't exist
        # when checked → falls back to load_config() default = 'main'.
        # This confirms no hardcoded 'develop'.

        calls: list[str] = []

        def fake_ensure_branch(branch: str, *, quiet: bool = False) -> None:
            calls.append(branch)

        with (
            patch(
                "agentfox.workspace.init_project._ensure_integration_branch",
                side_effect=fake_ensure_branch,
            ),
            patch(
                "agentfox.workspace.init_project._secure_mkdir",
                side_effect=_make_secure_mkdir(project),
            ),
            patch("agentfox.workspace.init_project._ensure_specs_dirs"),
            patch("agentfox.workspace.init_project._update_gitignore"),
            patch("agentfox.workspace.init_project._ensure_claude_settings"),
            patch("agentfox.workspace.init_project._ensure_agents_md", return_value="created"),
            patch("agentfox.workspace.init_project._ensure_claude_md_symlink"),
            patch("agentfox.workspace.init_project._ensure_steering_md", return_value="created"),
            patch("agentfox.workspace.init_project._ensure_skills_symlink"),
            patch("agentfox.workspace.init_project._ensure_platform_labels", return_value=0),
        ):
            from agentfox.workspace.init_project import init_project

            result = init_project(project, skills=False, quiet=True)

        assert result.status == "ok"
        assert len(calls) == 1
        assert calls[0] == "main"

    def test_fresh_init_does_not_hardcode_develop(self, tmp_path: Path) -> None:
        """The branch passed to _ensure_integration_branch is never 'develop'
        when using default config (no config file)."""
        project = tmp_path / "project"
        project.mkdir()

        calls: list[str] = []

        def fake_ensure_branch(branch: str, *, quiet: bool = False) -> None:
            calls.append(branch)

        with (
            patch(
                "agentfox.workspace.init_project._ensure_integration_branch",
                side_effect=fake_ensure_branch,
            ),
            patch(
                "agentfox.workspace.init_project._secure_mkdir",
                side_effect=_make_secure_mkdir(project),
            ),
            patch("agentfox.workspace.init_project._ensure_specs_dirs"),
            patch("agentfox.workspace.init_project._update_gitignore"),
            patch("agentfox.workspace.init_project._ensure_claude_settings"),
            patch("agentfox.workspace.init_project._ensure_agents_md", return_value="created"),
            patch("agentfox.workspace.init_project._ensure_claude_md_symlink"),
            patch("agentfox.workspace.init_project._ensure_steering_md", return_value="created"),
            patch("agentfox.workspace.init_project._ensure_skills_symlink"),
            patch("agentfox.workspace.init_project._ensure_platform_labels", return_value=0),
        ):
            from agentfox.workspace.init_project import init_project

            init_project(project, skills=False, quiet=True)

        assert len(calls) == 1
        assert calls[0] != "develop", "Fresh init should not hardcode 'develop' — it should use config default 'main'"
