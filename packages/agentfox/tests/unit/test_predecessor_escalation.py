"""Tests for predecessor retry counting in the retry-predecessor path.

Verifies that reviewer-triggered resets correctly record failures on the
predecessor's failure counter, and block the predecessor when retries
are exhausted.

Test Spec: TS-58-1 through TS-58-8, TS-58-E1, TS-58-E2
Requirements: 58-REQ-1.1 through 58-REQ-3.2
"""

from __future__ import annotations

from unittest.mock import MagicMock

from agentfox.core.config import OrchestratorConfig
from agentfox.engine.engine import Orchestrator
from agentfox.engine.graph_sync import GraphSync
from agentfox.engine.result_handler import SessionResultHandler
from agentfox.engine.state import ExecutionState, SessionRecord
from agentfox.graph.types import Edge, Node, TaskGraph

# ---------------------------------------------------------------------------
# Shared helpers
# ---------------------------------------------------------------------------

#: Default graph: Coder spec:1 -> Verifier spec:2
CODER_VERIFIER_NODES: dict = {
    "spec:1": {"spec_name": "spec", "group_number": 1, "archetype": "coder"},
    "spec:2": {"spec_name": "spec", "group_number": 2, "archetype": "verifier"},
}
CODER_VERIFIER_EDGES: list[dict] = [
    {"source": "spec:1", "target": "spec:2", "kind": "intra_spec"},
]


def _make_orchestrator(
    plan_nodes: dict,
    edges_list: list[dict],
    node_states: dict[str, str],
    *,
    max_retries: int = 5,
) -> tuple[Orchestrator, ExecutionState, dict[str, str | None]]:
    """Create a minimal Orchestrator with pre-built graph and routing state.

    Returns (orchestrator, state, error_tracker).
    """
    config = OrchestratorConfig(max_retries=max_retries)
    orch = Orchestrator(
        config=config,
        session_runner_factory=MagicMock(),
    )

    # Build typed TaskGraph
    typed_nodes = {
        nid: Node(
            id=nid,
            spec_name=n.get("spec_name", ""),
            group_number=n.get("group_number", 0),
            title=n.get("title", nid),
            optional=n.get("optional", False),
            archetype=n.get("archetype", "coder"),
            mode=n.get("mode"),
            instances=n.get("instances", 1),
        )
        for nid, n in plan_nodes.items()
    }
    typed_edges = [Edge(source=e["source"], target=e["target"], kind=e.get("kind", "intra_spec")) for e in edges_list]
    orch._graph = TaskGraph(
        nodes=typed_nodes,
        edges=typed_edges,
        order=list(plan_nodes.keys()),
    )

    # Build edges dict for GraphSync (target -> list of predecessors)
    edges_dict: dict[str, list[str]] = {nid: [] for nid in node_states}
    for edge in edges_list:
        target = edge["target"]
        source = edge["source"]
        if target in edges_dict:
            edges_dict[target].append(source)

    orch._graph_sync = GraphSync(node_states, edges_dict)

    # Initialize result handler (normally done in run())
    orch._result_handler = SessionResultHandler(
        graph_sync=orch._graph_sync,
        max_retries=config.max_retries,
        task_callback=None,
        sink=None,
        run_id="test-run",
        graph=orch._graph,
        archetypes_config=None,
        knowledge_db_conn=None,
        block_task_fn=orch._block_task,
        check_block_budget_fn=orch._check_block_budget,
    )

    state = ExecutionState(
        plan_hash="test",
        node_states=node_states,
        started_at="2024-01-01",
        updated_at="2024-01-01",
    )

    error_tracker: dict[str, str | None] = {}

    return orch, state, error_tracker


def _make_failed_reviewer_record(
    node_id: str = "spec:2",
    attempt: int = 1,
    error_message: str = "Verification failed",
    archetype: str = "verifier",
) -> SessionRecord:
    """Create a failed reviewer (verifier/auditor) session record."""
    return SessionRecord(
        node_id=node_id,
        attempt=attempt,
        status="failed",
        input_tokens=100,
        output_tokens=50,
        cost=0.01,
        duration_ms=5000,
        error_message=error_message,
        timestamp="2024-01-01T00:00:00Z",
        archetype=archetype,
    )


# ---------------------------------------------------------------------------
# TS-58-1: Reviewer Failure Increments Predecessor Failure Counter
# Requirement: 58-REQ-1.1
# ---------------------------------------------------------------------------


