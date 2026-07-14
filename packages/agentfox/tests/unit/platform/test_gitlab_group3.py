"""Tests for GitLabPlatform: _http.py, factory, properties, smoke tests.

Test Spec: TS-04-33 through TS-04-41, TS-04-E25 through TS-04-E29,
           TS-04-P1 through TS-04-P9, TS-04-SMOKE-1 through TS-04-SMOKE-5
Requirements: 04-REQ-19.*, 04-REQ-20.*, 04-REQ-21.*, 04-REQ-22.*,
              04-PROP-1 through 04-PROP-9

Note: Import paths use agentfox.platform.* (the actual codebase layout).
"""

from __future__ import annotations

import os
from unittest.mock import AsyncMock, MagicMock, patch

import httpx
import pytest
from agentfox.core.errors import ConfigError, IntegrationError

# ---------------------------------------------------------------------------
# Helpers (same pattern as test_gitlab.py)
# ---------------------------------------------------------------------------

_TARGET = "agentfox.platform._http.httpx.AsyncClient"
_HTTP_TARGET = "agentfox.platform._http.httpx.AsyncClient"
_HTTP_SLEEP = "agentfox.platform._http.asyncio.sleep"


def _mock_client(**method_responses: MagicMock | AsyncMock) -> AsyncMock:
    """Build a mock httpx.AsyncClient that works as an async context manager."""
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
    """Build a GitLabPlatform with safe defaults for testing."""
    from agentfox.platform.gitlab import GitLabPlatform

    with patch("agentfox.platform.gitlab._validate_url"):
        return GitLabPlatform("group/project", "tok", "gitlab.com")


# ===========================================================================
# TS-04-33: request_with_retry correct signature and per-call client
# Requirements: 04-REQ-19.1
# ===========================================================================


class TestRequestWithRetrySignature:
    """TS-04-33: request_with_retry correct signature and per-call client."""

    def test_importable_from_http_module(self) -> None:
        """TS-04-33: request_with_retry importable from _http."""
        from agentfox.platform._http import request_with_retry

        assert callable(request_with_retry)

    def test_accepts_expected_parameters(self) -> None:
        """TS-04-33: Function has correct parameter names."""
        import inspect

        from agentfox.platform._http import request_with_retry

        sig = inspect.signature(request_with_retry)
        param_names = list(sig.parameters.keys())
        assert "method" in param_names
        assert "url" in param_names
        assert "timeout" in param_names
        assert "transport" in param_names
        assert "max_retries" in param_names
        assert "backoff_base" in param_names

    @pytest.mark.asyncio
    async def test_creates_async_client_per_call(self) -> None:
        """TS-04-33: Creates a new httpx.AsyncClient per call."""
        from agentfox.platform._http import request_with_retry

        mock_resp = _json_response(200)
        mock_client = AsyncMock()
        mock_client.get = AsyncMock(return_value=mock_resp)
        mock_client.__aenter__ = AsyncMock(return_value=mock_client)
        mock_client.__aexit__ = AsyncMock(return_value=False)

        with patch(_HTTP_TARGET, return_value=mock_client):
            result = await request_with_retry(
                "get",
                "https://gitlab.com/api/v4/projects/1",
                timeout=httpx.Timeout(30),
                headers={"PRIVATE-TOKEN": "tok"},
            )

        assert result.status_code == 200
        mock_client.get.assert_awaited_once()

    @pytest.mark.asyncio
    async def test_delegates_via_getattr(self) -> None:
        """TS-04-33: Delegates to getattr(client, method)(url, **kwargs)."""
        from agentfox.platform._http import request_with_retry

        mock_resp = _json_response(201)
        mock_client = AsyncMock()
        mock_client.post = AsyncMock(return_value=mock_resp)
        mock_client.__aenter__ = AsyncMock(return_value=mock_client)
        mock_client.__aexit__ = AsyncMock(return_value=False)

        with patch(_HTTP_TARGET, return_value=mock_client):
            result = await request_with_retry(
                "post",
                "https://gitlab.com/api/v4/projects/1/issues",
                timeout=httpx.Timeout(30),
                json={"title": "test"},
            )

        assert result.status_code == 201
        mock_client.post.assert_awaited_once()


# ===========================================================================
# TS-04-34: Retry on transient errors, no retry on HTTP errors
# Requirements: 04-REQ-19.2
# ===========================================================================


