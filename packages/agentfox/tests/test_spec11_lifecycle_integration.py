"""Integration and smoke tests for session lifecycle summary storage.

Tests verify that compose_enriched_summary is called during session summary
storage and that the enriched text ends up in the session_summaries DuckDB
table.  Also verifies that generate_archetype_summary returning None
prevents row insertion.

Test Spec: TS-11-14, TS-11-20, TS-11-SMOKE-1, TS-11-SMOKE-2,
           TS-11-SMOKE-3, TS-11-SMOKE-4
Requirements: 11-REQ-3.5, 11-REQ-4.5
"""

from __future__ import annotations

import uuid

import duckdb
import pytest
from agentfox.core.config import KnowledgeProviderConfig
from agentfox.knowledge.db import KnowledgeDB
from agentfox.knowledge.fox_provider import FoxKnowledgeProvider
from agentfox.knowledge.migrations import run_migrations

# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture()
def lifecycle_conn() -> duckdb.DuckDBPyConnection:
    """DuckDB in-memory connection with full migrated schema."""
    conn = duckdb.connect(":memory:")
    run_migrations(conn)
    yield conn
    conn.close()


@pytest.fixture()
def lifecycle_db(lifecycle_conn: duckdb.DuckDBPyConnection) -> KnowledgeDB:
    """KnowledgeDB wrapper around lifecycle_conn."""
    db = KnowledgeDB.__new__(KnowledgeDB)
    db._conn = lifecycle_conn
    return db


@pytest.fixture()
def lifecycle_provider(lifecycle_db: KnowledgeDB) -> FoxKnowledgeProvider:
    """FoxKnowledgeProvider with in-memory DB for integration tests."""
    provider = FoxKnowledgeProvider(lifecycle_db, KnowledgeProviderConfig())
    provider.set_run_id("test-run")
    return provider


def _count_summaries(conn: duckdb.DuckDBPyConnection, session_id: str) -> int:
    """Count rows in session_summaries for a given session_id (node_id)."""
    rows = conn.execute(
        "SELECT count(*) FROM session_summaries WHERE node_id = ?",
        [session_id],
    ).fetchone()
    return rows[0] if rows else 0


def _get_summary_text(conn: duckdb.DuckDBPyConnection, session_id: str) -> str | None:
    """Get the summary column for a session_id (node_id), or None."""
    row = conn.execute(
        "SELECT summary FROM session_summaries WHERE node_id = ?",
        [session_id],
    ).fetchone()
    return row[0] if row else None


# ---------------------------------------------------------------------------
# TS-11-14: compose_enriched_summary integration — enriched text stored
# (11-REQ-3.5)
# ---------------------------------------------------------------------------


class TestEnrichedSummaryStored:
    """Verify enriched session-summary fields are composed and stored."""

    def test_enriched_fields_in_stored_summary(
        self,
        lifecycle_conn: duckdb.DuckDBPyConnection,
        lifecycle_provider: FoxKnowledgeProvider,
    ) -> None:
        session_id = f"test-enriched-{uuid.uuid4().hex[:8]}"
        context = {
            "session_status": "completed",
            "summary": "Used a new indexing strategy.",
            "rejected_approaches": [
                {"approach": "Full table scan", "reason": "Too slow"},
            ],
            "gotchas": ["Index does not cover NULL values"],
            "assumptions": ["Table size stays under 1M rows"],
            "archetype": "coder",
            "task_group": "1",
            "attempt": 1,
            "run_id": "test-run",
        }
        lifecycle_provider.ingest(session_id, "test_spec", context)
        stored = _get_summary_text(lifecycle_conn, session_id)
        assert stored is not None
        assert "Tried: Full table scan — rejected because: Too slow" in stored
        assert "Watch out: Index does not cover NULL values" in stored
        assert "Assumes: Table size stays under 1M rows" in stored


# ---------------------------------------------------------------------------
# TS-11-20: generate_archetype_summary returns None → no row inserted
# (11-REQ-4.5)
# ---------------------------------------------------------------------------


class TestNoneSummaryNoRow:
    """Verify no session_summaries row when summary is None."""

    def test_no_row_for_none_summary(
        self,
        lifecycle_conn: duckdb.DuckDBPyConnection,
        lifecycle_provider: FoxKnowledgeProvider,
    ) -> None:
        session_id = f"test-nosummary-{uuid.uuid4().hex[:8]}"
        # Ingest with no summary — should not store a row
        context = {
            "session_status": "completed",
            "archetype": "reviewer",
            "task_group": "0",
            "attempt": 1,
            "run_id": "test-run",
        }
        lifecycle_provider.ingest(session_id, "test_spec", context)
        assert _count_summaries(lifecycle_conn, session_id) == 0


# ---------------------------------------------------------------------------
# TS-11-SMOKE-1: Full coder enriched summary end-to-end
# ---------------------------------------------------------------------------


