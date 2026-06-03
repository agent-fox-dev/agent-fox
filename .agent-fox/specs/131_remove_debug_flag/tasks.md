# Implementation Plan: Remove Dead `--debug` Flag

<!-- AGENT INSTRUCTIONS
- Implement exactly ONE top-level task group per session
- Task group 1 writes failing tests from test_spec.md — all subsequent groups
  implement code to make those tests pass
- Follow the git-flow: feature branch from develop -> implement -> test -> merge to develop
- Update checkbox states as you go: [-] in progress, [x] complete
-->

## Overview

This is a removal/cleanup spec. Group 1 writes tests that assert the
`--debug` flag and `debug` parameter are absent. Group 2 removes the flag
and parameter from source code, updates docstrings/docs, and updates existing
tests. Group 3 verifies wiring.

The ordering is: tests first (group 1), then implementation + existing test
updates (group 2), then wiring verification (group 3).

## Test Commands

- Spec tests: `uv run pytest -q tests/unit/cli/test_remove_debug.py tests/unit/knowledge/test_remove_debug_sink.py tests/property/knowledge/test_remove_debug_props.py`
- Unit tests: `uv run pytest -q tests/unit/`
- Property tests: `uv run pytest -q tests/property/`
- All tests: `uv run pytest -q`
- Linter: `make lint`

## Tasks

- [x] 1. Write failing spec tests
  - [x] 1.1 Create `tests/unit/cli/test_remove_debug.py`
    - Test `--debug` not in help output (TS-131-1)
    - Test `--debug` rejected by Click (TS-131-2)
    - Test `run_code` rejects `debug` keyword (TS-131-3)
    - Test `_setup_infrastructure` rejects `debug` keyword (TS-131-4)
    - Test `_check_dry_run_conflicts` has no `debug` parameter (TS-131-7)
    - Test dry-run + `--watch` still rejected (TS-131-8)
    - Test dry-run + `--force-clean` still rejected (TS-131-9)
    - Test dry-run alone does not mention debug (TS-131-E1)
    - Smoke test: `code` invocation without debug in kwargs (TS-131-SMOKE-1)
    - Smoke test: dry-run conflict check without debug (TS-131-SMOKE-2)
    - _Test Spec: TS-131-1, TS-131-2, TS-131-3, TS-131-4, TS-131-7, TS-131-8, TS-131-9, TS-131-E1, TS-131-SMOKE-1, TS-131-SMOKE-2_

  - [x] 1.2 Create `tests/unit/knowledge/test_remove_debug_sink.py`
    - Test `DuckDBSink` rejects `debug` keyword (TS-131-5)
    - Test `DuckDBSink` has no `_debug` attribute (TS-131-6)
    - Test `DuckDBSink` without `debug` records outcomes (TS-131-E2)
    - _Test Spec: TS-131-5, TS-131-6, TS-131-E2_

  - [x] 1.3 Create `tests/property/knowledge/test_remove_debug_props.py`
    - Property test: DuckDB writes unchanged after removal (TS-131-P1)
    - _Test Spec: TS-131-P1_

  - [x] 1.V Verify task group 1
    - [x] All spec tests exist and are syntactically valid
    - [x] All spec tests FAIL (red) — no implementation yet
    - [x] No linter warnings introduced: `make lint`

