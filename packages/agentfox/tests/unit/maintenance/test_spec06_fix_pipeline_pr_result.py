"""Tests for spec 06: fix_pipeline.py create_pr() PrResult usage.

Task group 2 — failing tests for:
  - TS-06-25: fix_pipeline.py accesses result.html_url and result.number
    after create_pr() returns a PrResult.

Requirements: 06-REQ-7.5, 06-REQ-8.1
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
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
) -> MagicMock:
    """Create a mock platform with create_pr returning PrResult."""
    from afissues.protocol import PrResult

    platform = MagicMock()
    platform._owner = owner
    platform._repo = repo
    platform.create_pr = AsyncMock(
        return_value=PrResult(
            html_url="https://github.com/owner/repo/pull/42",
            number=42,
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
) -> IssueResult:
    """Create a minimal IssueResult for testing."""
    return IssueResult(
        number=number,
        title=title,
        html_url=f"https://github.com/test/repo/issues/{number}",
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
# TS-06-25: fix_pipeline.py accesses result.html_url for logging and
#           result.number for the tracking comment after create_pr()
#           returns a PrResult.
#
# Requirement: 06-REQ-7.5
# ---------------------------------------------------------------------------


class TestFixPipelineCreatePrReturnsResult:
    """TS-06-25: _integrate_fix uses PrResult fields correctly."""

    @pytest.mark.asyncio
    async def test_pr_number_stored_on_pipeline(self) -> None:
        """After create_pr returns PrResult, self._pr_number equals result.number."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(
            merge_strategy="pr",
            platform=mock_platform,
        )

        issue = _make_issue()
        spec = _make_spec()
        workspace = _make_workspace()

        with (
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["file.py"],
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
            status, _ = await pipeline._integrate_fix(issue, spec, workspace)

        # After the PrResult change, _pr_number should be set to 42
        assert pipeline._pr_number == 42

    @pytest.mark.asyncio
    async def test_status_is_pr_created(self) -> None:
        """_integrate_fix returns 'pr_created' status for PR mode."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(
            merge_strategy="pr",
            platform=mock_platform,
        )

        issue = _make_issue()
        spec = _make_spec()
        workspace = _make_workspace()

        with (
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["file.py"],
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
            status, _ = await pipeline._integrate_fix(issue, spec, workspace)

        assert status == "pr_created"

    @pytest.mark.asyncio
    async def test_no_attribute_error_on_pr_result(self) -> None:
        """create_pr returning PrResult does not cause AttributeError."""
        mock_platform = _make_mock_platform()
        pipeline = _make_fix_pipeline(
            merge_strategy="pr",
            platform=mock_platform,
        )

        issue = _make_issue()
        spec = _make_spec()
        workspace = _make_workspace()

        with (
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["file.py"],
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
            # Must not raise AttributeError when accessing .html_url or .number
            status, changed_files = await pipeline._integrate_fix(
                issue, spec, workspace,
            )

        # Basic assertions that the call succeeded
        assert isinstance(status, str)
        assert isinstance(changed_files, list)
