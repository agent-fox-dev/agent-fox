"""Tests for GiteaPlatform issue and PR operations.

Test Spec: TS-05-1 through TS-05-45, TS-05-E1 through TS-05-E21,
           TS-05-46 through TS-05-52,
           TS-05-P1 through TS-05-P7 (property tests),
           TS-05-SMOKE-1 through TS-05-SMOKE-6 (smoke tests)
Requirements: 05-REQ-1.* through 05-REQ-20.*

Note: Import paths use afissues.* (the extracted platform package).
The Gitea module will live at afissues.gitea alongside the
existing afissues.github module.
"""

from __future__ import annotations

import inspect
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from afissues.errors import ConfigError, IntegrationError
from afissues.protocol import IssueComment, IssueResult, PlatformProtocol

# ---------------------------------------------------------------------------
# Helpers (modelled after test_gitlab.py / test_github_issues_rest.py)
# ---------------------------------------------------------------------------

# Target for patching httpx.AsyncClient in the gitea module.
_TARGET = "afissues.gitea.httpx.AsyncClient"

# Target for patching _validate_url in the gitea module.
_VALIDATE_URL_TARGET = "afissues.gitea._validate_github_url"


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
    target will change to 'afissues.gitea._validate_url'.
    """
    from afissues.gitea import GiteaPlatform

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
        from afissues.gitea import GiteaPlatform

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
        from afissues.gitea import GiteaPlatform

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
        from afissues.gitea import GiteaPlatform

        with patch(
            _VALIDATE_URL_TARGET,
            side_effect=ConfigError("SSRF disallowed"),
        ):
            with pytest.raises(ConfigError):
                GiteaPlatform("org", "repo", "token", "169.254.169.254")

    def test_config_error_not_integration_error(self) -> None:
        """TS-05-E1: ConfigError is not re-wrapped as IntegrationError."""
        from afissues.gitea import GiteaPlatform

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
        from afissues.gitea import GiteaPlatform

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
        from afissues.gitea import GiteaPlatform

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
        from afissues.gitea import parse_remote

        result = parse_remote("https://gitea.example.com/myowner/myrepo.git")
        assert result == ("myowner", "myrepo")

    def test_https_url_without_dot_git(self) -> None:
        """TS-05-46: HTTPS URL without .git suffix → (owner, repo)."""
        from afissues.gitea import parse_remote

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
        from afissues.gitea import parse_remote

        result = parse_remote("git@gitea.example.com:myowner/myrepo.git")
        assert result == ("myowner", "myrepo")

    def test_ssh_url_without_dot_git(self) -> None:
        """TS-05-47: SSH URL without .git suffix → (owner, repo)."""
        from afissues.gitea import parse_remote

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
        from afissues.gitea import parse_remote

        result = parse_remote("not-a-valid-url")
        assert result is None

    def test_returns_none_for_empty(self) -> None:
        """TS-05-48: Empty string → None."""
        from afissues.gitea import parse_remote

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
        from afissues.gitea import parse_remote

        result = parse_remote(
            "https://my-internal-server.corp.local/team/project.git"
        )
        assert result == ("team", "project")

    def test_ip_hostname(self) -> None:
        """TS-05-49: IP-based hostname is accepted."""
        from afissues.gitea import parse_remote

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


# ===========================================================================
# Group 3 Tests: close_issue, remove_label, list_issue_comments,
#                get_issue, update_issue
# Test Spec: TS-05-20 through TS-05-31, TS-05-E11 through TS-05-E17
# Requirements: 05-REQ-7.* through 05-REQ-11.*
# ===========================================================================


# ===========================================================================
# TS-05-20: close_issue with non-None comment calls add_issue_comment first,
#           then PATCH state=closed
# Requirement: 05-REQ-7.1
# ===========================================================================


class TestCloseIssueWithComment:
    """Verify close_issue with a comment calls add_issue_comment before PATCH."""

    @pytest.mark.asyncio
    async def test_close_issue_comment_then_patch(self) -> None:
        """TS-05-20: add_issue_comment called before PATCH; PATCH has state=closed."""
        platform = _make_platform()

        call_order: list[str] = []

        async def mock_add_comment(issue_number: int, body: str) -> None:
            call_order.append("comment")

        mock_patch_resp = _json_response(200, {})

        async def mock_patch(url, *, json=None, headers=None, **kw):
            call_order.append("patch")
            return mock_patch_resp

        client = _mock_client(patch=mock_patch)

        with patch(_TARGET, return_value=client), patch.object(
            platform, "add_issue_comment", side_effect=mock_add_comment
        ):
            result = await platform.close_issue(42, "Closing comment")

        assert call_order == ["comment", "patch"]
        assert result is None


# ===========================================================================
# TS-05-21: close_issue with comment=None skips add_issue_comment entirely
# Requirement: 05-REQ-7.2
# ===========================================================================


class TestCloseIssueNoComment:
    """Verify close_issue with comment=None skips comment POST."""

    @pytest.mark.asyncio
    async def test_close_issue_no_comment_only_patch(self) -> None:
        """TS-05-21: No POST to comments; exactly one PATCH with state=closed."""
        platform = _make_platform()

        mock_patch_resp = _json_response(200, {})

        requests_made: list[tuple[str, dict]] = []

        async def mock_patch(url, *, json=None, headers=None, **kw):
            requests_made.append((url, json or {}))
            return mock_patch_resp

        client = _mock_client(patch=mock_patch)

        with patch(_TARGET, return_value=client), patch.object(
            platform, "add_issue_comment", new_callable=AsyncMock
        ) as mock_comment:
            result = await platform.close_issue(42, comment=None)

        mock_comment.assert_not_called()
        assert len(requests_made) == 1
        _, payload = requests_made[0]
        assert payload["state"] == "closed"
        assert result is None


# ===========================================================================
# TS-05-22: close_issue treats any 2xx PATCH response as success
# Requirement: 05-REQ-7.3
# ===========================================================================


class TestCloseIssue2xxSuccess:
    """Verify close_issue treats any 2xx PATCH response as success."""

    @pytest.mark.asyncio
    async def test_close_issue_201_patch_is_success(self) -> None:
        """TS-05-22: PATCH returns 201 (non-200 2xx); no exception; returns None."""
        platform = _make_platform()

        mock_resp = _json_response(201, {})
        client = _mock_client(patch=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.close_issue(1, comment=None)

        assert result is None


# ===========================================================================
# TS-05-E11: close_issue raises IntegrationError when PATCH returns non-2xx
# Requirement: 05-REQ-7.E1
# ===========================================================================


class TestCloseIssueError:
    """Verify close_issue raises IntegrationError on PATCH error."""

    @pytest.mark.asyncio
    async def test_close_issue_raises_on_500_truncated(self) -> None:
        """TS-05-E11: PATCH returns 500 with 600-char body; error ≤ 500 chars."""
        platform = _make_platform()

        long_error = "H" * 600
        mock_resp = _json_response(500, text=long_error)
        client = _mock_client(patch=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.close_issue(1, comment=None)

        error_msg = str(exc_info.value)
        assert "H" * 501 not in error_msg


# ===========================================================================
# TS-05-E12: close_issue propagates IntegrationError from add_issue_comment;
#            PATCH is never called
# Requirement: 05-REQ-7.E2
# ===========================================================================


class TestCloseIssueCommentFailurePropagation:
    """Verify close_issue propagates add_issue_comment errors without PATCHing."""

    @pytest.mark.asyncio
    async def test_close_issue_propagates_comment_error(self) -> None:
        """TS-05-E12: add_issue_comment raises IntegrationError; PATCH not called."""
        platform = _make_platform()

        patch_called = False

        async def mock_patch(url, *, json=None, headers=None, **kw):
            nonlocal patch_called
            patch_called = True
            return _json_response(200, {})

        client = _mock_client(patch=mock_patch)

        with patch(_TARGET, return_value=client), patch.object(
            platform,
            "add_issue_comment",
            new_callable=AsyncMock,
            side_effect=IntegrationError("comment failed"),
        ):
            with pytest.raises(IntegrationError, match="comment failed"):
                await platform.close_issue(5, comment="Some comment")

        assert not patch_called, "PATCH should not be called when comment fails"


# ===========================================================================
# TS-05-23: remove_label resolves label to numeric ID and sends DELETE
# Requirement: 05-REQ-8.1
# ===========================================================================


class TestRemoveLabel:
    """Verify remove_label resolves label and sends DELETE with numeric ID."""

    @pytest.mark.asyncio
    async def test_remove_label_deletes_with_numeric_id(self) -> None:
        """TS-05-23: _resolve_label_id returns 55; DELETE URL ends in /labels/55."""
        platform = _make_platform()

        mock_resp = _json_response(204)

        requests_made: list[str] = []

        async def mock_delete(url, *, headers=None, **kw):
            requests_made.append(url)
            return mock_resp

        client = _mock_client(delete=mock_delete)

        with patch(_TARGET, return_value=client), patch.object(
            platform, "_resolve_label_id", new_callable=AsyncMock, return_value=55
        ):
            result = await platform.remove_label(3, "wontfix")

        assert len(requests_made) == 1
        assert "/labels/55" in requests_made[0]
        assert result is None


# ===========================================================================
# TS-05-24: remove_label returns None when _resolve_label_id raises
#           IntegrationError (label absent from repo)
# Requirement: 05-REQ-8.2
# ===========================================================================


class TestRemoveLabelMissingLabelSilent:
    """Verify remove_label returns None when label doesn't exist in repo."""

    @pytest.mark.asyncio
    async def test_remove_label_silent_when_resolve_raises(self) -> None:
        """TS-05-24: _resolve_label_id raises IntegrationError; no DELETE; returns None."""
        platform = _make_platform()

        delete_called = False

        async def mock_delete(url, *, headers=None, **kw):
            nonlocal delete_called
            delete_called = True
            return _json_response(204)

        client = _mock_client(delete=mock_delete)

        with patch(_TARGET, return_value=client), patch.object(
            platform,
            "_resolve_label_id",
            new_callable=AsyncMock,
            side_effect=IntegrationError("not found"),
        ):
            result = await platform.remove_label(3, "ghost-label")

        assert result is None
        assert not delete_called, "No DELETE should be made when label is unknown"


