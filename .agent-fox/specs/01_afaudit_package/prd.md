---
spec_id: '01'
spec_name: afaudit_package
title: Afaudit Package
status: draft
created_at: '2026-07-02T09:27:28.438121+00:00'
updated_at: '2026-07-02T09:38:27.968253+00:00'
owner: ''
source: interactive
schema_version: 1
---
# Extract Audit File Writers into `afaudit` Package

## Background

This refactoring is part of a broader modularization initiative for the workspace. The workspace recently extracted `afspec` into a standalone package, establishing the pattern for how domain-specific logic is separated from the `agentfox` core. Audit code is the next candidate because dependency analysis shows it has clean boundaries — the core audit modules have zero inward dependencies on the rest of `agentfox`.

The primary driver is that audit infrastructure is currently locked inside `agentfox`, which carries 12+ third-party dependencies (duckdb, anthropic SDK, sentence-transformers, etc.). Any consumer that wants audit functionality must pull in the entire `agentfox` dependency graph. Extracting audit code into a stdlib-only `afaudit` package eliminates this coupling and enables lightweight consumers (e.g., `af`, `nightshift`) to depend directly on audit primitives without transitive bloat.

## Problem

The code that produces the three audit file types (`audit_*.jsonl`, `agent_*.jsonl`, `postmortem_*.json`) in `.agent-fox/audit/` is embedded inside the `agentfox` core package across multiple modules (`knowledge/audit.py`, `knowledge/sink.py`, `knowledge/agent_trace.py`, `workspace/audit_cleanup.py`, `engine/audit_helpers.py`, and part of `engine/run.py`). This makes the audit infrastructure non-reusable by other packages or projects without depending on the full `agentfox` library and all its heavy dependencies (duckdb, anthropic SDK, sentence-transformers, etc.).

## Goals

1. **Zero third-party dependencies:** `afaudit` has 0 third-party dependencies (versus 12+ for `agentfox`). This is verifiable by inspecting `pyproject.toml` and running `pip show afaudit`.
2. **Test isolation:** Audit-related unit tests run without `duckdb` or `anthropic` anywhere in the import graph, eliminating test isolation issues caused by heavy optional dependencies.
3. **Full audit file production via `afaudit` alone:** All three audit file types (`audit_*.jsonl`, `agent_*.jsonl`, `postmortem_*.json`) can be produced using only `afaudit` imports — no `agentfox` import required.
4. **Clean dependency boundary:** `agentfox` depends on `afaudit`; `afaudit` has no dependency on `agentfox`. This is verifiable via the workspace dependency graph.
5. **Downstream adoption:** `af` and `nightshift` depend on `afaudit` directly for call-sites that currently import audit symbols (`purge_stale_audit_files`, `AUDIT_DIR`), rather than transitively through `agentfox`.

## Scope

### In scope

- **Event model**: `AuditEvent` dataclass, `AuditEventType` StrEnum (49 values),
  `AuditSeverity` StrEnum, `AuditJsonlSink` class (lines 216–259 of
  `agentfox.knowledge.audit`), `default_severity_for()`, `generate_run_id()`,
  `event_to_json()`, `event_from_json()` — from `agentfox.knowledge.audit`.
  All of these move to `afaudit.events`; `AuditJsonlSink` is the existing JSONL
  file writer class and is re-exported from `afaudit.events` (not `afaudit.sink`).
- **Sink protocol and data models**: `SessionSink` Protocol,
  `SinkDispatcher` (including `_dispatch_optional` trace methods, which are
  private implementation details of `SinkDispatcher` and not part of the
  guaranteed public API), `SessionOutcome`, `ToolCall`, `ToolError`
  dataclasses — from `agentfox.knowledge.sink`.
- **Agent trace sink**: `AgentTraceSink`, `reconstruct_transcript()`,
  `truncate_tool_input()` — from `agentfox.knowledge.agent_trace`.
  `truncate_tool_input` is part of the guaranteed public API and is re-exported
  from `afaudit/__init__.py`; it is useful for any consumer that needs to write
  audit traces with large tool inputs.
- **Postmortem writer**: `build_postmortem()`, `write_postmortem()`,
  `should_dump()`, and internal helpers (`_build_task_summary`,
  `_build_blocked_tasks`, `_build_session_history`) — from
  `agentfox.engine.run` (lines 394-541). A `PostmortemInput` Protocol and a
  `SessionRecordLike` Protocol replace the hard dependency on `ExecutionState`
  and `SessionRecord`.
