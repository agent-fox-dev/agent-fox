"""Property tests for the post-mortem module.

Test Spec: TS-126-P1 through TS-126-P8
Properties: Properties 1-8 from design.md
Requirements: 126-REQ-1.1, 126-REQ-1.2, 126-REQ-1.3,
              126-REQ-3.1, 126-REQ-3.2, 126-REQ-3.3,
              126-REQ-4.1, 126-REQ-4.E1,
              126-REQ-5.1, 126-REQ-5.2,
              126-REQ-2.2
"""

from __future__ import annotations

import json
import tempfile
from pathlib import Path

from agentfox.engine.state import ExecutionState, SessionRecord
from hypothesis import given, settings
from hypothesis import strategies as st

# -- Strategies ---------------------------------------------------------------


@st.composite
def session_record_strategy(draw: st.DrawFn) -> SessionRecord:
    """Generate a random SessionRecord for property testing."""
    return SessionRecord(
        node_id=draw(
            st.text(
                alphabet=st.characters(whitelist_categories=("L", "N")),
                min_size=1,
                max_size=20,
            )
        ),
        attempt=draw(st.integers(min_value=1, max_value=10)),
        status=draw(st.sampled_from(["completed", "failed"])),
        input_tokens=draw(st.integers(min_value=0, max_value=1_000_000)),
        output_tokens=draw(st.integers(min_value=0, max_value=1_000_000)),
        cost=draw(
            st.floats(
                min_value=0.0,
                max_value=100.0,
                allow_nan=False,
                allow_infinity=False,
            )
        ),
        duration_ms=draw(st.integers(min_value=0, max_value=600_000)),
        error_message=draw(
            st.one_of(st.none(), st.text(min_size=1, max_size=50))
        ),
        timestamp="2026-06-03T10:00:00+00:00",
        model=draw(
            st.sampled_from(
                ["claude-sonnet-4-6", "claude-opus-4", "claude-haiku-3.5"]
            )
        ),
        archetype=draw(st.sampled_from(["coder", "skeptic", "verifier"])),
        is_transport_error=draw(st.booleans()),
        is_budget_exhausted=draw(st.booleans()),
        is_non_retryable=draw(st.booleans()),
    )


@st.composite
def execution_state_strategy(draw: st.DrawFn) -> ExecutionState:
    """Generate a random ExecutionState for property testing.

    Generates node_states with a mix of statuses, blocked_reasons
    that may or may not cover all blocked nodes, and arbitrary
    cost/token values.
    """
    statuses = ["completed", "pending", "blocked", "failed", "in_progress"]
    node_states = draw(
        st.dictionaries(
            keys=st.text(
                alphabet=st.characters(whitelist_categories=("L", "N")),
                min_size=1,
                max_size=20,
            ),
            values=st.sampled_from(statuses),
            min_size=0,
            max_size=20,
        )
    )

    # Generate blocked_reasons: some blocked nodes may have reasons, some not
    blocked_nodes = [nid for nid, s in node_states.items() if s == "blocked"]
    blocked_reasons: dict[str, str] = {}
    for nid in blocked_nodes:
        if draw(st.booleans()):
            blocked_reasons[nid] = draw(st.text(min_size=1, max_size=50))

    session_history = draw(
        st.lists(session_record_strategy(), min_size=0, max_size=10)
    )

    return ExecutionState(
        plan_hash=draw(st.text(min_size=1, max_size=10)),
        node_states=node_states,
        session_history=session_history,
        total_input_tokens=draw(
            st.integers(min_value=0, max_value=10_000_000)
        ),
        total_output_tokens=draw(
            st.integers(min_value=0, max_value=10_000_000)
        ),
        total_cost=draw(
            st.floats(
                min_value=0.0,
                max_value=1000.0,
                allow_nan=False,
                allow_infinity=False,
            )
        ),
        total_sessions=draw(st.integers(min_value=0, max_value=100)),
        started_at="2026-06-03T10:00:00+00:00",
        updated_at="2026-06-03T10:15:00+00:00",
        run_status=draw(
            st.sampled_from(
                ["stalled", "block_limit", "cost_limit", "session_limit"]
            )
        ),
        blocked_reasons=blocked_reasons,
        run_id=draw(
            st.text(
                alphabet=st.characters(
                    whitelist_categories=("L", "N"),
                    whitelist_characters="_",
                ),
                min_size=1,
                max_size=30,
            )
        ),
    )


# -- TS-126-P1: Trigger completeness -----------------------------------------


class TestTriggerCompleteness:
    """TS-126-P1: For any trigger status, should_dump returns True.

    Property 1 from design.md.
    Validates: 126-REQ-1.1
    """

    @given(
        status=st.sampled_from(
            ["stalled", "block_limit", "cost_limit", "session_limit"]
        ),
    )
    @settings(max_examples=50)
    def test_trigger_completeness(self, status: str) -> None:
        """should_dump returns True for every trigger status."""
        from agentfox.engine.run import should_dump

        state = ExecutionState(
            plan_hash="h", node_states={}, run_status=status
        )
        assert should_dump(state) is True


# -- TS-126-P2: No false triggers --------------------------------------------


