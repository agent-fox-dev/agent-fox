# Requirements Document

## Introduction

This spec removes the dead `--debug` CLI flag from the `code` command and all
internal plumbing that carries the flag value. The audit trail and tool
telemetry features that `--debug` was originally designed to gate are now
always-on (since fix #282 and spec 103), making the flag a no-op.

## Glossary

- **DuckDBSink**: Session sink implementation that writes session outcomes and
  tool signals to the DuckDB knowledge store.
- **SinkDispatcher**: Multiplexer that fans out sink calls to all registered
  `SessionSink` implementations.
- **AgentTraceSink**: Sink that writes structured JSONL agent conversation
  traces to the audit directory.
- **SessionSink**: Protocol defining the interface for recording session
  events (outcomes, tool calls, tool errors, audit events).
- **dry-run mutual exclusion**: Logic in `_check_dry_run_conflicts` that
  rejects flag combinations incompatible with `--dry-run`.

## Requirements

### Requirement 1: Remove `--debug` CLI Flag

**User Story:** As a CLI user, I want the `code` command to not expose a
no-op `--debug` flag, so that the CLI surface accurately reflects available
functionality.

#### Acceptance Criteria

[131-REQ-1.1] THE `code` command SHALL NOT accept a `--debug` flag.

[131-REQ-1.2] WHEN a user runs `agent-fox code --help`, THE system SHALL NOT
list `--debug` in the output.

[131-REQ-1.3] WHEN a user runs `agent-fox code --debug`, THE system SHALL
reject the invocation with a Click "no such option" error.

#### Edge Cases

[131-REQ-1.E1] WHEN a user runs `agent-fox code --dry-run`, THE system SHALL
NOT check for `--debug` in the mutual exclusion logic.

### Requirement 2: Remove `debug` Parameter from Internal APIs

**User Story:** As a developer, I want the internal function signatures to not
carry a dead `debug` parameter, so that the API surface accurately reflects
behavior.

#### Acceptance Criteria

[131-REQ-2.1] THE `run_code()` function in `engine/run.py` SHALL NOT accept a
`debug` keyword argument.

[131-REQ-2.2] THE `_setup_infrastructure()` function in `engine/run.py` SHALL
NOT accept a `debug` keyword argument.

[131-REQ-2.3] THE `DuckDBSink.__init__()` in `knowledge/duckdb_sink.py` SHALL
NOT accept a `debug` keyword argument AND SHALL NOT store a `_debug` instance
attribute.

#### Edge Cases

[131-REQ-2.E1] WHEN `DuckDBSink` is constructed without a `debug` argument,
THE system SHALL record session outcomes, tool calls, and tool errors
identically to the previous always-on behavior.

### Requirement 3: Update Dry-Run Mutual Exclusion

**User Story:** As a CLI user, I want the dry-run conflict check to only
mention flags that actually exist, so that error messages are accurate.

#### Acceptance Criteria

[131-REQ-3.1] THE `_check_dry_run_conflicts()` function SHALL accept only
`dry_run`, `watch`, and `force_clean` parameters (no `debug` parameter).

[131-REQ-3.2] WHEN `--dry-run` is combined with `--watch`, THE system SHALL
reject the invocation and list `--watch` as the incompatible flag.

[131-REQ-3.3] WHEN `--dry-run` is combined with `--force-clean`, THE system
SHALL reject the invocation and list `--force-clean` as the incompatible flag.

### Requirement 4: Update Stale Docstrings and Documentation

**User Story:** As a developer, I want docstrings and documentation to
accurately describe always-on behavior rather than referencing a removed debug
mode.

#### Acceptance Criteria

[131-REQ-4.1] THE `DuckDBSink` class docstring SHALL NOT reference a `debug`
parameter or debug-gated behavior.

[131-REQ-4.2] THE `duckdb_sink.py` module docstring SHALL describe tool
signals as always-on (not "debug-only").

[131-REQ-4.3] THE `SessionSink.record_tool_call()` docstring SHALL NOT
reference "non-debug mode".

[131-REQ-4.4] THE `SessionSink.record_tool_error()` docstring SHALL NOT
reference "non-debug mode".

[131-REQ-4.5] THE `docs/cli-reference.md` SHALL NOT list `--debug` in the
`code` command options table.

[131-REQ-4.6] THE `docs/cli-reference.md` dry-run mutual exclusion paragraph
SHALL NOT mention `--debug`.