class TestReviewerFailureRecordsOnPredCounter:
    """TS-58-1: Reviewer failure increments predecessor failure counter."""

    def test_reviewer_failure_increments_pred_counter(self) -> None:
        """Verify that a reviewer failure increments the predecessor's failure count.

        Test Spec: TS-58-1
        Requirement: 58-REQ-1.1
        """
        node_states = {"spec:1": "completed", "spec:2": "in_progress"}
        orch, state, error_tracker = _make_orchestrator(
            CODER_VERIFIER_NODES, CODER_VERIFIER_EDGES, node_states
        )

        orch._result_handler.process(  # type: ignore[union-attr]
            _make_failed_reviewer_record(),
            1,
            state,
            error_tracker,
        )

        # 58-REQ-1.1: predecessor failure counter must be incremented
        assert orch._result_handler.get_failure_count("spec:1") == 1


# ---------------------------------------------------------------------------
# TS-58-2: Predecessor Reset to Pending After Recorded Failure
# Requirement: 58-REQ-1.2
# ---------------------------------------------------------------------------


class TestPredecessorResetToPending:
    """TS-58-2: Predecessor reset to pending after recorded failure."""

    def test_predecessor_reset_to_pending(self) -> None:
        """Verify predecessor and reviewer are pending when retries are not exhausted.

        Test Spec: TS-58-2
        Requirement: 58-REQ-1.2
        """
        node_states = {"spec:1": "completed", "spec:2": "in_progress"}
        orch, state, error_tracker = _make_orchestrator(
            CODER_VERIFIER_NODES, CODER_VERIFIER_EDGES, node_states
        )

        orch._result_handler.process(  # type: ignore[union-attr]
            _make_failed_reviewer_record(),
            1,
            state,
            error_tracker,
        )

        # 58-REQ-1.2: both nodes must be pending (retries not exhausted)
        assert state.node_states["spec:1"] == "pending"
        assert state.node_states["spec:2"] == "pending"
        # The predecessor's failure counter must have recorded the failure
        assert orch._result_handler.get_failure_count("spec:1") == 1


# ---------------------------------------------------------------------------
# TS-58-3: Predecessor Retries Accumulate
# Requirement: 58-REQ-1.3
# ---------------------------------------------------------------------------


class TestPredecessorRetriesAccumulate:
    """TS-58-3: Predecessor retries accumulate across multiple failures."""

    def test_predecessor_retries_accumulate(self) -> None:
        """Verify predecessor failure count accumulates across failures.

        Test Spec: TS-58-3
        Requirement: 58-REQ-1.3
        """
        node_states = {"spec:1": "completed", "spec:2": "in_progress"}
        orch, state, error_tracker = _make_orchestrator(
            CODER_VERIFIER_NODES, CODER_VERIFIER_EDGES, node_states
        )

        # First failure
        orch._result_handler.process(  # type: ignore[union-attr]
            _make_failed_reviewer_record(attempt=1),
            1,
            state,
            error_tracker,
        )
        assert orch._result_handler.get_failure_count("spec:1") == 1

        # Reset state for the second call
        state.node_states["spec:1"] = "completed"
        state.node_states["spec:2"] = "in_progress"

        # Second failure
        orch._result_handler.process(  # type: ignore[union-attr]
            _make_failed_reviewer_record(attempt=2),
            2,
            state,
            error_tracker,
        )
        assert orch._result_handler.get_failure_count("spec:1") == 2
        # Predecessor should still be pending (not yet past max_retries=5)
        assert state.node_states["spec:1"] == "pending"


# ---------------------------------------------------------------------------
# TS-58-4: Predecessor Blocks on Retries Exhaustion
# Requirement: 58-REQ-2.1
# ---------------------------------------------------------------------------


class TestPredecessorBlocksOnExhaustion:
    """TS-58-4: Verifier blocks when retries exhausted, stopping predecessor retries."""

    def test_verifier_blocks_on_exhaustion(self) -> None:
        """Verify verifier is blocked when retries are exhausted, ending the retry loop.

        Test Spec: TS-58-4
        Requirement: 58-REQ-2.1
        """
        node_states = {"spec:1": "completed", "spec:2": "in_progress"}
        # max_retries=1: blocked after 2nd failure
        orch, state, error_tracker = _make_orchestrator(
            CODER_VERIFIER_NODES, CODER_VERIFIER_EDGES, node_states, max_retries=1
        )

        # First failure — still retrying (predecessor reset to pending)
        orch._result_handler.process(  # type: ignore[union-attr]
            _make_failed_reviewer_record(attempt=1),
            1,
            state,
            error_tracker,
        )
        assert state.node_states["spec:1"] == "pending"

        # Reset state for second call
        state.node_states["spec:1"] = "completed"
        state.node_states["spec:2"] = "in_progress"

        # Second failure — exhausts retries, verifier is blocked
        orch._result_handler.process(  # type: ignore[union-attr]
            _make_failed_reviewer_record(attempt=2),
            2,
            state,
            error_tracker,
        )

        # 58-REQ-2.1: verifier must be blocked (retries exhausted)
        assert state.node_states["spec:2"] == "blocked"


