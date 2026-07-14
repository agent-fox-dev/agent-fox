"""Tests for GitHubPlatform.create_pr() HTTP behavior.

Test Spec: TS-02-19 (HTTP 201 success), TS-02-20 (HTTP 422 duplicate-PR idempotent),
           TS-02-21 (non-201 non-duplicate raises IntegrationError),
           TS-02-E12 (httpx timeout propagates), TS-02-E13 (422 but GET finds no PR),
           TS-02-E14 (single POST attempt, no retry on HTTP errors),
           TS-02-P7 (request count property)
Requirements: 02-REQ-7.1, 02-REQ-7.2, 02-REQ-7.3,
              02-REQ-7.E1, 02-REQ-7.E2, 02-REQ-7.E3

Note: GitHubPlatform.create_pr() does not exist yet — it was removed in spec 65
and must be re-added by task group 12.  The codebase uses httpx (NOT aiohttp as
the spec claims).  All tests will fail (RED) with AttributeError until the
implementation is added, which is the expected Group 4 behavior.

Reviewer finding applied: The platform uses httpx, not aiohttp.  All transport-
level exception tests use httpx exception types (e.g. httpx.ReadTimeout) instead
of the spec's aiohttp.ServerTimeoutError.
"""

from __future__ import annotations

from typing import Any
from unittest.mock import AsyncMock, MagicMock, patch

import httpx
import pytest

from afissues.errors import IntegrationError
from afissues.github import GitHubPlatform

_TARGET = "afissues._http.httpx.AsyncClient"
_SLEEP_TARGET = "afissues._http.asyncio.sleep"


# ---------------------------------------------------------------------------
# Helpers — same patterns used by test_github_issues_rest.py and
# test_github_create_label.py
# ---------------------------------------------------------------------------