- **Emit helper**: `emit_audit_event()` — from
  `agentfox.engine.audit_helpers` (only this function, not
  `calculate_session_cost`).
- **Cleanup utilities**: `purge_stale_audit_files()`, `enforce_file_retention()` — from
  `agentfox.workspace.audit_cleanup`.
- **File-only retention**: The file-deletion half of
  `enforce_audit_retention()` (JSONL file glob + unlink), exposed as
  `enforce_file_retention(audit_dir: Path, *, max_runs: int = 20) -> int`.
  Split out of the current function which also does DuckDB row deletion.
- **Constant**: `AUDIT_DIR = Path(".agent-fox/audit")` — migrated from
  `agentfox.core.node_id` and removed from that module in the same change.
- **Import migration**: Update all ~60 production and ~30 test import sites
  across the workspace (`agentfox`, `af`, `nightshift`) in a single atomic
  change. `agentspec` and `spec` are confirmed (via grep) to have zero direct
  imports of any audit symbol and require no changes.

### Out of scope

- `DuckDBSink` — stays in `agentfox.knowledge.duckdb_sink` (heavy `duckdb`
  dependency; implements `afaudit.SessionSink` protocol).
- DuckDB migrations (`_migrate_v6` and the migration registry) — stays in
  `agentfox.knowledge.migrations`.
- `calculate_session_cost()` — stays in `agentfox.engine.audit_helpers`
  (depends on `agentfox.core.config.PricingConfig` and
  `agentfox.core.models.calculate_cost`).
- `ExecutionState`, `SessionRecord`, `RunStatus` — stays in
  `agentfox.engine.state` (core orchestrator state, consumed by postmortem via
  protocol).
- Markdown audit reports (`audit_{spec_name}.md` from `auditor_output.py` /
  `convergence.py`) — stays in agentfox (tied to review/convergence system).
- DB-retention half of `enforce_audit_retention()` — stays in agentfox
  (`duckdb_sink.py`).
- README, CHANGELOG, and generated documentation — not required at this stage;
  docstrings are sufficient for this internal workspace package.

## Tech Stack

- Python ≥ 3.12, stdlib only (no third-party dependencies).
- Hatchling build backend (matching all other workspace packages).
- Flat package layout: `packages/afaudit/afaudit/` (no `src/` directory).
- Initial version: `4.0.2` (matching current workspace version; all workspace packages share the same version).
- `pyproject.toml` structure: use `packages/afspec/pyproject.toml` as the template. The required constraints are: `build-system = hatchling`, `version = "4.0.2"`, `requires-python = ">=3.12"`, zero entries in `[project.dependencies]`. No `[project.optional-dependencies]` test group is added — test tooling (pytest, etc.) is declared at the workspace root in `[dependency-groups] dev` and is shared across all packages.
- Logging: stdlib `logging` with named loggers (`logging.getLogger('afaudit.<module>')`, e.g. `afaudit.events`, `afaudit.sink`, etc.), mirroring the existing logger-name pattern in the extracted code. No third-party loggers.

## Package Structure

```
packages/afaudit/
  pyproject.toml
  afaudit/
    __init__.py        # Re-exports key symbols (see Public API section)
    events.py          # AuditEvent, AuditEventType, AuditSeverity, AuditJsonlSink, helpers
    sink.py            # SessionSink protocol, SinkDispatcher, data classes
    trace.py           # AgentTraceSink, reconstruct_transcript, truncate_tool_input
    postmortem.py      # PostmortemInput Protocol, SessionRecordLike Protocol, build/write postmortem
    cleanup.py         # purge_stale_audit_files, enforce_file_retention
    emit.py            # emit_audit_event convenience function
    constants.py       # AUDIT_DIR
  tests/               # Tests migrated from agentfox/tests + new Protocol tests
```

## Public API (`afaudit/__init__.py`)

The following symbols are re-exported at the top level of the `afaudit` package. All other symbols are accessible via submodule import but are not part of the guaranteed public API.

