"""Tests for GiteaPlatform issue and PR operations.

Test Spec: TS-05-1 through TS-05-19, TS-05-E1 through TS-05-E10,
           TS-05-46 through TS-05-49
Requirements: 05-REQ-1.* through 05-REQ-6.*, 05-REQ-17.*

Note: Import paths use agentfox.platform.* (the actual codebase layout),
not afissues.* (the spec-03 future layout that has not been extracted yet).
The Gitea module will live at agentfox.platform.gitea alongside the
existing agentfox.platform.github module.
"""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from agentfox.core.errors import ConfigError, IntegrationError
from agentfox.platform.protocol import IssueComment, IssueResult  # noqa: F401

# ---------------------------------------------------------------------------
# Helpers (modelled after test_gitlab.py / test_github_issues_rest.py)
# ---------------------------------------------------------------------------

# Target for patching httpx.AsyncClient in the gitea module.
_TARGET = "agentfox.platform.gitea.httpx.AsyncClient"

# Target for patching _validate_url in the gitea module.
_VALIDATE_URL_TARGET = "agentfox.platform.gitea._validate_github_url"


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
    """Build a GiteaPlatform with safe defaults for testing.

    Patches _validate_github_url (SSRF check) to accept 'gitea.example.com'
    without performing real DNS resolution.

    Note: Once spec 04 extracts _validate_url into afissues._ssrf, the patch
    target will change to 'agentfox.platform.gitea._validate_url'.
    """
    from agentfox.platform.gitea import GiteaPlatform

    with patch(_VALIDATE_URL_TARGET):
        return GiteaPlatform("myorg", "myrepo", "mytoken", "gitea.example.com")


# ===========================================================================
# TS-05-1: GiteaPlatform.forge_type class attribute
# Requirement: 05-REQ-1.1
# ===========================================================================


class TestGiteaForgeType:
    """Verify GiteaPlatform exposes forge_type == 'gitea'."""

    def test_forge_type_is_gitea(self) -> None:
        """TS-05-1: forge_type class attribute equals 'gitea' without instantiating."""
        from agentfox.platform.gitea import GiteaPlatform

        assert GiteaPlatform.forge_type == "gitea"


# ===========================================================================
# TS-05-2: GiteaPlatform constructor sets attributes
# Requirement: 05-REQ-1.2
# ===========================================================================


class TestGiteaConstructor:
    """Verify GiteaPlatform initialises with correct attributes."""

    def test_base_url(self) -> None:
        """TS-05-2: base_url is https://{url}/api/v1."""
        platform = _make_platform()
        assert platform._base_url == "https://gitea.example.com/api/v1"

    def test_auth_headers(self) -> None:
        """TS-05-2: auth headers use token scheme."""
        platform = _make_platform()
        headers = platform._auth_headers
        assert headers["Authorization"] == "token mytoken"

    def test_owner_stored(self) -> None:
        """TS-05-2: owner is stored correctly."""
        platform = _make_platform()
        assert platform._owner == "myorg"

    def test_repo_stored(self) -> None:
        """TS-05-2: repo is stored correctly."""
        platform = _make_platform()
        assert platform._repo == "myrepo"


# ===========================================================================
# TS-05-3: Constructor calls _validate_url (SSRF guard)
# Requirement: 05-REQ-1.3
# ===========================================================================


class TestConstructorCallsValidateUrl:
    """Verify constructor calls SSRF validation synchronously."""

    def test_validate_url_called_once(self) -> None:
        """TS-05-3: _validate_url called exactly once with the supplied url."""
        from agentfox.platform.gitea import GiteaPlatform

        with patch(_VALIDATE_URL_TARGET) as mock_validate:
            GiteaPlatform("myorg", "myrepo", "token123", "gitea.example.com")
            assert mock_validate.call_count == 1
            mock_validate.assert_called_with("gitea.example.com")


# ===========================================================================
# TS-05-4: Constructor initializes empty label cache
# Requirement: 05-REQ-1.4
# ===========================================================================


class TestConstructorLabelCache:
    """Verify constructor initializes an empty label ID cache."""

    def test_label_cache_is_empty_dict(self) -> None:
        """TS-05-4: _label_cache is an empty dict at construction."""
        platform = _make_platform()
        assert isinstance(platform._label_cache, dict)
        assert len(platform._label_cache) == 0