def _mock_client(**method_responses: MagicMock | Any) -> AsyncMock:
    """Build a mock httpx.AsyncClient with specified method responses.

    Pass keyword arguments like get=mock_response or post=mock_response.
    Returns an AsyncMock that works as an async context manager.
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
    """Build a mock httpx response."""
    resp = MagicMock()
    resp.status_code = status_code
    resp.text = text
    if json_data is not None:
        resp.json.return_value = json_data
    return resp


# ---------------------------------------------------------------------------
# TS-02-19: GitHubPlatform.create_pr() sends a single POST
#           /repos/{owner}/{repo}/pulls request and returns html_url from the
#           HTTP 201 response body.
# ---------------------------------------------------------------------------


class TestCreatePrSuccess:
    """TS-02-19: HTTP 201 success path — POST returns html_url.

    Requirements: 02-REQ-7.1
    """

    async def test_returns_html_url_from_201_response(self) -> None:
        """create_pr returns the html_url string from a 201 JSON response."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        mock_resp = _json_response(
            201,
            {"html_url": "https://github.com/owner/repo/pull/99"},
        )

        requests_made: list[tuple[str, str, dict | None]] = []

        async def mock_post(url: str, *, json: dict | None = None, headers: dict | None = None, **kw: Any) -> MagicMock:
            requests_made.append(("POST", url, json))
            return mock_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            result = await platform.create_pr(title="My PR", body="body text", head="feat/x", base="main")

        assert result == "https://github.com/owner/repo/pull/99"

    async def test_exactly_one_post_request_sent(self) -> None:
        """Exactly one POST request is sent to /repos/{owner}/{repo}/pulls."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        mock_resp = _json_response(
            201,
            {"html_url": "https://github.com/owner/repo/pull/99"},
        )

        post_count = 0

        async def mock_post(url: str, **kw: Any) -> MagicMock:
            nonlocal post_count
            post_count += 1
            return mock_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            await platform.create_pr(title="My PR", body="body text", head="feat/x", base="main")

        assert post_count == 1

    async def test_post_sent_to_correct_endpoint(self) -> None:
        """POST request is sent to /repos/{owner}/{repo}/pulls."""
        platform = GitHubPlatform(owner="myorg", repo="myrepo", token="tok")

        mock_resp = _json_response(
            201,
            {"html_url": "https://github.com/myorg/myrepo/pull/1"},
        )

        captured_urls: list[str] = []

        async def mock_post(url: str, **kw: Any) -> MagicMock:
            captured_urls.append(url)
            return mock_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            await platform.create_pr(title="PR Title", body="body", head="feat/x", base="main")

        assert len(captured_urls) == 1
        assert "/repos/myorg/myrepo/pulls" in captured_urls[0]

    async def test_post_sends_correct_payload(self) -> None:
        """POST request includes title, body, head, and base in JSON payload."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        mock_resp = _json_response(
            201,
            {"html_url": "https://github.com/owner/repo/pull/1"},
        )

        captured_payloads: list[dict] = []

        async def mock_post(url: str, *, json: dict | None = None, headers: dict | None = None, **kw: Any) -> MagicMock:
            captured_payloads.append(json or {})
            return mock_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            await platform.create_pr(
                title="My PR Title",
                body="PR description",
                head="feat/my-branch",
                base="main",
            )

        assert len(captured_payloads) == 1
        payload = captured_payloads[0]
        assert payload["title"] == "My PR Title"
        assert payload["body"] == "PR description"
        assert payload["head"] == "feat/my-branch"
        assert payload["base"] == "main"

    async def test_post_uses_auth_headers(self) -> None:
        """POST request includes Bearer token and GitHub API headers."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="test-token")

        mock_resp = _json_response(
            201,
            {"html_url": "https://github.com/owner/repo/pull/1"},
        )

        captured_headers: list[dict] = []

        async def mock_post(url: str, *, json: dict | None = None, headers: dict | None = None, **kw: Any) -> MagicMock:
            captured_headers.append(headers or {})
            return mock_resp

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            await platform.create_pr(title="T", body="B", head="H", base="main")

        assert len(captured_headers) == 1
        h = captured_headers[0]
        assert h["Authorization"] == "Bearer test-token"
        assert h["Accept"] == "application/vnd.github+json"
        assert h["X-GitHub-Api-Version"] == "2022-11-28"

    async def test_no_get_request_on_success(self) -> None:
        """On 201 success, no GET request is made (no duplicate-PR lookup)."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        mock_resp = _json_response(
            201,
            {"html_url": "https://github.com/owner/repo/pull/1"},
        )

        get_count = 0

        async def mock_get(url: str, **kw: Any) -> MagicMock:
            nonlocal get_count
            get_count += 1
            return _json_response(200, [])

        client = _mock_client(post=AsyncMock(return_value=mock_resp), get=mock_get)

        with patch(_TARGET, return_value=client):
            await platform.create_pr(title="T", body="B", head="H", base="main")

        assert get_count == 0


# ---------------------------------------------------------------------------
# TS-02-20: GitHubPlatform.create_pr() handles HTTP 422 duplicate-PR
#           idempotently by querying the existing PR and returning its
#           html_url.
# ---------------------------------------------------------------------------


