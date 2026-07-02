"""Tests for afaudit.postmortem module — Protocols, builders, writer, and logging.

TS-01-20: PostmortemInput Protocol has exactly 11 attributes
TS-01-21: SessionRecordLike Protocol has exactly 12 attributes
TS-01-22: All 6 symbols callable (public + internal helpers)
TS-01-23: build_postmortem works with stub objects satisfying the Protocols
TS-01-24: stdlib logging with 'afaudit.postmortem' logger
"""

from __future__ import annotations

import typing
from pathlib import Path

import afaudit.postmortem as postmortem

WORKSPACE_ROOT = Path(__file__).resolve().parents[3]
POSTMORTEM_SOURCE = WORKSPACE_ROOT / "packages" / "afaudit" / "afaudit" / "postmortem.py"

POSTMORTEM_INPUT_ATTRS = {
    "run_id",
    "run_status",
    "node_states",
    "total_cost",
    "total_input_tokens",
    "total_output_tokens",
    "total_sessions",
    "blocked_reasons",
    "session_history",
    "started_at",
    "updated_at",
}

SESSION_RECORD_LIKE_ATTRS = {
    "node_id",
    "attempt",
    "status",
    "archetype",
    "model",
    "duration_ms",
    "cost",
    "error_message",
    "timestamp",
    "is_transport_error",
    "is_budget_exhausted",
    "is_non_retryable",
}


class _StubSessionRecord:
    """Minimal stub satisfying SessionRecordLike protocol."""

    node_id = "spec01/1/coder"
    attempt = 1
    status = "completed"
    archetype = "coder"
    model = "claude-sonnet-4-20250514"
    duration_ms = 12000
    cost = 0.05
    error_message = None
    timestamp = "2024-01-15T10:30:00Z"
    is_transport_error = False
    is_budget_exhausted = False
    is_non_retryable = False


class _StubPostmortemInput:
    """Minimal stub satisfying PostmortemInput protocol."""

    run_id = "20240115_103000_abc123"
    run_status = "stalled"
    node_states = {"spec01/1/coder": "completed", "spec01/2/coder": "blocked"}
    total_cost = 0.05
    total_input_tokens = 5000
    total_output_tokens = 2000
    total_sessions = 1
    blocked_reasons = {"spec01/2/coder": "dependency not met"}
    session_history = [_StubSessionRecord()]
    started_at = "2024-01-15T10:00:00Z"
    updated_at = "2024-01-15T10:30:00Z"


class TestPostmortemInputProtocol:
    """TS-01-20: PostmortemInput Protocol defines exactly 11 attributes.

    Requirement: 01-REQ-6.1
    """

    def test_has_exactly_11_attributes(self) -> None:
        """PostmortemInput Protocol must define exactly 11 typed attributes."""
        hints = typing.get_type_hints(postmortem.PostmortemInput)
        assert len(hints) == 11, f"Expected 11 attributes, got {len(hints)}: {sorted(hints.keys())}"

    def test_has_correct_attribute_names(self) -> None:
        """PostmortemInput must define the exact expected attribute names."""
        hints = typing.get_type_hints(postmortem.PostmortemInput)
        assert set(hints.keys()) == POSTMORTEM_INPUT_ATTRS, (
            f"Attribute mismatch: extra={set(hints.keys()) - POSTMORTEM_INPUT_ATTRS}, "
            f"missing={POSTMORTEM_INPUT_ATTRS - set(hints.keys())}"
        )


class TestSessionRecordLikeProtocol:
    """TS-01-21: SessionRecordLike Protocol defines exactly 12 attributes.

    Requirement: 01-REQ-6.2
    """

    def test_has_exactly_12_attributes(self) -> None:
        """SessionRecordLike Protocol must define exactly 12 typed attributes."""
        hints = typing.get_type_hints(postmortem.SessionRecordLike)
        assert len(hints) == 12, f"Expected 12 attributes, got {len(hints)}: {sorted(hints.keys())}"

    def test_has_correct_attribute_names(self) -> None:
        """SessionRecordLike must define the exact expected attribute names."""
        hints = typing.get_type_hints(postmortem.SessionRecordLike)
        assert set(hints.keys()) == SESSION_RECORD_LIKE_ATTRS, (
            f"Attribute mismatch: extra={set(hints.keys()) - SESSION_RECORD_LIKE_ATTRS}, "
            f"missing={SESSION_RECORD_LIKE_ATTRS - set(hints.keys())}"
        )


class TestPostmortemSymbols:
    """TS-01-22: afaudit.postmortem defines all required symbols.

    Requirement: 01-REQ-6.3
    """

    def test_build_postmortem_is_callable(self) -> None:
        """build_postmortem must be callable."""
        assert callable(postmortem.build_postmortem)

    def test_write_postmortem_is_callable(self) -> None:
        """write_postmortem must be callable."""
        assert callable(postmortem.write_postmortem)

    def test_should_dump_is_callable(self) -> None:
        """should_dump must be callable."""
        assert callable(postmortem.should_dump)

    def test_build_task_summary_is_callable(self) -> None:
        """_build_task_summary internal helper must be callable."""
        assert callable(postmortem._build_task_summary)

    def test_build_blocked_tasks_is_callable(self) -> None:
        """_build_blocked_tasks internal helper must be callable."""
        assert callable(postmortem._build_blocked_tasks)

    def test_build_session_history_is_callable(self) -> None:
        """_build_session_history internal helper must be callable."""
        assert callable(postmortem._build_session_history)


class TestBuildPostmortemWithStubs:
    """TS-01-23: build_postmortem accepts any PostmortemInput-satisfying object.

    Requirement: 01-REQ-6.4
    """

    def test_returns_non_none_result(self) -> None:
        """build_postmortem must return a non-None result with a stub input."""
        result = postmortem.build_postmortem(_StubPostmortemInput())
        assert result is not None

    def test_returns_dict(self) -> None:
        """build_postmortem must return a dict."""
        result = postmortem.build_postmortem(_StubPostmortemInput())
        assert isinstance(result, dict)

    def test_result_contains_run_id(self) -> None:
        """The postmortem dict must contain a run_id key."""
        result = postmortem.build_postmortem(_StubPostmortemInput())
        assert "run_id" in result

    def test_no_agentfox_import_error(self) -> None:
        """build_postmortem must not require agentfox types."""
        # If we got here without ImportError, the function works
        # with protocol-only stubs — no agentfox dependency.
        result = postmortem.build_postmortem(_StubPostmortemInput())
        assert result is not None


class TestPostmortemLogging:
    """TS-01-24: afaudit.postmortem uses stdlib logging with 'afaudit.postmortem'.

    Requirement: 01-REQ-6.5
    """

    def test_imports_stdlib_logging(self) -> None:
        """postmortem.py must import stdlib logging."""
        source = POSTMORTEM_SOURCE.read_text(encoding="utf-8")
        assert "import logging" in source

    def test_uses_correct_logger_name(self) -> None:
        """postmortem.py must create a logger named 'afaudit.postmortem'."""
        source = POSTMORTEM_SOURCE.read_text(encoding="utf-8")
        assert "getLogger('afaudit.postmortem')" in source or 'getLogger("afaudit.postmortem")' in source

    def test_no_loguru(self) -> None:
        """postmortem.py must not import loguru."""
        source = POSTMORTEM_SOURCE.read_text(encoding="utf-8")
        assert "loguru" not in source

    def test_no_structlog(self) -> None:
        """postmortem.py must not import structlog."""
        source = POSTMORTEM_SOURCE.read_text(encoding="utf-8")
        assert "structlog" not in source
