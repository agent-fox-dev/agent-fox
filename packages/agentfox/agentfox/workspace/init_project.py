"""Backing module for the ``init`` CLI command.

Contains all project initialization logic: directory creation, config
generation, git branch setup, skill installation, and settings merging.
The CLI handler is a thin wrapper that delegates here.

Requirements: 59-REQ-5.1, 59-REQ-5.2, 59-REQ-5.3
"""

from __future__ import annotations

import json
import logging
import os
import shutil
import stat
import subprocess
from dataclasses import dataclass
from pathlib import Path

logger = logging.getLogger(__name__)

# Template paths
_TEMPLATES_DIR = Path(__file__).resolve().parent.parent / "_templates"
_AGENTS_MD_TEMPLATE = _TEMPLATES_DIR / "agents_md.md"

# Lines to add to .gitignore
_GITIGNORE_ENTRIES = [
    "# agent-fox",
    ".agent-fox/*",
    "!.agent-fox/config.toml",
    "!.agent-fox/steering.md",
    "!.agent-fox/specs/",
    "!.agent-fox/profiles/",
    "!.agent-fox/profiles/*",
    ".claude/worktrees/",
]

CANONICAL_PERMISSIONS: list[str] = [
    "Bash(bash:*)",
    "Bash(wc:*)",
    "Bash(git:*)",
    "Bash(python:*)",
    "Bash(python3:*)",
    "Bash(uv:*)",
    "Bash(make:*)",
    "Bash(sort:*)",
    "Bash(awk:*)",
    "Bash(ruff:*)",
    "Bash(gh:*)",
    "Bash(claude:*)",
    "Bash(source .venv/bin/activate:*)",
    "WebSearch",
    "WebFetch(domain:pypi.org)",
    "WebFetch(domain:github.com)",
    "WebFetch(domain:raw.githubusercontent.com)",
    "Grep",
    "Read",
    "Glob",
    "Edit",
    "Write",
]

_STEERING_PLACEHOLDER: str = """\
<!-- steering:placeholder -->
<!--
  Steering Directives
  ===================
  This file is read by every agent and skill working on this repository.
  Add your directives below to influence agent behavior across all sessions.

  Examples:
    - "Always prefer composition over inheritance."
    - "Never modify files under legacy/ without approval."
    - "Use pytest parametrize for all new test cases."

  Remove this comment block and the placeholder marker above when you add
  your first directive. Or simply add content below — the system ignores
  this file when it contains only the placeholder marker and comments.
-->
"""


def _secure_write_text(path: Path, content: str) -> None:
    """Write *content* to *path* and restrict permissions to owner-only (0o600)."""
    path.write_text(content)
    os.chmod(path, stat.S_IRUSR | stat.S_IWUSR)


def _secure_mkdir(path: Path) -> None:
    """Create directory (if needed) and restrict permissions to owner-only (0o700)."""
    path.mkdir(parents=True, exist_ok=True)
    os.chmod(path, stat.S_IRWXU)


def _is_git_repo() -> bool:
    """Check if the current directory is inside a git repository."""
    try:
        result = subprocess.run(
            ["git", "rev-parse", "--is-inside-work-tree"],
            capture_output=True,
            text=True,
        )
        return result.returncode == 0
    except FileNotFoundError:
        return False


def _branch_exists(branch: str) -> bool:
    """Check if a local git branch exists."""
    result = subprocess.run(
        ["git", "branch", "--list", branch],
        capture_output=True,
        text=True,
    )
    return branch in result.stdout


def _create_branch(branch: str) -> None:
    """Create a new git branch without switching to it."""
    subprocess.run(
        ["git", "branch", branch],
        capture_output=True,
        text=True,
        check=True,
    )


def _ensure_skills_symlink(project_root: Path) -> None:
    """Create .claude/skills symlink pointing to ../.agents/skills.

    If .claude/skills/ exists as a regular directory, its contents are
    migrated to .agents/skills/ first, the directory is removed, and
    the symlink is created.  If it is already a symlink, it is left
    untouched.  Symlink creation failure is logged as a warning.

    Requirements: 709-AC-2, 709-AC-3, 709-AC-6
    """
    claude_skills = project_root / ".claude" / "skills"
    agents_skills = project_root / ".agents" / "skills"

    # Already a symlink — nothing to do
    if claude_skills.is_symlink():
        return

    # Migration: .claude/skills/ is a real directory from a prior install
    if claude_skills.is_dir():
        agents_skills.mkdir(parents=True, exist_ok=True)
        for child in claude_skills.iterdir():
            dest = agents_skills / child.name
            if dest.exists():
                logger.debug("Skipping migration of '%s': already exists at destination", child.name)
                continue
            shutil.move(str(child), str(dest))
        shutil.rmtree(claude_skills)
        logger.debug("Migrated skills from .claude/skills/ to .agents/skills/")

    # Only create the symlink if the canonical directory exists
    if not agents_skills.is_dir():
        return

    # Ensure .claude/ directory exists (may already from _ensure_claude_settings)
    claude_dir = project_root / ".claude"
    claude_dir.mkdir(parents=True, exist_ok=True)

    try:
        claude_skills.symlink_to(Path("..", ".agents", "skills"))
    except OSError as exc:
        logger.warning("Could not create .claude/skills symlink: %s", exc)


