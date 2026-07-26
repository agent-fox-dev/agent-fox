"""Tests for spec 06: GitHubPlatform PR lifecycle methods and create_pr PrResult.

Task group 2 — failing tests for:
  - GitHubPlatform.get_pr_state (TS-06-14, TS-06-15, TS-06-E3, TS-06-E4)
  - GitHubPlatform.get_pr_checks with pagination (TS-06-16, TS-06-17, TS-06-18,
    TS-06-E5, TS-06-E6, TS-06-E7)
  - GitHubPlatform.get_pr_reviews (TS-06-19, TS-06-20, TS-06-E8, TS-06-E9)
  - create_pr() return type change to PrResult (TS-06-21, TS-06-22, TS-06-23,
    TS-06-E10, TS-06-E11)

Requirements: 06-REQ-4.1, 06-REQ-4.2, 06-REQ-4.E1, 06-REQ-4.E2,
              06-REQ-5.1, 06-REQ-5.2, 06-REQ-5.3, 06-REQ-5.E1, 06-REQ-5.E2,
              06-REQ-5.E3, 06-REQ-6.1, 06-REQ-6.2, 06-REQ-6.E1, 06-REQ-6.E2,
              06-REQ-7.1, 06-REQ-7.2, 06-REQ-7.3, 06-REQ-7.E1, 06-REQ-7.E2
"""

from __future__ import annotations

from typing import Any, get_type_hints
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from afissues.errors import IntegrationError
from afissues.github import GitHubPlatform

_TARGET = "afissues._http.httpx.AsyncClient"
_SLEEP_TARGET = "afissues._http.asyncio.sleep"


# ---------------------------------------------------------------------------
# Helpers — shared mock builders (same pattern as test_merge_strategy_github_pr)
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


def _make_check_run(name: str, **overrides: Any) -> dict:
    """Build a single check-run API response dict."""
    run: dict[str, Any] = {
        "name": name,
        "status": "completed",
        "conclusion": "success",
        "output": {"title": "OK", "summary": "pass"},
    }
    run.update(overrides)
    return run


# ---------------------------------------------------------------------------
# TS-06-14: GitHubPlatform.get_pr_state(42) calls GET /repos/{owner}/{repo}/pulls/42
#           and maps the response to a PrState.
#
# Requirement: 06-REQ-4.1
# ---------------------------------------------------------------------------


class TestGetPrStateHappyPath:
    """TS-06-14: get_pr_state sends correct GET and returns mapped PrState."""

    async def test_returns_pr_state_from_api_response(self) -> None:
        """get_pr_state returns a PrState with correct field mappings."""
        from afissues.protocol import PrState

        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        api_response = {
            "number": 42,
            "state": "open",
            "merged": False,
            "head": {"sha": "abc123"},
        }

        async def mock_get(url: str, **kw: Any) -> MagicMock:
            return _json_response(200, api_response)

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            result = await platform.get_pr_state(42)

        assert result == PrState(
            number=42, state="open", merged=False, head_sha="abc123"
        )

    async def test_calls_correct_api_endpoint(self) -> None:
        """GET request is sent to /repos/{owner}/{repo}/pulls/42."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        captured_urls: list[str] = []

        async def mock_get(url: str, **kw: Any) -> MagicMock:
            captured_urls.append(url)
            return _json_response(
                200,
                {
                    "number": 42,
                    "state": "open",
                    "merged": False,
                    "head": {"sha": "abc123"},
                },
            )

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            await platform.get_pr_state(42)

        assert len(captured_urls) == 1
        assert "/repos/owner/repo/pulls/42" in captured_urls[0]

    async def test_uses_get_method(self) -> None:
        """Request uses HTTP GET method."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        get_called = False

        async def mock_get(url: str, **kw: Any) -> MagicMock:
            nonlocal get_called
            get_called = True
            return _json_response(
                200,
                {
                    "number": 42,
                    "state": "open",
                    "merged": False,
                    "head": {"sha": "abc123"},
                },
            )

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            await platform.get_pr_state(42)

        assert get_called


# ---------------------------------------------------------------------------
# TS-06-15: GitHubPlatform.get_pr_state raises IntegrationError on non-2xx.
#
# Requirement: 06-REQ-4.2
# ---------------------------------------------------------------------------


