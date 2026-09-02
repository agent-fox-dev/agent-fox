"""Tests for pre-flight reviewer blocking gate (issue #713).

Verifies that pre-flight reviewer (group 0) drift and review findings
correctly trigger the blocking gate, rather than being silently discarded
by the group-0 -> "1" task_group remapping.

Acceptance Criteria:
  AC-1: Drift findings with task_group="0" trigger blocking when
        include_prereview is used.
  AC-2: Review findings scoped to arbitrary target task groups are not
        filtered out for pre-flight mode.
  AC-3: Advisory mode (threshold=None) still returns should_block=False.
  AC-4: Non-group-0 reviewers are unaffected (no regression).

Requirements: 713-AC-1, 713-AC-2, 713-AC-3, 713-AC-4
"""

from __future__ import annotations

import uuid

import duckdb
from agentfox.core.config import ArchetypesConfig, ReviewerConfig
from agentfox.engine.blocking import evaluate_review_blocking
from agentfox.engine.state import SessionRecord
from agentfox.knowledge.migrations import run_migrations
from agentfox.knowledge.review_store import (
    DriftFinding,
    ReviewFinding,
    insert_drift_findings,
    insert_findings,
)


def _make_conn() -> duckdb.DuckDBPyConnection:
    conn = duckdb.connect(":memory:")
    run_migrations(conn)
    return conn


def _preflight_record(
    spec_name: str = "myspec",
    attempt: int = 1,
) -> SessionRecord:
    return SessionRecord(
        node_id=f"{spec_name}:0:reviewer:pre-flight",
        archetype="reviewer",
        attempt=attempt,
        status="completed",
        input_tokens=0,
        output_tokens=0,
        cost=0.0,
        duration_ms=0,
        error_message=None,
        timestamp="2026-01-01T00:00:00",
    )


def _archetypes(threshold: int | None = 1) -> ArchetypesConfig:
    return ArchetypesConfig(
        reviewer_config=ReviewerConfig(pre_flight_drift_block_threshold=threshold),
    )


def _drift_finding(
    spec_name: str = "myspec",
    task_group: str = "0",
    severity: str = "critical",
) -> DriftFinding:
    return DriftFinding(
        id=str(uuid.uuid4()),
        severity=severity,
        description="Method does_not_exist() referenced in spec but absent",
        spec_ref="REQ-1",
        artifact_ref="src/foo.py",
        spec_name=spec_name,
        task_group=task_group,
        session_id=f"{spec_name}:0:reviewer:pre-flight:1",
    )


def _review_finding(
    spec_name: str = "myspec",
    task_group: str = "6",
    severity: str = "critical",
    session_id: str | None = None,
) -> ReviewFinding:
    return ReviewFinding(
        id=str(uuid.uuid4()),
        severity=severity,
        description="Handler references removed HTTP verb",
        requirement_ref="REQ-2",
        spec_name=spec_name,
        task_group=task_group,
        session_id=session_id or f"{spec_name}:0:reviewer:pre-flight:1",
    )


# ---------------------------------------------------------------------------
# AC-1: Drift findings with task_group="0" trigger blocking
# ---------------------------------------------------------------------------


class TestPreflightDriftBlocking:
    def test_group0_drift_findings_trigger_block(self) -> None:
        """AC-1: Critical drift findings stored as task_group='0' are found
        by the blocking evaluator via include_prereview."""
        conn = _make_conn()
        insert_drift_findings(conn, [_drift_finding()])

        record = _preflight_record()
        decision = evaluate_review_blocking(record, _archetypes(threshold=1), conn, mode="pre-flight")

        assert decision.should_block is True
        assert decision.coder_node_id == "myspec:1"

    def test_multiple_drift_findings_above_threshold(self) -> None:
        conn = _make_conn()
        findings = [
            _drift_finding(severity="critical"),
            _drift_finding(severity="major"),
        ]
        insert_drift_findings(conn, findings)

        record = _preflight_record()
        decision = evaluate_review_blocking(record, _archetypes(threshold=2), conn, mode="pre-flight")

        assert decision.should_block is True

    def test_below_threshold_does_not_block(self) -> None:
        conn = _make_conn()
        insert_drift_findings(conn, [_drift_finding(severity="minor")])

        record = _preflight_record()
        decision = evaluate_review_blocking(record, _archetypes(threshold=1), conn, mode="pre-flight")

        assert decision.should_block is False