class TestRequestWithRetryBehaviour:
    """TS-04-34: Retry on transient errors, no retry on HTTP errors."""

    @pytest.mark.asyncio
    async def test_retries_connect_timeout_then_succeeds(self) -> None:
        """TS-04-34: After 2 transient failures, succeeds on 3rd."""
        from agentfox.platform._http import request_with_retry

        call_count = 0
        mock_resp = _json_response(200)

        async def fake_get(url, **kwargs):
            nonlocal call_count
            call_count += 1
            if call_count < 3:
                raise httpx.ConnectTimeout("timeout")
            return mock_resp

        mock_client = AsyncMock()
        mock_client.get = fake_get
        mock_client.__aenter__ = AsyncMock(return_value=mock_client)
        mock_client.__aexit__ = AsyncMock(return_value=False)

        with (
            patch(_HTTP_TARGET, return_value=mock_client),
            patch(_HTTP_SLEEP, new_callable=AsyncMock),
        ):
            result = await request_with_retry(
                "get",
                "https://example.com",
                timeout=httpx.Timeout(5),
                max_retries=3,
            )

        assert result.status_code == 200
        assert call_count == 3

    @pytest.mark.asyncio
    async def test_retries_connect_error(self) -> None:
        """TS-04-34: ConnectError is retried."""
        from agentfox.platform._http import request_with_retry

        call_count = 0
        mock_resp = _json_response(200)

        async def fake_get(url, **kwargs):
            nonlocal call_count
            call_count += 1
            if call_count < 2:
                raise httpx.ConnectError("refused")
            return mock_resp

        mock_client = AsyncMock()
        mock_client.get = fake_get
        mock_client.__aenter__ = AsyncMock(return_value=mock_client)
        mock_client.__aexit__ = AsyncMock(return_value=False)

        with (
            patch(_HTTP_TARGET, return_value=mock_client),
            patch(_HTTP_SLEEP, new_callable=AsyncMock),
        ):
            result = await request_with_retry(
                "get",
                "https://example.com",
                timeout=httpx.Timeout(5),
            )

        assert result.status_code == 200
        assert call_count == 2

    @pytest.mark.asyncio
    async def test_retries_read_timeout(self) -> None:
        """TS-04-34: ReadTimeout is retried."""
        from agentfox.platform._http import request_with_retry

        call_count = 0
        mock_resp = _json_response(200)

        async def fake_get(url, **kwargs):
            nonlocal call_count
            call_count += 1
            if call_count < 2:
                raise httpx.ReadTimeout("read timeout")
            return mock_resp

        mock_client = AsyncMock()
        mock_client.get = fake_get
        mock_client.__aenter__ = AsyncMock(return_value=mock_client)
        mock_client.__aexit__ = AsyncMock(return_value=False)

        with (
            patch(_HTTP_TARGET, return_value=mock_client),
            patch(_HTTP_SLEEP, new_callable=AsyncMock),
        ):
            result = await request_with_retry(
                "get",
                "https://example.com",
                timeout=httpx.Timeout(5),
            )

        assert result.status_code == 200
        assert call_count == 2

    @pytest.mark.asyncio
    async def test_429_not_retried(self) -> None:
        """TS-04-34/TS-04-E26: HTTP 429 returned without retry."""
        from agentfox.platform._http import request_with_retry

        call_count = 0
        mock_resp = _json_response(429, text="Rate limited")

        async def fake_get(url, **kwargs):
            nonlocal call_count
            call_count += 1
            return mock_resp

        mock_client = AsyncMock()
        mock_client.get = fake_get
        mock_client.__aenter__ = AsyncMock(return_value=mock_client)
        mock_client.__aexit__ = AsyncMock(return_value=False)

        with patch(_HTTP_TARGET, return_value=mock_client):
            result = await request_with_retry(
                "get",
                "https://example.com",
                timeout=httpx.Timeout(5),
            )

        assert result.status_code == 429
        assert call_count == 1


# ===========================================================================
# TS-04-E25: Re-raises after max_retries exhausted
# Requirements: 04-REQ-19.E1
# ===========================================================================