class TestGetPrStateError:
    """TS-06-15: get_pr_state raises IntegrationError on API failure."""

    async def test_non_2xx_raises_integration_error(self) -> None:
        """Non-2xx response raises IntegrationError."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        client = _mock_client(
            get=AsyncMock(return_value=_json_response(404, text="Not Found")),
        )

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.get_pr_state(42)


# ---------------------------------------------------------------------------
# TS-06-E3: get_pr_state raises KeyError/IntegrationError on missing fields.
#
# Requirement: 06-REQ-4.E1
# ---------------------------------------------------------------------------


class TestGetPrStateMissingFields:
    """TS-06-E3: Missing fields raise KeyError or IntegrationError."""

    async def test_missing_head_field_raises(self) -> None:
        """Response missing 'head' field raises KeyError or IntegrationError."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        # Response has number, state, merged but no 'head'
        incomplete = {"number": 42, "state": "open", "merged": False}

        client = _mock_client(
            get=AsyncMock(return_value=_json_response(200, incomplete)),
        )

        with patch(_TARGET, return_value=client):
            with pytest.raises((KeyError, IntegrationError)):
                await platform.get_pr_state(42)

    async def test_missing_state_field_raises(self) -> None:
        """Response missing 'state' field raises KeyError or IntegrationError."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        incomplete = {
            "number": 42,
            "merged": False,
            "head": {"sha": "abc123"},
        }

        client = _mock_client(
            get=AsyncMock(return_value=_json_response(200, incomplete)),
        )

        with patch(_TARGET, return_value=client):
            with pytest.raises((KeyError, IntegrationError)):
                await platform.get_pr_state(42)

    async def test_missing_merged_field_raises(self) -> None:
        """Response missing 'merged' field raises KeyError or IntegrationError."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        incomplete = {
            "number": 42,
            "state": "open",
            "head": {"sha": "abc123"},
        }

        client = _mock_client(
            get=AsyncMock(return_value=_json_response(200, incomplete)),
        )

        with patch(_TARGET, return_value=client):
            with pytest.raises((KeyError, IntegrationError)):
                await platform.get_pr_state(42)


# ---------------------------------------------------------------------------
# TS-06-E4: get_pr_state raises IntegrationError after timeout without hanging.
#
# Requirement: 06-REQ-4.E2
# ---------------------------------------------------------------------------


