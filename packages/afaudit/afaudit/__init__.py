"""afaudit — audit file-writing infrastructure for agent-fox.

Re-exports the public API so consumers can ``from afaudit import <symbol>``.
"""

from afaudit.cleanup import enforce_file_retention, purge_stale_audit_files
from afaudit.constants import AUDIT_DIR
from afaudit.emit import emit_audit_event
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
from afaudit.postmortem import (
    PostmortemInput,
    SessionRecordLike,
    build_postmortem,
    should_dump,
    write_postmortem,
)
from afaudit.sink import (
    SessionOutcome,
    SessionSink,
    SinkDispatcher,
    ToolCall,
    ToolError,
)
from afaudit.trace import (
    AgentTraceSink,
    reconstruct_transcript,
    truncate_tool_input,
)

__all__ = [
    # cleanup
    "enforce_file_retention",
    "purge_stale_audit_files",
    # constants
    "AUDIT_DIR",
    # emit
    "emit_audit_event",
    # events
    "AuditEvent",
    "AuditEventType",
    "AuditJsonlSink",
    "AuditSeverity",
    "default_severity_for",
    "event_from_json",
    "event_to_json",
    "generate_run_id",
    # postmortem
    "PostmortemInput",
    "SessionRecordLike",
    "build_postmortem",
    "should_dump",
    "write_postmortem",
    # sink
    "SessionOutcome",
    "SessionSink",
    "SinkDispatcher",
    "ToolCall",
    "ToolError",
    # trace
    "AgentTraceSink",
    "reconstruct_transcript",
    "truncate_tool_input",
]