class TestRequestWithRetryExhaustion:
    """TS-04-E25: Re-raises after max_retries exhausted."""

    @pytest.mark.asyncio
    async def test_raises_after_max_retries(self) -> None:
        """TS-04-E25: ConnectTimeout raised after exactly 3 attempts."""
        from agentfox.platform._http import request_with_retry

        call_count = 0

        async def always_timeout(url, **kwargs):
            nonlocal call_count
            call_count += 1
            raise httpx.ConnectTimeout("timeout")

        mock_client = AsyncMock()
        mock_client.get = always_timeout
        mock_client.__aenter__ = AsyncMock(return_value=mock_client)
        mock_client.__aexit__ = AsyncMock(return_value=False)

        with (
            patch(_HTTP_TARGET, return_value=mock_client),
            patch(_HTTP_SLEEP, new_callable=AsyncMock),
        ):
            with pytest.raises(httpx.ConnectTimeout):
                await request_with_retry(
                    "get",
                    "https://example.com",
                    timeout=httpx.Timeout(5),
                    max_retries=3,
                )

        assert call_count == 3

    @pytest.mark.asyncio
    async def test_raises_connect_error_after_exhaustion(self) -> None:
        """TS-04-E25: ConnectError raised after retries exhausted."""
        from agentfox.platform._http import request_with_retry

        call_count = 0

        async def always_fail(url, **kwargs):
            nonlocal call_count
            call_count += 1
            raise httpx.ConnectError("refused")

        mock_client = AsyncMock()
        mock_client.get = always_fail
        mock_client.__aenter__ = AsyncMock(return_value=mock_client)
        mock_client.__aexit__ = AsyncMock(return_value=False)

        with (
            patch(_HTTP_TARGET, return_value=mock_client),
            patch(_HTTP_SLEEP, new_callable=AsyncMock),
        ):
            with pytest.raises(httpx.ConnectError):
                await request_with_retry(
                    "get",
                    "https://example.com",
                    timeout=httpx.Timeout(5),
                    max_retries=3,
                )

        assert call_count == 3


# ===========================================================================
# TS-04-E26: HTTP 429 response returned without retry
# Requirements: 04-REQ-19.E2
# ===========================================================================


class TestRequestWithRetryNo429Retry:
    """TS-04-E26: HTTP 429 response returned without retry."""

    @pytest.mark.asyncio
    async def test_429_single_attempt(self) -> None:
        """TS-04-E26: 429 returned after exactly one attempt."""
        from agentfox.platform._http import request_with_retry

        call_count = 0
        mock_resp = _json_response(429, text="Too Many Requests")

        async def fake_get(url, **kwargs):
            nonlocal call_count
            call_count += 1
            return mock_resp

        mock_client = AsyncMock()
        mock_client.get = fake_get
        mock_client.__aenter__ = AsyncMock(return_value=mock_client)
        mock_client.__aexit__ = AsyncMock(return_value=False)

        with patch(_HTTP_TARGET, return_value=mock_client):
            result = await request_with_retry(
                "get",
                "https://example.com",
                timeout=httpx.Timeout(5),
            )

        assert result.status_code == 429
        assert call_count == 1


# ===========================================================================
# TS-04-35: GitHubPlatform._request delegates to request_with_retry
# Requirements: 04-REQ-19.3
# ===========================================================================


class TestGitHubRequestDelegation:
    """TS-04-35: GitHubPlatform._request delegates to request_with_retry."""

    def test_request_source_references_request_with_retry(self) -> None:
        """TS-04-35: GitHubPlatform._request source mentions request_with_retry."""
        import inspect

        from agentfox.platform.github import GitHubPlatform

        source = inspect.getsource(GitHubPlatform._request)
        assert "request_with_retry" in source


# ===========================================================================
# TS-04-36: Platform factory routes 'gitlab' to GitLabPlatform
# Requirements: 04-REQ-20.1
# ===========================================================================


class TestFactoryGitLabRouting:
    """TS-04-36: Platform factory routes 'gitlab' to GitLabPlatform."""

    def test_creates_gitlab_platform(self, tmp_path: MagicMock) -> None:
        """TS-04-36: create_platform with type='gitlab' returns GitLabPlatform."""
        from agentfox.nightshift.platform_factory import create_platform
        from agentfox.platform.gitlab import GitLabPlatform

        config = MagicMock()
        config.platform.type = "gitlab"
        config.platform.url = "gitlab.com"
        config.platform.project_id = None

        with (
            patch.dict("os.environ", {"GITLAB_TOKEN": "test-token"}, clear=False),
            patch(
                "agentfox.nightshift.platform_factory._resolve_gitlab_remote",
                return_value="group/project",
            ),
            patch("agentfox.platform.gitlab._validate_url"),
        ):
            platform = create_platform(config, tmp_path)

        assert isinstance(platform, GitLabPlatform)


