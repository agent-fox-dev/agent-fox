# Design Document: Remove Dead `--debug` Flag

## Overview

Remove the `--debug` flag from the `code` command and all internal plumbing
(`code_cmd` → `run_code` → `_setup_infrastructure` → `DuckDBSink`). Update
stale docstrings and documentation. No behavioral change — audit/telemetry
remains always-on.

## Architecture

No architectural change. The call chain from CLI to DuckDBSink is simplified
by removing one parameter at each layer.

```mermaid
flowchart TD
    CLI["cli/code.py: code_cmd"] --> RC["engine/run.py: run_code()"]
    RC --> SI["engine/run.py: _setup_infrastructure()"]
    SI --> DS["knowledge/duckdb_sink.py: DuckDBSink()"]
    CLI --> CDC["cli/code.py: _check_dry_run_conflicts()"]
```

All arrows lose their `debug=` parameter. No new modules, no new interfaces.

### Module Responsibilities

1. **cli/code.py** — CLI entry point for `code` command; flag definitions and
   dry-run conflict checking.
2. **engine/run.py** — Orchestrator setup; wires sinks, knowledge store, and
   session runner factory.
3. **knowledge/duckdb_sink.py** — DuckDB-backed session sink; records outcomes
   and tool signals.
4. **knowledge/sink.py** — Protocol definition for session sinks.
5. **docs/cli-reference.md** — User-facing CLI documentation.

## Execution Paths

### Path 1: `code` command invocation (post-removal)

1. `cli/code.py: code_cmd` — Click parses args (no `--debug` option)
2. `cli/code.py: _check_dry_run_conflicts(dry_run, watch, force_clean)` — checks only `--watch`, `--force-clean`
3. `engine/run.py: run_code(config, watch=..., ...)` — no `debug` param
4. `engine/run.py: _setup_infrastructure(config, ...)` — no `debug` param
5. `knowledge/duckdb_sink.py: DuckDBSink(conn)` — no `debug` param; always-on writes

### Path 2: `code --dry-run` conflict detection (post-removal)

1. `cli/code.py: code_cmd` — Click parses `--dry-run`
2. `cli/code.py: _check_dry_run_conflicts(dry_run=True, watch, force_clean)` → `list[str]`
3. If conflicts non-empty → exit code 1 with flag list (never includes `--debug`)

## Components and Interfaces

### Modified Signatures

```python
# cli/code.py — remove --debug decorator and parameter
def code_cmd(ctx, specs_dir, watch, watch_interval, force_clean, dry_run): ...

# cli/code.py — remove debug parameter
def _check_dry_run_conflicts(dry_run: bool, watch: bool, force_clean: bool) -> list[str]: ...

# engine/run.py — remove debug parameter
def _setup_infrastructure(config, *, activity_callback=None) -> dict[str, Any]: ...

# engine/run.py — remove debug parameter
async def run_code(config, *, max_cost=None, max_sessions=None,
                   watch=False, watch_interval=None, specs_dir=None,
                   activity_callback=None, task_callback=None) -> ...: ...

# knowledge/duckdb_sink.py — remove debug parameter and _debug field
class DuckDBSink:
    def __init__(self, conn: duckdb.DuckDBPyConnection) -> None: ...
```

## Data Models

No data model changes. DuckDB schema is unchanged. Session outcomes, tool
calls, and tool errors continue to be written unconditionally.

## Operational Readiness

- **Rollout:** Pure removal — no new behavior to roll out or roll back.
- **Observability:** No change — audit/telemetry remains always-on.
- **Migration:** No migration needed. Callers passing `debug=` as a keyword
  argument will get a `TypeError` at call time, caught by tests.

## Correctness Properties

### Property 1: DuckDB Writes Unchanged

*For any* session outcome, tool call, or tool error, `DuckDBSink` SHALL
record the event identically to pre-removal behavior.

**Validates: Requirements 2.E1**

### Property 2: CLI Flag Removal Complete

*For any* invocation of `agent-fox code`, the command SHALL NOT accept
`--debug` and SHALL NOT pass a `debug` parameter to any downstream function.

**Validates: Requirements 1.1, 1.2, 1.3, 2.1, 2.2, 2.3**

### Property 3: Dry-Run Conflict Accuracy

*For any* combination of `--dry-run` with `--watch` and/or `--force-clean`,
`_check_dry_run_conflicts` SHALL return only the flags that are actually
present, and SHALL never include `--debug`.

**Validates: Requirements 3.1, 3.2, 3.3, 1.E1**

## Error Handling

| Error Condition | Behavior | Requirement |
|----------------|----------|-------------|
| User passes `--debug` to `code` | Click rejects with "no such option" | 131-REQ-1.3 |
| Code passes `debug=` to `DuckDBSink()` | Python raises `TypeError` | 131-REQ-2.3 |

## Technology Stack

- Python 3.12+
- Click (CLI framework)
- DuckDB (knowledge store)
- pytest + Hypothesis (testing)

## Definition of Done

A task group is complete when ALL of the following are true:

1. All subtasks within the group are checked off (`[x]`)
2. All spec tests (`test_spec.md` entries) for the task group pass
3. All property tests for the task group pass
4. All previously passing tests still pass (no regressions)
5. No linter warnings or errors introduced
6. Code is committed on a feature branch and merged into `develop`
7. Feature branch is merged back to `develop`
8. `tasks.md` checkboxes are updated to reflect completion

## Testing Strategy

- **Unit tests:** Verify CLI rejects `--debug`, verify `run_code` and
  `_setup_infrastructure` signatures lack `debug`, verify `DuckDBSink`
  constructor lacks `debug`.
- **Property tests:** Verify DuckDB writes are unchanged (existing property
  tests adapted to remove `debug=` parameter).
- **Integration tests:** Verify smoke tests pass with updated
  docstrings/comments (no functional change to smoke tests).
- **Existing tests:** All non-debug-related tests must continue to pass
  unchanged, proving no behavioral regression.