# ---------------------------------------------------------------------------
# TS-58-6: Neither Node Reset When Predecessor Blocks
# Requirement: 58-REQ-2.3
# ---------------------------------------------------------------------------


class TestNeitherNodeResetWhenBlocked:
    """TS-58-6: Neither node reset when retries exhausted."""

    def test_neither_node_reset_when_blocked(self) -> None:
        """Verify that when retries are exhausted, the verifier is blocked.

        Test Spec: TS-58-6
        Requirement: 58-REQ-2.3
        """
        node_states = {"spec:1": "completed", "spec:2": "in_progress"}
        # max_retries=0: immediate exhaustion on first failure
        orch, state, error_tracker = _make_orchestrator(
            CODER_VERIFIER_NODES, CODER_VERIFIER_EDGES, node_states, max_retries=0
        )

        orch._result_handler.process(  # type: ignore[union-attr]
            _make_failed_reviewer_record(attempt=1),
            1,
            state,
            error_tracker,
        )

        # 58-REQ-2.3: verifier blocked, predecessor NOT reset to pending
        assert state.node_states["spec:2"] == "blocked"
        assert state.node_states["spec:1"] != "pending"


# ---------------------------------------------------------------------------
# TS-58-7: Multiple Reviewers Share Predecessor Counter
# Requirement: 58-REQ-3.1
# ---------------------------------------------------------------------------


class TestMultipleReviewersShareCounter:
    """TS-58-7: Multiple reviewers share predecessor failure counter."""

    def test_multiple_reviewers_share_counter(self) -> None:
        """Verify verifier and reviewer:audit-review failures accumulate on the same counter.

        Test Spec: TS-58-7
        Requirement: 58-REQ-3.1
        """
        plan_nodes = {
            "spec:1": {"spec_name": "spec", "group_number": 1, "archetype": "coder"},
            "spec:2": {"spec_name": "spec", "group_number": 2, "archetype": "verifier"},
            "spec:1:reviewer:audit-review": {
                "spec_name": "spec",
                "group_number": 1,
                "archetype": "reviewer",
                "mode": "audit-review",
            },
        }
        edges_list = [
            {"source": "spec:1", "target": "spec:2", "kind": "intra_spec"},
            {"source": "spec:1", "target": "spec:1:reviewer:audit-review", "kind": "intra_spec"},
        ]
        node_states = {
            "spec:1": "completed",
            "spec:2": "in_progress",
            "spec:1:reviewer:audit-review": "pending",
        }

        orch, state, error_tracker = _make_orchestrator(plan_nodes, edges_list, node_states)

        # 1st failure (verifier)
        orch._result_handler.process(  # type: ignore[union-attr]
            _make_failed_reviewer_record("spec:2", 1),
            1,
            state,
            error_tracker,
        )
        assert orch._result_handler.get_failure_count("spec:1") == 1

        # Reset state for reviewer:audit-review
        state.node_states["spec:1"] = "completed"
        state.node_states["spec:1:reviewer:audit-review"] = "in_progress"

        # 2nd failure (reviewer:audit-review)
        orch._result_handler.process(  # type: ignore[union-attr]
            _make_failed_reviewer_record("spec:1:reviewer:audit-review", 1, archetype="reviewer"),
            1,
            state,
            error_tracker,
        )
        assert orch._result_handler.get_failure_count("spec:1") == 2

        # Reset state for verifier again
        state.node_states["spec:1"] = "completed"
        state.node_states["spec:2"] = "in_progress"

        # 3rd failure (verifier again)
        orch._result_handler.process(  # type: ignore[union-attr]
            _make_failed_reviewer_record("spec:2", 2),
            2,
            state,
            error_tracker,
        )
        assert orch._result_handler.get_failure_count("spec:1") == 3


# ---------------------------------------------------------------------------
# TS-58-8: Cumulative Retry Decision
# Requirement: 58-REQ-3.2
# ---------------------------------------------------------------------------


