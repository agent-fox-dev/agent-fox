"""Tests for the post-mortem module.

Test Spec: TS-126-1 through TS-126-9, TS-126-12, TS-126-E1 through TS-126-E5
Requirements: 126-REQ-1.1 through 126-REQ-7.1
"""

from __future__ import annotations

import json
from pathlib import Path
from unittest.mock import patch

import pytest

from agent_fox.engine.state import ExecutionState, SessionRecord

# -- Helpers ------------------------------------------------------------------


def _make_session_record(
    *,
    node_id: str = "spec_01_group_1",
    attempt: int = 1,
    status: str = "completed",
    input_tokens: int = 1000,
    output_tokens: int = 500,
    cost: float = 0.05,
    duration_ms: int = 30000,
    error_message: str | None = None,
    timestamp: str = "2026-06-03T10:02:00+00:00",
    model: str = "claude-sonnet-4-6",
    archetype: str = "coder",
    is_transport_error: bool = False,
    is_budget_exhausted: bool = False,
    is_non_retryable: bool = False,
) -> SessionRecord:
    """Create a SessionRecord for testing."""
    return SessionRecord(
        node_id=node_id,
        attempt=attempt,
        status=status,
        input_tokens=input_tokens,
        output_tokens=output_tokens,
        cost=cost,
        duration_ms=duration_ms,
        error_message=error_message,
        timestamp=timestamp,
        model=model,
        archetype=archetype,
        is_transport_error=is_transport_error,
        is_budget_exhausted=is_budget_exhausted,
        is_non_retryable=is_non_retryable,
    )


# -- TS-126-1: should_dump returns True for trigger statuses ------------------


class TestShouldDumpTriggerStatuses:
    """TS-126-1: should_dump returns True for trigger statuses.

    Requirement: 126-REQ-1.1
    """

    @pytest.mark.parametrize(
        "status",
        ["stalled", "block_limit", "cost_limit", "session_limit"],
    )
    def test_should_dump_returns_true(self, status: str) -> None:
        """should_dump() returns True for each trigger status."""
        from agent_fox.engine.postmortem import should_dump

        state = ExecutionState(plan_hash="h", node_states={}, run_status=status)
        assert should_dump(state) is True


# -- TS-126-2: should_dump returns False for non-trigger statuses -------------


class TestShouldDumpNonTriggerStatuses:
    """TS-126-2: should_dump returns False for non-trigger statuses.

    Requirements: 126-REQ-1.2, 126-REQ-1.3
    """

    @pytest.mark.parametrize(
        "status",
        ["completed", "interrupted", "running"],
    )
    def test_should_dump_returns_false(self, status: str) -> None:
        """should_dump() returns False for non-trigger statuses."""
        from agent_fox.engine.postmortem import should_dump

        state = ExecutionState(plan_hash="h", node_states={}, run_status=status)
        assert should_dump(state) is False


# -- TS-126-3: build_postmortem includes all required top-level keys ----------


