# Implementation Plan: Abort Post-Mortem Dump

<!-- AGENT INSTRUCTIONS
- Implement exactly ONE top-level task group per session
- Task group 1 writes failing tests from test_spec.md — all subsequent groups
  implement code to make those tests pass
- Follow the git-flow: feature branch from develop -> implement -> test -> merge to develop
- Update checkbox states as you go: [-] in progress, [x] complete
-->

## Overview

The implementation adds a post-mortem JSON dump feature in four groups:
(1) failing tests, (2) the postmortem module and state changes,
(3) wiring into run.py and cli/code.py, (4) wiring verification.

The feature is small and self-contained: one new module, two dataclass
field additions, and two wiring points. No database migrations or config
changes are needed.

## Test Commands

- Spec tests: `uv run pytest -q tests/unit/engine/test_postmortem.py tests/unit/cli/test_code.py -k postmortem`
- Unit tests: `uv run pytest -q tests/unit/engine/test_postmortem.py`
- Property tests: `uv run pytest -q tests/property/engine/test_postmortem_props.py`
- All tests: `uv run pytest -q`
- Linter: `uv run ruff check agent_fox/ tests/`

## Tasks

- [x] 1. Write failing spec tests
  - [x] 1.1 Create test file for postmortem module
    - Create `tests/unit/engine/test_postmortem.py`
    - Implement TS-126-1 through TS-126-9, TS-126-12 as unit tests
    - Use local imports within test methods for not-yet-existing module
    - Tests MUST fail (module does not exist yet)
    - _Test Spec: TS-126-1 through TS-126-9, TS-126-12_

  - [x] 1.2 Create property test file
    - Create `tests/property/engine/test_postmortem_props.py`
    - Implement TS-126-P1 through TS-126-P8 using Hypothesis
    - Build ExecutionState strategy generating random node_states,
      session_history, blocked_reasons, cost/token values
    - Tests MUST fail (module does not exist yet)
    - _Test Spec: TS-126-P1 through TS-126-P8_

  - [x] 1.3 Add CLI post-mortem path tests
    - Add TS-126-10 and TS-126-11 to existing `tests/unit/cli/test_code.py`
    - Tests verify `_print_summary()` output includes/excludes
      post-mortem path based on `postmortem_path` field
    - Tests MUST fail (field does not exist yet)
    - _Test Spec: TS-126-10, TS-126-11_

  - [x] 1.4 Create edge case tests
    - Add TS-126-E1 through TS-126-E5 to `tests/unit/engine/test_postmortem.py`
    - _Test Spec: TS-126-E1 through TS-126-E5_

  - [x] 1.V Verify task group 1
    - [x] All spec tests exist and are syntactically valid
    - [x] All spec tests FAIL (red) — no implementation yet
    - [x] No linter warnings introduced: `uv run ruff check tests/unit/engine/test_postmortem.py tests/property/engine/test_postmortem_props.py`

- [x] 2. Implement postmortem module and state changes
  - [x] 2.1 Add `run_id` and `postmortem_path` fields to ExecutionState
    - Add `run_id: str = ""` and `postmortem_path: str = ""` to
      `ExecutionState` in `agent_fox/engine/state.py`
    - _Requirements: 126-REQ-7.1_

  - [x] 2.2 Set `state.run_id` in Orchestrator._init_run()
    - After `self._run_id = generate_run_id()`, add
      `self.state.run_id = self._run_id`
    - In `agent_fox/engine/engine.py`
    - _Requirements: 126-REQ-7.2_

  - [x] 2.3 Create `agent_fox/engine/postmortem.py`
    - Implement `TRIGGER_STATUSES`, `SCHEMA_VERSION`
    - Implement `should_dump(state) -> bool`
    - Implement `build_postmortem(state) -> dict` with fallback run_id
      for empty state.run_id
    - Implement `write_postmortem(postmortem, audit_dir) -> Path`
    - All blocked tasks derived from node_states (not just blocked_reasons)
    - Missing reasons default to "unknown"
    - _Requirements: 126-REQ-1.1, 126-REQ-1.2, 126-REQ-1.3, 126-REQ-1.E2,
      126-REQ-2.1, 126-REQ-2.2, 126-REQ-2.3, 126-REQ-3.1 through 126-REQ-3.6,
      126-REQ-4.1, 126-REQ-4.2, 126-REQ-4.E1, 126-REQ-5.1, 126-REQ-5.2,
      126-REQ-5.E1_

  - [x] 2.V Verify task group 2
    - [x] Spec tests for this group pass: `uv run pytest -q tests/unit/engine/test_postmortem.py tests/property/engine/test_postmortem_props.py`
    - [x] All existing tests still pass: `uv run pytest -q`
    - [x] No linter warnings introduced: `uv run ruff check agent_fox/engine/postmortem.py agent_fox/engine/state.py`
    - [x] Requirements 126-REQ-1.1 through 126-REQ-5.E1, 126-REQ-7.1, 126-REQ-7.2 met

