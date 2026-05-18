# Implementation Plan: --dry-run Flag on code Command

<!-- AGENT INSTRUCTIONS
- Implement exactly ONE top-level task group per session
- Task group 1 writes failing tests from test_spec.md — all subsequent groups
  implement code to make those tests pass
- Follow the git-flow: feature branch from develop -> implement -> test -> merge to develop
- Update checkbox states as you go: [-] in progress, [x] complete
-->

## Overview

This spec adds a `--dry-run` flag to the `code` CLI command. The flag loads the
persisted plan from DuckDB read-only, filters completed nodes, and displays the
same analysis (phases, critical path, edges) already implemented for
`plan --dry-run` in spec 122. No orchestrator is started, no infrastructure is
set up, no coding sessions are dispatched.

The implementation is straightforward: all analysis infrastructure exists. The
work is adding the flag, mutual exclusion validation, and the dry-run branch
in `cli/code.py`.

## Test Commands

- Spec tests: `uv run pytest -q tests/unit/cli/test_code_dry_run.py`
- All tests: `uv run pytest -q`
- Linter: `make lint`

## Tasks

- [ ] 1. Write failing spec tests
  - [ ] 1.1 Create test file `tests/unit/cli/test_code_dry_run.py`
    - Set up fixtures: `cli_runner`, mock DB path, mock `load_plan`, mock
      `open_knowledge_store`, mock `discover_specs`
    - Helper to build TaskGraphs with configurable node statuses
    - _Test Spec: TS-123-1 through TS-123-11_

  - [ ] 1.2 Translate acceptance-criterion tests
    - `TestDryRunDisplaysAnalysis` (TS-123-1)
    - `TestDryRunSkipsOrchestrator` (TS-123-2)
    - `TestDryRunFiltersCompleted` (TS-123-3)
    - `TestNonDryRunUnchanged` (TS-123-4)
    - `TestMutualExclusionWatch` (TS-123-5)
    - `TestMutualExclusionDebug` (TS-123-6)
    - `TestMutualExclusionParallel` (TS-123-7)
    - `TestMutualExclusionForceClean` (TS-123-8)
    - `TestJsonOutput` (TS-123-9)
    - `TestDaemonGuardBypassed` (TS-123-10)
    - `TestDaemonGuardEnforced` (TS-123-11)
    - _Test Spec: TS-123-1 through TS-123-11_

  - [ ] 1.3 Translate edge-case tests
    - `TestMissingDbDryRun` (TS-123-E1)
    - `TestEmptyPlanDryRun` (TS-123-E2)
    - `TestAllCompletedDryRun` (TS-123-E3)
    - `TestMultipleIncompatibleFlags` (TS-123-E4)
    - `TestEmptyPlanJsonDryRun` (TS-123-E5)
    - _Test Spec: TS-123-E1 through TS-123-E5_

  - [ ] 1.4 Translate property tests
    - `TestPropertyNoOrchestrator` (TS-123-P1)
    - `TestPropertyCompletedExclusion` (TS-123-P2)
    - `TestPropertyMutualExclusion` (TS-123-P3)
    - `TestPropertyReadOnly` (TS-123-P4)
    - `TestPropertyDaemonBypass` (TS-123-P5)
    - _Test Spec: TS-123-P1 through TS-123-P5_

  - [ ] 1.5 Translate smoke tests
    - `TestSmokeTextOutput` (TS-123-SMOKE-1)
    - `TestSmokeJsonOutput` (TS-123-SMOKE-2)
    - `TestSmokeIncompatibleFlags` (TS-123-SMOKE-3)
    - _Test Spec: TS-123-SMOKE-1 through TS-123-SMOKE-3_

  - [ ] 1.V Verify task group 1
    - [ ] All spec tests exist and are syntactically valid
    - [ ] All spec tests FAIL (red) -- no implementation yet
    - [ ] No linter warnings introduced: `make lint`