```python
# from afaudit.events
from afaudit.events import (
    AuditEvent,
    AuditEventType,
    AuditSeverity,
    AuditJsonlSink,          # JSONL file writer; source: agentfox.knowledge.audit lines 216-259
    default_severity_for,
    generate_run_id,
    event_to_json,
    event_from_json,
)

# from afaudit.sink
from afaudit.sink import (
    SessionSink,
    SinkDispatcher,
    SessionOutcome,
    ToolCall,
    ToolError,
)

# from afaudit.trace
from afaudit.trace import (
    AgentTraceSink,
    reconstruct_transcript,
    truncate_tool_input,     # intentionally public; useful for consumers writing traces with large tool inputs
)

# from afaudit.postmortem
from afaudit.postmortem import (
    PostmortemInput,
    SessionRecordLike,
    build_postmortem,
    write_postmortem,
    should_dump,
)

# from afaudit.emit
from afaudit.emit import emit_audit_event

# from afaudit.cleanup
from afaudit.cleanup import (
    purge_stale_audit_files,
    enforce_file_retention,
)

# from afaudit.constants
from afaudit.constants import AUDIT_DIR
```

**Note on `SinkDispatcher._dispatch_optional`:** The `_dispatch_optional` trace methods are private implementation details of `SinkDispatcher`. The single-underscore prefix is intentional. `SinkDispatcher` itself is public and re-exported from `afaudit/__init__.py`; its `_dispatch_optional` methods are not part of the guaranteed public API and must not be referenced in external call-sites or tests.

## Protocol Definitions

### `PostmortemInput` Protocol

Defined in `afaudit.postmortem`. Replaces the hard dependency on `agentfox.engine.state.ExecutionState`. `ExecutionState` satisfies this protocol structurally with no changes required.

```python
class PostmortemInput(Protocol):
    run_id: str
    run_status: str          # may be a StrEnum; .value is used where a string is needed
    node_states: dict[str, str]
    total_cost: float
    total_input_tokens: int
    total_output_tokens: int
    total_sessions: int
    blocked_reasons: dict[str, str]
    session_history: list[SessionRecordLike]
    started_at: str
    updated_at: str
```

### `SessionRecordLike` Protocol

Defined in `afaudit.postmortem`. Replaces the hard dependency on `agentfox.engine.state.SessionRecord`. `SessionRecord` satisfies this protocol structurally with no changes required.

```python
class SessionRecordLike(Protocol):
    node_id: str
    attempt: int
    status: str
    archetype: str
    model: str
    duration_ms: int
    cost: float
    error_message: str | None
    timestamp: str
    is_transport_error: bool
    is_budget_exhausted: bool
    is_non_retryable: bool
```

## Cleanup Function Signatures

### `enforce_file_retention(audit_dir: Path, *, max_runs: int = 20) -> int`

Defined in `afaudit.cleanup`. This is the file-only half of the current `agentfox.workspace.audit_cleanup.enforce_audit_retention()`.

- Discovers `run_id`s from `audit_*.jsonl` filenames in `audit_dir`.
- Parses the embedded timestamp (`YYYYMMDD_HHMMSS_hex`) from each filename and sorts runs chronologically.
- Deletes the oldest runs beyond `max_runs`, removing all three corresponding files: `audit_*.jsonl`, `agent_*.jsonl`, and `postmortem_*.json`.
- Returns the total number of files **successfully** removed.
- Pure filesystem operation — no database interaction.

**Edge case behavior** (matching the best-effort pattern used throughout existing audit cleanup code):

| Condition | Behavior |
|---|---|
| `audit_dir` does not exist | Return `0` silently (matches `purge_stale_audit_files` pattern) |
| Filename does not match expected timestamp pattern | Skip the file; log a `WARNING` |
| File deletion fails (e.g., permissions error, race condition) | Log the error at `WARNING` level; continue; count only successfully deleted files in the return value |

The DB-retention half (DuckDB row deletion) remains in `agentfox` and is moved to `agentfox.knowledge.duckdb_sink`.

## Module Mapping

| New afaudit module | Source module | What moves |
|---|---|---|
| `events.py` | `agentfox.knowledge.audit` | Everything except `enforce_audit_retention`; includes `AuditJsonlSink` (lines 216–259) |
| `sink.py` | `agentfox.knowledge.sink` | Entire module |
| `trace.py` | `agentfox.knowledge.agent_trace` | Entire module |
| `postmortem.py` | `agentfox.engine.run` (lines 394-541) | Postmortem functions + new Protocols |
| `cleanup.py` | `agentfox.workspace.audit_cleanup` | Entire module + file-retention from `enforce_audit_retention` |
| `emit.py` | `agentfox.engine.audit_helpers` | `emit_audit_event` only |
| `constants.py` | `agentfox.core.node_id` | `AUDIT_DIR` (removed from source, not duplicated) |