class TestCreatePrDuplicateIdempotent:
    """TS-02-20: HTTP 422 duplicate-PR handled idempotently.

    Requirements: 02-REQ-7.2
    """

    async def test_422_duplicate_returns_existing_pr_url(self) -> None:
        """On 422 with duplicate-PR message, queries existing PR and returns
        its html_url."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        post_resp = _json_response(
            422,
            {
                "message": "Validation Failed",
                "errors": [{"message": "A pull request already exists for owner:feat/x."}],
            },
            text='{"message":"Validation Failed","errors":[{"message":"A pull request already exists"}]}',
        )

        get_resp = _json_response(
            200,
            [{"html_url": "https://github.com/owner/repo/pull/7"}],
        )

        async def mock_post(url: str, **kw: Any) -> MagicMock:
            return post_resp

        async def mock_get(url: str, **kw: Any) -> MagicMock:
            return get_resp

        client = _mock_client(post=mock_post, get=mock_get)

        with patch(_TARGET, return_value=client):
            result = await platform.create_pr(title="My PR", body="body", head="feat/x", base="main")

        assert result == "https://github.com/owner/repo/pull/7"

    async def test_422_duplicate_no_integration_error_raised(self) -> None:
        """No IntegrationError is raised for a duplicate-PR 422 response."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        post_resp = _json_response(
            422,
            {
                "message": "Validation Failed",
                "errors": [{"message": "A pull request already exists for owner:feat/x."}],
            },
        )

        get_resp = _json_response(
            200,
            [{"html_url": "https://github.com/owner/repo/pull/7"}],
        )

        client = _mock_client(
            post=AsyncMock(return_value=post_resp),
            get=AsyncMock(return_value=get_resp),
        )

        with patch(_TARGET, return_value=client):
            # Must NOT raise IntegrationError
            result = await platform.create_pr(title="My PR", body="body", head="feat/x", base="main")
            assert isinstance(result, str)

    async def test_422_duplicate_sends_get_with_head_and_base(self) -> None:
        """On duplicate-PR 422, GET request is made with head and base params
        to /repos/{owner}/{repo}/pulls."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        post_resp = _json_response(
            422,
            {
                "message": "Validation Failed",
                "errors": [{"message": "A pull request already exists"}],
            },
        )

        get_resp = _json_response(
            200,
            [{"html_url": "https://github.com/owner/repo/pull/7"}],
        )

        get_requests: list[tuple[str, dict | None]] = []

        async def mock_post(url: str, **kw: Any) -> MagicMock:
            return post_resp

        async def mock_get(
            url: str, *, params: dict | None = None, headers: dict | None = None, **kw: Any
        ) -> MagicMock:
            get_requests.append((url, params))
            return get_resp

        client = _mock_client(post=mock_post, get=mock_get)

        with patch(_TARGET, return_value=client):
            await platform.create_pr(title="PR", body="body", head="feat/x", base="main")

        assert len(get_requests) == 1
        url, params = get_requests[0]
        assert "/repos/owner/repo/pulls" in url
        # The params should include head and base for filtering
        assert params is not None
        # The head param may include the owner prefix (e.g. "owner:feat/x")
        # or the raw branch name — implementation decides the format
        assert "base" in params or "main" in str(params)

    async def test_422_duplicate_returns_first_result_html_url(self) -> None:
        """When multiple existing PRs are returned, the first html_url is used."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        post_resp = _json_response(
            422,
            {
                "message": "Validation Failed",
                "errors": [{"message": "A pull request already exists"}],
            },
        )

        get_resp = _json_response(
            200,
            [
                {"html_url": "https://github.com/owner/repo/pull/3"},
                {"html_url": "https://github.com/owner/repo/pull/5"},
            ],
        )

        client = _mock_client(
            post=AsyncMock(return_value=post_resp),
            get=AsyncMock(return_value=get_resp),
        )

        with patch(_TARGET, return_value=client):
            result = await platform.create_pr(title="PR", body="body", head="feat/x", base="main")

        assert result == "https://github.com/owner/repo/pull/3"


# ---------------------------------------------------------------------------
# TS-02-21: GitHubPlatform.create_pr() raises IntegrationError for any
#           non-201 HTTP response that is not a duplicate-PR 422.
# ---------------------------------------------------------------------------


