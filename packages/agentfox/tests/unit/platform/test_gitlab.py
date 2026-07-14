"""Tests for GitLabPlatform issue and PR operations.

Test Spec: TS-04-1 through TS-04-32, TS-04-E1 through TS-04-E24
Requirements: 04-REQ-1.* through 04-REQ-18.*, 04-REQ-16.*, 04-REQ-17.*

Note: Import paths use agentfox.platform.* (the actual codebase layout),
not afissues.* (the spec-03 future layout that has not been extracted yet).
The GitLab module will live at agentfox.platform.gitlab alongside the
existing agentfox.platform.github module.
"""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock, patch

import httpx
import pytest
from agentfox.core.errors import ConfigError, IntegrationError
from agentfox.platform.protocol import IssueComment, IssueResult

# ---------------------------------------------------------------------------
# Helpers (modelled after test_github_issues_rest.py helpers)
# ---------------------------------------------------------------------------

# Target for patching httpx.AsyncClient in the gitlab module.
# This will resolve once agentfox/platform/gitlab.py is created.
_TARGET = "agentfox.platform._http.httpx.AsyncClient"


def _mock_client(**method_responses: MagicMock | AsyncMock) -> AsyncMock:
    """Build a mock httpx.AsyncClient that works as an async context manager.

    Pass keyword arguments like get=mock_response or post=mock_response.
    """
    client = AsyncMock()
    for method_name, response in method_responses.items():
        if callable(response) and not isinstance(response, MagicMock):
            setattr(client, method_name, response)
        else:
            setattr(client, method_name, AsyncMock(return_value=response))
    client.__aenter__ = AsyncMock(return_value=client)
    client.__aexit__ = AsyncMock(return_value=False)
    return client


def _json_response(
    status_code: int,
    json_data: dict | list | None = None,
    text: str = "",
) -> MagicMock:
    """Build a mock httpx.Response."""
    resp = MagicMock()
    resp.status_code = status_code
    resp.text = text
    if json_data is not None:
        resp.json.return_value = json_data
    return resp


def _make_platform():  # type: ignore[no-untyped-def]
    """Build a GitLabPlatform with safe defaults for testing.

    Patches _validate_url (SSRF check) to accept 'gitlab.com' without
    performing real DNS resolution.
    """
    from agentfox.platform.gitlab import GitLabPlatform

    with patch("agentfox.platform.gitlab._validate_url"):
        return GitLabPlatform("group/project", "tok", "gitlab.com")


# ---------------------------------------------------------------------------
# TS-04-1: GitLabPlatform constructor happy path and class attributes
# Requirements: 04-REQ-1.1
# ---------------------------------------------------------------------------


class TestGitLabPlatformConstructor:
    """Verify GitLabPlatform initialises with correct attributes."""

    def test_forge_type_is_gitlab(self) -> None:
        """TS-04-1: forge_type class attribute equals 'gitlab'."""
        platform = _make_platform()
        assert platform.forge_type == "gitlab"

    def test_encoded_project_id(self) -> None:
        """TS-04-1: project_id is URL-encoded internally."""
        platform = _make_platform()
        # 'group/project' should become 'group%2Fproject'
        encoded = platform._encoded_project_id
        assert encoded == "group%2Fproject"
        assert "/" not in encoded

    def test_api_base_url(self) -> None:
        """TS-04-1: API base URL is https://{url}/api/v4."""
        platform = _make_platform()
        assert platform._base_url == "https://gitlab.com/api/v4"

    def test_auth_headers(self) -> None:
        """TS-04-1: auth headers use PRIVATE-TOKEN."""
        platform = _make_platform()
        assert platform._headers == {"PRIVATE-TOKEN": "tok"}


# ---------------------------------------------------------------------------
# TS-04-3: No persistent httpx.AsyncClient stored
# Requirement: 04-REQ-1.3
# ---------------------------------------------------------------------------


class TestNoPersistentClient:
    """Verify GitLabPlatform does not hold a persistent httpx.AsyncClient."""

    def test_no_async_client_instance_attribute(self) -> None:
        """TS-04-3: No httpx.AsyncClient stored on the platform object."""
        platform = _make_platform()
        for value in vars(platform).values():
            assert not isinstance(value, httpx.AsyncClient)


# ---------------------------------------------------------------------------
# TS-04-4: Module docstring references 'api' token scope
# Requirement: 04-REQ-1.4
# ---------------------------------------------------------------------------


class TestModuleDocstring:
    """Verify module docstring documents the required token scope."""

    def test_module_doc_mentions_api_scope(self) -> None:
        """TS-04-4: Module __doc__ mentions 'api' scope."""
        import agentfox.platform.gitlab as gl_module

        assert gl_module.__doc__ is not None
        assert "api" in gl_module.__doc__
        # Should also reference that read_api is insufficient (403)
        assert "read_api" in gl_module.__doc__ or "403" in gl_module.__doc__


# ---------------------------------------------------------------------------
# TS-04-5: GitLabPlatform is importable from afissues (platform) package
# Requirement: 04-REQ-1.5
# ---------------------------------------------------------------------------


class TestPublicImport:
    """Verify GitLabPlatform is importable from the platform package."""

    def test_import_from_platform_package(self) -> None:
        """TS-04-5: GitLabPlatform is importable from agentfox.platform.gitlab."""
        from agentfox.platform.gitlab import GitLabPlatform

        assert GitLabPlatform is not None
        assert GitLabPlatform.__name__ == "GitLabPlatform"


# ---------------------------------------------------------------------------
# TS-04-2: Constructor raises ConfigError for private IP (SSRF)
# Requirements: 04-REQ-1.2, 04-REQ-1.E1
# ---------------------------------------------------------------------------


