"""Unit tests for fix pipeline archetype usage (coder with mode='fix').

Test Spec: TS-88-10, TS-88-11, TS-88-12
Requirements: 88-REQ-3.1, 88-REQ-3.2, 88-REQ-3.3
"""

from __future__ import annotations

from pathlib import Path
from typing import TYPE_CHECKING
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from agentfox.workspace import WorkspaceInfo

if TYPE_CHECKING:
    from agentfox.maintenance.fix_pipeline import TriageResult
    from agentfox.maintenance.spec_builder import InMemorySpec


def _make_config() -> MagicMock:
    """Return a minimal mock AgentFoxConfig."""
    config = MagicMock()
    config.archetypes.overrides.get.return_value = None
    config.security = None
    return config


_DEFAULT_TASK_PROMPT = (
    "Fix the issue: test (#42)\n\nRefer to the issue description and acceptance criteria in the context above."
)


def _make_spec(task_prompt: str = _DEFAULT_TASK_PROMPT) -> InMemorySpec:
    """Return a minimal InMemorySpec-like object."""
    from agentfox.maintenance.spec_builder import InMemorySpec

    return InMemorySpec(
        issue_number=42,
        title="test",
        task_prompt=task_prompt,
        system_context="Repository context here.",
        branch_name="fix/test",
    )


def _make_triage() -> TriageResult:
    """Return an empty TriageResult."""
    from agentfox.maintenance.fix_pipeline import TriageResult

    return TriageResult()


def _make_workspace() -> WorkspaceInfo:
    """Return a mock WorkspaceInfo."""
    return WorkspaceInfo(
        path=Path("/tmp/mock-worktree"),
        branch="fix/test",
        spec_name="fix-issue-42",
        task_group=0,
    )


# ---------------------------------------------------------------------------
# TS-88-10: _build_coder_prompt uses coder archetype with mode='fix'
# Requirement: 88-REQ-3.1
# ---------------------------------------------------------------------------


class TestBuildCoderPromptArchetype:
    """Verify _build_coder_prompt passes archetype='coder', mode='fix' to build_system_prompt."""

    def test_build_system_prompt_called_with_coder_fix_mode(self) -> None:
        """build_system_prompt receives archetype='coder' and mode='fix'."""
        from agentfox.maintenance.fix_pipeline import FixPipeline

        config = _make_config()
        platform = MagicMock()
        pipeline = FixPipeline(config=config, platform=platform)
        spec = _make_spec()
        triage = _make_triage()

        with patch(
            "agentfox.session.prompt.build_system_prompt",
            return_value="mocked-system-prompt",
        ) as mock_bsp:
            pipeline._build_coder_prompt(spec, triage)

        # Verify archetype and mode keyword arguments
        assert mock_bsp.called, "build_system_prompt was not called"
        call_kwargs = mock_bsp.call_args.kwargs
        assert call_kwargs.get("archetype") == "coder", (
            f"Expected archetype='coder', got {call_kwargs.get('archetype')!r}"
        )
        assert call_kwargs.get("mode") == "fix", f"Expected mode='fix', got {call_kwargs.get('mode')!r}"

    def test_build_system_prompt_not_called_with_fix_coder(self) -> None:
        """build_system_prompt is NOT called with archetype='fix_coder'."""
        from agentfox.maintenance.fix_pipeline import FixPipeline

        config = _make_config()
        platform = MagicMock()
        pipeline = FixPipeline(config=config, platform=platform)
        spec = _make_spec()
        triage = _make_triage()

        with patch(
            "agentfox.session.prompt.build_system_prompt",
            return_value="mocked-system-prompt",
        ) as mock_bsp:
            pipeline._build_coder_prompt(spec, triage)

        call_kwargs = mock_bsp.call_args.kwargs
        assert call_kwargs.get("archetype") != "fix_coder", (
            "build_system_prompt was called with archetype='fix_coder' (expected 'coder' with mode='fix')"
        )


# ---------------------------------------------------------------------------
# TS-88-11: _build_coder_prompt does not append commit format
# Requirement: 88-REQ-3.3
# ---------------------------------------------------------------------------


