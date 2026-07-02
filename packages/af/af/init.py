"""Init CLI command: initialize an agent-fox project.

Thin CLI wrapper that delegates to ``workspace.init_project`` for
all initialization logic, then handles output formatting.

Global and local config file management is handled directly by
``init_cmd`` according to spec 13 requirements, while
``init_project`` handles git setup, gitignore, skills, etc.

Requirements: 01-REQ-3.1, 01-REQ-3.2, 01-REQ-3.3, 01-REQ-3.4,
              01-REQ-3.5, 01-REQ-3.E1, 01-REQ-3.E2,
              04-REQ-2.1, 04-REQ-2.6,
              13-REQ-8.1, 13-REQ-8.2, 13-REQ-8.3, 13-REQ-8.4,
              13-REQ-8.5, 13-REQ-8.E1,
              99-REQ-3.1, 99-REQ-3.2, 99-REQ-3.3, 99-REQ-3.E1
"""

from __future__ import annotations

import logging
import os
import shutil
import subprocess
from pathlib import Path

import agentfox
import click
from agentfox.io import exit_codes
from agentfox.workspace.init_project import (
    _is_git_repo,
    init_project,
)

from af import get_output_manager

logger = logging.getLogger(__name__)

# Package-embedded default profiles directory (mirrors profiles.py resolution)
_DEFAULT_PROFILES_DIR: Path = Path(agentfox.__file__).resolve().parent / "_templates" / "profiles"


def init_profiles(project_dir: Path) -> list[Path]:
    """Copy default archetype profiles into ``.agent-fox/profiles/``.

    Copies all ``*.md`` files from the package-embedded
    ``_templates/profiles/`` directory into
    ``<project_dir>/.agent-fox/profiles/``.  Existing files are skipped
    without modification.  The destination directory is created if absent.

    Args:
        project_dir: Root of the project directory.

    Returns:
        List of newly created profile file paths.  Files that already
        existed are not included.

    Requirements: 99-REQ-3.1, 99-REQ-3.2, 99-REQ-3.3, 99-REQ-3.E1
    """
    profiles_dest = project_dir / ".agent-fox" / "profiles"
    profiles_dest.mkdir(parents=True, exist_ok=True)

    created: list[Path] = []
    for src_file in sorted(_DEFAULT_PROFILES_DIR.glob("*.md")):
        dest_file = profiles_dest / src_file.name
        if dest_file.exists():
            logger.debug("Preserving existing profile: %s", dest_file)
            continue
        shutil.copy2(src_file, dest_file)
        created.append(dest_file)

    if created:
        # Stage the newly created profiles in git.  The .gitignore exception
        # !.agent-fox/profiles/* ensures git accepts the files without --force.
        try:
            subprocess.run(
                ["git", "add", *[str(p) for p in created]],
                cwd=project_dir,
                capture_output=True,
                text=True,
                check=True,
            )
            logger.debug("Staged %d profile(s) in git", len(created))
        except (subprocess.CalledProcessError, FileNotFoundError) as exc:
            logger.warning("Could not git add profiles: %s", exc)

    return created


def _ensure_global_config_for_init() -> str | None:
    """Create the global config at ``$HOME/.agent-fox/config.toml`` if absent.

    Returns a user-facing message string, or ``None`` when HOME is
    unresolvable.  Never overwrites an existing global config.

    Requirements: 13-REQ-8.1, 13-REQ-8.2, 13-REQ-8.E1
    """
    try:
        home = Path.home()
    except (RuntimeError, OSError):
        # 13-REQ-8.E1: HOME unresolvable — skip global config
        logger.debug("$HOME could not be resolved; skipping global config creation")
        return None

    global_dir = home / ".agent-fox"
    global_config = global_dir / "config.toml"

    if global_config.exists():
        # 13-REQ-8.2: never overwrite existing global config
        return f"Skipped existing global config at {global_config}"

    # 13-REQ-8.1: create directory with 0o700 and write default config
    try:
        os.makedirs(str(global_dir), mode=0o700, exist_ok=True)
    except OSError as exc:
        logger.warning("Could not create global config directory %s: %s", global_dir, exc)
        return None

    from agentfox.core.config_gen import generate_default_config

    global_config.write_text(generate_default_config(), encoding="utf-8")
    return f"Created global config at {global_config}"


def _create_local_config(project_root: Path) -> str:
    """Create or overwrite the local config at ``.agent-fox/config.toml``.

    Called only when ``--config`` is passed to ``af init``.  Always
    writes the promoted-fields template, overwriting any existing file.
    """
    from agentfox.core.config_gen import generate_local_config_template

    local_dir = project_root / ".agent-fox"
    config_path = local_dir / "config.toml"

    local_dir.mkdir(parents=True, exist_ok=True)
    config_path.write_text(generate_local_config_template(), encoding="utf-8")

    return f"Created local config at {config_path}"


@exit_codes(**{"0": "Success", "1": "Error"})
@click.command("init")
@click.option(
    "--config",
    "create_config",
    is_flag=True,
    default=False,
    help="Create a local .agent-fox/config.toml (overwrites if present).",
)
@click.option(
    "--skills",
    is_flag=True,
    default=False,
    help="Install bundled Claude Code skills into .claude/skills/.",
)
@click.option(
    "--profiles",
    is_flag=True,
    default=False,
    help="Copy default archetype profiles into .agent-fox/profiles/.",
)
@click.pass_context
def init_cmd(ctx: click.Context, create_config: bool, skills: bool, profiles: bool) -> None:
    """Initialize the current project for agent-fox.

    Creates the .agent-fox/ directory structure, sets up the integration
    branch, and updates .gitignore.  Pass --config to also create a
    local config.toml for per-project overrides.
    """
    om = get_output_manager(ctx)
    json_mode = om.json_mode

    project_root = Path.cwd()

    # --- Global config scaffolding ---
    global_msg = _ensure_global_config_for_init()

    # --- Local config (opt-in via --config) ---
    local_msg: str | None = None
    if create_config:
        local_msg = _create_local_config(project_root)

    # --- Git-dependent initialization ---
    if not _is_git_repo():
        if json_mode:
            data: dict = {"status": "ok", "global_config": global_msg}
            if local_msg:
                data["local_config"] = local_msg
            om.emit(data)
            return
        if global_msg:
            click.echo(global_msg)
        if local_msg:
            click.echo(local_msg)
        return

    result = init_project(project_root, skills=skills, quiet=json_mode)

    if json_mode:
        result_data: dict = {
            "status": "ok",
            "agents_md": result.agents_md,
            "steering_md": result.steering_md,
            "global_config": global_msg,
        }
        if local_msg:
            result_data["local_config"] = local_msg
        if result.skills_installed:
            result_data["skills_installed"] = result.skills_installed
        if result.labels_ensured:
            result_data["labels_ensured"] = result.labels_ensured
        om.emit(result_data)
        return

    if global_msg:
        click.echo(global_msg)
    if local_msg:
        click.echo(local_msg)

    if result.agents_md == "created":
        click.echo("Created AGENTS.md.")
    if result.steering_md == "created":
        click.echo("Created steering.md in .agent-fox/.")
    if result.skills_installed:
        click.echo(f"Installed {result.skills_installed} skills.")
    if result.labels_ensured:
        click.echo(f"Ensured {result.labels_ensured} required label(s) on GitHub repository.")
    if profiles:
        created_profiles = init_profiles(project_root)
        if created_profiles:
            click.echo(f"Installed {len(created_profiles)} archetype profiles.")
        else:
            click.echo("All archetype profiles already exist; nothing to install.")