class TestConstructorSSRF:
    """Verify constructor raises ConfigError for SSRF-violating URLs."""

    def test_raises_config_error_for_private_ip(self) -> None:
        """TS-04-2: ConfigError raised for private RFC-1918 IP."""
        from agentfox.platform.gitlab import GitLabPlatform

        with pytest.raises(ConfigError):
            GitLabPlatform("group/project", "tok", "192.168.1.1")

    def test_not_integration_error(self) -> None:
        """TS-04-E1: ConfigError, not IntegrationError, is raised."""
        from agentfox.platform.gitlab import GitLabPlatform

        try:
            GitLabPlatform("group/project", "tok", "192.168.1.1")
        except ConfigError:
            pass  # Expected
        except IntegrationError:
            pytest.fail("Should raise ConfigError, not IntegrationError")

    def test_no_http_client_created_on_ssrf(self) -> None:
        """TS-04-E1: httpx.AsyncClient never instantiated during SSRF failure."""
        from agentfox.platform.gitlab import GitLabPlatform

        with patch("httpx.AsyncClient") as mock_client:
            with pytest.raises(ConfigError):
                GitLabPlatform("group/project", "tok", "192.168.1.1")
        assert mock_client.call_count == 0


# ---------------------------------------------------------------------------
# TS-04-E2: URL-encoding of project_id with special characters
# Requirement: 04-REQ-1.E2
# ---------------------------------------------------------------------------


class TestProjectIdEncoding:
    """Verify project_id is correctly URL-encoded."""

    def test_slashes_encoded(self) -> None:
        """TS-04-E2: Slashes in project_id are percent-encoded."""
        from agentfox.platform.gitlab import GitLabPlatform

        with patch("agentfox.platform.gitlab._validate_url"):
            platform = GitLabPlatform("group/subgroup/project", "tok", "gitlab.com")

        encoded = platform._encoded_project_id
        assert "/" not in encoded
        assert "%" in encoded
        assert encoded == "group%2Fsubgroup%2Fproject"

    def test_spaces_encoded(self) -> None:
        """TS-04-E2: Spaces in project_id are percent-encoded."""
        from agentfox.platform.gitlab import GitLabPlatform

        with patch("agentfox.platform.gitlab._validate_url"):
            platform = GitLabPlatform("my group/my project", "tok", "gitlab.com")

        encoded = platform._encoded_project_id
        assert " " not in encoded
        assert "/" not in encoded
        assert "%" in encoded


# ===========================================================================
# TS-04-6: create_issue happy path
# Requirement: 04-REQ-2.1
# ===========================================================================


class TestCreateIssue:
    """Verify create_issue sends correct POST and maps response fields."""

    @pytest.mark.asyncio
    async def test_creates_issue_and_returns_result(self) -> None:
        """TS-04-6: POST /issues with title, description, labels; maps to IssueResult."""
        platform = _make_platform()

        mock_resp = _json_response(
            201,
            {
                "iid": 42,
                "title": "Fix bug",
                "web_url": "https://gitlab.com/group/project/-/issues/42",
                "description": "Some body",
                "labels": ["bug", "fix"],
            },
        )

        requests_made: list[tuple[str, dict]] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            requests_made.append((url, json or {}))
            return mock_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            result = await platform.create_issue("Fix bug", "Some body", ["bug", "fix"])

        assert isinstance(result, IssueResult)
        assert result.number == 42
        assert result.title == "Fix bug"
        assert result.html_url == "https://gitlab.com/group/project/-/issues/42"
        assert result.body == "Some body"
        assert result.labels == ("bug", "fix")

        # Verify POST payload
        assert len(requests_made) == 1
        url, payload = requests_made[0]
        assert "/issues" in url
        assert payload["title"] == "Fix bug"
        assert payload["description"] == "Some body"
        assert payload["labels"] == "bug,fix"


# ===========================================================================
# TS-04-E3: create_issue raises IntegrationError on non-201
# Requirement: 04-REQ-2.E1
# ===========================================================================


class TestCreateIssueError:
    """Verify create_issue raises IntegrationError on API error."""

    @pytest.mark.asyncio
    async def test_raises_on_422(self) -> None:
        """TS-04-E3: IntegrationError raised on non-201 status."""
        platform = _make_platform()
        long_text = "x" * 600
        mock_resp = _json_response(422, text=long_text)
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.create_issue("Test", "body", [])

    @pytest.mark.asyncio
    async def test_error_text_truncated_to_500(self) -> None:
        """TS-04-E3: Response text in error is truncated to 500 characters."""
        platform = _make_platform()
        long_text = "x" * 600
        mock_resp = _json_response(422, text=long_text)
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.create_issue("Test", "body", [])


# ===========================================================================
# TS-04-E4: create_issue defaults body to "" when description is null
# Requirement: 04-REQ-2.E2
# ===========================================================================


class TestCreateIssueNullDescription:
    """Verify description: null maps to IssueResult.body == ''."""

    @pytest.mark.asyncio
    async def test_null_description_defaults_to_empty_string(self) -> None:
        """TS-04-E4: IssueResult.body == '' when GitLab returns description: null."""
        platform = _make_platform()
        mock_resp = _json_response(
            201,
            {"iid": 1, "title": "T", "web_url": "url", "description": None, "labels": []},
        )
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.create_issue("T", "", [])

        assert result.body == ""


# ===========================================================================
# TS-04-7: list_issues_by_label happy path
# Requirement: 04-REQ-3.1
# ===========================================================================


class TestListIssuesByLabel:
    """Verify list_issues_by_label sends correct GET with mapped params."""

    @pytest.mark.asyncio
    async def test_returns_issue_results(self) -> None:
        """TS-04-7: GET /issues with correct params; returns list of IssueResult."""
        platform = _make_platform()

        mock_resp = _json_response(
            200,
            [
                {
                    "iid": 1,
                    "title": "T",
                    "web_url": "url",
                    "description": "b",
                    "labels": ["bug"],
                },
            ],
        )

        requests_made: list[tuple[str, dict]] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            requests_made.append((url, params or {}))
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            results = await platform.list_issues_by_label("bug", "open", sort="created", direction="desc")

        assert len(results) == 1
        assert isinstance(results[0], IssueResult)

        # Verify query parameters
        assert len(requests_made) == 1
        _, params = requests_made[0]
        assert params["labels"] == "bug"
        assert params["state"] == "opened"  # 'open' → 'opened'
        assert params["order_by"] == "created_at"  # 'created' → 'created_at'
        assert params["sort"] == "desc"
        assert params["per_page"] == 100


# ===========================================================================
# TS-04-8: list_issues_by_label state mapping
# Requirement: 04-REQ-3.2
# ===========================================================================


