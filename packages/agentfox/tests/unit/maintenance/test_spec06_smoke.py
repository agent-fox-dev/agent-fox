"""Smoke tests for spec 06: PR lifecycle end-to-end verification.

Task group 12 — smoke tests for:
  - TS-06-SMOKE-1: Happy path: fix pipeline creates PR, applies af:pr label,
    posts tracking comment, and leaves the issue open.
  - TS-06-SMOKE-2: Idempotent retry: _handle_result with 'pr_created' when
    af:pr is already on the issue.
  - TS-06-SMOKE-3: Protocol consumer queries PR state, checks, and reviews
    via GitHubPlatform with mocked HTTP responses.
  - TS-06-SMOKE-4: Bootstrap ensures af:pr label is created from REQUIRED_LABELS.

Execution paths: 06-PATH-1, 06-PATH-2, 06-PATH-3, 06-PATH-4

Requirements: 06-REQ-8.1, 06-REQ-9.1, 06-REQ-9.2, 06-REQ-9.3,
              06-REQ-10.4, 06-REQ-4.1, 06-REQ-5.1, 06-REQ-6.1,
              06-REQ-1.1, 06-REQ-1.2, 06-REQ-1.3
"""

from __future__ import annotations

import dataclasses
from pathlib import Path
from typing import Any
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from afissues.errors import IntegrationError
from afissues.protocol import IssueResult

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_fix_pipeline(
    merge_strategy: str = "pr",
    platform: object | None = None,
) -> object:
    """Create a FixPipeline with the specified merge_strategy config."""
    from agentfox.core.config import AgentFoxConfig, WorkspaceConfig
    from agentfox.maintenance.fix_pipeline import FixPipeline

    config = AgentFoxConfig(
        workspace=WorkspaceConfig(
            merge_strategy=merge_strategy,
            integration_branch="main",
        ),
    )
    if platform is None:
        platform = MagicMock()
    return FixPipeline(
        config=config,
        platform=platform,
    )


def _make_mock_platform(
    *,
    pr_number: int = 42,
    pr_html_url: str = "https://github.com/owner/repo/pull/42",
) -> MagicMock:
    """Create a mock platform with create_pr returning PrResult."""
    from afissues.protocol import PrResult

    platform = MagicMock()
    platform._owner = "owner"
    platform._repo = "repo"
    platform.create_pr = AsyncMock(
        return_value=PrResult(html_url=pr_html_url, number=pr_number),
    )
    platform.add_issue_comment = AsyncMock()
    platform.close_issue = AsyncMock()
    platform.assign_label = AsyncMock()
    platform.remove_label = AsyncMock()
    return platform


def _make_issue(
    number: int = 42,
    title: str = "Login fails on empty password",
    labels: tuple[str, ...] = (),
) -> IssueResult:
    return IssueResult(
        number=number,
        title=title,
        html_url=f"https://github.com/test/repo/issues/{number}",
        labels=labels,
    )


def _make_spec(issue_number: int = 42) -> object:
    from agentfox.maintenance.spec_builder import InMemorySpec

    return InMemorySpec(
        issue_number=issue_number,
        title="Login fails on empty password",
        task_prompt="Fix the bug",
        system_context="Bug context",
        branch_name="fix/test-branch",
    )


def _make_workspace() -> object:
    from agentfox.workspace import WorkspaceInfo

    return WorkspaceInfo(
        path=Path("/tmp/test-worktree"),
        branch="fix/test-branch",
        spec_name="fix-issue-42",
        task_group=0,
    )


# ---------------------------------------------------------------------------
# TS-06-SMOKE-1: End-to-end happy path — fix pipeline creates PR, applies
#                af:pr label, posts tracking comment, and leaves issue open.
#
# Execution Path: 06-PATH-1
# Requirements: 06-REQ-8.1, 06-REQ-9.1, 06-REQ-10.4
# ---------------------------------------------------------------------------


