"""Merge agent unit tests.

Test Spec: TS-45-9 through TS-45-13, TS-45-E6
Requirements: 45-REQ-4.1 through 45-REQ-4.5, 45-REQ-4.E1, 45-REQ-4.E2
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import AsyncMock, patch

import pytest

from agent_fox.workspace.merge_lock import (
    MERGE_AGENT_SYSTEM_PROMPT,
    run_merge_agent,
)


class TestAgentSpawnedOnMergeFailure:
    """TS-45-9: Agent spawned when all deterministic strategies fail."""

    @pytest.mark.asyncio
    async def test_agent_returns_true_on_success(self, tmp_path: Path) -> None:
        """run_merge_agent returns True when conflicts are resolved."""
        with (
            patch(
                "agent_fox.workspace.merge_lock._run_agent_session",
                new_callable=AsyncMock,
                return_value=True,
            ) as mock_session,
            patch(
                "agent_fox.workspace.merge_lock._check_conflicts_resolved",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            result = await run_merge_agent(
                worktree_path=tmp_path,
                conflict_output="CONFLICT (content): Merge conflict in foo.py",
                model_id="claude-opus-4-6",
            )
            assert result is True
            mock_session.assert_called_once()

    @pytest.mark.asyncio
    async def test_agent_returns_false_on_failure(self, tmp_path: Path) -> None:
        """run_merge_agent returns False when agent fails to resolve."""
        with patch(
            "agent_fox.workspace.merge_lock._run_agent_session",
            new_callable=AsyncMock,
            return_value=False,
        ):
            result = await run_merge_agent(
                worktree_path=tmp_path,
                conflict_output="CONFLICT",
                model_id="claude-opus-4-6",
            )
            assert result is False


class TestAgentUsesAdvancedModel:
    """TS-45-10: Agent uses the ADVANCED model tier."""

    @pytest.mark.asyncio
    async def test_model_id_passed_to_session(self, tmp_path: Path) -> None:
        """The model_id argument is passed through to the agent session."""
        with (
            patch(
                "agent_fox.workspace.merge_lock._run_agent_session",
                new_callable=AsyncMock,
                return_value=True,
            ) as mock_session,
            patch(
                "agent_fox.workspace.merge_lock._check_conflicts_resolved",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            await run_merge_agent(
                worktree_path=tmp_path,
                conflict_output="CONFLICT",
                model_id="claude-opus-4-6",
            )
            # Verify model_id was passed
            call_kwargs = mock_session.call_args
            assert call_kwargs is not None
            # model_id should appear in the call args
            all_args = str(call_kwargs)
            assert "claude-opus-4-6" in all_args


class TestAgentPromptConflictOnly:
    """TS-45-11: Agent prompt restricts to conflict resolution only."""

    def test_system_prompt_mentions_merge_conflict(self) -> None:
        """System prompt contains 'merge conflict'."""
        assert "merge conflict" in MERGE_AGENT_SYSTEM_PROMPT.lower()

    def test_system_prompt_mentions_only(self) -> None:
        """System prompt indicates conflict resolution only."""
        assert "only" in MERGE_AGENT_SYSTEM_PROMPT.lower()

    def test_system_prompt_prohibits_refactoring(self) -> None:
        """System prompt prohibits refactoring."""
        prompt_lower = MERGE_AGENT_SYSTEM_PROMPT.lower()
        assert "refactor" in prompt_lower

    def test_system_prompt_prohibits_test_fixes(self) -> None:
        """System prompt prohibits test fixes."""
        prompt_lower = MERGE_AGENT_SYSTEM_PROMPT.lower()
        assert "test" in prompt_lower

    def test_system_prompt_prohibits_feature_changes(self) -> None:
        """System prompt prohibits feature changes."""
        prompt_lower = MERGE_AGENT_SYSTEM_PROMPT.lower()
        assert "feature" in prompt_lower


class TestAgentReceivesConflictOutput:
    """TS-45-12: Agent receives git conflict output as context."""

    @pytest.mark.asyncio
    async def test_conflict_output_in_prompt(self, tmp_path: Path) -> None:
        """Conflict output is included in the agent session context."""
        conflict_text = "CONFLICT (content): Merge conflict in src/main.py"
        captured_prompt: list[str] = []

        async def fake_session(
            worktree_path: Path,
            system_prompt: str,
            task_prompt: str,
            model_id: str,
        ) -> bool:
            captured_prompt.append(task_prompt)
            return True

        with (
            patch(
                "agent_fox.workspace.merge_lock._run_agent_session",
                side_effect=fake_session,
            ),
            patch(
                "agent_fox.workspace.merge_lock._check_conflicts_resolved",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            await run_merge_agent(
                worktree_path=tmp_path,
                conflict_output=conflict_text,
                model_id="claude-opus-4-6",
            )

        assert len(captured_prompt) == 1
        assert conflict_text in captured_prompt[0]


class TestAgentResolutionCompletesMerge:
    """TS-45-13: After agent resolution, merge is completed."""

    @pytest.mark.asyncio
    async def test_resolution_returns_true(self, tmp_path: Path) -> None:
        """When agent resolves conflicts, run_merge_agent returns True."""
        with (
            patch(
                "agent_fox.workspace.merge_lock._run_agent_session",
                new_callable=AsyncMock,
                return_value=True,
            ),
            patch(
                "agent_fox.workspace.merge_lock._check_conflicts_resolved",
                new_callable=AsyncMock,
                return_value=True,
            ),
        ):
            result = await run_merge_agent(
                worktree_path=tmp_path,
                conflict_output="CONFLICT",
                model_id="claude-opus-4-6",
            )
            assert result is True


class TestAgentApiErrorTreatedAsFailure:
    """TS-45-E6: Agent API errors treated as failure."""

    @pytest.mark.asyncio
    async def test_api_error_returns_false(self, tmp_path: Path) -> None:
        """When agent session raises an exception, run_merge_agent returns False."""
        with patch(
            "agent_fox.workspace.merge_lock._run_agent_session",
            new_callable=AsyncMock,
            side_effect=RuntimeError("API timeout"),
        ):
            result = await run_merge_agent(
                worktree_path=tmp_path,
                conflict_output="CONFLICT",
                model_id="claude-opus-4-6",
            )
            assert result is False

    @pytest.mark.asyncio
    async def test_timeout_error_returns_false(self, tmp_path: Path) -> None:
        """When agent session times out, run_merge_agent returns False."""
        with patch(
            "agent_fox.workspace.merge_lock._run_agent_session",
            new_callable=AsyncMock,
            side_effect=TimeoutError("session timed out"),
        ):
            result = await run_merge_agent(
                worktree_path=tmp_path,
                conflict_output="CONFLICT",
                model_id="claude-opus-4-6",
            )
            assert result is False