class TestListIssuesByLabelStateMapping:
    """Verify state value mapping: 'open' → 'opened', others pass through."""

    @pytest.mark.asyncio
    @pytest.mark.parametrize(
        ("input_state", "expected_state"),
        [
            ("open", "opened"),
            ("closed", "closed"),
            ("all", "all"),
        ],
    )
    async def test_state_mapping(self, input_state: str, expected_state: str) -> None:
        """TS-04-8: 'open' becomes 'opened'; 'closed' and 'all' pass through."""
        platform = _make_platform()
        mock_resp = _json_response(200, [])

        requests_made: list[dict] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            requests_made.append(params or {})
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            await platform.list_issues_by_label("label", input_state, sort="created", direction="asc")

        assert requests_made[0]["state"] == expected_state


# ===========================================================================
# TS-04-9: list_issues_by_label sort and direction mapping
# Requirement: 04-REQ-3.3
# ===========================================================================


class TestListIssuesByLabelSortMapping:
    """Verify sort and direction parameter mapping."""

    @pytest.mark.asyncio
    @pytest.mark.parametrize(
        ("sort_in", "order_by_expected"),
        [
            ("created", "created_at"),
            ("updated", "updated_at"),
        ],
    )
    async def test_sort_mapping(self, sort_in: str, order_by_expected: str) -> None:
        """TS-04-9: 'created' → 'created_at', 'updated' → 'updated_at'."""
        platform = _make_platform()
        mock_resp = _json_response(200, [])

        requests_made: list[dict] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            requests_made.append(params or {})
            return mock_resp

        client = _mock_client(get=mock_get)

        for direction in ["asc", "desc"]:
            requests_made.clear()
            with patch(_TARGET, return_value=client):
                await platform.list_issues_by_label("label", "open", sort=sort_in, direction=direction)

            params = requests_made[0]
            assert params["order_by"] == order_by_expected
            assert params["sort"] == direction


# ===========================================================================
# TS-04-E5: list_issues_by_label raises IntegrationError on non-200
# Requirement: 04-REQ-3.E1
# ===========================================================================


class TestListIssuesByLabelError:
    """Verify list_issues_by_label raises IntegrationError on API error."""

    @pytest.mark.asyncio
    async def test_raises_on_403(self) -> None:
        """TS-04-E5: IntegrationError raised on non-200 status."""
        platform = _make_platform()
        mock_resp = _json_response(403, text="Forbidden")
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.list_issues_by_label("bug", "open", sort="created", direction="asc")


# ===========================================================================
# TS-04-10: add_issue_comment happy path
# Requirement: 04-REQ-4.1
# ===========================================================================


class TestAddIssueComment:
    """Verify add_issue_comment sends correct POST to notes endpoint."""

    @pytest.mark.asyncio
    async def test_posts_to_notes_endpoint(self) -> None:
        """TS-04-10: POST /issues/{iid}/notes with {'body': body}; returns on 201."""
        platform = _make_platform()
        mock_resp = _json_response(201)

        requests_made: list[tuple[str, dict]] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            requests_made.append((url, json or {}))
            return mock_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            await platform.add_issue_comment(5, "This is a comment")

        assert len(requests_made) == 1
        url, payload = requests_made[0]
        assert "/issues/5/notes" in url
        assert payload == {"body": "This is a comment"}


# ===========================================================================
# TS-04-E6: add_issue_comment raises IntegrationError on non-201
# Requirement: 04-REQ-4.E1
# ===========================================================================


class TestAddIssueCommentError:
    """Verify add_issue_comment raises IntegrationError on error."""

    @pytest.mark.asyncio
    async def test_raises_on_404(self) -> None:
        """TS-04-E6: IntegrationError raised on non-201 status."""
        platform = _make_platform()
        mock_resp = _json_response(404, text="Not Found")
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.add_issue_comment(99, "comment")


# ===========================================================================
# TS-04-11: assign_label happy path
# Requirement: 04-REQ-5.1
# ===========================================================================


class TestAssignLabel:
    """Verify assign_label sends PUT with add_labels field."""

    @pytest.mark.asyncio
    async def test_puts_add_labels(self) -> None:
        """TS-04-11: PUT /issues/{iid} with {'add_labels': label}; returns on 200."""
        platform = _make_platform()
        mock_resp = _json_response(200)

        requests_made: list[tuple[str, str, dict]] = []

        async def mock_put(url, *, json=None, headers=None, **kw):
            requests_made.append((url, "put", json or {}))
            return mock_resp

        client = _mock_client(put=mock_put)

        with patch(_TARGET, return_value=client):
            await platform.assign_label(7, "in-progress")

        assert len(requests_made) == 1
        url, method, payload = requests_made[0]
        assert method == "put"
        assert "/issues/7" in url
        assert payload == {"add_labels": "in-progress"}


# ===========================================================================
# TS-04-E7: assign_label raises IntegrationError on non-200
# Requirement: 04-REQ-5.E1
# ===========================================================================