- [x] 2. Remove `--debug` flag and update all references
  - [x] 2.1 Remove `--debug` from CLI and dry-run conflict check
    - Remove `@click.option("--debug", ...)` decorator from `code_cmd` in `agent_fox/cli/code.py`
    - Remove `debug` parameter from `code_cmd` function signature
    - Remove `debug=debug` from `_check_dry_run_conflicts(...)` call
    - Remove `debug=debug` from `run_code(...)` call
    - Remove `debug` parameter from `_check_dry_run_conflicts` function signature and body
    - _Requirements: 131-REQ-1.1, 131-REQ-1.2, 131-REQ-1.3, 131-REQ-1.E1, 131-REQ-3.1_

  - [x] 2.2 Remove `debug` from engine and sink
    - Remove `debug` parameter from `run_code()` signature and docstring in `engine/run.py`
    - Remove `debug` parameter from `_setup_infrastructure()` signature in `engine/run.py`
    - Remove `debug=debug` from `DuckDBSink(...)` call in `_setup_infrastructure()`
    - Remove `debug` parameter from `DuckDBSink.__init__()` and `self._debug` in `knowledge/duckdb_sink.py`
    - _Requirements: 131-REQ-2.1, 131-REQ-2.2, 131-REQ-2.3_

  - [x] 2.3 Update stale docstrings
    - Update `DuckDBSink` class docstring to remove debug references (131-REQ-4.1)
    - Update `duckdb_sink.py` module docstring: "debug-only" → "always-on" (131-REQ-4.2)
    - Update `SessionSink.record_tool_call()` docstring to remove "non-debug mode" (131-REQ-4.3)
    - Update `SessionSink.record_tool_error()` docstring to remove "non-debug mode" (131-REQ-4.4)
    - _Requirements: 131-REQ-4.1, 131-REQ-4.2, 131-REQ-4.3, 131-REQ-4.4_

  - [x] 2.4 Update documentation
    - Remove `--debug` row from `docs/cli-reference.md` options table (131-REQ-4.5)
    - Remove `--debug` from dry-run mutual exclusion paragraph in `docs/cli-reference.md` (131-REQ-4.6)
    - _Requirements: 131-REQ-4.5, 131-REQ-4.6_

  - [x] 2.5 Update existing tests
    - Remove `TestDebugFlag` class from `tests/unit/cli/test_code.py`
    - Remove `TestMutualExclusionDebug` class from `tests/unit/cli/test_code_dry_run.py`
    - Update `TestMultipleIncompatibleFlags` in `tests/unit/cli/test_code_dry_run.py`:
      remove `--debug` from test invocations, update assertions to not expect `--debug`
    - Update parametrized `test_all_flag_combos_rejected` in `tests/unit/cli/test_code_dry_run.py`:
      change combinations source from `["--watch", "--debug", "--force-clean"]` to
      `["--watch", "--force-clean"]`
    - Remove `debug=False` / `debug=True` from all `DuckDBSink(...)` calls in
      `tests/unit/knowledge/test_duckdb_sink.py`
    - Collapse `test_records_outcome_with_debug_false` and
      `test_records_outcome_with_debug_true` into one test
    - Collapse `test_tool_calls_written_when_debug_false` and
      `test_tool_calls_written_when_debug_true` into one test
    - Remove `debug=False` / `debug=True` from all `DuckDBSink(...)` calls in
      `tests/property/knowledge/test_sink_props.py`
    - Collapse `test_tool_signals_written_when_debug_false` and
      `test_tool_signals_written_when_debug_true` into one property test
    - Update class docstring for `TestToolTelemetryAlwaysOnInvariant` to remove
      "regardless of the debug flag value"
    - Update `tests/integration/test_agent_trace_smoke.py` docstrings/comments:
      remove `debug=True` references from module docstring, class docstrings,
      and test method docstrings

  - [x] 2.V Verify task group 2
    - [x] Spec tests for this group pass: `uv run pytest -q tests/unit/cli/test_remove_debug.py tests/unit/knowledge/test_remove_debug_sink.py tests/property/knowledge/test_remove_debug_props.py`
    - [x] All existing tests still pass: `uv run pytest -q`
    - [x] No linter warnings introduced: `make lint`
    - [x] Requirements 131-REQ-1.1 through 131-REQ-4.6 acceptance criteria met

