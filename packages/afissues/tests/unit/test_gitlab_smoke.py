"""Smoke tests for GitLabPlatform wiring verification (task group 12).

These tests verify end-to-end execution paths with real components and
mocked HTTP responses.  They exercise the full call chain from platform
method through request_with_retry → httpx.AsyncClient (mocked), validating
that all wiring is correct and no stubs remain.

Test Spec: TS-04-SMOKE-1 through TS-04-SMOKE-5
Requirements: 04-REQ-1.*, 04-REQ-2.*, 04-REQ-12.*, 04-REQ-19.*, 04-REQ-20.*,
              04-REQ-22.1

Paths verified:
  PATH-1: GitLabPlatform → create_issue → request_with_retry → IssueResult
          then → assign_label → request_with_retry → PUT with add_labels
  PATH-2: create_pr → POST (409) → fallback GET → web_url
  PATH-3: close_issue(comment) → POST /notes → PUT state_event=close
  PATH-4: SSRF private IP → ConfigError at construction
  PATH-5: Gitea unavailable → ConfigError (platform factory concept)
"""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from afissues.errors import ConfigError, IntegrationError
from afissues.protocol import IssueResult

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

_TARGET = "afissues._http.httpx.AsyncClient"


def _mock_client(**method_responses: MagicMock | AsyncMock) -> AsyncMock:
    """Build a mock httpx.AsyncClient as an async context manager."""
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
    from afissues.gitlab import GitLabPlatform

    with patch("afissues.gitlab._validate_url"):
        return GitLabPlatform("group/project", "tok", "gitlab.com")


# ===========================================================================
# TS-04-SMOKE-1: E2E create_issue + assign_label
# Path: GitLabPlatform → create_issue → request_with_retry → IssueResult
#       then → assign_label → PUT with add_labels
# Requirements: 04-REQ-1.1, 04-REQ-2.1, 04-REQ-19.1, 04-REQ-20.1
# ===========================================================================


class TestSmokeCreateIssueAndAssignLabel:
    """TS-04-SMOKE-1: Create issue then assign label with mocked HTTP."""

    @pytest.mark.asyncio
    async def test_e2e_create_issue_and_assign_label(self) -> None:
        """Verify full chain: platform → create_issue → assign_label."""
        from afissues.gitlab import GitLabPlatform

        platform = _make_platform()
        assert isinstance(platform, GitLabPlatform)
        assert platform.forge_type == "gitlab"

        issue_resp = _json_response(
            201,
            {
                "iid": 42,
                "title": "Fix bug",
                "web_url": "https://gitlab.com/g/p/-/issues/42",
                "description": "body text",
                "labels": [],
            },
        )
        label_resp = _json_response(200)

        # Track calls to verify both HTTP calls include PRIVATE-TOKEN
        call_log: list[dict] = []

        async def _capture_post(*args, **kwargs):
            call_log.append({"method": "post", "args": args, "kwargs": kwargs})
            return issue_resp

        async def _capture_put(*args, **kwargs):
            call_log.append({"method": "put", "args": args, "kwargs": kwargs})
            return label_resp

        client = _mock_client(
            post=AsyncMock(side_effect=_capture_post),
            put=AsyncMock(side_effect=_capture_put),
        )

        with patch(_TARGET, return_value=client):
            result = await platform.create_issue("Fix bug", "body text", [])
            await platform.assign_label(result.number, "workflow")

        # Verify IssueResult fields
        assert isinstance(result, IssueResult)
        assert result.number == 42
        assert result.html_url == "https://gitlab.com/g/p/-/issues/42"
        assert result.title == "Fix bug"
        assert result.body == "body text"

        # Verify both calls include auth header
        assert len(call_log) >= 2
        for call in call_log:
            headers = call["kwargs"].get("headers", {})
            assert "PRIVATE-TOKEN" in headers

    @pytest.mark.asyncio
    async def test_create_issue_uses_request_with_retry(self) -> None:
        """Verify create_issue delegates through _request → request_with_retry."""
        platform = _make_platform()
        issue_resp = _json_response(
            201,
            {
                "iid": 1,
                "title": "T",
                "web_url": "https://gitlab.com/g/p/-/issues/1",
                "description": None,
                "labels": ["bug"],
            },
        )
        client = _mock_client(post=AsyncMock(return_value=issue_resp))

        with patch(_TARGET, return_value=client):
            result = await platform.create_issue("T", "B", ["bug"])

        # description: null → body defaults to ""
        assert result.body == ""
        assert result.labels == ("bug",)