# ===========================================================================
# TS-05-5: Constructor uses afissues._http retry pattern
# Requirement: 05-REQ-1.5
# ===========================================================================


class TestConstructorHttpIntegration:
    """Verify GiteaPlatform uses the same _http retry pattern as GitHubPlatform.

    Until spec 04 extracts _http into a shared module, GiteaPlatform follows
    the same inline retry pattern as GitHubPlatform (httpx.AsyncClient with
    SSRFGuardTransport and retry logic in _request).
    """

    def test_has_request_method(self) -> None:
        """TS-05-5: GiteaPlatform has an async _request method for HTTP retry logic."""
        platform = _make_platform()
        # Verify the platform has a _request method (the HTTP integration point)
        assert hasattr(platform, "_request")
        assert callable(platform._request)


# ===========================================================================
# TS-05-E1: ConfigError propagation from _validate_url
# Requirement: 05-REQ-1.E1
# ===========================================================================


class TestConstructorSSRF:
    """Verify constructor raises ConfigError for SSRF-violating URLs."""

    def test_raises_config_error(self) -> None:
        """TS-05-E1: ConfigError raised when _validate_url raises."""
        from agentfox.platform.gitea import GiteaPlatform

        with patch(
            _VALIDATE_URL_TARGET,
            side_effect=ConfigError("SSRF disallowed"),
        ):
            with pytest.raises(ConfigError):
                GiteaPlatform("org", "repo", "token", "169.254.169.254")

    def test_config_error_not_integration_error(self) -> None:
        """TS-05-E1: ConfigError is not re-wrapped as IntegrationError."""
        from agentfox.platform.gitea import GiteaPlatform

        with patch(
            _VALIDATE_URL_TARGET,
            side_effect=ConfigError("SSRF disallowed"),
        ):
            try:
                GiteaPlatform("org", "repo", "token", "169.254.169.254")
            except ConfigError:
                pass  # Expected
            except IntegrationError:
                pytest.fail("Should raise ConfigError, not IntegrationError")

    def test_config_error_type_exact(self) -> None:
        """TS-05-E1: Raised exception type is exactly ConfigError."""
        from agentfox.platform.gitea import GiteaPlatform

        with patch(
            _VALIDATE_URL_TARGET,
            side_effect=ConfigError("SSRF disallowed"),
        ):
            with pytest.raises(ConfigError) as exc_info:
                GiteaPlatform("org", "repo", "token", "169.254.169.254")
            assert type(exc_info.value) is ConfigError


# ===========================================================================
# TS-05-E2: Token used verbatim in Authorization header
# Requirement: 05-REQ-1.E2
# ===========================================================================


class TestConstructorTokenVerbatim:
    """Verify the token is used as-is with no validation or sanitization."""

    def test_special_characters_in_token(self) -> None:
        """TS-05-E2: Special characters in token are preserved verbatim."""
        from agentfox.platform.gitea import GiteaPlatform

        with patch(_VALIDATE_URL_TARGET):
            platform = GiteaPlatform(
                "org", "repo", "a!weird#token$value", "gitea.example.com"
            )
        assert platform._auth_headers["Authorization"] == "token a!weird#token$value"


# ===========================================================================
# TS-05-6: _resolve_label_id cache hit (no HTTP)
# Requirement: 05-REQ-2.1
# ===========================================================================


class TestResolveLabelIdCacheHit:
    """Verify _resolve_label_id returns cached ID without making HTTP calls."""

    @pytest.mark.asyncio
    async def test_returns_cached_id_without_http(self) -> None:
        """TS-05-6: Pre-populated cache returns ID; no HTTP GET is made."""
        platform = _make_platform()
        platform._label_cache = {"bug": 42}
        # Mark cache as populated so we know it won't re-fetch
        platform._cache_populated = True

        client = _mock_client(get=AsyncMock())

        with patch(_TARGET, return_value=client) as mock_cls:
            result = await platform._resolve_label_id("bug")

        assert result == 42
        # No AsyncClient was created (no HTTP call)
        mock_cls.assert_not_called()


# ===========================================================================
# TS-05-7: _resolve_label_id cache miss → fetch and populate
# Requirement: 05-REQ-2.2
# ===========================================================================


