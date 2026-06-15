# Implementation Plan: v1.2 Context Assembly and Rendering

<!-- AGENT INSTRUCTIONS
- Implement exactly ONE top-level task group per session
- Task group 1 writes failing tests from test_spec.md — all subsequent groups
  implement code to make those tests pass
- Follow the git-flow: feature branch from develop -> implement -> test -> merge to develop
- Update checkbox states as you go: [-] in progress, [x] complete
-->

## Overview

This plan updates the context assembly pipeline, spec helpers, and
verification checklist to support v1.2 JSON spec rendering via afspec.
Four task groups: write tests, implement context assembly, implement
helpers and checklist, wiring verification.

## Test Commands

- Spec tests: `uv run pytest -q tests/spec/test_134_v12_context.py`
- All tests: `uv run pytest -q`
- Linter: `uv run ruff check`

## Tasks

- [x] 1. Write failing spec tests
  - [x] 1.1 Create test file and fixtures
    - Create `tests/spec/test_134_v12_context.py`
    - Create shared fixtures: v1.2 spec directory with valid JSON artifacts (prd.md with frontmatter, requirements.json, test_spec.json, tasks.json)
    - Create shared fixtures: v1 spec directory with markdown artifacts (requirements.md, design.md, test_spec.md, tasks.md)
    - Create shared fixtures: mock DuckDB connection returning no findings
    - _Test Spec: TS-134-1 through TS-134-9_

  - [x] 1.2 Translate context assembly tests
    - Test v1.2 format detection in assemble_context (TS-134-1)
    - Test v1 format unchanged in assemble_context (TS-134-2)
    - Test architecture.md included for v1.2 (TS-134-3)
    - Test architecture.md omitted when absent (TS-134-4)
    - _Test Spec: TS-134-1 through TS-134-4_

  - [x] 1.3 Translate spec helper tests
    - Test count_ts_entries with v1.2 test_spec.json (TS-134-5)
    - Test count_ts_entries with v1 test_spec.md (TS-134-6)
    - Test spec_has_existing_code with v1.2 architecture.md (TS-134-7)
    - _Test Spec: TS-134-5 through TS-134-7_

  - [x] 1.4 Translate verification checklist tests
    - Test v1.2 task checkbox audit from tasks.json (TS-134-8)
    - Test v1.2 requirement coverage from requirements.json (TS-134-9)
    - _Test Spec: TS-134-8, TS-134-9_

  - [x] 1.5 Translate edge-case and property tests
    - Test LoadError fallback in assemble_context (TS-134-E1)
    - Test empty render_individual artifact omitted (TS-134-E2)
    - Test count_ts_entries returns 0 on load failure (TS-134-E3)
    - Test verification checklist returns empty on load failure (TS-134-E4)
    - Test v1 path produces identical output (TS-134-P1)
    - Test v1.2 section order preserved (TS-134-P2)
    - _Test Spec: TS-134-E1 through TS-134-E4, TS-134-P1, TS-134-P2_

  - [x] 1.V Verify task group 1
    - [x] All spec tests exist and are syntactically valid
    - [x] All spec tests FAIL (red) -- no implementation yet
    - [x] No linter warnings introduced: `uv run ruff check`

- [x] 2. Implement v1.2 context assembly
  - [x] 2.1 Add v1.2 detection helper to context.py
    - Add `_is_v12_spec(spec_dir: Path) -> bool` function
    - Add `_V12_SECTION_HEADERS` constant mapping artifact names to headers
    - _Requirements: 1.1, 1.2_

  - [x] 2.2 Add v1.2 rendering function to context.py
    - Add `_render_v12_sections(spec_dir: Path) -> list[str]` function
    - Load spec via `afspec.load_spec()`, render via `afspec.render_individual()`
    - Read `architecture.md` from disk when present
    - Sanitize all rendered content via `sanitize_prompt_content()`
    - _Requirements: 2.1, 2.2, 2.3, 2.E1_

  - [x] 2.3 Update assemble_context to branch on format
    - Before the `_CORE_SPEC_FILES` loop, check `_is_v12_spec()`
    - If v1.2: call `_render_v12_sections()`, use result as `file_sections`
    - If v1.2 and LoadError: log warning, fall through to v1 path
    - If v1: use existing `_CORE_SPEC_FILES` loop unchanged
    - _Requirements: 1.1, 1.2, 1.E1_

  - [x] 2.V Verify task group 2
    - [x] Context assembly tests pass: TS-134-1 through TS-134-4, TS-134-E1, TS-134-E2, TS-134-P1, TS-134-P2
    - [x] All existing tests still pass: `uv run pytest -q`
    - [x] No linter warnings introduced: `uv run ruff check`

