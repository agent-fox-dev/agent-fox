"""GiteaPlatform: Gitea REST API v1 issue and PR operations.

Implements PlatformProtocol for Gitea-hosted repositories, providing
issue creation, label management, comment handling, and pull request
operations via the Gitea REST API v1.

Follows the same HTTP integration pattern as GitLabPlatform: SSRF
validation at construction, SSRFGuardTransport for request-time
DNS re-checks, and retry-on-transient-error via request_with_retry.
All protocol methods delegate to ``self._request()`` which wraps
``request_with_retry`` from ``afissues._http``.

This module is self-contained: it imports only from ``afissues.errors``,
``afissues.protocol``, ``afissues._http``, ``afissues._ssrf``,
``httpx``, and the standard library.

Requirements: 05-REQ-1.* through 05-REQ-19.*
"""

from __future__ import annotations

import logging
import re

import httpx

from afissues._http import request_with_retry
from afissues._ssrf import SSRFGuardTransport, _validate_url
from afissues.errors import IntegrationError
from afissues.protocol import IssueComment, IssueResult, PrResult

logger = logging.getLogger(__name__)

_MAX_ERROR_TEXT = 500

# Timeout for all Gitea API calls: 30s connect, 30s read/write.
_GITEA_TIMEOUT = httpx.Timeout(connect=30.0, read=30.0, write=30.0, pool=30.0)

# Maximum number of attempts before giving up (1 initial + 2 retries).
_MAX_RETRIES = 3

# Base backoff in seconds; doubles on each retry (0s, 1s, 2s, ...).
_RETRY_BACKOFF = 1.0

# Sort+direction mapping from protocol params to Gitea combined sort values.
_SORT_MAP: dict[tuple[str, str], str] = {
    ("created", "asc"): "oldest",
    ("created", "desc"): "newest",
    ("updated", "asc"): "leastupdate",
    ("updated", "desc"): "recentupdate",
}


def _truncate_response(text: str) -> str:
    """Truncate API response text to avoid leaking verbose error details."""
    if len(text) <= _MAX_ERROR_TEXT:
        return text
    return text[:_MAX_ERROR_TEXT] + "..."


def _map_issue(data: dict) -> IssueResult:
    """Map a Gitea issue JSON object to an IssueResult."""
    return IssueResult(
        number=data["number"],
        title=data["title"],
        html_url=data["html_url"],
        body=data.get("body") or "",
        labels=tuple(lbl["name"] for lbl in data.get("labels") or []),
    )