def _update_gitignore(project_root: Path) -> None:
    """Add agent-fox entries to .gitignore if not already present.

    Reads the existing .gitignore (if any), appends missing entries,
    and writes the file back.
    """
    gitignore_path = project_root / ".gitignore"

    if gitignore_path.exists():
        existing = gitignore_path.read_text()
    else:
        existing = ""

    lines_to_add: list[str] = []
    for entry in _GITIGNORE_ENTRIES:
        if entry not in existing:
            lines_to_add.append(entry)

    if lines_to_add:
        # Ensure we start on a new line
        separator = "\n" if existing and not existing.endswith("\n") else ""
        addition = separator + "\n".join(lines_to_add) + "\n"
        gitignore_path.write_text(existing + addition)
        logger.debug("Updated .gitignore with agent-fox entries")


def _ensure_claude_settings(project_root: Path) -> None:
    """Create or update .claude/settings.local.json with canonical permissions.

    - If the file does not exist, create it with CANONICAL_PERMISSIONS.
    - If the file exists, merge: add missing canonical entries, preserve
      user-added entries and their ordering.
    - If the file contains invalid JSON, log a warning and skip.

    Requirements: 17-REQ-1.1, 17-REQ-1.2, 17-REQ-1.3, 17-REQ-1.E1,
                  17-REQ-2.1, 17-REQ-2.2, 17-REQ-2.3,
                  17-REQ-2.E1, 17-REQ-2.E2, 17-REQ-2.E3
    """
    claude_dir = project_root / ".claude"
    settings_path = claude_dir / "settings.local.json"

    # 17-REQ-1.2: Create .claude/ directory if absent
    _secure_mkdir(claude_dir)

    if not settings_path.exists():
        # 17-REQ-1.1: Create with canonical permissions
        data = {"permissions": {"allow": list(CANONICAL_PERMISSIONS)}}
        _secure_write_text(settings_path, json.dumps(data, indent=2) + "\n")
        logger.debug("Created .claude/settings.local.json")
        return

    # File exists — merge
    raw = settings_path.read_text()
    try:
        data = json.loads(raw)
    except (json.JSONDecodeError, ValueError):
        # 17-REQ-2.E1: Invalid JSON — warn and skip
        logger.warning(
            "Invalid JSON in %s, skipping settings merge",
            settings_path,
        )
        return

    if not isinstance(data, dict):
        logger.warning("Settings file is not a JSON object, skipping merge")
        return

    # 17-REQ-2.E2: Missing permissions structure — create it
    if "permissions" not in data:
        data["permissions"] = {}
    permissions = data["permissions"]

    if not isinstance(permissions, dict):
        logger.warning("permissions is not a JSON object, skipping merge")
        return

    if "allow" not in permissions:
        permissions["allow"] = []

    allow = permissions["allow"]
    if not isinstance(allow, list):
        # 17-REQ-2.E3: allow is not a list — warn and skip
        logger.warning("permissions.allow is not a list, skipping merge")
        return

    # 17-REQ-2.1, 17-REQ-2.2, 17-REQ-2.3: Merge
    existing_set = set(allow)
    missing = [p for p in CANONICAL_PERMISSIONS if p not in existing_set]

    if not missing:
        # 17-REQ-1.E1: All canonical entries present — no-op
        logger.debug("All canonical permissions already present")
        return

    # Preserve order: existing first, new appended
    allow.extend(missing)
    _secure_write_text(settings_path, json.dumps(data, indent=2) + "\n")
    logger.debug(
        "Merged %d missing canonical permissions into settings",
        len(missing),
    )


