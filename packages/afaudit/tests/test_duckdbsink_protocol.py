"""Integration test for DuckDBSink satisfying the afaudit.SessionSink protocol.

TS-01-16: DuckDBSink implements all methods required by SessionSink.

This test requires agentfox (and duckdb) to be installed.
"""

from __future__ import annotations

import inspect

import pytest

try:
    from agentfox.knowledge.duckdb_sink import DuckDBSink

    _HAS_DUCKDB_SINK = True
except ImportError:
    _HAS_DUCKDB_SINK = False

from afaudit.sink import SessionSink

pytestmark = pytest.mark.integration

# The five methods required by the SessionSink protocol.
_REQUIRED_METHODS = [
    "record_session_outcome",
    "record_tool_call",
    "record_tool_error",
    "emit_audit_event",
    "close",
]


@pytest.mark.skipif(not _HAS_DUCKDB_SINK, reason="agentfox/duckdb not installed")
class TestDuckDBSinkSatisfiesSessionSink:
    """TS-01-16: DuckDBSink structurally satisfies SessionSink protocol.

    Requirement: 01-REQ-4.3
    """

    def test_all_protocol_methods_present(self) -> None:
        """DuckDBSink must have all methods required by SessionSink."""
        for method_name in _REQUIRED_METHODS:
            assert hasattr(DuckDBSink, method_name), f"DuckDBSink missing SessionSink method: {method_name}"

    def test_protocol_methods_are_callable(self) -> None:
        """All required methods on DuckDBSink must be callable."""
        for method_name in _REQUIRED_METHODS:
            method = getattr(DuckDBSink, method_name)
            assert callable(method), f"DuckDBSink.{method_name} is not callable"

    def test_method_signatures_compatible(self) -> None:
        """DuckDBSink method signatures must be compatible with SessionSink.

        Each required method must accept the same positional parameters
        (excluding 'self') as the protocol definition.
        """
        for method_name in _REQUIRED_METHODS:
            protocol_method = getattr(SessionSink, method_name)
            impl_method = getattr(DuckDBSink, method_name)
            protocol_params = list(inspect.signature(protocol_method).parameters.keys())
            impl_params = list(inspect.signature(impl_method).parameters.keys())
            # Both should have the same parameter names (excluding self which
            # is implicit in Protocol but explicit in the class)
            proto_names = [p for p in protocol_params if p != "self"]
            impl_names = [p for p in impl_params if p != "self"]
            assert proto_names == impl_names, (
                f"DuckDBSink.{method_name} signature mismatch: "
                f"protocol expects {proto_names}, got {impl_names}"
            )

    def test_runtime_checkable_isinstance(self) -> None:
        """If SessionSink is @runtime_checkable, DuckDBSink must pass issubclass check."""
        # SessionSink is decorated with @runtime_checkable in the source.
        assert issubclass(DuckDBSink, SessionSink), (
            "DuckDBSink does not satisfy the @runtime_checkable SessionSink protocol"
        )
