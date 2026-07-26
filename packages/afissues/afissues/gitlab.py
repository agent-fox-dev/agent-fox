"""GitLab platform implementation: issue and MR operations via the REST API v4.

Requires a Personal Access Token with the ``api`` scope; the narrower
``read_api`` scope is insufficient and will result in 403 responses.

Requirements: 04-REQ-1.* through 04-REQ-17.*
"""

from __future__ import annotations

import logging
import re
from urllib.parse import quote

import httpx

from afissues._http import _truncate_response, request_with_retry
from afissues._ssrf import SSRFGuardTransport, _validate_url
from afissues.errors import IntegrationError
from afissues.protocol import IssueComment, IssueResult

logger = logging.getLogger(__name__)

_GITLAB_TIMEOUT = httpx.Timeout(connect=30.0, read=30.0, write=30.0, pool=30.0)
_MAX_RETRIES = 3
_RETRY_BACKOFF = 1.0

_STATE_MAP = {"open": "opened"}
_ORDER_BY_MAP = {"created": "created_at", "updated": "updated_at"}


def _map_issue(data: dict) -> IssueResult:
    return IssueResult(
        number=data["iid"],
        title=data["title"],
        html_url=data["web_url"],
        body=data.get("description") or "",
        labels=tuple(data.get("labels") or []),
    )