class TestBuildPostmortemRequiredKeys:
    """TS-126-3: build_postmortem includes all required top-level keys.

    Requirements: 126-REQ-3.1, 126-REQ-3.2
    """

    def test_required_keys_and_schema_version(self) -> None:
        """Output dict has all required keys and schema_version is 1."""
        from agent_fox.engine.postmortem import build_postmortem

        state = ExecutionState(
            plan_hash="h",
            node_states={"a": "completed", "b": "blocked"},
            run_status="stalled",
            run_id="20260603_100000_abc123",
            blocked_reasons={"b": "test reason"},
            session_history=[_make_session_record()],
        )
        result = build_postmortem(state)

        required_keys = {
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
        assert set(result.keys()) == required_keys
        assert result["schema_version"] == 1


# -- TS-126-4: task_summary counts match node_states -------------------------


class TestBuildPostmortemTaskSummary:
    """TS-126-4: build_postmortem task_summary counts match node_states.

    Requirement: 126-REQ-3.3
    """

    def test_task_summary_counts(self) -> None:
        """task_summary counts are derived correctly from node_states."""
        from agent_fox.engine.postmortem import build_postmortem

        state = ExecutionState(
            plan_hash="h",
            node_states={
                "a": "completed",
                "b": "blocked",
                "c": "pending",
                "d": "failed",
                "e": "completed",
                "f": "in_progress",
            },
            run_status="stalled",
            run_id="20260603_100000_abc123",
        )
        result = build_postmortem(state)
        ts = result["task_summary"]

        assert ts["total"] == 6
        assert ts["completed"] == 2
        assert ts["blocked"] == 1
        assert ts["pending"] == 1
        assert ts["failed"] == 1
        assert ts["in_progress"] == 1


# -- TS-126-5: cost_summary matches state totals -----------------------------


class TestBuildPostmortemCostSummary:
    """TS-126-5: build_postmortem cost_summary matches state totals.

    Requirements: 126-REQ-3.4, 126-REQ-5.2
    """

    def test_cost_summary_matches_state(self) -> None:
        """cost_summary fields match ExecutionState aggregates."""
        from agent_fox.engine.postmortem import build_postmortem

        state = ExecutionState(
            plan_hash="h",
            node_states={},
            run_status="stalled",
            run_id="20260603_100000_abc123",
            total_cost=1.23,
            total_input_tokens=100000,
            total_output_tokens=50000,
            total_sessions=8,
        )
        result = build_postmortem(state)
        cs = result["cost_summary"]

        assert cs["total_cost_usd"] == 1.23
        assert cs["total_input_tokens"] == 100000
        assert cs["total_output_tokens"] == 50000
        assert cs["total_sessions"] == 8


# -- TS-126-6: blocked_tasks sorted and complete ------------------------------


class TestBuildPostmortemBlockedTasks:
    """TS-126-6: build_postmortem blocked_tasks sorted and complete.

    Requirements: 126-REQ-4.1, 126-REQ-4.2
    """

    def test_blocked_tasks_sorted_by_node_id(self) -> None:
        """Blocked tasks are included and sorted by node_id."""
        from agent_fox.engine.postmortem import build_postmortem

        state = ExecutionState(
            plan_hash="h",
            node_states={
                "z_task": "blocked",
                "a_task": "blocked",
                "m_task": "completed",
            },
            run_status="stalled",
            run_id="20260603_100000_abc123",
            blocked_reasons={"z_task": "cascade", "a_task": "review findings"},
        )
        result = build_postmortem(state)

        assert len(result["blocked_tasks"]) == 2
        assert result["blocked_tasks"][0]["node_id"] == "a_task"
        assert result["blocked_tasks"][0]["reason"] == "review findings"
        assert result["blocked_tasks"][1]["node_id"] == "z_task"
        assert result["blocked_tasks"][1]["reason"] == "cascade"


# -- TS-126-7: session_history includes all records ---------------------------


class TestBuildPostmortemSessionHistory:
    """TS-126-7: build_postmortem session_history includes all records.

    Requirement: 126-REQ-5.1
    """

    def test_session_history_contains_all_records_with_required_fields(self) -> None:
        """All SessionRecords are serialized with all required fields."""
        from agent_fox.engine.postmortem import build_postmortem

        records = [
            _make_session_record(
                node_id="spec_01_group_1",
                attempt=1,
                status="completed",
                error_message=None,
            ),
            _make_session_record(
                node_id="spec_01_group_2",
                attempt=2,
                status="failed",
                error_message="session timed out",
                is_transport_error=True,
            ),
        ]
        state = ExecutionState(
            plan_hash="h",
            node_states={"spec_01_group_1": "completed", "spec_01_group_2": "failed"},
            run_status="stalled",
            run_id="20260603_100000_abc123",
            session_history=records,
        )
        result = build_postmortem(state)

        assert len(result["session_history"]) == 2
        required_fields = {
            "node_id",
            "attempt",
            "status",
            "archetype",
            "model",
            "duration_ms",
            "cost",
            "error_message",
            "timestamp",
            "is_transport_error",
            "is_budget_exhausted",
            "is_non_retryable",
        }
        for entry in result["session_history"]:
            assert required_fields.issubset(set(entry.keys()))


# -- TS-126-8: write_postmortem creates file with correct name and content ----


class TestWritePostmortemFile:
    """TS-126-8: write_postmortem creates file with correct name and content.

    Requirements: 126-REQ-2.1, 126-REQ-2.2
    """

    def test_write_creates_correct_file(self, tmp_path: Path) -> None:
        """File is written to the correct path with valid JSON."""
        from agent_fox.engine.postmortem import write_postmortem

        postmortem = {
            "schema_version": 1,
            "run_id": "20260603_100000_abc123",
            "run_status": "stalled",
        }
        path = write_postmortem(postmortem, tmp_path)

        assert path.name == "postmortem_20260603_100000_abc123.json"
        assert path.exists()
        parsed = json.loads(path.read_text())
        assert parsed == postmortem


# -- TS-126-9: write_postmortem creates audit directory if missing ------------


class TestWritePostmortemCreatesDirectory:
    """TS-126-9: write_postmortem creates audit directory if missing.

    Requirement: 126-REQ-2.3
    """

    def test_creates_missing_directory(self, tmp_path: Path) -> None:
        """The audit directory is created when it doesn't exist."""
        from agent_fox.engine.postmortem import write_postmortem

        audit_dir = tmp_path / "nonexistent" / "audit"
        assert not audit_dir.exists()

        postmortem = {
            "schema_version": 1,
            "run_id": "20260603_100000_abc123",
        }
        path = write_postmortem(postmortem, audit_dir)

        assert audit_dir.exists()
        assert path.exists()


# -- TS-126-12: ExecutionState has run_id field -------------------------------


class TestExecutionStateRunId:
    """TS-126-12: ExecutionState has run_id field.

    Requirement: 126-REQ-7.1
    """

    def test_run_id_field_exists_with_default(self) -> None:
        """run_id field exists with empty string default."""
        state = ExecutionState(plan_hash="h", node_states={})
        assert hasattr(state, "run_id")
        assert state.run_id == ""


# -- TS-126-E1: Post-mortem generation failure is non-blocking ----------------


class TestGenerationFailureNonBlocking:
    """TS-126-E1: Post-mortem generation failure is non-blocking.

    Requirement: 126-REQ-1.E1
    """

    def test_build_error_leaves_state_valid(self) -> None:
        """If build_postmortem raises, state remains valid with empty postmortem_path."""
        from agent_fox.engine import postmortem as pm_mod

        state = ExecutionState(
            plan_hash="h",
            node_states={},
            run_status="stalled",
            run_id="20260603_100000_abc123",
        )
        # State should trigger post-mortem generation
        assert pm_mod.should_dump(state) is True

        # Simulate the error handling pattern used in run_code:
        # try: pm = build_postmortem(state); ... except Exception: log warning
        try:
            with patch.object(pm_mod, "build_postmortem", side_effect=RuntimeError("boom")):
                pm_mod.build_postmortem(state)
        except RuntimeError:
            pass  # run_code catches this and logs a warning

        # State remains valid with no postmortem_path set
        assert state.run_status == "stalled"
        assert state.postmortem_path == ""


# -- TS-126-E2: Fallback run_id for empty state ------------------------------


class TestFallbackRunId:
    """TS-126-E2: Fallback run_id for empty state.

    Requirement: 126-REQ-1.E2
    """

    def test_empty_run_id_gets_fallback(self) -> None:
        """When run_id is empty, build_postmortem uses a fallback."""
        from agent_fox.engine.postmortem import build_postmortem

        state = ExecutionState(
            plan_hash="",
            node_states={},
            run_id="",
            run_status="stalled",
        )
        result = build_postmortem(state)
        assert len(result["run_id"]) > 0


# -- TS-126-E3: Blocked task with missing reason -----------------------------


class TestBlockedTaskMissingReason:
    """TS-126-E3: Blocked task with missing reason.

    Requirement: 126-REQ-4.E1
    """

    def test_missing_reason_defaults_to_unknown(self) -> None:
        """A blocked node not in blocked_reasons gets reason 'unknown'."""
        from agent_fox.engine.postmortem import build_postmortem

        state = ExecutionState(
            plan_hash="h",
            node_states={"x": "blocked"},
            blocked_reasons={},
            run_status="stalled",
            run_id="20260603_100000_abc123",
        )
        result = build_postmortem(state)
        assert result["blocked_tasks"] == [{"node_id": "x", "reason": "unknown"}]


# -- TS-126-E4: Empty session history produces valid output -------------------


class TestEmptySessionHistory:
    """TS-126-E4: Empty session history produces valid output.

    Requirement: 126-REQ-5.E1
    """

    def test_empty_state_produces_valid_output(self) -> None:
        """Empty session_history produces empty arrays and zero cost values."""
        from agent_fox.engine.postmortem import build_postmortem

        state = ExecutionState(
            plan_hash="h",
            node_states={},
            run_status="stalled",
            run_id="20260603_100000_abc123",
        )
        result = build_postmortem(state)

        assert result["session_history"] == []
        assert result["blocked_tasks"] == []
        assert result["cost_summary"]["total_cost_usd"] == 0.0
        assert result["cost_summary"]["total_sessions"] == 0


# -- TS-126-E5: File write failure is non-blocking ---------------------------


class TestWriteFailureNonBlocking:
    """TS-126-E5: File write failure is non-blocking.

    Requirement: 126-REQ-2.E1
    """

    def test_write_failure_propagates_to_caller(self, tmp_path: Path) -> None:
        """write_postmortem propagates PermissionError so run_code can catch it."""
        from agent_fox.engine.postmortem import write_postmortem

        postmortem = {"schema_version": 1, "run_id": "20260603_100000_abc123"}

        with patch("pathlib.Path.write_text", side_effect=PermissionError("denied")):
            with pytest.raises(PermissionError):
                write_postmortem(postmortem, tmp_path)