class TestCreatePrErrorResponses:
    """TS-02-21: Non-201 non-duplicate responses raise IntegrationError.

    Requirements: 02-REQ-7.3
    """

    @pytest.mark.parametrize(
        "status_code",
        [400, 403, 404, 500],
        ids=["bad_request_400", "forbidden_403", "not_found_404", "server_error_500"],
    )
    async def test_non_201_raises_integration_error(self, status_code: int) -> None:
        """Non-201 responses (400, 403, 404, 500) raise IntegrationError."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        mock_resp = _json_response(status_code, text=f"Error {status_code}")
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.create_pr(title="T", body="B", head="H", base="main")

    async def test_422_without_duplicate_message_raises_integration_error(self) -> None:
        """A 422 response without the duplicate-PR indicator raises
        IntegrationError (not treated as idempotent success)."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        mock_resp = _json_response(
            422,
            {
                "message": "Validation Failed",
                "errors": [{"message": "Some other validation error"}],
            },
            text='{"message":"Validation Failed","errors":[{"message":"Some other validation error"}]}',
        )

        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.create_pr(title="T", body="B", head="H", base="main")

    async def test_422_with_empty_errors_raises_integration_error(self) -> None:
        """A 422 response with an empty errors list raises IntegrationError."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        mock_resp = _json_response(
            422,
            {"message": "Validation Failed", "errors": []},
            text='{"message":"Validation Failed","errors":[]}',
        )

        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.create_pr(title="T", body="B", head="H", base="main")

    async def test_422_with_no_errors_key_raises_integration_error(self) -> None:
        """A 422 response without an 'errors' key raises IntegrationError."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        mock_resp = _json_response(
            422,
            {"message": "Validation Failed"},
            text='{"message":"Validation Failed"}',
        )

        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.create_pr(title="T", body="B", head="H", base="main")

    async def test_integration_error_includes_status_details(self) -> None:
        """IntegrationError includes failure details for diagnosis."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        mock_resp = _json_response(500, text="Internal Server Error")
        client = _mock_client(post=AsyncMock(return_value=mock_resp))

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError, match="500|failed|error"):
                await platform.create_pr(title="T", body="B", head="H", base="main")


# ---------------------------------------------------------------------------
# TS-02-E12: When the httpx request to GitHub times out during create_pr(),
#            the httpx exception propagates from GitHubPlatform.create_pr();
#            it is not caught or wrapped within the method.
#
# Note: The spec references aiohttp.ServerTimeoutError, but the codebase
# uses httpx.  The equivalent retryable transport errors are
# httpx.ConnectTimeout and httpx.ReadTimeout (in _RETRYABLE_ERRORS).
# The _request() method retries these up to _MAX_RETRIES times; after all
# retries are exhausted, the exception propagates.  The key property is
# that create_pr() does NOT add its own catch for these exceptions.
# ---------------------------------------------------------------------------


class TestCreatePrTimeoutPropagation:
    """TS-02-E12: httpx timeout exceptions propagate from create_pr().

    Requirements: 02-REQ-7.E1
    """

    async def test_read_timeout_propagates(self) -> None:
        """httpx.ReadTimeout propagates from create_pr() after retries
        are exhausted by _request()."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        mock_post = AsyncMock(side_effect=httpx.ReadTimeout("read timeout"))
        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            with patch(_SLEEP_TARGET, new_callable=AsyncMock):
                with pytest.raises(httpx.ReadTimeout):
                    await platform.create_pr(title="T", body="B", head="H", base="main")

    async def test_connect_timeout_propagates(self) -> None:
        """httpx.ConnectTimeout propagates from create_pr() after retries
        are exhausted by _request()."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        mock_post = AsyncMock(side_effect=httpx.ConnectTimeout("connect timeout"))
        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            with patch(_SLEEP_TARGET, new_callable=AsyncMock):
                with pytest.raises(httpx.ConnectTimeout):
                    await platform.create_pr(title="T", body="B", head="H", base="main")

    async def test_connect_error_propagates(self) -> None:
        """httpx.ConnectError propagates from create_pr() after retries
        are exhausted by _request()."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        mock_post = AsyncMock(side_effect=httpx.ConnectError("connection refused"))
        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            with patch(_SLEEP_TARGET, new_callable=AsyncMock):
                with pytest.raises(httpx.ConnectError):
                    await platform.create_pr(title="T", body="B", head="H", base="main")

    async def test_timeout_not_wrapped_in_integration_error(self) -> None:
        """Transport timeouts are NOT wrapped in IntegrationError by
        create_pr() — they propagate as their original exception type."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        mock_post = AsyncMock(side_effect=httpx.ReadTimeout("timeout"))
        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            with patch(_SLEEP_TARGET, new_callable=AsyncMock):
                with pytest.raises(httpx.ReadTimeout):
                    await platform.create_pr(title="T", body="B", head="H", base="main")
                # If create_pr wrapped it in IntegrationError, this test
                # would fail because pytest.raises(httpx.ReadTimeout)
                # would not match.


# ---------------------------------------------------------------------------
# TS-02-E13: GitHubPlatform.create_pr() raises IntegrationError when
#            HTTP 422 is returned but the subsequent GET query finds no
#            existing PR.
# ---------------------------------------------------------------------------


class TestCreatePrDuplicateNoExistingPr:
    """TS-02-E13: 422 duplicate but GET returns empty list -> IntegrationError.

    Requirements: 02-REQ-7.E2
    """

    async def test_422_duplicate_empty_get_raises_integration_error(self) -> None:
        """When POST returns 422 (duplicate-PR) but GET returns an empty list,
        IntegrationError is raised to signal the unexpected state."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        post_resp = _json_response(
            422,
            {
                "message": "Validation Failed",
                "errors": [{"message": "A pull request already exists for owner:feat/x."}],
            },
        )

        get_resp = _json_response(200, [])  # Empty list — no existing PR found

        client = _mock_client(
            post=AsyncMock(return_value=post_resp),
            get=AsyncMock(return_value=get_resp),
        )

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.create_pr(title="T", body="B", head="feat/x", base="main")

    async def test_422_duplicate_empty_get_does_not_return_url(self) -> None:
        """When GET returns no results after a 422 duplicate, no html_url is
        returned — the function must raise, not return None or empty string."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        post_resp = _json_response(
            422,
            {
                "message": "Validation Failed",
                "errors": [{"message": "A pull request already exists"}],
            },
        )

        get_resp = _json_response(200, [])

        client = _mock_client(
            post=AsyncMock(return_value=post_resp),
            get=AsyncMock(return_value=get_resp),
        )

        with patch(_TARGET, return_value=client):
            raised = False
            try:
                result = await platform.create_pr(title="T", body="B", head="feat/x", base="main")
                # If we reach here, the test should fail
                pytest.fail(f"Expected IntegrationError but got return value: {result!r}")
            except IntegrationError:
                raised = True
            assert raised


# ---------------------------------------------------------------------------
# TS-02-E14: GitHubPlatform.create_pr() makes exactly one POST attempt
#            (no retry) on failure; no additional POST requests are sent.
#
# Note: The _request() method retries transport-level errors (httpx.*Timeout,
# httpx.ConnectError) but NOT HTTP-level errors (4xx/5xx responses).
# This test verifies that an HTTP 500 response results in exactly one POST
# call to the mock — _request returns it without retrying, and create_pr()
# does not add its own retry logic.
# ---------------------------------------------------------------------------


class TestCreatePrSingleAttempt:
    """TS-02-E14: Exactly one POST attempt on HTTP error; no retry.

    Requirements: 02-REQ-7.E3
    """

    async def test_500_response_exactly_one_post(self) -> None:
        """On HTTP 500, exactly one POST request is sent — no retry by
        create_pr() itself."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        post_count = 0

        async def mock_post(url: str, **kw: Any) -> MagicMock:
            nonlocal post_count
            post_count += 1
            return _json_response(500, text="Internal Server Error")

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.create_pr(title="T", body="B", head="H", base="main")

        assert post_count == 1, f"Expected exactly 1 POST attempt, got {post_count}"

    async def test_403_response_exactly_one_post(self) -> None:
        """On HTTP 403, exactly one POST request is sent."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        post_count = 0

        async def mock_post(url: str, **kw: Any) -> MagicMock:
            nonlocal post_count
            post_count += 1
            return _json_response(403, text="Forbidden")

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.create_pr(title="T", body="B", head="H", base="main")

        assert post_count == 1

    async def test_integration_error_raised_after_single_attempt(self) -> None:
        """IntegrationError is raised after exactly one failed POST — no
        internal retry loop in create_pr()."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        mock_resp = _json_response(500, text="Server Error")
        mock_post = AsyncMock(return_value=mock_resp)
        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.create_pr(title="T", body="B", head="H", base="main")

        # AsyncMock records call_count
        assert mock_post.call_count == 1


