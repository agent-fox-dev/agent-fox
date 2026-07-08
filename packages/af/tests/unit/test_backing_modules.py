"""Tests for backing module separation: code and remaining.

Test Spec: TS-59-14 through TS-59-19
Requirements: 59-REQ-4.1 through 59-REQ-4.E1, 59-REQ-5.1 through 59-REQ-5.3
"""

from __future__ import annotations

import inspect
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest


def _mock_infra() -> dict:
    """Return a minimal mock infrastructure dict for run_code tests."""
    mock_db = MagicMock()
    mock_db.connection = MagicMock()
    return {
        "session_runner_factory": MagicMock(),
        "sink_dispatcher": MagicMock(),
        "knowledge_db": mock_db,
        "context_knowledge_db": mock_db,
        "knowledge_provider": MagicMock(),
        "audit_dir": Path("/tmp/audit"),
        "platform": None,
    }


# ---------------------------------------------------------------------------
# TS-59-14 through TS-59-16: Code backing module
# ---------------------------------------------------------------------------


class TestRunCodeCallable:
    """TS-59-14: run_code() can be imported and called with explicit params.

    Requirement: 59-REQ-4.1
    """

    @pytest.mark.asyncio()
    async def test_run_code_returns_execution_state(self) -> None:
        """run_code returns ExecutionState."""
        from agentfox.engine.run import run_code

        config = MagicMock()

        with (
            patch("agentfox.engine.run._setup_infrastructure", return_value=_mock_infra()),
            patch("agentfox.engine.run.Orchestrator") as mock_orch_cls,
        ):
            mock_state = MagicMock()
            mock_state.status = "completed"
            mock_orch = MagicMock()
            mock_orch.run = AsyncMock(return_value=mock_state)
            mock_orch_cls.return_value = mock_orch

            result = await run_code(config, max_cost=1.0)

        assert result is not None
        assert result.status == "completed"  # type: ignore[union-attr]


class TestRunCodeReturnsExecutionState:
    """TS-59-15: run_code() returns ExecutionState with status.

    Requirement: 59-REQ-4.2
    """

    @pytest.mark.asyncio()
    async def test_execution_state_has_status(self) -> None:
        """Returned ExecutionState has a status field."""
        from agentfox.engine.run import run_code

        config = MagicMock()

        with (
            patch("agentfox.engine.run._setup_infrastructure", return_value=_mock_infra()),
            patch("agentfox.engine.run.Orchestrator") as mock_orch_cls,
        ):
            mock_state = MagicMock()
            mock_state.status = "stalled"
            mock_orch = MagicMock()
            mock_orch.run = AsyncMock(return_value=mock_state)
            mock_orch_cls.return_value = mock_orch

            result = await run_code(config)

        assert result.status in (  # type: ignore[union-attr]
            "completed",
            "stalled",
            "cost_limit",
            "interrupted",
        )


class TestRunCodeKeyboardInterrupt:
    """TS-59-16: KeyboardInterrupt during run_code returns interrupted state.

    Requirement: 59-REQ-4.E1
    """

    @pytest.mark.asyncio()
    async def test_keyboard_interrupt_returns_interrupted(self) -> None:
        """KeyboardInterrupt produces ExecutionState(status='interrupted')."""
        from agentfox.engine.run import run_code

        config = MagicMock()

        with (
            patch("agentfox.engine.run._setup_infrastructure", return_value=_mock_infra()),
            patch("agentfox.engine.run.Orchestrator") as mock_orch_cls,
        ):
            mock_orch = MagicMock()
            mock_orch.run = AsyncMock(side_effect=KeyboardInterrupt)
            mock_orch_cls.return_value = mock_orch

            result = await run_code(config)

        assert result.status == "interrupted"  # type: ignore[union-attr]


# ---------------------------------------------------------------------------
# TS-59-17 through TS-59-19: Remaining commands backing functions
# ---------------------------------------------------------------------------


class TestRemainingBackingFunctions:
    """TS-59-17: All 6 remaining commands have importable backing functions.

    Requirement: 59-REQ-5.1
    """

    def test_run_plan_importable(self) -> None:
        """run_plan can be imported."""
        from agentfox.graph.planner import run_plan

        assert callable(run_plan)

    def test_run_reset_importable(self) -> None:
        """run_reset can be imported."""
        from agentfox.engine.reset import run_reset

        assert callable(run_reset)

    def test_init_project_importable(self) -> None:
        """init_project can be imported."""
        from agentfox.workspace.init_project import init_project

        assert callable(init_project)

    def test_generate_standup_importable(self) -> None:
        """generate_standup can be imported."""
        from agentfox.reporting.standup import generate_standup

        assert callable(generate_standup)


class TestBackingFunctionsAcceptParameters:
    """TS-59-18: Backing function signatures match CLI options.

    Requirement: 59-REQ-5.2
    """

    def test_run_plan_has_config_param(self) -> None:
        """run_plan signature includes config."""
        from agentfox.graph.planner import run_plan

        sig = inspect.signature(run_plan)
        assert "config" in sig.parameters

    def test_run_reset_has_target_param(self) -> None:
        """run_reset signature includes target."""
        from agentfox.engine.reset import run_reset

        sig = inspect.signature(run_reset)
        assert "target" in sig.parameters


class TestBackingFunctionsReturnResults:
    """TS-59-19: Backing functions return structured results, not None.

    Requirement: 59-REQ-5.3
    """

    def test_generate_standup_returns_result(self) -> None:
        """generate_standup returns a non-None result."""
        from agentfox.reporting.standup import generate_standup

        config = MagicMock()
        result = generate_standup(config)
        assert result is not None
