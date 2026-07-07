"""Tests for retry history rendering in reviewer context (issue #682).

Verifies ``render_retry_history()`` in ``session/context.py``.
"""

from __future__ import annotations

import uuid

import duckdb
import pytest
from agentfox.knowledge.migrations import run_migrations
from agentfox.knowledge.review_store import record_finding_injections
from agentfox.session.context import render_retry_history


@pytest.fixture
def conn() -> duckdb.DuckDBPyConnection:
    c = duckdb.connect(":memory:")
    run_migrations(c)
    yield c
    c.close()


def _insert_and_inject(
    conn: duckdb.DuckDBPyConnection,
    descriptions: list[str],
    session_id: str,
    spec_name: str = "spec_a",
    task_group: str = "2",
    severity: str = "critical",
) -> list[str]:
    ids = []
    for i, desc in enumerate(descriptions):
        fid = str(uuid.uuid4())
        ids.append(fid)
        conn.execute(
            "INSERT INTO review_findings "
            "(id, severity, description, spec_name, task_group, session_id, category) "
            "VALUES (?, ?, ?, ?, ?, ?, 'audit')",
            [fid, severity, desc, spec_name, task_group, f"{spec_name}:audit:{i}"],
        )
    record_finding_injections(conn, ids, session_id)
    return ids


class TestRenderRetryHistory:
    def test_unresolved_findings_rendered(self, conn: duckdb.DuckDBPyConnection) -> None:
        _insert_and_inject(conn, ["Test lacks invalidation spy", "Missing abort handler test"], session_id="spec_a:2")
        result = render_retry_history(conn, "spec_a", "2")
        assert result is not None
        assert "## Retry History" in result
        assert "Test lacks invalidation spy" in result
        assert "Missing abort handler test" in result
        assert "[critical]" in result

    def test_all_resolved_returns_none(self, conn: duckdb.DuckDBPyConnection) -> None:
        ids = _insert_and_inject(conn, ["Resolved finding"], session_id="spec_a:2")
        for fid in ids:
            conn.execute("UPDATE review_findings SET superseded_by = 'resolved' WHERE id = ?::UUID", [fid])
        assert render_retry_history(conn, "spec_a", "2") is None

    def test_no_injections_returns_none(self, conn: duckdb.DuckDBPyConnection) -> None:
        assert render_retry_history(conn, "spec_a", "2") is None

    def test_wrong_spec_returns_none(self, conn: duckdb.DuckDBPyConnection) -> None:
        _insert_and_inject(conn, ["Some finding"], session_id="spec_a:2", spec_name="spec_a")
        assert render_retry_history(conn, "spec_b", "2") is None

    def test_severity_label_included(self, conn: duckdb.DuckDBPyConnection) -> None:
        _insert_and_inject(conn, ["A major finding"], session_id="spec_a:2", severity="major")
        result = render_retry_history(conn, "spec_a", "2")
        assert result is not None
        assert "[major]" in result

    def test_downgrade_guidance_included(self, conn: duckdb.DuckDBPyConnection) -> None:
        _insert_and_inject(conn, ["Unfixable pattern"], session_id="spec_a:2")
        result = render_retry_history(conn, "spec_a", "2")
        assert result is not None
        assert "WEAK" in result
