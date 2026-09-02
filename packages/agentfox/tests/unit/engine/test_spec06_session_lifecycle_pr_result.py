"""Tests for spec 06: session_lifecycle.py create_pr() PrResult usage.

Task group 2 — failing test for:
  - TS-06-24: session_lifecycle.py accesses result.html_url on the PrResult
    returned by create_pr() rather than treating the result as a bare string.

Requirements: 06-REQ-7.4
"""

from __future__ import annotations

import logging
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from afaudit.sink import SessionOutcome
from agentfox.core.config import AgentFoxConfig, WorkspaceConfig
from agentfox.engine.session_lifecycle import NodeSessionRunner
from agentfox.knowledge.db import KnowledgeDB
from agentfox.workspace import WorkspaceInfo

_MOCK_KB = MagicMock(spec=KnowledgeDB)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_workspace(branch: str = "feature/test_spec/1") -> WorkspaceInfo:
    """Create a minimal WorkspaceInfo for testing."""
    return WorkspaceInfo(
        path=Path("/tmp/test-worktree"),
        branch=branch,
        spec_name="test_spec",
        task_group=1,
    )


def _make_session_outcome(status: str = "completed") -> SessionOutcome:
    """Create a minimal SessionOutcome for testing."""
    return SessionOutcome(
        status=status,
        spec_name="test_spec",
        task_group="1",
        node_id="test_spec:1",
    )


def _make_runner(
    merge_strategy: str = "pr",
    integration_branch: str = "main",
) -> NodeSessionRunner:
    """Create a NodeSessionRunner with the specified merge_strategy config."""
    config = AgentFoxConfig(
        workspace=WorkspaceConfig(
            merge_strategy=merge_strategy,
            integration_branch=integration_branch,
        ),
    )
    return NodeSessionRunner(
        "test_spec:1",
        config,
        knowledge_db=_MOCK_KB,
    )


def _make_mock_platform_with_pr_result() -> MagicMock:
    """Create a mock platform with create_pr returning PrResult."""
    from afissues.protocol import PrResult

    platform = MagicMock()
    platform._owner = "owner"
    platform._repo = "repo"
    platform.create_pr = AsyncMock(
        return_value=PrResult(
            html_url="https://github.com/owner/repo/pull/5",
            number=5,
        ),
    )
    platform.add_issue_comment = AsyncMock()
    platform.close_issue = AsyncMock()
    platform.assign_label = AsyncMock()
    return platform


# ---------------------------------------------------------------------------
# TS-06-24: session_lifecycle.py accesses result.html_url on the PrResult
#           returned by create_pr() rather than treating the result as a
#           bare string.
#
# Requirement: 06-REQ-7.4
# ---------------------------------------------------------------------------


class TestSessionLifecyclePrResult:
    """TS-06-24: session_lifecycle uses result.html_url from PrResult."""

    @pytest.mark.asyncio
    async def test_pr_url_logged_from_pr_result(self, caplog: pytest.LogCaptureFixture) -> None:
        """PR URL is logged correctly when create_pr returns PrResult."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform_with_pr_result()

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
            caplog.at_level(logging.INFO),
        ):
            await runner._harvest_and_integrate(
                "test_spec:1",
                outcome,
                workspace,
                Path("/tmp/repo"),
            )

        # The URL from PrResult.html_url should appear in log output
        assert "https://github.com/owner/repo/pull/5" in caplog.text

    @pytest.mark.asyncio
    async def test_no_attribute_error_on_pr_result(self) -> None:
        """create_pr returning PrResult does not cause AttributeError."""
        runner = _make_runner(merge_strategy="pr")
        workspace = _make_workspace(branch="feat/ms")
        outcome = _make_session_outcome("completed")
        mock_platform = _make_mock_platform_with_pr_result()

        with (
            patch(
                "agentfox.engine.session_lifecycle.harvest",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.engine.session_lifecycle.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.run_git",
                new_callable=AsyncMock,
                return_value=(0, "abc123\n", ""),
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch(
                "agentfox.workspace.git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["config.py"],
            ),
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.workspace.git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            # Must not raise AttributeError when accessing .html_url
            result = await runner._harvest_and_integrate(
                "test_spec:1",
                outcome,
                workspace,
                Path("/tmp/repo"),
            )

        # Basic sanity check: result should be a tuple
        assert isinstance(result, tuple)
