"""Tests for spec 06: premature-close bug fix, _handle_result, and tracking comment utilities.

Task group 3 — failing tests for:
  - TS-06-26 through TS-06-29, TS-06-E12: _integrate_fix returns pr_created
    status and _pr_number propagation (subtask 3.1)
  - TS-06-30 through TS-06-32, TS-06-E13, TS-06-E14: _handle_result with
    pr_created status — premature-close regression (subtask 3.2)
  - TS-06-33 through TS-06-37, TS-06-E15, TS-06-E16: tracking comment
    utilities and _handle_result comment posting (subtask 3.3)

Requirements: 06-REQ-8.1 through 06-REQ-8.4, 06-REQ-8.E1,
              06-REQ-9.1 through 06-REQ-9.3, 06-REQ-9.E1, 06-REQ-9.E2,
              06-REQ-10.1 through 06-REQ-10.5, 06-REQ-10.E1, 06-REQ-10.E2
"""

from __future__ import annotations

import re
from pathlib import Path
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
    owner: str = "owner",
    repo: str = "repo",
    pr_number: int = 42,
    pr_html_url: str | None = None,
) -> MagicMock:
    """Create a mock platform with create_pr returning PrResult."""
    from afissues.protocol import PrResult

    if pr_html_url is None:
        pr_html_url = f"https://github.com/{owner}/{repo}/pull/{pr_number}"

    platform = MagicMock()
    platform._owner = owner
    platform._repo = repo
    platform.create_pr = AsyncMock(
        return_value=PrResult(
            html_url=pr_html_url,
            number=pr_number,
        ),
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
    """Create a minimal IssueResult for testing."""
    return IssueResult(
        number=number,
        title=title,
        html_url=f"https://github.com/test/repo/issues/{number}",
        labels=labels,
    )


def _make_spec(
    issue_number: int = 42,
    branch_name: str = "fix/test-branch",
) -> object:
    """Create a minimal InMemorySpec for testing."""
    from agentfox.maintenance.spec_builder import InMemorySpec

    return InMemorySpec(
        issue_number=issue_number,
        title="Login fails on empty password",
        task_prompt="Fix the bug",
        system_context="Bug context",
        branch_name=branch_name,
    )


def _make_workspace(branch: str = "fix/test-branch") -> object:
    """Create a minimal WorkspaceInfo for testing."""
    from agentfox.workspace import WorkspaceInfo

    return WorkspaceInfo(
        path=Path("/tmp/test-worktree"),
        branch=branch,
        spec_name="fix-issue-42",
        task_group=0,
    )


# ---------------------------------------------------------------------------
# TS-06-27: FixPipeline.__init__ initialises self._pr_number to None
#
# Requirement: 06-REQ-8.2
# ---------------------------------------------------------------------------


class TestFixPipelineInit:
    """TS-06-27: FixPipeline.__init__ initialises self._pr_number = None."""

    def test_pr_number_initialised_to_none(self) -> None:
        """_pr_number must be None immediately after construction."""
        pipeline = _make_fix_pipeline()
        assert pipeline._pr_number is None


# ---------------------------------------------------------------------------
# TS-06-26: _integrate_fix returns ('pr_created', changed_files) for PR mode
#
# Requirement: 06-REQ-8.1
# ---------------------------------------------------------------------------


class TestIntegrateFixReturnsPrCreated:
    """TS-06-26: _integrate_fix returns ('pr_created', <list>) for PR mode."""

    @pytest.mark.asyncio
    async def test_returns_pr_created_status(self) -> None:
        """_integrate_fix returns 'pr_created' when merge_strategy='pr'."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)

        issue = _make_issue()
        spec = _make_spec()
        workspace = _make_workspace()

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
                return_value=["file.py"],
            ),
        ):
            status, files = await pipeline._integrate_fix(issue, spec, workspace)

        assert status == "pr_created"
        assert isinstance(files, list)

    @pytest.mark.asyncio
    async def test_returns_list_of_changed_files(self) -> None:
        """Second element must be the list of changed files."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)

        issue = _make_issue()
        spec = _make_spec()
        workspace = _make_workspace()

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
                return_value=["src/main.py", "tests/test_main.py"],
            ),
        ):
            status, files = await pipeline._integrate_fix(issue, spec, workspace)

        assert files == ["src/main.py", "tests/test_main.py"]


# ---------------------------------------------------------------------------
# TS-06-28: _integrate_fix sets self._pr_number = result.number
#
# Requirement: 06-REQ-8.3
# ---------------------------------------------------------------------------


