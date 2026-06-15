# Implementation Plan: Legacy Format Removal

<!-- AGENT INSTRUCTIONS
- Implement exactly ONE top-level task group per session
- Task group 1 writes failing tests from test_spec.md — all subsequent groups
  implement code to make those tests pass
- Follow the git-flow: feature branch from develop -> implement -> test -> merge to develop
- Update checkbox states as you go: [-] in progress, [x] complete
-->

## Overview

Eight implementation groups after writing tests. The order is designed so that
each group produces a codebase that passes `make test`:

1. Write failing tests
2. Create `types.py` (additive — nothing breaks)
3. Rewire source imports (both old and new imports work while parser.py exists)
4. Delete legacy source modules (safe because imports are already rewired)
5. Delete/update test files (safe because source modules are already deleted)
6. Remove format-routing and v1 references from remaining source
7. Update documentation
8. Wiring verification

## Test Commands

- Spec tests: `uv run pytest -q tests/spec/test_137_legacy_removal.py`
- All tests: `uv run pytest -q`
- Linter: `uv run ruff check`

## Tasks

- [ ] 1. Write failing spec tests
  - [ ] 1.1 Create test file structure
    - Create `tests/spec/test_137_legacy_removal.py`
    - _Test Spec: TS-137-1 through TS-137-10_

  - [ ] 1.2 Translate acceptance-criterion tests
    - Test types.py exports (TS-137-1, TS-137-2, TS-137-3)
    - Test file deletions (TS-137-4, TS-137-5, TS-137-6)
    - Test import rewiring (TS-137-7, TS-137-8, TS-137-9, TS-137-10)
    - _Test Spec: TS-137-1 through TS-137-10_

  - [ ] 1.3 Translate edge-case and property tests
    - Test ImportError from deleted modules (TS-137-E1, TS-137-E2)
    - Test no deleted module imports in tests (TS-137-E3)
    - Test type identity preserved (TS-137-P1)
    - Test full package importability (TS-137-P2)
    - _Test Spec: TS-137-E1 through TS-137-E3, TS-137-P1, TS-137-P2_

  - [ ] 1.4 Translate smoke tests
    - Test full test suite passes (TS-137-SMOKE-1)
    - Test lint-specs works after deletion (TS-137-SMOKE-2)
    - _Test Spec: TS-137-SMOKE-1, TS-137-SMOKE-2_

  - [ ] 1.V Verify task group 1
    - [ ] All spec tests exist and are syntactically valid
    - [ ] All spec tests FAIL (red) — no implementation yet
    - [ ] No linter warnings introduced: `uv run ruff check`

- [ ] 2. Create `spec/types.py` with shared types
  - [ ] 2.1 Create `agent_fox/spec/types.py`
    - Copy `TaskGroupDef`, `SubtaskDef`, `CrossSpecDep` dataclasses from
      `parser.py` with identical field signatures
    - Copy `Finding` dataclass from `validators/_helpers.py`
    - Copy `SEVERITY_ERROR`, `SEVERITY_WARNING`, `SEVERITY_HINT` constants
    - Copy `compute_exit_code()` and `sort_findings()` functions
    - _Requirements: 1.1, 1.2_

  - [ ] 2.V Verify task group 2
    - [ ] `types.py` exports are importable: `python -c "from agent_fox.spec.types import TaskGroupDef, Finding"`
    - [ ] All existing tests still pass: `uv run pytest -q`
    - [ ] No linter warnings introduced: `uv run ruff check`

- [ ] 3. Rewire source imports to `spec/types.py`
  - [ ] 3.1 Rewire spec-layer imports
    - Update `spec/parser_v12.py` to import from `spec.types`
    - Update `spec/lint.py` to import `Finding`, `compute_exit_code`,
      `sort_findings` from `spec.types`
    - Update `cli/lint_specs.py` to import `Finding`, severity constants
      from `spec.types`
    - _Requirements: 1.3, 5.4_

  - [ ] 3.2 Rewire graph-layer imports
    - Update `graph/builder.py` to import from `spec.types`
    - Update `graph/planner.py` to import from `spec.types` and use
      `parse_tasks_v12`/`parse_cross_deps_v12` exclusively
    - _Requirements: 5.1, 5.2_

  - [ ] 3.3 Rewire engine-layer imports
    - Update `engine/session_lifecycle.py`: replace `parse_tasks` with
      `parse_tasks_v12`, adapt `extract_subtask_descriptions()` to use
      `TaskGroupDef.body` from parsed v1.2 tasks
    - Update `engine/hot_load.py`: replace `EXPECTED_FILES` with v1.2 file
      list, replace `validate_specs()` with `afspec.validate()`, replace
      `parse_tasks`/`parse_cross_deps` with v1.2 equivalents, import
      `Finding` from `spec.types`
    - Update `engine/engine.py`: replace `parse_tasks` with `parse_tasks_v12`
    - Update `engine/dispatch.py`: replace `parse_tasks` with `parse_tasks_v12`
    - _Requirements: 5.3_

  - [ ] 3.V Verify task group 3
    - [ ] All existing tests still pass: `uv run pytest -q`
    - [ ] No linter warnings introduced: `uv run ruff check`
    - [ ] Grep confirms no engine module imports from `spec.parser`

