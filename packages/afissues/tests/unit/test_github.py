"""Tests for afissues.github module (TS-03-10 through TS-03-13, TS-03-E3 to TS-03-E5).

Verifies GitHubPlatform class attributes, parse_github_remote function,
SSRF guard helpers, import independence from agentfox, and edge-case
behaviour.

Requirements: 03-REQ-3.1, 03-REQ-3.2, 03-REQ-3.3, 03-REQ-3.4,
              03-REQ-3.E1, 03-REQ-3.E2, 03-REQ-3.E3

Drift errata:
  - 03-REQ-3.E2 / TS-03-E4: The spec claims IntegrationError(retryable=True)
    is raised after exhausting retries.  The actual code at _request():229
    does ``raise last_exc`` which re-raises the raw httpx transport exception.
    Tests below match the actual verbatim-moved behaviour.
  - 03-REQ-3.3: The module also contains _RETRYABLE_ERRORS, _RETRY_BACKOFF,
    _MAX_ERROR_TEXT, and _truncate_response() which the spec omits but are
    required for GitHubPlatform to function.
"""

from __future__ import annotations

import inspect
from pathlib import Path
from unittest.mock import AsyncMock, patch

import httpx
import pytest

# Path to the source file for static-analysis tests.
_GITHUB_SRC = Path(__file__).resolve().parents[2] / "afissues" / "github.py"


# ── TS-03-10: GitHubPlatform has 14 async methods and forge_type ───


class TestGitHubPlatformStructure:
    """TS-03-10: GitHubPlatform class attributes and async methods."""

    def test_forge_type_is_github(self) -> None:
        from afissues.github import GitHubPlatform

        assert GitHubPlatform.forge_type == "github"

    def test_has_14_async_methods(self) -> None:
        from afissues.github import GitHubPlatform

        async_methods = [
            name
            for name, fn in inspect.getmembers(GitHubPlatform, predicate=inspect.isfunction)
            if inspect.iscoroutinefunction(fn) and not name.startswith("_")
        ]
        assert len(async_methods) == 14, (
            f"Expected 14 public async methods, got {len(async_methods)}: {sorted(async_methods)}"
        )

    def test_uses_httpx_async_client(self) -> None:
        from afissues.github import GitHubPlatform

        source = inspect.getsource(GitHubPlatform)
        assert "AsyncClient" in source or "httpx" in source


# ── TS-03-11: parse_github_remote ───────────────────────────────────


class TestParseGithubRemote:
    """TS-03-11: Signature and return values for parse_github_remote."""

    def test_https_url(self) -> None:
        from afissues.github import parse_github_remote

        result = parse_github_remote("https://github.com/owner/repo.git")
        assert result == ("owner", "repo")

    def test_ssh_url(self) -> None:
        from afissues.github import parse_github_remote

        result = parse_github_remote("git@github.com:owner/repo.git")
        assert result == ("owner", "repo")

    def test_https_without_dot_git(self) -> None:
        from afissues.github import parse_github_remote

        result = parse_github_remote("https://github.com/owner/repo")
        assert result == ("owner", "repo")

    def test_non_github_returns_none(self) -> None:
        from afissues.github import parse_github_remote

        assert parse_github_remote("https://gitlab.com/owner/repo") is None

    def test_malformed_url_returns_none(self) -> None:
        from afissues.github import parse_github_remote

        assert parse_github_remote("not-a-url") is None

    def test_empty_string_returns_none(self) -> None:
        from afissues.github import parse_github_remote

        assert parse_github_remote("") is None


# ── TS-03-12: SSRF guard helpers and constants ─────────────────────


