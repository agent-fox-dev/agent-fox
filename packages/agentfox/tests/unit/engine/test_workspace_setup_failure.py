"""Tests for workspace-setup failure classification, backoff, and session counting.

Covers:
- SessionRecord.is_workspace_setup_failure flag
- update_state_with_session conditional counting
- SessionResultHandler workspace backoff and circuit-breaking
- is_workspace_backoff_active dispatch integration
"""

from __future__ import annotations

import time

from agentfox.engine.graph_sync import GraphSync
from agentfox.engine.result_handler import (
    _MAX_WORKSPACE_FAILURES,
    SessionResultHandler,
)
from agentfox.engine.state import ExecutionState, SessionRecord, update_state_with_session


class TestSessionRecordWorkspaceFlag:
    """SessionRecord carries the workspace-setup failure flag."""

    def test_default_is_false(self) -> None:
        record = SessionRecord(
            node_id="spec:1",
            attempt=1,
            status="failed",
            input_tokens=0,
            output_tokens=0,
            cost=0.0,
            duration_ms=0,
            error_message="git worktree add failed",
            timestamp="2026-01-01T00:00:00",
        )
        assert record.is_workspace_setup_failure is False

    def test_can_be_set_true(self) -> None:
        record = SessionRecord(
            node_id="spec:1",
            attempt=1,
            status="failed",
            input_tokens=0,
            output_tokens=0,
            cost=0.0,
            duration_ms=0,
            error_message="git worktree add failed",
            timestamp="2026-01-01T00:00:00",
            is_workspace_setup_failure=True,
        )
        assert record.is_workspace_setup_failure is True


class TestConditionalSessionCounting:
    """update_state_with_session skips total_sessions for workspace failures."""

    def _make_state(self) -> ExecutionState:
        return ExecutionState(
            plan_hash="abc",
            node_states={"spec:1": "in_progress"},
        )

    def test_normal_failure_increments_total_sessions(self) -> None:
        state = self._make_state()
        record = SessionRecord(
            node_id="spec:1",
            attempt=1,
            status="failed",
            input_tokens=100,
            output_tokens=200,
            cost=0.10,
            duration_ms=5000,
            error_message="coding error",
            timestamp="2026-01-01T00:00:00",
        )
        update_state_with_session(state, record)
        assert state.total_sessions == 1
        assert state.workspace_setup_failures == 0

    def test_workspace_failure_does_not_increment_total_sessions(self) -> None:
        state = self._make_state()
        record = SessionRecord(
            node_id="spec:1",
            attempt=1,
            status="failed",
            input_tokens=0,
            output_tokens=0,
            cost=0.0,
            duration_ms=0,
            error_message="git worktree add failed (exit code 128)",
            timestamp="2026-01-01T00:00:00",
            is_workspace_setup_failure=True,
        )
        update_state_with_session(state, record)
        assert state.total_sessions == 0
        assert state.workspace_setup_failures == 1

    def test_mixed_sessions_counted_correctly(self) -> None:
        state = self._make_state()
        for i in range(3):
            update_state_with_session(
                state,
                SessionRecord(
                    node_id="spec:1",
                    attempt=i + 1,
                    status="failed",
                    input_tokens=0,
                    output_tokens=0,
                    cost=0.0,
                    duration_ms=0,
                    error_message="worktree failed",
                    timestamp="2026-01-01T00:00:00",
                    is_workspace_setup_failure=True,
                ),
            )
        update_state_with_session(
            state,
            SessionRecord(
                node_id="spec:1",
                attempt=4,
                status="completed",
                input_tokens=1000,
                output_tokens=500,
                cost=0.50,
                duration_ms=30000,
                error_message=None,
                timestamp="2026-01-01T00:00:00",
            ),
        )
        assert state.total_sessions == 1
        assert state.workspace_setup_failures == 3


