"""Tests for #732: backoff log-once-per-window and create_branch stderr.

Covers:
- TS-NS-1: Workspace backoff emits at most one log message per window
- TS-NS-2: Environment backoff emits at most one log message per window
- TS-NS-3: create_branch() includes git stderr in WorkspaceError
- TS-NS-4: Circuit breaker unchanged (blocks after _MAX_WORKSPACE_FAILURES)
- TS-NS-5: Log flag resets between backoff windows
"""

from __future__ import annotations

import logging
import time
from unittest.mock import AsyncMock, patch

import pytest

from agentfox.engine.graph_sync import GraphSync
from agentfox.engine.result_handler import (
    _MAX_WORKSPACE_FAILURES,
    SessionResultHandler,
)
from agentfox.engine.state import ExecutionState, SessionRecord


def _make_graph_sync(
    node_id: str = "spec:1",
) -> tuple[GraphSync, dict[str, str], dict[str, list[str]]]:
    node_states = {node_id: "in_progress"}
    edges: dict[str, list[str]] = {node_id: []}
    return GraphSync(node_states, edges), node_states, edges


def _make_handler(
    graph_sync: GraphSync,
    block_calls: list[tuple[str, str]],
) -> SessionResultHandler:
    def _block_task(node_id: str, state: ExecutionState, reason: str) -> None:
        block_calls.append((node_id, reason))
        graph_sync.mark_blocked(node_id, reason)
        state.blocked_reasons[node_id] = reason

    return SessionResultHandler(
        graph_sync=graph_sync,
        max_retries=3,
        task_callback=None,
        sink=None,
        run_id="test-run",
        graph=None,
        archetypes_config=None,
        knowledge_db_conn=None,
        block_task_fn=_block_task,
        check_block_budget_fn=lambda _state: False,
    )


def _make_workspace_record(
    node_id: str = "spec:1", attempt: int = 1
) -> SessionRecord:
    return SessionRecord(
        node_id=node_id,
        attempt=attempt,
        status="failed",
        input_tokens=0,
        output_tokens=0,
        cost=0.0,
        duration_ms=0,
        error_message="git worktree add failed (exit code 128)",
        timestamp="2026-01-01T00:00:00",
        is_workspace_setup_failure=True,
    )


def _make_environment_record(
    node_id: str = "spec:1", attempt: int = 1
) -> SessionRecord:
    """Create a record that satisfies the is_environment_failure property.

    is_environment_failure is a computed property: status != completed,
    0 tokens, 0 cost, not workspace_setup_failure, not transport_error.
    """
    return SessionRecord(
        node_id=node_id,
        attempt=attempt,
        status="failed",
        input_tokens=0,
        output_tokens=0,
        cost=0.0,
        duration_ms=0,
        error_message="environment bootstrap crashed",
        timestamp="2026-01-01T00:00:00",
    )


# ---------------------------------------------------------------------------
# TS-NS-1: Workspace backoff emits at most one log per window
# ---------------------------------------------------------------------------