- [ ] 3. Implement v1.2-aware helpers and checklist
  - [ ] 3.1 Update count_ts_entries in spec_helpers.py
    - Add v1.2 branch: check for test_spec.json, load via afspec, count test entries
    - Preserve v1 branch unchanged
    - Handle load failures: return 0, log warning
    - _Requirements: 3.1, 3.2, 3.E1_

  - [ ] 3.2 Update spec_has_existing_code in spec_helpers.py
    - Add v1.2 branch: check for requirements.json, read architecture.md instead of design.md
    - Preserve v1 branch unchanged
    - _Requirements: 3.3_

  - [ ] 3.3 Update _audit_task_checkboxes in verification_checklist.py
    - Add v1.2 branch: check for tasks.json, load via afspec
    - Map TaskGroup.subtasks to SubtaskAuditEntry objects
    - Use Subtask.state enum for checked/skipped fields
    - Handle load failures: return empty list, log warning
    - _Requirements: 4.1, 4.E1_

  - [ ] 3.4 Update scan_requirement_test_coverage in verification_checklist.py
    - Add v1.2 branch: check for requirements.json, load via afspec
    - Extract requirement IDs from Requirements.requirements[*].id
    - Preserve test file scanning logic unchanged
    - Handle load failures: return empty list, log warning
    - _Requirements: 4.2, 4.E1_

  - [ ] 3.V Verify task group 3
    - [ ] Helper tests pass: TS-134-5 through TS-134-7, TS-134-E3
    - [ ] Checklist tests pass: TS-134-8, TS-134-9, TS-134-E4
    - [ ] All existing tests still pass: `uv run pytest -q`
    - [ ] No linter warnings introduced: `uv run ruff check`

- [ ] 4. Wiring verification
  - [ ] 4.1 Trace every execution path from design.md end-to-end
    - Verify assemble_context branches to v1.2 path for v1.2 specs
    - Verify assemble_context uses v1 path for v1 specs
    - Verify LoadError fallback actually works end-to-end
    - Verify count_ts_entries routes to correct branch
    - Verify verification checklist routes to correct branch
    - _Requirements: all_

  - [ ] 4.2 Run the integration smoke tests
    - TS-134-SMOKE-1 passes using real afspec components
    - TS-134-SMOKE-2 passes using real afspec components
    - _Test Spec: TS-134-SMOKE-1, TS-134-SMOKE-2_

  - [ ] 4.3 Stub / dead-code audit
    - Search all files touched for: return [], return None, pass, # TODO, NotImplementedError
    - Each hit must be justified or replaced
    - _Requirements: all_

  - [ ] 4.V Verify wiring group
    - [ ] All smoke tests pass
    - [ ] No unjustified stubs remain in touched files
    - [ ] All execution paths from design.md are live
    - [ ] All existing tests still pass: `uv run pytest -q`

## Traceability

| Requirement | Test Spec Entry | Implemented By Task | Verified By Test |
|-------------|-----------------|---------------------|------------------|
| 134-REQ-1.1 | TS-134-1 | 2.3 | tests/spec/test_134_v12_context.py::test_v12_format_detection |
| 134-REQ-1.2 | TS-134-2 | 2.3 | tests/spec/test_134_v12_context.py::test_v1_format_unchanged |
| 134-REQ-1.E1 | TS-134-E1 | 2.3 | tests/spec/test_134_v12_context.py::test_load_error_fallback |
| 134-REQ-2.1 | TS-134-1 | 2.2 | tests/spec/test_134_v12_context.py::test_v12_format_detection |
| 134-REQ-2.2 | TS-134-3 | 2.2 | tests/spec/test_134_v12_context.py::test_architecture_md_included |
| 134-REQ-2.3 | TS-134-4 | 2.2 | tests/spec/test_134_v12_context.py::test_architecture_md_omitted |
| 134-REQ-2.E1 | TS-134-E2 | 2.2 | tests/spec/test_134_v12_context.py::test_empty_artifact_omitted |
| 134-REQ-3.1 | TS-134-5 | 3.1 | tests/spec/test_134_v12_context.py::test_count_ts_v12 |
| 134-REQ-3.2 | TS-134-6 | 3.1 | tests/spec/test_134_v12_context.py::test_count_ts_v1 |
| 134-REQ-3.3 | TS-134-7 | 3.2 | tests/spec/test_134_v12_context.py::test_existing_code_v12 |
| 134-REQ-3.E1 | TS-134-E3 | 3.1 | tests/spec/test_134_v12_context.py::test_count_ts_load_failure |
| 134-REQ-4.1 | TS-134-8 | 3.3 | tests/spec/test_134_v12_context.py::test_checklist_tasks_v12 |
| 134-REQ-4.2 | TS-134-9 | 3.4 | tests/spec/test_134_v12_context.py::test_checklist_requirements_v12 |
| 134-REQ-4.E1 | TS-134-E4 | 3.3, 3.4 | tests/spec/test_134_v12_context.py::test_checklist_load_failure |
| Property 1 | TS-134-P2 | 2.2 | tests/spec/test_134_v12_context.py::test_v12_section_order |
| Property 2 | TS-134-P1 | 2.3 | tests/spec/test_134_v12_context.py::test_v1_path_identical |
| Path 1 | TS-134-SMOKE-1 | 2.2, 2.3 | tests/spec/test_134_v12_context.py::test_smoke_v12_assembly |
| Path 5 | TS-134-SMOKE-2 | 3.3, 3.4 | tests/spec/test_134_v12_context.py::test_smoke_v12_checklist |
