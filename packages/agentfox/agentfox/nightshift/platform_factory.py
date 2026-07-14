"""Platform factory: instantiate platform from config.

Requirements: 61-REQ-8.3, 61-REQ-8.E1, 04-REQ-20.*, 04-REQ-21.*
"""

from __future__ import annotations

import logging
import os
import sys
from pathlib import Path

from agentfox.platform.github import GitHubPlatform
from agentfox.platform.github import parse_remote as github_parse_remote
from agentfox.platform.protocol import PlatformProtocol

logger = logging.getLogger(__name__)

_SUPPORTED_PLATFORMS = {"github", "gitlab", "gitea"}


def _resolve_github_remote(project_root: Path) -> tuple[str, str]:
    """Detect owner/repo from ``git remote get-url origin``.

    Returns ``(owner, repo)`` on success, ``("owner", "repo")`` as fallback.
    """
    from agentfox.workspace.git import run_git_sync

    owner, repo = "owner", "repo"
    rc, stdout, _ = run_git_sync(["remote", "get-url", "origin"], cwd=project_root)
    if rc == 0:
        parsed = github_parse_remote(stdout.strip())
        if parsed:
            owner, repo = parsed
    return owner, repo


def _resolve_gitlab_remote(project_root: Path) -> str | None:
    """Detect GitLab project path from ``git remote get-url origin``.

    Returns ``"namespace/project"`` on success, ``None`` if the remote
    URL does not match a GitLab pattern.

    Requirements: 04-REQ-20.1
    """
    from agentfox.platform.gitlab import parse_remote as gitlab_parse_remote
    from agentfox.workspace.git import run_git_sync

    rc, stdout, _ = run_git_sync(["remote", "get-url", "origin"], cwd=project_root)
    if rc == 0:
        parsed = gitlab_parse_remote(stdout.strip())
        if parsed:
            namespace, project = parsed
            return f"{namespace}/{project}"
    return None


def _create_github(platform_cfg: object, project_root: Path) -> GitHubPlatform:
    """Build a GitHubPlatform from config and environment.

    Raises ``SystemExit`` on missing credentials.
    """
    token = os.environ.get("GITHUB_PAT", "")
    if not token.strip():
        logger.error("GITHUB_PAT environment variable is required")
        sys.exit(1)

    owner, repo = _resolve_github_remote(project_root)
    url = getattr(platform_cfg, "url", "") or "github.com"
    return GitHubPlatform(owner=owner, repo=repo, token=token, url=url)


def _create_gitlab(platform_cfg: object, project_root: Path) -> PlatformProtocol:
    """Build a GitLabPlatform from config and environment.

    Raises ``SystemExit`` on missing token or project identifier.

    Requirements: 04-REQ-20.1, 04-REQ-20.E1, 04-REQ-20.E2
    """
    from agentfox.platform.gitlab import GitLabPlatform

    token = os.environ.get("GITLAB_TOKEN", "")
    if not token.strip():
        logger.error("GITLAB_TOKEN environment variable is required")
        sys.exit(1)

    # Resolve project_id: try git remote first, fall back to config.
    project_id = _resolve_gitlab_remote(project_root)
    if project_id is None:
        project_id = getattr(platform_cfg, "project_id", None)
    if not project_id:
        logger.error(
            "Could not determine GitLab project identifier. "
            "Set platform.project_id in your config or ensure the git "
            "remote points to a GitLab repository."
        )
        sys.exit(1)

    url = getattr(platform_cfg, "url", "") or "gitlab.com"
    return GitLabPlatform(project_id=project_id, token=token, url=url)