class GitLabPlatform:
    """GitLab platform using the REST API v4."""

    forge_type: str = "gitlab"

    def __init__(self, project_id: str, token: str, url: str = "gitlab.com") -> None:
        _validate_url(url)
        self._project_id = project_id
        self._encoded_project_id = quote(project_id, safe="")
        self._base_url = f"https://{url}/api/v4"
        self._headers = {"PRIVATE-TOKEN": token}

    def __repr__(self) -> str:
        return f"GitLabPlatform(project_id={self._project_id!r}, base_url={self._base_url!r})"

    async def _request(self, method: str, url: str, **kwargs: object) -> httpx.Response:
        return await request_with_retry(
            method,
            url,
            timeout=_GITLAB_TIMEOUT,
            transport=SSRFGuardTransport(),
            max_retries=_MAX_RETRIES,
            backoff_base=_RETRY_BACKOFF,
            **kwargs,
        )

    def _project_url(self, path: str = "") -> str:
        return f"{self._base_url}/projects/{self._encoded_project_id}{path}"

    # -- Issue CRUD -----------------------------------------------------------

    async def create_issue(
        self, title: str, body: str, labels: list[str] | None = None
    ) -> IssueResult:
        payload: dict[str, object] = {"title": title, "description": body}
        if labels:
            payload["labels"] = ",".join(labels)
        resp = await self._request(
            "post", self._project_url("/issues"), json=payload, headers=self._headers
        )
        if resp.status_code != 201:
            raise IntegrationError(
                f"GitLab issue creation failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
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
        params = {
            "labels": label,
            "state": _STATE_MAP.get(state, state),
            "order_by": _ORDER_BY_MAP.get(sort, sort),
            "sort": direction,
            "per_page": 100,
        }
        resp = await self._request(
            "get", self._project_url("/issues"), params=params, headers=self._headers
        )
        if resp.status_code != 200:
            raise IntegrationError(
                f"GitLab issue list failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )
        return [_map_issue(item) for item in resp.json()]

    async def add_issue_comment(self, issue_number: int, body: str) -> None:
        resp = await self._request(
            "post",
            self._project_url(f"/issues/{issue_number}/notes"),
            json={"body": body},
            headers=self._headers,
        )
        if resp.status_code != 201:
            raise IntegrationError(
                f"GitLab comment creation failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )

    async def assign_label(self, issue_number: int, label: str) -> None:
        resp = await self._request(
            "put",
            self._project_url(f"/issues/{issue_number}"),
            json={"add_labels": label},
            headers=self._headers,
        )
        if resp.status_code != 200:
            raise IntegrationError(
                f"GitLab label assignment failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )

    async def close_issue(self, issue_number: int, comment: str | None = None) -> None:
        if comment:
            await self.add_issue_comment(issue_number, comment)
        resp = await self._request(
            "put",
            self._project_url(f"/issues/{issue_number}"),
            json={"state_event": "close"},
            headers=self._headers,
        )
        if resp.status_code != 200:
            raise IntegrationError(
                f"GitLab issue close failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )

    async def remove_label(self, issue_number: int, label: str) -> None:
        resp = await self._request(
            "put",
            self._project_url(f"/issues/{issue_number}"),
            json={"remove_labels": label},
            headers=self._headers,
        )
        if resp.status_code != 200:
            raise IntegrationError(
                f"GitLab label removal failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )

    async def list_issue_comments(self, issue_number: int) -> list[IssueComment]:
        params = {"sort": "asc", "order_by": "created_at", "per_page": 100}
        resp = await self._request(
            "get",
            self._project_url(f"/issues/{issue_number}/notes"),
            params=params,
            headers=self._headers,
        )
        if resp.status_code != 200:
            raise IntegrationError(
                f"GitLab issue comments failed ({resp.status_code}): "
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

    async def get_issue(self, issue_number: int) -> IssueResult:
        resp = await self._request(
            "get",
            self._project_url(f"/issues/{issue_number}"),
            headers=self._headers,
        )
        if resp.status_code != 200:
            raise IntegrationError(
                f"GitLab get issue failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )
        return _map_issue(resp.json())

    async def update_issue(self, issue_number: int, body: str) -> None:
        resp = await self._request(
            "put",
            self._project_url(f"/issues/{issue_number}"),
            json={"description": body},
            headers=self._headers,
        )
        if resp.status_code != 200:
            raise IntegrationError(
                f"GitLab issue update failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )

    # -- Labels ---------------------------------------------------------------

    async def create_label(self, name: str, color: str, description: str = "") -> None:
        payload = {"name": name, "color": f"#{color}", "description": description}
        resp = await self._request(
            "post", self._project_url("/labels"), json=payload, headers=self._headers
        )
        if resp.status_code == 201:
            return None
        if resp.status_code == 409:
            return None
        raise IntegrationError(
            f"GitLab label creation failed ({resp.status_code}): "
            f"{_truncate_response(resp.text)}"
        )

    # -- Merge Requests -------------------------------------------------------

    async def create_pr(self, *, title: str, body: str, head: str, base: str) -> str:
        payload = {
            "source_branch": head,
            "target_branch": base,
            "title": title,
            "description": body,
        }
        resp = await self._request(
            "post",
            self._project_url("/merge_requests"),
            json=payload,
            headers=self._headers,
        )
        if resp.status_code == 201:
            return resp.json()["web_url"]
        if resp.status_code == 409:
            params = {
                "source_branch": head,
                "target_branch": base,
                "state": "opened",
            }
            fallback = await self._request(
                "get",
                self._project_url("/merge_requests"),
                params=params,
                headers=self._headers,
            )
            if fallback.status_code != 200:
                raise IntegrationError(
                    f"GitLab MR creation returned 409 and fallback GET failed "
                    f"({fallback.status_code}): {_truncate_response(fallback.text)}"
                )
            mrs = fallback.json()
            if not mrs:
                raise IntegrationError(
                    f"GitLab MR creation returned 409 (duplicate) but no existing "
                    f"open MR found for source={head!r} target={base!r}"
                )
            return mrs[0]["web_url"]
        raise IntegrationError(
            f"GitLab MR creation failed ({resp.status_code}): "
            f"{_truncate_response(resp.text)}"
        )

    # -- Non-protocol methods -------------------------------------------------

    async def search_issues(
        self, query: str, state: str = "open"
    ) -> list[IssueResult]:
        params = {
            "search": query,
            "state": _STATE_MAP.get(state, state),
            "per_page": 100,
        }
        resp = await self._request(
            "get", self._project_url("/issues"), params=params, headers=self._headers
        )
        if resp.status_code != 200:
            raise IntegrationError(
                f"GitLab issue search failed ({resp.status_code}): "
                f"{_truncate_response(resp.text)}"
            )
        return [_map_issue(item) for item in resp.json()]

    async def check_credentials(self) -> None:
        resp = await self._request(
            "get",
            f"{self._base_url}/projects/{self._encoded_project_id}",
            headers=self._headers,
        )
        if resp.status_code in (401, 403):
            raise IntegrationError(
                f"GitLab credential check failed ({resp.status_code}): "
                "check that GITLAB_TOKEN is set to a valid token with api scope"
            )

    async def close(self) -> None:
        pass


# -- Remote URL parsing -------------------------------------------------------

_GITLAB_HTTPS_RE = re.compile(r"^https://gitlab\.com/(.+?)/([^/]+?)(?:\.git)?$")
_GITLAB_SSH_RE = re.compile(r"^git@gitlab\.com:(.+?)/([^/]+?)(?:\.git)?$")


def parse_remote(remote_url: str) -> tuple[str, str] | None:
    """Extract (namespace, project) from a GitLab remote URL.

    Requirements: 04-REQ-16.1, 04-REQ-16.2, 04-REQ-16.3
    """
    if not remote_url:
        return None
    if "gitlab.com:" in remote_url and not remote_url.startswith("git@"):
        return None
    if "gitlab.com:8080" in remote_url or ":8080" in remote_url:
        return None
    for pattern in (_GITLAB_HTTPS_RE, _GITLAB_SSH_RE):
        m = pattern.match(remote_url)
        if m:
            namespace, project = m.group(1), m.group(2)
            if namespace and project:
                return (namespace, project)
    return None