class GiteaPlatform:
    """Gitea platform using the REST API v1.

    Manages issues, labels, comments, and pull requests via the Gitea
    REST API, authenticated with a token.

    Requirements: 05-REQ-1.* through 05-REQ-19.*
    """

    forge_type: str = "gitea"

    def __init__(self, owner: str, repo: str, token: str, url: str) -> None:
        # SSRF guard -- must run before any other initialization.
        # ConfigError propagates directly to the caller (05-REQ-1.E1).
        _validate_url(url)

        self._owner = owner
        self._repo = repo
        self._base_url = f"https://{url}/api/v1"
        self._auth_headers: dict[str, str] = {"Authorization": f"token {token}"}
        self._label_cache: dict[str, int] = {}
        self._cache_populated: bool = False

    def __repr__(self) -> str:
        return f"GiteaPlatform(owner={self._owner!r}, repo={self._repo!r}, base_url={self._base_url!r})"

    async def _request(self, method: str, url: str, **kwargs: object) -> httpx.Response:
        """Execute an HTTP request with retry on transient network errors.

        Delegates to the shared ``request_with_retry`` helper from
        ``afissues._http``.  Creates a new ``AsyncClient`` with
        ``_GITEA_TIMEOUT`` and ``SSRFGuardTransport`` for each attempt.

        All protocol methods route through this method to ensure
        consistent retry behaviour matching GitLabPlatform.

        Requirements: 05-REQ-1.5
        """
        return await request_with_retry(
            method,
            url,
            timeout=_GITEA_TIMEOUT,
            transport=SSRFGuardTransport(),
            max_retries=_MAX_RETRIES,
            backoff_base=_RETRY_BACKOFF,
            **kwargs,
        )

    # ------------------------------------------------------------------
    # Label cache (05-REQ-2.*)
    # ------------------------------------------------------------------

    async def _resolve_label_id(self, label_name: str) -> int:
        """Resolve a label name to its numeric Gitea ID.

        Uses an in-memory cache to avoid repeated API calls.  On cache
        miss, fetches all labels and populates the cache.  Once the
        cache has been fully populated, missing labels raise
        IntegrationError immediately without re-fetching.

        Requirements: 05-REQ-2.1, 05-REQ-2.2, 05-REQ-2.3, 05-REQ-2.4
        """
        # Cache hit -- return immediately (05-REQ-2.1).
        if label_name in self._label_cache:
            return self._label_cache[label_name]

        # Cache already populated -- label does not exist (05-REQ-2.4).
        if self._cache_populated:
            raise IntegrationError(
                f"Label {label_name!r} not found in repo {self._owner}/{self._repo}",
            )

        # Cache miss -- fetch all labels and populate (05-REQ-2.2).
        url = f"{self._base_url}/repos/{self._owner}/{self._repo}/labels"
        resp = await self._request("get", url, headers=self._auth_headers)

        if resp.status_code < 200 or resp.status_code >= 300:
            raise IntegrationError(
                f"Failed to fetch labels ({resp.status_code}): {_truncate_response(resp.text)}",
            )

        for item in resp.json():
            self._label_cache[item["name"]] = item["id"]
        self._cache_populated = True

        # Check if the requested label was found (05-REQ-2.3).
        if label_name not in self._label_cache:
            raise IntegrationError(
                f"Label {label_name!r} not found in repo {self._owner}/{self._repo}",
            )

        return self._label_cache[label_name]

    # ------------------------------------------------------------------
    # Issue operations (05-REQ-3.* through 05-REQ-11.*)
    # ------------------------------------------------------------------

    async def create_issue(
        self,
        title: str,
        body: str,
        labels: list[str] | None = None,
    ) -> IssueResult:
        """Create a new Gitea issue with optional label assignments.

        Requirements: 05-REQ-3.1, 05-REQ-3.2, 05-REQ-3.3
        """
        payload: dict = {"title": title, "body": body}

        if labels:
            label_ids = [await self._resolve_label_id(name) for name in labels]
            payload["labels"] = label_ids

        url = f"{self._base_url}/repos/{self._owner}/{self._repo}/issues"
        resp = await self._request("post", url, json=payload, headers=self._auth_headers)

        if resp.status_code < 200 or resp.status_code >= 300:
            raise IntegrationError(
                f"Failed to create issue ({resp.status_code}): {_truncate_response(resp.text)}",
            )

        return _map_issue(resp.json())

    async def list_issues_by_label(
        self,
        label: str,
        state: str = "open",
        *,
        sort: str = "created",
        direction: str = "asc",
    ) -> list[IssueResult]:
        """List issues filtered by label with Gitea sort mapping.

        Requirements: 05-REQ-4.1, 05-REQ-4.2, 05-REQ-4.3, 05-REQ-4.4
        """
        gitea_sort = _SORT_MAP.get((sort, direction), "newest")

        params = {
            "labels": label,
            "state": state,
            "type": "issues",
            "sort": gitea_sort,
            "limit": 50,
        }

        url = f"{self._base_url}/repos/{self._owner}/{self._repo}/issues"
        resp = await self._request("get", url, params=params, headers=self._auth_headers)

        if resp.status_code < 200 or resp.status_code >= 300:
            raise IntegrationError(
                f"Failed to list issues ({resp.status_code}): {_truncate_response(resp.text)}",
            )

        return [_map_issue(item) for item in resp.json()]

    async def add_issue_comment(
        self,
        issue_number: int,
        body: str,
    ) -> None:
        """Post a comment on a Gitea issue.

        Requirements: 05-REQ-5.1, 05-REQ-5.2
        """
        url = f"{self._base_url}/repos/{self._owner}/{self._repo}/issues/{issue_number}/comments"
        resp = await self._request("post", url, json={"body": body}, headers=self._auth_headers)

        if resp.status_code < 200 or resp.status_code >= 300:
            raise IntegrationError(
                f"Failed to add comment ({resp.status_code}): {_truncate_response(resp.text)}",
            )

    async def assign_label(
        self,
        issue_number: int,
        label: str,
    ) -> None:
        """Assign a label to a Gitea issue by numeric ID.

        Requirements: 05-REQ-6.1
        """
        label_id = await self._resolve_label_id(label)

        url = f"{self._base_url}/repos/{self._owner}/{self._repo}/issues/{issue_number}/labels"
        resp = await self._request(
            "post", url, json={"labels": [label_id]}, headers=self._auth_headers
        )

        if resp.status_code < 200 or resp.status_code >= 300:
            raise IntegrationError(
                f"Failed to assign label ({resp.status_code}): {_truncate_response(resp.text)}",
            )

    async def close_issue(
        self,
        issue_number: int,
        comment: str | None = None,
    ) -> None:
        """Close a Gitea issue with an optional comment.

        Requirements: 05-REQ-7.1, 05-REQ-7.2, 05-REQ-7.3
        """
        if comment is not None:
            await self.add_issue_comment(issue_number, comment)

        url = f"{self._base_url}/repos/{self._owner}/{self._repo}/issues/{issue_number}"
        resp = await self._request(
            "patch", url, json={"state": "closed"}, headers=self._auth_headers
        )

        if resp.status_code < 200 or resp.status_code >= 300:
            raise IntegrationError(
                f"Failed to close issue ({resp.status_code}): {_truncate_response(resp.text)}",
            )

    async def remove_label(
        self,
        issue_number: int,
        label: str,
    ) -> None:
        """Remove a label from a Gitea issue idempotently.

        Requirements: 05-REQ-8.1, 05-REQ-8.2, 05-REQ-8.3, 05-REQ-8.4
        """
        try:
            label_id = await self._resolve_label_id(label)
        except IntegrationError:
            return  # Label doesn't exist in repo -- silently succeed.

        url = (
            f"{self._base_url}/repos/{self._owner}/{self._repo}"
            f"/issues/{issue_number}/labels/{label_id}"
        )
        resp = await self._request("delete", url, headers=self._auth_headers)

        if resp.status_code in (204, 404, 422):
            return  # Success or idempotent (label not on issue).

        raise IntegrationError(
            f"Failed to remove label ({resp.status_code}): {_truncate_response(resp.text)}",
        )

    async def list_issue_comments(
        self,
        issue_number: int,
    ) -> list[IssueComment]:
        """Retrieve all comments on a Gitea issue.

        Requirements: 05-REQ-9.1, 05-REQ-9.2
        """
        url = f"{self._base_url}/repos/{self._owner}/{self._repo}/issues/{issue_number}/comments"
        resp = await self._request("get", url, headers=self._auth_headers)

        if resp.status_code < 200 or resp.status_code >= 300:
            raise IntegrationError(
                f"Failed to list comments ({resp.status_code}): {_truncate_response(resp.text)}",
            )

        return [
            IssueComment(
                id=item["id"],
                body=item["body"],
                user=item["user"]["login"],
                created_at=item["created_at"],
            )
            for item in resp.json()
        ]

    async def get_issue(
        self,
        issue_number: int,
    ) -> IssueResult:
        """Fetch a single Gitea issue by number.

        Requirements: 05-REQ-10.1
        """
        url = f"{self._base_url}/repos/{self._owner}/{self._repo}/issues/{issue_number}"
        resp = await self._request("get", url, headers=self._auth_headers)

        if resp.status_code < 200 or resp.status_code >= 300:
            raise IntegrationError(
                f"Failed to get issue ({resp.status_code}): {_truncate_response(resp.text)}",
            )

        return _map_issue(resp.json())

    async def update_issue(
        self,
        issue_number: int,
        body: str,
    ) -> None:
        """Update the body of an existing Gitea issue.

        Requirements: 05-REQ-11.1, 05-REQ-11.2
        """
        url = f"{self._base_url}/repos/{self._owner}/{self._repo}/issues/{issue_number}"
        resp = await self._request(
            "patch", url, json={"body": body}, headers=self._auth_headers
        )

        if resp.status_code < 200 or resp.status_code >= 300:
            raise IntegrationError(
                f"Failed to update issue ({resp.status_code}): {_truncate_response(resp.text)}",
            )

    async def create_label(
        self,
        name: str,
        color: str,
        description: str = "",
    ) -> None:
        """Create a repo label idempotently with color prefix.

        Requirements: 05-REQ-12.1, 05-REQ-12.2, 05-REQ-12.3, 05-REQ-12.4
        """
        try:
            await self._resolve_label_id(name)
            return  # Label already exists -- no-op (05-REQ-12.1).
        except IntegrationError:
            pass  # Label not found -- proceed to create.

        url = f"{self._base_url}/repos/{self._owner}/{self._repo}/labels"
        payload = {
            "name": name,
            "color": f"#{color}",
            "description": description,
        }
        resp = await self._request("post", url, json=payload, headers=self._auth_headers)

        if resp.status_code < 200 or resp.status_code >= 300:
            raise IntegrationError(
                f"Failed to create label ({resp.status_code}): {_truncate_response(resp.text)}",
            )

        # Single-entry cache insert -- no full re-fetch (05-REQ-12.3).
        data = resp.json()
        self._label_cache[name] = data["id"]

    async def create_pr(
        self,
        *,
        title: str,
        body: str,
        head: str,
        base: str,
    ) -> PrResult:
        """Open a pull request on Gitea with duplicate detection.

        Requirements: 05-REQ-13.1, 05-REQ-13.2, 05-REQ-13.3
        """
        url = f"{self._base_url}/repos/{self._owner}/{self._repo}/pulls"
        payload = {"title": title, "body": body, "head": head, "base": base}

        resp = await self._request("post", url, json=payload, headers=self._auth_headers)

        if 200 <= resp.status_code < 300:
            data = resp.json()
            return PrResult(html_url=data["html_url"], number=data["number"])

        if resp.status_code == 409:
            # Duplicate PR -- look up existing open PR (05-REQ-13.2).
            params = {"head": head, "base": base, "state": "open"}
            get_resp = await self._request(
                "get", url, params=params, headers=self._auth_headers
            )

            if get_resp.status_code < 200 or get_resp.status_code >= 300:
                raise IntegrationError(
                    f"Failed to find existing PR ({get_resp.status_code}): "
                    f"{_truncate_response(get_resp.text)}",
                )

            existing = get_resp.json()
            if not existing:
                raise IntegrationError(
                    f"409 duplicate returned but no existing open PR found "
                    f"for head={head} base={base}",
                )
            return PrResult(html_url=existing[0]["html_url"], number=existing[0]["number"])

        raise IntegrationError(
            f"Failed to create PR ({resp.status_code}): {_truncate_response(resp.text)}",
        )

    async def search_issues(
        self,
        title_prefix: str,
        state: str = "open",
    ) -> list[IssueResult]:
        """Search Gitea issues by title prefix keyword.

        Requirements: 05-REQ-15.1, 05-REQ-15.2
        """
        params = {
            "q": title_prefix,
            "type": "issues",
            "state": state,
            "limit": 50,
        }

        url = f"{self._base_url}/repos/{self._owner}/{self._repo}/issues"
        resp = await self._request("get", url, params=params, headers=self._auth_headers)

        if resp.status_code < 200 or resp.status_code >= 300:
            raise IntegrationError(
                f"Failed to search issues ({resp.status_code}): {_truncate_response(resp.text)}",
            )

        return [_map_issue(item) for item in resp.json()]

    async def check_credentials(self) -> None:
        """Validate Gitea API token against the repository.

        Requirements: 05-REQ-16.1, 05-REQ-16.2, 05-REQ-16.3, 05-REQ-16.4
        """
        url = f"{self._base_url}/repos/{self._owner}/{self._repo}"
        resp = await self._request("get", url, headers=self._auth_headers)

        if resp.status_code in (401, 403):
            raise IntegrationError(
                f"Authentication failed ({resp.status_code})",
            )

    async def close(self) -> None:
        """No-op close satisfying PlatformProtocol.

        Requirements: 05-REQ-14.1
        """


# ------------------------------------------------------------------
# Remote URL parser (05-REQ-17.*)
# ------------------------------------------------------------------

# Matches any-hostname HTTPS and SSH remote URLs:
#   https://host/owner/repo[.git]
#   git@host:owner/repo[.git]
_REMOTE_RE = re.compile(
    r"(?:https?://[^/]+/|[^@]+@[^:]+:)"  # protocol or SSH prefix
    r"([^/]+)/([^/]+?)(?:\.git)?$"  # owner / repo [.git]
)


def parse_remote(remote_url: str) -> tuple[str, str] | None:
    """Extract (owner, repo) from a Gitea remote URL.

    Accepts any hostname -- the platform factory determines which
    parser to invoke based on configured platform type.

    Returns None for URLs that cannot be parsed.

    Requirements: 05-REQ-17.1, 05-REQ-17.2, 05-REQ-17.3, 05-REQ-17.4
    """
    m = _REMOTE_RE.match(remote_url)
    if m is None:
        return None
    return (m.group(1), m.group(2))