class TestIntegrateFixSetsPrNumber:
    """TS-06-28: _pr_number == result.number after create_pr succeeds."""

    @pytest.mark.asyncio
    async def test_pr_number_set_after_create_pr(self) -> None:
        """self._pr_number must equal 42 after create_pr returns PrResult(number=42)."""
        mock_platform = _make_mock_platform(pr_number=42)
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)

        issue = _make_issue()
        spec = _make_spec()
        workspace = _make_workspace()

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
                return_value=["file.py"],
            ),
        ):
            await pipeline._integrate_fix(issue, spec, workspace)

        assert pipeline._pr_number == 42


# ---------------------------------------------------------------------------
# TS-06-29: create_pr raises → exception propagates, _pr_number stays None
#
# Requirement: 06-REQ-8.4
# ---------------------------------------------------------------------------


class TestIntegrateFixPropagatesCreatePrException:
    """TS-06-29: create_pr exception propagates; _pr_number remains None."""

    @pytest.mark.asyncio
    async def test_integration_error_propagates(self) -> None:
        """IntegrationError from create_pr must propagate to caller."""
        mock_platform = _make_mock_platform()
        mock_platform.create_pr = AsyncMock(
            side_effect=IntegrationError("API error"),
        )
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)

        issue = _make_issue()
        spec = _make_spec()
        workspace = _make_workspace()

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
                return_value=["file.py"],
            ),
        ):
            with pytest.raises(IntegrationError):
                await pipeline._integrate_fix(issue, spec, workspace)

        assert pipeline._pr_number is None


# ---------------------------------------------------------------------------
# TS-06-E12: create_pr timeout raises IntegrationError, _pr_number stays None
#
# Requirement: 06-REQ-8.E1
# ---------------------------------------------------------------------------


class TestIntegrateFixTimeout:
    """TS-06-E12: create_pr timeout → IntegrationError; _pr_number stays None."""

    @pytest.mark.asyncio
    async def test_timeout_raises_integration_error(self) -> None:
        """Timeout in create_pr raises IntegrationError; _pr_number remains None."""
        mock_platform = _make_mock_platform()
        mock_platform.create_pr = AsyncMock(
            side_effect=IntegrationError("timeout"),
        )
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)

        issue = _make_issue()
        spec = _make_spec()
        workspace = _make_workspace()

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
                return_value=["file.py"],
            ),
        ):
            with pytest.raises(IntegrationError, match="timeout"):
                await pipeline._integrate_fix(issue, spec, workspace)

        assert pipeline._pr_number is None


# ===========================================================================
# Subtask 3.2: _handle_result with pr_created status (premature-close fix)
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-06-30: _handle_result('pr_created') → af:pr label, remove af:fix,
#           post tracking comment, do NOT close issue.
#
# Requirement: 06-REQ-9.1
# ---------------------------------------------------------------------------


class TestHandleResultPrCreated:
    """TS-06-30: _handle_result applies af:pr and leaves issue open."""

    @pytest.mark.asyncio
    async def test_adds_af_pr_label(self) -> None:
        """_handle_result('pr_created') must call assign_label with 'af:pr'."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42

        issue = _make_issue()
        spec = _make_spec()

        await pipeline._handle_result(issue, spec, "pr_created")

        # af:pr must have been assigned
        label_calls = [call.args[1] for call in mock_platform.assign_label.call_args_list]
        assert "af:pr" in label_calls

    @pytest.mark.asyncio
    async def test_removes_af_fix_label(self) -> None:
        """_handle_result('pr_created') must call remove_label with 'af:fix'."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42

        issue = _make_issue()
        spec = _make_spec()

        await pipeline._handle_result(issue, spec, "pr_created")

        # af:fix must have been removed
        remove_calls = [call.args[1] for call in mock_platform.remove_label.call_args_list]
        assert "af:fix" in remove_calls

    @pytest.mark.asyncio
    async def test_posts_tracking_comment(self) -> None:
        """_handle_result('pr_created') must post a comment on the issue."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42

        issue = _make_issue()
        spec = _make_spec()

        await pipeline._handle_result(issue, spec, "pr_created")

        # At least one comment must have been posted
        assert mock_platform.add_issue_comment.call_count >= 1

    @pytest.mark.asyncio
    async def test_does_not_close_issue(self) -> None:
        """_handle_result('pr_created') must NOT call close_issue."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42

        issue = _make_issue()
        spec = _make_spec()

        await pipeline._handle_result(issue, spec, "pr_created")

        mock_platform.close_issue.assert_not_awaited()