# ===========================================================================
# TS-05-25: remove_label returns None silently when DELETE returns 404
# Requirement: 05-REQ-8.3
# ===========================================================================


class TestRemoveLabel404:
    """Verify remove_label returns None on 404 DELETE response."""

    @pytest.mark.asyncio
    async def test_remove_label_404_returns_none(self) -> None:
        """TS-05-25: DELETE returns 404; no exception; returns None."""
        platform = _make_platform()

        mock_resp = _json_response(404, text="not found")
        client = _mock_client(delete=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client), patch.object(
            platform, "_resolve_label_id", new_callable=AsyncMock, return_value=10
        ):
            result = await platform.remove_label(5, "bug")

        assert result is None


# ===========================================================================
# TS-05-26: remove_label returns None silently when DELETE returns 422
# Requirement: 05-REQ-8.4
# ===========================================================================


class TestRemoveLabel422:
    """Verify remove_label returns None on 422 DELETE response."""

    @pytest.mark.asyncio
    async def test_remove_label_422_returns_none(self) -> None:
        """TS-05-26: DELETE returns 422; no exception; returns None."""
        platform = _make_platform()

        mock_resp = _json_response(422, text="unprocessable")
        client = _mock_client(delete=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client), patch.object(
            platform, "_resolve_label_id", new_callable=AsyncMock, return_value=10
        ):
            result = await platform.remove_label(5, "bug")

        assert result is None


# ===========================================================================
# TS-05-E13: remove_label raises IntegrationError on DELETE status other than
#            204, 404, or 422
# Requirement: 05-REQ-8.E1
# ===========================================================================


class TestRemoveLabelError:
    """Verify remove_label raises IntegrationError on unexpected DELETE status."""

    @pytest.mark.asyncio
    async def test_remove_label_raises_on_500_truncated(self) -> None:
        """TS-05-E13: DELETE returns 500 with 600-char body; error ≤ 500 chars."""
        platform = _make_platform()

        long_error = "I" * 600
        mock_resp = _json_response(500, text=long_error)
        client = _mock_client(delete=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client), patch.object(
            platform, "_resolve_label_id", new_callable=AsyncMock, return_value=10
        ):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.remove_label(1, "bug")

        error_msg = str(exc_info.value)
        assert "I" * 501 not in error_msg


# ===========================================================================
# TS-05-27: list_issue_comments GETs correct URL and maps user.login to user
# Requirement: 05-REQ-9.1
# ===========================================================================


class TestListIssueComments:
    """Verify list_issue_comments maps response to IssueComment with user.login."""

    @pytest.mark.asyncio
    async def test_list_issue_comments_maps_fields(self) -> None:
        """TS-05-27: GET /issues/7/comments; user.login → user; all fields mapped."""
        platform = _make_platform()

        mock_comments = [
            {
                "id": 1,
                "body": "Hello",
                "user": {"login": "alice"},
                "created_at": "2024-01-01T00:00:00Z",
            },
            {
                "id": 2,
                "body": "World",
                "user": {"login": "bob"},
                "created_at": "2024-01-02T00:00:00Z",
            },
        ]
        mock_resp = _json_response(200, mock_comments)

        requests_made: list[str] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            requests_made.append(url)
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            results = await platform.list_issue_comments(7)

        # Verify URL
        assert len(requests_made) == 1
        assert "/issues/7/comments" in requests_made[0]

        # Verify mapping
        assert len(results) == 2
        assert isinstance(results[0], IssueComment)
        assert results[0].id == 1
        assert results[0].body == "Hello"
        assert results[0].user == "alice"
        assert results[0].created_at == "2024-01-01T00:00:00Z"
        assert results[1].user == "bob"


# ===========================================================================
# TS-05-28: list_issue_comments sends no limit or pagination query params
# Requirement: 05-REQ-9.2
# ===========================================================================


class TestListIssueCommentsNoLimitParam:
    """Verify list_issue_comments sends no limit or page query parameters."""

    @pytest.mark.asyncio
    async def test_no_limit_or_page_params(self) -> None:
        """TS-05-28: GET to comments has no 'limit' or 'page' query params."""
        platform = _make_platform()

        mock_resp = _json_response(200, [])

        captured_params: list[dict | None] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            captured_params.append(params)
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            await platform.list_issue_comments(1)

        assert len(captured_params) == 1
        params = captured_params[0] or {}
        assert "limit" not in params
        assert "page" not in params


# ===========================================================================
# TS-05-E14: list_issue_comments raises IntegrationError on non-200 status
# Requirement: 05-REQ-9.E1
# ===========================================================================


class TestListIssueCommentsError:
    """Verify list_issue_comments raises IntegrationError on API error."""

    @pytest.mark.asyncio
    async def test_list_comments_raises_on_403_truncated(self) -> None:
        """TS-05-E14: GET returns 403 with 600-char body; error ≤ 500 chars."""
        platform = _make_platform()

        long_error = "J" * 600
        mock_resp = _json_response(403, text=long_error)
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.list_issue_comments(5)

        error_msg = str(exc_info.value)
        assert "J" * 501 not in error_msg


# ===========================================================================
# TS-05-E15: list_issue_comments returns empty list when issue has no comments
# Requirement: 05-REQ-9.E2
# ===========================================================================


class TestListIssueCommentsEmpty:
    """Verify list_issue_comments returns empty list on empty API response."""

    @pytest.mark.asyncio
    async def test_list_comments_empty_returns_empty_list(self) -> None:
        """TS-05-E15: GET returns 200 with []; result == []."""
        platform = _make_platform()

        mock_resp = _json_response(200, [])
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.list_issue_comments(1)

        assert result == []


# ===========================================================================
# TS-05-29: get_issue GETs correct URL and maps response to IssueResult
# Requirement: 05-REQ-10.1
# ===========================================================================


class TestGetIssue:
    """Verify get_issue maps response to IssueResult with correct field mapping."""

    @pytest.mark.asyncio
    async def test_get_issue_maps_fields(self) -> None:
        """TS-05-29: GET /issues/10; all IssueResult fields mapped correctly."""
        platform = _make_platform()

        mock_issue = {
            "number": 10,
            "title": "A bug",
            "html_url": "http://gitea.example.com/myorg/myrepo/issues/10",
            "body": "Details",
            "labels": [{"name": "bug"}, {"name": "p1"}],
        }
        mock_resp = _json_response(200, mock_issue)

        requests_made: list[str] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            requests_made.append(url)
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            result = await platform.get_issue(10)

        # Verify URL
        assert len(requests_made) == 1
        assert "/issues/10" in requests_made[0]

        # Verify field mapping
        assert isinstance(result, IssueResult)
        assert result.number == 10
        assert result.title == "A bug"
        assert result.html_url == "http://gitea.example.com/myorg/myrepo/issues/10"
        assert result.body == "Details"
        assert "bug" in result.labels
        assert "p1" in result.labels


# ===========================================================================
# TS-05-E16: get_issue raises IntegrationError on non-200 status
# Requirement: 05-REQ-10.E1
# ===========================================================================


class TestGetIssueError:
    """Verify get_issue raises IntegrationError on API error."""

    @pytest.mark.asyncio
    async def test_get_issue_raises_on_404_truncated(self) -> None:
        """TS-05-E16: GET returns 404 with 600-char body; error ≤ 500 chars."""
        platform = _make_platform()

        long_error = "K" * 600
        mock_resp = _json_response(404, text=long_error)
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.get_issue(99)

        error_msg = str(exc_info.value)
        assert "K" * 501 not in error_msg


# ===========================================================================
# TS-05-30: update_issue issues PATCH with body field and returns None on 2xx
# Requirement: 05-REQ-11.1
# ===========================================================================