class TestWorkspaceBackoffAndCircuitBreaker:
    """SessionResultHandler handles workspace setup failures with backoff."""

    def _make_handler(
        self,
        graph_sync: GraphSync,
        block_calls: list,
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

    def _make_record(self, node_id: str = "spec:1", attempt: int = 1) -> SessionRecord:
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

    def test_first_failure_sets_backoff(self) -> None:
        node_states = {"spec:1": "in_progress"}
        edges: dict[str, list[str]] = {"spec:1": []}
        graph_sync = GraphSync(node_states, edges)
        block_calls: list = []
        handler = self._make_handler(graph_sync, block_calls)

        state = ExecutionState(plan_hash="abc", node_states=node_states)
        handler.process(self._make_record(), 1, state, {}, {})

        assert len(block_calls) == 0
        assert graph_sync.node_states["spec:1"] == "pending"
        assert handler.is_workspace_backoff_active("spec:1") is True

    def test_third_failure_blocks_node(self) -> None:
        node_states = {"spec:1": "in_progress"}
        edges: dict[str, list[str]] = {"spec:1": []}
        graph_sync = GraphSync(node_states, edges)
        block_calls: list = []
        handler = self._make_handler(graph_sync, block_calls)

        state = ExecutionState(plan_hash="abc", node_states=node_states)
        attempt_tracker: dict[str, int] = {}
        error_tracker: dict[str, str | None] = {}

        for i in range(_MAX_WORKSPACE_FAILURES):
            graph_sync.node_states["spec:1"] = "in_progress"
            handler.process(self._make_record(attempt=i + 1), i + 1, state, attempt_tracker, error_tracker)

        assert len(block_calls) == 1
        assert "Workspace setup failed" in block_calls[0][1]
        assert "stale worktrees" in block_calls[0][1]

    def test_fifth_failure_still_retries_with_backoff(self) -> None:
        """Failures below _MAX_WORKSPACE_FAILURES get backoff, not blocking.

        Regression test for #701: under concurrent git pressure, 3 failures
        with short backoffs (2s, 4s) was too aggressive. With the raised
        threshold, failure 5 should still get backoff (not trip the breaker).
        """
        node_states = {"spec:1": "in_progress"}
        edges: dict[str, list[str]] = {"spec:1": []}
        graph_sync = GraphSync(node_states, edges)
        block_calls: list = []
        handler = self._make_handler(graph_sync, block_calls)

        state = ExecutionState(plan_hash="abc", node_states=node_states)
        attempt_tracker: dict[str, int] = {}
        error_tracker: dict[str, str | None] = {}

        for i in range(5):
            graph_sync.node_states["spec:1"] = "in_progress"
            handler.process(self._make_record(attempt=i + 1), i + 1, state, attempt_tracker, error_tracker)

        assert len(block_calls) == 0
        assert graph_sync.node_states["spec:1"] == "pending"
        assert handler.is_workspace_backoff_active("spec:1") is True

    def test_backoff_does_not_consume_failure_retries(self) -> None:
        """Workspace failures should not consume failure counter retries."""
        node_states = {"spec:1": "in_progress"}
        edges: dict[str, list[str]] = {"spec:1": []}
        graph_sync = GraphSync(node_states, edges)
        block_calls: list = []
        handler = self._make_handler(graph_sync, block_calls)

        state = ExecutionState(plan_hash="abc", node_states=node_states)
        handler.process(self._make_record(), 1, state, {}, {})

        assert handler._node_failure_counts.get("spec:1", 0) == 0

    def test_backoff_clears_on_success(self) -> None:
        node_states = {"spec:1": "in_progress"}
        edges: dict[str, list[str]] = {"spec:1": []}
        graph_sync = GraphSync(node_states, edges)
        block_calls: list = []
        handler = self._make_handler(graph_sync, block_calls)

        state = ExecutionState(plan_hash="abc", node_states=node_states)

        handler.process(self._make_record(), 1, state, {}, {})
        assert handler.is_workspace_backoff_active("spec:1") is True

        graph_sync.node_states["spec:1"] = "in_progress"
        success_record = SessionRecord(
            node_id="spec:1",
            attempt=2,
            status="completed",
            input_tokens=1000,
            output_tokens=500,
            cost=0.50,
            duration_ms=30000,
            error_message=None,
            timestamp="2026-01-01T00:00:00",
        )
        handler.process(success_record, 2, state, {}, {})

        assert handler.is_workspace_backoff_active("spec:1") is False

    def test_backoff_expires_naturally(self) -> None:
        node_states = {"spec:1": "in_progress"}
        edges: dict[str, list[str]] = {"spec:1": []}
        graph_sync = GraphSync(node_states, edges)
        block_calls: list = []
        handler = self._make_handler(graph_sync, block_calls)

        state = ExecutionState(plan_hash="abc", node_states=node_states)
        handler.process(self._make_record(), 1, state, {}, {})

        ns = handler._node_retry_states["spec:1"]
        ns.workspace_next_eligible = time.monotonic() - 1
        assert handler.is_workspace_backoff_active("spec:1") is False