class TestSSRFGuardHelpers:
    """TS-03-12: Module-level constants and helpers exist with correct types."""

    def test_ssrf_guard_transport_subclass(self) -> None:
        from afissues.github import _SSRFGuardTransport

        assert issubclass(_SSRFGuardTransport, httpx.AsyncHTTPTransport)

    def test_max_retries_is_3(self) -> None:
        from afissues.github import _MAX_RETRIES

        assert _MAX_RETRIES == 3

    def test_github_timeout_is_httpx_timeout(self) -> None:
        from afissues.github import _GITHUB_TIMEOUT

        assert isinstance(_GITHUB_TIMEOUT, httpx.Timeout)

    def test_validate_github_url_callable(self) -> None:
        from afissues.github import _validate_github_url

        assert callable(_validate_github_url)

    def test_validate_transport_address_callable(self) -> None:
        from afissues.github import _validate_transport_address

        assert callable(_validate_transport_address)

    def test_check_address_callable(self) -> None:
        from afissues.github import _check_address

        assert callable(_check_address)

    def test_retryable_errors_is_tuple(self) -> None:
        """Drift: _RETRYABLE_ERRORS is not in the spec but is required."""
        from afissues.github import _RETRYABLE_ERRORS

        assert isinstance(_RETRYABLE_ERRORS, tuple)

    def test_truncate_response_callable(self) -> None:
        """Drift: _truncate_response is not in the spec but is required."""
        from afissues.github import _truncate_response

        assert callable(_truncate_response)


# ── TS-03-13: No agentfox imports in github.py ─────────────────────


class TestGitHubImportIndependence:
    """TS-03-13: afissues/github.py has zero agentfox references."""

    def test_no_agentfox_in_source(self) -> None:
        source = _GITHUB_SRC.read_text()
        assert "agentfox" not in source, "github.py must not import from agentfox"

    def test_imports_from_afissues_errors(self) -> None:
        source = _GITHUB_SRC.read_text()
        assert "from afissues.errors import" in source

    def test_imports_config_error(self) -> None:
        source = _GITHUB_SRC.read_text()
        assert "ConfigError" in source

    def test_imports_integration_error(self) -> None:
        source = _GITHUB_SRC.read_text()
        assert "IntegrationError" in source

    def test_imports_from_afissues_protocol(self) -> None:
        source = _GITHUB_SRC.read_text()
        assert "afissues.protocol" in source


# ── TS-03-E3: _validate_github_url rejects private IPs ─────────────


class TestValidateGithubUrlSSRF:
    """TS-03-E3: ConfigError raised for private/internal network addresses.

    The function accepts a hostname or bare IP (not a full URL like
    ``http://192.168.1.1/api``).  Tests use bare IPs to match the actual
    calling convention (``GitHubPlatform.__init__`` passes ``self._url``
    which is a hostname like ``'github.com'``).
    """

    def test_rejects_private_ipv4(self) -> None:
        from afissues.errors import ConfigError
        from afissues.github import _validate_github_url

        with pytest.raises(ConfigError):
            _validate_github_url("192.168.1.1")

    def test_rejects_class_a_private(self) -> None:
        from afissues.errors import ConfigError
        from afissues.github import _validate_github_url

        with pytest.raises(ConfigError):
            _validate_github_url("10.0.0.1")

    def test_rejects_loopback(self) -> None:
        from afissues.errors import ConfigError
        from afissues.github import _validate_github_url

        with pytest.raises(ConfigError):
            _validate_github_url("127.0.0.1")

    def test_rejects_link_local_ipv4(self) -> None:
        from afissues.errors import ConfigError
        from afissues.github import _validate_github_url

        with pytest.raises(ConfigError):
            _validate_github_url("169.254.1.1")

    def test_error_message_is_descriptive(self) -> None:
        from afissues.errors import ConfigError
        from afissues.github import _validate_github_url

        with pytest.raises(ConfigError, match="restricted"):
            _validate_github_url("10.0.0.1")

    def test_config_error_is_afissues_error(self) -> None:
        from afissues.errors import AfIssuesError
        from afissues.github import _validate_github_url

        with pytest.raises(AfIssuesError):
            _validate_github_url("192.168.0.1")


# ── TS-03-E4: Retry exhaustion re-raises raw httpx exception ───────