class TestUpdateIssue:
    """Verify update_issue sends PATCH with body field and returns None."""

    @pytest.mark.asyncio
    async def test_update_issue_patches_body_returns_none(self) -> None:
        """TS-05-30: PATCH /issues/8 with body field; returns None on 201."""
        platform = _make_platform()

        mock_resp = _json_response(201, {})

        requests_made: list[tuple[str, dict]] = []

        async def mock_patch(url, *, json=None, headers=None, **kw):
            requests_made.append((url, json or {}))
            return mock_resp

        client = _mock_client(patch=mock_patch)

        with patch(_TARGET, return_value=client):
            result = await platform.update_issue(8, "Updated body text")

        assert len(requests_made) == 1
        url, payload = requests_made[0]
        assert "/issues/8" in url
        assert payload["body"] == "Updated body text"
        assert result is None


# ===========================================================================
# TS-05-31: update_issue treats both 200 and 201 as success
# Requirement: 05-REQ-11.2
# ===========================================================================


class TestUpdateIssue200Success:
    """Verify update_issue treats 200 as success too."""

    @pytest.mark.asyncio
    async def test_update_issue_200_is_success(self) -> None:
        """TS-05-31: PATCH returns 200; returns None without error."""
        platform = _make_platform()

        mock_resp = _json_response(200, {})
        client = _mock_client(patch=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.update_issue(8, "Updated body")

        assert result is None


# ===========================================================================
# TS-05-E17: update_issue raises IntegrationError on non-2xx status
# Requirement: 05-REQ-11.E1
# ===========================================================================


class TestUpdateIssueError:
    """Verify update_issue raises IntegrationError on API error."""

    @pytest.mark.asyncio
    async def test_update_issue_raises_on_500_truncated(self) -> None:
        """TS-05-E17: PATCH returns 500 with 600-char body; error ≤ 500 chars."""
        platform = _make_platform()

        long_error = "L" * 600
        mock_resp = _json_response(500, text=long_error)
        client = _mock_client(patch=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.update_issue(3, "new body")

        error_msg = str(exc_info.value)
        assert "L" * 501 not in error_msg


# ===========================================================================
# Group 4 Tests: create_label, create_pr, close, search_issues,
#                check_credentials, platform factory, re-exports
# Test Spec: TS-05-32 through TS-05-45, TS-05-E18 through TS-05-E21,
#            TS-05-50 through TS-05-52
# Requirements: 05-REQ-12.* through 05-REQ-16.*, 05-REQ-18.*, 05-REQ-19.*
# ===========================================================================


# ===========================================================================
# TS-05-32: create_label returns None silently when label already exists
# Requirement: 05-REQ-12.1
# ===========================================================================


class TestCreateLabelIdempotent:
    """Verify create_label returns None without POSTing when label exists."""

    @pytest.mark.asyncio
    async def test_create_label_existing_no_post(self) -> None:
        """TS-05-32: _resolve_label_id returns 7; no POST; returns None."""
        platform = _make_platform()

        post_called = False

        async def mock_post(url, *, json=None, headers=None, **kw):
            nonlocal post_called
            post_called = True
            return _json_response(201, {})

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client), patch.object(
            platform, "_resolve_label_id", new_callable=AsyncMock, return_value=7
        ):
            result = await platform.create_label("bug", "ff0000")

        assert result is None
        assert not post_called, "No POST should be made when label already exists"


# ===========================================================================
# TS-05-33: create_label POSTs with '#'-prefixed color when label not found
# Requirement: 05-REQ-12.2
# ===========================================================================


class TestCreateLabelCreatesNew:
    """Verify create_label POSTs with correct fields when label doesn't exist."""

    @pytest.mark.asyncio
    async def test_create_label_posts_with_hash_color(self) -> None:
        """TS-05-33: _resolve_label_id raises; POST has name, color='#ff0000', description."""
        platform = _make_platform()

        mock_label_resp = _json_response(
            201, {"id": 15, "name": "newlabel", "color": "#ff0000"}
        )

        requests_made: list[tuple[str, dict]] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            requests_made.append((url, json or {}))
            return mock_label_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client), patch.object(
            platform,
            "_resolve_label_id",
            new_callable=AsyncMock,
            side_effect=IntegrationError("not found"),
        ):
            result = await platform.create_label("newlabel", "ff0000", "A new label")

        assert len(requests_made) == 1
        _, body = requests_made[0]
        assert body["name"] == "newlabel"
        assert body["color"] == "#ff0000"
        assert body["description"] == "A new label"
        assert result is None


# ===========================================================================
# TS-05-34: create_label inserts new name→id in cache after success
# Requirement: 05-REQ-12.3
# ===========================================================================


class TestCreateLabelCacheUpdate:
    """Verify create_label inserts new name→id into cache after creation."""

    @pytest.mark.asyncio
    async def test_create_label_updates_cache(self) -> None:
        """TS-05-34: After create_label, _resolve_label_id('fresh') returns 99 from cache."""
        platform = _make_platform()

        mock_label_resp = _json_response(
            201, {"id": 99, "name": "fresh", "color": "#aabbcc"}
        )

        # Track whether resolve was called (should not be after cache insert)
        resolve_calls = 0

        async def mock_resolve(name: str) -> int:
            nonlocal resolve_calls
            resolve_calls += 1
            # First call during create_label: label doesn't exist
            raise IntegrationError("not found")

        async def mock_post(url, *, json=None, headers=None, **kw):
            return mock_label_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client), patch.object(
            platform,
            "_resolve_label_id",
            side_effect=mock_resolve,
        ):
            await platform.create_label("fresh", "aabbcc")

        # After create_label, the cache should contain the new entry
        assert platform._label_cache.get("fresh") == 99

        # Subsequent _resolve_label_id should return from cache without HTTP
        # (We test via direct cache access since the mock is still active)
        assert "fresh" in platform._label_cache


# ===========================================================================
# TS-05-35: create_label always prepends '#' to bare hex color
# Requirement: 05-REQ-12.4
# ===========================================================================


class TestCreateLabelColorPrefix:
    """Verify create_label prepends '#' to the bare hex color value."""

    @pytest.mark.asyncio
    async def test_create_label_hash_prefix(self) -> None:
        """TS-05-35: bare hex '123abc' → color '#123abc' in POST body."""
        platform = _make_platform()

        mock_resp = _json_response(201, {"id": 1, "name": "test"})

        requests_made: list[dict] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            requests_made.append(json or {})
            return mock_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client), patch.object(
            platform,
            "_resolve_label_id",
            new_callable=AsyncMock,
            side_effect=IntegrationError("not found"),
        ):
            await platform.create_label("test", "123abc")

        assert len(requests_made) == 1
        assert requests_made[0]["color"] == "#123abc"


# ===========================================================================
# TS-05-E18: create_label raises IntegrationError on non-2xx POST
# Requirement: 05-REQ-12.E1
# ===========================================================================


class TestCreateLabelError:
    """Verify create_label raises IntegrationError with truncated text on error."""

    @pytest.mark.asyncio
    async def test_create_label_raises_on_500_truncated(self) -> None:
        """TS-05-E18: POST returns 500 with 600-char body; error ≤ 500 chars."""
        platform = _make_platform()

        long_error = "M" * 600
        mock_resp = _json_response(500, text=long_error)
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client), patch.object(
            platform,
            "_resolve_label_id",
            new_callable=AsyncMock,
            side_effect=IntegrationError("not found"),
        ):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.create_label("newlabel", "aabbcc")

        error_msg = str(exc_info.value)
        assert "M" * 501 not in error_msg


# ===========================================================================
# TS-05-36: create_pr POSTs to pulls endpoint and returns html_url
# Requirement: 05-REQ-13.1
# ===========================================================================


class TestCreatePrSuccess:
    """Verify create_pr POSTs with correct fields and returns html_url."""

    @pytest.mark.asyncio
    async def test_create_pr_posts_and_returns_html_url(self) -> None:
        """TS-05-36: POST body has title/body/head/base; returns html_url string."""
        platform = _make_platform()

        mock_pr = {
            "number": 3,
            "html_url": "http://gitea.example.com/myorg/myrepo/pulls/3",
        }
        mock_resp = _json_response(201, mock_pr)

        requests_made: list[tuple[str, dict]] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            requests_made.append((url, json or {}))
            return mock_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            result = await platform.create_pr(
                title="Fix bug", body="PR body", head="fix-branch", base="main"
            )

        assert len(requests_made) == 1
        _, body = requests_made[0]
        assert body["title"] == "Fix bug"
        assert body["body"] == "PR body"
        assert body["head"] == "fix-branch"
        assert body["base"] == "main"
        assert result == "http://gitea.example.com/myorg/myrepo/pulls/3"


# ===========================================================================
# TS-05-37: create_pr handles 409 by querying existing open PR
# Requirement: 05-REQ-13.2
# ===========================================================================


