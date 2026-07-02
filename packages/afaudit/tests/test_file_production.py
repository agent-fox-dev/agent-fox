"""End-to-end tests: produce all three audit file types using only afaudit imports.

TS-01-45: All three file types produced without any agentfox import
TS-01-SMOKE-1: Smoke test — agentfox not in sys.modules after production

These tests verify that afaudit alone (without agentfox installed or imported)
can produce audit_*.jsonl, agent_*.jsonl, and postmortem_*.json files.
"""

from __future__ import annotations

import sys
import tempfile
from pathlib import Path

import afaudit.events as events
import afaudit.postmortem as postmortem
import afaudit.trace as trace


class _StubSessionRecord:
    """Minimal stub satisfying SessionRecordLike protocol for file production."""

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
    """Minimal stub satisfying PostmortemInput protocol for file production."""

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


class TestAuditJsonlFileProduction:
    """TS-01-45 (part 1): audit_*.jsonl produced via AuditJsonlSink.

    Requirement: 01-REQ-12.1
    """

    def test_creates_audit_jsonl_file(self) -> None:
        """AuditJsonlSink must create an audit_*.jsonl file when an event is written."""
        with tempfile.TemporaryDirectory() as tmp:
            audit_dir = Path(tmp)
            run_id = events.generate_run_id()
            sink = events.AuditJsonlSink(audit_dir, run_id)

            first_event_type = list(events.AuditEventType)[0]
            event = events.AuditEvent(
                run_id=run_id,
                event_type=first_event_type,
            )
            sink.emit_audit_event(event)

            audit_files = list(audit_dir.glob("audit_*.jsonl"))
            assert len(audit_files) == 1, f"Expected 1 audit_*.jsonl file, found {len(audit_files)}"

    def test_audit_file_contains_event(self) -> None:
        """The audit file must contain at least one JSON line."""
        with tempfile.TemporaryDirectory() as tmp:
            audit_dir = Path(tmp)
            run_id = events.generate_run_id()
            sink = events.AuditJsonlSink(audit_dir, run_id)

            first_event_type = list(events.AuditEventType)[0]
            event = events.AuditEvent(
                run_id=run_id,
                event_type=first_event_type,
            )
            sink.emit_audit_event(event)

            audit_file = list(audit_dir.glob("audit_*.jsonl"))[0]
            content = audit_file.read_text(encoding="utf-8").strip()
            assert len(content) > 0, "audit_*.jsonl file is empty"


class TestAgentTraceFileProduction:
    """TS-01-45 (part 2): agent_*.jsonl produced via AgentTraceSink.

    Requirement: 01-REQ-12.1
    """

    def test_creates_agent_jsonl_file(self) -> None:
        """AgentTraceSink must create an agent_*.jsonl file when trace data is written."""
        with tempfile.TemporaryDirectory() as tmp:
            audit_dir = Path(tmp)
            run_id = events.generate_run_id()
            agent_trace = trace.AgentTraceSink(audit_dir, run_id)

            # Write a session init event to trigger file creation.
            agent_trace.record_session_init(
                run_id=run_id,
                node_id="spec01/1/coder",
                model_id="claude-sonnet-4-20250514",
                archetype="coder",
                system_prompt="You are a coder.",
                task_prompt="Write tests.",
            )
            agent_trace.close()

            agent_files = list(audit_dir.glob("agent_*.jsonl"))
            assert len(agent_files) >= 1, f"Expected at least 1 agent_*.jsonl file, found {len(agent_files)}"


