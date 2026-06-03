# Test Specification: Remove Dead `--debug` Flag

## Overview

Tests verify that the `--debug` flag is fully removed from the CLI, internal
APIs, docstrings, and documentation. Existing DuckDB sink behavior (always-on
writes) must be preserved unchanged. Tests are organized by requirement area:
CLI removal, API cleanup, dry-run simplification, and docstring accuracy.

## Test Cases

### TS-131-1: `--debug` Not in Help Output

**Requirement:** 131-REQ-1.2
**Type:** unit
**Description:** The `code` command help output does not list `--debug`.

**Preconditions:**
- CLI app is importable.

**Input:**
- `agent-fox code --help`

**Expected:**
- Exit code 0, output does not contain the string `--debug`.

**Assertion pseudocode:**
```
result = cli_runner.invoke(main, ["code", "--help"])
ASSERT result.exit_code == 0
ASSERT "--debug" NOT IN result.output
```

### TS-131-2: `--debug` Rejected by Click

**Requirement:** 131-REQ-1.3
**Type:** unit
**Description:** Passing `--debug` to `code` produces a Click error.

**Preconditions:**
- CLI app is importable.

**Input:**
- `agent-fox code --debug`

**Expected:**
- Exit code 2 (Click usage error), output contains "no such option" or
  similar Click error text referencing `--debug`.

**Assertion pseudocode:**
```
result = cli_runner.invoke(main, ["code", "--debug"])
ASSERT result.exit_code == 2
ASSERT "--debug" IN result.output
```

### TS-131-3: `run_code` Rejects `debug` Keyword

**Requirement:** 131-REQ-2.1
**Type:** unit
**Description:** Calling `run_code(config, debug=True)` raises TypeError.

**Preconditions:**
- `run_code` is importable.

**Input:**
- `run_code(config, debug=True)` (with minimal valid config).

**Expected:**
- `TypeError` raised (unexpected keyword argument).

**Assertion pseudocode:**
```
ASSERT_RAISES TypeError:
    run_code(config, debug=True)
```

### TS-131-4: `_setup_infrastructure` Rejects `debug` Keyword

**Requirement:** 131-REQ-2.2
**Type:** unit
**Description:** Calling `_setup_infrastructure(config, debug=True)` raises TypeError.

**Preconditions:**
- `_setup_infrastructure` is importable.

**Input:**
- `_setup_infrastructure(config, debug=True)`.

**Expected:**
- `TypeError` raised.

**Assertion pseudocode:**
```
ASSERT_RAISES TypeError:
    _setup_infrastructure(config, debug=True)
```

### TS-131-5: `DuckDBSink` Rejects `debug` Keyword

**Requirement:** 131-REQ-2.3
**Type:** unit
**Description:** Calling `DuckDBSink(conn, debug=True)` raises TypeError.

**Preconditions:**
- `DuckDBSink` is importable, valid DuckDB connection available.

**Input:**
- `DuckDBSink(conn, debug=True)`.

**Expected:**
- `TypeError` raised.

**Assertion pseudocode:**
```
conn = duckdb.connect(":memory:")
ASSERT_RAISES TypeError:
    DuckDBSink(conn, debug=True)
```

### TS-131-6: `DuckDBSink` Has No `_debug` Attribute

**Requirement:** 131-REQ-2.3
**Type:** unit
**Description:** `DuckDBSink` instances do not have a `_debug` attribute.

**Preconditions:**
- Valid DuckDB connection with schema.

**Input:**
- `DuckDBSink(conn)`.

**Expected:**
- `hasattr(sink, "_debug")` is False.

**Assertion pseudocode:**
```
sink = DuckDBSink(conn)
ASSERT NOT hasattr(sink, "_debug")
```

### TS-131-7: `_check_dry_run_conflicts` Has No `debug` Parameter

**Requirement:** 131-REQ-3.1
**Type:** unit
**Description:** The function signature does not accept `debug`.

**Preconditions:**
- `_check_dry_run_conflicts` is importable.

**Input:**
- `inspect.signature(_check_dry_run_conflicts)`.

**Expected:**
- `"debug"` not in parameter names.

**Assertion pseudocode:**
```
sig = inspect.signature(_check_dry_run_conflicts)
ASSERT "debug" NOT IN sig.parameters
```