class TestResolveLabelIdCacheMiss:
    """Verify _resolve_label_id fetches labels on cache miss and populates cache."""

    @pytest.mark.asyncio
    async def test_fetches_labels_and_populates_cache(self) -> None:
        """TS-05-7: GET /labels populates cache; returns correct ID."""
        platform = _make_platform()

        mock_resp = _json_response(
            200,
            [
                {"id": 7, "name": "bug"},
                {"id": 8, "name": "enhancement"},
            ],
        )

        async def mock_get(url, *, params=None, headers=None, **kw):
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            result = await platform._resolve_label_id("bug")

        assert result == 7
        assert platform._label_cache == {"bug": 7, "enhancement": 8}


# ===========================================================================
# TS-05-8: _resolve_label_id — label not found after full fetch
# Requirement: 05-REQ-2.3
# ===========================================================================


class TestResolveLabelIdNotFound:
    """Verify _resolve_label_id raises IntegrationError when label is absent."""

    @pytest.mark.asyncio
    async def test_raises_integration_error_for_missing_label(self) -> None:
        """TS-05-8: IntegrationError raised with descriptive message."""
        platform = _make_platform()

        mock_resp = _json_response(200, [{"id": 7, "name": "bug"}])

        async def mock_get(url, *, params=None, headers=None, **kw):
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError, match="nonexistent"):
                await platform._resolve_label_id("nonexistent")


# ===========================================================================
# TS-05-9: _resolve_label_id — no re-fetch for known-missing label
# Requirement: 05-REQ-2.4
# ===========================================================================


class TestResolveLabelIdNoReFetch:
    """Verify subsequent calls for a missing label don't re-fetch from API."""

    @pytest.mark.asyncio
    async def test_no_refetch_on_second_call_for_missing(self) -> None:
        """TS-05-9: Second call raises immediately; no additional HTTP GET."""
        platform = _make_platform()

        call_count = 0

        async def mock_get(url, *, params=None, headers=None, **kw):
            nonlocal call_count
            call_count += 1
            return _json_response(200, [{"id": 7, "name": "bug"}])

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            # First call — fetches from API, label absent
            with pytest.raises(IntegrationError):
                await platform._resolve_label_id("nonexistent")
            calls_after_first = call_count

            # Second call — should NOT re-fetch
            with pytest.raises(IntegrationError):
                await platform._resolve_label_id("nonexistent")
            assert call_count == calls_after_first  # no additional GET


# ===========================================================================
# TS-05-E3: _resolve_label_id raises IntegrationError on non-2xx GET
# Requirement: 05-REQ-2.E1
# ===========================================================================


class TestResolveLabelIdHttpError:
    """Verify _resolve_label_id raises IntegrationError with truncated text on error."""

    @pytest.mark.asyncio
    async def test_raises_on_non_2xx_with_truncated_text(self) -> None:
        """TS-05-E3: 500 response with 600-char body; error text <= 500 chars."""
        platform = _make_platform()

        long_error = "E" * 600
        mock_resp = _json_response(500, text=long_error)

        async def mock_get(url, *, params=None, headers=None, **kw):
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform._resolve_label_id("bug")

        # Verify the 600-char response text is not embedded verbatim
        error_msg = str(exc_info.value)
        assert "E" * 501 not in error_msg


# ===========================================================================
# TS-05-46: parse_remote — valid HTTPS URL
# Requirement: 05-REQ-17.1
# ===========================================================================


class TestParseRemoteHTTPS:
    """Verify parse_remote parses HTTPS Gitea remote URLs."""

    def test_https_url_with_dot_git(self) -> None:
        """TS-05-46: HTTPS URL with .git suffix → (owner, repo)."""
        from agentfox.platform.gitea import parse_remote

        result = parse_remote("https://gitea.example.com/myowner/myrepo.git")
        assert result == ("myowner", "myrepo")

    def test_https_url_without_dot_git(self) -> None:
        """TS-05-46: HTTPS URL without .git suffix → (owner, repo)."""
        from agentfox.platform.gitea import parse_remote

        result = parse_remote("https://gitea.example.com/myowner/myrepo")
        assert result == ("myowner", "myrepo")


# ===========================================================================
# TS-05-47: parse_remote — valid SSH URL
# Requirement: 05-REQ-17.2
# ===========================================================================