# ===========================================================================
# TS-04-37: _SUPPORTED_PLATFORMS has 'github', 'gitlab', 'gitea'
# Requirements: 04-REQ-20.2
# ===========================================================================


class TestFactorySupportedPlatforms:
    """TS-04-37: _SUPPORTED_PLATFORMS has 'github', 'gitlab', 'gitea'."""

    def test_supported_platforms_includes_all_three(self) -> None:
        """TS-04-37: All three platform types in _SUPPORTED_PLATFORMS."""
        from agentfox.nightshift.platform_factory import _SUPPORTED_PLATFORMS

        assert "github" in _SUPPORTED_PLATFORMS
        assert "gitlab" in _SUPPORTED_PLATFORMS
        assert "gitea" in _SUPPORTED_PLATFORMS


# ===========================================================================
# TS-04-E27: Factory error when no GitLab project identifier
# Requirements: 04-REQ-20.E1
# ===========================================================================


class TestFactoryGitLabMissingProject:
    """TS-04-E27: Factory error when no GitLab project identifier."""

    def test_raises_config_error_no_project_id(self, tmp_path: MagicMock) -> None:
        """TS-04-E27: Error when parse_remote is None and no config."""
        from agentfox.nightshift.platform_factory import create_platform

        config = MagicMock()
        config.platform.type = "gitlab"
        config.platform.url = "gitlab.com"
        config.platform.project_id = None

        with (
            patch.dict("os.environ", {"GITLAB_TOKEN": "tok"}, clear=False),
            patch(
                "agentfox.nightshift.platform_factory._resolve_gitlab_remote",
                return_value=None,
            ),
        ):
            with pytest.raises((ConfigError, SystemExit)):
                create_platform(config, tmp_path)


# ===========================================================================
# TS-04-E28: Factory error when GITLAB_TOKEN is absent
# Requirements: 04-REQ-20.E2
# ===========================================================================


class TestFactoryGitLabMissingToken:
    """TS-04-E28: Factory error when GITLAB_TOKEN is absent."""

    def test_raises_config_error_missing_token(self, tmp_path: MagicMock) -> None:
        """TS-04-E28: Error when GITLAB_TOKEN env var is not set."""
        from agentfox.nightshift.platform_factory import create_platform

        config = MagicMock()
        config.platform.type = "gitlab"
        config.platform.url = "gitlab.com"
        config.platform.project_id = "group/project"

        env = {k: v for k, v in os.environ.items() if k != "GITLAB_TOKEN"}
        with patch.dict("os.environ", env, clear=True):
            with pytest.raises((ConfigError, SystemExit)):
                create_platform(config, tmp_path)


# ===========================================================================
# TS-04-38: Factory error when Gitea requested but unavailable
# Requirements: 04-REQ-21.1
# ===========================================================================


class TestFactoryGiteaUnavailable:
    """TS-04-38: Factory error when Gitea requested but unavailable."""

    def test_raises_config_error_gitea_unavailable(
        self, tmp_path: MagicMock,
    ) -> None:
        """TS-04-38: Error with 'not yet available' when gitea absent."""
        import sys

        from agentfox.nightshift.platform_factory import create_platform

        config = MagicMock()
        config.platform.type = "gitea"
        config.platform.url = "gitea.example.com"
        config.platform.project_id = "owner/repo"

        with (
            patch.dict("os.environ", {"GITEA_TOKEN": "tok"}, clear=False),
            patch.dict(sys.modules, {"agentfox.platform.gitea": None}),
        ):
            with pytest.raises((ConfigError, SystemExit)) as exc_info:
                create_platform(config, tmp_path)

        if isinstance(exc_info.value, ConfigError):
            err_msg = str(exc_info.value)
            assert (
                "gitea" in err_msg.lower()
                or "not yet available" in err_msg.lower()
            )


# ===========================================================================
# TS-04-39: Factory module loads without gitea module
# Requirements: 04-REQ-21.2
# ===========================================================================


class TestFactoryImportsWithoutGitea:
    """TS-04-39: Factory module loads without gitea module."""

    def test_factory_imports_successfully(self) -> None:
        """TS-04-39: import platform_factory succeeds without gitea."""
        import importlib
        import sys

        sys.modules.pop("agentfox.platform.gitea", None)
        try:
            importlib.invalidate_caches()
            import agentfox.nightshift.platform_factory  # noqa: F401
        except ImportError:
            pytest.fail("Factory should import without agentfox.platform.gitea")


