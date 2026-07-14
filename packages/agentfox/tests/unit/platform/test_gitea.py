"""Tests for GiteaPlatform issue and PR operations.

Test Spec: TS-05-1 through TS-05-9, TS-05-E1 through TS-05-E3,
           TS-05-46 through TS-05-49
Requirements: 05-REQ-1.* through 05-REQ-2.*, 05-REQ-17.*

Note: Import paths use agentfox.platform.* (the actual codebase layout),
not afissues.* (the spec-03 future layout that has not been extracted yet).
The Gitea module will live at agentfox.platform.gitea alongside the
existing agentfox.platform.github module.
"""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from agentfox.core.errors import ConfigError, IntegrationError
from agentfox.platform.protocol import IssueResult  # noqa: F401 (used in later groups)

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