class TestSmokeCodderEnrichedSummary:
    """Smoke: coder session with all structured fields populated → stored."""

    def test_smoke_coder_enriched(
        self,
        lifecycle_conn: duckdb.DuckDBPyConnection,
        lifecycle_provider: FoxKnowledgeProvider,
    ) -> None:
        session_id = f"smoke-coder-{uuid.uuid4().hex[:8]}"
        context = {
            "session_status": "completed",
            "summary": "Implemented feature X with two-pass approach.",
            "rejected_approaches": [
                {"approach": "Single-pass", "reason": "Missed edge cases"},
                {"approach": "Recursive", "reason": "Stack overflow on large input"},
            ],
            "gotchas": [
                "DuckDB closes connection on fork",
                "Empty arrays serialize as null",
            ],
            "assumptions": [
                "Input always valid UTF-8",
                "Max input size under 10MB",
            ],
            "archetype": "coder",
            "task_group": "2",
            "attempt": 1,
            "run_id": "test-run",
        }
        lifecycle_provider.ingest(session_id, "test_spec", context)

        stored = _get_summary_text(lifecycle_conn, session_id)
        assert stored is not None
        assert stored.startswith("Implemented feature X with two-pass approach.")
        assert "Tried: Single-pass — rejected because: Missed edge cases" in stored
        assert "Tried: Recursive — rejected because: Stack overflow on large input" in stored
        assert "Watch out: DuckDB closes connection on fork" in stored
        assert "Watch out: Empty arrays serialize as null" in stored
        assert "Assumes: Input always valid UTF-8" in stored
        assert "Assumes: Max input size under 10MB" in stored


# ---------------------------------------------------------------------------
# TS-11-SMOKE-2: Reviewer no-findings → zero rows
# ---------------------------------------------------------------------------


class TestSmokeReviewerNoFindings:
    """Smoke: reviewer session with zero findings → no summary stored."""

    def test_smoke_reviewer_no_findings(
        self,
        lifecycle_conn: duckdb.DuckDBPyConnection,
        lifecycle_provider: FoxKnowledgeProvider,
    ) -> None:
        session_id = f"smoke-rev-nf-{uuid.uuid4().hex[:8]}"
        context = {
            "session_status": "completed",
            "archetype": "reviewer",
            "task_group": "0",
            "attempt": 1,
            "run_id": "test-run",
        }
        lifecycle_provider.ingest(session_id, "test_spec", context)
        assert _count_summaries(lifecycle_conn, session_id) == 0


# ---------------------------------------------------------------------------
# TS-11-SMOKE-3: Legacy session-summary.json stored unchanged
# ---------------------------------------------------------------------------


class TestSmokeLegacySummary:
    """Smoke: legacy summary (no structured fields) stored unchanged."""

    def test_smoke_legacy_unchanged(
        self,
        lifecycle_conn: duckdb.DuckDBPyConnection,
        lifecycle_provider: FoxKnowledgeProvider,
    ) -> None:
        session_id = f"smoke-legacy-{uuid.uuid4().hex[:8]}"
        original_summary = "Completed task group 3 for spec 9."
        context = {
            "session_status": "completed",
            "summary": original_summary,
            "archetype": "coder",
            "task_group": "3",
            "attempt": 1,
            "run_id": "test-run",
        }
        lifecycle_provider.ingest(session_id, "test_spec", context)
        stored = _get_summary_text(lifecycle_conn, session_id)
        assert stored == original_summary


# ---------------------------------------------------------------------------
# TS-11-SMOKE-4: Reviewer with findings → non-empty summary stored
# ---------------------------------------------------------------------------


class TestSmokeReviewerWithFindings:
    """Smoke: reviewer session with findings → summary row stored.

    Exercises PATH-4: generate_archetype_summary produces the summary from
    actual findings, then the summary is ingested and stored.
    """

    def test_smoke_reviewer_with_findings(
        self,
        lifecycle_conn: duckdb.DuckDBPyConnection,
        lifecycle_provider: FoxKnowledgeProvider,
    ) -> None:
        from agentfox.knowledge.formatting import generate_archetype_summary
        from agentfox.knowledge.review_store import ReviewFinding, insert_findings

        session_id = f"smoke-rev-wf-{uuid.uuid4().hex[:8]}"

        # Insert actual findings into DB for this session
        findings = [
            ReviewFinding(
                id=str(uuid.uuid4()),
                severity="critical",
                description="Missing input validation on user ID field",
                requirement_ref="REQ-1.1",
                spec_name="test_spec",
                task_group="1",
                session_id=session_id,
            ),
            ReviewFinding(
                id=str(uuid.uuid4()),
                severity="minor",
                description="Unused import in formatting.py",
                requirement_ref=None,
                spec_name="test_spec",
                task_group="1",
                session_id=session_id,
            ),
        ]
        insert_findings(lifecycle_conn, findings)

        # Exercise generate_archetype_summary to produce the summary (PATH-4)
        summary_text = generate_archetype_summary("reviewer", findings=findings)
        assert summary_text is not None  # Expected effect 1
        assert len(summary_text) > 0

        # Ingest the generated summary
        context = {
            "session_status": "completed",
            "summary": summary_text,
            "archetype": "reviewer",
            "task_group": "1",
            "attempt": 1,
            "run_id": "test-run",
        }
        lifecycle_provider.ingest(session_id, "test_spec", context)

        # Verify stored row (expected effects 3, 4, 5)
        assert _count_summaries(lifecycle_conn, session_id) == 1
        stored = _get_summary_text(lifecycle_conn, session_id)
        assert stored is not None
        assert len(stored) > 0
        # Stored text must contain severity or finding description content
        assert "critical" in stored.lower() or "finding" in stored.lower()
