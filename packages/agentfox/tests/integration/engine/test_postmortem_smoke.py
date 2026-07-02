"""Integration smoke tests for abort post-mortem dump.

Test Spec: TS-126-SMOKE-1 through TS-126-SMOKE-3
Requirements: 126-REQ-1.1, 126-REQ-1.2, 126-REQ-6.1, 126-REQ-7.2

These tests exercise the real post-mortem module (no mocking of
build_postmortem, write_postmortem, or should_dump) to verify
end-to-end wiring.
"""

from __future__ import annotations

import json
from pathlib import Path

import click
from afaudit.postmortem import build_postmortem, should_dump, write_postmortem
from agentfox.engine.state import ExecutionState, SessionRecord

# -- Helpers ------------------------------------------------------------------


def _make_session_record(
    *,
    node_id: str = "spec_01_group_1",
    attempt: int = 1,
    status: str = "completed",
) -> SessionRecord:
    """Create a minimal SessionRecord for testing."""
    return SessionRecord(
        node_id=node_id,
        attempt=attempt,
        status=status,
        input_tokens=1000,
        output_tokens=500,
        cost=0.05,
        duration_ms=30000,
        error_message=None,
        timestamp="2026-06-03T10:02:00+00:00",
        model="claude-sonnet-4-6",
        archetype="coder",
        is_transport_error=False,
        is_budget_exhausted=False,
        is_non_retryable=False,
    )


# -- TS-126-SMOKE-1: Post-mortem generated on stalled run ---------------------


def test_postmortem_generated_on_stalled_run(tmp_path: Path) -> None:
    """TS-126-SMOKE-1: A complete stalled run produces a valid post-mortem file.

    Execution Path: Path 1 from design.md
    Must NOT satisfy with: Mocking build_postmortem or write_postmortem.

    Requirements: 126-REQ-1.1, 126-REQ-7.2
    """
    # Build an ExecutionState that simulates a stalled run with blocked tasks
    state = ExecutionState(
        plan_hash="abc123",
        node_states={
            "spec_01_group_1": "completed",
            "spec_01_group_2": "blocked",
            "spec_01_group_3": "blocked",
        },
        run_status="stalled",
        run_id="20260603_100000_smoke1",
        started_at="2026-06-03T10:00:00+00:00",
        updated_at="2026-06-03T10:15:00+00:00",
        total_cost=0.15,
        total_input_tokens=5000,
        total_output_tokens=2000,
        total_sessions=2,
        blocked_reasons={
            "spec_01_group_2": "review-blocking: critical findings (2 critical)",
            "spec_01_group_3": "cascade: spec_01_group_2 blocked",
        },
        session_history=[
            _make_session_record(node_id="spec_01_group_1"),
            _make_session_record(node_id="spec_01_group_2", status="failed", attempt=2),
        ],
    )

    # Exercise the real post-mortem pipeline (no mocking)
    assert should_dump(state) is True

    pm = build_postmortem(state)
    audit_dir = tmp_path / "audit"
    pm_path = write_postmortem(pm, audit_dir)
    state.postmortem_path = str(pm_path)

    # Assertions per test spec
    assert state.postmortem_path != ""
    assert pm_path.exists()

    parsed = json.loads(pm_path.read_text())
    assert parsed["schema_version"] == 1
    assert parsed["run_status"] == "stalled"
    assert parsed["run_id"] == "20260603_100000_smoke1"
    assert len(parsed["blocked_tasks"]) == 2
    assert len(parsed["session_history"]) == 2

    # Verify blocked tasks are sorted
    assert parsed["blocked_tasks"][0]["node_id"] == "spec_01_group_2"
    assert parsed["blocked_tasks"][1]["node_id"] == "spec_01_group_3"

    # Verify cost summary
    assert parsed["cost_summary"]["total_cost_usd"] == 0.15
    assert parsed["cost_summary"]["total_sessions"] == 2


# -- TS-126-SMOKE-2: CLI displays post-mortem path on non-successful run ------


def test_cli_displays_postmortem_path(tmp_path: Path) -> None:
    """TS-126-SMOKE-2: _print_summary outputs the post-mortem path.

    Execution Path: Path 2 from design.md
    Must NOT satisfy with: Mocking _print_summary or click.echo.

    Requirement: 126-REQ-6.1
    """
    from af.code import _print_summary

    # Write a real post-mortem file so the path is meaningful
    pm = {"schema_version": 1, "run_id": "20260603_100000_smoke2"}
    audit_dir = tmp_path / "audit"
    pm_path = write_postmortem(pm, audit_dir)

    state = ExecutionState(
        plan_hash="abc123",
        node_states={"a": "blocked", "b": "completed"},
        run_status="block_limit",
        postmortem_path=str(pm_path),
        total_input_tokens=10000,
        total_output_tokens=5000,
        total_cost=0.50,
        total_sessions=3,
    )

    # Capture the real _print_summary output
    runner = click.testing.CliRunner()

    @click.command()
    def _capture() -> None:
        _print_summary(state)

    result = runner.invoke(_capture)

    assert f"Post-mortem: {pm_path}" in result.output
    assert "block_limit" in result.output


# -- TS-126-SMOKE-3: No post-mortem on completed run --------------------------


def test_no_postmortem_on_completed_run(tmp_path: Path) -> None:
    """TS-126-SMOKE-3: A successful run does not produce a post-mortem file.

    Execution Path: Path 3 from design.md
    Must NOT satisfy with: Mocking should_dump.

    Requirements: 126-REQ-1.2
    """
    state = ExecutionState(
        plan_hash="abc123",
        node_states={
            "spec_01_group_1": "completed",
            "spec_01_group_2": "completed",
        },
        run_status="completed",
        run_id="20260603_100000_smoke3",
        started_at="2026-06-03T10:00:00+00:00",
        updated_at="2026-06-03T10:15:00+00:00",
        total_cost=0.30,
        total_input_tokens=8000,
        total_output_tokens=3000,
        total_sessions=2,
        session_history=[
            _make_session_record(node_id="spec_01_group_1"),
            _make_session_record(node_id="spec_01_group_2"),
        ],
    )

    # Exercise the real should_dump — must NOT mock it
    assert should_dump(state) is False

    # The real post-mortem pipeline should not produce a file
    audit_dir = tmp_path / "audit"
    audit_dir.mkdir(parents=True, exist_ok=True)

    # Simulate what run_code does: only generate post-mortem if should_dump is True
    if should_dump(state):
        pm = build_postmortem(state)
        write_postmortem(pm, audit_dir)
        state.postmortem_path = str(audit_dir / f"postmortem_{state.run_id}.json")

    assert state.postmortem_path == ""
    postmortem_files = list(audit_dir.glob("postmortem_*.json"))
    assert len(postmortem_files) == 0