class TestCumulativeRetryDecision:
    """TS-58-8: Cumulative retry decision based on all reviewers."""

    def test_cumulative_retry_decision(self) -> None:
        """Verify retry decision is based on cumulative count, not per-reviewer.

        After max_retries total failures (across all reviewers),
        the predecessor must be blocked.

        Test Spec: TS-58-8
        Requirement: 58-REQ-3.2
        """
        plan_nodes = {
            "spec:1": {"spec_name": "spec", "group_number": 1, "archetype": "coder"},
            "spec:2": {"spec_name": "spec", "group_number": 2, "archetype": "verifier"},
            "spec:1:reviewer:audit-review": {
                "spec_name": "spec",
                "group_number": 1,
                "archetype": "reviewer",
                "mode": "audit-review",
            },
        }
        edges_list = [
            {"source": "spec:1", "target": "spec:2", "kind": "intra_spec"},
            {"source": "spec:1", "target": "spec:1:reviewer:audit-review", "kind": "intra_spec"},
        ]
        node_states = {
            "spec:1": "completed",
            "spec:2": "in_progress",
            "spec:1:reviewer:audit-review": "pending",
        }

        # max_retries=1: blocked after 2nd cumulative failure
        orch, state, error_tracker = _make_orchestrator(
            plan_nodes, edges_list, node_states, max_retries=1
        )

        # 1st failure (verifier): still pending
        orch._result_handler.process(  # type: ignore[union-attr]
            _make_failed_reviewer_record("spec:2", 1),
            1,
            state,
            error_tracker,
        )
        assert state.node_states["spec:1"] == "pending"

        # Reset state for reviewer:audit-review
        state.node_states["spec:1"] = "completed"
        state.node_states["spec:1:reviewer:audit-review"] = "in_progress"

        # 2nd failure (reviewer:audit-review): cumulative count triggers blocking
        orch._result_handler.process(  # type: ignore[union-attr]
            _make_failed_reviewer_record("spec:1:reviewer:audit-review", 1, archetype="reviewer"),
            1,
            state,
            error_tracker,
        )
        assert state.node_states["spec:1"] == "blocked"


# ---------------------------------------------------------------------------
# TS-58-E1: Predecessor Has No Counter (Created Implicitly)
# Requirement: 58-REQ-1.E1
# ---------------------------------------------------------------------------


class TestNoCounterCreatedImplicitly:
    """TS-58-E1: Predecessor has no counter — created implicitly."""

    def test_counter_created_implicitly(self) -> None:
        """Verify a failure counter is created implicitly when none exists.

        Test Spec: TS-58-E1
        Requirement: 58-REQ-1.E1
        """
        node_states = {"spec:1": "completed", "spec:2": "in_progress"}
        orch, state, error_tracker = _make_orchestrator(
            CODER_VERIFIER_NODES, CODER_VERIFIER_EDGES, node_states
        )

        # Confirm no predecessor counter exists before the call
        assert orch._result_handler.get_failure_count("spec:1") == 0

        orch._result_handler.process(  # type: ignore[union-attr]
            _make_failed_reviewer_record(),
            1,
            state,
            error_tracker,
        )

        # 58-REQ-1.E1: a counter must be created for the predecessor
        assert orch._result_handler.get_failure_count("spec:1") == 1


# ---------------------------------------------------------------------------
# TS-58-E2: Predecessor Blocks When Max Retries Reached
# Requirement: 58-REQ-2.E1
# ---------------------------------------------------------------------------


class TestMaxRetriesBlocks:
    """TS-58-E2: Verifier blocks when max retries are reached."""

    def test_max_retries_blocks(self) -> None:
        """Verify the verifier is blocked when max retries are reached.

        Test Spec: TS-58-E2
        Requirement: 58-REQ-2.E1
        """
        node_states = {"spec:1": "completed", "spec:2": "in_progress"}
        # max_retries=1: blocked after 2nd failure
        orch, state, error_tracker = _make_orchestrator(
            CODER_VERIFIER_NODES, CODER_VERIFIER_EDGES, node_states, max_retries=1
        )

        # First failure — still retrying
        orch._result_handler.process(  # type: ignore[union-attr]
            _make_failed_reviewer_record(attempt=1),
            1,
            state,
            error_tracker,
        )
        assert state.node_states["spec:1"] == "pending"

        # Reset state for second call
        state.node_states["spec:1"] = "completed"
        state.node_states["spec:2"] = "in_progress"

        # Second failure — retries exhausted, verifier blocked
        orch._result_handler.process(  # type: ignore[union-attr]
            _make_failed_reviewer_record(attempt=2),
            2,
            state,
            error_tracker,
        )

        # 58-REQ-2.E1: retries exhausted, verifier blocked
        assert state.node_states["spec:2"] == "blocked"