# ---------------------------------------------------------------------------
# TS-06-31: _handle_result with 'pr_created' and af:pr already present
#
# Requirement: 06-REQ-9.2
# ---------------------------------------------------------------------------


class TestHandleResultPrCreatedIdempotent:
    """TS-06-31: af:pr already present → no raise, tracking comment still posted."""

    @pytest.mark.asyncio
    async def test_no_exception_when_af_pr_already_present(self) -> None:
        """No exception raised when af:pr is already on the issue."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42

        issue = _make_issue(labels=("af:pr", "af:fix"))
        spec = _make_spec()

        # Must not raise
        await pipeline._handle_result(issue, spec, "pr_created")

    @pytest.mark.asyncio
    async def test_tracking_comment_posted_even_with_af_pr_present(self) -> None:
        """Tracking comment still posted when af:pr already present."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42

        issue = _make_issue(labels=("af:pr", "af:fix"))
        spec = _make_spec()

        await pipeline._handle_result(issue, spec, "pr_created")

        assert mock_platform.add_issue_comment.call_count >= 1

    @pytest.mark.asyncio
    async def test_issue_remains_open_with_af_pr_present(self) -> None:
        """Issue remains open even when af:pr already present."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42

        issue = _make_issue(labels=("af:pr", "af:fix"))
        spec = _make_spec()

        await pipeline._handle_result(issue, spec, "pr_created")

        mock_platform.close_issue.assert_not_awaited()


# ---------------------------------------------------------------------------
# TS-06-32: _handle_result('pr_created') never applies af:fixed, never closes
#
# Requirement: 06-REQ-9.3
# ---------------------------------------------------------------------------


class TestHandleResultNeverClosesForPrCreated:
    """TS-06-32: af:fixed never applied and close_issue never called."""

    @pytest.mark.asyncio
    async def test_af_fixed_never_assigned(self) -> None:
        """assign_label('af:fixed') must never be called for pr_created."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 1

        issue = _make_issue()
        spec = _make_spec()

        await pipeline._handle_result(issue, spec, "pr_created")

        label_calls = [call.args[1] for call in mock_platform.assign_label.call_args_list]
        assert "af:fixed" not in label_calls

    @pytest.mark.asyncio
    async def test_close_issue_never_called(self) -> None:
        """close_issue must never be called for pr_created."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 1

        issue = _make_issue()
        spec = _make_spec()

        await pipeline._handle_result(issue, spec, "pr_created")

        mock_platform.close_issue.assert_not_awaited()


# ---------------------------------------------------------------------------
# TS-06-E13: assign_label('af:pr') fails → IntegrationError,
#            close_issue and af:fixed never called.
#
# Requirement: 06-REQ-9.E1
# ---------------------------------------------------------------------------


class TestHandleResultLabelFailure:
    """TS-06-E13: assign_label('af:pr') failure raises IntegrationError."""

    @pytest.mark.asyncio
    async def test_integration_error_on_label_failure(self) -> None:
        """IntegrationError from assign_label propagates without close."""
        mock_platform = _make_mock_platform()
        mock_platform.assign_label = AsyncMock(
            side_effect=IntegrationError("422"),
        )
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 1

        issue = _make_issue()
        spec = _make_spec()

        with pytest.raises(IntegrationError):
            await pipeline._handle_result(issue, spec, "pr_created")

        # close_issue must NOT have been called
        mock_platform.close_issue.assert_not_awaited()

    @pytest.mark.asyncio
    async def test_af_fixed_never_applied_on_label_failure(self) -> None:
        """af:fixed must not be applied when assign_label fails."""
        mock_platform = _make_mock_platform()
        mock_platform.assign_label = AsyncMock(
            side_effect=IntegrationError("422"),
        )
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 1

        issue = _make_issue()
        spec = _make_spec()

        with pytest.raises(IntegrationError):
            await pipeline._handle_result(issue, spec, "pr_created")

        # Check that af:fixed was never the label in any assign_label call
        # (all calls raised, so none succeeded with af:fixed)
        label_calls = [call.args[1] for call in mock_platform.assign_label.call_args_list]
        assert "af:fixed" not in label_calls


# ---------------------------------------------------------------------------
# TS-06-E14: _handle_result('pr_created') with _pr_number=None raises
#
# Requirement: 06-REQ-9.E2
# ---------------------------------------------------------------------------


class TestHandleResultPrNumberNoneGuard:
    """TS-06-E14: pr_created with _pr_number=None raises RuntimeError/AssertionError."""

    @pytest.mark.asyncio
    async def test_raises_when_pr_number_is_none(self) -> None:
        """RuntimeError or AssertionError when _pr_number is None and status is pr_created."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = None  # Explicitly set to None

        issue = _make_issue()
        spec = _make_spec()

        with pytest.raises((RuntimeError, AssertionError)):
            await pipeline._handle_result(issue, spec, "pr_created")