class TestWorkspaceBackoffLogOnce:
    """TS-NS-1: Workspace backoff log message appears at most once per window."""

    def test_workspace_backoff_logs_once_across_many_cycles(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
        """Calling log_backoff_once N times for workspace produces exactly 1 log."""
        graph_sync, node_states, _ = _make_graph_sync()
        block_calls: list[tuple[str, str]] = []
        handler = _make_handler(graph_sync, block_calls)

        state = ExecutionState(plan_hash="abc", node_states=node_states)
        handler.process(_make_workspace_record(), 1, state, {})

        # Simulate N dispatch cycles hitting the backoff check
        with caplog.at_level(logging.DEBUG, logger="agentfox.engine.result_handler"):
            for _ in range(50):
                handler.log_backoff_once("spec:1", "workspace")

        backoff_messages = [
            r for r in caplog.records
            if "Workspace backoff active" in r.message
        ]
        assert len(backoff_messages) == 1

    def test_workspace_backoff_zero_debug_logs_on_subsequent_cycles(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
        """After the first log, subsequent cycles emit zero workspace backoff logs."""
        graph_sync, node_states, _ = _make_graph_sync()
        block_calls: list[tuple[str, str]] = []
        handler = _make_handler(graph_sync, block_calls)

        state = ExecutionState(plan_hash="abc", node_states=node_states)
        handler.process(_make_workspace_record(), 1, state, {})

        # First call — should log
        handler.log_backoff_once("spec:1", "workspace")

        # Clear and try again
        caplog.clear()
        with caplog.at_level(logging.DEBUG, logger="agentfox.engine.result_handler"):
            for _ in range(100):
                handler.log_backoff_once("spec:1", "workspace")

        backoff_messages = [
            r for r in caplog.records
            if "Workspace backoff active" in r.message
        ]
        assert len(backoff_messages) == 0


# ---------------------------------------------------------------------------
# TS-NS-2: Environment backoff emits at most one log per window
# ---------------------------------------------------------------------------

class TestEnvironmentBackoffLogOnce:
    """TS-NS-2: Environment backoff log message appears at most once per window."""

    def test_environment_backoff_logs_once_across_many_cycles(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
        """Calling log_backoff_once N times for environment produces exactly 1 log."""
        graph_sync, node_states, _ = _make_graph_sync()
        block_calls: list[tuple[str, str]] = []
        handler = _make_handler(graph_sync, block_calls)

        state = ExecutionState(plan_hash="abc", node_states=node_states)
        handler.process(_make_environment_record(), 1, state, {})

        with caplog.at_level(logging.DEBUG, logger="agentfox.engine.result_handler"):
            for _ in range(50):
                handler.log_backoff_once("spec:1", "environment")

        backoff_messages = [
            r for r in caplog.records
            if "Environment failure backoff active" in r.message
        ]
        assert len(backoff_messages) == 1


# ---------------------------------------------------------------------------
# TS-NS-3: create_branch() includes git stderr in WorkspaceError
# ---------------------------------------------------------------------------

class TestCreateBranchStderr:
    """TS-NS-3: create_branch() includes stderr in the error message."""

    @pytest.mark.asyncio
    async def test_stderr_included_in_error_message(self) -> None:
        """When git branch fails, str(exc) contains the stderr text."""
        from agentfox.workspace.git import create_branch
        from agentfox.core.errors import WorkspaceError

        mock_run_git = AsyncMock(
            return_value=(128, "", "fatal: not a valid object name 'main'\n")
        )

        with patch("agentfox.workspace.git.run_git", mock_run_git):
            with pytest.raises(WorkspaceError) as exc_info:
                await create_branch(
                    repo_path=__import__("pathlib").Path("/tmp/fake-repo"),
                    branch_name="feature/test",
                    start_point="main",
                )

        error_str = str(exc_info.value)
        assert "fatal: not a valid object name" in error_str
        assert "exit code 128" in error_str

    @pytest.mark.asyncio
    async def test_stderr_empty_still_works(self) -> None:
        """When stderr is empty, the error message is still valid."""
        from agentfox.workspace.git import create_branch
        from agentfox.core.errors import WorkspaceError

        mock_run_git = AsyncMock(return_value=(1, "", ""))

        with patch("agentfox.workspace.git.run_git", mock_run_git):
            with pytest.raises(WorkspaceError) as exc_info:
                await create_branch(
                    repo_path=__import__("pathlib").Path("/tmp/fake-repo"),
                    branch_name="feature/test",
                    start_point="main",
                )

        error_str = str(exc_info.value)
        assert "exit code 1" in error_str

    @pytest.mark.asyncio
    async def test_stderr_in_context_details(self) -> None:
        """The raw stderr is also available as exc.context['details']."""
        from agentfox.workspace.git import create_branch
        from agentfox.core.errors import WorkspaceError

        stderr_text = "fatal: not a valid object name 'main'\n"
        mock_run_git = AsyncMock(return_value=(128, "", stderr_text))

        with patch("agentfox.workspace.git.run_git", mock_run_git):
            with pytest.raises(WorkspaceError) as exc_info:
                await create_branch(
                    repo_path=__import__("pathlib").Path("/tmp/fake-repo"),
                    branch_name="feature/test",
                    start_point="main",
                )

        assert exc_info.value.context["details"] == stderr_text


# ---------------------------------------------------------------------------
# TS-NS-4: Circuit breaker unchanged
# ---------------------------------------------------------------------------

class TestCircuitBreakerUnchanged:
    """TS-NS-4: Circuit breaker blocks after _MAX_WORKSPACE_FAILURES."""

    def test_blocks_on_sixth_failure_not_fifth(self) -> None:
        """The node is blocked on the 6th failure (_MAX_WORKSPACE_FAILURES=6),
        not on the 5th."""
        graph_sync, node_states, _ = _make_graph_sync()
        block_calls: list[tuple[str, str]] = []
        handler = _make_handler(graph_sync, block_calls)

        state = ExecutionState(plan_hash="abc", node_states=node_states)
        error_tracker: dict[str, str | None] = {}

        # First 5 failures should not block
        for i in range(_MAX_WORKSPACE_FAILURES - 1):
            node_states["spec:1"] = "in_progress"
            handler.process(
                _make_workspace_record(attempt=i + 1), i + 1, state, error_tracker
            )

        assert len(block_calls) == 0, "Should not block before _MAX_WORKSPACE_FAILURES"

        # 6th failure should block
        node_states["spec:1"] = "in_progress"
        handler.process(
            _make_workspace_record(attempt=_MAX_WORKSPACE_FAILURES),
            _MAX_WORKSPACE_FAILURES,
            state,
            error_tracker,
        )

        assert len(block_calls) == 1
        assert "Workspace setup failed" in block_calls[0][1]


# ---------------------------------------------------------------------------
# TS-NS-5: Log flag resets between backoff windows
# ---------------------------------------------------------------------------

class TestBackoffLogFlagResets:
    """TS-NS-5: The log suppression flag resets when a new backoff window starts."""

    def test_two_windows_produce_two_log_entries(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
        """Simulating two complete backoff windows produces exactly two log entries."""
        graph_sync, node_states, _ = _make_graph_sync()
        block_calls: list[tuple[str, str]] = []
        handler = _make_handler(graph_sync, block_calls)

        state = ExecutionState(plan_hash="abc", node_states=node_states)
        error_tracker: dict[str, str | None] = {}

        with caplog.at_level(logging.DEBUG, logger="agentfox.engine.result_handler"):
            # --- Window 1 ---
            handler.process(_make_workspace_record(), 1, state, error_tracker)
            for _ in range(20):
                handler.log_backoff_once("spec:1", "workspace")

            # Simulate backoff expiry
            ns = handler._node_retry_states["spec:1"]
            ns.workspace_next_eligible = time.monotonic() - 1

            # --- Window 2: new failure triggers fresh backoff ---
            node_states["spec:1"] = "in_progress"
            handler.process(
                _make_workspace_record(attempt=2), 2, state, error_tracker
            )
            for _ in range(20):
                handler.log_backoff_once("spec:1", "workspace")

        backoff_messages = [
            r for r in caplog.records
            if "Workspace backoff active" in r.message
        ]
        assert len(backoff_messages) == 2

    def test_environment_flag_resets_between_windows(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
        """Environment backoff also resets the log flag between windows."""
        graph_sync, node_states, _ = _make_graph_sync()
        block_calls: list[tuple[str, str]] = []
        handler = _make_handler(graph_sync, block_calls)

        state = ExecutionState(plan_hash="abc", node_states=node_states)
        error_tracker: dict[str, str | None] = {}

        with caplog.at_level(logging.DEBUG, logger="agentfox.engine.result_handler"):
            # --- Window 1 ---
            handler.process(_make_environment_record(), 1, state, error_tracker)
            for _ in range(20):
                handler.log_backoff_once("spec:1", "environment")

            # Simulate backoff expiry
            ns = handler._node_retry_states["spec:1"]
            ns.environment_next_eligible = time.monotonic() - 1

            # --- Window 2 ---
            node_states["spec:1"] = "in_progress"
            handler.process(
                _make_environment_record(attempt=2), 2, state, error_tracker
            )
            for _ in range(20):
                handler.log_backoff_once("spec:1", "environment")

        backoff_messages = [
            r for r in caplog.records
            if "Environment failure backoff active" in r.message
        ]
        assert len(backoff_messages) == 2
