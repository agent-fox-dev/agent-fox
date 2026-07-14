"""GitLab REST API v4 platform implementation.

Provides ``GitLabPlatform``, a concrete implementation of
``PlatformProtocol`` targeting the GitLab REST API v4.

Token scope requirements
~~~~~~~~~~~~~~~~~~~~~~~~

The minimum required GitLab token scope is ``api`` (full read/write
REST API access).  Tokens with only ``read_api`` scope will receive
**403 Forbidden** errors on write operations (creating issues,
adding comments, creating labels, opening merge requests, etc.).

Requirements: 04-REQ-1.1 through 04-REQ-16.3
"""

from __future__ import annotations

import logging
import re
from urllib.parse import quote, urlsplit

import httpx

from agentfox.core.errors import IntegrationError
from agentfox.platform._http import request_with_retry
from agentfox.platform._ssrf import SSRFGuardTransport, _validate_url
from agentfox.platform.protocol import IssueComment, IssueResult

logger = logging.getLogger(__name__)

_MAX_ERROR_TEXT = 500

# Timeout for all GitLab API calls: 30s connect, 30s read/write.
_GITLAB_TIMEOUT = httpx.Timeout(connect=30.0, read=30.0, write=30.0, pool=30.0)

# Maximum number of attempts before giving up (1 initial + 2 retries).
_MAX_RETRIES = 3

# Base backoff in seconds; doubles on each retry (1s, 2s, 4s).
_RETRY_BACKOFF = 1.0


def _truncate_response(text: str) -> str:
    """Truncate API response text to avoid leaking verbose error details."""
    if len(text) <= _MAX_ERROR_TEXT:
        return text
    return text[:_MAX_ERROR_TEXT] + "..."


def _map_state(state: str) -> str:
    """Map protocol state values to GitLab API state values.

    GitLab uses ``"opened"`` instead of ``"open"``; ``"closed"``
    and ``"all"`` pass through unchanged.
    """
    if state == "open":
        return "opened"
    return state


def _issue_from_response(data: dict) -> IssueResult:
    """Map a GitLab API issue response dict to an ``IssueResult``.

    Field mapping:
        iid -> number, title -> title, web_url -> html_url,
        description -> body (default ""), labels array -> labels tuple
    """
    return IssueResult(
        number=data["iid"],
        title=data["title"],
        html_url=data["web_url"],
        body=data.get("description") or "",
        labels=tuple(data.get("labels", ())),
    )


