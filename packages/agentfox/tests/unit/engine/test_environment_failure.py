"""Tests for zero-turn environment failure handling.

Verifies that sessions that crash before any LLM work (0 tokens, $0 cost)
are classified as environment failures and handled with exponential backoff
instead of consuming the retry counter.

Fixes: #656
"""

from __future__ import annotations

from typing import Any
from unittest.mock import MagicMock

from agentfox.engine.graph_sync import GraphSync
from agentfox.engine.result_handler import (
    _MAX_ENVIRONMENT_FAILURES,
    SessionResultHandler,
)
from agentfox.engine.state import ExecutionState, SessionRecord


def _make_handler(
    *,
    max_retries: int = 2,
    sink: Any = None,
) -> tuple[SessionResultHandler, ExecutionState, dict[str, int], dict[str, str | None]]:
    graph_sync = GraphSync({"node1": "in_progress"}, {"node1": []})

    handler = SessionResultHandler(
        graph_sync=graph_sync,
        max_retries=max_retries,
        task_callback=None,
        sink=sink,
        run_id="test-run",
        graph=None,
        archetypes_config=None,
        knowledge_db_conn=None,
        block_task_fn=lambda nid, st, reason: None,
        check_block_budget_fn=lambda st: False,
    )

    state = ExecutionState(plan_hash="test", node_states={"node1": "in_progress"})
    attempt_tracker: dict[str, int] = {}
    error_tracker: dict[str, str | None] = {}

    return handler, state, attempt_tracker, error_tracker


def _make_env_failure_record(node_id: str = "node1", attempt: int = 1) -> SessionRecord:
    return SessionRecord(
        node_id=node_id,
        attempt=attempt,
        status="failed",
        input_tokens=0,
        output_tokens=0,
        cost=0.0,
        duration_ms=3000,
        error_message="error_during_execution",
        timestamp="2026-07-01T10:37:11Z",
    )


def _make_normal_failure_record(node_id: str = "node1", attempt: int = 1) -> SessionRecord:
    return SessionRecord(
        node_id=node_id,
        attempt=attempt,
        status="failed",
        input_tokens=5000,
        output_tokens=1200,
        cost=0.03,
        duration_ms=45000,
        error_message="make check failed",
        timestamp="2026-07-01T10:37:11Z",
    )


class _EventCaptureSink:
    def __init__(self) -> None:
        self.events: list[Any] = []

    def emit_audit_event(self, event: object) -> None:
        self.events.append(event)

    def record_session_outcome(self, outcome: object) -> None:
        pass

    def record_tool_call(self, call: object) -> None:
        pass

    def record_tool_error(self, error: object) -> None:
        pass

    def close(self) -> None:
        pass

    def find_events(self, event_type: object) -> list[Any]:
        return [e for e in self.events if e.event_type == event_type]


class TestSessionRecordIsEnvironmentFailure:

    def test_zero_token_zero_cost_failed_is_env_failure(self) -> None:
        record = _make_env_failure_record()
        assert record.is_environment_failure is True

    def test_normal_failure_is_not_env_failure(self) -> None:
        record = _make_normal_failure_record()
        assert record.is_environment_failure is False

    def test_completed_session_is_not_env_failure(self) -> None:
        record = SessionRecord(
            node_id="node1", attempt=1, status="completed",
            input_tokens=0, output_tokens=0, cost=0.0,
            duration_ms=100, error_message=None, timestamp="2026-07-01T10:00:00Z",
        )
        assert record.is_environment_failure is False

    def test_workspace_setup_failure_is_not_env_failure(self) -> None:
        record = SessionRecord(
            node_id="node1", attempt=1, status="failed",
            input_tokens=0, output_tokens=0, cost=0.0,
            duration_ms=100, error_message="worktree error", timestamp="2026-07-01T10:00:00Z",
            is_workspace_setup_failure=True,
        )
        assert record.is_environment_failure is False

    def test_transport_error_is_not_env_failure(self) -> None:
        record = SessionRecord(
            node_id="node1", attempt=1, status="failed",
            input_tokens=0, output_tokens=0, cost=0.0,
            duration_ms=100, error_message="connection reset", timestamp="2026-07-01T10:00:00Z",
            is_transport_error=True,
        )
        assert record.is_environment_failure is False


