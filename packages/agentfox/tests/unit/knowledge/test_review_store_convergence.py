"""Tests for finding convergence detection (issue #682).

Verifies ``check_finding_convergence()`` and ``query_unresolved_injections()``
in the review store.
"""

from __future__ import annotations

import uuid

import duckdb
import pytest
from agentfox.knowledge.migrations import run_migrations
from agentfox.knowledge.review_store import (
    check_finding_convergence,
    query_unresolved_injections,
    record_finding_injections,
)


@pytest.fixture
def conn() -> duckdb.DuckDBPyConnection:
    c = duckdb.connect(":memory:")
    run_migrations(c)
    yield c
    c.close()


def _insert_and_inject(
    conn: duckdb.DuckDBPyConnection,
    *,
    n_findings: int,
    session_id: str,
    spec_name: str = "spec_a",
    task_group: str = "2",
) -> list[str]:
    """Insert findings and record them as injected into a session."""
    ids = []
    for i in range(n_findings):
        fid = str(uuid.uuid4())
        ids.append(fid)
        conn.execute(
            "INSERT INTO review_findings "
            "(id, severity, description, spec_name, task_group, session_id, category) "
            "VALUES (?, 'critical', ?, ?, ?, ?, 'audit')",
            [fid, f"Finding {i}", spec_name, task_group, f"{spec_name}:audit:{i}"],
        )
    record_finding_injections(conn, ids, session_id)
    return ids


class TestCheckFindingConvergence:
    def test_all_active_returns_one(self, conn: duckdb.DuckDBPyConnection) -> None:
        _insert_and_inject(conn, n_findings=5, session_id="spec_a:2")
        assert check_finding_convergence(conn, "spec_a:2") == pytest.approx(1.0)

    def test_all_superseded_returns_zero(self, conn: duckdb.DuckDBPyConnection) -> None:
        ids = _insert_and_inject(conn, n_findings=3, session_id="spec_a:2")
        for fid in ids:
            conn.execute("UPDATE review_findings SET superseded_by = 'resolved' WHERE id = ?::UUID", [fid])
        assert check_finding_convergence(conn, "spec_a:2") == pytest.approx(0.0)

    def test_partial_returns_correct_ratio(self, conn: duckdb.DuckDBPyConnection) -> None:
        ids = _insert_and_inject(conn, n_findings=10, session_id="spec_a:2")
        for fid in ids[:3]:
            conn.execute("UPDATE review_findings SET superseded_by = 'resolved' WHERE id = ?::UUID", [fid])
        assert check_finding_convergence(conn, "spec_a:2") == pytest.approx(0.7)

    def test_no_injections_returns_zero(self, conn: duckdb.DuckDBPyConnection) -> None:
        assert check_finding_convergence(conn, "nonexistent:1") == pytest.approx(0.0)

    def test_different_session_id_isolated(self, conn: duckdb.DuckDBPyConnection) -> None:
        _insert_and_inject(conn, n_findings=5, session_id="spec_a:2")
        assert check_finding_convergence(conn, "spec_a:3") == pytest.approx(0.0)


class TestQueryUnresolvedInjections:
    def test_returns_unresolved_findings(self, conn: duckdb.DuckDBPyConnection) -> None:
        _insert_and_inject(conn, n_findings=3, session_id="spec_a:2", spec_name="spec_a", task_group="2")
        result = query_unresolved_injections(conn, "spec_a", "2")
        assert len(result) == 3
        assert all(isinstance(desc, str) and isinstance(sev, str) for desc, sev in result)

    def test_excludes_superseded(self, conn: duckdb.DuckDBPyConnection) -> None:
        ids = _insert_and_inject(conn, n_findings=3, session_id="spec_a:2", spec_name="spec_a", task_group="2")
        conn.execute("UPDATE review_findings SET superseded_by = 'resolved' WHERE id = ?::UUID", [ids[0]])
        result = query_unresolved_injections(conn, "spec_a", "2")
        assert len(result) == 2

    def test_all_resolved_returns_empty(self, conn: duckdb.DuckDBPyConnection) -> None:
        ids = _insert_and_inject(conn, n_findings=2, session_id="spec_a:2", spec_name="spec_a", task_group="2")
        for fid in ids:
            conn.execute("UPDATE review_findings SET superseded_by = 'resolved' WHERE id = ?::UUID", [fid])
        assert query_unresolved_injections(conn, "spec_a", "2") == []

    def test_no_injections_returns_empty(self, conn: duckdb.DuckDBPyConnection) -> None:
        assert query_unresolved_injections(conn, "spec_a", "2") == []

    def test_wrong_spec_returns_empty(self, conn: duckdb.DuckDBPyConnection) -> None:
        _insert_and_inject(conn, n_findings=2, session_id="spec_a:2", spec_name="spec_a", task_group="2")
        assert query_unresolved_injections(conn, "spec_b", "2") == []