- [ ] 4. Delete legacy source modules
  - [ ] 4.1 Delete v1 parser and AI validation
    - `git rm agent_fox/spec/parser.py`
    - `git rm agent_fox/spec/ai_validation.py`
    - _Requirements: 2.1, 4.1_

  - [ ] 4.2 Delete v1 validators directory
    - `git rm -r agent_fox/spec/validators/`
    - _Requirements: 3.1_

  - [ ] 4.3 Verify package importability
    - Run `python -c "import agent_fox"` to confirm no dangling imports
    - Fix any remaining import references found
    - _Requirements: 2.2_

  - [ ] 4.V Verify task group 4
    - [ ] `parser.py`, `validators/`, `ai_validation.py` do not exist on disk
    - [ ] `python -c "import agent_fox"` succeeds
    - [ ] All existing tests still pass: `uv run pytest -q` (expect some
          test failures from tests that import deleted modules — fixed in
          group 5)
    - [ ] No linter warnings introduced: `uv run ruff check`

- [ ] 5. Update test files
  - [ ] 5.1 Delete tests for deleted v1 modules
    - Delete `tests/unit/spec/test_parser.py`
    - Delete `tests/unit/spec/test_validator.py`
    - Delete `tests/unit/spec/test_validator_coverage_gaps.py`
    - Delete `tests/unit/spec/test_validator_plan_rules.py`
    - Delete `tests/unit/spec/test_validator_robustness_rules.py`
    - Delete `tests/unit/spec/test_ai_validator.py`
    - Delete `tests/unit/spec/test_stale_dependency.py`
    - _Requirements: 7.2, 7.3, 7.4_

  - [ ] 5.2 Update test imports for shared types
    - In all test files that import `TaskGroupDef`, `SubtaskDef`,
      `CrossSpecDep` from `agent_fox.spec.parser`, change to import from
      `agent_fox.spec.types`
    - In all test files that import `Finding`, severity constants from
      `agent_fox.spec.validators`, change to import from
      `agent_fox.spec.types`
    - Update `tests/spec/test_132_afspec_integration.py` to remove
      `V1_MARKDOWN` assertions
    - _Requirements: 7.1, 7.E1_

  - [ ] 5.3 Handle edge-case test files
    - Remove `_KNOWN_ARCHETYPES` test from
      `tests/unit/session/test_no_coordinator.py`
    - Remove `_KNOWN_ARCHETYPES` tests from
      `tests/property/test_no_coordinator_props.py`
    - Update or rewrite `tests/unit/spec/test_verification_checklist.py`
      to test only v1.2 code paths
    - _Requirements: 7.1_

  - [ ] 5.V Verify task group 5
    - [ ] All tests pass: `uv run pytest -q`
    - [ ] No test file imports from deleted modules (grep check)
    - [ ] No linter warnings introduced: `uv run ruff check`

- [ ] 6. Remove format-routing and v1 filename references
  - [ ] 6.1 Simplify `discovery.py`
    - Remove `V1_MARKDOWN` from `SpecFormat` enum
    - Simplify `_detect_format()` to only return `V1_2_JSON`
    - Remove v1 filtering from `discover_specs()` — all valid specs are v1.2
    - Ensure specs without `requirements.json` are excluded
    - _Requirements: 6.1, 6.2, 6.E1_

  - [ ] 6.2 Remove v1 code paths from lint and context
    - Remove v1 validation path from `spec/lint.py` (remove
      `v1_specs`/`v12_specs` partitioning, keep only `_validate_v12_spec`)
    - Remove v1 code path from `_is_spec_implemented()` in `lint.py`
    - Remove `_CORE_SPEC_FILES` from `session/context.py`
    - Remove v1 file-reading path from context assembly
    - _Requirements: 6.3, 3.2, 3.3_

  - [ ] 6.3 Remove v1 references from graph and spec modules
    - Update `graph/injection.py`: change `requirements.md` check to
      `requirements.json` in `build_review_only_graph()`
    - Update `graph/spec_helpers.py`: remove v1 branches for `design.md`
      and `test_spec.md`
    - Update `graph/file_impacts.py`: reference `architecture.md` instead
      of `design.md`
    - Strip v1 code paths from `spec/verification_checklist.py`
    - Remove `extract_test_spec_ids()` from `spec/_patterns.py` (its only
      callers were in deleted validators)
    - _Requirements: 6.4, 4.2, 4.3_

  - [ ] 6.V Verify task group 6
    - [ ] All tests pass: `uv run pytest -q`
    - [ ] Grep confirms no v1 filename strings in source (excluding
          `fix/spec_gen.py` and comments)
    - [ ] No `_CORE_SPEC_FILES` in `context.py`
    - [ ] No linter warnings introduced: `uv run ruff check`

