"""Convenience function for emitting a single audit event to a sink.

Migrated from ``agentfox.engine.audit_helpers`` — only
:func:`emit_audit_event` is here; ``calculate_session_cost`` remains in
agentfox because it depends on agentfox-internal pricing models.
"""

from __future__ import annotations

import logging
from typing import TYPE_CHECKING

from afaudit.events import (
    AuditEvent,
    AuditEventType,
    AuditSeverity,
    default_severity_for,
)

if TYPE_CHECKING:
    from afaudit.sink import SessionSink, SinkDispatcher

logger = logging.getLogger("afaudit.emit")


def emit_audit_event(
    sink: SinkDispatcher | SessionSink | None,
    run_id: str,
    event_type: AuditEventType,
    *,
    node_id: str = "",
    session_id: str = "",
    archetype: str = "",
    severity: AuditSeverity | None = None,
    payload: dict | None = None,
) -> None:
    """Emit an audit event to the sink dispatcher (best-effort).

    If *sink* is ``None`` or *run_id* is empty the call is a no-op.
    Any exception during event construction or emission is caught and
    logged at DEBUG level to avoid disrupting the caller.
    """
    if sink is None or not run_id:
        return
    try:
        event = AuditEvent(
            run_id=run_id,
            event_type=event_type,
            severity=severity or default_severity_for(event_type),
            node_id=node_id,
            session_id=session_id,
            archetype=archetype,
            payload=payload or {},
        )
        sink.emit_audit_event(event)
    except Exception:
        logger.debug(
            "Failed to emit audit event %s",
            event_type,
            exc_info=True,
        )