class TestSmokeHappyPath:
    """TS-06-SMOKE-1: End-to-end happy path for PR mode."""

    @pytest.mark.asyncio
    async def test_integrate_fix_returns_pr_created(self) -> None:
        """_integrate_fix returns ('pr_created', [...]) and sets _pr_number."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)

        with (
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.maintenance.fix_pipeline._workspace_git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
            patch(
                "agentfox.maintenance.fix_pipeline._workspace_git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["src/auth.py"],
            ),
        ):
            status, files = await pipeline._integrate_fix(
                _make_issue(),
                _make_spec(),
                _make_workspace(),
            )

        assert status == "pr_created"
        assert files == ["src/auth.py"]
        assert pipeline._pr_number == 42

    @pytest.mark.asyncio
    async def test_handle_result_applies_af_pr_label(self) -> None:
        """_handle_result('pr_created') applies af:pr label."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42
        pipeline._pr_url = "https://github.com/owner/repo/pull/42"

        await pipeline._handle_result(_make_issue(), _make_spec(), "pr_created")

        label_calls = [call.args[1] for call in mock_platform.assign_label.call_args_list]
        assert "af:pr" in label_calls

    @pytest.mark.asyncio
    async def test_handle_result_removes_af_fix_label(self) -> None:
        """_handle_result('pr_created') removes af:fix label."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42
        pipeline._pr_url = "https://github.com/owner/repo/pull/42"

        await pipeline._handle_result(_make_issue(), _make_spec(), "pr_created")

        remove_calls = [call.args[1] for call in mock_platform.remove_label.call_args_list]
        assert "af:fix" in remove_calls

    @pytest.mark.asyncio
    async def test_handle_result_posts_tracking_comment(self) -> None:
        """_handle_result posts comment with af:pr-tracking tag."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42
        pipeline._pr_url = "https://github.com/owner/repo/pull/42"

        await pipeline._handle_result(_make_issue(), _make_spec(), "pr_created")

        comment_bodies = [call.args[1] for call in mock_platform.add_issue_comment.call_args_list]
        tracking_found = any("<!-- af:pr-tracking pr_number=42 attempt=1 -->" in body for body in comment_bodies)
        assert tracking_found, f"No tracking comment found. Bodies: {comment_bodies}"

    @pytest.mark.asyncio
    async def test_handle_result_does_not_close_issue(self) -> None:
        """close_issue is never called for pr_created status."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42
        pipeline._pr_url = "https://github.com/owner/repo/pull/42"

        await pipeline._handle_result(_make_issue(), _make_spec(), "pr_created")

        mock_platform.close_issue.assert_not_awaited()

    @pytest.mark.asyncio
    async def test_handle_result_does_not_apply_af_fixed(self) -> None:
        """af:fixed label is never applied for pr_created status."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42
        pipeline._pr_url = "https://github.com/owner/repo/pull/42"

        await pipeline._handle_result(_make_issue(), _make_spec(), "pr_created")

        label_calls = [call.args[1] for call in mock_platform.assign_label.call_args_list]
        assert "af:fixed" not in label_calls


# ---------------------------------------------------------------------------
# TS-06-SMOKE-2: Idempotent retry — _handle_result with 'pr_created' when
#                af:pr is already present on the issue.
#
# Execution Path: 06-PATH-2
# Requirements: 06-REQ-9.2
# ---------------------------------------------------------------------------


class TestSmokeIdempotentRetry:
    """TS-06-SMOKE-2: Retry with af:pr already present does not raise."""

    @pytest.mark.asyncio
    async def test_no_exception_with_af_pr_present(self) -> None:
        """No exception raised when af:pr is already on the issue."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42
        pipeline._pr_url = "https://github.com/owner/repo/pull/42"

        issue = _make_issue(labels=("af:pr", "af:fix"))

        # Must not raise
        await pipeline._handle_result(issue, _make_spec(), "pr_created")

    @pytest.mark.asyncio
    async def test_new_tracking_comment_posted_on_retry(self) -> None:
        """A new tracking comment is posted even on retry."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42
        pipeline._pr_url = "https://github.com/owner/repo/pull/42"

        issue = _make_issue(labels=("af:pr",))

        await pipeline._handle_result(issue, _make_spec(), "pr_created")

        assert mock_platform.add_issue_comment.call_count >= 1

    @pytest.mark.asyncio
    async def test_close_issue_never_called_on_retry(self) -> None:
        """close_issue is never called on retry."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42
        pipeline._pr_url = "https://github.com/owner/repo/pull/42"

        issue = _make_issue(labels=("af:pr",))

        await pipeline._handle_result(issue, _make_spec(), "pr_created")

        mock_platform.close_issue.assert_not_awaited()

    @pytest.mark.asyncio
    async def test_af_fixed_never_applied_on_retry(self) -> None:
        """af:fixed label is never applied on retry."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42
        pipeline._pr_url = "https://github.com/owner/repo/pull/42"

        issue = _make_issue(labels=("af:pr",))

        await pipeline._handle_result(issue, _make_spec(), "pr_created")

        label_calls = [call.args[1] for call in mock_platform.assign_label.call_args_list]
        assert "af:fixed" not in label_calls


# ---------------------------------------------------------------------------
# TS-06-SMOKE-3: Protocol consumer queries PR state, checks, and reviews via
#                GitHubPlatform with mocked HTTP responses, receiving typed
#                frozen dataclasses.
#
# Execution Path: 06-PATH-3
# Requirements: 06-REQ-4.1, 06-REQ-5.1, 06-REQ-6.1
# ---------------------------------------------------------------------------