class TestParseRemoteSSH:
    """Verify parse_remote parses SSH Gitea remote URLs."""

    def test_ssh_url_with_dot_git(self) -> None:
        """TS-05-47: SSH URL with .git suffix → (owner, repo)."""
        from agentfox.platform.gitea import parse_remote

        result = parse_remote("git@gitea.example.com:myowner/myrepo.git")
        assert result == ("myowner", "myrepo")

    def test_ssh_url_without_dot_git(self) -> None:
        """TS-05-47: SSH URL without .git suffix → (owner, repo)."""
        from agentfox.platform.gitea import parse_remote

        result = parse_remote("git@gitea.example.com:myowner/myrepo")
        assert result == ("myowner", "myrepo")


# ===========================================================================
# TS-05-48: parse_remote — unparseable URL
# Requirement: 05-REQ-17.3
# ===========================================================================


class TestParseRemoteUnparseable:
    """Verify parse_remote returns None for unparseable URLs."""

    def test_returns_none_for_garbage(self) -> None:
        """TS-05-48: Random string → None."""
        from agentfox.platform.gitea import parse_remote

        result = parse_remote("not-a-valid-url")
        assert result is None

    def test_returns_none_for_empty(self) -> None:
        """TS-05-48: Empty string → None."""
        from agentfox.platform.gitea import parse_remote

        result = parse_remote("")
        assert result is None


# ===========================================================================
# TS-05-49: parse_remote — any hostname accepted
# Requirement: 05-REQ-17.4
# ===========================================================================


class TestParseRemoteAnyHostname:
    """Verify parse_remote is not restricted to specific domains."""

    def test_internal_hostname(self) -> None:
        """TS-05-49: Internal/arbitrary hostname is accepted."""
        from agentfox.platform.gitea import parse_remote

        result = parse_remote(
            "https://my-internal-server.corp.local/team/project.git"
        )
        assert result == ("team", "project")

    def test_ip_hostname(self) -> None:
        """TS-05-49: IP-based hostname is accepted."""
        from agentfox.platform.gitea import parse_remote

        result = parse_remote("https://192.168.1.100/org/repo.git")
        assert result == ("org", "repo")


# ===========================================================================
# Group 2 Tests: create_issue, list_issues_by_label, add_issue_comment,
#                assign_label
# Test Spec: TS-05-10 through TS-05-19, TS-05-E4 through TS-05-E10
# Requirements: 05-REQ-3.* through 05-REQ-6.*
# ===========================================================================


# ===========================================================================
# TS-05-10: create_issue with labels resolves IDs and POSTs numeric array
# Requirement: 05-REQ-3.1
# ===========================================================================


class TestCreateIssueWithLabels:
    """Verify create_issue resolves label names to numeric IDs via _resolve_label_id."""

    @pytest.mark.asyncio
    async def test_create_issue_resolves_labels_to_numeric_ids(self) -> None:
        """TS-05-10: labels=['bug'] → _resolve_label_id returns 42 → POST labels=[42]."""
        platform = _make_platform()

        mock_issue_resp = _json_response(
            201,
            {
                "number": 1,
                "title": "Fix crash",
                "html_url": "http://gitea.example.com/myorg/myrepo/issues/1",
                "body": "Description here",
                "labels": [{"name": "bug"}],
            },
        )

        requests_made: list[tuple[str, dict]] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            requests_made.append((url, json or {}))
            return mock_issue_resp

        client = _mock_client(post=mock_post)

        # Mock _resolve_label_id to return a known numeric ID
        with patch(_TARGET, return_value=client), patch.object(
            platform, "_resolve_label_id", new_callable=AsyncMock, return_value=42
        ):
            result = await platform.create_issue("Fix crash", "Description here", ["bug"])

        # POST body must contain labels as array of ints
        assert len(requests_made) == 1
        _, posted_body = requests_made[0]
        assert posted_body["labels"] == [42]

        # Return value is correctly mapped IssueResult
        assert isinstance(result, IssueResult)
        assert result.number == 1
        assert result.title == "Fix crash"
        assert result.html_url == "http://gitea.example.com/myorg/myrepo/issues/1"
        assert result.body == "Description here"
        assert result.labels == ("bug",)


# ===========================================================================
# TS-05-11: create_issue with empty labels omits labels field from POST body
# Requirement: 05-REQ-3.2
# ===========================================================================


