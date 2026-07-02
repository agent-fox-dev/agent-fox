"""Tests for afaudit public API re-exports.

TS-01-4: Event model symbols from afaudit.events
TS-01-5: Sink symbols from afaudit.sink
TS-01-6: Trace symbols from afaudit.trace
TS-01-7: Postmortem symbols from afaudit.postmortem
TS-01-8: Emit, cleanup, constants symbols
TS-01-9: AuditJsonlSink placement in events (not sink)
"""

from __future__ import annotations

from pathlib import Path

import afaudit
import afaudit.sink


class TestEventsReexports:
    """TS-01-4: All eight event-model symbols re-exported from afaudit.

    Requirement: 01-REQ-2.1
    """

    def test_all_event_symbols_importable(self) -> None:
        """All eight event symbols are importable via 'from afaudit import <symbol>'."""
        symbols = [
            "AuditEvent",
            "AuditEventType",
            "AuditSeverity",
            "AuditJsonlSink",
            "default_severity_for",
            "generate_run_id",
            "event_to_json",
            "event_from_json",
        ]
        for sym in symbols:
            assert hasattr(afaudit, sym), f"Missing re-export: {sym}"

    def test_event_symbols_resolve_to_events_module(self) -> None:
        """Each event symbol resolves to its definition in afaudit.events."""
        symbols = [
            "AuditEvent",
            "AuditEventType",
            "AuditSeverity",
            "AuditJsonlSink",
            "default_severity_for",
            "generate_run_id",
            "event_to_json",
            "event_from_json",
        ]
        for sym in symbols:
            obj = getattr(afaudit, sym)
            module = getattr(obj, "__module__", "")
            assert module.startswith("afaudit.events"), f"{sym}.__module__ is {module!r}, expected 'afaudit.events...'"


class TestSinkReexports:
    """TS-01-5: All five sink symbols re-exported from afaudit.

    Requirement: 01-REQ-2.2
    """

    def test_all_sink_symbols_importable(self) -> None:
        """All five sink symbols are importable via 'from afaudit import <symbol>'."""
        symbols = [
            "SessionSink",
            "SinkDispatcher",
            "SessionOutcome",
            "ToolCall",
            "ToolError",
        ]
        for sym in symbols:
            assert hasattr(afaudit, sym), f"Missing re-export: {sym}"

    def test_sink_symbols_resolve_to_sink_module(self) -> None:
        """Each sink symbol resolves to its definition in afaudit.sink."""
        symbols = [
            "SessionSink",
            "SinkDispatcher",
            "SessionOutcome",
            "ToolCall",
            "ToolError",
        ]
        for sym in symbols:
            obj = getattr(afaudit, sym)
            module = getattr(obj, "__module__", "")
            assert module.startswith("afaudit.sink"), f"{sym}.__module__ is {module!r}, expected 'afaudit.sink...'"


class TestTraceReexports:
    """TS-01-6: All three trace symbols re-exported from afaudit.

    Requirement: 01-REQ-2.3
    """

    def test_all_trace_symbols_importable(self) -> None:
        """All three trace symbols are importable via 'from afaudit import <symbol>'."""
        symbols = [
            "AgentTraceSink",
            "reconstruct_transcript",
            "truncate_tool_input",
        ]
        for sym in symbols:
            assert hasattr(afaudit, sym), f"Missing re-export: {sym}"

    def test_trace_symbols_resolve_to_trace_module(self) -> None:
        """Each trace symbol resolves to its definition in afaudit.trace."""
        symbols = [
            "AgentTraceSink",
            "reconstruct_transcript",
            "truncate_tool_input",
        ]
        for sym in symbols:
            obj = getattr(afaudit, sym)
            module = getattr(obj, "__module__", "")
            assert module.startswith("afaudit.trace"), f"{sym}.__module__ is {module!r}, expected 'afaudit.trace...'"


class TestPostmortemReexports:
    """TS-01-7: All five postmortem symbols re-exported from afaudit.

    Requirement: 01-REQ-2.4
    """

    def test_all_postmortem_symbols_importable(self) -> None:
        """All five postmortem symbols are importable via 'from afaudit import <symbol>'."""
        symbols = [
            "PostmortemInput",
            "SessionRecordLike",
            "build_postmortem",
            "write_postmortem",
            "should_dump",
        ]
        for sym in symbols:
            assert hasattr(afaudit, sym), f"Missing re-export: {sym}"

    def test_postmortem_symbols_resolve_to_postmortem_module(self) -> None:
        """Each postmortem symbol resolves to its definition in afaudit.postmortem."""
        symbols = [
            "PostmortemInput",
            "SessionRecordLike",
            "build_postmortem",
            "write_postmortem",
            "should_dump",
        ]
        for sym in symbols:
            obj = getattr(afaudit, sym)
            module = getattr(obj, "__module__", "")
            assert module.startswith("afaudit.postmortem"), (
                f"{sym}.__module__ is {module!r}, expected 'afaudit.postmortem...'"
            )


class TestEmitCleanupConstantsReexports:
    """TS-01-8: emit, cleanup, and constants symbols re-exported from afaudit.

    Requirement: 01-REQ-2.5
    """

    def test_emit_audit_event_importable(self) -> None:
        """emit_audit_event is importable from afaudit."""
        assert hasattr(afaudit, "emit_audit_event")

    def test_purge_stale_audit_files_importable(self) -> None:
        """purge_stale_audit_files is importable from afaudit."""
        assert hasattr(afaudit, "purge_stale_audit_files")

    def test_enforce_file_retention_importable(self) -> None:
        """enforce_file_retention is importable from afaudit."""
        assert hasattr(afaudit, "enforce_file_retention")

    def test_audit_dir_importable_and_correct(self) -> None:
        """AUDIT_DIR is importable from afaudit and equals Path('.agent-fox/audit')."""
        assert hasattr(afaudit, "AUDIT_DIR")
        assert afaudit.AUDIT_DIR == Path(".agent-fox/audit")


class TestAuditJsonlSinkPlacement:
    """TS-01-9: AuditJsonlSink defined in afaudit.events, not afaudit.sink.

    Requirement: 01-REQ-2.6
    """

    def test_audit_jsonl_sink_in_events(self) -> None:
        """'from afaudit.events import AuditJsonlSink' succeeds."""
        from afaudit.events import AuditJsonlSink  # noqa: F811

        assert AuditJsonlSink.__module__ == "afaudit.events"

    def test_audit_jsonl_sink_not_in_sink(self) -> None:
        """afaudit.sink does not define AuditJsonlSink."""
        assert not hasattr(afaudit.sink, "AuditJsonlSink")
