"""Tests for af:fixed label assignment on issue closure (fixes #429).

Verifies that the af:fixed label is added when issues are closed via:
1. fix_pipeline.py — successful fix closure
2. engine.py — supersession closure
3. engine.py — staleness closure

The af:fix label is intentionally preserved on closure to maintain provenance.
af:fixed is added as a re-processing guard and to signal resolution by agent-fox.
"""

from __future__ import annotations

import json
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from afissues.protocol import IssueResult
from agentfox.workspace import WorkspaceInfo


def _mock_workspace() -> WorkspaceInfo:
    return WorkspaceInfo(
        path=Path("/tmp/mock-worktree"),
        branch="fix/test-branch",
        spec_name="fix-issue-42",
        task_group=0,
    )


LABEL_FIXED = "af:fixed"


# ---------------------------------------------------------------------------
# Fix pipeline: af:fixed added after successful close
# ---------------------------------------------------------------------------


class TestFixPipelineLabelFixed:
    """Verify af:fixed label is assigned when fix pipeline closes an issue."""

    @pytest.mark.asyncio
    async def test_fixed_label_assigned_on_successful_close(self) -> None:
        """After close_issue succeeds, assign_label('af:fixed') is called."""
        from agentfox.maintenance.fix_pipeline import FixPipeline

        config = MagicMock()
        config.archetypes.overrides.get.return_value = None
        config.orchestrator.max_retries = 3
        mock_platform = AsyncMock()
        mock_platform.assign_label = AsyncMock()

        pipeline = FixPipeline(config=config, platform=mock_platform)
        pipeline._setup_workspace = AsyncMock(return_value=_mock_workspace())  # type: ignore[method-assign]
        pipeline._cleanup_workspace = AsyncMock()  # type: ignore[method-assign]

        triage_response = json.dumps(
            {
                "summary": "s",
                "affected_files": [],
                "acceptance_criteria": [
                    {"id": "AC-1", "description": "d", "preconditions": "p", "expected": "e", "assertion": "a"},
                ],
            }
        )
        review_response = json.dumps(
            {
                "verdicts": [{"criterion_id": "AC-1", "verdict": "PASS", "evidence": "ok"}],
                "overall_verdict": "PASS",
                "summary": "ok",
            }
        )

        async def mock_run_session(archetype: str, workspace: object = None, **kwargs: object) -> MagicMock:
            outcome = MagicMock(
                input_tokens=10,
                output_tokens=5,
                cache_read_input_tokens=0,
                cache_creation_input_tokens=0,
            )
            if archetype == "triage":
                outcome.response = triage_response
            elif archetype == "reviewer":
                outcome.response = review_response
            else:
                outcome.response = ""
            return outcome

        pipeline._run_session = mock_run_session  # type: ignore[assignment]

        issue = IssueResult(
            number=42,
            title="Some bug",
            html_url="https://github.com/test/repo/issues/42",
        )

        with patch.object(pipeline, "_harvest_and_push", AsyncMock(return_value="merged")):
            await pipeline.process_issue(issue, issue_body="Bug description.")

        mock_platform.close_issue.assert_awaited_once()
        mock_platform.assign_label.assert_any_await(42, LABEL_FIXED)

    @pytest.mark.asyncio
    async def test_fix_label_not_removed_on_successful_close(self) -> None:
        """After close_issue succeeds, remove_label is NOT called (af:fix preserved)."""
        from agentfox.maintenance.fix_pipeline import FixPipeline

        config = MagicMock()
        config.archetypes.overrides.get.return_value = None
        config.orchestrator.max_retries = 3
        mock_platform = AsyncMock()
        mock_platform.remove_label = AsyncMock()

        pipeline = FixPipeline(config=config, platform=mock_platform)
        pipeline._setup_workspace = AsyncMock(return_value=_mock_workspace())  # type: ignore[method-assign]
        pipeline._cleanup_workspace = AsyncMock()  # type: ignore[method-assign]

        triage_response = json.dumps(
            {
                "summary": "s",
                "affected_files": [],
                "acceptance_criteria": [
                    {"id": "AC-1", "description": "d", "preconditions": "p", "expected": "e", "assertion": "a"},
                ],
            }
        )
        review_response = json.dumps(
            {
                "verdicts": [{"criterion_id": "AC-1", "verdict": "PASS", "evidence": "ok"}],
                "overall_verdict": "PASS",
                "summary": "ok",
            }
        )

        async def mock_run_session(archetype: str, workspace: object = None, **kwargs: object) -> MagicMock:
            outcome = MagicMock(
                input_tokens=10,
                output_tokens=5,
                cache_read_input_tokens=0,
                cache_creation_input_tokens=0,
            )
            if archetype == "triage":
                outcome.response = triage_response
            elif archetype == "reviewer":
                outcome.response = review_response
            else:
                outcome.response = ""
            return outcome

        pipeline._run_session = mock_run_session  # type: ignore[assignment]

        issue = IssueResult(
            number=42,
            title="Some bug",
            html_url="https://github.com/test/repo/issues/42",
        )

        with patch.object(pipeline, "_harvest_and_push", AsyncMock(return_value="merged")):
            await pipeline.process_issue(issue, issue_body="Bug description.")

        mock_platform.close_issue.assert_awaited_once()
        mock_platform.remove_label.assert_not_awaited()

    @pytest.mark.asyncio
    async def test_fixed_label_not_assigned_when_harvest_fails(self) -> None:
        """When harvest fails and issue is NOT closed, assign_label is NOT called."""
        from agentfox.maintenance.fix_pipeline import FixPipeline

        config = MagicMock()
        config.archetypes.overrides.get.return_value = None
        config.orchestrator.max_retries = 3
        mock_platform = AsyncMock()
        mock_platform.assign_label = AsyncMock()

        pipeline = FixPipeline(config=config, platform=mock_platform)
        pipeline._setup_workspace = AsyncMock(return_value=_mock_workspace())  # type: ignore[method-assign]
        pipeline._cleanup_workspace = AsyncMock()  # type: ignore[method-assign]

        triage_response = json.dumps(
            {
                "summary": "s",
                "affected_files": [],
                "acceptance_criteria": [
                    {"id": "AC-1", "description": "d", "preconditions": "p", "expected": "e", "assertion": "a"},
                ],
            }
        )
        review_response = json.dumps(
            {
                "verdicts": [{"criterion_id": "AC-1", "verdict": "PASS", "evidence": "ok"}],
                "overall_verdict": "PASS",
                "summary": "ok",
            }
        )

        async def mock_run_session(archetype: str, workspace: object = None, **kwargs: object) -> MagicMock:
            outcome = MagicMock(
                input_tokens=10,
                output_tokens=5,
                cache_read_input_tokens=0,
                cache_creation_input_tokens=0,
            )
            if archetype == "triage":
                outcome.response = triage_response
            elif archetype == "reviewer":
                outcome.response = review_response
            else:
                outcome.response = ""
            return outcome

        pipeline._run_session = mock_run_session  # type: ignore[assignment]

        issue = IssueResult(
            number=42,
            title="Some bug",
            html_url="https://github.com/test/repo/issues/42",
        )

        with patch.object(pipeline, "_harvest_and_push", AsyncMock(side_effect=RuntimeError("harvest failed"))):
            await pipeline.process_issue(issue, issue_body="Bug description.")

        mock_platform.close_issue.assert_not_awaited()
        mock_platform.assign_label.assert_not_awaited()