class TestCreateIssueNoLabels:
    """Verify create_issue omits the labels key when called with an empty list."""

    @pytest.mark.asyncio
    async def test_create_issue_no_labels_key_in_body(self) -> None:
        """TS-05-11: labels=[] → POST body has no 'labels' key."""
        platform = _make_platform()

        mock_resp = _json_response(
            201,
            {
                "number": 1,
                "title": "Fix crash",
                "html_url": "http://gitea.example.com/myorg/myrepo/issues/1",
                "body": "Description",
                "labels": [],
            },
        )

        requests_made: list[tuple[str, dict]] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            requests_made.append((url, json or {}))
            return mock_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            result = await platform.create_issue("Fix crash", "Description", [])

        # POST body should not contain a 'labels' key
        assert len(requests_made) == 1
        _, posted_body = requests_made[0]
        assert "labels" not in posted_body

        assert isinstance(result, IssueResult)
        assert result.number == 1


# ===========================================================================
# TS-05-12: create_issue treats any 2xx (including 200) as success
# Requirement: 05-REQ-3.3
# ===========================================================================


class TestCreateIssue2xxSuccess:
    """Verify create_issue treats any 2xx response as success."""

    @pytest.mark.asyncio
    async def test_create_issue_200_is_success(self) -> None:
        """TS-05-12: POST returns 200 (not 201); IssueResult returned without error."""
        platform = _make_platform()

        mock_resp = _json_response(
            200,
            {
                "number": 5,
                "title": "Title",
                "html_url": "http://gitea.example.com/myorg/myrepo/issues/5",
                "body": "Body",
                "labels": [],
            },
        )
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.create_issue("Title", "Body", [])

        assert result.number == 5


# ===========================================================================
# TS-05-E4: create_issue raises IntegrationError on non-2xx with truncated text
# Requirement: 05-REQ-3.E1
# ===========================================================================


class TestCreateIssueError:
    """Verify create_issue raises IntegrationError with truncated response text."""

    @pytest.mark.asyncio
    async def test_create_issue_raises_on_500_with_truncated_text(self) -> None:
        """TS-05-E4: POST returns 500 with 600-char body; error text ≤ 500 chars."""
        platform = _make_platform()

        long_error = "X" * 600
        mock_resp = _json_response(500, text=long_error)
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.create_issue("Title", "Body", [])

        # Verify the 600-char response text is not embedded verbatim
        error_msg = str(exc_info.value)
        assert "X" * 501 not in error_msg


# ===========================================================================
# TS-05-E5: create_issue maps null body to empty string
# Requirement: 05-REQ-3.E2
# ===========================================================================


class TestCreateIssueNullBody:
    """Verify create_issue maps null/absent body field to ''."""

    @pytest.mark.asyncio
    async def test_create_issue_null_body_maps_to_empty_string(self) -> None:
        """TS-05-E5: Response body=null → IssueResult.body == ''."""
        platform = _make_platform()

        mock_resp = _json_response(
            201,
            {
                "number": 1,
                "title": "Title",
                "html_url": "http://gitea.example.com/myorg/myrepo/issues/1",
                "body": None,
                "labels": [],
            },
        )
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.create_issue("Title", "Body", [])

        assert result.body == ""


# ===========================================================================
# TS-05-E6: create_issue maps absent labels to empty tuple
# Requirement: 05-REQ-3.E3
# ===========================================================================


class TestCreateIssueAbsentLabels:
    """Verify create_issue maps absent labels field to empty tuple."""

    @pytest.mark.asyncio
    async def test_create_issue_absent_labels_maps_to_empty_tuple(self) -> None:
        """TS-05-E6: Response missing labels field → IssueResult.labels == ()."""
        platform = _make_platform()

        # Response JSON has no 'labels' key at all
        mock_resp = _json_response(
            201,
            {
                "number": 1,
                "title": "Title",
                "html_url": "http://gitea.example.com/myorg/myrepo/issues/1",
                "body": "",
            },
        )
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.create_issue("Title", "Body", [])

        assert result.labels == ()


# ===========================================================================
# TS-05-13: list_issues_by_label sends correct query params with sort mapping
# Requirement: 05-REQ-4.1
# ===========================================================================