class TestGetPrStateTimeout:
    """TS-06-E4: Timeout raises IntegrationError, does not hang."""

    async def test_timeout_raises_integration_error(self) -> None:
        """Simulated timeout raises IntegrationError via _request() delegation."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        # Simulate _request() raising IntegrationError after retries exhausted
        mock_get = AsyncMock(
            side_effect=IntegrationError("timeout after retries"),
        )
        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            with patch(_SLEEP_TARGET, new_callable=AsyncMock):
                with pytest.raises(IntegrationError):
                    await platform.get_pr_state(42)


# ---------------------------------------------------------------------------
# TS-06-16: get_pr_checks fetches head SHA via get_pr_state then calls
#           check-runs endpoint.
#
# Requirement: 06-REQ-5.1
# ---------------------------------------------------------------------------


class TestGetPrChecksHappyPath:
    """TS-06-16: get_pr_checks fetches head SHA and maps check-run results."""

    async def test_returns_check_result_list(self) -> None:
        """get_pr_checks returns list[CheckResult] with correct mappings."""
        from afissues.protocol import CheckResult, PrState

        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        # Mock get_pr_state to return a PrState with known head_sha
        platform.get_pr_state = AsyncMock(  # type: ignore[method-assign]
            return_value=PrState(
                number=42, state="open", merged=False, head_sha="deadbeef"
            ),
        )

        check_runs_response = {
            "total_count": 1,
            "check_runs": [
                {
                    "name": "test",
                    "status": "completed",
                    "conclusion": "success",
                    "output": {"title": "OK", "summary": "pass"},
                },
            ],
        }

        async def mock_get(url: str, **kw: Any) -> MagicMock:
            return _json_response(200, check_runs_response)

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            results = await platform.get_pr_checks(42)

        assert len(results) == 1
        assert isinstance(results[0], CheckResult)
        assert results[0].name == "test"
        assert results[0].status == "completed"
        assert results[0].conclusion == "success"
        assert results[0].output_title == "OK"
        assert results[0].output_summary == "pass"

    async def test_calls_check_runs_with_head_sha(self) -> None:
        """GET request to check-runs uses head_sha from get_pr_state."""
        from afissues.protocol import PrState

        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        platform.get_pr_state = AsyncMock(  # type: ignore[method-assign]
            return_value=PrState(
                number=42, state="open", merged=False, head_sha="deadbeef"
            ),
        )

        captured_urls: list[str] = []

        async def mock_get(url: str, **kw: Any) -> MagicMock:
            captured_urls.append(url)
            return _json_response(
                200,
                {"total_count": 0, "check_runs": []},
            )

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            await platform.get_pr_checks(42)

        # Should have called the check-runs endpoint with the head_sha
        check_run_urls = [
            u for u in captured_urls if "check-runs" in u
        ]
        assert len(check_run_urls) >= 1
        assert "/repos/owner/repo/commits/deadbeef/check-runs" in check_run_urls[0]


# ---------------------------------------------------------------------------
# TS-06-17: get_pr_checks paginates and accumulates all check runs.
#
# Requirement: 06-REQ-5.2
# ---------------------------------------------------------------------------


class TestGetPrChecksPagination:
    """TS-06-17: Pagination accumulates all check runs across pages."""

    async def test_paginates_two_pages(self) -> None:
        """total_count=35 results in two page requests returning all 35 results."""
        from afissues.protocol import PrState

        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        platform.get_pr_state = AsyncMock(  # type: ignore[method-assign]
            return_value=PrState(
                number=42, state="open", merged=False, head_sha="abc"
            ),
        )

        page1_runs = [_make_check_run(f"run-{i}") for i in range(30)]
        page2_runs = [_make_check_run(f"run-{i}") for i in range(30, 35)]

        call_count = 0

        async def mock_get(url: str, **kw: Any) -> MagicMock:
            nonlocal call_count
            call_count += 1
            if call_count == 1:
                return _json_response(
                    200, {"total_count": 35, "check_runs": page1_runs}
                )
            return _json_response(
                200, {"total_count": 35, "check_runs": page2_runs}
            )

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            results = await platform.get_pr_checks(42)

        assert len(results) == 35
        assert call_count >= 2


# ---------------------------------------------------------------------------
# TS-06-18: get_pr_checks sets output_title='' and output_summary='' when
#           output is null.
#
# Requirement: 06-REQ-5.3
# ---------------------------------------------------------------------------


class TestGetPrChecksNullOutput:
    """TS-06-18: Null output maps to empty strings on CheckResult."""

    async def test_null_output_produces_empty_strings(self) -> None:
        """Check run with output: null has output_title='' and output_summary=''."""
        from afissues.protocol import PrState

        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        platform.get_pr_state = AsyncMock(  # type: ignore[method-assign]
            return_value=PrState(
                number=42, state="open", merged=False, head_sha="abc"
            ),
        )

        response_data = {
            "total_count": 1,
            "check_runs": [
                {
                    "name": "lint",
                    "status": "queued",
                    "conclusion": None,
                    "output": None,
                },
            ],
        }

        client = _mock_client(
            get=AsyncMock(return_value=_json_response(200, response_data)),
        )

        with patch(_TARGET, return_value=client):
            results = await platform.get_pr_checks(42)

        assert results[0].output_title == ""
        assert results[0].output_summary == ""
        assert isinstance(results[0].output_title, str)
        assert isinstance(results[0].output_summary, str)


# ---------------------------------------------------------------------------
# TS-06-E5: get_pr_checks raises on missing check_runs or total_count.
#
# Requirement: 06-REQ-5.E1
# ---------------------------------------------------------------------------


class TestGetPrChecksMissingFields:
    """TS-06-E5: Missing response fields raise KeyError or IntegrationError."""

    async def test_empty_response_body_raises(self) -> None:
        """Response body {} (missing check_runs and total_count) raises."""
        from afissues.protocol import PrState

        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        platform.get_pr_state = AsyncMock(  # type: ignore[method-assign]
            return_value=PrState(
                number=42, state="open", merged=False, head_sha="abc"
            ),
        )

        client = _mock_client(
            get=AsyncMock(return_value=_json_response(200, {})),
        )

        with patch(_TARGET, return_value=client):
            with pytest.raises((KeyError, IntegrationError)):
                await platform.get_pr_checks(42)


# ---------------------------------------------------------------------------
# TS-06-E6: get_pr_checks raises IntegrationError on pagination failure.
#
# Requirement: 06-REQ-5.E2
# ---------------------------------------------------------------------------


class TestGetPrChecksPaginationError:
    """TS-06-E6: Pagination failure raises IntegrationError, no partial list."""

    async def test_page2_error_raises_integration_error(self) -> None:
        """Page 1 succeeds, page 2 raises IntegrationError; no partial result."""
        from afissues.protocol import PrState

        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        platform.get_pr_state = AsyncMock(  # type: ignore[method-assign]
            return_value=PrState(
                number=42, state="open", merged=False, head_sha="abc"
            ),
        )

        page1_runs = [_make_check_run(f"run-{i}") for i in range(30)]
        page1 = _json_response(
            200, {"total_count": 35, "check_runs": page1_runs}
        )

        call_count = 0

        async def mock_get(url: str, **kw: Any) -> MagicMock:
            nonlocal call_count
            call_count += 1
            if call_count == 1:
                return page1
            raise IntegrationError("500 on page 2")

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            with patch(_SLEEP_TARGET, new_callable=AsyncMock):
                with pytest.raises(IntegrationError):
                    await platform.get_pr_checks(42)


# ---------------------------------------------------------------------------
# TS-06-E7: get_pr_checks caps at 10 pages (300 check runs).
#
# Requirement: 06-REQ-5.E3
# ---------------------------------------------------------------------------


class TestGetPrChecksPageCap:
    """TS-06-E7: Safety cap at 10 pages returns at most 300 check runs."""

    async def test_caps_at_300_check_runs(self) -> None:
        """Method terminates with at most 300 runs when total_count=999."""
        from afissues.protocol import PrState

        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        platform.get_pr_state = AsyncMock(  # type: ignore[method-assign]
            return_value=PrState(
                number=42, state="open", merged=False, head_sha="abc"
            ),
        )

        page_data = {
            "total_count": 999,
            "check_runs": [_make_check_run(f"run-{i}") for i in range(30)],
        }

        check_run_request_count = 0

        async def mock_get(url: str, **kw: Any) -> MagicMock:
            nonlocal check_run_request_count
            if "check-runs" in url:
                check_run_request_count += 1
            return _json_response(200, page_data)

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            results = await platform.get_pr_checks(42)

        # At most 300 check runs (10 pages * 30 per page)
        assert len(results) <= 300
        # At most 10 page requests for check-runs
        assert check_run_request_count <= 10


# ---------------------------------------------------------------------------
# TS-06-19: get_pr_reviews calls GET /repos/{owner}/{repo}/pulls/42/reviews
#           and maps each review to a ReviewComment in submission order.
#
# Requirement: 06-REQ-6.1
# ---------------------------------------------------------------------------


class TestGetPrReviewsHappyPath:
    """TS-06-19: get_pr_reviews returns ordered list of ReviewComment."""

    async def test_returns_review_comments_in_order(self) -> None:
        """get_pr_reviews maps API reviews to ReviewComment in order."""
        from afissues.protocol import ReviewComment

        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        api_reviews = [
            {
                "user": {"login": "alice"},
                "state": "APPROVED",
                "body": "LGTM",
                "submitted_at": "2026-07-26T09:00:00Z",
            },
            {
                "user": {"login": "bob"},
                "state": "CHANGES_REQUESTED",
                "body": "needs work",
                "submitted_at": "2026-07-26T10:00:00Z",
            },
        ]

        async def mock_get(url: str, **kw: Any) -> MagicMock:
            return _json_response(200, api_reviews)

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            results = await platform.get_pr_reviews(42)

        assert len(results) == 2
        assert results[0] == ReviewComment(
            user="alice",
            state="APPROVED",
            body="LGTM",
            submitted_at="2026-07-26T09:00:00Z",
        )
        assert results[1].user == "bob"
        assert results[1].state == "CHANGES_REQUESTED"

    async def test_calls_correct_api_endpoint(self) -> None:
        """GET request is sent to /repos/{owner}/{repo}/pulls/42/reviews."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        captured_urls: list[str] = []

        async def mock_get(url: str, **kw: Any) -> MagicMock:
            captured_urls.append(url)
            return _json_response(200, [])

        client = _mock_client(get=mock_get)

        with patch(_TARGET, return_value=client):
            await platform.get_pr_reviews(42)

        assert len(captured_urls) == 1
        assert "/repos/owner/repo/pulls/42/reviews" in captured_urls[0]


