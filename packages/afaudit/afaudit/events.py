"""Structured audit log data model, enums, and utilities.

Provides the AuditEvent dataclass, AuditEventType and AuditSeverity enums,
run ID generation, serialization helpers, and the AuditJsonlSink class.

Migrated from agentfox.knowledge.audit with behaviour preserved.
"""

from __future__ import annotations

import json
import logging
import threading
from dataclasses import dataclass, field
from datetime import UTC, datetime
from enum import StrEnum
from pathlib import Path
from typing import Any
from uuid import UUID, uuid4

logger = logging.getLogger("afaudit.events")


# ---------------------------------------------------------------------------
# Enums
# ---------------------------------------------------------------------------


class AuditSeverity(StrEnum):
    """Severity levels for audit events."""

    INFO = "info"
    WARNING = "warning"
    ERROR = "error"
    CRITICAL = "critical"


class AuditEventType(StrEnum):
    """Event type constants for audit events."""

    RUN_START = "run.start"
    RUN_COMPLETE = "run.complete"
    RUN_LIMIT_REACHED = "run.limit_reached"
    SESSION_START = "session.start"
    SESSION_COMPLETE = "session.complete"
    SESSION_FAIL = "session.fail"
    SESSION_RETRY = "session.retry"
    SESSION_TIMEOUT_RETRY = "session.timeout_retry"
    SESSION_ENVIRONMENT_FAILURE = "session.environment_failure"
    TASK_STATUS_CHANGE = "task.status_change"
    MODEL_ASSESSMENT = "model.assessment"
    TOOL_INVOCATION = "tool.invocation"
    TOOL_ERROR = "tool.error"
    GIT_MERGE = "git.merge"
    GIT_CONFLICT = "git.conflict"
    HARVEST_COMPLETE = "harvest.complete"
    HARVEST_EMPTY = "harvest.empty"
    FACT_EXTRACTED = "fact.extracted"
    FACT_COMPACTED = "fact.compacted"
    FACT_CAUSAL_LINKS = "fact.causal_links"
    KNOWLEDGE_INGESTED = "knowledge.ingested"
    SYNC_BARRIER = "sync.barrier"
    CONFIG_RELOADED = "config.reloaded"
    REVIEW_PARSE_FAILURE = "review.parse_failure"
    REVIEW_PARSE_RETRY_SUCCESS = "review.parse_retry_success"
    REVIEW_FINDINGS_PERSISTED = "review.findings_persisted"
    REVIEW_VERDICTS_PERSISTED = "review.verdicts_persisted"
    REVIEW_DRIFT_PERSISTED = "review.drift_persisted"
    VERDICT_NORMALIZED = "review.verdict_normalized"
    NIGHT_SHIFT_START = "night_shift.start"
    NIGHT_SHIFT_STOP = "night_shift.stop"
    ISSUE_CREATED = "night_shift.issue_created"
    ISSUE_SUPERSEDED = "night_shift.issue_superseded"
    ISSUE_OBSOLETE = "night_shift.issue_obsolete"
    FIX_START = "night_shift.fix_start"
    FIX_COMPLETE = "night_shift.fix_complete"
    FIX_FAILED = "night_shift.fix_failed"
    WATCH_POLL = "watch.poll"
    SECURITY_FINDING_BLOCKED = "review.security_finding_blocked"
    FACT_CLEANUP = "fact.cleanup"
    CONSOLIDATION_COMPLETE = "consolidation.complete"
    CONSOLIDATION_COST = "consolidation.cost"
    SLEEP_COMPUTE_COMPLETE = "SLEEP_COMPUTE_COMPLETE"
    KNOWLEDGE_RETRIEVAL = "knowledge.retrieval"
    PREFLIGHT_SKIP = "preflight.skip"
    WORKSPACE_HEALTH_CHECK = "workspace.health_check"
    WORKSPACE_FORCE_CLEAN = "workspace.force_clean"
    DEVELOP_SYNC = "develop.sync"
    DEVELOP_SYNC_FAILED = "develop.sync_failed"
    DEVELOP_FETCH_FAILED = "develop.fetch_failed"
    RUN_STALE_DETECTED = "run.stale_detected"
    GIT_PUSH_FAILED = "git.push_failed"
    GIT_PUSH_RETRY_SUCCESS = "git.push_retry_success"
    WORKSPACE_SETUP_FAILED = "workspace.setup_failed"
    RUN_PREFLIGHT = "run.preflight"