# ===========================================================================
# Subtask 3.3: Tracking comment utilities and _handle_result comment posting
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-06-33: PR_TRACKING_PATTERN is a compiled re.Pattern
#
# Requirement: 06-REQ-10.1
# ---------------------------------------------------------------------------


class TestPrTrackingPattern:
    """TS-06-33: PR_TRACKING_PATTERN is a compiled re.Pattern."""

    def test_is_re_pattern(self) -> None:
        """PR_TRACKING_PATTERN must be a compiled re.Pattern."""
        from agentfox.maintenance.fix_pipeline import PR_TRACKING_PATTERN

        assert isinstance(PR_TRACKING_PATTERN, re.Pattern)

    def test_matches_valid_tag(self) -> None:
        """PR_TRACKING_PATTERN must match a valid tracking comment tag."""
        from agentfox.maintenance.fix_pipeline import PR_TRACKING_PATTERN

        m = PR_TRACKING_PATTERN.search("<!-- af:pr-tracking pr_number=42 attempt=1 -->")
        assert m is not None
        assert m.group(1) == "42"
        assert m.group(2) == "1"

    def test_does_not_match_unrelated_text(self) -> None:
        """PR_TRACKING_PATTERN must not match unrelated text."""
        from agentfox.maintenance.fix_pipeline import PR_TRACKING_PATTERN

        assert PR_TRACKING_PATTERN.search("unrelated text") is None


# ---------------------------------------------------------------------------
# TS-06-34: format_tracking_comment produces HTML comment tag + message
#
# Requirement: 06-REQ-10.2
# ---------------------------------------------------------------------------


class TestFormatTrackingComment:
    """TS-06-34: format_tracking_comment returns tag on first line, message on second."""

    def test_first_line_is_html_comment_tag(self) -> None:
        """First line must be the HTML comment tag with pr_number and attempt."""
        from agentfox.maintenance.fix_pipeline import format_tracking_comment

        result = format_tracking_comment(
            pr_number=42,
            attempt=1,
            pr_url="https://github.com/owner/repo/pull/42",
            message="Pull request created: https://github.com/owner/repo/pull/42",
        )
        lines = result.split("\n")
        assert lines[0] == "<!-- af:pr-tracking pr_number=42 attempt=1 -->"

    def test_message_in_result(self) -> None:
        """Result must contain the message text."""
        from agentfox.maintenance.fix_pipeline import format_tracking_comment

        result = format_tracking_comment(
            pr_number=42,
            attempt=1,
            pr_url="https://github.com/owner/repo/pull/42",
            message="Pull request created: https://github.com/owner/repo/pull/42",
        )
        assert "Pull request created: https://github.com/owner/repo/pull/42" in result


# ---------------------------------------------------------------------------
# TS-06-35: parse_tracking_comment extracts (pr_number, attempt) or None
#
# Requirement: 06-REQ-10.3
# ---------------------------------------------------------------------------


class TestParseTrackingComment:
    """TS-06-35: parse_tracking_comment returns (int, int) or None."""

    def test_extracts_pr_number_and_attempt(self) -> None:
        """parse_tracking_comment returns (pr_number, attempt) as integers."""
        from agentfox.maintenance.fix_pipeline import parse_tracking_comment

        result = parse_tracking_comment("some text\n<!-- af:pr-tracking pr_number=7 attempt=3 -->\nmore text")
        assert result == (7, 3)
        assert isinstance(result[0], int)
        assert isinstance(result[1], int)

    def test_returns_none_when_no_tag(self) -> None:
        """parse_tracking_comment returns None when no tracking tag present."""
        from agentfox.maintenance.fix_pipeline import parse_tracking_comment

        assert parse_tracking_comment("no tag here") is None