# ---------------------------------------------------------------------------
# TS-06-20: get_pr_reviews returns all review states unfiltered.
#
# Requirement: 06-REQ-6.2
# ---------------------------------------------------------------------------


class TestGetPrReviewsUnfiltered:
    """TS-06-20: All review states returned without filtering."""

    async def test_all_states_preserved_in_order(self) -> None:
        """APPROVED, DISMISSED, CHANGES_REQUESTED, COMMENTED all returned."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        states = ["APPROVED", "DISMISSED", "CHANGES_REQUESTED", "COMMENTED"]
        mock_reviews = [
            {
                "user": {"login": f"u{i}"},
                "state": s,
                "body": "",
                "submitted_at": "2026-07-26T00:00:00Z",
            }
            for i, s in enumerate(states)
        ]

        client = _mock_client(
            get=AsyncMock(return_value=_json_response(200, mock_reviews)),
        )

        with patch(_TARGET, return_value=client):
            results = await platform.get_pr_reviews(42)

        assert [r.state for r in results] == states


# ---------------------------------------------------------------------------
# TS-06-E8: get_pr_reviews raises on missing review fields.
#
# Requirement: 06-REQ-6.E1
# ---------------------------------------------------------------------------


class TestGetPrReviewsMissingFields:
    """TS-06-E8: Missing review fields raise KeyError or IntegrationError."""

    async def test_missing_state_field_raises(self) -> None:
        """Review missing 'state' raises KeyError or IntegrationError."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        # Review is missing 'state'
        incomplete_reviews = [
            {
                "user": {"login": "alice"},
                "body": "ok",
                "submitted_at": "2026-01-01T00:00:00Z",
            },
        ]

        client = _mock_client(
            get=AsyncMock(
                return_value=_json_response(200, incomplete_reviews)
            ),
        )

        with patch(_TARGET, return_value=client):
            with pytest.raises((KeyError, IntegrationError)):
                await platform.get_pr_reviews(42)

    async def test_missing_user_login_raises(self) -> None:
        """Review missing 'user.login' raises KeyError or IntegrationError."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        # Review has 'user' but no 'login' inside
        incomplete_reviews = [
            {
                "user": {},
                "state": "APPROVED",
                "body": "ok",
                "submitted_at": "2026-01-01T00:00:00Z",
            },
        ]

        client = _mock_client(
            get=AsyncMock(
                return_value=_json_response(200, incomplete_reviews)
            ),
        )

        with patch(_TARGET, return_value=client):
            with pytest.raises((KeyError, IntegrationError)):
                await platform.get_pr_reviews(42)


# ---------------------------------------------------------------------------
# TS-06-E9: get_pr_reviews raises IntegrationError on non-2xx.
#
# Requirement: 06-REQ-6.E2
# ---------------------------------------------------------------------------


class TestGetPrReviewsApiError:
    """TS-06-E9: Non-2xx response raises IntegrationError."""

    async def test_403_raises_integration_error(self) -> None:
        """Non-2xx (403 Forbidden) raises IntegrationError."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        client = _mock_client(
            get=AsyncMock(
                return_value=_json_response(403, text="Forbidden")
            ),
        )

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.get_pr_reviews(42)


