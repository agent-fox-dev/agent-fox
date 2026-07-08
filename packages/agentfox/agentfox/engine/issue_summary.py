"""GitHub issue-summary posting."""

from __future__ import annotations

import logging
import re
import subprocess
from dataclasses import dataclass
from pathlib import Path
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from agentfox.platform.protocol import PlatformProtocol

logger = logging.getLogger(__name__)


# GitHub issue URL pattern:
#   https://github.com/{owner}/{repo}/issues/{number}
_GITHUB_ISSUE_RE = re.compile(r"^https://github\.com/(?P<owner>[^/\s]+)/(?P<repo>[^/\s]+)/issues/(?P<number>\d+)\s*$")

# YAML frontmatter delimiters (must start at the very beginning of the file)
_FRONTMATTER_RE = re.compile(r"\A---\r?\n(.*?)^---\r?\n", re.DOTALL | re.MULTILINE)

# source: value line within YAML frontmatter
_YAML_SOURCE_RE = re.compile(r"^source:\s*[\"']?(.+?)[\"']?\s*$", re.MULTILINE)


@dataclass(frozen=True)
class SourceIssue:
    """Parsed issue reference from a prd.md frontmatter source field.

    Requirements: 108-REQ-1.2, 108-REQ-1.3
    """

    forge: str  # "github" (extensible to "gitlab", "linear", etc.)
    owner: str  # e.g., "agent-fox-dev"
    repo: str  # e.g., "agent-fox"
    issue_number: int  # e.g., 359


def _match_github_issue_url(url: str) -> SourceIssue | None:
    """Match a URL against the GitHub issue pattern.

    Returns a ``SourceIssue`` if the URL is a valid GitHub issue URL,
    or ``None`` otherwise.
    """
    gh_match = _GITHUB_ISSUE_RE.match(url.strip())
    if gh_match:
        return SourceIssue(
            forge="github",
            owner=gh_match.group("owner"),
            repo=gh_match.group("repo"),
            issue_number=int(gh_match.group("number")),
        )
    return None


def parse_source_url(prd_path: Path) -> SourceIssue | None:
    """Extract a GitHub issue reference from the PRD's frontmatter ``source`` field.

    Reads the YAML frontmatter from prd.md and checks the ``source`` field
    for a recognized issue URL pattern.  This is the single authoritative
    location per spec format v1.3 — there is no ``## Source`` body section.

    Returns None (never raises) if:
    - prd.md does not exist
    - frontmatter is missing or malformed
    - ``source`` key is absent or empty
    - ``source`` value does not match any known issue URL pattern

    Requirements: 108-REQ-1.1, 108-REQ-1.2, 108-REQ-1.3,
                  108-REQ-1.E1, 108-REQ-1.E2, 108-REQ-1.E3
    """
    try:
        if not prd_path.exists():
            return None

        text = prd_path.read_text(encoding="utf-8")

        # Extract YAML frontmatter block
        fm_match = _FRONTMATTER_RE.match(text)
        if fm_match is None:
            return None

        yaml_text = fm_match.group(1)

        # Extract the source value from frontmatter
        source_match = _YAML_SOURCE_RE.search(yaml_text)
        if source_match is None:
            return None

        source_value = source_match.group(1).strip()
        if not source_value:
            return None

        return _match_github_issue_url(source_value)

    except Exception:
        # Pure function — never propagate exceptions
        logger.debug("parse_source_url encountered an error", exc_info=True)
        return None


def _get_integration_head(repo_root: Path, branch: str) -> str:
    """Return the current integration branch HEAD SHA.

    Runs ``git rev-parse <branch>`` in the given repository root.
    Returns ``"unknown"`` if the command fails for any reason.

    Requirements: 108-REQ-6.1, 108-REQ-6.E1
    """
    try:
        result = subprocess.run(
            ["git", "rev-parse", branch],
            capture_output=True,
            text=True,
            cwd=str(repo_root),
        )
        if result.returncode == 0:
            return result.stdout.strip()
    except Exception:
        logger.debug("git rev-parse %s failed", branch, exc_info=True)
    return "unknown"


