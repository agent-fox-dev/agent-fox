# Implementation Plan: v1.2 Parsing Pipeline

<!-- AGENT INSTRUCTIONS
- Implement exactly ONE top-level task group per session
- Task group 1 writes failing tests from test_spec.md — all subsequent groups
  implement code to make those tests pass
- Follow the git-flow: feature branch from develop -> implement -> test -> merge to develop
- Update checkbox states as you go: [-] in progress, [x] complete
-->

## Overview

This plan creates the v1.2 parser module and updates the planner to route
v1.2 specs through it. Three task groups: write tests, implement parser and
planner update, verify wiring.

## Test Commands

- Spec tests: `uv run pytest -q tests/spec/test_133_v12_parsing.py`
- All tests: `uv run pytest -q`
- Linter: `uv run ruff check`

## Tasks

- [ ] 1. Write failing spec tests
  - [ ] 1.1 Create test file and fixtures
    - Create `tests/spec/test_133_v12_parsing.py`
    - Create helper functions to build afspec model instances (Subtask, TaskGroup, TaskDependency) for use in tests
    - Create temporary v1.2 spec directory fixtures with valid JSON artifacts
    - _Test Spec: all_

  - [ ] 1.2 Translate subtask and task group mapper tests
    - Test subtask mapping with DONE state (TS-133-1)
    - Test subtask mapping with non-DONE states (TS-133-1)
    - Test task group mapping fields (TS-133-2)
    - Test group completed when all non-dropped are DONE (TS-133-3)
    - Test group not completed with non-DONE subtask (TS-133-4)
    - Test group body contains markdown (TS-133-5)
    - _Test Spec: TS-133-1 through TS-133-5_

  - [ ] 1.3 Translate dependency mapper and integration tests
    - Test dependency mapping field assignment (TS-133-6)
    - Test parse_tasks_v12 returns TaskGroupDef list (TS-133-7)
    - Test parse_cross_deps_v12 returns CrossSpecDep list (TS-133-8)
    - _Test Spec: TS-133-6 through TS-133-8_

  - [ ] 1.4 Translate edge-case and property tests
    - Test dropped subtasks excluded from completion (TS-133-E1)
    - Test no dependencies returns empty list (TS-133-E2)
    - Test load error propagation (TS-133-E3)
    - Test subtask completion is function of state (TS-133-P1)
    - Test group completion consistent with subtask states (TS-133-P2)
    - _Test Spec: TS-133-E1 through TS-133-E3, TS-133-P1, TS-133-P2_

  - [ ] 1.V Verify task group 1
    - [ ] All spec tests exist and are syntactically valid
    - [ ] All spec tests FAIL (red) — no implementation yet
    - [ ] No linter warnings introduced: `uv run ruff check`

- [ ] 2. Implement v1.2 parser and update planner
  - [ ] 2.1 Create parser_v12.py with mapper functions
    - Create `agent_fox/spec/parser_v12.py`
    - Implement `_map_subtask(subtask: afspec.Subtask) -> SubtaskDef`
    - Implement `_render_group_body(group: afspec.TaskGroup) -> str`
    - Implement `_map_task_group(group: afspec.TaskGroup) -> TaskGroupDef`
    - Implement `_map_dependency(dep: afspec.TaskDependency, current_spec: str) -> CrossSpecDep`
    - _Requirements: 1.1, 1.2, 2.1, 2.2, 2.3, 2.4, 3.1_

  - [ ] 2.2 Implement parse_tasks_v12 and parse_cross_deps_v12
    - Implement `parse_tasks_v12(spec_dir: Path) -> list[TaskGroupDef]`
    - Implement `parse_cross_deps_v12(spec_dir: Path, spec_name: str) -> list[CrossSpecDep]`
    - Both call `afspec.load_spec(spec_dir)` internally
    - _Requirements: 1.1, 2.1, 3.1, 4.1_

  - [ ] 2.3 Update planner.py build_plan() for format routing
    - Import `parse_tasks_v12` and `parse_cross_deps_v12` from `parser_v12`
    - Import `SpecFormat` from `discovery`
    - Add format check in the spec iteration loop
    - Route V1_2_JSON specs to new parser, keep existing path for v1
    - _Requirements: 4.1, 4.2_

  - [ ] 2.V Verify task group 2
    - [ ] Spec tests pass: `uv run pytest -q tests/spec/test_133_v12_parsing.py`
    - [ ] All existing tests still pass: `uv run pytest -q`
    - [ ] No linter warnings introduced: `uv run ruff check`
    - [ ] Requirements 1.1-4.2 acceptance criteria met

