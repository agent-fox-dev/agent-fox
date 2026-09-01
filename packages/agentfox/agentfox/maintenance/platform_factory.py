"""Platform factory: instantiate platform from config.

Requirements: 61-REQ-8.3, 61-REQ-8.E1, 04-REQ-20.*, 04-REQ-21.*, 05-REQ-18.1, 05-REQ-18.2
"""

from __future__ import annotations

import logging
import os
import sys
from pathlib import Path

from afissues.gitea import GiteaPlatform
from afissues.gitea import parse_remote as parse_gitea_remote
from afissues.github import GitHubPlatform
from afissues.github import parse_remote as parse_github_remote
from afissues.protocol import PlatformProtocol

logger = logging.getLogger(__name__)

_SUPPORTED_PLATFORMS = {"github", "gitlab", "gitea"}

# Environment variable names for platform tokens.
_TOKEN_ENV_VARS: dict[str, str] = {
    "github": "GITHUB_PAT",
    "gitlab": "GITLAB_TOKEN",
    "gitea": "GITEA_TOKEN",
}

# Remote URL parsers keyed by platform type.
_REMOTE_PARSERS = {
    "github": parse_github_remote,
    "gitea": parse_gitea_remote,
}

# Default URLs for platforms that have a canonical host (None = required).
_DEFAULT_URLS: dict[str, str | None] = {
    "github": "github.com",
    "gitlab": "gitlab.com",
    "gitea": None,  # self-hosted; url must be provided in config
}


def _resolve_remote(
    project_root: Path,
    parse_fn: object,
) -> tuple[str, str]:
    """Detect owner/repo from ``git remote get-url origin``.

    Uses the platform-specific *parse_fn* to interpret the URL.
    Returns ``(owner, repo)`` on success, ``("owner", "repo")`` as fallback.
    """
    from agentfox.workspace.git import run_git_sync

    owner, repo = "owner", "repo"
    rc, stdout, _ = run_git_sync(["remote", "get-url", "origin"], cwd=project_root)
    if rc == 0:
        parsed = parse_fn(stdout.strip())  # type: ignore[operator]
        if parsed:
            owner, repo = parsed
    return owner, repo


# Keep the original helper for backward compatibility.
def _resolve_github_remote(project_root: Path) -> tuple[str, str]:
    """Detect owner/repo from ``git remote get-url origin`` (GitHub).

    Returns ``(owner, repo)`` on success, ``("owner", "repo")`` as fallback.
    """
    return _resolve_remote(project_root, parse_github_remote)


def _resolve_gitlab_remote(project_root: Path) -> str | None:
    """Detect GitLab project path from ``git remote get-url origin``.

    Returns ``"namespace/project"`` on success, ``None`` if the remote
    URL does not match a GitLab pattern.

    Requirements: 04-REQ-20.1
    """
    from afissues.gitlab import parse_remote as gitlab_parse_remote

    from agentfox.workspace.git import run_git_sync

    rc, stdout, _ = run_git_sync(["remote", "get-url", "origin"], cwd=project_root)
    if rc == 0:
        parsed = gitlab_parse_remote(stdout.strip())
        if parsed:
            namespace, project = parsed
            return f"{namespace}/{project}"
    return None


def _create_gitlab(platform_cfg: object, project_root: Path) -> PlatformProtocol:
    """Build a GitLabPlatform from config and environment.

    Raises ``SystemExit`` on missing token or project identifier.

    Requirements: 04-REQ-20.1, 04-REQ-20.E1, 04-REQ-20.E2
    """
    from afissues.gitlab import GitLabPlatform

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

    Raises ``SystemExit`` on missing token, URL, or project identifier.

    Requirements: 04-REQ-21.1, 04-REQ-21.2, 05-REQ-18.1, 05-REQ-18.2
    """
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
        parsed = parse_gitea_remote(stdout.strip())
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
                  04-REQ-21.1, 05-REQ-18.1
    """
    platform_cfg = getattr(config, "platform", None)
    platform_type = getattr(platform_cfg, "type", "none")

    if platform_type == "none":
        logger.error(
            "Night-shift requires a configured platform."
            " Set [platform] type = 'github' in your config."
        )
        sys.exit(1)

    if platform_type not in _SUPPORTED_PLATFORMS:
        logger.error(
            "Unsupported platform type '%s'. Supported types: %s",
            platform_type,
            ", ".join(sorted(_SUPPORTED_PLATFORMS)),
        )
        sys.exit(1)

    # GitLab uses project_id (not owner/repo); handle separately.
    if platform_type == "gitlab":
        return _create_gitlab(platform_cfg, project_root)

    token_var = _TOKEN_ENV_VARS[platform_type]
    token = os.environ.get(token_var, "")
    if not token.strip():
        logger.error("%s environment variable is required", token_var)
        sys.exit(1)

    parse_fn = _REMOTE_PARSERS[platform_type]
    owner, repo = _resolve_remote(project_root, parse_fn)

    url = getattr(platform_cfg, "url", "") or _DEFAULT_URLS.get(platform_type, "")
    if not url:
        logger.error(
            "Platform type '%s' requires a url in [platform] config",
            platform_type,
        )
        sys.exit(1)

    if platform_type == "gitea":
        return GiteaPlatform(owner=owner, repo=repo, token=token, url=url)
    # Default: github
    return GitHubPlatform(owner=owner, repo=repo, token=token, url=url)


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

    # GitLab uses project_id (not owner/repo); handle separately.
    if platform_type == "gitlab":
        from afissues.gitlab import GitLabPlatform

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

    token_var = _TOKEN_ENV_VARS.get(platform_type, "")
    if not token_var:
        return None
    token = os.environ.get(token_var, "")
    if not token:
        logger.debug("create_platform_safe: %s not set; returning None", token_var)
        return None

    parse_fn = _REMOTE_PARSERS.get(platform_type)
    if not parse_fn:
        return None
    owner, repo = _resolve_remote(project_root, parse_fn)

    url = getattr(platform_cfg, "url", "") or _DEFAULT_URLS.get(platform_type, "")
    if not url:
        logger.debug(
            "create_platform_safe: no url for platform type '%s'; returning None",
            platform_type,
        )
        return None

    if platform_type == "gitea":
        return GiteaPlatform(owner=owner, repo=repo, token=token, url=url)
    # Default: github
    return GitHubPlatform(owner=owner, repo=repo, token=token, url=url)