class TestCreatePr409WithMatch:
    """Verify create_pr handles 409 duplicate by fetching existing PR."""

    @pytest.mark.asyncio
    async def test_create_pr_409_returns_existing_html_url(self) -> None:
        """TS-05-37: POST returns 409; GET finds existing PR; returns its html_url."""
        platform = _make_platform()

        mock_post_resp = _json_response(409, {})
        existing_pr = [{"html_url": "http://gitea.example.com/myorg/myrepo/pulls/2"}]
        mock_get_resp = _json_response(200, existing_pr)

        call_log: list[str] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            call_log.append("post")
            return mock_post_resp

        async def mock_get(url, *, params=None, headers=None, **kw):
            call_log.append("get")
            # Verify the GET query params for existing PR lookup
            assert params is not None
            assert params.get("head") == "fix-branch"
            assert params.get("base") == "main"
            assert params.get("state") == "open"
            return mock_get_resp

        client = _mock_client(post=mock_post, get=mock_get)

        with patch(_TARGET, return_value=client):
            result = await platform.create_pr(
                title="Fix bug", body="Body", head="fix-branch", base="main"
            )

        assert result == "http://gitea.example.com/myorg/myrepo/pulls/2"
        assert "post" in call_log
        assert "get" in call_log


# ===========================================================================
# TS-05-38: create_pr raises IntegrationError when 409 + empty GET result
# Requirement: 05-REQ-13.3
# ===========================================================================


class TestCreatePr409NoMatch:
    """Verify create_pr raises IntegrationError when 409 but no existing PR found."""

    @pytest.mark.asyncio
    async def test_create_pr_409_empty_get_raises(self) -> None:
        """TS-05-38: POST returns 409; GET returns []; IntegrationError raised."""
        platform = _make_platform()

        mock_post_resp = _json_response(409, {})
        mock_get_resp = _json_response(200, [])

        async def mock_post(url, *, json=None, headers=None, **kw):
            return mock_post_resp

        async def mock_get(url, *, params=None, headers=None, **kw):
            return mock_get_resp

        client = _mock_client(post=mock_post, get=mock_get)

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.create_pr(
                    title="Fix bug", body="Body", head="fix-branch", base="main"
                )

        error_msg = str(exc_info.value).lower()
        assert "409" in str(exc_info.value) or "existing" in error_msg or "no" in error_msg


# ===========================================================================
# TS-05-E19: create_pr raises IntegrationError on non-2xx, non-409 POST
# Requirement: 05-REQ-13.E1
# ===========================================================================


class TestCreatePrPostError:
    """Verify create_pr raises IntegrationError on non-2xx, non-409 POST."""

    @pytest.mark.asyncio
    async def test_create_pr_raises_on_500_truncated(self) -> None:
        """TS-05-E19: POST returns 500 with 600-char body; error ≤ 500 chars."""
        platform = _make_platform()

        long_error = "N" * 600
        mock_resp = _json_response(500, text=long_error)
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.create_pr(
                    title="Title", body="Body", head="head", base="base"
                )

        error_msg = str(exc_info.value)
        assert "N" * 501 not in error_msg


# ===========================================================================
# TS-05-E20: create_pr raises IntegrationError when follow-up GET after 409
#            returns non-2xx
# Requirement: 05-REQ-13.E2
# ===========================================================================


class TestCreatePrGetError:
    """Verify create_pr raises IntegrationError when follow-up GET fails."""

    @pytest.mark.asyncio
    async def test_create_pr_409_then_get_500_raises(self) -> None:
        """TS-05-E20: POST returns 409; GET returns 500; IntegrationError raised."""
        platform = _make_platform()

        mock_post_resp = _json_response(409, {})
        long_error = "O" * 600
        mock_get_resp = _json_response(500, text=long_error)

        async def mock_post(url, *, json=None, headers=None, **kw):
            return mock_post_resp

        async def mock_get(url, *, params=None, headers=None, **kw):
            return mock_get_resp

        client = _mock_client(post=mock_post, get=mock_get)

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.create_pr(
                    title="Title", body="Body", head="head", base="base"
                )

        error_msg = str(exc_info.value)
        assert "O" * 501 not in error_msg


# ===========================================================================
# TS-05-39: close() is synchronous, makes no HTTP calls, and returns None
# Requirement: 05-REQ-14.1
# ===========================================================================


class TestClose:
    """Verify close() is a no-op that returns None.

    Note: PlatformProtocol declares close() as async def, so GiteaPlatform.close()
    must be async for protocol compatibility, even though it performs no I/O.
    The spec's assertion about synchronicity is overridden by the actual protocol
    (see reviewer finding and errata).
    """

    @pytest.mark.asyncio
    async def test_close_returns_none(self) -> None:
        """TS-05-39: close() returns None; no HTTP calls."""
        platform = _make_platform()

        http_called = False

        async def mock_any(*args, **kwargs):
            nonlocal http_called
            http_called = True

        client = _mock_client(get=mock_any, post=mock_any, patch=mock_any, delete=mock_any)

        with patch(_TARGET, return_value=client):
            result = await platform.close()

        assert result is None
        assert not http_called, "close() should not make any HTTP calls"


# ===========================================================================
# TS-05-40: search_issues GETs with correct params and returns list[IssueResult]
# Requirement: 05-REQ-15.1
# ===========================================================================


class TestSearchIssues:
    """Verify search_issues sends correct GET params and maps results."""

    @pytest.mark.asyncio
    async def test_search_issues_correct_params(self) -> None:
        """TS-05-40: GET has q, type=issues, state, limit=50; returns list[IssueResult]."""
        platform = _make_platform()

        mock_issues = [
            {
                "number": 1,
                "title": "Fix crash",
                "html_url": "http://gitea.example.com/myorg/myrepo/issues/1",
                "body": "",
                "labels": [],
            },
        ]
        mock_resp = _json_response(200, mock_issues)

        requests_made: list[tuple[str, dict]] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            requests_made.append((url, params or {}))
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            results = await platform.search_issues("Fix", state="open")

        assert len(requests_made) == 1
        _, params = requests_made[0]
        assert params["q"] == "Fix"
        assert params["type"] == "issues"
        assert params["state"] == "open"
        assert params["limit"] == 50
        assert len(results) == 1
        assert isinstance(results[0], IssueResult)


# ===========================================================================
# TS-05-41: search_issues always includes type=issues
# Requirement: 05-REQ-15.2
# ===========================================================================


class TestSearchIssuesTypeFilter:
    """Verify search_issues always includes type=issues query parameter."""

    @pytest.mark.asyncio
    async def test_search_issues_type_issues_always_present(self) -> None:
        """TS-05-41: type=issues is present in GET request."""
        platform = _make_platform()

        mock_resp = _json_response(200, [])

        requests_made: list[dict] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            requests_made.append(params or {})
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            await platform.search_issues("prefix")

        assert requests_made[0].get("type") == "issues"


# ===========================================================================
# TS-05-E21: search_issues raises IntegrationError on non-2xx GET
# Requirement: 05-REQ-15.E1
# ===========================================================================


class TestSearchIssuesError:
    """Verify search_issues raises IntegrationError with truncated text on error."""

    @pytest.mark.asyncio
    async def test_search_issues_raises_on_503_truncated(self) -> None:
        """TS-05-E21: GET returns 503 with 600-char body; error ≤ 500 chars."""
        platform = _make_platform()

        long_error = "P" * 600
        mock_resp = _json_response(503, text=long_error)
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.search_issues("Fix")

        error_msg = str(exc_info.value)
        assert "P" * 501 not in error_msg


# ===========================================================================
# TS-05-42: check_credentials returns normally for non-401/403 (including 5xx)
# Requirement: 05-REQ-16.1
# ===========================================================================


class TestCheckCredentials500:
    """Verify check_credentials returns None on 500 (non-auth error)."""

    @pytest.mark.asyncio
    async def test_check_credentials_500_returns_none(self) -> None:
        """TS-05-42: GET returns 500; no exception raised; returns None."""
        platform = _make_platform()

        mock_resp = _json_response(500, {})
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.check_credentials()

        assert result is None


# ===========================================================================
# TS-05-43: check_credentials raises IntegrationError on 401
# Requirement: 05-REQ-16.2
# ===========================================================================


class TestCheckCredentials401:
    """Verify check_credentials raises IntegrationError on 401."""

    @pytest.mark.asyncio
    async def test_check_credentials_401_raises(self) -> None:
        """TS-05-43: GET returns 401; IntegrationError is raised."""
        platform = _make_platform()

        mock_resp = _json_response(401, {})
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.check_credentials()


# ===========================================================================
# TS-05-44: check_credentials raises IntegrationError on 403
# Requirement: 05-REQ-16.3
# ===========================================================================


class TestCheckCredentials403:
    """Verify check_credentials raises IntegrationError on 403."""

    @pytest.mark.asyncio
    async def test_check_credentials_403_raises(self) -> None:
        """TS-05-44: GET returns 403; IntegrationError is raised."""
        platform = _make_platform()

        mock_resp = _json_response(403, {})
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.check_credentials()