# ---------------------------------------------------------------------------
# TS-06-21: PlatformProtocol.create_pr() return annotation is PrResult.
#
# Requirement: 06-REQ-7.1
# ---------------------------------------------------------------------------


class TestCreatePrReturnAnnotation:
    """TS-06-21: create_pr() return type annotation resolves to PrResult."""

    def test_protocol_return_type_is_pr_result(self) -> None:
        """PlatformProtocol.create_pr return annotation is PrResult."""
        from afissues.protocol import PlatformProtocol, PrResult

        hints = get_type_hints(PlatformProtocol.create_pr)
        assert hints["return"] is PrResult


# ---------------------------------------------------------------------------
# TS-06-22: GitHubPlatform.create_pr() returns PrResult on 201 success.
#
# Requirement: 06-REQ-7.2
# ---------------------------------------------------------------------------


class TestCreatePrReturnsPrResult:
    """TS-06-22: create_pr returns PrResult with html_url and number from 201."""

    async def test_201_returns_pr_result(self) -> None:
        """Successful 201 returns PrResult(html_url=..., number=...)."""
        from afissues.protocol import PrResult

        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        resp_data = {
            "html_url": "https://github.com/owner/repo/pull/99",
            "number": 99,
        }

        async def mock_post(url: str, **kw: Any) -> MagicMock:
            return _json_response(201, resp_data)

        client = _mock_client(post=mock_post)

        with patch(_TARGET, return_value=client):
            result = await platform.create_pr(
                title="fix", body="", head="fix-branch", base="main"
            )

        assert isinstance(result, PrResult)
        assert result.html_url == "https://github.com/owner/repo/pull/99"
        assert result.number == 99


