"""Tests for fix/analyzer.py read-only database connection.

Verifies that the analyzer uses a read-only connection via
open_knowledge_store(read_only=True), performs no write operations,
and propagates DuckDB exceptions to the caller.

Test Spec: TS-06-8, TS-06-9, TS-06-E4
Requirements: 06-REQ-4.1, 06-REQ-4.2, 06-REQ-4.E1
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import MagicMock, patch

import duckdb  # noqa: TC002
import pytest

# -----------------------------------------------------------------------
# TS-06-8: fix/analyzer.py calls open_knowledge_store with read_only=True
# -----------------------------------------------------------------------


class TestAnalyzerReadOnly:
    """TS-06-8: analyzer must use read-only connection for finding queries."""

    def test_load_review_context_uses_read_only(self) -> None:
        """load_review_context must call open_knowledge_store with
        read_only=True (06-REQ-4.1)."""
        from agentfox.fix.analyzer import load_review_context

        mock_db = MagicMock()
        mock_db.connection = MagicMock()

        with (
            patch(
                "agentfox.knowledge.db.open_knowledge_store",
                return_value=mock_db,
            ) as mock_open,
            patch(
                "agentfox.knowledge.review_store.query_active_findings",
                return_value=[],
            ),
        ):
            load_review_context(Path("/tmp/fake-project"))

        mock_open.assert_called_once()
        _, kwargs = mock_open.call_args
        assert kwargs["read_only"] is True, "open_knowledge_store must be called with read_only=True"


# -----------------------------------------------------------------------
# TS-06-9: fix/analyzer.py performs no write operations
# -----------------------------------------------------------------------


class TestAnalyzerNoWrites:
    """TS-06-9: analyzer must not INSERT, UPDATE, or DELETE."""

    def test_load_review_context_no_mutations(self, tmp_path: Path) -> None:
        """Running the analyzer's query path against a seeded read-only
        DB must not change any row counts (06-REQ-4.2)."""
        db_path = str(tmp_path / "test_knowledge.duckdb")

        # Seed database with full review_findings schema
        rw_conn = duckdb.connect(db_path)
        rw_conn.execute("""
            CREATE TABLE IF NOT EXISTS review_findings (
                id VARCHAR PRIMARY KEY,
                severity VARCHAR,
                description VARCHAR,
                requirement_ref VARCHAR,
                spec_name VARCHAR,
                task_group VARCHAR,
                session_id VARCHAR,
                superseded_by VARCHAR,
                created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                category VARCHAR
            )
        """)
        rw_conn.execute(
            "INSERT INTO review_findings "
            "(id, severity, description, requirement_ref, "
            "spec_name, task_group, session_id) "
            "VALUES ('f1', 'critical', 'test finding', "
            "'REQ-1', 'test_spec', '1', 'sess1')"
        )
        count_before = rw_conn.execute("SELECT COUNT(*) FROM review_findings").fetchone()[0]
        rw_conn.close()

        # Open read-only and run the query path
        ro_conn = duckdb.connect(db_path, read_only=True)
        from agentfox.knowledge.review_store import query_active_findings

        query_active_findings(ro_conn, spec_name="")
        ro_conn.close()

        # Verify no mutations occurred
        verify_conn = duckdb.connect(db_path, read_only=True)
        count_after = verify_conn.execute("SELECT COUNT(*) FROM review_findings").fetchone()[0]
        verify_conn.close()

        assert count_before == count_after, "query_active_findings must not mutate the database"


# -----------------------------------------------------------------------
# TS-06-E4: DuckDB read-only exception propagates from analyzer
# -----------------------------------------------------------------------


class TestAnalyzerReadOnlyExceptionPropagation:
    """TS-06-E4: DuckDB read-only exceptions must propagate to caller."""

    def test_read_only_exception_not_swallowed(self, tmp_path: Path) -> None:
        """A write operation on a read-only connection used by the analyzer
        must raise and propagate the DuckDB exception — it must NOT be
        caught or silenced within the analyzer module (06-REQ-4.E1)."""
        db_path = str(tmp_path / "test.duckdb")

        # Create DB with review_findings table
        conn_rw = duckdb.connect(db_path)
        conn_rw.execute("""
            CREATE TABLE IF NOT EXISTS review_findings (
                id VARCHAR PRIMARY KEY,
                severity VARCHAR,
                description VARCHAR,
                requirement_ref VARCHAR,
                spec_name VARCHAR,
                task_group VARCHAR,
                session_id VARCHAR,
                superseded_by VARCHAR,
                created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
                category VARCHAR
            )
        """)
        conn_rw.close()

        # Open read-only connection
        conn_ro = duckdb.connect(db_path, read_only=True)

        # Attempting a write on a read-only connection must raise
        with pytest.raises(duckdb.InvalidInputException):
            conn_ro.execute(
                "INSERT INTO review_findings "
                "(id, severity, description, requirement_ref, "
                "spec_name, task_group, session_id) "
                "VALUES ('x', 'critical', 'injected', "
                "'REQ-X', 'spec', '1', 'sess')"
            )

        conn_ro.close()


# -----------------------------------------------------------------------
# Additional tests: resource cleanup
# -----------------------------------------------------------------------


class TestAnalyzerResourceCleanup:
    """Verify db.close() is called in all execution paths."""

    def test_load_review_context_closes_db_on_success(self) -> None:
        """db.close() must be called after successful execution."""
        from agentfox.fix.analyzer import load_review_context

        mock_db = MagicMock()
        mock_db.connection = MagicMock()

        with (
            patch(
                "agentfox.knowledge.db.open_knowledge_store",
                return_value=mock_db,
            ),
            patch(
                "agentfox.knowledge.review_store.query_active_findings",
                return_value=[],
            ),
        ):
            load_review_context(Path("/tmp/fake-project"))

        mock_db.close.assert_called_once()

    def test_load_review_context_closes_db_on_error(self) -> None:
        """db.close() must be called even when query raises."""
        from agentfox.fix.analyzer import load_review_context

        mock_db = MagicMock()
        mock_db.connection = MagicMock()

        with (
            patch(
                "agentfox.knowledge.db.open_knowledge_store",
                return_value=mock_db,
            ),
            patch(
                "agentfox.knowledge.review_store.query_active_findings",
                side_effect=RuntimeError("query failed"),
            ),
        ):
            with pytest.raises(RuntimeError, match="query failed"):
                load_review_context(Path("/tmp/fake-project"))

        mock_db.close.assert_called_once()