_HTTP_CLIENT_TARGET = "afissues._http.httpx.AsyncClient"


def _json_response(status_code: int, json_data: Any = None) -> MagicMock:
    """Build a mock httpx response."""
    resp = MagicMock()
    resp.status_code = status_code
    resp.text = ""
    if json_data is not None:
        resp.json.return_value = json_data
    return resp


def _mock_client(**method_responses: Any) -> AsyncMock:
    """Build a mock httpx.AsyncClient with specified method responses."""
    client = AsyncMock()
    for method_name, response in method_responses.items():
        if callable(response) and not isinstance(response, MagicMock):
            setattr(client, method_name, response)
        else:
            setattr(client, method_name, AsyncMock(return_value=response))
    client.__aenter__ = AsyncMock(return_value=client)
    client.__aexit__ = AsyncMock(return_value=False)
    return client


class TestSmokeProtocolConsumer:
    """TS-06-SMOKE-3: PR state, checks, reviews return frozen dataclasses."""

    @pytest.mark.asyncio
    async def test_get_pr_state_returns_frozen_dataclass(self) -> None:
        """get_pr_state returns a frozen PrState dataclass."""
        from afissues.github import GitHubPlatform
        from afissues.protocol import PrState

        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        api_data = {
            "number": 42,
            "state": "open",
            "merged": False,
            "head": {"sha": "abc123def456"},
        }

        client = _mock_client(
            get=AsyncMock(return_value=_json_response(200, api_data)),
        )

        with patch(_HTTP_CLIENT_TARGET, return_value=client):
            result = await platform.get_pr_state(42)

        assert isinstance(result, PrState)
        assert result.number == 42
        assert result.state == "open"
        assert result.merged is False
        assert result.head_sha == "abc123def456"
        # Verify frozen
        with pytest.raises(dataclasses.FrozenInstanceError):
            result.state = "closed"  # type: ignore[misc]

    @pytest.mark.asyncio
    async def test_get_pr_checks_returns_frozen_check_results(self) -> None:
        """get_pr_checks returns list[CheckResult] with str output fields."""
        from afissues.github import GitHubPlatform
        from afissues.protocol import CheckResult, PrState

        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        # Mock get_pr_state
        platform.get_pr_state = AsyncMock(  # type: ignore[method-assign]
            return_value=PrState(
                number=42,
                state="open",
                merged=False,
                head_sha="deadbeef",
            ),
        )

        checks_data = {
            "total_count": 2,
            "check_runs": [
                {
                    "name": "test-suite",
                    "status": "completed",
                    "conclusion": "success",
                    "output": {"title": "Tests passed", "summary": "All 42 tests pass"},
                },
                {
                    "name": "lint",
                    "status": "completed",
                    "conclusion": "failure",
                    "output": None,
                },
            ],
        }

        client = _mock_client(
            get=AsyncMock(return_value=_json_response(200, checks_data)),
        )

        with patch(_HTTP_CLIENT_TARGET, return_value=client):
            results = await platform.get_pr_checks(42)

        assert len(results) == 2
        assert all(isinstance(r, CheckResult) for r in results)

        # First check: has real output
        assert results[0].name == "test-suite"
        assert results[0].output_title == "Tests passed"
        assert results[0].output_summary == "All 42 tests pass"

        # Second check: null output → empty strings
        assert results[1].name == "lint"
        assert results[1].output_title == ""
        assert results[1].output_summary == ""
        assert isinstance(results[1].output_title, str)
        assert isinstance(results[1].output_summary, str)

        # Verify frozen
        with pytest.raises(dataclasses.FrozenInstanceError):
            results[0].name = "mutated"  # type: ignore[misc]

    @pytest.mark.asyncio
    async def test_get_pr_reviews_returns_frozen_review_comments(self) -> None:
        """get_pr_reviews returns list[ReviewComment] in submission order."""
        from afissues.github import GitHubPlatform
        from afissues.protocol import ReviewComment

        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        reviews_data = [
            {
                "user": {"login": "alice"},
                "state": "APPROVED",
                "body": "LGTM",
                "submitted_at": "2026-07-26T09:00:00Z",
            },
            {
                "user": {"login": "bob"},
                "state": "CHANGES_REQUESTED",
                "body": "Needs tests",
                "submitted_at": "2026-07-26T10:00:00Z",
            },
        ]

        client = _mock_client(
            get=AsyncMock(return_value=_json_response(200, reviews_data)),
        )

        with patch(_HTTP_CLIENT_TARGET, return_value=client):
            results = await platform.get_pr_reviews(42)

        assert len(results) == 2
        assert all(isinstance(r, ReviewComment) for r in results)

        # Submission order preserved
        assert results[0].user == "alice"
        assert results[0].state == "APPROVED"
        assert results[1].user == "bob"
        assert results[1].state == "CHANGES_REQUESTED"

        # Verify frozen
        with pytest.raises(dataclasses.FrozenInstanceError):
            results[0].user = "mutated"  # type: ignore[misc]

    @pytest.mark.asyncio
    async def test_no_raw_dicts_exposed(self) -> None:
        """All returned objects are dataclass instances, not raw dicts."""
        from afissues.github import GitHubPlatform

        platform = GitHubPlatform(owner="owner", repo="repo", token="tok")

        pr_data = {
            "number": 42,
            "state": "open",
            "merged": False,
            "head": {"sha": "abc123"},
        }

        client = _mock_client(
            get=AsyncMock(return_value=_json_response(200, pr_data)),
        )

        with patch(_HTTP_CLIENT_TARGET, return_value=client):
            result = await platform.get_pr_state(42)

        assert not isinstance(result, dict)
        assert dataclasses.is_dataclass(result)


