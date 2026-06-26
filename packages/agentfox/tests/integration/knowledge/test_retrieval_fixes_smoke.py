"""Integration smoke tests for knowledge retrieval fixes.

Exercises real FoxKnowledgeProvider against real DuckDB (not mocked) to
verify end-to-end flows for summary injection, pre-review finding
elevation, and cross-run carry-forward.

Test Spec: TS-120-SMOKE-1, TS-120-SMOKE-2, TS-120-SMOKE-3
Requirements: 120-REQ-1.2, 120-REQ-1.4, 120-REQ-2.1, 120-REQ-2.2,
              120-REQ-2.3, 120-REQ-4.1, 120-REQ-4.2, 120-REQ-4.4
"""

from __future__ import annotations

import uuid

import duckdb
from agentfox.core.config import KnowledgeProviderConfig
from agentfox.knowledge.db import KnowledgeDB
from agentfox.knowledge.fox_provider import FoxKnowledgeProvider
from agentfox.knowledge.migrations import run_migrations

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_conn() -> duckdb.DuckDBPyConnection:
    """Create an in-memory DuckDB with all migrations applied."""
    conn = duckdb.connect(":memory:")
    run_migrations(conn)
    return conn


def _make_provider(
    conn: duckdb.DuckDBPyConnection,
    run_id: str | None = None,
) -> FoxKnowledgeProvider:
    """Build a real FoxKnowledgeProvider backed by *conn*."""
    db = KnowledgeDB.__new__(KnowledgeDB)
    db._conn = conn
    provider = FoxKnowledgeProvider(db, KnowledgeProviderConfig())
    if run_id is not None:
        provider.set_run_id(run_id)
    return provider


def _insert_finding_direct(
    conn: duckdb.DuckDBPyConnection,
    *,
    finding_id: str | None = None,
    spec_name: str = "test_spec",
    task_group: str = "1",
    severity: str = "critical",
    description: str = "Test issue",
    session_id: str = "prior_session",
    category: str | None = None,
    superseded_by: str | None = None,
) -> str:
    """Insert a finding directly into review_findings."""
    fid = finding_id or str(uuid.uuid4())
    conn.execute(
        "INSERT INTO review_findings "
        "(id, severity, description, requirement_ref, spec_name, task_group, "
        "session_id, category, created_at, superseded_by) "
        "VALUES (?, ?, ?, NULL, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?)",
        [fid, severity, description, spec_name, task_group, session_id, category, superseded_by],
    )
    return fid


# ---------------------------------------------------------------------------
# TS-120-SMOKE-1: End-to-End Summary Flow
# ---------------------------------------------------------------------------


class TestSmokeSummaryFlow:
    """Summaries stored via ingest() are retrieved by the next task group.

    Must NOT satisfy with mocked _query_same_spec_summaries -- must hit
    the real database.

    Execution Path: Path 1 from design.md
    Requirements: 120-REQ-1.2, 120-REQ-1.4
    """

    def test_ingest_then_retrieve_context(self) -> None:
        """Store a summary via ingest(), then retrieve() for next group."""
        conn = _make_conn()
        try:
            provider = _make_provider(conn, run_id="test_run")

            # Simulate session completion
            provider.ingest(
                "spec:1",
                "test_spec",
                {
                    "session_status": "completed",
                    "summary": "Built the auth module",
                    "archetype": "coder",
                    "task_group": "1",
                    "attempt": 1,
                    "run_id": "test_run",
                    "touched_files": [],
                    "commit_sha": "abc123",
                },
            )

            # Retrieve for next group
            result = provider.retrieve("test_spec", "test", task_group="2")
            context_items = [item for item in result if "[CONTEXT]" in item]
            assert len(context_items) >= 1
            assert any("Built the auth module" in item for item in context_items), (
                f"Expected summary text in context items: {context_items}"
            )
        finally:
            conn.close()

    def test_reviewer_summary_round_trip(self) -> None:
        """Reviewer summary stored via ingest() is retrievable."""
        conn = _make_conn()
        try:
            provider = _make_provider(conn, run_id="test_run")

            provider.ingest(
                "spec:1",
                "test_spec",
                {
                    "session_status": "completed",
                    "summary": "Reviewer found 2 critical issues",
                    "archetype": "reviewer",
                    "task_group": "1",
                    "attempt": 1,
                    "run_id": "test_run",
                    "touched_files": [],
                    "commit_sha": "abc123",
                },
            )

            result = provider.retrieve("test_spec", "test", task_group="2")
            context_items = [item for item in result if "[CONTEXT]" in item]
            assert any("Reviewer found 2 critical issues" in item for item in context_items)
            # Verify archetype is included in the prefix (120-REQ-3.4)
            assert any("reviewer" in item for item in context_items)
        finally:
            conn.close()

    # test_cross_spec_summary_round_trip removed in spec 10 — cross-spec channel deleted.