## Dependency Graph (After)

```
afspec    afaudit
  ↑          ↑
  └────┬─────┘
       │
   agentfox
       ↑
   ┌───┼───┐
   af  ns  spec/agentspec
```

Both `afspec` and `afaudit` are leaf packages with zero internal dependencies.
`agentfox` depends on both. `af` and `nightshift` depend on `afaudit`
directly for call-sites that import `purge_stale_audit_files` and `AUDIT_DIR`,
rather than going through `agentfox` transitively. `agentspec` and `spec` have
zero direct audit imports (confirmed via grep) and continue to consume audit
functionality transitively via `agentfox` without any import changes.

## Downstream Consumer Adoption

| Consumer | Dependency mode | Notes |
|---|---|---|
| `agentfox` | Direct (declares `afaudit>=4.0.2` in `pyproject.toml`) | Core orchestrator; uses all afaudit symbols |
| `af` | Direct | Imports `purge_stale_audit_files`, `AUDIT_DIR` directly from `afaudit` |
| `nightshift` | Direct | Same as `af` — small number of call-sites updated atomically |
| `agentspec` / `spec` | Transitive via `agentfox` | Confirmed zero direct audit imports; no change required |

## Logging Strategy

`afaudit` uses stdlib `logging` exclusively. Each module creates a named logger following the `afaudit.<module>` pattern:

| Module | Logger name |
|---|---|
| `events.py` | `afaudit.events` |
| `sink.py` | `afaudit.sink` |
| `trace.py` | `afaudit.trace` |
| `postmortem.py` | `afaudit.postmortem` |
| `cleanup.py` | `afaudit.cleanup` |
| `emit.py` | `afaudit.emit` |

This mirrors the existing pattern in the extracted code (previously `agentfox.knowledge.audit`, etc.) — only the logger name prefix changes. No third-party loggers (loguru, structlog) are permitted.

## Test Migration Strategy

The ~30 audit-related test import sites are resolved as follows:

**Tests that move physically into `packages/afaudit/tests/`:**
- Any test file that exclusively exercises `afaudit` symbols — `AuditEvent`, `AuditJsonlSink`, `AgentTraceSink`, postmortem builders (`build_postmortem`, `write_postmortem`, `should_dump`), cleanup functions (`purge_stale_audit_files`, `enforce_file_retention`), and related helpers.
- New Protocol boundary tests verifying that `agentfox.engine.state.ExecutionState` satisfies `PostmortemInput` and that `SessionRecord` satisfies `SessionRecordLike` structurally. These are added to `packages/afaudit/tests/` as part of this change.
- New unit tests covering `enforce_file_retention` edge cases (missing `audit_dir`, unparseable filenames, failed file deletions). These are added to `packages/afaudit/tests/`.

**Tests that stay in their current package (`agentfox/tests/`, `af/tests/`, etc.) with import paths updated only:**
- Any test file that requires `agentfox` infrastructure to run — DuckDB fixtures, `config` objects, `PricingConfig`, full orchestrator setup (`ExecutionState` instantiation, `DuckDBSink`, etc.).
- Tests for `calculate_session_cost`, `DuckDBSink`, and the DB-retention half of audit retention remain in `agentfox/tests/`.

**Test isolation goal verification:** The implementer runs the `packages/afaudit/tests/` suite in an environment where `duckdb` and `anthropic` are not installed and documents the result in the PR description.

## Rollout and Migration Strategy

The migration is performed as a **single atomic change** (one feature branch, one PR). The workspace is a `uv` monorepo where all packages are developed together, and feature branches are local-only. With ~90 import sites being manageable in a single pass, and Design Decision #3 prohibiting re-export shims, no intermediate state is possible or desired.

The migration sequence within the PR is:
1. Create `packages/afaudit/` with all modules and tests.
2. Delete source modules from `agentfox` (no shims left behind).
3. Update `agentfox/pyproject.toml` to declare `afaudit>=4.0.2` as a direct dependency. **This is a prerequisite for the import changes in the next step to resolve correctly and must be done before updating import sites.**
4. Update all ~60 production import sites across `agentfox`, `af`, and `nightshift`. This includes replacing all internal `agentfox` module imports (e.g., `from agentfox.knowledge.audit import ...`) with `afaudit.*` equivalents — this is the most impactful step, touching the majority of production code across the workspace.
5. Update all ~30 test import sites, physically relocating test files that exclusively exercise `afaudit` symbols into `packages/afaudit/tests/` (see Test Migration Strategy section).
6. Remove `AUDIT_DIR` from `agentfox.core.node_id`.
7. Move the DB-retention half of `enforce_audit_retention()` into `agentfox.knowledge.duckdb_sink`.
8. Verify `make check` passes (lint + full test suite).