class _MockClient:
    """Async-context-manager mock that raises on every HTTP method call."""

    def __init__(self, exc_type: type[Exception], message: str = "mock error") -> None:
        self._exc_type = exc_type
        self._message = message
        self.call_count = 0

    async def __aenter__(self):
        return self

    async def __aexit__(self, *args):
        pass

    def __getattr__(self, name: str):
        async def _raise(*args, **kwargs):
            self.call_count += 1
            raise self._exc_type(self._message)

        return _raise


class TestRetryBehaviour:
    """TS-03-E4: After _MAX_RETRIES, the raw httpx exception is re-raised.

    Drift: The spec (03-REQ-3.E2) claims IntegrationError(retryable=True)
    is raised.  The actual code does ``raise last_exc`` which propagates the
    raw httpx transport exception.  Tests here match the verbatim-moved code.
    """

    async def test_retries_exactly_max_retries_times(self) -> None:
        from afissues.github import _MAX_RETRIES, GitHubPlatform

        mock_client = _MockClient(httpx.ConnectTimeout, "timeout")

        with (
            patch("afissues._http.httpx.AsyncClient", return_value=mock_client),
            patch("afissues._http.asyncio.sleep", new_callable=AsyncMock),
            patch("afissues.github._validate_github_url"),
        ):
            platform = GitHubPlatform(owner="o", repo="r", token="test")
            with pytest.raises(httpx.ConnectTimeout):
                await platform.get_issue(1)
            assert mock_client.call_count == _MAX_RETRIES

    async def test_raises_raw_httpx_exception_not_integration_error(self) -> None:
        """Drift: raw httpx exception propagates, NOT IntegrationError."""
        from afissues.github import GitHubPlatform

        mock_client = _MockClient(httpx.ConnectTimeout, "timeout")

        with (
            patch("afissues._http.httpx.AsyncClient", return_value=mock_client),
            patch("afissues._http.asyncio.sleep", new_callable=AsyncMock),
            patch("afissues.github._validate_github_url"),
        ):
            platform = GitHubPlatform(owner="o", repo="r", token="test")
            # Must be the raw httpx exception, not IntegrationError
            with pytest.raises(httpx.ConnectTimeout):
                await platform.get_issue(1)

    async def test_retry_with_connect_error(self) -> None:
        from afissues.github import _MAX_RETRIES, GitHubPlatform

        mock_client = _MockClient(httpx.ConnectError, "connection refused")

        with (
            patch("afissues._http.httpx.AsyncClient", return_value=mock_client),
            patch("afissues._http.asyncio.sleep", new_callable=AsyncMock),
            patch("afissues.github._validate_github_url"),
        ):
            platform = GitHubPlatform(owner="o", repo="r", token="test")
            with pytest.raises(httpx.ConnectError):
                await platform.get_issue(1)
            assert mock_client.call_count == _MAX_RETRIES

    async def test_retry_with_read_timeout(self) -> None:
        from afissues.github import _MAX_RETRIES, GitHubPlatform

        mock_client = _MockClient(httpx.ReadTimeout, "read timeout")

        with (
            patch("afissues._http.httpx.AsyncClient", return_value=mock_client),
            patch("afissues._http.asyncio.sleep", new_callable=AsyncMock),
            patch("afissues.github._validate_github_url"),
        ):
            platform = GitHubPlatform(owner="o", repo="r", token="test")
            with pytest.raises(httpx.ReadTimeout):
                await platform.get_issue(1)
            assert mock_client.call_count == _MAX_RETRIES


# ── TS-03-E5: GitHubPlatform.close() is a no-op ───────────────────


class TestGitHubPlatformClose:
    """TS-03-E5: close() returns None without raising."""

    async def test_close_returns_none(self) -> None:
        from afissues.github import GitHubPlatform

        with patch("afissues.github._validate_github_url"):
            platform = GitHubPlatform(owner="o", repo="r", token="test")
            result = await platform.close()
            assert result is None

    async def test_close_no_exception(self) -> None:
        from afissues.github import GitHubPlatform

        with patch("afissues.github._validate_github_url"):
            platform = GitHubPlatform(owner="o", repo="r", token="test")
            try:
                await platform.close()
            except Exception as e:
                pytest.fail(f"close() raised unexpected exception: {type(e).__name__}: {e}")
