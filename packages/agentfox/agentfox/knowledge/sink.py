"""SessionSink Protocol definition, sink dispatcher, event dataclasses.

Requirements: 11-REQ-4.1, 11-REQ-4.2, 11-REQ-4.3, 40-REQ-4.1, 40-REQ-4.2
"""

from __future__ import annotations

import logging
from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import TYPE_CHECKING, Any, Protocol, runtime_checkable
from uuid import UUID, uuid4

if TYPE_CHECKING:
    from agentfox.knowledge.audit import AuditEvent

logger = logging.getLogger("agentfox.knowledge.sink")


@dataclass(frozen=True)
class SessionOutcome:
    """Structured record of a completed coding session."""

    id: UUID = field(default_factory=uuid4)
    spec_name: str = ""
    task_group: str = ""
    node_id: str = ""
    touched_paths: list[str] = field(default_factory=list)
    status: str = ""  # "completed" | "failed" | "timeout"
    input_tokens: int = 0
    output_tokens: int = 0
    cache_read_input_tokens: int = 0
    cache_creation_input_tokens: int = 0
    duration_ms: int = 0
    error_message: str | None = None
    response: str = ""  # Last assistant message text (used by review parsers)
    created_at: datetime = field(default_factory=lambda: datetime.now(UTC))
    is_transport_error: bool = False  # True when failure was a transient connection error


@dataclass(frozen=True)
class ToolCall:
    """Structured record of a tool invocation."""

    id: UUID = field(default_factory=uuid4)
    session_id: str = ""
    node_id: str = ""
    tool_name: str = ""
    called_at: datetime = field(default_factory=lambda: datetime.now(UTC))


@dataclass(frozen=True)
class ToolError:
    """Structured record of a failed tool invocation."""

    id: UUID = field(default_factory=uuid4)
    session_id: str = ""
    node_id: str = ""
    tool_name: str = ""
    failed_at: datetime = field(default_factory=lambda: datetime.now(UTC))


@runtime_checkable
class SessionSink(Protocol):
    """Protocol for recording session events.

    Implementations must handle their own error suppression -- a sink
    failure must never prevent the session runner from continuing.
    """

    def record_session_outcome(self, outcome: SessionOutcome) -> None:
        """Record a session outcome. Called after every session."""
        ...

    def record_tool_call(self, call: ToolCall) -> None:
        """Record a tool invocation (always-on)."""
        ...

    def record_tool_error(self, error: ToolError) -> None:
        """Record a tool error (always-on)."""
        ...

    def emit_audit_event(self, event: AuditEvent) -> None:
        """Record a structured audit event.

        Requirement: 40-REQ-4.1
        """
        ...

    def close(self) -> None:
        """Release any resources held by this sink."""
        ...


class SinkDispatcher:
    """Dispatches events to multiple SessionSink implementations."""

    def __init__(self, sinks: list[SessionSink] | None = None) -> None:
        self._sinks: list[SessionSink] = sinks or []

    def add(self, sink: SessionSink) -> None:
        """Add a sink to the dispatch list."""
        self._sinks.append(sink)

    def _dispatch(self, method: str, *args: object) -> None:
        """Forward a call to all sinks, logging and swallowing individual failures."""
        for sink in self._sinks:
            try:
                getattr(sink, method)(*args)
            except Exception:
                logger.warning("Sink %s failed on %s", type(sink).__name__, method, exc_info=True)

    def record_session_outcome(self, outcome: SessionOutcome) -> None:
        """Dispatch to all sinks. Logs and swallows individual failures."""
        self._dispatch("record_session_outcome", outcome)

    def record_tool_call(self, call: ToolCall) -> None:
        """Dispatch to all sinks. Logs and swallows individual failures."""
        self._dispatch("record_tool_call", call)

    def record_tool_error(self, error: ToolError) -> None:
        """Dispatch to all sinks. Logs and swallows individual failures."""
        self._dispatch("record_tool_error", error)

    def emit_audit_event(self, event: AuditEvent) -> None:
        """Dispatch to all sinks. Logs and swallows individual failures.

        Requirement: 40-REQ-4.2, 40-REQ-4.E1
        """
        self._dispatch("emit_audit_event", event)

    def close(self) -> None:
        """Close all sinks."""
        self._dispatch("close")

    # -- Trace-specific dispatch (duck-typed via hasattr) --------------------

    def _dispatch_optional(self, method: str, **kwargs: Any) -> None:
        """Forward to sinks that implement *method*, skipping those that don't.

        Requirements: 103-REQ-2.1, 103-REQ-3.1, 103-REQ-4.1, 103-REQ-5.1,
                      103-REQ-6.1
        """
        for sink in self._sinks:
            fn = getattr(sink, method, None)
            if fn is not None:
                try:
                    fn(**kwargs)
                except Exception:
                    logger.warning("Sink %s failed on %s", type(sink).__name__, method, exc_info=True)

    def record_session_init(self, **kwargs: Any) -> None:
        self._dispatch_optional("record_session_init", **kwargs)

    def record_assistant_message(self, **kwargs: Any) -> None:
        self._dispatch_optional("record_assistant_message", **kwargs)

    def record_tool_use(self, **kwargs: Any) -> None:
        self._dispatch_optional("record_tool_use", **kwargs)

    def record_tool_error_trace(self, **kwargs: Any) -> None:
        self._dispatch_optional("record_tool_error_trace", **kwargs)

    def record_session_result(self, **kwargs: Any) -> None:
        self._dispatch_optional("record_session_result", **kwargs)