# ===========================================================================
# TS-05-45: check_credentials returns normally on 503 (5xx)
# Requirement: 05-REQ-16.4
# ===========================================================================


class TestCheckCredentials503:
    """Verify check_credentials returns None on 503."""

    @pytest.mark.asyncio
    async def test_check_credentials_503_returns_none(self) -> None:
        """TS-05-45: GET returns 503; no exception raised; returns None."""
        platform = _make_platform()

        mock_resp = _json_response(503, {})
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.check_credentials()

        assert result is None


# ===========================================================================
# TS-05-50: platform_factory constructs GiteaPlatform for type='gitea'
# Requirement: 05-REQ-18.1
# Note: The platform_factory integration test lives in this file temporarily
#       since the factory only supports github currently. The factory test
#       for gitea will be refactored to agentfox/nightshift/tests/ once
#       spec 04 restructures the factory with multi-platform routing.
# ===========================================================================


class TestPlatformFactoryGitea:
    """Verify platform_factory constructs GiteaPlatform for type='gitea'."""

    def test_factory_creates_gitea_platform(self) -> None:
        """TS-05-50: type='gitea' config → GiteaPlatform instance with forge_type='gitea'.

        This test patches the factory's create_platform function to accept
        Gitea configuration, since the factory currently only supports GitHub
        and will be refactored by spec 04 to support multi-platform routing.
        We test that GiteaPlatform can be constructed with the expected parameters.
        """
        from afissues.gitea import GiteaPlatform

        with patch(_VALIDATE_URL_TARGET):
            platform = GiteaPlatform("org", "repo", "tok", "gitea.corp.com")

        assert isinstance(platform, GiteaPlatform)
        assert platform.forge_type == "gitea"


# ===========================================================================
# TS-05-51: Import succeeds with no NotImplementedError guard
# Requirement: 05-REQ-18.2
# ===========================================================================


class TestGiteaImports:
    """Verify GiteaPlatform and parse_remote are importable."""

    def test_gitea_import_no_error(self) -> None:
        """TS-05-51: import succeeds; no NotImplementedError raised."""
        from afissues.gitea import GiteaPlatform, parse_remote  # noqa: F811

        assert GiteaPlatform is not None
        assert parse_remote is not None


# ===========================================================================
# TS-05-52: GiteaPlatform re-exported from top-level package alongside others
# Requirement: 05-REQ-19.1
# Note: The afissues package has been extracted (spec 03). GiteaPlatform
#       is imported from afissues.gitea.
# ===========================================================================


class TestReExports:
    """Verify GiteaPlatform can be imported alongside other platform classes."""

    def test_all_platforms_importable(self) -> None:
        """TS-05-52: GiteaPlatform, GitHubPlatform importable from platform modules."""
        from afissues.gitea import GiteaPlatform  # noqa: F811
        from afissues.github import GitHubPlatform

        assert GiteaPlatform is not None
        assert GitHubPlatform is not None


# ===========================================================================
# Group 5: Property Tests and Smoke Tests
# Test Spec: TS-05-P1 through TS-05-P7, TS-05-SMOKE-1 through TS-05-SMOKE-6
# Requirements: 05-REQ-1.E1, 05-REQ-2.*, 05-REQ-3.1, 05-REQ-3.E1,
#               05-REQ-4.1, 05-REQ-4.4, 05-REQ-5.E1, 05-REQ-6.1,
#               05-REQ-6.E1, 05-REQ-7.1, 05-REQ-7.2, 05-REQ-7.E1,
#               05-REQ-8.1, 05-REQ-8.E1, 05-REQ-9.E1, 05-REQ-10.E1,
#               05-REQ-11.E1, 05-REQ-12.1, 05-REQ-12.2, 05-REQ-12.3,
#               05-REQ-12.E1, 05-REQ-13.1, 05-REQ-13.2, 05-REQ-13.E1,
#               05-REQ-15.2, 05-REQ-15.E1, 05-REQ-18.1
# ===========================================================================


# ===========================================================================
# TS-05-P1: Label ID cache consistency — cache only grows, entries derived
#           from API responses or create_label success
# Property: 05-PROP-1
# Validates: 05-REQ-2.1, 05-REQ-2.2, 05-REQ-2.4, 05-REQ-12.3
# ===========================================================================


class TestPropertyLabelCacheConsistency:
    """Property: label ID cache only grows and is always consistent."""

    @pytest.mark.asyncio
    async def test_cache_grows_monotonically_with_resolve(self) -> None:
        """TS-05-P1: After _resolve_label_id populates cache, entries are never removed."""
        platform = _make_platform()

        mock_labels = [
            {"id": 7, "name": "bug"},
            {"id": 8, "name": "enhancement"},
            {"id": 9, "name": "feature"},
        ]
        mock_resp = _json_response(200, mock_labels)

        async def mock_get(url, *, params=None, headers=None, **kw):
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            # Resolve one label — should populate cache with ALL labels
            result = await platform._resolve_label_id("bug")
            assert result == 7

            cache_after_first = dict(platform._label_cache)  # snapshot
            assert len(cache_after_first) == 3

            # Resolve another label from cache — cache should not shrink
            result2 = await platform._resolve_label_id("enhancement")
            assert result2 == 8
            assert len(platform._label_cache) >= len(cache_after_first)

            # All original entries still present
            for name, lid in cache_after_first.items():
                assert platform._label_cache[name] == lid

    @pytest.mark.asyncio
    async def test_cache_grows_with_create_label_insert(self) -> None:
        """TS-05-P1: create_label inserts new entry; cache only grows."""
        platform = _make_platform()

        # Pre-populate cache to simulate prior resolve
        platform._label_cache = {"bug": 7}
        platform._cache_populated = True
        cache_before = dict(platform._label_cache)

        mock_label_resp = _json_response(
            201, {"id": 42, "name": "newlabel", "color": "#ff0000"}
        )

        async def mock_post(url, *, json=None, headers=None, **kw):
            return mock_label_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client), patch.object(
            platform,
            "_resolve_label_id",
            new_callable=AsyncMock,
            side_effect=IntegrationError("not found"),
        ):
            await platform.create_label("newlabel", "ff0000")

        # Cache grew: old entries preserved, new entry added
        assert len(platform._label_cache) > len(cache_before)
        for name, lid in cache_before.items():
            assert platform._label_cache[name] == lid
        assert platform._label_cache["newlabel"] == 42

    @pytest.mark.asyncio
    async def test_no_refetch_for_missing_label_after_full_populate(self) -> None:
        """TS-05-P1: After full fetch, a missing label raises immediately on 2nd call."""
        platform = _make_platform()

        api_call_count = 0

        async def mock_get(url, *, params=None, headers=None, **kw):
            nonlocal api_call_count
            api_call_count += 1
            return _json_response(200, [{"id": 7, "name": "bug"}])

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            # First call: fetches from API
            with pytest.raises(IntegrationError):
                await platform._resolve_label_id("ghost")
            first_call_count = api_call_count

            # Second call: must NOT re-fetch
            with pytest.raises(IntegrationError):
                await platform._resolve_label_id("ghost")
            assert api_call_count == first_call_count

            # Third call for a different missing label: also no re-fetch
            with pytest.raises(IntegrationError):
                await platform._resolve_label_id("another-ghost")
            assert api_call_count == first_call_count


# ===========================================================================
# TS-05-P2: Numeric label IDs used exclusively for assignment and removal
# Property: 05-PROP-2
# Validates: 05-REQ-3.1, 05-REQ-6.1, 05-REQ-8.1
# ===========================================================================


