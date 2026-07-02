"""Tests for afaudit.events module — event model types, logging, and serialization.

TS-01-10: AuditEvent dataclass, AuditEventType StrEnum (55 members), AuditSeverity StrEnum,
          AuditJsonlSink class, and four helper functions
TS-01-11: stdlib logging with 'afaudit.events' logger
TS-01-12: event_to_json serializes to valid JSON
TS-01-13: event_from_json round-trips correctly

Note: The spec states AuditEventType has 49 members (01-REQ-3.1), but the
actual enum in the source has 55 members. See docs/errata/01_audit_event_type_count.md.
This test asserts the actual count (55) to match the migrated code.
"""

from __future__ import annotations

import dataclasses
import enum
import json
from pathlib import Path

import afaudit.events as events

WORKSPACE_ROOT = Path(__file__).resolve().parents[3]
EVENTS_SOURCE = WORKSPACE_ROOT / "packages" / "afaudit" / "afaudit" / "events.py"


class TestEventModelTypes:
    """TS-01-10: Event model type definitions.

    Requirement: 01-REQ-3.1
    """

    def test_audit_event_is_dataclass(self) -> None:
        """AuditEvent must be a dataclass."""
        assert dataclasses.is_dataclass(events.AuditEvent)

    def test_audit_event_type_is_str_enum(self) -> None:
        """AuditEventType must be a StrEnum (inherits from str and Enum)."""
        assert issubclass(events.AuditEventType, str)
        assert issubclass(events.AuditEventType, enum.Enum)

    def test_audit_event_type_has_55_members(self) -> None:
        """AuditEventType must have exactly 55 members.

        Note: spec says 49 but the actual source has 55.
        See docs/errata/01_audit_event_type_count.md.
        """
        assert len(events.AuditEventType) == 55, f"Expected 55 AuditEventType members, got {len(events.AuditEventType)}"

    def test_audit_severity_is_str_enum(self) -> None:
        """AuditSeverity must be a StrEnum (inherits from str and Enum)."""
        assert issubclass(events.AuditSeverity, str)
        assert issubclass(events.AuditSeverity, enum.Enum)

    def test_audit_jsonl_sink_is_class(self) -> None:
        """AuditJsonlSink must be a class (not a function or module)."""
        assert isinstance(events.AuditJsonlSink, type)

    def test_default_severity_for_is_callable(self) -> None:
        """default_severity_for must be callable."""
        assert callable(events.default_severity_for)

    def test_generate_run_id_is_callable(self) -> None:
        """generate_run_id must be callable."""
        assert callable(events.generate_run_id)

    def test_event_to_json_is_callable(self) -> None:
        """event_to_json must be callable."""
        assert callable(events.event_to_json)

    def test_event_from_json_is_callable(self) -> None:
        """event_from_json must be callable."""
        assert callable(events.event_from_json)


class TestEventsLogging:
    """TS-01-11: events.py uses stdlib logging with 'afaudit.events' logger.

    Requirement: 01-REQ-3.2
    """

    def test_imports_stdlib_logging(self) -> None:
        """events.py must import stdlib logging."""
        source = EVENTS_SOURCE.read_text(encoding="utf-8")
        assert "import logging" in source

    def test_uses_correct_logger_name(self) -> None:
        """events.py must create a logger named 'afaudit.events'."""
        source = EVENTS_SOURCE.read_text(encoding="utf-8")
        assert "getLogger('afaudit.events')" in source or 'getLogger("afaudit.events")' in source

    def test_no_loguru(self) -> None:
        """events.py must not import loguru."""
        source = EVENTS_SOURCE.read_text(encoding="utf-8")
        assert "loguru" not in source

    def test_no_structlog(self) -> None:
        """events.py must not import structlog."""
        source = EVENTS_SOURCE.read_text(encoding="utf-8")
        assert "structlog" not in source


class TestEventToJson:
    """TS-01-12: event_to_json serializes AuditEvent to valid JSON.

    Requirement: 01-REQ-3.3
    """

    def test_returns_valid_json_string(self) -> None:
        """event_to_json must return a valid JSON string."""
        run_id = events.generate_run_id()
        first_event_type = list(events.AuditEventType)[0]
        event = events.AuditEvent(
            run_id=run_id,
            event_type=first_event_type,
        )
        result = events.event_to_json(event)
        assert isinstance(result, str)
        parsed = json.loads(result)
        assert parsed is not None


class TestEventFromJson:
    """TS-01-13: event_from_json deserializes JSON back to equal AuditEvent.

    Requirement: 01-REQ-3.4
    """

    def test_round_trip_preserves_equality(self) -> None:
        """Serialize then deserialize must yield an equal AuditEvent."""
        run_id = events.generate_run_id()
        first_event_type = list(events.AuditEventType)[0]
        original = events.AuditEvent(
            run_id=run_id,
            event_type=first_event_type,
        )
        json_str = events.event_to_json(original)
        roundtripped = events.event_from_json(json_str)
        assert roundtripped == original
