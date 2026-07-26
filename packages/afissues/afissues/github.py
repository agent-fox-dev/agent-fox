"""GitHub platform implementation: PR and issue operations via the REST API.

Provides ``GitHubPlatform`` (the GitHub forge implementation of
``PlatformProtocol``), the ``parse_github_remote`` helper, and all
supporting retry infrastructure required for safe, reliable HTTP
communication with the GitHub REST API.

SSRF-guard utilities are imported from ``afissues._ssrf``.

Requirements: 03-REQ-3.1, 03-REQ-3.2, 03-REQ-3.3, 03-REQ-3.4,
              03-REQ-3.E1, 03-REQ-3.E2, 03-REQ-3.E3
"""

from __future__ import annotations

import logging
import re
from urllib.parse import quote

import httpx

from afissues._http import (
    _RETRYABLE_ERRORS,  # noqa: F401 – re-exported for backward compat
    _truncate_response,
    request_with_retry,
)
from afissues._ssrf import (
    SSRFGuardTransport,
    _check_address,  # noqa: F401 – re-exported for backward compat
    _validate_transport_address,  # noqa: F401 – re-exported for backward compat
    _validate_url,
)
from afissues.errors import ConfigError, IntegrationError  # noqa: F401 – ConfigError re-exported for SSRF callers
from afissues.protocol import IssueComment, IssueResult

logger = logging.getLogger(__name__)

# Backward-compatible aliases used by tests and gitea.py.
_validate_github_url = _validate_url
_SSRFGuardTransport = SSRFGuardTransport


# ---------------------------------------------------------------------------
# HTTP retry infrastructure (delegated to afissues._http)
# ---------------------------------------------------------------------------

# Timeout for all GitHub API calls: 30s connect, 30s read/write.
_GITHUB_TIMEOUT = httpx.Timeout(connect=30.0, read=30.0, write=30.0, pool=30.0)

# Maximum number of attempts before giving up (1 initial + 2 retries).
_MAX_RETRIES = 3

# Base backoff in seconds; doubles on each retry (0s, 1s, 2s, ...).
_RETRY_BACKOFF = 1.0


# ---------------------------------------------------------------------------
# GitHubPlatform
# ---------------------------------------------------------------------------