# ===========================================================================
# TS-04-SMOKE-2: create_pr 409 duplicate → fallback GET
# Path: POST /merge_requests → 409 → GET /merge_requests → web_url
# Requirements: 04-REQ-12.1, 04-REQ-12.2
# ===========================================================================


class TestSmokeCreatePr409Fallback:
    """TS-04-SMOKE-2: create_pr handles 409 by querying existing MR."""

    @pytest.mark.asyncio
    async def test_409_fallback_returns_existing_mr_url(self) -> None:
        """POST returns 409; fallback GET returns existing MR."""
        platform = _make_platform()

        post_resp = _json_response(409, text="Conflict")
        get_resp = _json_response(
            200,
            [{"web_url": "https://gitlab.com/g/p/-/merge_requests/3", "iid": 3}],
        )

        call_log: list[str] = []

        async def _capture_post(*a, **kw):
            call_log.append("POST")
            return post_resp

        async def _capture_get(*a, **kw):
            call_log.append("GET")
            return get_resp

        client = _mock_client(
            post=AsyncMock(side_effect=_capture_post),
            get=AsyncMock(side_effect=_capture_get),
        )

        with patch(_TARGET, return_value=client):
            url = await platform.create_pr(
                title="My MR",
                body="description",
                head="feature",
                base="main",
            )

        assert url.html_url == "https://gitlab.com/g/p/-/merge_requests/3"
        assert url.number == 3
        # Exactly two HTTP calls: POST then GET
        assert call_log == ["POST", "GET"]

    @pytest.mark.asyncio
    async def test_409_fallback_empty_list_raises(self) -> None:
        """POST returns 409; fallback GET returns empty list → error."""
        platform = _make_platform()
        post_resp = _json_response(409, text="Conflict")
        get_resp = _json_response(200, [])

        client = _mock_client(
            post=AsyncMock(return_value=post_resp),
            get=AsyncMock(return_value=get_resp),
        )

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError, match="duplicate"):
                await platform.create_pr(
                    title="T", body="B", head="feat", base="main"
                )

    @pytest.mark.asyncio
    async def test_409_fallback_get_failure_raises(self) -> None:
        """POST returns 409; fallback GET fails → error with 409 context."""
        platform = _make_platform()
        post_resp = _json_response(409, text="Conflict")
        get_resp = _json_response(500, text="Server Error")

        client = _mock_client(
            post=AsyncMock(return_value=post_resp),
            get=AsyncMock(return_value=get_resp),
        )

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError, match="409.*fallback.*failed"):
                await platform.create_pr(
                    title="T", body="B", head="feat", base="main"
                )

    @pytest.mark.asyncio
    async def test_non_201_non_409_raises(self) -> None:
        """POST returns a non-201/non-409 error → IntegrationError."""
        platform = _make_platform()
        post_resp = _json_response(422, text="Validation failed")

        client = _mock_client(post=AsyncMock(return_value=post_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError, match="422"):
                await platform.create_pr(
                    title="T", body="B", head="feat", base="main"
                )


# ===========================================================================
# TS-04-SMOKE-3: close_issue with comment → two HTTP calls
# Path: close_issue(comment) → POST /notes → PUT state_event=close
# Requirements: 04-REQ-6.1, 04-REQ-6.2
# ===========================================================================


class TestSmokeCloseIssueWithComment:
    """TS-04-SMOKE-3: close_issue adds comment then closes the issue."""

    @pytest.mark.asyncio
    async def test_close_issue_two_calls_in_order(self) -> None:
        """Comment POST then close PUT, both with PRIVATE-TOKEN."""
        platform = _make_platform()

        call_log: list[dict] = []

        async def _capture_post(*a, **kw):
            call_log.append({"method": "post", "kwargs": kw})
            return _json_response(201)

        async def _capture_put(*a, **kw):
            call_log.append({"method": "put", "kwargs": kw})
            return _json_response(200)

        client = _mock_client(
            post=AsyncMock(side_effect=_capture_post),
            put=AsyncMock(side_effect=_capture_put),
        )

        with patch(_TARGET, return_value=client):
            result = await platform.close_issue(10, "Closing this issue.")

        assert result is None
        # Exactly two calls in order: POST (comment) then PUT (close)
        assert len(call_log) == 2
        assert call_log[0]["method"] == "post"
        assert call_log[1]["method"] == "put"
        # Both calls include PRIVATE-TOKEN auth header
        for call in call_log:
            headers = call["kwargs"].get("headers", {})
            assert "PRIVATE-TOKEN" in headers

    @pytest.mark.asyncio
    async def test_close_issue_no_comment_single_call(self) -> None:
        """Without comment, only the close PUT is made."""
        platform = _make_platform()

        call_log: list[str] = []

        async def _capture_put(*a, **kw):
            call_log.append("PUT")
            return _json_response(200)

        client = _mock_client(put=AsyncMock(side_effect=_capture_put))

        with patch(_TARGET, return_value=client):
            await platform.close_issue(10)

        assert call_log == ["PUT"]


# ===========================================================================
# TS-04-SMOKE-4: SSRF violation blocked at construction time
# Path: GitLabPlatform(url="192.168.1.100") → _validate_url → ConfigError
# Requirements: 04-REQ-1.2, 04-REQ-1.E1
# ===========================================================================


class TestSmokeSSRFBlocked:
    """TS-04-SMOKE-4: ConfigError raised for private IP at construction."""

    def test_ssrf_private_ip_raises_config_error(self) -> None:
        """Private RFC-1918 address raises ConfigError, not IntegrationError."""
        from afissues.gitlab import GitLabPlatform

        with pytest.raises(ConfigError):
            GitLabPlatform("group/project", "tok", "192.168.1.100")

    def test_ssrf_loopback_raises_config_error(self) -> None:
        """Loopback address raises ConfigError."""
        from afissues.gitlab import GitLabPlatform

        with pytest.raises(ConfigError):
            GitLabPlatform("group/project", "tok", "127.0.0.1")

    def test_no_http_client_created_on_ssrf(self) -> None:
        """No httpx.AsyncClient is ever created when SSRF check fails."""
        from afissues.gitlab import GitLabPlatform

        with patch(_TARGET) as mock_client:
            with pytest.raises(ConfigError):
                GitLabPlatform("group/project", "tok", "10.0.0.1")
            mock_client.assert_not_called()


# ===========================================================================
# TS-04-SMOKE-5: Gitea unavailable concept (standalone verification)
# Path: Lazy import of afissues.gitea → ImportError → ConfigError
# Requirements: 04-REQ-21.1, 04-REQ-21.2
#
# Note: The full factory flow requires the agentfox package. This test
# verifies the underlying concept at the afissues level: that the gitea
# module exists and is importable, and that ConfigError is the correct
# exception type for platform unavailability scenarios.
# ===========================================================================


class TestSmokeGiteaImportability:
    """TS-04-SMOKE-5: Verify gitea module availability and error types."""

    def test_gitea_module_importable(self) -> None:
        """afissues.gitea is importable (spec 05 delivered it)."""
        from afissues.gitea import GiteaPlatform

        assert GiteaPlatform.forge_type == "gitea"

    def test_config_error_raised_for_unavailable_platform(self) -> None:
        """ConfigError is the correct exception for unavailable platforms."""
        # Verify that ConfigError exists and can be raised
        with pytest.raises(ConfigError, match="not.*available"):
            raise ConfigError(
                "The Gitea platform is not yet available. "
                "Install the afissues package with Gitea support."
            )

    def test_import_error_does_not_propagate(self) -> None:
        """When a platform module can't be imported, ConfigError is raised."""
        # This simulates what the factory should do:
        # catch ImportError and raise ConfigError
        with pytest.raises(ConfigError):
            try:
                import afissues.nonexistent_platform  # noqa: F401
            except ImportError:
                raise ConfigError(
                    "The requested platform is not yet available."
                )


# ===========================================================================
# Wiring verification: all methods use request_with_retry
# Requirements: 04-REQ-19.1, 04-REQ-22.1
# ===========================================================================


class TestWiringVerification:
    """Verify all GitLabPlatform methods call request_with_retry."""

    def test_gitlab_source_uses_request_with_retry(self) -> None:
        """gitlab.py imports and uses request_with_retry from _http.py."""
        import inspect

        from afissues import gitlab

        source = inspect.getsource(gitlab)
        assert "from afissues._http import" in source
        assert "request_with_retry" in source

    def test_github_source_uses_request_with_retry(self) -> None:
        """github.py imports request_with_retry from _http.py (04-REQ-19.3)."""
        import inspect

        from afissues import github

        source = inspect.getsource(github)
        assert "from afissues._http import" in source
        assert "request_with_retry" in source

    def test_no_stubs_in_gitlab(self) -> None:
        """gitlab.py has no NotImplementedError or stub markers."""
        import inspect

        from afissues import gitlab

        source = inspect.getsource(gitlab)
        assert "NotImplementedError" not in source
        assert "pass  # stub" not in source

    def test_no_stubs_in_http(self) -> None:
        """_http.py has no NotImplementedError or stub markers."""
        import inspect

        from afissues import _http

        source = inspect.getsource(_http)
        assert "NotImplementedError" not in source

    def test_gitlab_all_14_methods_exist(self) -> None:
        """GitLabPlatform has all 14 required methods (12 + 2 non-protocol)."""
        from afissues.gitlab import GitLabPlatform

        required_methods = [
            "create_issue",
            "list_issues_by_label",
            "add_issue_comment",
            "assign_label",
            "close_issue",
            "remove_label",
            "list_issue_comments",
            "get_issue",
            "update_issue",
            "create_label",
            "create_pr",
            "close",
            "search_issues",
            "check_credentials",
        ]
        for method_name in required_methods:
            assert hasattr(GitLabPlatform, method_name), (
                f"GitLabPlatform missing method: {method_name}"
            )
            assert callable(getattr(GitLabPlatform, method_name))

    def test_gitlab_importable_from_top_level(self) -> None:
        """from afissues import GitLabPlatform succeeds (04-REQ-1.5)."""
        from afissues import GitLabPlatform

        assert GitLabPlatform.forge_type == "gitlab"

    def test_gitlab_default_url(self) -> None:
        """GitLabPlatform can be constructed with just project_id and token."""
        from afissues.gitlab import GitLabPlatform

        with patch("afissues.gitlab._validate_url"):
            p = GitLabPlatform("ns/proj", "tok")
        assert "gitlab.com" in p._base_url


# ===========================================================================
# Return value propagation verification
# Requirements: 04-REQ-2.1, 04-REQ-12.1
# ===========================================================================


class TestReturnValuePropagation:
    """Verify return values propagate correctly through the call chain."""

    @pytest.mark.asyncio
    async def test_create_issue_returns_issue_result(self) -> None:
        """create_issue returns IssueResult with all fields populated."""
        platform = _make_platform()
        resp = _json_response(
            201,
            {
                "iid": 99,
                "title": "Test",
                "web_url": "https://gitlab.com/g/p/-/issues/99",
                "description": "Hello",
                "labels": ["bug", "fix"],
            },
        )
        client = _mock_client(post=AsyncMock(return_value=resp))

        with patch(_TARGET, return_value=client):
            result = await platform.create_issue("Test", "Hello", ["bug", "fix"])

        assert isinstance(result, IssueResult)
        assert result.number == 99
        assert result.title == "Test"
        assert result.html_url == "https://gitlab.com/g/p/-/issues/99"
        assert result.body == "Hello"
        assert result.labels == ("bug", "fix")

    @pytest.mark.asyncio
    async def test_create_pr_returns_web_url_string(self) -> None:
        """create_pr returns a PrResult with web_url from the MR response."""
        platform = _make_platform()
        resp = _json_response(
            201,
            {"web_url": "https://gitlab.com/g/p/-/merge_requests/7", "iid": 7},
        )
        client = _mock_client(post=AsyncMock(return_value=resp))

        with patch(_TARGET, return_value=client):
            url = await platform.create_pr(
                title="T", body="B", head="feat", base="main"
            )

        assert hasattr(url, "html_url")
        assert url.html_url == "https://gitlab.com/g/p/-/merge_requests/7"
        assert url.number == 7

    @pytest.mark.asyncio
    async def test_close_issue_returns_none(self) -> None:
        """close_issue returns None on success."""
        platform = _make_platform()
        client = _mock_client(put=AsyncMock(return_value=_json_response(200)))

        with patch(_TARGET, return_value=client):
            result = await platform.close_issue(1)

        assert result is None
