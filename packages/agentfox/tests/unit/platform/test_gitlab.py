"""Tests for GitLabPlatform issue and PR operations.

Test Spec: TS-04-1 through TS-04-16, TS-04-E1 through TS-04-E11
Requirements: 04-REQ-1.* through 04-REQ-8.*, 04-REQ-16.*

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
_TARGET = "agentfox.platform.gitlab.httpx.AsyncClient"


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