- [ ] 3. Wiring verification

  - [ ] 3.1 Trace every execution path from design.md end-to-end
    - For Path 1: verify `code_cmd` → `run_code` → `_setup_infrastructure` → `DuckDBSink`
      call chain has no `debug` parameter at any level
    - For Path 2: verify `_check_dry_run_conflicts` signature and body contain
      no `debug` reference
    - Every path must be live in production code
    - _Requirements: all_

  - [ ] 3.2 Verify no residual `debug` references in touched files
    - Grep all files modified by this spec for: `debug` as a parameter name,
      `self._debug`, `debug=`, `"--debug"` (in non-test, non-archived files)
    - Each hit must be either: (a) a `logger.debug()` call (acceptable), or
      (b) flagged for removal
    - _Requirements: all_

  - [ ] 3.3 Run the integration smoke tests
    - All `TS-131-SMOKE-*` tests pass
    - _Test Spec: TS-131-SMOKE-1, TS-131-SMOKE-2_

  - [ ] 3.4 Stub / dead-code audit
    - Search all files touched by this spec for: `return []`, `return None`
      on non-Optional returns, `pass` in non-abstract methods, `# TODO`,
      `# stub`, `NotImplementedError`
    - Each hit must be justified or replaced
    - Document any intentional stubs here with rationale

  - [ ] 3.5 Cross-spec entry point verification
    - Verify `DuckDBSink()` is still constructed in `_setup_infrastructure`
      (now without `debug=`) and that the sink is added to the dispatcher
    - Verify `run_code()` is still called from `code_cmd` (now without `debug=`)
    - _Requirements: all_

  - [ ] 3.V Verify wiring group
    - [ ] All smoke tests pass
    - [ ] No unjustified stubs remain in touched files
    - [ ] All execution paths from design.md are live (traceable in code)
    - [ ] All cross-spec entry points are called from production code
    - [ ] All existing tests still pass: `uv run pytest -q`

## Notes

- This is a pure removal spec — no new behavior is introduced.
- `logger.debug()` calls throughout the codebase are unrelated to the
  `--debug` flag and must not be touched.
- Archived specs (11, 103, 123) that reference `--debug` are left as-is;
  they are historical records.
- The `DuckDBSink` schema (tables, columns) is unchanged. Only the Python
  constructor signature changes.

## Traceability

| Requirement | Test Spec Entry | Implemented By Task | Verified By Test |
|-------------|-----------------|---------------------|------------------|
| 131-REQ-1.1 | TS-131-2 | 2.1 | `test_remove_debug.py::test_debug_rejected_by_click` |
| 131-REQ-1.2 | TS-131-1 | 2.1 | `test_remove_debug.py::test_debug_not_in_help` |
| 131-REQ-1.3 | TS-131-2 | 2.1 | `test_remove_debug.py::test_debug_rejected_by_click` |
| 131-REQ-1.E1 | TS-131-E1 | 2.1 | `test_remove_debug.py::test_dry_run_no_debug_mention` |
| 131-REQ-2.1 | TS-131-3 | 2.2 | `test_remove_debug.py::test_run_code_rejects_debug` |
| 131-REQ-2.2 | TS-131-4 | 2.2 | `test_remove_debug.py::test_setup_infra_rejects_debug` |
| 131-REQ-2.3 | TS-131-5, TS-131-6 | 2.2 | `test_remove_debug_sink.py::test_duckdb_rejects_debug`, `test_remove_debug_sink.py::test_no_debug_attr` |
| 131-REQ-2.E1 | TS-131-E2, TS-131-P1 | 2.2 | `test_remove_debug_sink.py::test_records_without_debug`, `test_remove_debug_props.py` |
| 131-REQ-3.1 | TS-131-7, TS-131-SMOKE-2 | 2.1 | `test_remove_debug.py::test_conflict_fn_no_debug_param`, `test_remove_debug.py::test_smoke_conflict_check` |
| 131-REQ-3.2 | TS-131-8 | 2.1 | `test_remove_debug.py::test_dry_run_watch_rejected` |
| 131-REQ-3.3 | TS-131-9 | 2.1 | `test_remove_debug.py::test_dry_run_force_clean_rejected` |
| 131-REQ-4.1 | — | 2.3 | (code review) |
| 131-REQ-4.2 | — | 2.3 | (code review) |
| 131-REQ-4.3 | — | 2.3 | (code review) |
| 131-REQ-4.4 | — | 2.3 | (code review) |
| 131-REQ-4.5 | — | 2.4 | (code review) |
| 131-REQ-4.6 | — | 2.4 | (code review) |
| Property 1 | TS-131-P1 | 2.2 | `test_remove_debug_props.py` |
| Property 2 | TS-131-P2 | 2.1, 2.2 | `test_remove_debug.py::test_property_flag_removal_complete` |
| Property 3 | TS-131-P3 | 2.1 | `test_remove_debug.py::test_property_conflict_no_debug` |