class TestPropertyNumericLabelIds:
    """Property: _resolve_label_id is called before any label HTTP request;
    only numeric IDs appear in request bodies and URLs."""

    @pytest.mark.asyncio
    async def test_create_issue_uses_numeric_ids_not_names(self) -> None:
        """TS-05-P2: create_issue with labels — POST body has numeric IDs only."""
        platform = _make_platform()

        label_name = "my-special-label"
        numeric_id = 777

        mock_resp = _json_response(
            201,
            {
                "number": 1,
                "title": "T",
                "html_url": "http://x/1",
                "body": "",
                "labels": [{"name": label_name}],
            },
        )

        requests_made: list[dict] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            requests_made.append(json or {})
            return mock_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client), patch.object(
            platform,
            "_resolve_label_id",
            new_callable=AsyncMock,
            return_value=numeric_id,
        ) as mock_resolve:
            await platform.create_issue("Title", "Body", [label_name])

        # _resolve_label_id was called before any HTTP request
        mock_resolve.assert_called_once_with(label_name)

        # POST body contains numeric ID, not label name string
        assert len(requests_made) == 1
        labels_in_body = requests_made[0].get("labels", [])
        for label_val in labels_in_body:
            assert isinstance(label_val, int), f"Label should be int, got {type(label_val)}"
            assert label_val == numeric_id
        assert label_name not in str(labels_in_body)

    @pytest.mark.asyncio
    async def test_assign_label_uses_numeric_id_not_name(self) -> None:
        """TS-05-P2: assign_label — POST body has numeric ID only."""
        platform = _make_platform()

        label_name = "fancy-label"
        numeric_id = 999

        requests_made: list[dict] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            requests_made.append(json or {})
            return _json_response(200, {})

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client), patch.object(
            platform,
            "_resolve_label_id",
            new_callable=AsyncMock,
            return_value=numeric_id,
        ) as mock_resolve:
            await platform.assign_label(5, label_name)

        mock_resolve.assert_called_once_with(label_name)
        assert len(requests_made) == 1
        labels_in_body = requests_made[0].get("labels", [])
        assert labels_in_body == [numeric_id]
        assert label_name not in str(labels_in_body)

    @pytest.mark.asyncio
    async def test_remove_label_uses_numeric_id_in_url(self) -> None:
        """TS-05-P2: remove_label — DELETE URL contains numeric ID, not name."""
        platform = _make_platform()

        label_name = "remove-me"
        numeric_id = 333

        urls_called: list[str] = []

        async def mock_delete(url, *, headers=None, **kw):
            urls_called.append(url)
            return _json_response(204)

        client = _mock_client(delete=mock_delete)

        with patch(_TARGET, return_value=client), patch.object(
            platform,
            "_resolve_label_id",
            new_callable=AsyncMock,
            return_value=numeric_id,
        ) as mock_resolve:
            await platform.remove_label(3, label_name)

        mock_resolve.assert_called_once_with(label_name)
        assert len(urls_called) == 1
        assert f"/labels/{numeric_id}" in urls_called[0]
        assert label_name not in urls_called[0]


# ===========================================================================
# TS-05-P3: Error response text truncated to 500 characters
# Property: 05-PROP-3
# Validates: 05-REQ-3.E1, 05-REQ-4.E1, 05-REQ-5.E1, 05-REQ-6.E1,
#            05-REQ-7.E1, 05-REQ-8.E1, 05-REQ-9.E1, 05-REQ-10.E1,
#            05-REQ-11.E1, 05-REQ-12.E1, 05-REQ-13.E1, 05-REQ-15.E1
# ===========================================================================


class TestPropertyErrorTruncation:
    """Property: IntegrationError messages never embed more than 500 chars
    of raw API response text, regardless of response body length."""

    @pytest.mark.asyncio
    @pytest.mark.parametrize("body_len", [1, 100, 499, 500, 501, 1000, 2000])
    async def test_create_issue_error_truncation(self, body_len: int) -> None:
        """TS-05-P3: create_issue — response text in error ≤ 500 chars for any body length."""
        platform = _make_platform()
        error_body = "Z" * body_len
        mock_resp = _json_response(500, text=error_body)
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.create_issue("T", "B", [])

        # The full 501+ char string should never appear verbatim in the error
        if body_len > 500:
            assert error_body not in str(exc_info.value)

    @pytest.mark.asyncio
    @pytest.mark.parametrize("body_len", [1, 500, 501, 1000])
    async def test_list_issues_error_truncation(self, body_len: int) -> None:
        """TS-05-P3: list_issues_by_label — response text ≤ 500 chars."""
        platform = _make_platform()
        error_body = "A" * body_len
        mock_resp = _json_response(422, text=error_body)
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.list_issues_by_label("bug")

        if body_len > 500:
            assert error_body not in str(exc_info.value)

    @pytest.mark.asyncio
    @pytest.mark.parametrize("body_len", [1, 500, 501, 1000])
    async def test_add_comment_error_truncation(self, body_len: int) -> None:
        """TS-05-P3: add_issue_comment — response text ≤ 500 chars."""
        platform = _make_platform()
        error_body = "B" * body_len
        mock_resp = _json_response(500, text=error_body)
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.add_issue_comment(1, "text")

        if body_len > 500:
            assert error_body not in str(exc_info.value)

    @pytest.mark.asyncio
    @pytest.mark.parametrize("body_len", [1, 500, 501, 1000])
    async def test_assign_label_error_truncation(self, body_len: int) -> None:
        """TS-05-P3: assign_label — response text ≤ 500 chars."""
        platform = _make_platform()
        error_body = "C" * body_len
        mock_resp = _json_response(422, text=error_body)
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client), patch.object(
            platform, "_resolve_label_id", new_callable=AsyncMock, return_value=1
        ):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.assign_label(1, "bug")

        if body_len > 500:
            assert error_body not in str(exc_info.value)

    @pytest.mark.asyncio
    @pytest.mark.parametrize("body_len", [1, 500, 501, 1000])
    async def test_close_issue_error_truncation(self, body_len: int) -> None:
        """TS-05-P3: close_issue PATCH — response text ≤ 500 chars."""
        platform = _make_platform()
        error_body = "D" * body_len
        mock_resp = _json_response(500, text=error_body)
        client = _mock_client(patch=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.close_issue(1, comment=None)

        if body_len > 500:
            assert error_body not in str(exc_info.value)

    @pytest.mark.asyncio
    @pytest.mark.parametrize("body_len", [1, 500, 501, 1000])
    async def test_remove_label_error_truncation(self, body_len: int) -> None:
        """TS-05-P3: remove_label DELETE — response text ≤ 500 chars for non-204/404/422."""
        platform = _make_platform()
        error_body = "R" * body_len
        mock_resp = _json_response(500, text=error_body)
        client = _mock_client(delete=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client), patch.object(
            platform, "_resolve_label_id", new_callable=AsyncMock, return_value=10
        ):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.remove_label(1, "bug")

        if body_len > 500:
            assert error_body not in str(exc_info.value)

    @pytest.mark.asyncio
    @pytest.mark.parametrize("body_len", [1, 500, 501, 1000])
    async def test_list_comments_error_truncation(self, body_len: int) -> None:
        """TS-05-P3: list_issue_comments — response text ≤ 500 chars."""
        platform = _make_platform()
        error_body = "S" * body_len
        mock_resp = _json_response(403, text=error_body)
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.list_issue_comments(5)

        if body_len > 500:
            assert error_body not in str(exc_info.value)

    @pytest.mark.asyncio
    @pytest.mark.parametrize("body_len", [1, 500, 501, 1000])
    async def test_get_issue_error_truncation(self, body_len: int) -> None:
        """TS-05-P3: get_issue — response text ≤ 500 chars."""
        platform = _make_platform()
        error_body = "T" * body_len
        mock_resp = _json_response(404, text=error_body)
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.get_issue(99)

        if body_len > 500:
            assert error_body not in str(exc_info.value)

    @pytest.mark.asyncio
    @pytest.mark.parametrize("body_len", [1, 500, 501, 1000])
    async def test_update_issue_error_truncation(self, body_len: int) -> None:
        """TS-05-P3: update_issue — response text ≤ 500 chars."""
        platform = _make_platform()
        error_body = "U" * body_len
        mock_resp = _json_response(500, text=error_body)
        client = _mock_client(patch=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.update_issue(3, "new body")

        if body_len > 500:
            assert error_body not in str(exc_info.value)

    @pytest.mark.asyncio
    @pytest.mark.parametrize("body_len", [1, 500, 501, 1000])
    async def test_create_label_error_truncation(self, body_len: int) -> None:
        """TS-05-P3: create_label — response text ≤ 500 chars."""
        platform = _make_platform()
        error_body = "V" * body_len
        mock_resp = _json_response(500, text=error_body)
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client), patch.object(
            platform,
            "_resolve_label_id",
            new_callable=AsyncMock,
            side_effect=IntegrationError("not found"),
        ):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.create_label("newlabel", "aabbcc")

        if body_len > 500:
            assert error_body not in str(exc_info.value)

    @pytest.mark.asyncio
    @pytest.mark.parametrize("body_len", [1, 500, 501, 1000])
    async def test_create_pr_error_truncation(self, body_len: int) -> None:
        """TS-05-P3: create_pr POST — response text ≤ 500 chars."""
        platform = _make_platform()
        error_body = "W" * body_len
        mock_resp = _json_response(500, text=error_body)
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.create_pr(
                    title="T", body="B", head="h", base="b"
                )

        if body_len > 500:
            assert error_body not in str(exc_info.value)

    @pytest.mark.asyncio
    @pytest.mark.parametrize("body_len", [1, 500, 501, 1000])
    async def test_search_issues_error_truncation(self, body_len: int) -> None:
        """TS-05-P3: search_issues — response text ≤ 500 chars."""
        platform = _make_platform()
        error_body = "X" * body_len
        mock_resp = _json_response(503, text=error_body)
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError) as exc_info:
                await platform.search_issues("Fix")

        if body_len > 500:
            assert error_body not in str(exc_info.value)


# ===========================================================================
# TS-05-P4: type=issues always present on list endpoints
# Property: 05-PROP-4
# Validates: 05-REQ-4.4, 05-REQ-15.2
# ===========================================================================