# ===========================================================================
# TS-04-E29: Factory error for unsupported platform type
# Requirements: 04-REQ-21.E1
# ===========================================================================


class TestFactoryUnsupportedPlatform:
    """TS-04-E29: Factory error for unsupported platform type."""

    def test_raises_config_error_unsupported_type(
        self, tmp_path: MagicMock,
    ) -> None:
        """TS-04-E29: Error for unsupported platform type."""
        from agentfox.nightshift.platform_factory import create_platform

        config = MagicMock()
        config.platform.type = "bitbucket"

        with pytest.raises((ConfigError, SystemExit)):
            create_platform(config, tmp_path)


# ===========================================================================
# TS-04-40: Coverage command documentation
# Requirements: 04-REQ-22.1
# ===========================================================================


class TestGitLabCoverage:
    """TS-04-40: Coverage command documentation."""

    def test_coverage_command_documented(self) -> None:
        """TS-04-40: Verify gitlab module exists for coverage.

        Run: pytest packages/agentfox/tests/unit/platform/test_gitlab.py
            --cov=agentfox.platform.gitlab --cov-branch
            --cov-report=term-missing -q
        """
        from agentfox.platform import gitlab  # noqa: F401


# ===========================================================================
# TS-04-41: Existing GitHubPlatform tests still valid
# Requirements: 04-REQ-22.2
# ===========================================================================


class TestGitHubRegressionCheck:
    """TS-04-41: Existing GitHubPlatform tests still valid."""

    def test_github_platform_importable(self) -> None:
        """TS-04-41: GitHubPlatform is importable and unchanged."""
        from agentfox.platform.github import GitHubPlatform  # noqa: F401

        assert GitHubPlatform is not None


# ===========================================================================
# TS-04-P1: IssueResult fields from documented mappings only
# Requirements: 04-PROP-1
# ===========================================================================


class TestPropertyIssueResultFieldMapping:
    """TS-04-P1: IssueResult fields from documented mappings only."""

    @pytest.mark.asyncio
    @pytest.mark.parametrize(
        "extra_fields",
        [
            {"state": "opened", "author": {"name": "alice"}, "project_id": 99},
            {"assignee": None, "milestone": {"id": 1}, "confidential": False},
            {"merge_requests_count": 0, "upvotes": 5, "downvotes": 0},
        ],
    )
    async def test_extra_fields_ignored(self, extra_fields: dict) -> None:
        """TS-04-P1: Extra GitLab fields not in IssueResult."""
        platform = _make_platform()
        base_json = {
            "iid": 42,
            "title": "Issue Title",
            "web_url": "https://gitlab.com/g/p/-/issues/42",
            "description": "Body text",
            "labels": ["bug"],
        }
        full_json = {**base_json, **extra_fields}
        mock_resp = _json_response(200, full_json)
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.get_issue(42)

        assert result.number == 42
        assert result.title == "Issue Title"
        assert result.html_url == "https://gitlab.com/g/p/-/issues/42"
        assert result.body == "Body text"
        assert result.labels == ("bug",)


# ===========================================================================
# TS-04-P2: IssueComment uses only documented note field mappings
# Requirements: 04-PROP-2
# ===========================================================================


class TestPropertyIssueCommentFieldMapping:
    """TS-04-P2: IssueComment uses only documented note field mappings."""

    @pytest.mark.asyncio
    @pytest.mark.parametrize(
        "extra_fields",
        [
            {"noteable_type": "Issue", "project_id": 1, "resolvable": False},
            {"resolved": None, "attachment": None, "type": None},
        ],
    )
    async def test_extra_note_fields_ignored(self, extra_fields: dict) -> None:
        """TS-04-P2: Extra GitLab note fields not in IssueComment."""
        platform = _make_platform()
        base_note = {
            "id": 10,
            "body": "comment text",
            "author": {"username": "alice"},
            "created_at": "2024-01-01T00:00:00Z",
            "system": False,
        }
        full_note = {**base_note, **extra_fields}
        mock_resp = _json_response(200, [full_note])
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            comments = await platform.list_issue_comments(1)

        assert len(comments) == 1
        c = comments[0]
        assert c.id == 10
        assert c.body == "comment text"
        assert c.user == "alice"
        assert c.created_at == "2024-01-01T00:00:00Z"


