"""Prompt template loading for agentspec.

Templates are markdown files loaded with a two-tier fallback:
  1. ``<project_dir>/.agent-fox/prompts/<name>.md``  (project override)
  2. ``_templates/prompts/<name>.md``                (package default)

The project-level path is only checked when *project_dir* is provided.

Template variables use ``string.Template`` syntax (``$variable``) to avoid
conflicts with literal curly braces in prompt content.
"""

from __future__ import annotations

import logging
import re
from pathlib import Path
from string import Template

logger = logging.getLogger(__name__)

_DEFAULT_PROMPTS_DIR: Path = Path(__file__).resolve().parent / "_templates" / "prompts"

_FRONTMATTER_RE = re.compile(r"\A---\s*\n.*?\n---\s*\n", re.DOTALL)

_SAFE_NAME_RE = re.compile(r"^[a-zA-Z0-9_-]+$")


def _validate_prompt_name(name: str) -> None:
    """Raise ``ValueError`` if *name* contains characters unsafe for filesystem paths.

    Accepts only alphanumeric characters, hyphens, and underscores.  Rejects
    anything else (dots, slashes, etc.) to prevent CWE-22 path traversal.
    """
    if not _SAFE_NAME_RE.match(name):
        raise ValueError(f"Invalid prompt name: {name!r}")


def _strip_frontmatter(content: str) -> str:
    """Strip YAML frontmatter block from prompt content.

    Removes the leading ``---`` ... ``---`` block if present.
    Returns content unchanged when no frontmatter is found.
    """
    return _FRONTMATTER_RE.sub("", content, count=1)


def load_prompt(name: str, *, project_dir: Path | None = None) -> str:
    """Load a prompt template by name.

    Resolution order (first match wins):
    1. ``<project_dir>/.agent-fox/prompts/<name>.md``
    2. ``_templates/prompts/<name>.md``

    Step 1 is skipped when *project_dir* is ``None``.

    The returned content has YAML frontmatter stripped.

    Raises:
        ValueError: If *name* contains unsafe characters.
        FileNotFoundError: If no prompt file is found in any location.
    """
    _validate_prompt_name(name)

    candidates: list[Path] = []
    if project_dir is not None:
        candidates.append(project_dir / ".agent-fox" / "prompts" / f"{name}.md")
    candidates.append(_DEFAULT_PROMPTS_DIR / f"{name}.md")

    for candidate in candidates:
        if candidate.is_symlink():
            logger.warning("Skipping symlink prompt candidate: %s", candidate)
            continue
        if candidate.exists():
            logger.debug("Loading prompt %r from: %s", name, candidate)
            content = candidate.read_text(encoding="utf-8")
            return _strip_frontmatter(content)

    raise FileNotFoundError(f"No prompt template found for {name!r}")


def load_prompt_template(
    name: str,
    *,
    project_dir: Path | None = None,
    **variables: str,
) -> str:
    """Load a prompt template and substitute variables.

    Loads the template via :func:`load_prompt`, then applies
    ``string.Template.safe_substitute`` with the given *variables*.
    Unmatched ``$variable`` references pass through unchanged.
    """
    raw = load_prompt(name, project_dir=project_dir)
    return Template(raw).safe_substitute(variables)