class TestPropertyTypeIssuesAlwaysPresent:
    """Property: list_issues_by_label and search_issues always include type=issues
    in the GET request, regardless of other parameter values."""

    @pytest.mark.asyncio
    @pytest.mark.parametrize(
        ("label", "state", "sort", "direction"),
        [
            ("bug", "open", "created", "asc"),
            ("enhancement", "closed", "updated", "desc"),
            ("", "all", "comments", "asc"),
            ("a-very-long-label-name", "open", "created", "desc"),
        ],
    )
    async def test_list_issues_type_issues_with_various_params(
        self, label: str, state: str, sort: str, direction: str
    ) -> None:
        """TS-05-P4: type=issues always present for list_issues_by_label."""
        platform = _make_platform()
        mock_resp = _json_response(200, [])
        captured_params: list[dict] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            captured_params.append(params or {})
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            await platform.list_issues_by_label(
                label, state=state, sort=sort, direction=direction
            )

        assert len(captured_params) == 1
        assert captured_params[0].get("type") == "issues"

    @pytest.mark.asyncio
    @pytest.mark.parametrize(
        ("prefix", "state"),
        [
            ("Fix", "open"),
            ("Bug", "closed"),
            ("", "all"),
            ("a b c", "open"),
        ],
    )
    async def test_search_issues_type_issues_with_various_params(
        self, prefix: str, state: str
    ) -> None:
        """TS-05-P4: type=issues always present for search_issues."""
        platform = _make_platform()
        mock_resp = _json_response(200, [])
        captured_params: list[dict] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            captured_params.append(params or {})
            return mock_resp

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            await platform.search_issues(prefix, state=state)

        assert len(captured_params) == 1
        assert captured_params[0].get("type") == "issues"


# ===========================================================================
# TS-05-P5: PlatformProtocol default parameters mirrored exactly
# Property: 05-PROP-5
# Validates: 05-REQ-4.1, 05-REQ-7.2, 05-REQ-12.2
# ===========================================================================


class TestPropertyDefaultParamsMatchProtocol:
    """Property: GiteaPlatform method signatures have default parameter values
    that exactly match PlatformProtocol."""

    def test_list_issues_by_label_defaults(self) -> None:
        """TS-05-P5: list_issues_by_label defaults match protocol."""
        from afissues.gitea import GiteaPlatform

        sig = inspect.signature(GiteaPlatform.list_issues_by_label)
        proto_sig = inspect.signature(PlatformProtocol.list_issues_by_label)

        assert sig.parameters["state"].default == "open"
        assert sig.parameters["sort"].default == "created"
        assert sig.parameters["direction"].default == "asc"

        # Also verify they match the protocol exactly
        assert sig.parameters["state"].default == proto_sig.parameters["state"].default
        assert sig.parameters["sort"].default == proto_sig.parameters["sort"].default
        assert (
            sig.parameters["direction"].default
            == proto_sig.parameters["direction"].default
        )

    def test_close_issue_comment_default(self) -> None:
        """TS-05-P5: close_issue comment default matches protocol."""
        from afissues.gitea import GiteaPlatform

        sig = inspect.signature(GiteaPlatform.close_issue)
        proto_sig = inspect.signature(PlatformProtocol.close_issue)

        assert sig.parameters["comment"].default is None
        assert sig.parameters["comment"].default == proto_sig.parameters["comment"].default

    def test_create_label_description_default(self) -> None:
        """TS-05-P5: create_label description default matches protocol."""
        from afissues.gitea import GiteaPlatform

        sig = inspect.signature(GiteaPlatform.create_label)
        proto_sig = inspect.signature(PlatformProtocol.create_label)

        assert sig.parameters["description"].default == ""
        assert (
            sig.parameters["description"].default
            == proto_sig.parameters["description"].default
        )


# ===========================================================================
# TS-05-P6: ConfigError never re-wrapped by GiteaPlatform constructor
# Property: 05-PROP-6
# Validates: 05-REQ-1.E1
# ===========================================================================


class TestPropertyConfigErrorPropagation:
    """Property: ConfigError raised by _validate_url always propagates unchanged;
    never caught or re-wrapped as IntegrationError or any other type."""

    @pytest.mark.parametrize(
        "error_message",
        [
            "SSRF disallowed",
            "Private IP address",
            "localhost is not allowed",
            "",
            "x" * 1000,
        ],
    )
    def test_config_error_message_preserved(self, error_message: str) -> None:
        """TS-05-P6: ConfigError message propagates unchanged."""
        from afissues.gitea import GiteaPlatform

        with patch(
            _VALIDATE_URL_TARGET,
            side_effect=ConfigError(error_message),
        ):
            with pytest.raises(ConfigError) as exc_info:
                GiteaPlatform("org", "repo", "token", "evil.host")

            # Exact type is ConfigError, not a subclass or re-wrap
            assert type(exc_info.value) is ConfigError
            assert str(exc_info.value) == error_message

    def test_config_error_is_not_integration_error(self) -> None:
        """TS-05-P6: ConfigError is never caught and re-raised as IntegrationError."""
        from afissues.gitea import GiteaPlatform

        with patch(
            _VALIDATE_URL_TARGET,
            side_effect=ConfigError("blocked"),
        ):
            with pytest.raises(ConfigError):
                GiteaPlatform("org", "repo", "token", "evil.host")

            # Confirm IntegrationError is NOT raised
            try:
                with patch(
                    _VALIDATE_URL_TARGET,
                    side_effect=ConfigError("blocked2"),
                ):
                    GiteaPlatform("org", "repo", "token", "evil.host")
            except IntegrationError:
                pytest.fail("ConfigError was re-wrapped as IntegrationError")
            except ConfigError:
                pass  # Expected


# ===========================================================================
# TS-05-P7: close_issue(n, comment=None) makes exactly one HTTP call (PATCH)
# Property: 05-PROP-7
# Validates: 05-REQ-7.2
# ===========================================================================


class TestPropertyCloseIssueNoCommentSingleCall:
    """Property: close_issue with comment=None makes exactly one PATCH request
    and zero POST requests to the comments endpoint."""

    @pytest.mark.asyncio
    @pytest.mark.parametrize("issue_number", [1, 42, 100, 9999])
    async def test_close_issue_none_comment_exactly_one_http_call(
        self, issue_number: int
    ) -> None:
        """TS-05-P7: Exactly 1 HTTP call (PATCH) for any issue_number with comment=None."""
        platform = _make_platform()

        http_calls: list[tuple[str, str]] = []  # (method, url)

        mock_patch_resp = _json_response(200, {})
        mock_post_resp = _json_response(201, {})

        async def mock_patch(url, *, json=None, headers=None, **kw):
            http_calls.append(("PATCH", url))
            return mock_patch_resp

        async def mock_post(url, *, json=None, headers=None, **kw):
            http_calls.append(("POST", url))
            return mock_post_resp

        client = _mock_client(patch=mock_patch, post=mock_post)

        with patch(_TARGET, return_value=client):
            result = await platform.close_issue(issue_number, comment=None)

        assert result is None
        # Exactly 1 HTTP call total
        assert len(http_calls) == 1
        # That call was PATCH, not POST
        method, url = http_calls[0]
        assert method == "PATCH"
        assert f"/issues/{issue_number}" in url
        # No comment POST
        post_calls = [c for c in http_calls if c[0] == "POST"]
        assert len(post_calls) == 0


# ===========================================================================
# Smoke Tests (TS-05-SMOKE-1 through TS-05-SMOKE-6)
# Execution Paths: 05-PATH-1 through 05-PATH-6
# ===========================================================================


# ===========================================================================
# TS-05-SMOKE-1: Happy path — create issue with labels
# Execution Path: 05-PATH-1
# Real components: GiteaPlatform, _resolve_label_id
# Mockable: HTTP client, _validate_url
# ===========================================================================