# ---------------------------------------------------------------------------
# TS-06-23: create_pr() 422 recovery returns PrResult from existing PR.
#
# Requirement: 06-REQ-7.3
# ---------------------------------------------------------------------------


class TestCreatePr422RecoveryPrResult:
    """TS-06-23: 422 recovery returns PrResult with number from existing PR."""

    async def test_422_recovery_returns_pr_result(self) -> None:
        """Duplicate 422 + lookup returns PrResult with html_url and number."""
        from afissues.protocol import PrResult

        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        post_resp = _json_response(
            422,
            {
                "message": "Validation Failed",
                "errors": [
                    {
                        "message": "A pull request already exists for owner:fix-branch.",
                    },
                ],
            },
        )

        existing_pr = [
            {
                "html_url": "https://github.com/owner/repo/pull/7",
                "number": 7,
            },
        ]

        call_count = 0

        async def mock_post(url: str, **kw: Any) -> MagicMock:
            return post_resp

        async def mock_get(url: str, **kw: Any) -> MagicMock:
            nonlocal call_count
            call_count += 1
            return _json_response(200, existing_pr)

        client = _mock_client(post=mock_post, get=mock_get)

        with patch(_TARGET, return_value=client):
            result = await platform.create_pr(
                title="fix", body="", head="fix-branch", base="main"
            )

        assert isinstance(result, PrResult)
        assert result.number == 7
        assert result.html_url == "https://github.com/owner/repo/pull/7"


# ---------------------------------------------------------------------------
# TS-06-E10: create_pr() raises IntegrationError when 422 recovery list is empty.
#
# Requirement: 06-REQ-7.E1
# ---------------------------------------------------------------------------


class TestCreatePr422EmptyRecovery:
    """TS-06-E10: Empty 422 recovery list raises IntegrationError."""

    async def test_empty_recovery_list_raises(self) -> None:
        """422 duplicate with empty GET result raises IntegrationError."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        post_resp = _json_response(
            422,
            {
                "message": "Validation Failed",
                "errors": [
                    {"message": "A pull request already exists"},
                ],
            },
        )

        async def mock_post(url: str, **kw: Any) -> MagicMock:
            return post_resp

        async def mock_get(url: str, **kw: Any) -> MagicMock:
            return _json_response(200, [])

        client = _mock_client(post=mock_post, get=mock_get)

        with patch(_TARGET, return_value=client):
            with pytest.raises(IntegrationError):
                await platform.create_pr(
                    title="fix", body="", head="branch", base="main"
                )


# ---------------------------------------------------------------------------
# TS-06-E11: create_pr() raises on 201 body missing number.
#
# Requirement: 06-REQ-7.E2
# ---------------------------------------------------------------------------


class TestCreatePr201MissingNumber:
    """TS-06-E11: 201 response missing 'number' raises KeyError/IntegrationError."""

    async def test_missing_number_raises(self) -> None:
        """201 response without 'number' field raises."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        # Missing 'number' field
        resp_data = {"html_url": "https://github.com/owner/repo/pull/1"}

        client = _mock_client(
            post=AsyncMock(return_value=_json_response(201, resp_data)),
        )

        with patch(_TARGET, return_value=client):
            with pytest.raises((KeyError, IntegrationError)):
                await platform.create_pr(
                    title="fix", body="", head="branch", base="main"
                )

    async def test_missing_html_url_raises(self) -> None:
        """201 response without 'html_url' field raises."""
        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        # Missing 'html_url' field
        resp_data = {"number": 1}

        client = _mock_client(
            post=AsyncMock(return_value=_json_response(201, resp_data)),
        )

        with patch(_TARGET, return_value=client):
            with pytest.raises((KeyError, IntegrationError)):
                await platform.create_pr(
                    title="fix", body="", head="branch", base="main"
                )