# ---------------------------------------------------------------------------
# TS-06-36: _handle_result('pr_created') posts comment with tracking tag
#
# Requirement: 06-REQ-10.4
# ---------------------------------------------------------------------------


class TestHandleResultPostsTrackingComment:
    """TS-06-36: _handle_result posts tracking comment with correct body."""

    @pytest.mark.asyncio
    async def test_posted_comment_contains_tracking_tag(self) -> None:
        """Posted comment must contain the HTML tracking comment tag."""
        mock_platform = _make_mock_platform(
            pr_number=42,
            pr_html_url="https://github.com/owner/repo/pull/42",
        )
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42
        pipeline._pr_url = "https://github.com/owner/repo/pull/42"

        issue = _make_issue()
        spec = _make_spec()

        await pipeline._handle_result(issue, spec, "pr_created")

        # Find the tracking comment among posted comments
        comment_bodies = [call.args[1] for call in mock_platform.add_issue_comment.call_args_list]
        tracking_comment = None
        for body in comment_bodies:
            if "af:pr-tracking" in body:
                tracking_comment = body
                break

        assert tracking_comment is not None, f"No tracking comment found. Posted bodies: {comment_bodies}"
        assert "<!-- af:pr-tracking pr_number=42 attempt=1 -->" in tracking_comment

    @pytest.mark.asyncio
    async def test_posted_comment_contains_pr_url(self) -> None:
        """Posted comment must contain the PR URL."""
        mock_platform = _make_mock_platform(
            pr_number=42,
            pr_html_url="https://github.com/owner/repo/pull/42",
        )
        pipeline = _make_fix_pipeline(merge_strategy="pr", platform=mock_platform)
        pipeline._pr_number = 42
        pipeline._pr_url = "https://github.com/owner/repo/pull/42"

        issue = _make_issue()
        spec = _make_spec()

        await pipeline._handle_result(issue, spec, "pr_created")

        comment_bodies = [call.args[1] for call in mock_platform.add_issue_comment.call_args_list]
        # At least one comment must mention the PR URL
        all_text = "\n".join(comment_bodies)
        assert "https://github.com/owner/repo/pull/42" in all_text


# ---------------------------------------------------------------------------
# TS-06-37: All three tracking utilities importable from fix_pipeline
#
# Requirement: 06-REQ-10.5
# ---------------------------------------------------------------------------


class TestTrackingUtilitiesImportable:
    """TS-06-37: PR_TRACKING_PATTERN, format_tracking_comment, parse_tracking_comment importable."""

    def test_all_three_importable(self) -> None:
        """All three tracking utilities must be importable at module level."""
        from agentfox.maintenance.fix_pipeline import (
            PR_TRACKING_PATTERN,
            format_tracking_comment,
            parse_tracking_comment,
        )

        assert callable(format_tracking_comment)
        assert callable(parse_tracking_comment)
        assert isinstance(PR_TRACKING_PATTERN, re.Pattern)


# ---------------------------------------------------------------------------
# TS-06-E15: parse_tracking_comment returns first match for multiple tags
#
# Requirement: 06-REQ-10.E1
# ---------------------------------------------------------------------------


class TestParseTrackingCommentMultipleTags:
    """TS-06-E15: Multiple tracking tags → returns first match."""

    def test_returns_first_match(self) -> None:
        """parse_tracking_comment returns first (pr_number, attempt) from multiple tags."""
        from agentfox.maintenance.fix_pipeline import parse_tracking_comment

        body = "<!-- af:pr-tracking pr_number=10 attempt=1 -->\ntext\n<!-- af:pr-tracking pr_number=11 attempt=2 -->"
        result = parse_tracking_comment(body)
        assert result == (10, 1)


# ---------------------------------------------------------------------------
# TS-06-E16: parse_tracking_comment returns None for empty/untagged body
#
# Requirement: 06-REQ-10.E2
# ---------------------------------------------------------------------------


class TestParseTrackingCommentNoTag:
    """TS-06-E16: Empty string or untagged body → None without raising."""

    def test_empty_string_returns_none(self) -> None:
        """parse_tracking_comment('') must return None."""
        from agentfox.maintenance.fix_pipeline import parse_tracking_comment

        assert parse_tracking_comment("") is None

    def test_untagged_body_returns_none(self) -> None:
        """parse_tracking_comment with no tracking tag must return None."""
        from agentfox.maintenance.fix_pipeline import parse_tracking_comment

        assert parse_tracking_comment("Some random comment with no tag") is None