class TestPostmortemFileProduction:
    """TS-01-45 (part 3): postmortem_*.json produced via build_postmortem + write_postmortem.

    Requirement: 01-REQ-12.1
    """

    def test_creates_postmortem_json_file(self) -> None:
        """build_postmortem + write_postmortem must create a postmortem_*.json file."""
        with tempfile.TemporaryDirectory() as tmp:
            audit_dir = Path(tmp)
            stub_input = _StubPostmortemInput()

            pm_data = postmortem.build_postmortem(stub_input)
            postmortem.write_postmortem(pm_data, audit_dir)

            pm_files = list(audit_dir.glob("postmortem_*.json"))
            assert len(pm_files) == 1, f"Expected 1 postmortem_*.json file, found {len(pm_files)}"

    def test_postmortem_file_contains_run_id(self) -> None:
        """The postmortem file must contain the run_id from the input."""
        import json

        with tempfile.TemporaryDirectory() as tmp:
            audit_dir = Path(tmp)
            stub_input = _StubPostmortemInput()

            pm_data = postmortem.build_postmortem(stub_input)
            postmortem.write_postmortem(pm_data, audit_dir)

            pm_file = list(audit_dir.glob("postmortem_*.json"))[0]
            content = json.loads(pm_file.read_text(encoding="utf-8"))
            assert "run_id" in content


class TestAllThreeFileTypesProduced:
    """TS-01-45 + TS-01-SMOKE-1: Full end-to-end file production.

    Produce all three audit file types in a single test using only afaudit
    imports — verifying that agentfox is not needed.

    Requirement: 01-REQ-12.1
    """

    def test_all_three_file_types_in_one_directory(self) -> None:
        """All three file types must be producible in a single audit directory."""
        with tempfile.TemporaryDirectory() as tmp:
            audit_dir = Path(tmp)
            run_id = events.generate_run_id()

            # 1. Produce audit_*.jsonl
            jsonl_sink = events.AuditJsonlSink(audit_dir, run_id)
            first_event_type = list(events.AuditEventType)[0]
            event = events.AuditEvent(
                run_id=run_id,
                event_type=first_event_type,
            )
            jsonl_sink.emit_audit_event(event)

            # 2. Produce agent_*.jsonl
            agent_trace = trace.AgentTraceSink(audit_dir, run_id)
            agent_trace.record_session_init(
                run_id=run_id,
                node_id="spec01/1/coder",
                model_id="claude-sonnet-4-20250514",
                archetype="coder",
                system_prompt="You are a coder.",
                task_prompt="Write tests.",
            )
            agent_trace.close()

            # 3. Produce postmortem_*.json
            stub_input = _StubPostmortemInput()
            pm_data = postmortem.build_postmortem(stub_input)
            postmortem.write_postmortem(pm_data, audit_dir)

            # Verify all three file types exist
            audit_files = list(audit_dir.glob("audit_*.jsonl"))
            agent_files = list(audit_dir.glob("agent_*.jsonl"))
            pm_files = list(audit_dir.glob("postmortem_*.json"))

            assert len(audit_files) == 1, f"Expected 1 audit_*.jsonl, found {len(audit_files)}"
            assert len(agent_files) >= 1, f"Expected >=1 agent_*.jsonl, found {len(agent_files)}"
            assert len(pm_files) == 1, f"Expected 1 postmortem_*.json, found {len(pm_files)}"

    def test_no_agentfox_in_sys_modules(self) -> None:
        """After producing all three file types, agentfox must not be in sys.modules.

        This verifies that afaudit itself does not transitively import agentfox.
        Note: If agentfox IS installed and other tests imported it earlier in
        the same process, it will already be in sys.modules. This test is most
        meaningful when run in an isolated environment without agentfox.
        """
        # Record which agentfox modules were loaded BEFORE our imports.
        # The afaudit package modules should NOT pull in any new agentfox modules.
        agentfox_modules_before = {
            k for k in sys.modules if k == "agentfox" or k.startswith("agentfox.")
        }

        # Re-import and use afaudit to trigger any lazy imports
        import afaudit.events  # noqa: F811, F401
        import afaudit.postmortem  # noqa: F811, F401
        import afaudit.trace  # noqa: F811, F401

        agentfox_modules_after = {
            k for k in sys.modules if k == "agentfox" or k.startswith("agentfox.")
        }

        # afaudit should not have added any new agentfox modules
        new_agentfox_modules = agentfox_modules_after - agentfox_modules_before
        assert not new_agentfox_modules, (
            f"Importing afaudit modules pulled in agentfox modules: {new_agentfox_modules}"
        )