def _ensure_agents_md(project_root: Path) -> str:
    """Create AGENTS.md from template if it does not exist.

    Args:
        project_root: The project root directory (Path.cwd()).

    Returns:
        "created" if the file was written, "skipped" if it already existed.

    Raises:
        FileNotFoundError: If the bundled template is missing.

    Requirements: 44-REQ-1.E1, 44-REQ-2.1, 44-REQ-3.1, 44-REQ-3.E1
    """
    agents_md = project_root / "AGENTS.md"
    if agents_md.exists():
        return "skipped"

    # This raises FileNotFoundError if the template is missing (44-REQ-1.E1)
    content = _AGENTS_MD_TEMPLATE.read_text(encoding="utf-8")
    # 371-REQ-3.1: Replace template variables with configured spec root
    from agentfox.core.config import load_config

    config_path = project_root / ".agent-fox" / "config.toml"
    _config = load_config(config_path if config_path.exists() else None)
    content = content.replace("{{SPEC_ROOT}}", _config.paths.spec_root)
    agents_md.write_text(content, encoding="utf-8")
    logger.debug("Created AGENTS.md from template")
    return "created"


def _ensure_claude_md_symlink(project_root: Path) -> None:
    """Create CLAUDE.md -> AGENTS.md symlink if CLAUDE.md does not exist.

    If CLAUDE.md exists as a regular file it is left untouched.
    If CLAUDE.md already exists as a symlink it is left as-is.
    Symlink creation failure is logged as a warning.

    Requirements: 709-AC-4, 709-AC-5, 709-AC-6
    """
    claude_md = project_root / "CLAUDE.md"

    if claude_md.exists() or claude_md.is_symlink():
        return

    if not (project_root / "AGENTS.md").exists():
        return

    try:
        claude_md.symlink_to(Path("AGENTS.md"))
    except OSError as exc:
        logger.warning("Could not create CLAUDE.md symlink: %s", exc)


def _ensure_steering_md(project_root: Path, specs_dir: Path | None = None) -> str:
    """Create .agent-fox/steering.md placeholder if it does not exist.

    Returns:
        "created" if the file was written, "skipped" if it already existed
        or could not be created due to a permission error.

    Requirements: 64-REQ-1.1, 64-REQ-1.2, 64-REQ-1.3, 64-REQ-1.4,
                  64-REQ-1.E1
    """
    agent_fox_dir = project_root / ".agent-fox"
    steering_path = agent_fox_dir / "steering.md"

    # 64-REQ-1.2: Skip if already exists
    if steering_path.exists():
        return "skipped"

    # 64-REQ-1.4: Create .agent-fox directory if needed
    try:
        agent_fox_dir.mkdir(parents=True, exist_ok=True)
    except OSError as exc:
        # 64-REQ-1.E1: Permission error — log warning and continue
        logger.warning(
            "Cannot create .agent-fox directory at %s: %s — skipping steering.md",
            agent_fox_dir,
            exc,
        )
        return "skipped"

    # 64-REQ-1.1, 64-REQ-1.3: Write placeholder with sentinel
    steering_path.write_text(_STEERING_PLACEHOLDER, encoding="utf-8")
    logger.debug("Created %s/steering.md placeholder", agent_fox_dir)
    return "created"


def _ensure_integration_branch(branch: str, *, quiet: bool = False) -> None:
    """Create or recover the integration branch using the robust ensure logic.

    Uses the async ``ensure_integration_branch()`` from workspace, which
    handles remote tracking, fast-forwarding, and fallback to the default
    branch.

    Args:
        branch: Name of the integration branch (e.g. "main" or "develop").
        quiet: If True, suppress human-readable output (for JSON mode).

    Requirements: 19-REQ-1.5
    """
    import asyncio

    from agentfox.workspace import ensure_integration_branch

    try:
        asyncio.run(ensure_integration_branch(Path.cwd(), branch))
    except Exception as exc:
        if not quiet:
            import click

            click.echo(f"Warning: Could not ensure {branch} branch: {exc}", err=True)
        # Fall back to the simple approach
        if _branch_exists(branch):
            pass
        else:
            try:
                _create_branch(branch)
            except Exception:
                if not quiet:
                    import click

                    click.echo(f"Error: Could not create {branch} branch.", err=True)


@dataclass(frozen=True)
class InitResult:
    """Structured result from project initialization."""

    status: str  # "ok" | "already_initialized"
    agents_md: str  # "created" | "skipped"
    steering_md: str = "skipped"
    labels_ensured: int = 0  # number of required labels created/verified