If `make check` fails, the feature branch is amended or rebased locally until it passes — no partial merge is possible since the branch is local-only. The atomic nature of the PR means rollback is a branch deletion with no production impact.

**Dependency isolation verification:** The zero-third-party-dependency acceptance criterion is verified manually as part of the PR review — the implementer documents the check in the PR description. Automated CI enforcement is not added at this stage; `pyproject.toml` having an empty `[project.dependencies]` table is self-documenting, and the `uv` workspace isolates package dependency graphs structurally.

## Design Decisions

1. **DB retention stays in agentfox.** The `enforce_audit_retention` function
   currently does both DuckDB row deletion and JSONL file deletion. The
   DuckDB half moves to `agentfox.knowledge.duckdb_sink` (as a new function
   or method) since it already owns the DuckDB connection. The file-deletion
   half moves to `afaudit.cleanup` as `enforce_file_retention`.

2. **Postmortem uses Protocols, not imports.** `build_postmortem` currently
   takes `ExecutionState` (an agentfox-internal dataclass). In afaudit, it
   accepts a `PostmortemInput` Protocol that defines only the 11 attributes
   actually read. A `SessionRecordLike` Protocol defines the 12 attributes
   accessed by `_build_session_history`. `agentfox.engine.state.ExecutionState`
   and `SessionRecord` already satisfy both protocols structurally — no
   changes needed on the agentfox side.

3. **No re-export shims in agentfox.** Original modules are deleted. All
   import sites are updated to use `afaudit.*` paths. Per project convention,
   no backward-compatibility re-exports. The `agentfox/__init__.py` is not
   updated to re-expose audit symbols — callers must import from `afaudit`
   directly.

4. **`calculate_session_cost` stays.** It depends on `PricingConfig` and
   `calculate_cost` from agentfox core — it's pricing logic that happens to
   live near audit code, not audit logic itself.

5. **`AUDIT_DIR` is moved, not duplicated.** The constant is removed from
   `agentfox.core.node_id` in the same atomic change that creates `afaudit`.
   Since all import sites are being updated anyway, there is no reason to
   maintain a duplicate. Both the definition and all usages migrate together.

6. **Scope excludes markdown audit reports.** The `audit_{spec_name}.md`
   files produced by `auditor_output.py` / `convergence.py` are part of the
   review/convergence system, not the audit file infrastructure. They stay
   in agentfox.

7. **`SessionRecordLike` Protocol.** The postmortem `_build_session_history`
   accesses 12 attributes on each session record. A Protocol makes this
   contract explicit and type-checkable, rather than relying on `list[Any]`
   duck typing.

8. **Versioning matches workspace.** `afaudit` is initialized at version
   `4.0.2`, matching the current workspace version. All workspace packages
   share the same version number; no independent semver policy applies.

9. **No documentation artifacts required.** As an internal workspace package,
   `afaudit` requires only docstrings. No `README.md`, `CHANGELOG.md`, or
   mkdocs configuration is needed at this stage, consistent with the
   conventions of other internal packages.

10. **`AuditJsonlSink` lives in `afaudit.events`, not `afaudit.sink`.** It is
    the JSONL file writer class from `agentfox.knowledge.audit` (lines 216–259)
    and is co-located with the event model it serializes. It is not a protocol
    implementor of `SessionSink` — it is a lower-level writer that operates
    directly on `AuditEvent` objects. The previous public API listing that
    placed it under `afaudit.sink` was incorrect and is resolved here.

11. **`truncate_tool_input` is intentionally public.** It is re-exported from
    `afaudit/__init__.py` because any consumer writing audit traces with large
    tool inputs needs access to it. It is not an internal helper.

12. **`enforce_file_retention` uses best-effort error handling.** Missing
    `audit_dir`, unparseable filenames, and deletion failures are all handled
    gracefully with logging rather than exceptions, consistent with the
    existing pattern in `purge_stale_audit_files`.