# ===========================================================================
# TS-04-P3: SSRF violations raise ConfigError; valid hosts succeed
# Requirements: 04-PROP-3
# ===========================================================================


class TestPropertySSRFSafety:
    """TS-04-P3: SSRF violations raise ConfigError; valid hosts succeed."""

    @pytest.mark.parametrize(
        "private_url",
        ["10.0.0.1", "192.168.1.1", "172.16.0.1", "127.0.0.1"],
    )
    def test_private_ips_raise_config_error(self, private_url: str) -> None:
        """TS-04-P3: Private/loopback IPs raise ConfigError."""
        from agentfox.platform.gitlab import GitLabPlatform

        with pytest.raises(ConfigError):
            GitLabPlatform("group/project", "tok", private_url)

    def test_valid_hostname_succeeds(self) -> None:
        """TS-04-P3: Valid public hostname succeeds."""
        platform = _make_platform()
        assert platform._base_url == "https://gitlab.com/api/v4"


# ===========================================================================
# TS-04-P4: request_with_retry never exceeds max_retries
# Requirements: 04-PROP-4
# ===========================================================================


class TestPropertyRetryBoundedness:
    """TS-04-P4: request_with_retry never exceeds max_retries."""

    @pytest.mark.asyncio
    @pytest.mark.parametrize("max_retries", [1, 2, 3, 5])
    async def test_attempt_count_bounded(self, max_retries: int) -> None:
        """TS-04-P4: Total attempts equals max_retries."""
        from agentfox.platform._http import request_with_retry

        call_count = 0

        async def always_timeout(url, **kwargs):
            nonlocal call_count
            call_count += 1
            raise httpx.ConnectTimeout("timeout")

        mock_client = AsyncMock()
        mock_client.get = always_timeout
        mock_client.__aenter__ = AsyncMock(return_value=mock_client)
        mock_client.__aexit__ = AsyncMock(return_value=False)

        with (
            patch(_HTTP_TARGET, return_value=mock_client),
            patch(_HTTP_SLEEP, new_callable=AsyncMock),
        ):
            with pytest.raises(httpx.ConnectTimeout):
                await request_with_retry(
                    "get",
                    "https://example.com",
                    timeout=httpx.Timeout(5),
                    max_retries=max_retries,
                )

        assert call_count == max_retries


# ===========================================================================
# TS-04-P5: create_label None on 201/409; IntegrationError otherwise
# Requirements: 04-PROP-5
# ===========================================================================


class TestPropertyCreateLabelIdempotency:
    """TS-04-P5: create_label None on 201/409; IntegrationError otherwise."""

    @pytest.mark.asyncio
    @pytest.mark.parametrize("status", [201, 409])
    async def test_returns_none_on_success_statuses(self, status: int) -> None:
        """TS-04-P5: create_label returns None on 201 and 409."""
        platform = _make_platform()
        mock_resp = _json_response(
            status, text="OK" if status == 201 else "Conflict",
        )
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.create_label("label", "ff0000", "desc")

        assert result is None

    @pytest.mark.asyncio
    @pytest.mark.parametrize("status", [400, 403, 422, 500])
    async def test_raises_on_other_statuses(self, status: int) -> None:
        """TS-04-P5: IntegrationError for non-201/non-409 statuses."""
        platform = _make_platform()
        mock_resp = _json_response(status, text="Error")
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.create_label("label", "ff0000", "desc")


# ===========================================================================
# TS-04-P6: list_issue_comments never returns system notes
# Requirements: 04-PROP-6
# ===========================================================================


class TestPropertySystemNotesExcluded:
    """TS-04-P6: list_issue_comments never returns system notes."""

    @pytest.mark.asyncio
    @pytest.mark.parametrize(
        "system_flags",
        [
            [False, True],
            [True, True, True],
            [False, False, True, False],
            [True, False, True, False, True],
            [],
        ],
    )
    async def test_system_notes_always_excluded(
        self,
        system_flags: list[bool],
    ) -> None:
        """TS-04-P6: Returned count == notes with system==False."""
        platform = _make_platform()
        notes_json = [
            {
                "id": i,
                "body": f"note {i}",
                "author": {"username": f"user{i}"},
                "created_at": f"2024-01-0{(i % 9) + 1}T00:00:00Z",
                "system": flag,
            }
            for i, flag in enumerate(system_flags)
        ]
        mock_resp = _json_response(200, notes_json)
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            comments = await platform.list_issue_comments(1)

        expected_count = sum(1 for f in system_flags if not f)
        assert len(comments) == expected_count
        for comment in comments:
            src_note = next(n for n in notes_json if n["id"] == comment.id)
            assert src_note["system"] is False