class TestBuildCoderPromptNoCommitFormat:
    """Verify the task prompt is not modified with hardcoded commit format."""

    def test_task_prompt_unchanged_without_review_feedback(self) -> None:
        """Task prompt starts with spec.task_prompt (plus subtask phrase) when review_feedback is None.

        After spec 02 prompt alignment, _build_coder_prompt appends the
        subtask reference phrase (02-REQ-1.2). This test verifies only that
        no unrelated content (like a hardcoded commit format) is injected.
        """
        from agentfox.maintenance.fix_pipeline import FixPipeline

        config = _make_config()
        platform = MagicMock()
        pipeline = FixPipeline(config=config, platform=platform)

        original_task = (
            "Fix the issue: test (#42)\n\nRefer to the issue description and acceptance criteria in the context above."
        )
        spec = _make_spec(task_prompt=original_task)
        triage = _make_triage()

        with patch(
            "agentfox.session.prompt.build_system_prompt",
            return_value="mocked-system-prompt",
        ):
            _, task_prompt = pipeline._build_coder_prompt(spec, triage, review_feedback=None)

        assert task_prompt.startswith(original_task), f"task_prompt does not start with original; got {task_prompt!r}"
        assert "Refer to the tasks subtask list in the context above" in task_prompt

    def test_task_prompt_has_no_hardcoded_nightshift_suffix(self) -> None:
        """task_prompt does not contain hardcoded commit format appended by the method."""
        from agentfox.maintenance.fix_pipeline import FixPipeline

        config = _make_config()
        platform = MagicMock()
        pipeline = FixPipeline(config=config, platform=platform)
        spec = _make_spec()
        triage = _make_triage()

        with patch(
            "agentfox.session.prompt.build_system_prompt",
            return_value="mocked-system-prompt",
        ):
            _, task_prompt = pipeline._build_coder_prompt(spec, triage, review_feedback=None)

        # The method must not have appended a hardcoded commit format block
        assert "fix(#" not in task_prompt or task_prompt == spec.task_prompt, (
            "task_prompt contains 'fix(#' appended by _build_coder_prompt"
        )


# ---------------------------------------------------------------------------
# TS-88-12: _run_coder_session passes coder archetype with mode='fix'
# Requirement: 88-REQ-3.2
# ---------------------------------------------------------------------------


class TestRunCoderSessionArchetype:
    """Verify _run_coder_session calls _run_session with 'coder'."""

    @pytest.mark.asyncio
    async def test_run_session_called_with_coder(self) -> None:
        """_run_session is called with 'coder' as the first argument."""
        from agentfox.maintenance.fix_pipeline import FixPipeline

        config = _make_config()
        platform = MagicMock()
        pipeline = FixPipeline(config=config, platform=platform)

        mock_outcome = MagicMock()
        mock_outcome.input_tokens = 0
        mock_outcome.output_tokens = 0
        mock_outcome.cache_read_input_tokens = 0
        mock_outcome.cache_creation_input_tokens = 0

        spec = _make_spec()
        workspace = _make_workspace()

        with patch.object(
            pipeline,
            "_run_session",
            new_callable=AsyncMock,
            return_value=mock_outcome,
        ) as mock_rs:
            await pipeline._run_coder_session(
                workspace,
                spec,
                "system-prompt",
                "task-prompt",
            )

        assert mock_rs.called, "_run_session was not called"
        first_arg = mock_rs.call_args[0][0]
        assert first_arg == "coder", f"Expected _run_session called with 'coder', got {first_arg!r}"

    @pytest.mark.asyncio
    async def test_run_session_not_called_with_fix_coder(self) -> None:
        """_run_session is NOT called with 'fix_coder' as the archetype."""
        from agentfox.maintenance.fix_pipeline import FixPipeline

        config = _make_config()
        platform = MagicMock()
        pipeline = FixPipeline(config=config, platform=platform)

        mock_outcome = MagicMock()
        mock_outcome.input_tokens = 0
        mock_outcome.output_tokens = 0
        mock_outcome.cache_read_input_tokens = 0
        mock_outcome.cache_creation_input_tokens = 0

        spec = _make_spec()
        workspace = _make_workspace()

        with patch.object(
            pipeline,
            "_run_session",
            new_callable=AsyncMock,
            return_value=mock_outcome,
        ) as mock_rs:
            await pipeline._run_coder_session(
                workspace,
                spec,
                "system-prompt",
                "task-prompt",
            )

        first_arg = mock_rs.call_args[0][0]
        assert first_arg != "fix_coder", "_run_session was called with 'fix_coder' instead of 'coder'"