# ---------------------------------------------------------------------------
# TS-02-P7: Property test — GitHubPlatform.create_pr() makes at most one
#           POST request per invocation; a follow-up GET is only issued on
#           duplicate-PR 422; no other retries occur.
# ---------------------------------------------------------------------------


class TestCreatePrRequestCountProperty:
    """TS-02-P7: Request count invariants for create_pr().

    Property: 02-PROP-7
    Validates: 02-REQ-7.1, 02-REQ-7.3, 02-REQ-7.E3

    For any HTTP response status, create_pr() makes at most one POST and
    at most one GET (only on duplicate-PR 422).  Total requests <= 2.
    """

    @pytest.mark.parametrize(
        "status_code,is_duplicate_422",
        [
            (201, False),
            (400, False),
            (403, False),
            (404, False),
            (500, False),
            (422, True),  # duplicate-PR 422 -> GET follows
            (422, False),  # non-duplicate 422 -> no GET
        ],
        ids=[
            "success_201",
            "bad_request_400",
            "forbidden_403",
            "not_found_404",
            "server_error_500",
            "duplicate_pr_422",
            "non_duplicate_422",
        ],
    )
    async def test_request_count_invariant(self, status_code: int, is_duplicate_422: bool) -> None:
        """POST count == 1; GET count <= 1 on duplicate-PR 422, else 0;
        total POST + GET <= 2."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        post_count = 0
        get_count = 0

        if status_code == 201:
            post_json = {"html_url": "https://github.com/owner/repo/pull/1"}
        elif status_code == 422 and is_duplicate_422:
            post_json = {
                "message": "Validation Failed",
                "errors": [{"message": "A pull request already exists"}],
            }
        elif status_code == 422:
            post_json = {
                "message": "Validation Failed",
                "errors": [{"message": "Some other error"}],
            }
        else:
            post_json = {"message": f"Error {status_code}"}

        post_text = str(post_json)

        async def mock_post(url: str, *, json: dict | None = None, headers: dict | None = None, **kw: Any) -> MagicMock:
            nonlocal post_count
            post_count += 1
            return _json_response(status_code, post_json, text=post_text)

        async def mock_get(
            url: str, *, params: dict | None = None, headers: dict | None = None, **kw: Any
        ) -> MagicMock:
            nonlocal get_count
            get_count += 1
            return _json_response(
                200,
                [{"html_url": "https://github.com/owner/repo/pull/7"}],
            )

        client = _mock_client(post=mock_post, get=mock_get)

        with patch(_TARGET, return_value=client):
            try:
                await platform.create_pr(title="T", body="B", head="feat/x", base="main")
            except (IntegrationError, NotImplementedError):
                pass  # Expected for error status codes

        # Invariant: exactly one POST attempt
        assert post_count == 1, (
            f"Expected exactly 1 POST, got {post_count} (status={status_code}, is_duplicate_422={is_duplicate_422})"
        )

        # Invariant: GET only on duplicate-PR 422
        if is_duplicate_422:
            assert get_count <= 1, f"Expected at most 1 GET on duplicate-PR 422, got {get_count}"
        else:
            assert get_count == 0, f"Expected 0 GET requests for status {status_code} (non-duplicate), got {get_count}"

        # Invariant: total bounded
        assert post_count + get_count <= 2, f"Total requests {post_count + get_count} exceeds bound of 2"

    async def test_201_success_no_get_request(self) -> None:
        """On 201 success: exactly 1 POST, 0 GET."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        post_count = 0
        get_count = 0

        async def mock_post(url: str, **kw: Any) -> MagicMock:
            nonlocal post_count
            post_count += 1
            return _json_response(
                201,
                {"html_url": "https://github.com/owner/repo/pull/1"},
            )

        async def mock_get(url: str, **kw: Any) -> MagicMock:
            nonlocal get_count
            get_count += 1
            return _json_response(200, [])

        client = _mock_client(post=mock_post, get=mock_get)

        with patch(_TARGET, return_value=client):
            await platform.create_pr(title="T", body="B", head="H", base="main")

        assert post_count == 1
        assert get_count == 0

    async def test_duplicate_422_exactly_one_post_one_get(self) -> None:
        """On duplicate-PR 422: exactly 1 POST + 1 GET = 2 total."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        post_count = 0
        get_count = 0

        async def mock_post(url: str, **kw: Any) -> MagicMock:
            nonlocal post_count
            post_count += 1
            return _json_response(
                422,
                {
                    "message": "Validation Failed",
                    "errors": [{"message": "A pull request already exists"}],
                },
            )

        async def mock_get(url: str, **kw: Any) -> MagicMock:
            nonlocal get_count
            get_count += 1
            return _json_response(
                200,
                [{"html_url": "https://github.com/owner/repo/pull/7"}],
            )

        client = _mock_client(post=mock_post, get=mock_get)

        with patch(_TARGET, return_value=client):
            await platform.create_pr(title="T", body="B", head="feat/x", base="main")

        assert post_count == 1
        assert get_count == 1
        assert post_count + get_count == 2