- [ ] 2. Implement dry-run flag in cli/code.py
  - [ ] 2.1 Add `--dry-run` Click option to `code_cmd`
    - Add `is_flag=True, default=False` option
    - Add `dry_run` parameter to `code_cmd` function signature
    - _Requirements: 1.1, 1.4_

  - [ ] 2.2 Implement `_check_dry_run_conflicts()` helper
    - Accept `dry_run`, `parallel`, `debug`, `watch`, `force_clean` params
    - Return list of incompatible flag names (empty if no conflicts)
    - _Requirements: 2.1, 2.E1_

  - [ ] 2.3 Add mutual exclusion check at start of `code_cmd`
    - Call `_check_dry_run_conflicts()` after parsing args, before daemon check
    - If conflicts found, print error to stderr listing flags, exit 1
    - In JSON mode, emit error via `json_io.emit_error()`
    - _Requirements: 2.1, 2.E1_

  - [ ] 2.4 Implement dry-run branch in `code_cmd`
    - After mutual exclusion check, before daemon check:
      if `dry_run` is set, skip daemon PID guard entirely
    - Check `DEFAULT_DB_PATH.exists()` -- error if missing
    - Open knowledge store (read-only), call `load_plan(conn)`, close DB
    - Handle empty plan (no nodes) with "No tasks in plan." message
    - Filter completed nodes from `graph.nodes`, `graph.edges`, `graph.order`
    - Handle all-completed with "All tasks completed." message
    - Compute analysis: `compute_phases()`, `critical_path()`, `group_edges()`
    - Discover specs for display: `discover_specs()`
    - Text mode: `format_plan_analysis()` and `click.echo()`
    - JSON mode: build dict with all keys and `emit()`
    - Return before reaching orchestrator code
    - _Requirements: 1.1, 1.2, 1.3, 1.E1, 1.E2, 1.E3, 3.1, 3.E1, 4.1_

  - [ ] 2.V Verify task group 2
    - [ ] Spec tests for this group pass: `uv run pytest -q tests/unit/cli/test_code_dry_run.py`
    - [ ] All existing tests still pass: `uv run pytest -q`
    - [ ] No linter warnings introduced: `make lint`
    - [ ] Requirements 1.1-1.4, 1.E1-1.E3, 2.1, 2.E1, 3.1, 3.E1, 4.1, 4.2 met

- [ ] 3. Update documentation
  - [ ] 3.1 Update `docs/cli-reference.md`
    - Add `--dry-run` to the `code` command options table
    - Add a "Dry-Run Mode" subsection under the `code` command section,
      following the same structure as the `plan` command's dry-run docs
    - Document mutual exclusion with execution flags
    - Document JSON output keys
    - _Requirements: 1.1, 2.1, 3.1_

  - [ ] 3.V Verify task group 3
    - [ ] All tests still pass: `uv run pytest -q`
    - [ ] No linter warnings introduced: `make lint`
    - [ ] CLI reference accurately describes the new flag

- [ ] 4. Wiring verification

  - [ ] 4.1 Trace every execution path from design.md end-to-end
    - Path 1 (text output): verify `code_cmd` calls `load_plan` ->
      filter -> `compute_phases` -> `critical_path` -> `group_edges` ->
      `format_plan_analysis` -> `click.echo`
    - Path 2 (JSON output): verify same chain but ends with
      `_node_to_dict` / `_edge_to_dict` / `_metadata_to_dict` -> `emit()`
    - Path 3 (incompatible flags): verify `_check_dry_run_conflicts` ->
      error -> `sys.exit(1)`
    - _Requirements: all_

  - [ ] 4.2 Verify return values propagate correctly
    - `load_plan()` return consumed by filter logic
    - Filter output consumed by analyzer functions
    - Analyzer outputs consumed by formatter
    - No return values discarded
    - _Requirements: all_

  - [ ] 4.3 Run the integration smoke tests
    - All `TS-123-SMOKE-*` tests pass using real analyzer (not mocked)
    - _Test Spec: TS-123-SMOKE-1 through TS-123-SMOKE-3_

  - [ ] 4.4 Stub / dead-code audit
    - Search `cli/code.py` for: `return []`, `return None` on non-Optional,
      `pass` in non-abstract, `# TODO`, `# stub`, `NotImplementedError`
    - Each hit must be justified or replaced

  - [ ] 4.5 Cross-spec entry point verification
    - Verify `compute_phases`, `critical_path`, `group_edges` from spec 122
      are importable and called from `cli/code.py`
    - Verify `format_plan_analysis` from spec 122 is importable and called
    - Verify `_node_to_dict`, `_edge_to_dict`, `_metadata_to_dict` from
      `cli/plan.py` are importable for JSON path
    - _Requirements: all_

  - [ ] 4.V Verify wiring group
    - [ ] All smoke tests pass
    - [ ] No unjustified stubs remain in touched files
    - [ ] All execution paths from design.md are live (traceable in code)
    - [ ] All cross-spec entry points are called from production code
    - [ ] All existing tests still pass: `uv run pytest -q`

