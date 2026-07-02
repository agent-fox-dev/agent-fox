"""Property tests for DuckDB writes unchanged after debug removal.

Test Spec: TS-131-P1
Property: Property 1 from design.md
Validates: 131-REQ-2.E1
"""

from __future__ import annotations

import duckdb
from afaudit.sink import ToolCall, ToolError
from agentfox.knowledge.duckdb_sink import DuckDBSink
from hypothesis import given, settings
from hypothesis import strategies as st

from tests.unit.knowledge.conftest import create_schema


class TestDuckDBWritesUnchanged:
    """TS-131-P1: DuckDB writes unchanged after removal.

    For any N tool calls (1 <= N <= 10) and M tool errors (1 <= M <= 10),
    tool_calls table has exactly N rows and tool_errors table has exactly
    M rows when DuckDBSink is constructed without debug.

    Property 1 from design.md.
    """

    @given(
        n=st.integers(min_value=1, max_value=10),
        m=st.integers(min_value=1, max_value=10),
    )
    @settings(max_examples=20)
    def test_tool_signals_written_without_debug(self, n: int, m: int) -> None:
        """N tool calls and M errors produce exactly N and M rows."""
        conn = duckdb.connect(":memory:")
        create_schema(conn)
        sink = DuckDBSink(conn)

        for _ in range(n):
            sink.record_tool_call(ToolCall(tool_name="test"))
        for _ in range(m):
            sink.record_tool_error(ToolError(tool_name="test"))

        assert conn.execute("SELECT COUNT(*) FROM tool_calls").fetchone()[0] == n  # type: ignore[index]
        assert conn.execute("SELECT COUNT(*) FROM tool_errors").fetchone()[0] == m  # type: ignore[index]

        conn.close()