# ---------------------------------------------------------------------------
# TS-06-SMOKE-4: Bootstrap ensures af:pr label is created in the repository
#                if absent, using REQUIRED_LABELS from afissues.labels.
#
# Execution Path: 06-PATH-4
# Requirements: 06-REQ-1.1, 06-REQ-1.2, 06-REQ-1.3
# ---------------------------------------------------------------------------


class TestSmokeBootstrapLabel:
    """TS-06-SMOKE-4: Bootstrap creates af:pr label from REQUIRED_LABELS."""

    def test_required_labels_contains_af_pr(self) -> None:
        """REQUIRED_LABELS includes af:pr with correct color and description."""
        from afissues.labels import LABEL_PR, REQUIRED_LABELS

        pr_specs = [s for s in REQUIRED_LABELS if s.name == LABEL_PR]
        assert len(pr_specs) == 1

        pr_spec = pr_specs[0]
        assert pr_spec.name == "af:pr"
        assert pr_spec.color == "#1d76db"
        assert pr_spec.description == "Pull request created — awaiting merge"

    def test_label_pr_constant_value(self) -> None:
        """LABEL_PR constant is 'af:pr'."""
        from afissues.labels import LABEL_PR

        assert LABEL_PR == "af:pr"

    @pytest.mark.asyncio
    async def test_bootstrap_calls_create_label_for_af_pr(
        self,
        tmp_path: Path,
    ) -> None:
        """Bootstrap calls create_label with af:pr parameters."""
        from agentfox.workspace.init_project import _ensure_platform_labels_async

        mock_platform = AsyncMock()
        mock_platform.create_label = AsyncMock()

        # _ensure_platform_labels_async does local imports, so patch at source
        with (
            patch(
                "agentfox.core.config.load_config",
                return_value=MagicMock(),
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
        ):
            count = await _ensure_platform_labels_async(tmp_path)

        # At least one label created (all labels in REQUIRED_LABELS)
        assert count > 0

        # Find the create_label call for af:pr
        create_label_calls = mock_platform.create_label.call_args_list
        af_pr_calls = [call for call in create_label_calls if call.args[0] == "af:pr"]
        assert len(af_pr_calls) == 1

        af_pr_call = af_pr_calls[0]
        assert af_pr_call.args[0] == "af:pr"
        assert af_pr_call.args[1] == "#1d76db"
        assert af_pr_call.args[2] == "Pull request created — awaiting merge"

    @pytest.mark.asyncio
    async def test_bootstrap_completes_without_error(
        self,
        tmp_path: Path,
    ) -> None:
        """Bootstrap completes without error when platform succeeds."""
        from agentfox.workspace.init_project import _ensure_platform_labels_async

        mock_platform = AsyncMock()
        mock_platform.create_label = AsyncMock()

        with (
            patch(
                "agentfox.core.config.load_config",
                return_value=MagicMock(),
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
        ):
            # Must not raise
            count = await _ensure_platform_labels_async(tmp_path)

        # All labels should have been created
        from afissues.labels import REQUIRED_LABELS

        assert count == len(REQUIRED_LABELS)

    @pytest.mark.asyncio
    async def test_bootstrap_propagates_integration_error(
        self,
        tmp_path: Path,
    ) -> None:
        """Bootstrap propagates IntegrationError when label creation fails."""
        from agentfox.workspace.init_project import _ensure_platform_labels_async

        mock_platform = AsyncMock()
        mock_platform.create_label = AsyncMock(
            side_effect=IntegrationError("API error"),
        )

        with (
            patch(
                "agentfox.core.config.load_config",
                return_value=MagicMock(),
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
        ):
            with pytest.raises(IntegrationError):
                await _ensure_platform_labels_async(tmp_path)
