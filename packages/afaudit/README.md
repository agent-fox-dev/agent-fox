# afaudit

Zero-dependency audit infrastructure for
[agent-fox](https://github.com/agent-fox-dev/agent-fox). Provides structured
event logging, sink-based dispatch, postmortem generation, conversation trace
reconstruction, and file retention — all using only the Python standard library.

Requires Python 3.12+. No external dependencies.

## Installation

Install from the agent-fox monorepo via git:

```bash
pip install "afaudit @ git+https://github.com/agent-fox-dev/agent-fox.git#subdirectory=packages/afaudit"
```

Pin to a release tag:

```bash
pip install "afaudit @ git+https://github.com/agent-fox-dev/agent-fox.git@v4.1.17#subdirectory=packages/afaudit"
```

In `pyproject.toml`:

```toml
[project]
dependencies = [
    "afaudit @ git+https://github.com/agent-fox-dev/agent-fox.git@v4.1.17#subdirectory=packages/afaudit",
]
```

## Quick Start

```python
from pathlib import Path
from afaudit import (
    AuditEvent,
    AuditEventType,
    AuditJsonlSink,
    SinkDispatcher,
    emit_audit_event,
    generate_run_id,
)

# Generate a unique run ID
run_id = generate_run_id()  # e.g. "20260704_143022_a1b2c3"

# Set up the sink pipeline
audit_dir = Path(".agent-fox/audit")
sink = SinkDispatcher()
sink.add(AuditJsonlSink(audit_dir, run_id))

# Emit events (best-effort, never raises)
emit_audit_event(
    sink,
    run_id,
    AuditEventType.RUN_START,
    payload={"specs": ["01_foundation", "02_planning"]},
)

# Or construct events directly
event = AuditEvent(
    run_id=run_id,
    event_type=AuditEventType.SESSION_COMPLETE,
    node_id="01_foundation:1",
    payload={"duration_ms": 12000, "tokens": 50000},
)
sink.emit_audit_event(event)

sink.close()
```

## API Reference

All symbols are importable from the top-level package: `from afaudit import <symbol>`.

### Events

| Symbol | Description |
|--------|-------------|
| `AuditEvent` | Frozen dataclass — a structured record of a significant agent action. Fields: `run_id`, `event_type: AuditEventType`, `severity: AuditSeverity`, `node_id`, `session_id`, `archetype`, `payload: dict`, `id: UUID`, `timestamp: datetime`. |
| `AuditEventType` | StrEnum with ~50 event type constants. Categories: `run.*` (start, complete, limit_reached), `session.*` (start, complete, fail, retry, timeout_retry), `task.*` (status_change), `git.*` (merge, conflict, push_failed), `review.*` (parse_failure, findings_persisted), `knowledge.*`, `night_shift.*`, `watch.*`, `workspace.*`. |
| `AuditSeverity` | StrEnum: `info`, `warning`, `error`, `critical`. |
| `generate_run_id` | `() -> str` — Generate a unique run ID: `{YYYYMMDD}_{HHMMSS}_{6hex}`. |
| `default_severity_for` | `(event_type: AuditEventType) -> AuditSeverity` — Default severity for an event type (session.fail -> error, limit_reached -> warning, others -> info). |
| `event_to_json` | `(event: AuditEvent) -> str` — Serialize an event to JSON. |
| `event_from_json` | `(json_str: str) -> AuditEvent` — Deserialize JSON to an event. |
| `AuditJsonlSink` | `(audit_dir: Path, run_id: str)` — SessionSink that appends events as JSON lines to `audit_{run_id}.jsonl`. Thread-safe. |

### Emit

| Symbol | Signature | Description |
|--------|-----------|-------------|
| `emit_audit_event` | `(sink, run_id, event_type, *, node_id="", session_id="", archetype="", severity=None, payload=None) -> None` | Best-effort event emission. No-op when sink is None or run_id is empty. Never raises. |

### Sink Protocol

| Symbol | Description |
|--------|-------------|
| `SessionSink` | Runtime-checkable Protocol. Methods: `record_session_outcome(outcome)`, `record_tool_call(call)`, `record_tool_error(error)`, `emit_audit_event(event)`, `close()`. Implementations must handle their own error suppression. |
| `SinkDispatcher` | Fan-out dispatcher — forwards calls to multiple `SessionSink` implementations. Logs and swallows individual sink failures. `add(sink)` to register, all Protocol methods dispatch to all registered sinks. |
| `SessionOutcome` | Frozen dataclass — session completion record. Fields: `spec_name`, `task_group`, `node_id`, `status`, `input_tokens`, `output_tokens`, `duration_ms`, `error_message`, `response`, `is_transport_error`. |
| `ToolCall` | Frozen dataclass — tool invocation record. Fields: `session_id`, `node_id`, `tool_name`, `called_at`. |
| `ToolError` | Frozen dataclass — failed tool invocation. Fields: `session_id`, `node_id`, `tool_name`, `failed_at`. |

### Postmortem

| Symbol | Description |
|--------|-------------|
| `PostmortemInput` | Runtime-checkable Protocol — 11 attributes read from execution state: `run_id`, `run_status`, `node_states`, `total_cost`, `total_input_tokens`, `total_output_tokens`, `total_sessions`, `blocked_reasons`, `session_history`, `started_at`, `updated_at`. |
| `SessionRecordLike` | Runtime-checkable Protocol — 12 attributes per session record: `node_id`, `attempt`, `status`, `archetype`, `model`, `duration_ms`, `cost`, `error_message`, `timestamp`, `is_transport_error`, `is_budget_exhausted`, `is_non_retryable`. |
| `build_postmortem` | `(state: PostmortemInput) -> dict` — Build a postmortem data structure from execution state. |
| `write_postmortem` | `(postmortem: dict, audit_dir: Path) -> Path` — Write postmortem JSON to `audit_dir/postmortem_{run_id}.json`. Returns the written path. |
| `should_dump` | `(run_status: str) -> bool` — Returns True for terminal failure statuses (stalled, block_limit, cost_limit, session_limit). |

### Trace

| Symbol | Description |
|--------|-------------|
| `AgentTraceSink` | JSONL conversation trace sink. Writes `agent_{run_id}.jsonl` with session.init, assistant.message, tool.use, tool.error, and session.result events. |
| `reconstruct_transcript` | `(audit_dir: Path, run_id: str, node_id: str) -> str` — Reconstruct the full conversation transcript for a node from the trace JSONL. Returns empty string if not found. |
| `truncate_tool_input` | `(text: str, max_len: int = 2000) -> str` — Truncate tool input text for trace recording. |

### Cleanup

| Symbol | Signature | Description |
|--------|-----------|-------------|
| `purge_stale_audit_files` | `(audit_dir: Path) -> int` | Delete stale `agent_*.jsonl`, `audit_*.jsonl`, and `postmortem_*.json` files. Returns count of deleted files. |
| `enforce_file_retention` | `(audit_dir: Path, *, max_runs: int = 10) -> int` | Delete the oldest audit run file sets beyond `max_runs`. Returns count of deleted files. |

### Constants

| Symbol | Value | Description |
|--------|-------|-------------|
| `AUDIT_DIR` | `Path(".agent-fox/audit")` | Default audit directory path. |

## Implementing a Custom Sink

Any object satisfying the `SessionSink` protocol can be registered with
`SinkDispatcher`:

```python
from afaudit import SessionSink, SinkDispatcher, SessionOutcome, AuditEvent

class MyDatabaseSink:
    def record_session_outcome(self, outcome: SessionOutcome) -> None:
        db.insert("sessions", outcome.node_id, outcome.status, outcome.duration_ms)

    def record_tool_call(self, call):
        pass  # no-op

    def record_tool_error(self, error):
        pass  # no-op

    def emit_audit_event(self, event: AuditEvent) -> None:
        db.insert("events", event.event_type, event.payload)

    def close(self) -> None:
        db.close()

dispatcher = SinkDispatcher()
dispatcher.add(MyDatabaseSink())
dispatcher.add(AuditJsonlSink(audit_dir, run_id))
```