class TestEnvironmentFailureDoesNotConsumeRetries:

    def test_env_failure_does_not_consume_retry_counter(self) -> None:
        handler, state, at, et = _make_handler(max_retries=2)
        record = _make_env_failure_record()
        handler.process(record, attempt=1, state=state, attempt_tracker=at, error_tracker=et)
        assert handler._node_failure_counts.get("node1", 0) == 0

    def test_normal_failure_does_consume_retry_counter(self) -> None:
        handler, state, at, et = _make_handler(max_retries=2)
        record = _make_normal_failure_record()
        handler.process(record, attempt=1, state=state, attempt_tracker=at, error_tracker=et)
        assert handler._node_failure_counts.get("node1", 0) == 1


class TestEnvironmentFailureBackoff:

    def test_first_env_failure_sets_backoff(self) -> None:
        handler, state, at, et = _make_handler()
        record = _make_env_failure_record()
        handler.process(record, attempt=1, state=state, attempt_tracker=at, error_tracker=et)
        ns = handler._get_node_state("node1")
        assert ns.environment_failures == 1
        assert ns.environment_next_eligible > 0

    def test_env_failure_backoff_is_active(self) -> None:
        handler, state, at, et = _make_handler()
        record = _make_env_failure_record()
        handler.process(record, attempt=1, state=state, attempt_tracker=at, error_tracker=et)
        assert handler.is_environment_backoff_active("node1") is True


class TestEnvironmentFailureCircuitBreaker:

    def test_blocks_after_max_consecutive_failures(self) -> None:
        blocked_nodes: list[str] = []

        def block_fn(nid: str, st: ExecutionState, reason: str) -> None:
            blocked_nodes.append(nid)

        graph_sync = GraphSync({"node1": "in_progress"}, {"node1": []})
        handler = SessionResultHandler(
            graph_sync=graph_sync,
            max_retries=10,
            task_callback=None,
            sink=None,
            run_id="test-run",
            graph=None,
            archetypes_config=None,
            knowledge_db_conn=None,
            block_task_fn=block_fn,
            check_block_budget_fn=lambda st: False,
        )
        state = ExecutionState(plan_hash="test", node_states={"node1": "in_progress"})
        at: dict[str, int] = {}
        et: dict[str, str | None] = {}

        for i in range(_MAX_ENVIRONMENT_FAILURES):
            graph_sync.node_states["node1"] = "in_progress"
            record = _make_env_failure_record(attempt=i + 1)
            handler.process(record, attempt=i + 1, state=state, attempt_tracker=at, error_tracker=et)

        assert "node1" in blocked_nodes
        assert handler._node_failure_counts.get("node1", 0) == 0


class TestEnvironmentFailureAuditEvent:

    def test_emits_audit_event(self) -> None:
        from afaudit.events import AuditEventType
        from afaudit.sink import SinkDispatcher

        capture = _EventCaptureSink()
        sink = SinkDispatcher([capture])  # type: ignore[list-item]

        handler, state, at, et = _make_handler(sink=sink)
        record = _make_env_failure_record()
        handler.process(record, attempt=1, state=state, attempt_tracker=at, error_tracker=et)

        env_events = capture.find_events(AuditEventType.SESSION_ENVIRONMENT_FAILURE)
        assert len(env_events) >= 1
        payload = env_events[0].payload
        assert payload["consecutive_failures"] == 1
        assert payload["blocked"] is False


class TestEnvironmentFailureResetsOnSuccess:

    def test_success_resets_counter(self) -> None:
        handler, state, at, et = _make_handler()

        fail_record = _make_env_failure_record()
        handler.process(fail_record, attempt=1, state=state, attempt_tracker=at, error_tracker=et)
        assert handler._get_node_state("node1").environment_failures == 1

        success_record = SessionRecord(
            node_id="node1", attempt=2, status="completed",
            input_tokens=5000, output_tokens=1200, cost=0.03,
            duration_ms=45000, error_message=None, timestamp="2026-07-01T10:38:00Z",
        )
        handler._graph_sync.node_states["node1"] = "in_progress"
        handler.process(success_record, attempt=2, state=state, attempt_tracker=at, error_tracker=et)

        assert handler._get_node_state("node1").environment_failures == 0