class TestListIssuesByLabelParams:
    """Verify list_issues_by_label sends correct GET query parameters."""

    @pytest.mark.asyncio
    async def test_list_issues_correct_params(self) -> None:
        """TS-05-13: GET has labels, state, type=issues, sort=oldest, limit=50."""
        platform = _make_platform()

        mock_issues = [
            {
                "number": 1,
                "title": "Bug fix",
                "html_url": "http://gitea.example.com/myorg/myrepo/issues/1",
                "body": "Details",
                "labels": [{"name": "bug"}],
            },
        ]
        mock_resp = _json_response(200, mock_issues)

        requests_made: list[tuple[str, dict]] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            requests_made.append((url, params or {}))
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            result = await platform.list_issues_by_label(
                "bug", state="open", sort="created", direction="asc"
            )

        assert len(requests_made) == 1
        _, params = requests_made[0]
        assert params["labels"] == "bug"
        assert params["state"] == "open"
        assert params["type"] == "issues"
        assert params["sort"] == "oldest"
        assert params["limit"] == 50

        assert isinstance(result, list)
        assert len(result) == 1
        assert isinstance(result[0], IssueResult)


# ===========================================================================
# TS-05-14: list_issues_by_label sort+direction mapping (all 4 combos)
# Requirement: 05-REQ-4.2
# ===========================================================================


class TestListIssuesByLabelSortMapping:
    """Verify all four sort+direction combinations map to correct Gitea values."""

    @pytest.mark.asyncio
    @pytest.mark.parametrize(
        ("sort_in", "dir_in", "expected_sort"),
        [
            ("created", "asc", "oldest"),
            ("created", "desc", "newest"),
            ("updated", "asc", "leastupdate"),
            ("updated", "desc", "recentupdate"),
        ],
    )
    async def test_sort_direction_mapping(
        self, sort_in: str, dir_in: str, expected_sort: str
    ) -> None:
        """TS-05-14: sort+direction maps to Gitea's combined sort values."""
        platform = _make_platform()

        mock_resp = _json_response(200, [])

        requests_made: list[dict] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            requests_made.append(params or {})
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            await platform.list_issues_by_label(
                "bug", state="open", sort=sort_in, direction=dir_in
            )

        assert requests_made[0]["sort"] == expected_sort


# ===========================================================================
# TS-05-15: list_issues_by_label unmapped sort+direction defaults to 'newest'
# Requirement: 05-REQ-4.3
# ===========================================================================


class TestListIssuesByLabelSortFallback:
    """Verify unmapped sort+direction combination silently defaults to 'newest'."""

    @pytest.mark.asyncio
    async def test_sort_fallback_to_newest(self) -> None:
        """TS-05-15: sort='comments', direction='asc' → sort=newest silently."""
        platform = _make_platform()

        mock_resp = _json_response(200, [])

        requests_made: list[dict] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            requests_made.append(params or {})
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            result = await platform.list_issues_by_label(
                "bug", sort="comments", direction="asc"
            )

        assert requests_made[0]["sort"] == "newest"
        assert result == []


# ===========================================================================
# TS-05-16: list_issues_by_label always includes type=issues
# Requirement: 05-REQ-4.4
# ===========================================================================


class TestListIssuesByLabelTypeFilter:
    """Verify list_issues_by_label always includes type=issues query parameter."""

    @pytest.mark.asyncio
    async def test_type_issues_always_present(self) -> None:
        """TS-05-16: type=issues is present in every GET request."""
        platform = _make_platform()

        mock_resp = _json_response(200, [])

        requests_made: list[dict] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            requests_made.append(params or {})
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            await platform.list_issues_by_label("bug")

        assert requests_made[0].get("type") == "issues"


# ===========================================================================
# TS-05-E7: list_issues_by_label raises IntegrationError on non-200 with
#           truncated text
# Requirement: 05-REQ-4.E1
# ===========================================================================


class TestListIssuesByLabelError:
    """Verify list_issues_by_label raises IntegrationError on API error."""

    @pytest.mark.asyncio
    async def test_raises_on_422_with_truncated_text(self) -> None:
        """TS-05-E7: GET returns 422 with 600-char body; error text ≤ 500 chars."""
        platform = _make_platform()

        long_error = "E" * 600
        mock_resp = _json_response(422, text=long_error)
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.list_issues_by_label("bug")

        error_msg = str(exc_info.value)
        assert "E" * 501 not in error_msg


# ===========================================================================
# TS-05-E8: list_issues_by_label returns empty list on empty API response
# Requirement: 05-REQ-4.E2
# ===========================================================================