# ---------------------------------------------------------------------------
# AC-2: Review findings with target task_groups not filtered out
# ---------------------------------------------------------------------------


class TestPreflightReviewFindingsNotFiltered:
    def test_findings_with_various_target_groups_participate(self) -> None:
        """AC-2: Pre-flight review findings scoped to task groups 6, 7, 8
        are not discarded by the task_group='1' filter."""
        conn = _make_conn()
        findings = [
            _review_finding(task_group="6", severity="critical"),
            _review_finding(task_group="7", severity="critical"),
            _review_finding(task_group="8", severity="major"),
        ]
        insert_findings(conn, findings)

        record = _preflight_record()
        decision = evaluate_review_blocking(record, _archetypes(threshold=None), conn, mode="pre-flight")

        assert decision.should_block is True

    def test_single_critical_finding_in_distant_group(self) -> None:
        conn = _make_conn()
        insert_findings(conn, [_review_finding(task_group="10", severity="critical")])

        record = _preflight_record()
        decision = evaluate_review_blocking(record, _archetypes(threshold=None), conn, mode="pre-flight")

        assert decision.should_block is True


# ---------------------------------------------------------------------------
# AC-3: Advisory mode (threshold=None) returns should_block=False for drift
# ---------------------------------------------------------------------------


class TestPreflightAdvisoryMode:
    def test_null_threshold_does_not_block_drift(self) -> None:
        """AC-3: When pre_flight_drift_block_threshold is None, drift findings
        are advisory only."""
        conn = _make_conn()
        insert_drift_findings(conn, [_drift_finding()])

        record = _preflight_record()
        decision = evaluate_review_blocking(record, _archetypes(threshold=None), conn, mode="pre-flight")

        assert decision.should_block is False


# ---------------------------------------------------------------------------
# AC-4: Non-group-0 reviewers unaffected (no regression)
# ---------------------------------------------------------------------------


class TestNonPreflightReviewerRegression:
    def test_drift_review_group2_unaffected(self) -> None:
        """AC-4: drift-review for group 2 still queries task_group='2'."""
        conn = _make_conn()
        insert_drift_findings(
            conn,
            [_drift_finding(task_group="2", severity="critical")],
        )

        record = SessionRecord(
            node_id="myspec:2:reviewer:drift-review",
            archetype="reviewer",
            attempt=1,
            status="completed",
            input_tokens=0,
            output_tokens=0,
            cost=0.0,
            duration_ms=0,
            error_message=None,
            timestamp="2026-01-01T00:00:00",
        )
        decision = evaluate_review_blocking(record, _archetypes(threshold=1), conn, mode="drift-review")

        assert decision.should_block is True
        assert decision.coder_node_id == "myspec:2"

    def test_pre_review_group3_filters_by_group(self) -> None:
        """AC-4: pre-review for group 3 still scopes review findings by group."""
        conn = _make_conn()
        insert_findings(
            conn,
            [_review_finding(task_group="3", severity="critical", session_id="myspec:3:reviewer:pre-review:1")],
        )
        insert_findings(
            conn,
            [_review_finding(task_group="5", severity="critical", session_id="myspec:3:reviewer:pre-review:1")],
        )

        record = SessionRecord(
            node_id="myspec:3:reviewer:pre-review",
            archetype="reviewer",
            attempt=1,
            status="completed",
            input_tokens=0,
            output_tokens=0,
            cost=0.0,
            duration_ms=0,
            error_message=None,
            timestamp="2026-01-01T00:00:00",
        )
        decision = evaluate_review_blocking(record, _archetypes(threshold=None), conn, mode="pre-review")

        # Only the group-3 finding should be counted, not the group-5 one
        assert decision.should_block is True
        assert decision.coder_node_id == "myspec:3"