13. **`SinkDispatcher._dispatch_optional` methods are private.** The
    underscore prefix is intentional. `SinkDispatcher` is public; its
    `_dispatch_optional` trace methods are implementation details and are
    not part of the guaranteed public API. External call-sites and tests
    must not reference these methods directly.

14. **No optional test dependencies in `afaudit/pyproject.toml`.** Test
    tooling (pytest, etc.) is declared at the workspace root in
    `[dependency-groups] dev` and is shared across all packages. No
    `[project.optional-dependencies]` test group is added to `afaudit`'s
    `pyproject.toml`.

15. **`agentfox/pyproject.toml` update is a migration prerequisite.** Adding
    `afaudit>=4.0.2` to `agentfox`'s direct dependencies must be done before
    updating `agentfox` internal import sites (Step 3 in the rollout
    sequence), so that the workspace dependency resolver can locate the new
    package before any imports are changed.

## Acceptance Criteria

- `afaudit` is a workspace package at version `4.0.2` with zero third-party dependencies (verifiable via `pyproject.toml` and `pip show afaudit`).
- `pyproject.toml` follows the `afspec/pyproject.toml` template with `build-system = hatchling`, `requires-python = ">=3.12"`, and an empty `[project.dependencies]` table. No `[project.optional-dependencies]` section is present.
- All symbols listed in the Public API section are importable directly from `afaudit` (e.g., `from afaudit import AuditEvent, AuditJsonlSink, AUDIT_DIR`).
- `AuditJsonlSink` is defined in `afaudit.events` (not `afaudit.sink`) and is re-exported from `afaudit/__init__.py`.
- All three audit file types (`audit_*.jsonl`, `agent_*.jsonl`, `postmortem_*.json`) can be produced using only `afaudit` imports.
- `agentfox` depends on `afaudit>=4.0.2` (declared in `agentfox/pyproject.toml`) and no longer contains the moved code (no shim re-exports). `agentfox/__init__.py` does not re-expose audit symbols.
- All internal `agentfox` modules that previously imported from `agentfox.knowledge.audit`, `agentfox.knowledge.sink`, `agentfox.knowledge.agent_trace`, `agentfox.engine.audit_helpers`, `agentfox.workspace.audit_cleanup`, and `agentfox.core.node_id` (for `AUDIT_DIR`) now import from `afaudit.*` equivalents.
- `af` and `nightshift` depend on `afaudit` directly (declared in their respective `pyproject.toml` files) for their audit import sites.
- `DuckDBSink` in agentfox implements `afaudit.SessionSink`.
- `AUDIT_DIR` is defined only in `afaudit.constants` — the copy in `agentfox.core.node_id` is removed.
- `enforce_file_retention(audit_dir: Path, *, max_runs: int = 20) -> int` is implemented in `afaudit.cleanup` as a pure filesystem operation returning the count of successfully deleted files.
- `enforce_file_retention` edge cases: returns `0` silently if `audit_dir` does not exist; logs `WARNING` and skips files with unparseable timestamps; logs `WARNING` and continues if a file deletion fails, counting only successfully deleted files.
- `PostmortemInput` Protocol defines exactly 11 attributes as specified; `SessionRecordLike` Protocol defines exactly 12 attributes as specified.
- `agentfox.engine.state.ExecutionState` and `SessionRecord` satisfy `PostmortemInput` and `SessionRecordLike` respectively, verified by structural protocol tests in `packages/afaudit/tests/`.
- Each `afaudit` module uses stdlib `logging` with a `afaudit.<module>` logger name.
- Test files that exclusively exercise `afaudit` symbols are physically located in `packages/afaudit/tests/`. Test files requiring `agentfox` infrastructure remain in their original package test directories with import paths updated.
- The `packages/afaudit/tests/` suite passes in an environment where `duckdb` and `anthropic` are not installed. The implementer documents this verification in the PR description.
- All existing tests pass after the import migration.
- New unit tests are present for `PostmortemInput` and `SessionRecordLike` Protocol boundaries (verifying that `ExecutionState` and `SessionRecord` satisfy the protocols structurally).
- New unit tests cover `enforce_file_retention` edge cases: missing `audit_dir`, unparseable filenames, and failed file deletions.
- `SinkDispatcher._dispatch_optional` methods are not referenced in any test or external call-site (they are private implementation details).
- `agentspec` and `spec` require no import changes (confirmed zero direct audit imports).
- `make check` passes (lint + full test suite).

## Source

Source: Input provided by user via interactive prompt (refactoring discussion)