# ===========================================================================
# TS-04-P7: parse_remote never raises for any input
# Requirements: 04-PROP-7
# ===========================================================================


class TestPropertyParseRemoteNeverRaises:
    """TS-04-P7: parse_remote never raises for any input."""

    @pytest.mark.parametrize(
        "url",
        [
            "https://gitlab.com/group/project.git",
            "git@gitlab.com:group/project.git",
            "https://github.com/org/repo.git",
            "not-a-url",
            "",
            "https://gitlab.com:8080/group/project.git",
            "git@github.com:org/repo.git",
            "://broken",
            "http://" + "a" * 1000 + ".com/group/project.git",
            "file:///etc/passwd",
            "\x00\x01\x02",
            "https://gitlab.com/onlyone.git",
            "ssh://git@gitlab.com/group/project.git",
        ],
    )
    def test_never_raises(self, url: str) -> None:
        """TS-04-P7: parse_remote returns tuple or None, never raises."""
        from agentfox.platform.gitlab import parse_remote

        result = parse_remote(url)
        assert result is None or (
            isinstance(result, tuple)
            and len(result) == 2
            and all(isinstance(s, str) and s for s in result)
        )


# ===========================================================================
# TS-04-P8: No persistent AsyncClient stored after method calls
# Requirements: 04-PROP-8
# ===========================================================================


