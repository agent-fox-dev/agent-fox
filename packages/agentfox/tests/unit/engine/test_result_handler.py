"""Result handler non-retryable error classification tests.

Test Spec: TS-118-8 (result handler blocks immediately on non-retryable)
Requirements: 118-REQ-3.2, 118-REQ-3.3
"""

from __future__ import annotations

from agentfox.engine.graph_sync import GraphSync
from agentfox.engine.result_handler import SessionResultHandler
from agentfox.engine.state import ExecutionState, SessionRecord


class TestNonRetryableImmediateBlock:
    """TS-118-8: result handler blocks immediately on non-retryable error.

    Requirements: 118-REQ-3.2, 118-REQ-3.3
    """

    def _make_handler(
        self,
        graph_sync: GraphSync,
        block_calls: list,
    ) -> SessionResultHandler:
        """Create a SessionResultHandler with mocked dependencies."""

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

    def test_nonretryable_blocks_immediately(self) -> None:
        """Non-retryable errors block the node immediately without consuming
        retry-counter attempts, with 'workspace-state' in reason."""
        node_states = {"spec:1": "in_progress"}
        edges: dict[str, list[str]] = {"spec:1": []}
        graph_sync = GraphSync(node_states, edges)

        block_calls: list[tuple[str, str]] = []
        handler = self._make_handler(graph_sync, block_calls)

        record = SessionRecord(
            node_id="spec:1",
            attempt=1,
            status="failed",
            input_tokens=100,
            output_tokens=200,
            cost=0.10,
            duration_ms=5000,
            error_message="Divergent untracked files",
            timestamp="2026-01-01T00:00:00",
            is_non_retryable=True,
        )

        state = ExecutionState(
            plan_hash="abc123",
            node_states=node_states,
        )

        error_tracker: dict[str, str | None] = {}

        handler.process(record, 1, state, error_tracker)

        # Node must be blocked
        assert node_states["spec:1"] == "blocked"

        # Blocked reason must contain "workspace-state"
        assert len(block_calls) == 1
        assert "workspace-state" in block_calls[0][1]

        # Failure counter must NOT have been incremented
        # (the non-retryable path blocks immediately without counting)
        assert handler.get_failure_count("spec:1") == 0