- [ ] 3. Wiring verification
  - [ ] 3.1 Trace every execution path from design.md end-to-end
    - Verify parse_tasks_v12 calls afspec.load_spec and maps all groups
    - Verify parse_cross_deps_v12 calls afspec.load_spec and maps all deps
    - Verify build_plan routes V1_2_JSON specs to parser_v12 functions
    - Verify build_plan does NOT call markdown parser for V1_2_JSON specs
    - _Requirements: all_

  - [ ] 3.2 Verify return values propagate correctly
    - parse_tasks_v12 returns list[TaskGroupDef] consumable by build_graph
    - parse_cross_deps_v12 returns list[CrossSpecDep] consumable by build_graph
    - build_graph produces valid TaskGraph from v1.2-parsed input
    - _Requirements: all_

  - [ ] 3.3 Run the integration smoke test
    - TS-133-SMOKE-1 passes using real components (no mocks)
    - _Test Spec: TS-133-SMOKE-1_

  - [ ] 3.4 Stub / dead-code audit
    - Search all files touched for: return [], return None, pass, # TODO, NotImplementedError
    - Each hit must be justified or replaced
    - _Requirements: all_

  - [ ] 3.V Verify wiring group
    - [ ] All smoke tests pass
    - [ ] No unjustified stubs remain in touched files
    - [ ] All execution paths from design.md are live
    - [ ] All existing tests still pass: `uv run pytest -q`

## Traceability

| Requirement | Test Spec Entry | Implemented By Task | Verified By Test |
|-------------|-----------------|---------------------|------------------|
| 133-REQ-1.1 | TS-133-1 | 2.1 | tests/spec/test_133_v12_parsing.py::test_map_subtask_done |
| 133-REQ-1.2 | TS-133-1 | 2.1 | tests/spec/test_133_v12_parsing.py::test_map_subtask_not_done |
| 133-REQ-1.E1 | TS-133-E1 | 2.1 | tests/spec/test_133_v12_parsing.py::test_dropped_subtask_completion |
| 133-REQ-2.1 | TS-133-2 | 2.1 | tests/spec/test_133_v12_parsing.py::test_map_task_group_fields |
| 133-REQ-2.2 | TS-133-3 | 2.1 | tests/spec/test_133_v12_parsing.py::test_group_completed_all_done |
| 133-REQ-2.3 | TS-133-4 | 2.1 | tests/spec/test_133_v12_parsing.py::test_group_not_completed |
| 133-REQ-2.4 | TS-133-5 | 2.1 | tests/spec/test_133_v12_parsing.py::test_group_body_markdown |
| 133-REQ-2.E1 | TS-133-E1 | 2.1 | tests/spec/test_133_v12_parsing.py::test_all_dropped_vacuously_complete |
| 133-REQ-3.1 | TS-133-6, TS-133-8 | 2.1, 2.2 | tests/spec/test_133_v12_parsing.py::test_map_dependency |
| 133-REQ-3.E1 | TS-133-E2, TS-133-8 | 2.2 | tests/spec/test_133_v12_parsing.py::test_no_deps_empty_list |
| 133-REQ-4.1 | TS-133-7 | 2.2, 2.3 | tests/spec/test_133_v12_parsing.py::test_parse_tasks_v12 |
| 133-REQ-4.2 | TS-133-SMOKE-1 | 2.3 | tests/spec/test_133_v12_parsing.py::test_smoke_full_pipeline |
| 133-REQ-4.E1 | TS-133-E3 | 2.2 | tests/spec/test_133_v12_parsing.py::test_load_error_propagates |
| Property 1 | TS-133-P1 | 2.1 | tests/spec/test_133_v12_parsing.py::test_completion_is_function_of_state |
| Property 2 | TS-133-P2 | 2.1 | tests/spec/test_133_v12_parsing.py::test_group_completion_consistent |