class TestListIssuesByLabelEmpty:
    """Verify list_issues_by_label returns empty list when API returns []."""

    @pytest.mark.asyncio
    async def test_empty_response_returns_empty_list(self) -> None:
        """TS-05-E8: GET returns 200 with []; result == []."""
        platform = _make_platform()

        mock_resp = _json_response(200, [])
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.list_issues_by_label("bug")

        assert result == []


# ===========================================================================
# TS-05-17: add_issue_comment POSTs to correct endpoint with body, returns None
# Requirement: 05-REQ-5.1
# ===========================================================================


class TestAddIssueComment:
    """Verify add_issue_comment sends correct POST and returns None."""

    @pytest.mark.asyncio
    async def test_add_issue_comment_posts_body_and_returns_none(self) -> None:
        """TS-05-17: POST to /issues/42/comments with body field; returns None."""
        platform = _make_platform()

        mock_resp = _json_response(201, {})

        requests_made: list[tuple[str, dict]] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            requests_made.append((url, json or {}))
            return mock_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            result = await platform.add_issue_comment(42, "This is a comment")

        assert len(requests_made) == 1
        url, payload = requests_made[0]
        assert "/issues/42/comments" in url
        assert payload["body"] == "This is a comment"
        assert result is None


# ===========================================================================
# TS-05-18: add_issue_comment treats any 2xx response as success
# Requirement: 05-REQ-5.2
# ===========================================================================


class TestAddIssueComment2xxSuccess:
    """Verify add_issue_comment treats any 2xx response as success."""

    @pytest.mark.asyncio
    async def test_add_issue_comment_200_is_success(self) -> None:
        """TS-05-18: POST returns 200 (not 201); no exception; returns None."""
        platform = _make_platform()

        mock_resp = _json_response(200, {})
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.add_issue_comment(10, "Hello")

        assert result is None


# ===========================================================================
# TS-05-E9: add_issue_comment raises IntegrationError on non-2xx with
#           truncated text
# Requirement: 05-REQ-5.E1
# ===========================================================================


class TestAddIssueCommentError:
    """Verify add_issue_comment raises IntegrationError with truncated text."""

    @pytest.mark.asyncio
    async def test_add_issue_comment_raises_on_500_truncated(self) -> None:
        """TS-05-E9: POST returns 500 with 600-char body; error text ≤ 500 chars."""
        platform = _make_platform()

        long_error = "F" * 600
        mock_resp = _json_response(500, text=long_error)
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.add_issue_comment(1, "text")

        error_msg = str(exc_info.value)
        assert "F" * 501 not in error_msg


# ===========================================================================
# TS-05-19: assign_label resolves label name to numeric ID and POSTs labels=[id]
# Requirement: 05-REQ-6.1
# ===========================================================================


class TestAssignLabel:
    """Verify assign_label resolves label and POSTs with numeric ID array."""

    @pytest.mark.asyncio
    async def test_assign_label_posts_numeric_id_array(self) -> None:
        """TS-05-19: _resolve_label_id returns 99; POST body has labels=[99]."""
        platform = _make_platform()

        mock_resp = _json_response(200, {})

        requests_made: list[tuple[str, dict]] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            requests_made.append((url, json or {}))
            return mock_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client), patch.object(
            platform, "_resolve_label_id", new_callable=AsyncMock, return_value=99
        ):
            result = await platform.assign_label(5, "enhancement")

        assert len(requests_made) == 1
        url, payload = requests_made[0]
        assert "/issues/5/labels" in url
        assert payload["labels"] == [99]
        assert result is None


# ===========================================================================
# TS-05-E10: assign_label raises IntegrationError on non-2xx with truncated text
# Requirement: 05-REQ-6.E1
# ===========================================================================


class TestAssignLabelError:
    """Verify assign_label raises IntegrationError with truncated response text."""

    @pytest.mark.asyncio
    async def test_assign_label_raises_on_422_truncated(self) -> None:
        """TS-05-E10: POST returns 422 with 600-char body; error ≤ 500 chars."""
        platform = _make_platform()

        long_error = "G" * 600
        mock_resp = _json_response(422, text=long_error)
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client), patch.object(
            platform, "_resolve_label_id", new_callable=AsyncMock, return_value=1
        ):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.assign_label(1, "bug")

        error_msg = str(exc_info.value)
        assert "G" * 501 not in error_msg