- [x] 3. Wire into run.py and cli/code.py
  - [x] 3.1 Call post-mortem generation in run_code()
    - In `agent_fox/engine/run.py`, after `state = await orchestrator.run()`:
      wrap in try/except, call `should_dump()`, `build_postmortem()`,
      `write_postmortem()`, set `state.postmortem_path`
    - Import `AUDIT_DIR` from `agent_fox.core.paths`
    - _Requirements: 126-REQ-1.1, 126-REQ-1.E1, 126-REQ-2.E1_

  - [x] 3.2 Add post-mortem path to CLI summary output
    - In `agent_fox/cli/code.py`, in `_print_summary()`, after the status
      line: print `Post-mortem: {path}` if `state.postmortem_path` is set
    - _Requirements: 126-REQ-6.1, 126-REQ-6.2_

  - [x] 3.V Verify task group 3
    - [x] Spec tests for this group pass: `uv run pytest -q tests/unit/cli/test_code.py -k postmortem`
    - [x] All existing tests still pass: `uv run pytest -q`
    - [x] No linter warnings introduced: `uv run ruff check agent_fox/engine/run.py agent_fox/cli/code.py`
    - [x] Requirements 126-REQ-6.1, 126-REQ-6.2 met

- [ ] 4. Wiring verification

  - [ ] 4.1 Trace every execution path from design.md end-to-end
    - For each path, verify the entry point actually calls the next function
      in the chain (read the calling code, do not assume)
    - Confirm no function in the chain is a stub (`return []`, `return None`,
      `pass`, `raise NotImplementedError`) that was never replaced
    - Every path must be live in production code — errata or deferrals do not
      satisfy this check
    - _Requirements: all_

  - [ ] 4.2 Verify return values propagate correctly
    - For every function in this spec that returns data consumed by a caller,
      confirm the caller receives and uses the return value
    - Grep for callers of each such function; confirm none discards the return
    - _Requirements: all_

  - [ ] 4.3 Run the integration smoke tests
    - All `TS-126-SMOKE-*` tests pass using real components (no stub bypass)
    - _Test Spec: TS-126-SMOKE-1 through TS-126-SMOKE-3_

  - [ ] 4.4 Stub / dead-code audit
    - Search all files touched by this spec for: `return []`, `return None`
      on non-Optional returns, `pass` in non-abstract methods, `# TODO`,
      `# stub`, `override point`, `NotImplementedError`
    - Each hit must be either: (a) justified with a comment explaining why it
      is intentional, or (b) replaced with a real implementation
    - Document any intentional stubs here with rationale

  - [ ] 4.5 Cross-spec entry point verification
    - Verify `postmortem.should_dump()` is called from `run.py`
    - Verify `postmortem.build_postmortem()` is called from `run.py`
    - Verify `postmortem.write_postmortem()` is called from `run.py`
    - Verify `state.run_id` is set in `engine.py`
    - Verify `state.postmortem_path` is checked in `cli/code.py`
    - _Requirements: all_

  - [ ] 4.V Verify wiring group
    - [ ] All smoke tests pass
    - [ ] No unjustified stubs remain in touched files
    - [ ] All execution paths from design.md are live (traceable in code)
    - [ ] All cross-spec entry points are called from production code
    - [ ] All existing tests still pass: `uv run pytest -q`

## Traceability