### Checkbox States

| Syntax   | Meaning                |
|----------|------------------------|
| `- [ ]`  | Not started (required) |
| `- [ ]*` | Not started (optional) |
| `- [x]`  | Completed              |
| `- [-]`  | In progress            |
| `- [~]`  | Queued                 |

## Traceability

| Requirement | Test Spec Entry | Implemented By Task | Verified By Test |
|-------------|-----------------|---------------------|------------------|
| 123-REQ-1.1 | TS-123-1 | 2.4 | `test_code_dry_run.py::TestDryRunDisplaysAnalysis` |
| 123-REQ-1.2 | TS-123-2 | 2.4 | `test_code_dry_run.py::TestDryRunSkipsOrchestrator` |
| 123-REQ-1.3 | TS-123-3 | 2.4 | `test_code_dry_run.py::TestDryRunFiltersCompleted` |
| 123-REQ-1.4 | TS-123-4 | 2.1 | `test_code_dry_run.py::TestNonDryRunUnchanged` |
| 123-REQ-1.E1 | TS-123-E1 | 2.4 | `test_code_dry_run.py::TestMissingDbDryRun` |
| 123-REQ-1.E2 | TS-123-E2 | 2.4 | `test_code_dry_run.py::TestEmptyPlanDryRun` |
| 123-REQ-1.E3 | TS-123-E3 | 2.4 | `test_code_dry_run.py::TestAllCompletedDryRun` |
| 123-REQ-2.1 | TS-123-5,6,7,8 | 2.2, 2.3 | `test_code_dry_run.py::TestMutualExclusion*` |
| 123-REQ-2.E1 | TS-123-E4 | 2.2, 2.3 | `test_code_dry_run.py::TestMultipleIncompatibleFlags` |
| 123-REQ-3.1 | TS-123-9 | 2.4 | `test_code_dry_run.py::TestJsonOutput` |
| 123-REQ-3.E1 | TS-123-E5 | 2.4 | `test_code_dry_run.py::TestEmptyPlanJsonDryRun` |
| 123-REQ-4.1 | TS-123-10 | 2.4 | `test_code_dry_run.py::TestDaemonGuardBypassed` |
| 123-REQ-4.2 | TS-123-11 | 2.1 | `test_code_dry_run.py::TestDaemonGuardEnforced` |
| Property 1 | TS-123-P1 | 2.4 | `test_code_dry_run.py::TestPropertyNoOrchestrator` |
| Property 2 | TS-123-P2 | 2.4 | `test_code_dry_run.py::TestPropertyCompletedExclusion` |
| Property 3 | TS-123-P3 | 2.2, 2.3 | `test_code_dry_run.py::TestPropertyMutualExclusion` |
| Property 5 | TS-123-P4 | 2.4 | `test_code_dry_run.py::TestPropertyReadOnly` |
| Property 6 | TS-123-P5 | 2.4 | `test_code_dry_run.py::TestPropertyDaemonBypass` |
| Path 1 | TS-123-SMOKE-1 | 2.4 | `test_code_dry_run.py::TestSmokeTextOutput` |
| Path 2 | TS-123-SMOKE-2 | 2.4 | `test_code_dry_run.py::TestSmokeJsonOutput` |
| Path 3 | TS-123-SMOKE-3 | 2.2, 2.3 | `test_code_dry_run.py::TestSmokeIncompatibleFlags` |

## Notes

- All analysis functions (`compute_phases`, `critical_path`, `group_edges`,
  `format_plan_analysis`) are already implemented and tested in spec 122.
  No changes to those modules are needed.
- The `_node_to_dict`, `_edge_to_dict`, `_metadata_to_dict` helpers in
  `cli/plan.py` should be imported directly for the JSON output path.
- The daemon PID guard is the only pre-existing check that needs conditional
  bypass; the DB existence check is shared between dry-run and normal paths.