### TS-131-8: Dry-run + `--watch` Still Rejected

**Requirement:** 131-REQ-3.2
**Type:** unit
**Description:** `--dry-run --watch` still produces an error listing `--watch`.

**Preconditions:**
- CLI app importable.

**Input:**
- `agent-fox code --dry-run --watch`.

**Expected:**
- Exit code 1, output contains `--watch`.

**Assertion pseudocode:**
```
result = cli_runner.invoke(main, ["code", "--dry-run", "--watch"])
ASSERT result.exit_code == 1
ASSERT "--watch" IN result.output
```

### TS-131-9: Dry-run + `--force-clean` Still Rejected

**Requirement:** 131-REQ-3.3
**Type:** unit
**Description:** `--dry-run --force-clean` still produces an error listing `--force-clean`.

**Preconditions:**
- CLI app importable.

**Input:**
- `agent-fox code --dry-run --force-clean`.

**Expected:**
- Exit code 1, output contains `--force-clean`.

**Assertion pseudocode:**
```
result = cli_runner.invoke(main, ["code", "--dry-run", "--force-clean"])
ASSERT result.exit_code == 1
ASSERT "--force-clean" IN result.output
```

## Property Test Cases

### TS-131-P1: DuckDB Writes Unchanged After Removal

**Property:** Property 1 from design.md
**Validates:** 131-REQ-2.E1
**Type:** property
**Description:** DuckDBSink constructed without `debug` writes tool signals
identically to the previous always-on behavior.

**For any:** N tool calls (1 <= N <= 10) and M tool errors (1 <= M <= 10).
**Invariant:** `tool_calls` table has exactly N rows and `tool_errors` table
has exactly M rows.

**Assertion pseudocode:**
```
FOR ANY n IN integers(1, 10), m IN integers(1, 10):
    conn = duckdb.connect(":memory:")
    create_schema(conn)
    sink = DuckDBSink(conn)
    FOR _ IN range(n): sink.record_tool_call(ToolCall(tool_name="test"))
    FOR _ IN range(m): sink.record_tool_error(ToolError(tool_name="test"))
    ASSERT count(tool_calls) == n
    ASSERT count(tool_errors) == m
```

### TS-131-P2: CLI Flag Removal Complete

**Property:** Property 2 from design.md
**Validates:** 131-REQ-1.1, 131-REQ-1.2, 131-REQ-1.3, 131-REQ-2.1, 131-REQ-2.2, 131-REQ-2.3
**Type:** property
**Description:** No invocation of `agent-fox code` accepts `--debug`, and no
downstream function accepts a `debug` parameter.

**For any:** function in {`run_code`, `_setup_infrastructure`, `DuckDBSink.__init__`}.
**Invariant:** `"debug"` is not in the function's parameter names, and
`agent-fox code --help` does not contain `--debug`.

**Assertion pseudocode:**
```
FOR EACH fn IN [run_code, _setup_infrastructure, DuckDBSink.__init__]:
    sig = inspect.signature(fn)
    ASSERT "debug" NOT IN sig.parameters

result = cli_runner.invoke(main, ["code", "--help"])
ASSERT "--debug" NOT IN result.output
```

### TS-131-P3: Dry-Run Conflict Accuracy

**Property:** Property 3 from design.md
**Validates:** 131-REQ-3.1, 131-REQ-3.2, 131-REQ-3.3, 131-REQ-1.E1
**Type:** property
**Description:** `_check_dry_run_conflicts` never returns `--debug` for any
combination of its inputs.

**For any:** booleans `watch` and `force_clean`.
**Invariant:** The return value of `_check_dry_run_conflicts(dry_run=True, watch, force_clean)` never contains `"--debug"`.

**Assertion pseudocode:**
```
FOR ANY watch IN [True, False], force_clean IN [True, False]:
    result = _check_dry_run_conflicts(dry_run=True, watch=watch, force_clean=force_clean)
    ASSERT "--debug" NOT IN result
```

## Edge Case Tests

### TS-131-E1: Dry-run Without `--debug` Does Not Mention Debug

**Requirement:** 131-REQ-1.E1
**Type:** unit
**Description:** `--dry-run` alone does not produce any debug-related output.