class GitHubPlatform:
    """GitHub platform using the REST API.

    Manages issues via the GitHub REST API, authenticated with a
    GITHUB_PAT environment variable.  Supports GitHub Enterprise via
    the ``url`` constructor parameter.

    Requirements: 65-REQ-4.2, 65-REQ-4.3, 65-REQ-5.1, 65-REQ-5.2,
                  65-REQ-5.3, 65-REQ-5.E1, 28-REQ-1.*, 28-REQ-2.*,
                  28-REQ-3.*, 28-REQ-4.*, 108-REQ-4.E2
    """

    # Forge identifier used by issue_summary.post_issue_summaries() to match
    # the source forge in a spec's prd.md against the configured platform.
    # 108-REQ-4.E2: skip posting when forge type doesn't match source URL.
    forge_type: str = "github"

    def __init__(self, owner: str, repo: str, token: str, url: str = "github.com") -> None:
        self._owner = owner
        self._repo = repo
        self._token = token
        self._url = url or "github.com"
        # Validate the URL before using it to prevent SSRF attacks.
        _validate_github_url(self._url)
        # Resolve API base URL -- github.com uses api.github.com; anything
        # else (e.g. GitHub Enterprise) uses https://{host}/api/v3.
        if self._url == "github.com":
            self._api_base = "https://api.github.com"
        else:
            self._api_base = f"https://{self._url}/api/v3"

    def __repr__(self) -> str:
        return f"GitHubPlatform(owner={self._owner!r}, repo={self._repo!r}, url={self._url!r})"

    def _auth_headers(self) -> dict[str, str]:
        """Build authentication headers for GitHub API requests."""
        return {
            "Authorization": f"Bearer {self._token}",
            "Accept": "application/vnd.github+json",
            "X-GitHub-Api-Version": "2022-11-28",
        }

    async def _request(self, method: str, url: str, **kwargs: object) -> httpx.Response:
        """Execute an HTTP request with explicit timeout and retry on transient errors.

        Delegates to the shared ``request_with_retry`` helper from
        ``afissues._http``.  Creates a new ``AsyncClient`` with
        ``_GITHUB_TIMEOUT`` and ``_SSRFGuardTransport`` for each attempt.
        Retries up to ``_MAX_RETRIES`` times on transport-level network
        exceptions.  HTTP-level error responses (4xx, 5xx) are returned
        as-is -- callers are responsible for raising on bad status codes.

        Requirements: 313-AC-1, 313-AC-2, 313-AC-3, 313-AC-4, 313-AC-5,
                      04-REQ-19.3
        """
        return await request_with_retry(
            method,
            url,
            timeout=_GITHUB_TIMEOUT,
            transport=_SSRFGuardTransport(),
            max_retries=_MAX_RETRIES,
            backoff_base=_RETRY_BACKOFF,
            **kwargs,
        )

    # ------------------------------------------------------------------
    # Issue operations (28-REQ-1.* through 28-REQ-4.*)
    # ------------------------------------------------------------------

    async def search_issues(
        self,
        title_prefix: str,
        state: str = "open",
    ) -> list[IssueResult]:
        """Search for issues by title prefix.

        Uses GET /search/issues with query:
        repo:{owner}/{repo} in:title {title_prefix} state:{state} type:issue

        Returns list of IssueResult, empty if none found.
        Raises IntegrationError on API error.

        Requirements: 28-REQ-1.1, 28-REQ-1.2, 28-REQ-1.3, 28-REQ-1.E1, 28-REQ-1.E2
        """
        headers = self._auth_headers()
        q = f"repo:{self._owner}/{self._repo} in:title {title_prefix} state:{state} type:issue"
        url = f"{self._api_base}/search/issues"
        resp = await self._request("get", url, params={"q": q}, headers=headers)
        if resp.status_code != 200:
            detail = _truncate_response(resp.text)
            logger.debug("Issue search response (%d): %s", resp.status_code, detail)
            raise IntegrationError(
                f"GitHub issue search failed ({resp.status_code})",
            )
        items = resp.json().get("items", [])
        results = [
            IssueResult(
                number=item["number"],
                title=item["title"],
                html_url=item["html_url"],
            )
            for item in items
        ]
        logger.debug(
            "Issue search for %r found %d result(s)",
            title_prefix,
            len(results),
        )
        return results

    async def create_issue(
        self,
        title: str,
        body: str,
        labels: list[str] | None = None,
    ) -> IssueResult:
        """Create a new issue.

        Uses POST /repos/{owner}/{repo}/issues.
        Returns IssueResult with the created issue's number, title, and URL.
        Raises IntegrationError on API error.

        Requirements: 28-REQ-2.1, 28-REQ-2.2, 28-REQ-2.E1
        """
        headers = self._auth_headers()
        url = f"{self._api_base}/repos/{self._owner}/{self._repo}/issues"
        payload: dict[str, object] = {"title": title, "body": body}
        if labels:
            payload["labels"] = labels
        resp = await self._request("post", url, json=payload, headers=headers)
        if resp.status_code != 201:
            detail = _truncate_response(resp.text)
            logger.debug("Issue creation response (%d): %s", resp.status_code, detail)
            raise IntegrationError(
                f"GitHub issue creation failed ({resp.status_code})",
            )
        data = resp.json()
        result = IssueResult(
            number=data["number"],
            title=data["title"],
            html_url=data["html_url"],
        )
        logger.info("Created issue #%d: %s", result.number, result.html_url)
        return result

    async def update_issue(
        self,
        issue_number: int,
        body: str,
    ) -> None:
        """Update an existing issue's body.

        Uses PATCH /repos/{owner}/{repo}/issues/{issue_number}.
        Raises IntegrationError on API error.

        Requirements: 28-REQ-3.1, 28-REQ-3.E1
        """
        headers = self._auth_headers()
        url = f"{self._api_base}/repos/{self._owner}/{self._repo}/issues/{issue_number}"
        payload = {"body": body}
        resp = await self._request("patch", url, json=payload, headers=headers)
        if resp.status_code != 200:
            detail = _truncate_response(resp.text)
            logger.debug("Issue update response (%d): %s", resp.status_code, detail)
            raise IntegrationError(
                f"GitHub issue update failed ({resp.status_code})",
            )
        logger.info("Updated issue #%d", issue_number)

    async def add_issue_comment(
        self,
        issue_number: int,
        comment: str,
    ) -> None:
        """Add a comment to an existing issue.

        Uses POST /repos/{owner}/{repo}/issues/{issue_number}/comments.
        Raises IntegrationError on API error.

        Requirements: 28-REQ-3.2, 28-REQ-3.E1
        """
        headers = self._auth_headers()
        url = f"{self._api_base}/repos/{self._owner}/{self._repo}/issues/{issue_number}/comments"
        payload = {"body": comment}
        resp = await self._request("post", url, json=payload, headers=headers)
        if resp.status_code != 201:
            detail = _truncate_response(resp.text)
            logger.debug("Issue comment response (%d): %s", resp.status_code, detail)
            raise IntegrationError(
                f"GitHub issue comment failed ({resp.status_code})",
            )
        logger.info("Added comment to issue #%d", issue_number)

    async def list_issues_by_label(
        self,
        label: str,
        state: str = "open",
        *,
        sort: str = "created",
        direction: str = "asc",
    ) -> list[IssueResult]:
        """List issues with a specific label.

        Uses GET /repos/{owner}/{repo}/issues with label filter.
        Returns list of IssueResult, empty if none found.
        Issues are requested sorted by ``sort`` in ``direction`` order;
        the result is also sorted locally by issue number ascending as a
        fallback for platforms that ignore sort parameters (71-REQ-1.E1).

        Requirements: 61-REQ-8.1, 71-REQ-1.1, 71-REQ-1.E1
        """
        headers = self._auth_headers()
        url = f"{self._api_base}/repos/{self._owner}/{self._repo}/issues"
        params = {
            "labels": label,
            "state": state,
            "per_page": "100",
            "sort": sort,
            "direction": direction,
        }
        resp = await self._request("get", url, params=params, headers=headers)
        if resp.status_code != 200:
            detail = _truncate_response(resp.text)
            logger.debug(
                "Issue list by label response (%d): %s",
                resp.status_code,
                detail,
            )
            raise IntegrationError(
                f"GitHub issue list failed ({resp.status_code})",
            )
        items = resp.json()
        results = [
            IssueResult(
                number=item["number"],
                title=item["title"],
                html_url=item["html_url"],
                body=item.get("body") or "",
                labels=tuple(lbl["name"] for lbl in item.get("labels", [])),
            )
            for item in items
            if "pull_request" not in item  # exclude PRs
        ]
        logger.debug("Issues with label %r: %d result(s)", label, len(results))
        return results

    async def assign_label(
        self,
        issue_number: int,
        label: str,
    ) -> None:
        """Assign a label to an issue.

        Uses POST /repos/{owner}/{repo}/issues/{issue_number}/labels.

        Requirements: 61-REQ-8.1
        """
        headers = self._auth_headers()
        url = f"{self._api_base}/repos/{self._owner}/{self._repo}/issues/{issue_number}/labels"
        payload = {"labels": [label]}
        resp = await self._request("post", url, json=payload, headers=headers)
        if resp.status_code not in (200, 201):
            detail = _truncate_response(resp.text)
            logger.debug(
                "Label assignment response (%d): %s",
                resp.status_code,
                detail,
            )
            raise IntegrationError(
                f"GitHub label assignment failed ({resp.status_code})",
            )
        logger.info("Assigned label %r to issue #%d", label, issue_number)

    async def remove_label(
        self,
        issue_number: int,
        label: str,
    ) -> None:
        """Remove a label from an issue.

        Uses DELETE /repos/{owner}/{repo}/issues/{issue_number}/labels/{label}.
        Succeeds silently if the label is not present (404).
        Raises IntegrationError on other API errors.

        Requirements: 86-REQ-1.1, 86-REQ-1.2, 86-REQ-1.E1
        """
        headers = self._auth_headers()
        encoded_label = quote(label, safe="")
        url = f"{self._api_base}/repos/{self._owner}/{self._repo}/issues/{issue_number}/labels/{encoded_label}"
        resp = await self._request("delete", url, headers=headers)
        if resp.status_code == 404:
            # Label not present -- succeed silently (idempotent)
            logger.debug(
                "Label %r not present on issue #%d, nothing to remove",
                label,
                issue_number,
            )
            return
        if resp.status_code not in (200, 204):
            detail = _truncate_response(resp.text)
            logger.debug(
                "Label removal response (%d): %s",
                resp.status_code,
                detail,
            )
            raise IntegrationError(
                f"GitHub label removal failed ({resp.status_code})",
            )
        logger.info("Removed label %r from issue #%d", label, issue_number)

    async def list_issue_comments(
        self,
        issue_number: int,
    ) -> list[IssueComment]:
        """List all comments on an issue in chronological order.

        Uses GET /repos/{owner}/{repo}/issues/{issue_number}/comments.
        Returns empty list if no comments exist.
        Raises IntegrationError on API error.

        Requirements: 86-REQ-1.3, 86-REQ-1.E2
        """
        headers = self._auth_headers()
        url = f"{self._api_base}/repos/{self._owner}/{self._repo}/issues/{issue_number}/comments"
        resp = await self._request("get", url, headers=headers)
        if resp.status_code != 200:
            detail = _truncate_response(resp.text)
            logger.debug(
                "Issue comments response (%d): %s",
                resp.status_code,
                detail,
            )
            raise IntegrationError(
                f"GitHub issue comments failed ({resp.status_code})",
            )
        items = resp.json()
        results = [
            IssueComment(
                id=item["id"],
                body=item.get("body") or "",
                user=item["user"]["login"],
                created_at=item["created_at"],
            )
            for item in items
        ]
        logger.debug("Issue #%d has %d comment(s)", issue_number, len(results))
        return results

    async def get_issue(
        self,
        issue_number: int,
    ) -> IssueResult:
        """Fetch a single issue by number.

        Uses GET /repos/{owner}/{repo}/issues/{issue_number}.
        Raises IntegrationError on 404 or other API error.

        Requirements: 86-REQ-1.4, 86-REQ-1.E3
        """
        headers = self._auth_headers()
        url = f"{self._api_base}/repos/{self._owner}/{self._repo}/issues/{issue_number}"
        resp = await self._request("get", url, headers=headers)
        if resp.status_code != 200:
            detail = _truncate_response(resp.text)
            logger.debug(
                "Get issue response (%d): %s",
                resp.status_code,
                detail,
            )
            raise IntegrationError(
                f"GitHub get issue failed ({resp.status_code})",
            )
        data = resp.json()
        result = IssueResult(
            number=data["number"],
            title=data["title"],
            html_url=data["html_url"],
            body=data.get("body") or "",
            labels=tuple(lbl["name"] for lbl in data.get("labels", [])),
        )
        logger.debug("Fetched issue #%d: %s", result.number, result.title)
        return result

    async def create_label(
        self,
        name: str,
        color: str,
        description: str = "",
    ) -> None:
        """Create a label on the repository.

        Uses POST /repos/{owner}/{repo}/labels.
        Treats 422 "already_exists" as success (idempotent).
        Raises IntegrationError on any other API error.

        Requirements: 358-REQ-1, 358-REQ-2
        """
        headers = self._auth_headers()
        url = f"{self._api_base}/repos/{self._owner}/{self._repo}/labels"
        payload = {"name": name, "color": color, "description": description}
        resp = await self._request("post", url, json=payload, headers=headers)
        if resp.status_code == 201:
            logger.info("Created label %r on %s/%s", name, self._owner, self._repo)
            return
        if resp.status_code == 422:
            # Check if this is an "already_exists" error -- treat as success.
            try:
                errors = resp.json().get("errors", [])
                if any(e.get("code") == "already_exists" for e in errors):
                    logger.debug(
                        "Label %r already exists on %s/%s, skipping creation",
                        name,
                        self._owner,
                        self._repo,
                    )
                    return
            except Exception:
                pass
        detail = _truncate_response(resp.text)
        logger.debug("Label creation response (%d): %s", resp.status_code, detail)
        raise IntegrationError(
            f"GitHub label creation failed ({resp.status_code})",
        )

    async def create_pr(
        self,
        *,
        title: str,
        body: str,
        head: str,
        base: str,
    ) -> str:
        """Create a pull request and return its ``html_url``.

        Sends ``POST /repos/{owner}/{repo}/pulls``.  On HTTP 201 returns the
        ``html_url`` from the response.  On HTTP 422 with a duplicate-PR
        message, queries the existing PR and returns its ``html_url``
        (idempotent).  All other errors raise ``IntegrationError``.

        Makes exactly one POST attempt -- no retry logic beyond what
        ``_request()`` provides for transport-level errors.  Transport
        exceptions (httpx timeouts, connection errors) propagate to the
        caller without being caught.

        Requirements: 02-REQ-7.1, 02-REQ-7.2, 02-REQ-7.3,
                      02-REQ-7.E1, 02-REQ-7.E2, 02-REQ-7.E3
        """
        headers = self._auth_headers()
        url = f"{self._api_base}/repos/{self._owner}/{self._repo}/pulls"
        payload = {"title": title, "body": body, "head": head, "base": base}
        resp = await self._request("post", url, json=payload, headers=headers)

        if resp.status_code == 201:
            data = resp.json()
            return data["html_url"]

        if resp.status_code == 422:
            # Check if this is a duplicate-PR error -- treat as idempotent
            # success, mirroring the pattern established by create_label().
            try:
                errors = resp.json().get("errors", [])
                is_duplicate = any("pull request already exists" in (e.get("message") or "").lower() for e in errors)
            except Exception:
                is_duplicate = False

            if is_duplicate:
                # Query the existing PR via GET /repos/{owner}/{repo}/pulls
                existing = await self._request(
                    "get",
                    url,
                    params={"head": f"{self._owner}:{head}", "base": base},
                    headers=headers,
                )
                prs = existing.json()
                if not prs:
                    raise IntegrationError(
                        f"GitHub PR creation returned 422 (duplicate) but "
                        f"no existing PR found for head={head!r} base={base!r}"
                    )
                return prs[0]["html_url"]

        detail = _truncate_response(resp.text)
        logger.debug("PR creation response (%d): %s", resp.status_code, detail)
        raise IntegrationError(
            f"GitHub PR creation failed ({resp.status_code})",
        )

    async def check_credentials(self) -> None:
        """Verify that the stored token has access to the configured repository.

        Makes a lightweight ``GET /repos/{owner}/{repo}`` call and raises
        ``IntegrationError`` when the response indicates an authentication or
        authorisation failure (401 Unauthorized or 403 Forbidden).  Returns
        normally on any other status code so callers can treat non-auth errors
        (e.g. 404 for a private repo the token cannot see) separately if
        needed.

        Intended as a pre-flight check at startup to surface bad credentials
        before entering the daemon loop.

        Requirements: 598-AC-1, 598-AC-3, 598-AC-4
        """
        headers = self._auth_headers()
        url = f"{self._api_base}/repos/{self._owner}/{self._repo}"
        resp = await self._request("get", url, headers=headers)
        if resp.status_code in (401, 403):
            raise IntegrationError(
                f"GitHub credential check failed ({resp.status_code}): "
                "check that GITHUB_PAT is set to a valid token with repository access",
            )

    async def close(self) -> None:
        """Clean up resources.

        No-op for the REST-based implementation (no persistent connections).

        Requirements: 61-REQ-8.1
        """

    async def close_issue(
        self,
        issue_number: int,
        comment: str | None = None,
    ) -> None:
        """Close an issue and optionally add a closing comment.

        Uses PATCH /repos/{owner}/{repo}/issues/{issue_number}
        with body {"state": "closed"}.
        If comment is provided, adds it before closing.
        Raises IntegrationError on API error.

        Requirements: 28-REQ-4.1, 28-REQ-4.E1
        """
        if comment is not None:
            await self.add_issue_comment(issue_number, comment)

        headers = self._auth_headers()
        url = f"{self._api_base}/repos/{self._owner}/{self._repo}/issues/{issue_number}"
        payload = {"state": "closed"}
        resp = await self._request("patch", url, json=payload, headers=headers)
        if resp.status_code != 200:
            detail = _truncate_response(resp.text)
            logger.debug("Issue close response (%d): %s", resp.status_code, detail)
            raise IntegrationError(
                f"GitHub issue close failed ({resp.status_code})",
            )
        logger.info("Closed issue #%d", issue_number)


# ---------------------------------------------------------------------------
# Remote URL parser
# ---------------------------------------------------------------------------


def parse_github_remote(remote_url: str) -> tuple[str, str] | None:
    """Extract (owner, repo) from a GitHub remote URL.

    Supports HTTPS and SSH formats:
      - https://github.com/owner/repo.git
      - git@github.com:owner/repo.git

    Returns None if the URL is not a recognized GitHub URL.

    Requirements: 19-REQ-4.4, 19-REQ-4.E4
    """
    pattern = r"github\.com[:/]([^/]+)/(.+?)(?:\.git)?$"
    match = re.search(pattern, remote_url)
    if match:
        return match.group(1), match.group(2)
    return None


# Backward-compatible alias used by platform_factory and other callers.
parse_remote = parse_github_remote