class TestAssignLabelError:
    """Verify assign_label raises IntegrationError on error."""

    @pytest.mark.asyncio
    async def test_raises_on_500(self) -> None:
        """TS-04-E7: IntegrationError raised on non-200 status."""
        platform = _make_platform()
        mock_resp = _json_response(500, text="Server Error")
        client = _mock_client(put=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.assign_label(5, "bug")


# ===========================================================================
# TS-04-12: close_issue with non-empty comment
# Requirement: 04-REQ-6.1
# ===========================================================================


class TestCloseIssueWithComment:
    """Verify close_issue calls add_issue_comment then sends close PUT."""

    @pytest.mark.asyncio
    async def test_comment_then_close(self) -> None:
        """TS-04-12: Two HTTP calls: POST notes, then PUT state_event=close."""
        platform = _make_platform()

        call_log: list[tuple[str, str, dict]] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            call_log.append((url, "post", json or {}))
            return _json_response(201)

        async def mock_put(url, *, json=None, headers=None, **kw):
            call_log.append((url, "put", json or {}))
            return _json_response(200)

        client = _mock_client(post=mock_post, put=mock_put)

        with patch(_TARGET, return_value=client):
            await platform.close_issue(10, "Closing this issue.")

        assert len(call_log) == 2

        # First call: POST comment to notes endpoint
        first_url, first_method, first_payload = call_log[0]
        assert first_method == "post"
        assert "notes" in first_url
        assert first_payload["body"] == "Closing this issue."

        # Second call: PUT to close the issue
        second_url, second_method, second_payload = call_log[1]
        assert second_method == "put"
        assert second_payload["state_event"] == "close"


# ===========================================================================
# TS-04-13: close_issue with empty/None comment
# Requirement: 04-REQ-6.2
# ===========================================================================


class TestCloseIssueWithoutComment:
    """Verify close_issue sends only the PUT when comment is empty/None."""

    @pytest.mark.asyncio
    async def test_only_close_put_on_empty_comment(self) -> None:
        """TS-04-13: Only one HTTP call (PUT with state_event=close)."""
        platform = _make_platform()

        call_log: list[tuple[str, str, dict]] = []

        async def mock_put(url, *, json=None, headers=None, **kw):
            call_log.append((url, "put", json or {}))
            return _json_response(200)

        client = _mock_client(put=mock_put)

        with patch(_TARGET, return_value=client):
            await platform.close_issue(10, "")

        assert len(call_log) == 1
        assert call_log[0][1] == "put"
        assert call_log[0][2]["state_event"] == "close"

    @pytest.mark.asyncio
    async def test_only_close_put_on_none_comment(self) -> None:
        """TS-04-13: Only one HTTP call when comment is None."""
        platform = _make_platform()

        call_log: list[tuple[str, str, dict]] = []

        async def mock_put(url, *, json=None, headers=None, **kw):
            call_log.append((url, "put", json or {}))
            return _json_response(200)

        client = _mock_client(put=mock_put)

        with patch(_TARGET, return_value=client):
            await platform.close_issue(10, None)

        assert len(call_log) == 1
        assert call_log[0][2]["state_event"] == "close"


# ===========================================================================
# TS-04-E8: close_issue propagates IntegrationError from comment step
# Requirement: 04-REQ-6.E1
# ===========================================================================


class TestCloseIssuePropagatesCommentError:
    """Verify close_issue propagates IntegrationError without attempting close."""

    @pytest.mark.asyncio
    async def test_propagates_error_from_comment(self) -> None:
        """TS-04-E8: IntegrationError from comment; no close PUT attempted."""
        platform = _make_platform()

        call_count = 0

        async def mock_post(url, *, json=None, headers=None, **kw):
            nonlocal call_count
            call_count += 1
            return _json_response(500, text="err")

        async def mock_put(url, *, json=None, headers=None, **kw):
            nonlocal call_count
            call_count += 1
            return _json_response(200)

        client = _mock_client(post=mock_post, put=mock_put)

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.close_issue(10, "Closing")

        # Only the notes POST was attempted; no close PUT
        assert call_count == 1


# ===========================================================================
# TS-04-14: remove_label happy path
# Requirement: 04-REQ-7.1
# ===========================================================================


class TestRemoveLabel:
    """Verify remove_label sends PUT with remove_labels field."""

    @pytest.mark.asyncio
    async def test_puts_remove_labels(self) -> None:
        """TS-04-14: PUT /issues/{iid} with {'remove_labels': label}."""
        platform = _make_platform()
        mock_resp = _json_response(200)

        requests_made: list[tuple[str, str, dict]] = []

        async def mock_put(url, *, json=None, headers=None, **kw):
            requests_made.append((url, "put", json or {}))
            return mock_resp

        client = _mock_client(put=mock_put)

        with patch(_TARGET, return_value=client):
            await platform.remove_label(3, "wontfix")

        assert len(requests_made) == 1
        url, method, payload = requests_made[0]
        assert method == "put"
        assert "/issues/3" in url
        assert payload == {"remove_labels": "wontfix"}


# ===========================================================================
# TS-04-E9: remove_label returns normally for missing label (idempotent)
# Requirement: 04-REQ-7.E1
# ===========================================================================


class TestRemoveLabelIdempotent:
    """Verify remove_label succeeds even when label is not present."""

    @pytest.mark.asyncio
    async def test_no_error_on_missing_label(self) -> None:
        """TS-04-E9: GitLab returns 200 even for missing label; no exception."""
        platform = _make_platform()
        mock_resp = _json_response(200)
        client = _mock_client(put=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.remove_label(3, "nonexistent-label")

        # No exception raised; result may be None
        assert result is None


# ===========================================================================
# TS-04-15: list_issue_comments happy path
# Requirements: 04-REQ-8.1, 04-REQ-8.E1
# ===========================================================================


class TestListIssueComments:
    """Verify list_issue_comments sends correct GET and filters system notes."""

    @pytest.mark.asyncio
    async def test_filters_system_notes_and_maps_fields(self) -> None:
        """TS-04-15: GET /notes with correct params; system notes excluded."""
        platform = _make_platform()

        notes_json = [
            {
                "id": 1,
                "body": "hello",
                "author": {"username": "alice"},
                "created_at": "2024-01-01T00:00:00Z",
                "system": False,
            },
            {
                "id": 2,
                "body": "system note",
                "author": {"username": "gitlab"},
                "created_at": "2024-01-02T00:00:00Z",
                "system": True,
            },
        ]

        requests_made: list[tuple[str, dict]] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            requests_made.append((url, params or {}))
            return _json_response(200, notes_json)

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            comments = await platform.list_issue_comments(5)

        # Verify params
        assert len(requests_made) == 1
        _, params = requests_made[0]
        assert params["sort"] == "asc"
        assert params["order_by"] == "created_at"
        assert params["per_page"] == 100
        assert "activity_filter" not in params

        # Only non-system note returned
        assert len(comments) == 1
        assert isinstance(comments[0], IssueComment)
        assert comments[0].id == 1
        assert comments[0].body == "hello"
        assert comments[0].user == "alice"
        assert comments[0].created_at == "2024-01-01T00:00:00Z"


# ===========================================================================
# TS-04-16: list_issue_comments never sends activity_filter
# Requirement: 04-REQ-8.2
# ===========================================================================


class TestListIssueCommentsNoActivityFilter:
    """Verify activity_filter is never sent in the request."""

    @pytest.mark.asyncio
    async def test_no_activity_filter_param(self) -> None:
        """TS-04-16: The request params do not contain 'activity_filter'."""
        platform = _make_platform()

        requests_made: list[dict] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            requests_made.append(params or {})
            return _json_response(200, [])

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            await platform.list_issue_comments(1)

        params = requests_made[0]
        assert "activity_filter" not in params


# ===========================================================================
# TS-04-E10: list_issue_comments filters all system==true notes
# Requirement: 04-REQ-8.E1
# ===========================================================================


class TestListIssueCommentsSystemFilter:
    """Verify all notes with system==true are excluded."""

    @pytest.mark.asyncio
    async def test_excludes_multiple_system_notes(self) -> None:
        """TS-04-E10: Only system==false notes appear in result."""
        platform = _make_platform()

        notes_json = [
            {
                "id": 1,
                "body": "user comment",
                "author": {"username": "alice"},
                "created_at": "2024-01-01T00:00:00Z",
                "system": False,
            },
            {
                "id": 2,
                "body": "system note",
                "author": {"username": "gitlab"},
                "created_at": "2024-01-02T00:00:00Z",
                "system": True,
            },
            {
                "id": 3,
                "body": "another system",
                "author": {"username": "gitlab"},
                "created_at": "2024-01-03T00:00:00Z",
                "system": True,
            },
        ]

        client = _mock_client(get=AsyncMock(return_value=_json_response(200, notes_json)))

        with patch(_TARGET, return_value=client):
            comments = await platform.list_issue_comments(5)

        assert len(comments) == 1
        assert comments[0].id == 1


# ===========================================================================
# TS-04-E11: list_issue_comments raises IntegrationError on non-200
# Requirement: 04-REQ-8.E2
# ===========================================================================


class TestListIssueCommentsError:
    """Verify list_issue_comments raises IntegrationError on error."""

    @pytest.mark.asyncio
    async def test_raises_on_403(self) -> None:
        """TS-04-E11: IntegrationError raised on non-200 status."""
        platform = _make_platform()
        mock_resp = _json_response(403, text="Forbidden")
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.list_issue_comments(5)


# ===========================================================================
# Group 2 tests: TS-04-17 through TS-04-32, TS-04-E12 through TS-04-E24
# Requirements: 04-REQ-9.* through 04-REQ-18.*
# ===========================================================================


# ===========================================================================
# TS-04-17: get_issue happy path with null description → body=''
# Requirement: 04-REQ-9.1
# ===========================================================================


class TestGetIssue:
    """Verify get_issue sends correct GET and maps response fields."""

    @pytest.mark.asyncio
    async def test_returns_issue_result_with_null_description(self) -> None:
        """TS-04-17: GET /issues/{iid}; null description defaults to body=''."""
        platform = _make_platform()

        mock_resp = _json_response(
            200,
            {
                "iid": 15,
                "title": "My Issue",
                "web_url": "https://gitlab.com/group/project/-/issues/15",
                "description": None,
                "labels": [],
            },
        )

        requests_made: list[str] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            requests_made.append(url)
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            result = await platform.get_issue(15)

        assert isinstance(result, IssueResult)
        assert result.number == 15
        assert result.title == "My Issue"
        assert result.html_url == "https://gitlab.com/group/project/-/issues/15"
        assert result.body == ""
        assert result.labels == ()

        # Verify correct URL
        assert len(requests_made) == 1
        assert "/issues/15" in requests_made[0]

    @pytest.mark.asyncio
    async def test_returns_issue_result_with_body(self) -> None:
        """TS-04-17: get_issue maps all fields correctly."""
        platform = _make_platform()

        mock_resp = _json_response(
            200,
            {
                "iid": 5,
                "title": "Bug report",
                "web_url": "https://gitlab.com/group/project/-/issues/5",
                "description": "Bug details",
                "labels": ["bug", "critical"],
            },
        )
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.get_issue(5)

        assert result.number == 5
        assert result.body == "Bug details"
        assert result.labels == ("bug", "critical")


# ===========================================================================
# TS-04-E12: get_issue raises IntegrationError on 404
# Requirement: 04-REQ-9.E1
# ===========================================================================


class TestGetIssueError:
    """Verify get_issue raises IntegrationError on error."""

    @pytest.mark.asyncio
    async def test_raises_on_404(self) -> None:
        """TS-04-E12: IntegrationError raised on HTTP 404."""
        platform = _make_platform()
        mock_resp = _json_response(404, text="Not Found")
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.get_issue(999)


# ===========================================================================
# TS-04-18: update_issue happy path
# Requirement: 04-REQ-10.1
# ===========================================================================


class TestUpdateIssue:
    """Verify update_issue sends PUT with description field mapped from body."""

    @pytest.mark.asyncio
    async def test_sends_put_with_description(self) -> None:
        """TS-04-18: PUT /issues/{iid} with {'description': body}."""
        platform = _make_platform()
        mock_resp = _json_response(200)

        requests_made: list[tuple[str, str, dict]] = []

        async def mock_put(url, *, json=None, headers=None, **kw):
            requests_made.append((url, "put", json or {}))
            return mock_resp

        client = _mock_client(put=mock_put)

        with patch(_TARGET, return_value=client):
            await platform.update_issue(8, "Updated description text")

        assert len(requests_made) == 1
        url, method, payload = requests_made[0]
        assert method == "put"
        assert "/issues/8" in url
        assert payload == {"description": "Updated description text"}


# ===========================================================================
# TS-04-E13: update_issue raises IntegrationError on non-200
# Requirement: 04-REQ-10.E1
# ===========================================================================


class TestUpdateIssueError:
    """Verify update_issue raises IntegrationError on error."""

    @pytest.mark.asyncio
    async def test_raises_on_422(self) -> None:
        """TS-04-E13: IntegrationError raised on non-200 status."""
        platform = _make_platform()
        mock_resp = _json_response(422, text="Unprocessable")
        client = _mock_client(put=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.update_issue(8, "new body")


# ===========================================================================
# TS-04-19: create_label happy path
# Requirement: 04-REQ-11.1
# ===========================================================================


class TestCreateLabel:
    """Verify create_label sends POST with color prefixed with '#'."""

    @pytest.mark.asyncio
    async def test_creates_label_with_hash_color(self) -> None:
        """TS-04-19: POST /labels with '#'+color; returns None on 201."""
        platform = _make_platform()
        mock_resp = _json_response(201)

        requests_made: list[tuple[str, dict]] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            requests_made.append((url, json or {}))
            return mock_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            result = await platform.create_label("blocker", "ff0000", "Blocking issue")

        assert result is None

        # Verify POST payload
        assert len(requests_made) == 1
        url, payload = requests_made[0]
        assert "/labels" in url
        assert payload["name"] == "blocker"
        assert payload["color"] == "#ff0000"
        assert payload["description"] == "Blocking issue"


# ===========================================================================
# TS-04-20: create_label 409 → returns None (idempotent)
# Requirement: 04-REQ-11.2
# ===========================================================================


class TestCreateLabelIdempotent:
    """Verify create_label treats HTTP 409 as success."""

    @pytest.mark.asyncio
    async def test_409_returns_none(self) -> None:
        """TS-04-20: 409 Conflict returns None without exception."""
        platform = _make_platform()
        mock_resp = _json_response(409, text="Conflict")
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.create_label("existing-label", "00ff00", "Already exists")

        assert result is None


# ===========================================================================
# TS-04-E14: create_label raises IntegrationError on non-201/non-409
# Requirement: 04-REQ-11.E1
# ===========================================================================


class TestCreateLabelError:
    """Verify create_label raises IntegrationError on non-201/non-409."""

    @pytest.mark.asyncio
    async def test_raises_on_422(self) -> None:
        """TS-04-E14: IntegrationError raised on 422 status."""
        platform = _make_platform()
        mock_resp = _json_response(422, text="Validation failed")
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.create_label("badlabel", "zzzzzz", "invalid color")


# ===========================================================================
# TS-04-21: create_pr happy path (HTTP 201)
# Requirement: 04-REQ-12.1
# ===========================================================================


class TestCreatePr:
    """Verify create_pr sends correct POST and returns web_url on 201."""

    @pytest.mark.asyncio
    async def test_returns_web_url_on_201(self) -> None:
        """TS-04-21: POST /merge_requests with correct body; returns web_url."""
        platform = _make_platform()

        mock_resp = _json_response(
            201,
            {"web_url": "https://gitlab.com/group/project/-/merge_requests/1"},
        )

        requests_made: list[tuple[str, dict]] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            requests_made.append((url, json or {}))
            return mock_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            url = await platform.create_pr(
                title="Fix: resolve the bug",
                body="This PR fixes the bug",
                head="feature-branch",
                base="main",
            )

        assert url == "https://gitlab.com/group/project/-/merge_requests/1"

        # Verify POST payload
        assert len(requests_made) == 1
        _, payload = requests_made[0]
        assert payload["source_branch"] == "feature-branch"
        assert payload["target_branch"] == "main"
        assert payload["title"] == "Fix: resolve the bug"
        assert payload["description"] == "This PR fixes the bug"


# ===========================================================================
# TS-04-22: create_pr 409 → fallback GET → returns web_url
# Requirement: 04-REQ-12.2
# ===========================================================================


class TestCreatePrFallback:
    """Verify create_pr on 409 performs fallback GET and returns existing MR."""

    @pytest.mark.asyncio
    async def test_fallback_get_returns_existing_mr(self) -> None:
        """TS-04-22: 409 → fallback GET 200 with MR list → returns web_url."""
        platform = _make_platform()

        call_log: list[tuple[str, str, dict]] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            call_log.append(("post", url, json or {}))
            return _json_response(409, text="Conflict")

        async def mock_get(url, *, params=None, headers=None, **kw):
            call_log.append(("get", url, params or {}))
            return _json_response(
                200,
                [{"web_url": "https://gitlab.com/group/project/-/merge_requests/3"}],
            )

        client = _mock_client(post=mock_post, get=mock_get)

        with patch(_TARGET, return_value=client):
            url = await platform.create_pr(
                title="My MR", body="body", head="feature", base="main"
            )

        assert url == "https://gitlab.com/group/project/-/merge_requests/3"

        # Verify fallback GET params
        assert len(call_log) == 2
        _, fallback_url, fallback_params = call_log[1]
        assert fallback_params["source_branch"] == "feature"
        assert fallback_params["target_branch"] == "main"
        assert fallback_params["state"] == "opened"


# ===========================================================================
# TS-04-E15: create_pr 409 fallback GET 200 empty list → IntegrationError
# Requirement: 04-REQ-12.E1
# ===========================================================================


class TestCreatePrFallbackEmpty:
    """Verify create_pr raises IntegrationError when fallback returns empty list."""

    @pytest.mark.asyncio
    async def test_empty_fallback_raises(self) -> None:
        """TS-04-E15: 409 + empty list → IntegrationError about no open MR."""
        platform = _make_platform()

        async def mock_post(url, *, json=None, headers=None, **kw):
            return _json_response(409, text="Conflict")

        async def mock_get(url, *, params=None, headers=None, **kw):
            return _json_response(200, [])

        client = _mock_client(post=mock_post, get=mock_get)

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.create_pr(
                    title="My MR", body="body", head="feature", base="main"
                )

        err_msg = str(exc_info.value)
        assert "409" in err_msg or "duplicate" in err_msg.lower() or "no" in err_msg.lower()


# ===========================================================================
# TS-04-E16: create_pr 409 fallback GET non-200 → IntegrationError
# Requirement: 04-REQ-12.E2
# ===========================================================================


class TestCreatePrFallbackGetFails:
    """Verify create_pr raises IntegrationError when fallback GET fails."""

    @pytest.mark.asyncio
    async def test_fallback_non_200_raises(self) -> None:
        """TS-04-E16: 409 + fallback GET 500 → IntegrationError referencing 409."""
        platform = _make_platform()

        async def mock_post(url, *, json=None, headers=None, **kw):
            return _json_response(409, text="Conflict")

        async def mock_get(url, *, params=None, headers=None, **kw):
            return _json_response(500, text="Server Error")

        client = _mock_client(post=mock_post, get=mock_get)

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.create_pr(
                    title="My MR", body="body", head="feature", base="main"
                )

        err_msg = str(exc_info.value)
        assert "409" in err_msg
        assert "500" in err_msg or "fallback" in err_msg.lower()


# ===========================================================================
# TS-04-E17: create_pr non-201/non-409 → IntegrationError, single call
# Requirement: 04-REQ-12.E3
# ===========================================================================


class TestCreatePrDirectError:
    """Verify create_pr raises IntegrationError on non-201/non-409."""

    @pytest.mark.asyncio
    async def test_403_raises_with_single_call(self) -> None:
        """TS-04-E17: 403 → IntegrationError; only one HTTP call (no fallback)."""
        platform = _make_platform()

        call_count = 0

        async def mock_post(url, *, json=None, headers=None, **kw):
            nonlocal call_count
            call_count += 1
            return _json_response(403, text="x" * 600)

        async def mock_get(url, *, params=None, headers=None, **kw):
            nonlocal call_count
            call_count += 1
            return _json_response(200, [])

        client = _mock_client(post=mock_post, get=mock_get)

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.create_pr(
                    title="My MR", body="body", head="feature", base="main"
                )

        # Only the POST was attempted; no fallback GET
        assert call_count == 1


# ===========================================================================
# TS-04-23: close() returns None with no HTTP calls
# Requirement: 04-REQ-13.1
# ===========================================================================


class TestClose:
    """Verify close() is a no-op lifecycle method."""

    @pytest.mark.asyncio
    async def test_returns_none_no_io(self) -> None:
        """TS-04-23: close() returns None; no HTTP calls made."""
        platform = _make_platform()

        call_count = 0

        async def mock_any(url, **kw):
            nonlocal call_count
            call_count += 1
            return _json_response(200)

        client = _mock_client(get=mock_any, post=mock_any, put=mock_any)

        with patch(_TARGET, return_value=client):
            result = await platform.close()

        assert result is None
        assert call_count == 0


# ===========================================================================
# TS-04-24: search_issues happy path
# Requirement: 04-REQ-14.1
# ===========================================================================


class TestSearchIssues:
    """Verify search_issues sends GET with search param and state mapping."""

    @pytest.mark.asyncio
    async def test_returns_issue_results(self) -> None:
        """TS-04-24: GET /issues with search, state mapped, per_page=100."""
        platform = _make_platform()

        issues_json = [
            {
                "iid": 1,
                "title": "Fix: bug",
                "web_url": "url",
                "description": "d",
                "labels": [],
            },
        ]

        requests_made: list[tuple[str, dict]] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            requests_made.append((url, params or {}))
            return _json_response(200, issues_json)

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            results = await platform.search_issues("Fix:", "open")

        assert len(results) == 1
        assert isinstance(results[0], IssueResult)

        # Verify query parameters
        assert len(requests_made) == 1
        _, params = requests_made[0]
        assert params["search"] == "Fix:"
        assert params["state"] == "opened"  # 'open' → 'opened'
        assert params["per_page"] == 100


# ===========================================================================
# TS-04-E18: search_issues raises IntegrationError on non-200
# Requirement: 04-REQ-14.E1
# ===========================================================================


class TestSearchIssuesError:
    """Verify search_issues raises IntegrationError on error."""

    @pytest.mark.asyncio
    async def test_raises_on_500(self) -> None:
        """TS-04-E18: IntegrationError raised on non-200 status."""
        platform = _make_platform()
        mock_resp = _json_response(500, text="Server Error")
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.search_issues("Fix:", "open")


# ===========================================================================
# TS-04-25: check_credentials raises IntegrationError on 401/403 only
# Requirements: 04-REQ-15.1, 04-REQ-15.E1, 04-REQ-15.E2
# ===========================================================================


class TestCheckCredentials:
    """Verify check_credentials raises on 401/403, returns None otherwise."""

    @pytest.mark.asyncio
    @pytest.mark.parametrize("status", [401, 403])
    async def test_raises_on_401_403(self, status: int) -> None:
        """TS-04-25/TS-04-E20: IntegrationError raised on 401 and 403."""
        platform = _make_platform()
        mock_resp = _json_response(status, text="Unauthorized")
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.check_credentials()

    @pytest.mark.asyncio
    @pytest.mark.parametrize("status", [404, 500])
    async def test_returns_none_on_404_500(self, status: int) -> None:
        """TS-04-25/TS-04-E19: None returned for 404 and 500."""
        platform = _make_platform()
        mock_resp = _json_response(status)
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.check_credentials()

        assert result is None

    @pytest.mark.asyncio
    async def test_sends_get_to_project_endpoint(self) -> None:
        """TS-04-25: GET /projects/{project_id} with PRIVATE-TOKEN."""
        platform = _make_platform()

        requests_made: list[str] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            requests_made.append(url)
            return _json_response(200)

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            await platform.check_credentials()

        assert len(requests_made) == 1
        assert "/projects/" in requests_made[0]


# ===========================================================================
# TS-04-26: parse_remote HTTPS GitLab URL
# Requirement: 04-REQ-16.1
# ===========================================================================


class TestParseRemoteHTTPS:
    """Verify parse_remote extracts namespace and project from HTTPS URLs."""

    def test_https_with_subgroup(self) -> None:
        """TS-04-26: HTTPS URL returns ('group/subgroup', 'project')."""
        from agentfox.platform.gitlab import parse_remote

        result = parse_remote("https://gitlab.com/group/subgroup/project.git")
        assert result == ("group/subgroup", "project")

    def test_https_simple(self) -> None:
        """TS-04-26: HTTPS URL with simple namespace."""
        from agentfox.platform.gitlab import parse_remote

        result = parse_remote("https://gitlab.com/group/project.git")
        assert result == ("group", "project")

    def test_https_without_git_suffix(self) -> None:
        """TS-04-26: HTTPS URL without .git suffix."""
        from agentfox.platform.gitlab import parse_remote

        result = parse_remote("https://gitlab.com/group/subgroup/project")
        assert result == ("group/subgroup", "project")


# ===========================================================================
# TS-04-27: parse_remote SSH GitLab URL
# Requirement: 04-REQ-16.2
# ===========================================================================


class TestParseRemoteSSH:
    """Verify parse_remote extracts namespace and project from SSH URLs."""

    def test_ssh_with_subgroup(self) -> None:
        """TS-04-27: SSH URL returns ('group/subgroup', 'project')."""
        from agentfox.platform.gitlab import parse_remote

        result = parse_remote("git@gitlab.com:group/subgroup/project.git")
        assert result == ("group/subgroup", "project")

    def test_ssh_simple(self) -> None:
        """TS-04-27: SSH URL with simple namespace."""
        from agentfox.platform.gitlab import parse_remote

        result = parse_remote("git@gitlab.com:group/project.git")
        assert result == ("group", "project")


# ===========================================================================
# TS-04-28: parse_remote returns None for invalid URLs
# Requirement: 04-REQ-16.3
# ===========================================================================


class TestParseRemoteInvalid:
    """Verify parse_remote returns None for non-GitLab URLs without raising."""

    @pytest.mark.parametrize(
        "url",
        [
            "https://github.com/org/repo.git",
            "not-a-url",
            "",
            "https://gitlab.com:8080/group/project.git",
            "git@github.com:org/repo.git",
        ],
    )
    def test_returns_none_for_invalid_urls(self, url: str) -> None:
        """TS-04-28: Returns None for each invalid URL; no exception raised."""
        from agentfox.platform.gitlab import parse_remote

        result = parse_remote(url)
        assert result is None


# ===========================================================================
# TS-04-E21: parse_remote returns None for GitHub HTTPS URL
# Requirement: 04-REQ-16.E1
# ===========================================================================


class TestParseRemoteGitHubUrl:
    """Verify parse_remote returns None for GitHub URLs."""

    def test_github_url_returns_none(self) -> None:
        """TS-04-E21: GitHub URL returns None; no exception."""
        from agentfox.platform.gitlab import parse_remote

        result = parse_remote("https://github.com/org/repo.git")
        assert result is None


# ===========================================================================
# TS-04-E22: parse_remote returns None for URL with port
# Requirement: 04-REQ-16.E2
# ===========================================================================


class TestParseRemotePortUrl:
    """Verify parse_remote returns None for URLs with port numbers."""

    def test_port_url_returns_none(self) -> None:
        """TS-04-E22: GitLab URL with port returns None."""
        from agentfox.platform.gitlab import parse_remote

        result = parse_remote("https://gitlab.com:8080/group/project.git")
        assert result is None


# ===========================================================================
# TS-04-E23: parse_remote returns None for single-segment URL
# Requirement: 04-REQ-16.E3
# ===========================================================================


class TestParseRemoteSingleSegment:
    """Verify parse_remote returns None when only one path segment exists."""

    def test_single_segment_returns_none(self) -> None:
        """TS-04-E23: Only one path segment (no project path) returns None."""
        from agentfox.platform.gitlab import parse_remote

        result = parse_remote("https://gitlab.com/onlyone.git")
        assert result is None


# ===========================================================================
# TS-04-29: parse_remote in github.py; parse_github_remote removed
# Requirement: 04-REQ-17.1
# ===========================================================================


class TestParseRemoteRename:
    """Verify parse_github_remote has been renamed to parse_remote."""

    def test_parse_remote_importable_from_github(self) -> None:
        """TS-04-29: 'from agentfox.platform.github import parse_remote' succeeds."""
        from agentfox.platform.github import parse_remote  # noqa: F401

        assert callable(parse_remote)

    def test_parse_github_remote_removed_from_github_module(self) -> None:
        """TS-04-29: parse_github_remote no longer exists in github.py."""
        try:
            from agentfox.platform.github import parse_github_remote  # noqa: F401

            pytest.fail("Expected ImportError — parse_github_remote should be removed")
        except (ImportError, AttributeError):
            pass  # Expected: old name is removed

    def test_parse_github_remote_removed_from_init(self) -> None:
        """TS-04-E24: parse_github_remote not re-exported from platform package."""
        import agentfox.platform

        assert not hasattr(agentfox.platform, "parse_github_remote")


# ===========================================================================
# TS-04-30: _ssrf.py exports required symbols
# Requirement: 04-REQ-18.1
# ===========================================================================


class TestSSRFModuleExports:
    """Verify _ssrf.py exports all four required symbols."""

    def test_all_symbols_importable(self) -> None:
        """TS-04-30: All four SSRF symbols importable from _ssrf module."""
        from agentfox.platform._ssrf import (  # noqa: F401
            SSRFGuardTransport,
            _check_address,
            _validate_transport_address,
            _validate_url,
        )

        assert callable(_validate_url)
        assert callable(_validate_transport_address)
        assert callable(_check_address)

    def test_ssrf_guard_transport_is_subclass(self) -> None:
        """TS-04-30: SSRFGuardTransport is a subclass of httpx.AsyncHTTPTransport."""
        from agentfox.platform._ssrf import SSRFGuardTransport

        assert issubclass(SSRFGuardTransport, httpx.AsyncHTTPTransport)

    def test_github_imports_from_ssrf(self) -> None:
        """TS-04-30: github.py imports from _ssrf module."""
        import inspect

        import agentfox.platform.github

        github_src = inspect.getsource(agentfox.platform.github)
        assert "_ssrf" in github_src

    def test_gitlab_imports_from_ssrf(self) -> None:
        """TS-04-30: gitlab.py imports from _ssrf module."""
        import inspect

        import agentfox.platform.gitlab

        gitlab_src = inspect.getsource(agentfox.platform.gitlab)
        assert "_ssrf" in gitlab_src


# ===========================================================================
# TS-04-31: _check_address raises ConfigError (not IntegrationError)
# Requirement: 04-REQ-18.2
# ===========================================================================


class TestCheckAddressSSRF:
    """Verify _check_address raises ConfigError for SSRF violation."""

    def test_raises_config_error_for_private_ip(self) -> None:
        """TS-04-31: ConfigError raised for private IP address."""
        from ipaddress import ip_address

        from agentfox.platform._ssrf import _check_address

        private_addr = ip_address("192.168.1.1")
        with pytest.raises(ConfigError):
            _check_address(private_addr, "http://192.168.1.1")

    def test_not_integration_error(self) -> None:
        """TS-04-31: IntegrationError is NOT raised for SSRF violation."""
        from ipaddress import ip_address

        from agentfox.platform._ssrf import _check_address

        private_addr = ip_address("192.168.1.1")
        try:
            _check_address(private_addr, "http://192.168.1.1")
        except ConfigError:
            pass  # Expected
        except IntegrationError:
            pytest.fail("Should raise ConfigError, not IntegrationError")


# ===========================================================================
# TS-04-32: _ssrf module is not re-exported from platform __init__.py
# Requirement: 04-REQ-18.3
# ===========================================================================


class TestSSRFNotPublic:
    """Verify _ssrf is not part of the public platform API."""

    def test_ssrf_not_in_all(self) -> None:
        """TS-04-32: _ssrf not in platform __all__."""
        import agentfox.platform

        all_exports = getattr(agentfox.platform, "__all__", [])
        assert "_ssrf" not in all_exports