def build_summary_comment(
    spec_name: str,
    commit_sha: str,
    tasks_path: Path,
    branch: str = "main",
) -> str:
    """Construct the Markdown comment body for the originating issue.

    Includes the spec name, the integration branch HEAD commit SHA, a bulleted list
    of task group titles derived from tasks.md, and an auto-generated footer.

    Requirements: 108-REQ-3.1, 108-REQ-3.2, 108-REQ-3.3, 108-REQ-3.4
    """
    from agentfox.spec.parser import parse_tasks  # noqa: PLC0415

    # Extract task group titles from spec directory
    group_lines: list[str] = []
    try:
        spec_dir = tasks_path.parent
        if spec_dir.is_dir():
            groups = parse_tasks(spec_dir)
            group_lines = [f"- {g.title}" for g in groups]
    except Exception:
        logger.debug("Failed to parse tasks for summary comment", exc_info=True)

    task_section = "\n".join(group_lines) if group_lines else "*(no task groups found)*"

    return (
        f"## Spec Implemented\n\n"
        f"Spec `{spec_name}` has been fully implemented and merged to `{branch}`.\n\n"
        f"**Commit:** `{commit_sha}`\n\n"
        f"### Task Groups\n\n"
        f"{task_section}\n\n"
        f"---\n"
        f"*Auto-generated by agent-fox.*"
    )


async def post_issue_summaries(
    platform: PlatformProtocol,
    specs_dir: Path,
    completed_specs: set[str],
    already_posted: set[str],
    repo_root: Path,
    integration_branch: str = "main",
) -> set[str]:
    """Post summary comments for newly completed specs.

    Iterates specs that are in ``completed_specs`` but not yet in
    ``already_posted``, extracts the originating issue URL from each
    spec's ``prd.md``, and posts a roll-up summary comment to that issue.

    Skips specs that:
    - have no valid source URL (108-REQ-1.E1, 108-REQ-1.E2)
    - are already in ``already_posted`` (108-REQ-2.E1)
    - have a forge type that doesn't match the platform (108-REQ-4.E2)

    Handles ``add_issue_comment()`` failures gracefully: logs a warning
    and continues without affecting the run status (108-REQ-4.E1).

    Returns:
        Set of spec names for which a comment was successfully posted.

    Requirements: 108-REQ-2.1, 108-REQ-2.2, 108-REQ-2.E1,
                  108-REQ-4.1, 108-REQ-4.E1, 108-REQ-4.E2
    """
    newly_completed = completed_specs - already_posted
    posted: set[str] = set()

    from agentfox.platform.labels import LABEL_IMPLEMENTED

    for spec_name in sorted(newly_completed):
        prd_path = specs_dir / spec_name / "prd.md"
        source_issue = parse_source_url(prd_path)

        if source_issue is None:
            logger.debug("No valid source URL for spec '%s'; skipping", spec_name)
            continue

        # 108-REQ-4.E2: Skip when platform forge type doesn't match source forge
        platform_forge = getattr(platform, "forge_type", None)
        if platform_forge != source_issue.forge:
            logger.info(
                "Skipping issue summary for spec '%s': source forge='%s', platform forge='%s'",
                spec_name,
                source_issue.forge,
                platform_forge,
            )
            continue

        # Issue #648: use af:implemented label as durable dedup marker
        try:
            issue = await platform.get_issue(source_issue.issue_number)
            if LABEL_IMPLEMENTED in issue.labels:
                logger.debug(
                    "Issue #%d already has '%s' label; skipping spec '%s'",
                    source_issue.issue_number,
                    LABEL_IMPLEMENTED,
                    spec_name,
                )
                posted.add(spec_name)
                continue
        except Exception:
            logger.debug(
                "Could not check labels for issue #%d; proceeding with post",
                source_issue.issue_number,
                exc_info=True,
            )

        commit_sha = _get_integration_head(repo_root, integration_branch)
        tasks_path = specs_dir / spec_name / "tasks.md"
        body = build_summary_comment(spec_name, commit_sha, tasks_path, integration_branch)

        try:
            await platform.add_issue_comment(source_issue.issue_number, body)
            posted.add(spec_name)
            logger.info(
                "Posted issue summary for spec '%s' to issue #%d",
                spec_name,
                source_issue.issue_number,
            )
        except Exception:
            # 108-REQ-4.E1: Graceful failure — warn and continue
            logger.warning(
                "Failed to post issue summary for spec '%s'",
                spec_name,
                exc_info=True,
            )
            continue

        # Issue #636: assign af:implemented label after successful comment
        try:
            await platform.assign_label(source_issue.issue_number, LABEL_IMPLEMENTED)
            logger.info(
                "Assigned '%s' label to issue #%d for spec '%s'",
                LABEL_IMPLEMENTED,
                source_issue.issue_number,
                spec_name,
            )
        except Exception:
            logger.warning(
                "Failed to assign '%s' label for spec '%s'",
                LABEL_IMPLEMENTED,
                spec_name,
                exc_info=True,
            )

    return posted