class GitLabPlatform:
    """Concrete ``PlatformProtocol`` implementation for GitLab REST API v4.

    Each HTTP request creates a fresh ``httpx.AsyncClient`` via
    ``request_with_retry``; no persistent client state is held.

    Requirements: 04-REQ-1.1, 04-REQ-1.2, 04-REQ-1.3
    """

    forge_type: str = "gitlab"

    def __init__(
        self,
        project_id: str,
        token: str,
        url: str = "gitlab.com",
    ) -> None:
        _validate_url(url)
        self._encoded_project_id = quote(project_id, safe="")
        self._base_url = f"https://{url}/api/v4"
        self._headers: dict[str, str] = {"PRIVATE-TOKEN": token}

    # ------------------------------------------------------------------
    # Internal HTTP helper
    # ------------------------------------------------------------------

    async def _request(self, method: str, url: str, **kwargs: object) -> httpx.Response:
        """Execute an HTTP request with retry and SSRF guard.

        Delegates to the shared ``request_with_retry`` helper in
        ``agentfox.platform._http``.  Creates a new ``AsyncClient`` with
        ``_GITLAB_TIMEOUT`` and ``SSRFGuardTransport`` for each attempt.
        """
        return await request_with_retry(
            method,
            url,
            timeout=_GITLAB_TIMEOUT,
            transport=SSRFGuardTransport(),
            max_retries=_MAX_RETRIES,
            backoff_base=_RETRY_BACKOFF,
            **kwargs,
        )

    # ------------------------------------------------------------------
    # Issue operations (04-REQ-2 through 04-REQ-10)
    # ------------------------------------------------------------------

    async def create_issue(
        self,
        title: str,
        body: str,
        labels: list[str] | None = None,
    ) -> IssueResult:
        """Create a new issue on the GitLab project.

        Requirements: 04-REQ-2.1, 04-REQ-2.E1, 04-REQ-2.E2
        """
        url = f"{self._base_url}/projects/{self._encoded_project_id}/issues"
        payload: dict[str, str] = {
            "title": title,
            "description": body,
            "labels": ",".join(labels) if labels else "",
        }
        resp = await self._request("post", url, json=payload, headers=self._headers)
        if resp.status_code != 201:
            raise IntegrationError(
                f"GitLab create_issue failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )
        return _issue_from_response(resp.json())

    async def list_issues_by_label(
        self,
        label: str,
        state: str = "open",
        *,
        sort: str = "created",
        direction: str = "asc",
    ) -> list[IssueResult]:
        """List issues filtered by label, state, sort, and direction.

        Requirements: 04-REQ-3.1, 04-REQ-3.2, 04-REQ-3.3, 04-REQ-3.E1
        """
        sort_map = {"created": "created_at", "updated": "updated_at"}
        url = f"{self._base_url}/projects/{self._encoded_project_id}/issues"
        params = {
            "labels": label,
            "state": _map_state(state),
            "order_by": sort_map.get(sort, sort),
            "sort": direction,
            "per_page": 100,
        }
        resp = await self._request("get", url, params=params, headers=self._headers)
        if resp.status_code != 200:
            raise IntegrationError(
                f"GitLab list_issues_by_label failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )
        return [_issue_from_response(item) for item in resp.json()]

    async def add_issue_comment(
        self,
        issue_number: int,
        body: str,
    ) -> None:
        """Add a comment (note) to an issue.

        Requirements: 04-REQ-4.1, 04-REQ-4.E1
        """
        url = (
            f"{self._base_url}/projects/{self._encoded_project_id}"
            f"/issues/{issue_number}/notes"
        )
        resp = await self._request(
            "post", url, json={"body": body}, headers=self._headers
        )
        if resp.status_code != 201:
            raise IntegrationError(
                f"GitLab add_issue_comment failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )

    async def assign_label(
        self,
        issue_number: int,
        label: str,
    ) -> None:
        """Add a label to an issue.

        Requirements: 04-REQ-5.1, 04-REQ-5.E1
        """
        url = (
            f"{self._base_url}/projects/{self._encoded_project_id}"
            f"/issues/{issue_number}"
        )
        resp = await self._request(
            "put", url, json={"add_labels": label}, headers=self._headers
        )
        if resp.status_code != 200:
            raise IntegrationError(
                f"GitLab assign_label failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )

    async def close_issue(
        self,
        issue_number: int,
        comment: str | None = None,
    ) -> None:
        """Close an issue, optionally adding a final comment first.

        Requirements: 04-REQ-6.1, 04-REQ-6.2, 04-REQ-6.E1
        """
        if comment:
            await self.add_issue_comment(issue_number, comment)
        url = (
            f"{self._base_url}/projects/{self._encoded_project_id}"
            f"/issues/{issue_number}"
        )
        resp = await self._request(
            "put", url, json={"state_event": "close"}, headers=self._headers
        )
        if resp.status_code != 200:
            raise IntegrationError(
                f"GitLab close_issue failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )

    async def remove_label(
        self,
        issue_number: int,
        label: str,
    ) -> None:
        """Remove a label from an issue (idempotent).

        Requirements: 04-REQ-7.1, 04-REQ-7.E1
        """
        url = (
            f"{self._base_url}/projects/{self._encoded_project_id}"
            f"/issues/{issue_number}"
        )
        resp = await self._request(
            "put", url, json={"remove_labels": label}, headers=self._headers
        )
        if resp.status_code != 200:
            raise IntegrationError(
                f"GitLab remove_label failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )

    async def list_issue_comments(
        self,
        issue_number: int,
    ) -> list[IssueComment]:
        """List user-authored comments on an issue, excluding system notes.

        Requirements: 04-REQ-8.1, 04-REQ-8.2, 04-REQ-8.E1, 04-REQ-8.E2
        """
        url = (
            f"{self._base_url}/projects/{self._encoded_project_id}"
            f"/issues/{issue_number}/notes"
        )
        params = {
            "sort": "asc",
            "order_by": "created_at",
            "per_page": 100,
        }
        resp = await self._request("get", url, params=params, headers=self._headers)
        if resp.status_code != 200:
            raise IntegrationError(
                f"GitLab list_issue_comments failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )
        return [
            IssueComment(
                id=note["id"],
                body=note["body"],
                user=note["author"]["username"],
                created_at=note["created_at"],
            )
            for note in resp.json()
            if not note.get("system", False)
        ]

    async def get_issue(
        self,
        issue_number: int,
    ) -> IssueResult:
        """Fetch a single issue by project-internal ID.

        Requirements: 04-REQ-9.1, 04-REQ-9.E1
        """
        url = (
            f"{self._base_url}/projects/{self._encoded_project_id}"
            f"/issues/{issue_number}"
        )
        resp = await self._request("get", url, headers=self._headers)
        if resp.status_code != 200:
            raise IntegrationError(
                f"GitLab get_issue failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )
        return _issue_from_response(resp.json())

    async def update_issue(
        self,
        issue_number: int,
        body: str,
    ) -> None:
        """Update the body/description of an issue.

        Requirements: 04-REQ-10.1, 04-REQ-10.E1
        """
        url = (
            f"{self._base_url}/projects/{self._encoded_project_id}"
            f"/issues/{issue_number}"
        )
        resp = await self._request(
            "put", url, json={"description": body}, headers=self._headers
        )
        if resp.status_code != 200:
            raise IntegrationError(
                f"GitLab update_issue failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )

    # ------------------------------------------------------------------
    # Label operations (04-REQ-11)
    # ------------------------------------------------------------------

    async def create_label(
        self,
        name: str,
        color: str,
        description: str = "",
    ) -> None:
        """Create a label, treating 409 (already exists) as success.

        Requirements: 04-REQ-11.1, 04-REQ-11.2, 04-REQ-11.E1
        """
        url = f"{self._base_url}/projects/{self._encoded_project_id}/labels"
        payload = {
            "name": name,
            "color": "#" + color,
            "description": description,
        }
        resp = await self._request("post", url, json=payload, headers=self._headers)
        if resp.status_code in (201, 409):
            return None
        raise IntegrationError(
            f"GitLab create_label failed ({resp.status_code}): "
            f"{_truncate_response(resp.text)}"
        )

    # ------------------------------------------------------------------
    # Merge request operations (04-REQ-12)
    # ------------------------------------------------------------------

    async def create_pr(
        self,
        *,
        title: str,
        body: str,
        head: str,
        base: str,
    ) -> str:
        """Create a merge request, handling 409 duplicates via fallback GET.

        Requirements: 04-REQ-12.1, 04-REQ-12.2, 04-REQ-12.E1, 04-REQ-12.E2,
                      04-REQ-12.E3
        """
        url = f"{self._base_url}/projects/{self._encoded_project_id}/merge_requests"
        payload = {
            "title": title,
            "description": body,
            "source_branch": head,
            "target_branch": base,
        }
        resp = await self._request("post", url, json=payload, headers=self._headers)

        if resp.status_code == 201:
            return resp.json()["web_url"]

        if resp.status_code == 409:
            # Attempt to find the existing open MR
            fallback_params = {
                "source_branch": head,
                "target_branch": base,
                "state": "opened",
            }
            fallback = await self._request(
                "get", url, params=fallback_params, headers=self._headers
            )
            if fallback.status_code != 200:
                raise IntegrationError(
                    f"409 duplicate MR returned; fallback GET failed with "
                    f"{fallback.status_code}"
                )
            mrs = fallback.json()
            if not mrs:
                raise IntegrationError(
                    "409 duplicate MR returned but no existing open merge "
                    "request found"
                )
            return mrs[0]["web_url"]

        raise IntegrationError(
            f"GitLab create_pr failed ({resp.status_code}): "
            f"{_truncate_response(resp.text)}"
        )

    # ------------------------------------------------------------------
    # Lifecycle (04-REQ-13)
    # ------------------------------------------------------------------

    async def close(self) -> None:
        """No-op: GitLabPlatform holds no persistent state.

        Requirements: 04-REQ-13.1
        """

    # ------------------------------------------------------------------
    # Non-protocol methods (04-REQ-14, 04-REQ-15)
    # ------------------------------------------------------------------

    async def search_issues(
        self,
        title_prefix: str,
        state: str = "open",
    ) -> list[IssueResult]:
        """Search issues by title prefix.

        Requirements: 04-REQ-14.1, 04-REQ-14.E1
        """
        url = f"{self._base_url}/projects/{self._encoded_project_id}/issues"
        params = {
            "search": title_prefix,
            "state": _map_state(state),
            "per_page": 100,
        }
        resp = await self._request("get", url, params=params, headers=self._headers)
        if resp.status_code != 200:
            raise IntegrationError(
                f"GitLab search_issues failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )
        return [_issue_from_response(item) for item in resp.json()]

    async def check_credentials(self) -> None:
        """Verify token access to the project.

        Only raises on 401 or 403; returns normally for all other
        statuses (including 404 and 5xx), consistent with
        GitHubPlatform.check_credentials() behaviour.

        Requirements: 04-REQ-15.1, 04-REQ-15.E1, 04-REQ-15.E2
        """
        url = f"{self._base_url}/projects/{self._encoded_project_id}"
        resp = await self._request("get", url, headers=self._headers)
        if resp.status_code in (401, 403):
            raise IntegrationError(
                f"GitLab check_credentials failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )


# ------------------------------------------------------------------
# Remote URL parsing (04-REQ-16)
# ------------------------------------------------------------------

# Regex patterns for GitLab remote URLs
_HTTPS_RE = re.compile(
    r"^https://gitlab\.com/(.+?)(?:\.git)?$"
)
_SSH_RE = re.compile(
    r"^git@gitlab\.com:(.+?)(?:\.git)?$"
)


def parse_remote(remote_url: str) -> tuple[str, str] | None:
    """Parse a GitLab remote URL into ``(namespace_path, project_name)``.

    Supports HTTPS and SSH formats.  Returns ``None`` for any URL that
    does not match the expected GitLab patterns.  Never raises exceptions.

    Requirements: 04-REQ-16.1, 04-REQ-16.2, 04-REQ-16.3
    """
    try:
        # Check for port numbers in HTTPS URLs -> reject
        if remote_url.startswith("https://"):
            parsed = urlsplit(remote_url)
            if parsed.port is not None:
                return None

        for pattern in (_HTTPS_RE, _SSH_RE):
            m = pattern.match(remote_url)
            if m:
                path = m.group(1)
                parts = path.split("/")
                if len(parts) < 2:
                    return None
                namespace = "/".join(parts[:-1])
                project = parts[-1]
                return (namespace, project)
        return None
    except Exception:
        return None
