"""Tests for removal of the debug parameter from DuckDBSink.

Test Spec: TS-131-5, TS-131-6, TS-131-E2
Requirements: 131-REQ-2.3, 131-REQ-2.E1
"""

from __future__ import annotations

import duckdb
import pytest
from afaudit.sink import SessionOutcome
from agentfox.knowledge.duckdb_sink import DuckDBSink

from tests.unit.knowledge.conftest import create_schema

# ---------------------------------------------------------------------------
# TS-131-5: DuckDBSink rejects debug keyword
# ---------------------------------------------------------------------------


class TestDuckDBSinkRejectsDebug:
    """TS-131-5: DuckDBSink(conn, debug=True) raises TypeError.

    Requirement: 131-REQ-2.3
    """

    def test_duckdb_rejects_debug(self) -> None:
        """DuckDBSink raises TypeError when constructed with debug=."""
        conn = duckdb.connect(":memory:")
        create_schema(conn)
        try:
            with pytest.raises(TypeError):
                DuckDBSink(conn, debug=True)  # type: ignore[call-arg]
        finally:
            conn.close()


# ---------------------------------------------------------------------------
# TS-131-6: DuckDBSink has no _debug attribute
# ---------------------------------------------------------------------------


class TestDuckDBSinkNoDebugAttr:
    """TS-131-6: DuckDBSink instances do not have a _debug attribute.

    Requirement: 131-REQ-2.3
    """

    def test_no_debug_attr(self) -> None:
        """hasattr(sink, '_debug') is False."""
        conn = duckdb.connect(":memory:")
        create_schema(conn)
        try:
            sink = DuckDBSink(conn)
            assert not hasattr(sink, "_debug")
        finally:
            conn.close()


# ---------------------------------------------------------------------------
# TS-131-E2: DuckDBSink without debug records outcomes
# ---------------------------------------------------------------------------


class TestDuckDBSinkRecordsWithoutDebug:
    """TS-131-E2: Session outcomes are written when DuckDBSink is
    constructed without debug.

    Requirement: 131-REQ-2.E1
    """

    def test_records_without_debug(self, knowledge_conn: duckdb.DuckDBPyConnection) -> None:
        """DuckDBSink(conn) records a session outcome row."""
        sink = DuckDBSink(knowledge_conn)
        sink.record_session_outcome(
            SessionOutcome(
                spec_name="test_spec",
                status="completed",
            )
        )

        rows = knowledge_conn.execute("SELECT COUNT(*) FROM session_outcomes").fetchone()
        assert rows is not None
        assert rows[0] == 1