async def _ensure_platform_labels_async(project_root: Path) -> int:
    """Create required platform labels if the platform is configured.

    Attempts to create the labels defined in ``REQUIRED_LABELS`` via the
    configured platform.  Returns silently if no platform is configured or
    if ``GITHUB_PAT`` is absent (fail-open: local-only init still succeeds).

    Returns:
        Number of labels successfully created or already existing.

    Requirements: 358-REQ-3, 358-REQ-4, 358-REQ-5
    """
    from afissues.errors import IntegrationError
    from afissues.labels import REQUIRED_LABELS

    from agentfox.core.config import load_config
    from agentfox.maintenance.platform_factory import create_platform_safe

    config_path = project_root / ".agent-fox" / "config.toml"
    try:
        config = load_config(config_path) if config_path.exists() else load_config()
    except Exception:
        logger.debug("Could not load config for label creation; skipping")
        return 0

    platform = create_platform_safe(config, project_root)
    if platform is None:
        logger.debug("Platform not configured; skipping required label creation")
        return 0

    count = 0
    for spec in REQUIRED_LABELS:
        try:
            await platform.create_label(spec.name, spec.color, spec.description)
            count += 1
        except IntegrationError:
            # 06-REQ-1.E1: IntegrationError propagates and halts — the
            # label is required for pipeline correctness.
            raise
        except Exception:
            logger.warning(
                "Could not ensure label %r on platform; skipping",
                spec.name,
                exc_info=True,
            )

    return count


def _ensure_platform_labels(project_root: Path) -> int:
    """Synchronous wrapper for :func:`_ensure_platform_labels_async`.

    Uses :func:`asyncio.run` following the same pattern as
    :func:`_ensure_integration_branch`.  Returns 0 silently on any error so that
    a platform misconfiguration does not block local ``af init``.

    Requirements: 358-REQ-3, 358-REQ-4, 358-REQ-5
    """
    import asyncio

    try:
        return asyncio.run(_ensure_platform_labels_async(project_root))
    except Exception as exc:
        logger.warning("Failed to ensure platform labels: %s", exc)
        return 0


def _ensure_specs_dirs(project_root: Path) -> None:
    """Create .agent-fox/specs/ and .agent-fox/specs/archive/ with a .gitkeep."""
    specs_dir = project_root / ".agent-fox" / "specs"
    archive_dir = specs_dir / "archive"
    archive_dir.mkdir(parents=True, exist_ok=True)
    gitkeep = archive_dir / ".gitkeep"
    if not gitkeep.exists():
        gitkeep.write_text("", encoding="utf-8")


def init_project(
    path: Path,
    *,
    quiet: bool = False,
) -> InitResult:
    """Initialize agent-fox in a project directory.

    This function can be called without the Click framework.

    Args:
        path: Project root directory.
        quiet: Suppress human-readable output.

    Returns:
        InitResult with initialization status.

    Requirements: 59-REQ-5.1, 59-REQ-5.2, 59-REQ-5.3
    """
    agent_fox_dir = path / ".agent-fox"
    config_path = agent_fox_dir / "config.toml"

    already_initialized = agent_fox_dir.exists()

    if already_initialized:
        from agentfox.core.config import load_config

        cfg = load_config(config_path) if config_path.exists() else load_config()

        (agent_fox_dir / "worktrees").mkdir(parents=True, exist_ok=True)
        _ensure_specs_dirs(path)
        _update_gitignore(path)
        _ensure_integration_branch(cfg.workspace.integration_branch, quiet=quiet)
        _ensure_claude_settings(path)
        agents_md_status = _ensure_agents_md(path)
        _ensure_claude_md_symlink(path)
        steering_status = _ensure_steering_md(path)
        _ensure_skills_symlink(path)

        labels_count = _ensure_platform_labels(path)

        return InitResult(
            status="already_initialized",
            agents_md=agents_md_status,
            steering_md=steering_status,
            labels_ensured=labels_count,
        )

    # Fresh initialization
    _secure_mkdir(agent_fox_dir)
    (agent_fox_dir / "worktrees").mkdir(exist_ok=True)
    _ensure_specs_dirs(path)

    from agentfox.core.config import load_config

    cfg = load_config(config_path) if config_path.exists() else load_config()
    _ensure_integration_branch(cfg.workspace.integration_branch, quiet=quiet)
    _update_gitignore(path)
    _ensure_claude_settings(path)
    agents_md_status = _ensure_agents_md(path)
    _ensure_claude_md_symlink(path)
    steering_status = _ensure_steering_md(path)
    _ensure_skills_symlink(path)

    labels_count = _ensure_platform_labels(path)

    return InitResult(
        status="ok",
        agents_md=agents_md_status,
        steering_md=steering_status,
        labels_ensured=labels_count,
    )