| Requirement | Test Spec Entry | Implemented By Task | Verified By Test |
|-------------|-----------------|---------------------|------------------|
| 126-REQ-1.1 | TS-126-1 | 2.3, 3.1 | test_postmortem.py::test_should_dump_trigger_statuses |
| 126-REQ-1.2 | TS-126-2 | 2.3 | test_postmortem.py::test_should_dump_non_trigger_statuses |
| 126-REQ-1.3 | TS-126-2 | 2.3 | test_postmortem.py::test_should_dump_non_trigger_statuses |
| 126-REQ-1.E1 | TS-126-E1 | 3.1 | test_postmortem.py::test_generation_failure_non_blocking |
| 126-REQ-1.E2 | TS-126-E2 | 2.3 | test_postmortem.py::test_fallback_run_id |
| 126-REQ-2.1 | TS-126-8 | 2.3 | test_postmortem.py::test_write_postmortem_file |
| 126-REQ-2.2 | TS-126-8 | 2.3 | test_postmortem.py::test_write_postmortem_file |
| 126-REQ-2.3 | TS-126-9 | 2.3 | test_postmortem.py::test_write_creates_directory |
| 126-REQ-2.E1 | TS-126-E5 | 3.1 | test_postmortem.py::test_write_failure_non_blocking |
| 126-REQ-3.1 | TS-126-3 | 2.3 | test_postmortem.py::test_required_keys |
| 126-REQ-3.2 | TS-126-3 | 2.3 | test_postmortem.py::test_required_keys |
| 126-REQ-3.3 | TS-126-4 | 2.3 | test_postmortem.py::test_task_summary_counts |
| 126-REQ-3.4 | TS-126-5 | 2.3 | test_postmortem.py::test_cost_summary |
| 126-REQ-3.5 | TS-126-6 | 2.3 | test_postmortem.py::test_blocked_tasks |
| 126-REQ-3.6 | TS-126-7 | 2.3 | test_postmortem.py::test_session_history |
| 126-REQ-4.1 | TS-126-6 | 2.3 | test_postmortem.py::test_blocked_tasks |
| 126-REQ-4.2 | TS-126-6 | 2.3 | test_postmortem.py::test_blocked_tasks |
| 126-REQ-4.E1 | TS-126-E3 | 2.3 | test_postmortem.py::test_missing_reason |
| 126-REQ-5.1 | TS-126-7 | 2.3 | test_postmortem.py::test_session_history |
| 126-REQ-5.2 | TS-126-5 | 2.3 | test_postmortem.py::test_cost_summary |
| 126-REQ-5.E1 | TS-126-E4 | 2.3 | test_postmortem.py::test_empty_state |
| 126-REQ-6.1 | TS-126-10 | 3.2 | test_code.py::test_postmortem_path_printed |
| 126-REQ-6.2 | TS-126-11 | 3.2 | test_code.py::test_postmortem_path_not_printed |
| 126-REQ-7.1 | TS-126-12 | 2.1 | test_postmortem.py::test_execution_state_run_id |
| 126-REQ-7.2 | TS-126-SMOKE-1 | 2.2 | TS-126-SMOKE-1 |
| Property 1 | TS-126-P1 | 2.3 | test_postmortem_props.py::test_trigger_completeness |
| Property 2 | TS-126-P2 | 2.3 | test_postmortem_props.py::test_no_false_triggers |
| Property 3 | TS-126-P3 | 2.3 | test_postmortem_props.py::test_schema_completeness |
| Property 4 | TS-126-P4 | 2.3 | test_postmortem_props.py::test_blocked_task_fidelity |
| Property 5 | TS-126-P5 | 2.3 | test_postmortem_props.py::test_session_history_fidelity |
| Property 6 | TS-126-P6 | 2.3 | test_postmortem_props.py::test_cost_summary_accuracy |
| Property 7 | TS-126-P7 | 2.3 | test_postmortem_props.py::test_file_round_trip |
| Property 8 | TS-126-P8 | 2.3 | test_postmortem_props.py::test_task_summary_accuracy |

## Notes

- No database migrations required — post-mortem files are standalone JSON.
- No config changes required — the feature is always active for non-successful runs.
- The `AUDIT_DIR` constant from `core/paths.py` is reused; no new paths.
- The `postmortem_path` field on `ExecutionState` is an output-only field;
  it is never loaded from the database.