# ---------------------------------------------------------------------------
# Data model
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class AuditEvent:
    """Structured record of a significant agent action."""

    run_id: str
    event_type: AuditEventType
    severity: AuditSeverity = AuditSeverity.INFO
    node_id: str = ""
    session_id: str = ""
    archetype: str = ""
    payload: dict[str, Any] = field(default_factory=dict)
    id: UUID = field(default_factory=uuid4)
    timestamp: datetime = field(default_factory=lambda: datetime.now(UTC))


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def generate_run_id() -> str:
    """Generate a unique run ID: {YYYYMMDD}_{HHMMSS}_{short_hex}.

    The short_hex is the first 6 characters of a UUID4 hex string,
    ensuring uniqueness even when two runs start in the same second.
    """
    now = datetime.now(UTC)
    short_hex = uuid4().hex[:6]
    return f"{now:%Y%m%d}_{now:%H%M%S}_{short_hex}"


_SEVERITY_MAP: dict[AuditEventType, AuditSeverity] = {
    AuditEventType.SESSION_FAIL: AuditSeverity.ERROR,
    AuditEventType.RUN_LIMIT_REACHED: AuditSeverity.WARNING,
    AuditEventType.GIT_CONFLICT: AuditSeverity.WARNING,
    AuditEventType.HARVEST_EMPTY: AuditSeverity.WARNING,
    AuditEventType.REVIEW_PARSE_FAILURE: AuditSeverity.WARNING,
}


def default_severity_for(event_type: AuditEventType) -> AuditSeverity:
    """Return the default severity for a given event type.

    - session.fail -> error
    - run.limit_reached, git.conflict -> warning
    - all others -> info
    """
    return _SEVERITY_MAP.get(event_type, AuditSeverity.INFO)


# ---------------------------------------------------------------------------
# Serialization
# ---------------------------------------------------------------------------


def event_to_json(event: AuditEvent) -> str:
    """Serialize an AuditEvent to a JSON string."""
    return json.dumps(
        {
            "id": str(event.id),
            "timestamp": event.timestamp.isoformat(),
            "run_id": event.run_id,
            "event_type": event.event_type.value,
            "node_id": event.node_id,
            "session_id": event.session_id,
            "archetype": event.archetype,
            "severity": event.severity.value,
            "payload": event.payload,
        }
    )


def event_from_json(json_str: str) -> AuditEvent:
    """Deserialize a JSON string to an AuditEvent."""
    data = json.loads(json_str)
    return AuditEvent(
        id=UUID(data["id"]),
        timestamp=datetime.fromisoformat(data["timestamp"]),
        run_id=data["run_id"],
        event_type=AuditEventType(data["event_type"]),
        node_id=data.get("node_id", ""),
        session_id=data.get("session_id", ""),
        archetype=data.get("archetype", ""),
        severity=AuditSeverity(data["severity"]),
        payload=data.get("payload", {}),
    )


# ---------------------------------------------------------------------------
# AuditJsonlSink
# ---------------------------------------------------------------------------


class AuditJsonlSink:
    """SessionSink that writes audit events to a JSONL file.

    One file per run: .agent-fox/audit/audit_{run_id}.jsonl
    Other SessionSink methods are no-ops.
    """

    def __init__(self, audit_dir: Path, run_id: str) -> None:
        self._audit_dir = audit_dir
        self._run_id = run_id
        self._file_path = audit_dir / f"audit_{run_id}.jsonl"
        self._lock = threading.Lock()
        try:
            self._audit_dir.mkdir(parents=True, exist_ok=True)
        except OSError:
            logger.warning("Failed to create audit directory: %s", self._audit_dir)

    def emit_audit_event(self, event: AuditEvent) -> None:
        """Append a JSON line to the audit file.

        Uses a threading.Lock to serialize concurrent writes from multiple
        threads, preventing interleaved or lost appends.
        """
        line = event_to_json(event)
        try:
            with self._lock:
                with open(self._file_path, "a") as f:
                    f.write(line + "\n")
        except OSError:
            logger.warning("Failed to write audit event to %s", self._file_path)

    def record_session_outcome(self, outcome: object) -> None:
        """No-op -- handled by other sinks."""

    def record_tool_call(self, call: object) -> None:
        """No-op -- handled by other sinks."""

    def record_tool_error(self, error: object) -> None:
        """No-op -- handled by other sinks."""

    def close(self) -> None:
        """No-op -- file handle opened/closed per write."""