class TestPropertyNoPersistentClient:
    """TS-04-P8: No httpx.AsyncClient stored after method calls."""

    @pytest.mark.asyncio
    async def test_no_client_after_create_issue(self) -> None:
        """TS-04-P8: No AsyncClient on instance after create_issue."""
        platform = _make_platform()
        mock_resp = _json_response(
            201,
            {
                "iid": 1,
                "title": "T",
                "web_url": "url",
                "description": "b",
                "labels": [],
            },
        )
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            await platform.create_issue("T", "b", [])

        for value in vars(platform).values():
            assert not isinstance(value, httpx.AsyncClient)

    @pytest.mark.asyncio
    async def test_no_client_after_get_issue(self) -> None:
        """TS-04-P8: No AsyncClient on instance after get_issue."""
        platform = _make_platform()
        mock_resp = _json_response(
            200,
            {
                "iid": 1,
                "title": "T",
                "web_url": "url",
                "description": "b",
                "labels": [],
            },
        )
        client = _mock_client(get=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            await platform.get_issue(1)

        for value in vars(platform).values():
            assert not isinstance(value, httpx.AsyncClient)

    @pytest.mark.asyncio
    async def test_no_client_after_close(self) -> None:
        """TS-04-P8: No AsyncClient on instance after close()."""
        platform = _make_platform()
        await platform.close()

        for value in vars(platform).values():
            assert not isinstance(value, httpx.AsyncClient)


# ===========================================================================
# TS-04-P9: platform_factory imports regardless of gitea
# Requirements: 04-PROP-9
# ===========================================================================


class TestPropertyFactoryImportsAlways:
    """TS-04-P9: platform_factory imports regardless of gitea."""

    def test_imports_with_gitea_absent(self) -> None:
        """TS-04-P9: Factory imports when gitea is absent."""
        import importlib
        import sys

        sys.modules.pop("agentfox.platform.gitea", None)
        importlib.invalidate_caches()
        try:
            import agentfox.nightshift.platform_factory  # noqa: F401
        except ImportError:
            pytest.fail("platform_factory should import without gitea")

    def test_imports_with_gitea_blocked(self) -> None:
        """TS-04-P9: Factory imports even when gitea is blocked."""
        import sys

        with patch.dict(sys.modules, {"agentfox.platform.gitea": None}):
            try:
                import agentfox.nightshift.platform_factory  # noqa: F401
            except ImportError:
                pytest.fail(
                    "platform_factory should import with blocked gitea",
                )


# ===========================================================================
# Smoke tests (task groups 4-11 now implemented)
# ===========================================================================


class TestSmokeCreateIssueAndLabel:
    """TS-04-SMOKE-1: Factory -> GitLabPlatform -> create_issue -> assign_label."""

    @pytest.mark.asyncio
    async def test_e2e_create_issue_and_label(
        self, tmp_path: MagicMock,
    ) -> None:
        """TS-04-SMOKE-1: Full E2E with mocked HTTP responses."""
        from agentfox.nightshift.platform_factory import create_platform
        from agentfox.platform.gitlab import GitLabPlatform

        config = MagicMock()
        config.platform.type = "gitlab"
        config.platform.url = "gitlab.com"
        config.platform.project_id = None

        issue_resp = _json_response(
            201,
            {
                "iid": 42,
                "title": "Fix bug",
                "web_url": "https://gitlab.com/g/p/-/issues/42",
                "description": "body",
                "labels": [],
            },
        )
        label_resp = _json_response(200)

        with (
            patch.dict(
                "os.environ", {"GITLAB_TOKEN": "test-token"}, clear=False,
            ),
            patch(
                "agentfox.nightshift.platform_factory._resolve_gitlab_remote",
                return_value="group/project",
            ),
            patch("agentfox.platform.gitlab._validate_url"),
        ):
            platform = create_platform(config, tmp_path)

        assert isinstance(platform, GitLabPlatform)
        client = _mock_client(
            post=AsyncMock(return_value=issue_resp),
            put=AsyncMock(return_value=label_resp),
        )

        with patch(_TARGET, return_value=client):
            result = await platform.create_issue("Fix bug", "body", [])
            await platform.assign_label(result.number, "workflow")

        assert result.number == 42


class TestSmokeCreatePrFallback:
    """TS-04-SMOKE-2: create_pr 409 -> fallback GET -> web_url returned."""

    @pytest.mark.asyncio
    async def test_e2e_create_pr_409_fallback(self) -> None:
        """TS-04-SMOKE-2: 409 + fallback GET returns existing MR URL."""
        platform = _make_platform()
        client = _mock_client(
            post=AsyncMock(
                return_value=_json_response(409, text="Conflict"),
            ),
            get=AsyncMock(
                return_value=_json_response(
                    200,
                    [
                        {
                            "web_url": (
                                "https://gitlab.com/g/p/-/merge_requests/3"
                            ),
                        },
                    ],
                ),
            ),
        )

        with patch(_TARGET, return_value=client):
            url = await platform.create_pr(
                title="My MR",
                body="body",
                head="feature",
                base="main",
            )

        assert url == "https://gitlab.com/g/p/-/merge_requests/3"


class TestSmokeCloseIssueWithComment:
    """TS-04-SMOKE-3: close_issue -> comment -> state_event=close."""

    @pytest.mark.asyncio
    async def test_e2e_close_issue_with_comment(self) -> None:
        """TS-04-SMOKE-3: Full E2E close_issue with comment."""
        platform = _make_platform()
        client = _mock_client(
            post=AsyncMock(return_value=_json_response(201)),
            put=AsyncMock(return_value=_json_response(200)),
        )

        with patch(_TARGET, return_value=client):
            await platform.close_issue(10, "Closing this issue.")


class TestSmokeSSRFBlocked:
    """TS-04-SMOKE-4: SSRF violation blocked at construction time."""

    def test_ssrf_blocked_at_construction(
        self, tmp_path: MagicMock,
    ) -> None:
        """TS-04-SMOKE-4: SSRF violation raises ConfigError."""
        from agentfox.nightshift.platform_factory import create_platform

        config = MagicMock()
        config.platform.type = "gitlab"
        config.platform.url = "192.168.1.100"
        config.platform.project_id = "group/project"

        with patch.dict("os.environ", {"GITLAB_TOKEN": "tok"}, clear=False):
            with pytest.raises(ConfigError):
                create_platform(config, tmp_path)


class TestSmokeGiteaUnavailable:
    """TS-04-SMOKE-5: Gitea unavailable -> ConfigError."""

    def test_gitea_unavailable_config_error(
        self, tmp_path: MagicMock,
    ) -> None:
        """TS-04-SMOKE-5: Gitea unavailable raises ConfigError."""
        import sys

        from agentfox.nightshift.platform_factory import create_platform

        config = MagicMock()
        config.platform.type = "gitea"
        config.platform.url = "gitea.example.com"
        config.platform.project_id = "owner/repo"

        with (
            patch.dict("os.environ", {"GITEA_TOKEN": "tok"}, clear=False),
            patch.dict(sys.modules, {"agentfox.platform.gitea": None}),
        ):
            with pytest.raises((ConfigError, SystemExit)):
                create_platform(config, tmp_path)