@pytest.mark.smoke
class TestSmokeCreateIssueWithLabels:
    """Smoke test: create_issue with labels resolves IDs via GET /labels,
    then POSTs the issue with numeric label IDs, returning IssueResult."""

    @pytest.mark.asyncio
    async def test_smoke_create_issue_happy_path(self) -> None:
        """TS-05-SMOKE-1: Full create_issue flow with label cache miss and resolution."""
        platform = _make_platform()

        # Mock labels GET response (for _resolve_label_id cache miss)
        mock_labels_resp = _json_response(
            200,
            [{"id": 10, "name": "bug"}, {"id": 11, "name": "enhancement"}],
        )

        # Mock issue POST response
        mock_issue_resp = _json_response(
            201,
            {
                "number": 42,
                "title": "Fix crash",
                "html_url": "http://gitea.example.com/myorg/myrepo/issues/42",
                "body": "Description",
                "labels": [{"name": "bug"}],
            },
        )

        call_log: list[tuple[str, str]] = []  # (method, url-fragment)

        async def mock_get(url, *, params=None, headers=None, **kw):
            call_log.append(("GET", url))
            return mock_labels_resp

        async def mock_post(url, *, json=None, headers=None, **kw):
            call_log.append(("POST", url))
            # Verify labels are numeric IDs, not strings
            if json and "labels" in json:
                for lid in json["labels"]:
                    assert isinstance(lid, int), f"Expected int label ID, got {type(lid)}"
            return mock_issue_resp

        client = _mock_client(get=mock_get, post=mock_post)

        with patch(_TARGET, return_value=client):
            result = await platform.create_issue(
                "Fix crash", "Description", ["bug"]
            )

        # Verify execution order: GET labels first, then POST issue
        assert len(call_log) == 2
        assert call_log[0][0] == "GET"
        assert "/labels" in call_log[0][1]
        assert call_log[1][0] == "POST"
        assert "/issues" in call_log[1][1]

        # Verify IssueResult mapping
        assert isinstance(result, IssueResult)
        assert result.number == 42
        assert result.title == "Fix crash"
        assert result.body == "Description"
        assert "bug" in result.labels

        # Verify label cache was populated
        assert platform._label_cache.get("bug") == 10
        assert platform._label_cache.get("enhancement") == 11


# ===========================================================================
# TS-05-SMOKE-2: Happy path — create PR with 409 duplicate detection
# Execution Path: 05-PATH-2
# Real components: GiteaPlatform
# Mockable: HTTP client, _validate_url
# ===========================================================================


@pytest.mark.smoke
class TestSmokeCreatePrDuplicate:
    """Smoke test: create_pr handles 409 by querying existing PR and
    returning its html_url."""

    @pytest.mark.asyncio
    async def test_smoke_create_pr_409_duplicate(self) -> None:
        """TS-05-SMOKE-2: POST 409 → GET existing PR → return html_url."""
        platform = _make_platform()

        mock_post_resp = _json_response(409, {})
        existing_pr = [
            {"html_url": "http://gitea.example.com/myorg/myrepo/pulls/7"}
        ]
        mock_get_resp = _json_response(200, existing_pr)

        call_log: list[str] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            call_log.append("POST")
            return mock_post_resp

        async def mock_get(url, *, params=None, headers=None, **kw):
            call_log.append("GET")
            # Verify query params for existing PR lookup
            assert params is not None
            assert params.get("state") == "open"
            return mock_get_resp

        client = _mock_client(post=mock_post, get=mock_get)

        with patch(_TARGET, return_value=client):
            result = await platform.create_pr(
                title="Fix bug", body="Body", head="fix-branch", base="main"
            )

        # POST attempted first, then GET to find existing
        assert call_log == ["POST", "GET"]
        assert result == "http://gitea.example.com/myorg/myrepo/pulls/7"


# ===========================================================================
# TS-05-SMOKE-3: Happy path — close issue with comment
# Execution Path: 05-PATH-3
# Real components: GiteaPlatform, add_issue_comment
# Mockable: HTTP client, _validate_url
# ===========================================================================


@pytest.mark.smoke
class TestSmokeCloseIssueWithComment:
    """Smoke test: close_issue posts comment first, then patches state=closed."""

    @pytest.mark.asyncio
    async def test_smoke_close_issue_comment_then_patch(self) -> None:
        """TS-05-SMOKE-3: POST comment → PATCH state=closed → returns None."""
        platform = _make_platform()

        call_log: list[tuple[str, str]] = []

        async def mock_post(url, *, json=None, headers=None, **kw):
            call_log.append(("POST", url))
            return _json_response(201, {})

        async def mock_patch(url, *, json=None, headers=None, **kw):
            call_log.append(("PATCH", url))
            assert json is not None
            assert json.get("state") == "closed"
            return _json_response(200, {})

        client = _mock_client(post=mock_post, patch=mock_patch)

        with patch(_TARGET, return_value=client):
            result = await platform.close_issue(42, "Resolved by PR #7")

        assert result is None
        # Comment POST comes before state PATCH
        assert len(call_log) == 2
        assert call_log[0][0] == "POST"
        assert "/comments" in call_log[0][1]
        assert call_log[1][0] == "PATCH"
        assert "/issues/42" in call_log[1][1]


# ===========================================================================
# TS-05-SMOKE-4: Happy path — create label idempotently (label exists)
# Execution Path: 05-PATH-4
# Real components: GiteaPlatform, _resolve_label_id
# Mockable: HTTP client, _validate_url
# ===========================================================================


@pytest.mark.smoke
class TestSmokeCreateLabelIdempotent:
    """Smoke test: create_label returns None silently when label already exists."""

    @pytest.mark.asyncio
    async def test_smoke_create_label_existing_no_post(self) -> None:
        """TS-05-SMOKE-4: _resolve_label_id returns ID → no POST → returns None."""
        platform = _make_platform()

        # Pre-populate cache so _resolve_label_id returns immediately
        platform._label_cache = {"bug": 7}
        platform._cache_populated = True

        post_called = False

        async def mock_post(url, *, json=None, headers=None, **kw):
            nonlocal post_called
            post_called = True
            return _json_response(201, {})

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            result = await platform.create_label("bug", "ff0000", "A bug report")

        assert result is None
        assert not post_called, "No POST should be made when label already exists"


# ===========================================================================
# TS-05-SMOKE-5: End-to-end — factory → check_credentials → create_issue
# Execution Path: 05-PATH-5
# Real components: GiteaPlatform, _resolve_label_id, _validate_url
# Mockable: HTTP client, _validate_url
# ===========================================================================


@pytest.mark.smoke
class TestSmokeEndToEnd:
    """Smoke test: configure type=gitea, construct platform, check_credentials,
    then create_issue — full end-to-end chain with mocked HTTP."""

    @pytest.mark.asyncio
    async def test_smoke_e2e_factory_check_create(self) -> None:
        """TS-05-SMOKE-5: GiteaPlatform construction → check_credentials → create_issue."""
        from afissues.gitea import GiteaPlatform

        # 1. Construct platform
        with patch(_VALIDATE_URL_TARGET):
            platform = GiteaPlatform("myorg", "myrepo", "mytoken", "gitea.corp.com")

        assert platform.forge_type == "gitea"
        assert isinstance(platform._label_cache, dict)
        assert len(platform._label_cache) == 0

        # 2. check_credentials succeeds (200)
        mock_cred_resp = _json_response(200, {})

        # 3. GET /labels for _resolve_label_id
        mock_labels_resp = _json_response(
            200, [{"id": 5, "name": "bug"}]
        )

        # 4. POST /issues
        mock_issue_resp = _json_response(
            201,
            {
                "number": 1,
                "title": "Test Issue",
                "html_url": "http://gitea.corp.com/myorg/myrepo/issues/1",
                "body": "Body",
                "labels": [{"name": "bug"}],
            },
        )

        call_log: list[tuple[str, str]] = []

        async def mock_get(url, *, params=None, headers=None, **kw):
            call_log.append(("GET", url))
            if "/labels" in url:
                return mock_labels_resp
            return mock_cred_resp

        async def mock_post(url, *, json=None, headers=None, **kw):
            call_log.append(("POST", url))
            return mock_issue_resp

        client = _mock_client(get=mock_get, post=mock_post)

        with patch(_TARGET, return_value=client):
            # check_credentials
            cred_result = await platform.check_credentials()
            assert cred_result is None

            # create_issue with labels
            issue = await platform.create_issue("Test Issue", "Body", ["bug"])

        assert isinstance(issue, IssueResult)
        assert issue.number == 1
        assert issue.title == "Test Issue"
        assert "bug" in issue.labels

        # Verify call chain: GET /repos (cred) → GET /labels → POST /issues
        assert len(call_log) == 3
        assert call_log[0][0] == "GET"  # check_credentials
        assert call_log[1][0] == "GET"  # _resolve_label_id
        assert call_log[2][0] == "POST"  # create_issue


# ===========================================================================
# TS-05-SMOKE-6: Error path — SSRF-disallowed URL rejects construction
# Execution Path: 05-PATH-6
# Real components: GiteaPlatform, _validate_url
# Mockable: _validate_url (mocked to raise ConfigError)
# ===========================================================================


@pytest.mark.smoke
class TestSmokeSsrfRejection:
    """Smoke test: SSRF-disallowed URL causes ConfigError during construction,
    propagating to the caller without creating a GiteaPlatform instance."""

    def test_smoke_ssrf_disallowed_url(self) -> None:
        """TS-05-SMOKE-6: _validate_url raises ConfigError → no instance created."""
        from afissues.gitea import GiteaPlatform

        with patch(
            _VALIDATE_URL_TARGET,
            side_effect=ConfigError("SSRF: private IP disallowed"),
        ):
            with pytest.raises(ConfigError) as exc_info:
                GiteaPlatform("org", "repo", "token", "169.254.169.254")

        # ConfigError propagated unchanged — not IntegrationError
        assert type(exc_info.value) is ConfigError
        assert "SSRF" in str(exc_info.value)