- [ ] 7. Update documentation
  - [ ] 7.1 Update architecture docs
    - Update `docs/architecture/06-spec-format-v12.md` to describe v1.2
      as the sole format (remove dual-format coexistence language, remove
      "Migration Status" framing)
    - Update `docs/architecture/01-spec-authoring.md` to remove v1 artifact
      table and references to v1 format as an active option
    - Update `docs/architecture/02-planning.md` to remove dual-format
      references
    - Update `docs/architecture/03-execution-and-archetypes.md`
    - Update `docs/architecture/README.md`
    - _Requirements: 8.1, 8.2_

  - [ ] 7.2 Update top-level docs
    - Update `docs/architecture.md` (single-doc overview) to list only
      v1.2 artifacts
    - Update `docs/README.md` to reference only v1.2 format
    - Update `docs/spec-format-v2-implementation-plan.md` to mark
      migration as complete
    - _Requirements: 8.3, 8.4_

  - [ ] 7.V Verify task group 7
    - [ ] All tests pass: `uv run pytest -q`
    - [ ] Documentation renders correctly (no broken links)
    - [ ] No linter warnings introduced: `uv run ruff check`

- [ ] 8. Wiring verification

  - [ ] 8.1 Trace every execution path from design.md end-to-end
    - For each path, verify the entry point actually calls the next function
      in the chain (read the calling code, do not assume)
    - Confirm no function in the chain is a stub that was never replaced
    - Every path must be live in production code
    - _Requirements: all_

  - [ ] 8.2 Verify return values propagate correctly
    - For every function in this spec that returns data consumed by a caller,
      confirm the caller receives and uses the return value
    - Grep for callers of each such function; confirm none discards the return
    - _Requirements: all_

  - [ ] 8.3 Run the integration smoke tests
    - All `TS-137-SMOKE-*` tests pass using real components
    - _Test Spec: TS-137-SMOKE-1, TS-137-SMOKE-2_

  - [ ] 8.4 Stub / dead-code audit
    - Search all files touched by this spec for: `return []`, `return None`
      on non-Optional returns, `pass` in non-abstract methods, `# TODO`,
      `# stub`, `NotImplementedError`
    - Each hit must be either justified or replaced
    - _Requirements: all_

  - [ ] 8.5 Cross-spec entry point verification
    - Verify that lint-specs CLI, plan command, and code command all
      work without importing deleted modules
    - Verify that `build_review_only_graph()` correctly injects Verifier
      nodes for v1.2 specs (checks `requirements.json`)
    - _Requirements: all_

  - [ ] 8.V Verify wiring group
    - [ ] All smoke tests pass
    - [ ] No unjustified stubs remain in touched files
    - [ ] All execution paths from design.md are live
    - [ ] All existing tests still pass: `uv run pytest -q`

## Traceability

| Requirement | Test Spec Entry | Implemented By Task | Verified By Test |
|---|---|---|---|
| 137-REQ-1.1 | TS-137-1 | 2.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-1.2 | TS-137-2 | 2.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-1.3 | TS-137-3 | 3.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-1.E1 | TS-137-E1 | 4.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-2.1 | TS-137-4 | 4.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-2.2 | TS-137-P2 | 3.1-3.3, 4.3 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-3.1 | TS-137-5 | 4.2 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-3.2 | TS-137-7 | 6.2 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-3.3 | TS-137-7 | 6.2 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-3.4 | TS-137-8 | 3.3 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-3.E1 | TS-137-E2 | 4.2 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-4.1 | TS-137-6 | 4.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-4.2 | TS-137-9 | 6.3 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-4.3 | TS-137-9 | 6.3 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-5.1 | TS-137-3 | 3.2 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-5.2 | TS-137-8 | 3.2 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-5.3 | TS-137-8 | 3.3 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-5.4 | TS-137-7 | 3.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-5.E1 | TS-137-9 | 3.1-3.3, 6.2-6.3 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-6.1 | TS-137-9 | 6.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-6.2 | TS-137-SMOKE-2 | 6.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-6.3 | TS-137-10 | 6.2 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-6.4 | TS-137-9 | 6.3 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-6.E1 | TS-137-SMOKE-2 | 6.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-7.1 | TS-137-SMOKE-1 | 5.1-5.3 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-7.2 | TS-137-E3 | 5.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-7.3 | TS-137-E3 | 5.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-7.4 | TS-137-E3 | 5.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-7.E1 | TS-137-E3 | 5.2 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-8.1 | TS-137-SMOKE-1 | 7.1 | manual review |
| 137-REQ-8.2 | TS-137-SMOKE-1 | 7.1 | manual review |
| 137-REQ-8.3 | TS-137-SMOKE-1 | 7.2 | manual review |
| 137-REQ-8.4 | TS-137-SMOKE-1 | 7.2 | manual review |
