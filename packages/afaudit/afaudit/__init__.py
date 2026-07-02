"""afaudit — audit file-writing infrastructure for agent-fox.

Re-exports the public API so consumers can ``from afaudit import <symbol>``.
Symbols are added incrementally as submodules are implemented.
"""

from afaudit.constants import AUDIT_DIR
from afaudit.events import (
    AuditEvent,
    AuditEventType,
    AuditJsonlSink,
    AuditSeverity,
    default_severity_for,
    event_from_json,
    event_to_json,
    generate_run_id,
)

__all__ = [
    # constants
    "AUDIT_DIR",
    # events
    "AuditEvent",
    "AuditEventType",
    "AuditJsonlSink",
    "AuditSeverity",
    "default_severity_for",
    "event_from_json",
    "event_to_json",
    "generate_run_id",
]