class TestNoFalseTriggers:
    """TS-126-P2: For any non-trigger status, should_dump returns False.

    Property 2 from design.md.
    Validates: 126-REQ-1.2, 126-REQ-1.3
    """

    @given(
        status=st.sampled_from(["completed", "interrupted", "running"]),
    )
    @settings(max_examples=50)
    def test_no_false_triggers(self, status: str) -> None:
        """should_dump returns False for every non-trigger status."""
        from agentfox.engine.run import should_dump

        state = ExecutionState(
            plan_hash="h", node_states={}, run_status=status
        )
        assert should_dump(state) is False


# -- TS-126-P3: Schema completeness -------------------------------------------


class TestSchemaCompleteness:
    """TS-126-P3: build_postmortem produces a dict with all required keys.

    Property 3 from design.md.
    Validates: 126-REQ-3.1, 126-REQ-3.2
    """

    @given(state=execution_state_strategy())
    @settings(max_examples=50)
    def test_schema_completeness(self, state: ExecutionState) -> None:
        """All required keys present and schema_version == 1."""
        from agentfox.engine.run import build_postmortem

        result = build_postmortem(state)
        required = {
            "schema_version",
            "run_id",
            "run_status",
            "started_at",
            "completed_at",
            "task_summary",
            "cost_summary",
            "blocked_tasks",
            "session_history",
        }
        assert required.issubset(set(result.keys()))
        assert result["schema_version"] == 1


# -- TS-126-P4: Blocked task fidelity ----------------------------------------


class TestBlockedTaskFidelity:
    """TS-126-P4: blocked_tasks array has one entry per blocked node.

    Property 4 from design.md.
    Validates: 126-REQ-4.1, 126-REQ-4.E1
    """

    @given(state=execution_state_strategy())
    @settings(max_examples=50)
    def test_blocked_task_fidelity(self, state: ExecutionState) -> None:
        """Blocked task count matches node_states; each has non-empty fields."""
        from agentfox.engine.run import build_postmortem

        result = build_postmortem(state)
        blocked_count = sum(
            1 for s in state.node_states.values() if s == "blocked"
        )
        assert len(result["blocked_tasks"]) == blocked_count
        for entry in result["blocked_tasks"]:
            assert len(entry["node_id"]) > 0
            assert len(entry["reason"]) > 0


# -- TS-126-P5: Session history fidelity --------------------------------------


class TestSessionHistoryFidelity:
    """TS-126-P5: session_history array length matches state.session_history.

    Property 5 from design.md.
    Validates: 126-REQ-5.1
    """

    @given(state=execution_state_strategy())
    @settings(max_examples=50)
    def test_session_history_fidelity(self, state: ExecutionState) -> None:
        """session_history length matches state."""
        from agentfox.engine.run import build_postmortem

        result = build_postmortem(state)
        assert len(result["session_history"]) == len(state.session_history)


# -- TS-126-P6: Cost summary accuracy ----------------------------------------


class TestCostSummaryAccuracy:
    """TS-126-P6: cost_summary fields equal state aggregate values.

    Property 6 from design.md.
    Validates: 126-REQ-5.2
    """

    @given(state=execution_state_strategy())
    @settings(max_examples=50)
    def test_cost_summary_accuracy(self, state: ExecutionState) -> None:
        """cost_summary matches state totals exactly."""
        from agentfox.engine.run import build_postmortem

        result = build_postmortem(state)
        assert (
            result["cost_summary"]["total_cost_usd"] == state.total_cost
        )
        assert (
            result["cost_summary"]["total_input_tokens"]
            == state.total_input_tokens
        )
        assert (
            result["cost_summary"]["total_output_tokens"]
            == state.total_output_tokens
        )
        assert (
            result["cost_summary"]["total_sessions"] == state.total_sessions
        )


# -- TS-126-P7: File round-trip -----------------------------------------------


class TestFileRoundTrip:
    """TS-126-P7: Writing and reading back produces identical dict.

    Property 7 from design.md.
    Validates: 126-REQ-2.2
    """

    @given(state=execution_state_strategy())
    @settings(max_examples=20)
    def test_file_round_trip(self, state: ExecutionState) -> None:
        """json.loads(path.read_text()) == postmortem."""
        from agentfox.engine.run import build_postmortem, write_postmortem

        with tempfile.TemporaryDirectory() as tmp_dir:
            pm = build_postmortem(state)
            path = write_postmortem(pm, Path(tmp_dir))
            parsed = json.loads(path.read_text())
            assert parsed == pm


# -- TS-126-P8: Task summary accuracy -----------------------------------------


class TestTaskSummaryAccuracy:
    """TS-126-P8: task_summary.total equals len(node_states).

    Property 8 from design.md.
    Validates: 126-REQ-3.3
    """

    @given(state=execution_state_strategy())
    @settings(max_examples=50)
    def test_task_summary_accuracy(self, state: ExecutionState) -> None:
        """total == len(node_states) and status counts sum <= total."""
        from agentfox.engine.run import build_postmortem

        result = build_postmortem(state)
        ts = result["task_summary"]
        assert ts["total"] == len(state.node_states)
        count_sum = (
            ts["completed"]
            + ts["pending"]
            + ts["blocked"]
            + ts["failed"]
            + ts["in_progress"]
        )
        # Note: sum may be <= total if node_states contains statuses outside
        # the five named buckets (e.g. "deferred"). See test_spec.md TS-126-P8.
        assert count_sum <= ts["total"]