# ---------------------------------------------------------------------------
# TS-120-SMOKE-2: Pre-Review to Coder Flow
# ---------------------------------------------------------------------------


class TestSmokePreReviewToCoderFlow:
    """Pre-review findings appear as tracked [REVIEW] items (not
    [CROSS-GROUP]) in the first coder session.

    Must NOT satisfy with mocked _query_reviews or
    _query_cross_group_reviews.

    Execution Path: Path 2 from design.md
    Requirements: 120-REQ-2.1, 120-REQ-2.2, 120-REQ-2.3
    """

    def test_prereview_findings_tracked_not_cross_group(self) -> None:
        """Group 0 findings in [REVIEW], tracked in finding_injections,
        absent from [CROSS-GROUP]."""
        conn = _make_conn()
        try:
            provider = _make_provider(conn)

            prereview_finding_id = str(uuid.uuid4())
            finding_id = _insert_finding_direct(
                conn,
                finding_id=prereview_finding_id,
                spec_name="s",
                task_group="0",
                severity="critical",
                description="Bad design",
                session_id="prereview_session",
            )

            result = provider.retrieve(
                "s",
                "test",
                task_group="1",
                session_id="sess-1",
            )

            # Group 0 finding should be in [REVIEW] results
            review_items = [i for i in result if "[REVIEW]" in i]
            assert any("Bad design" in i for i in review_items), f"Group 0 finding not in review items: {review_items}"

            # Group 0 finding should NOT be in [CROSS-GROUP] results
            cross_group_items = [i for i in result if "[CROSS-GROUP]" in i]
            assert not any("Bad design" in i for i in cross_group_items), (
                f"Group 0 finding leaked into cross-group: {cross_group_items}"
            )

            # Finding ID should be recorded in finding_injections
            injections = conn.execute(
                "SELECT * FROM finding_injections WHERE finding_id = ?",
                [finding_id],
            ).fetchall()
            assert len(injections) == 1, f"Expected 1 injection record, got {len(injections)}"
        finally:
            conn.close()

    def test_prereview_and_same_group_both_in_review(self) -> None:
        """Both group 0 and same-group findings appear in [REVIEW]."""
        conn = _make_conn()
        try:
            provider = _make_provider(conn)

            _insert_finding_direct(
                conn,
                spec_name="s",
                task_group="0",
                severity="critical",
                description="Design flaw from pre-review",
            )
            _insert_finding_direct(
                conn,
                spec_name="s",
                task_group="1",
                severity="major",
                description="Implementation bug",
            )

            result = provider.retrieve("s", "test", task_group="1")
            review_items = [i for i in result if "[REVIEW]" in i]

            assert any("Design flaw from pre-review" in i for i in review_items)
            assert any("Implementation bug" in i for i in review_items)
        finally:
            conn.close()


# TS-120-SMOKE-3 (cross-run carry-forward) removed in spec 10 — prior-run channel deleted.