def _create_gitea(platform_cfg: object, project_root: Path) -> PlatformProtocol:
    """Build a GiteaPlatform from config and environment.

    Raises ``SystemExit`` when the Gitea module is not yet available.

    Requirements: 04-REQ-21.1, 04-REQ-21.2
    """
    try:
        from agentfox.platform.gitea import GiteaPlatform  # noqa: F401
        from agentfox.platform.gitea import parse_remote as gitea_parse_remote  # noqa: F401
    except ImportError:
        logger.error(
            "The Gitea platform is not yet available. "
            "Install the afissues package with Gitea support."
        )
        sys.exit(1)

    token = os.environ.get("GITEA_TOKEN", "")
    if not token.strip():
        logger.error("GITEA_TOKEN environment variable is required")
        sys.exit(1)

    url = getattr(platform_cfg, "url", "")
    if not url:
        logger.error("platform.url is required for Gitea (no default host)")
        sys.exit(1)

    # Resolve owner/repo from git remote or config.
    owner: str | None = None
    repo: str | None = None
    from agentfox.workspace.git import run_git_sync

    rc, stdout, _ = run_git_sync(["remote", "get-url", "origin"], cwd=project_root)
    if rc == 0:
        parsed = gitea_parse_remote(stdout.strip())
        if parsed:
            owner, repo = parsed

    if not owner or not repo:
        owner = getattr(platform_cfg, "owner", None)
        repo = getattr(platform_cfg, "repo", None)
    if not owner or not repo:
        project_id = getattr(platform_cfg, "project_id", None)
        if project_id and "/" in project_id:
            owner, repo = project_id.split("/", 1)

    if not owner or not repo:
        logger.error(
            "Could not determine Gitea owner/repo. "
            "Set platform.project_id in your config."
        )
        sys.exit(1)

    return GiteaPlatform(owner=owner, repo=repo, token=token, url=url)


def create_platform(config: object, project_root: Path) -> PlatformProtocol:
    """Create a platform instance from configuration.

    Requirements: 61-REQ-8.3, 61-REQ-8.E1, 04-REQ-20.1, 04-REQ-20.2,
                  04-REQ-21.1
    """
    platform_cfg = getattr(config, "platform", None)
    platform_type = getattr(platform_cfg, "type", "none")

    if platform_type == "none":
        logger.error("Night-shift requires a configured platform. Set [platform] type = 'github' in your config.")
        sys.exit(1)

    if platform_type not in _SUPPORTED_PLATFORMS:
        logger.error(
            "Unsupported platform type '%s'. Supported types: %s",
            platform_type,
            ", ".join(sorted(_SUPPORTED_PLATFORMS)),
        )
        sys.exit(1)

    if platform_type == "github":
        return _create_github(platform_cfg, project_root)

    if platform_type == "gitlab":
        return _create_gitlab(platform_cfg, project_root)

    if platform_type == "gitea":
        return _create_gitea(platform_cfg, project_root)

    # Should not be reachable given the _SUPPORTED_PLATFORMS check above.
    logger.error("Unsupported platform type '%s'", platform_type)  # pragma: no cover
    sys.exit(1)  # pragma: no cover


def create_platform_safe(config: object, project_root: Path) -> PlatformProtocol | None:
    """Create a platform instance, returning None if not configured.

    Unlike create_platform(), does not call sys.exit() on missing config
    or credentials. Returns None silently when:
    - platform type is "none" or unsupported
    - required environment variables are absent

    Requirements: 108-REQ-5.3, 04-REQ-20.2
    """
    platform_cfg = getattr(config, "platform", None)
    platform_type = getattr(platform_cfg, "type", "none")

    if platform_type == "none":
        return None

    if platform_type not in _SUPPORTED_PLATFORMS:
        logger.debug(
            "create_platform_safe: unsupported platform type '%s'; returning None",
            platform_type,
        )
        return None

    if platform_type == "github":
        token = os.environ.get("GITHUB_PAT", "")
        if not token:
            logger.debug("create_platform_safe: GITHUB_PAT not set; returning None")
            return None
        owner, repo = _resolve_github_remote(project_root)
        url = getattr(platform_cfg, "url", "") or "github.com"
        return GitHubPlatform(owner=owner, repo=repo, token=token, url=url)

    if platform_type == "gitlab":
        from agentfox.platform.gitlab import GitLabPlatform

        token = os.environ.get("GITLAB_TOKEN", "")
        if not token:
            logger.debug("create_platform_safe: GITLAB_TOKEN not set; returning None")
            return None
        project_id = _resolve_gitlab_remote(project_root)
        if project_id is None:
            project_id = getattr(platform_cfg, "project_id", None)
        if not project_id:
            logger.debug(
                "create_platform_safe: no GitLab project identifier; returning None",
            )
            return None
        url = getattr(platform_cfg, "url", "") or "gitlab.com"
        return GitLabPlatform(project_id=project_id, token=token, url=url)

    if platform_type == "gitea":
        try:
            from agentfox.platform.gitea import GiteaPlatform  # noqa: F401
        except ImportError:
            logger.debug(
                "create_platform_safe: Gitea platform not available; returning None",
            )
            return None
        # Gitea support not yet fully implemented
        logger.debug("create_platform_safe: Gitea platform not yet available; returning None")
        return None

    return None