**Preconditions:**
- CLI app importable, plan exists in DuckDB.

**Input:**
- `agent-fox code --dry-run` (with mocked plan).

**Expected:**
- Exit code 0, output does not contain `--debug`.

**Assertion pseudocode:**
```
result = cli_runner.invoke(main, ["code", "--dry-run"])
ASSERT "--debug" NOT IN result.output
```

### TS-131-E2: DuckDBSink Without `debug` Records Outcomes

**Requirement:** 131-REQ-2.E1
**Type:** unit
**Description:** Session outcomes are written when DuckDBSink is constructed
without `debug`.

**Preconditions:**
- Valid DuckDB connection with schema.

**Input:**
- `DuckDBSink(conn).record_session_outcome(outcome)`.

**Expected:**
- One row in `session_outcomes` table.

**Assertion pseudocode:**
```
sink = DuckDBSink(conn)
sink.record_session_outcome(SessionOutcome(status="completed"))
ASSERT count(session_outcomes) == 1
```

## Integration Smoke Tests

### TS-131-SMOKE-1: `code` Command End-to-End Without `--debug`

**Execution Path:** Path 1 from design.md
**Description:** The `code` command can be invoked (with mocked orchestrator)
without any `debug` parameter flowing through the call chain.

**Setup:** Mock `run_code` to return a completed `ExecutionState`. Mock
`DEFAULT_DB_PATH.exists()` to return True.

**Trigger:** `cli_runner.invoke(main, ["code"])`

**Expected side effects:**
- `run_code` called once with no `debug` keyword argument in `call_args.kwargs`.
- Exit code 0.

**Must NOT satisfy with:** Mocking `code_cmd` itself (the real Click command
must be invoked).

**Assertion pseudocode:**
```
mock_rc = mock_run_code(completed_state)
result = cli_runner.invoke(main, ["code"])
ASSERT "debug" NOT IN mock_rc.call_args.kwargs
ASSERT result.exit_code == 0
```

### TS-131-SMOKE-2: Dry-Run Conflict Check Without `--debug`

**Execution Path:** Path 2 from design.md
**Description:** Dry-run conflict detection works correctly with only
`--watch` and `--force-clean` as possible conflicts.

**Setup:** No mocks needed for conflict check (pure function).

**Trigger:** Call `_check_dry_run_conflicts(dry_run=True, watch=True, force_clean=True)`.

**Expected side effects:**
- Returns `["--watch", "--force-clean"]` (no `--debug`).

**Must NOT satisfy with:** Mocking `_check_dry_run_conflicts`.

**Assertion pseudocode:**
```
result = _check_dry_run_conflicts(dry_run=True, watch=True, force_clean=True)
ASSERT result == ["--watch", "--force-clean"]
ASSERT "--debug" NOT IN result
```

## Coverage Matrix

| Requirement | Test Spec Entry | Type |
|-------------|-----------------|------|
| 131-REQ-1.1 | TS-131-2 | unit |
| 131-REQ-1.2 | TS-131-1 | unit |
| 131-REQ-1.3 | TS-131-2 | unit |
| 131-REQ-1.E1 | TS-131-E1 | unit |
| 131-REQ-2.1 | TS-131-3 | unit |
| 131-REQ-2.2 | TS-131-4 | unit |
| 131-REQ-2.3 | TS-131-5, TS-131-6 | unit |
| 131-REQ-2.E1 | TS-131-E2, TS-131-P1 | unit, property |
| 131-REQ-3.1 | TS-131-7, TS-131-SMOKE-2 | unit, integration |
| 131-REQ-3.2 | TS-131-8 | unit |
| 131-REQ-3.3 | TS-131-9 | unit |
| 131-REQ-4.1 | (verified by code review) | — |
| 131-REQ-4.2 | (verified by code review) | — |
| 131-REQ-4.3 | (verified by code review) | — |
| 131-REQ-4.4 | (verified by code review) | — |
| 131-REQ-4.5 | (verified by code review) | — |
| 131-REQ-4.6 | (verified by code review) | — |
| Property 1 | TS-131-P1 | property |
| Property 2 | TS-131-P2 | property |
| Property 3 | TS-131-P3 | property |
