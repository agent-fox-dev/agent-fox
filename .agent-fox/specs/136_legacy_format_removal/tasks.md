# Implementation Plan: Legacy Format Removal

<!-- AGENT INSTRUCTIONS
- Implement exactly ONE top-level task group per session
- Task group 1 writes failing tests from test_spec.md — all subsequent groups
  implement code to make those tests pass
- Follow the git-flow: feature branch from develop -> implement -> test -> merge to develop
- Update checkbox states as you go: [-] in progress, [x] complete
-->

## Overview

Three implementation groups after writing tests: extract types, delete files
and rewire imports, then verify wiring. The order matters — types must exist
before the parser is deleted.

## Test Commands

- Spec tests: `uv run pytest -q tests/spec/test_136_legacy_removal.py`
- All tests: `uv run pytest -q`
- Linter: `uv run ruff check`

## Tasks

- [ ] 1. Write failing spec tests
  - [ ] 1.1 Create test file structure
    - Create `tests/spec/test_136_legacy_removal.py`
    - _Test Spec: TS-136-1 through TS-136-10_

  - [ ] 1.2 Translate acceptance-criterion tests
    - Test types.py exports (TS-136-1)
    - Test import compatibility (TS-136-2)
    - Test parser.py deleted (TS-136-3)
    - Test validators/ deleted (TS-136-4)
    - Test verification_checklist.py deleted (TS-136-5)
    - Test ai_validation.py deleted (TS-136-6)
    - Test engine imports clean (TS-136-7)
    - Test graph imports clean (TS-136-8)
    - Test no stale references (TS-136-9)
    - Test full suite passes (TS-136-10)
    - _Test Spec: TS-136-1 through TS-136-10_

  - [ ] 1.3 Translate edge-case tests
    - Test ImportError from deleted parser (TS-136-E1)
    - Test legacy test files cleaned (TS-136-E2)
    - Test fix/spec_gen.py preserved (TS-136-E3)
    - _Test Spec: TS-136-E1 through TS-136-E3_

  - [ ] 1.4 Translate property and smoke tests
    - Test full package importability (TS-136-P1, TS-136-SMOKE-1)
    - Test no old-format references (TS-136-P2)
    - Test lint-specs after deletion (TS-136-SMOKE-2)
    - _Test Spec: TS-136-P1, TS-136-P2, TS-136-SMOKE-1, TS-136-SMOKE-2_

  - [ ] 1.V Verify task group 1
    - [ ] All spec tests exist and are syntactically valid
    - [ ] All spec tests FAIL (red) — no implementation yet
    - [ ] No linter warnings introduced: `uv run ruff check`

- [ ] 2. Extract types and delete legacy modules
  - [ ] 2.1 Create agent_fox/spec/types.py
    - Copy `TaskGroupDef`, `SubtaskDef`, `CrossSpecDep` dataclasses from parser.py
    - Preserve identical field signatures and defaults
    - _Requirements: 1.1, 1.2_

  - [ ] 2.2 Rewire consumer imports to spec/types.py
    - Update `graph/planner.py` to import from `spec.types`
    - Update `graph/builder.py` to import from `spec.types`
    - Update `spec/parser_v12.py` to import from `spec.types`
    - Update `engine/session_lifecycle.py` to import from `spec.types` and use parser_v12
    - Update `engine/hot_load.py` to use parser_v12 functions
    - Update `engine/engine.py` to use parser_v12
    - Update `engine/dispatch.py` to use parser_v12
    - _Requirements: 4.1, 4.2, 4.3, 4.4_

  - [ ] 2.3 Delete legacy parser module
    - `git rm agent_fox/spec/parser.py`
    - _Requirements: 2.1_

  - [ ] 2.4 Delete legacy validators directory
    - `git rm -r agent_fox/spec/validators/`
    - _Requirements: 3.1_

  - [ ] 2.5 Delete legacy verification and AI validation modules
    - `git rm agent_fox/spec/verification_checklist.py`
    - `git rm agent_fox/spec/ai_validation.py`
    - _Requirements: 3.2, 3.3_

  - [ ] 2.6 Remove stale file references and constants
    - Remove `_CORE_SPEC_FILES` from `session/context.py` if not already done by spec 134
    - Remove `EXPECTED_FILES` references from `engine/hot_load.py`
    - Update `graph/spec_helpers.py` to reference JSON filenames
    - Update `graph/file_impacts.py` to reference JSON filenames
    - Update `graph/injection.py` to reference JSON filenames
    - _Requirements: 5.1, 5.2, 5.3_

  - [ ] 2.V Verify task group 2
    - [ ] Spec tests pass: `uv run pytest -q tests/spec/test_136_legacy_removal.py`
    - [ ] All existing tests still pass: `uv run pytest -q`
    - [ ] No linter warnings introduced: `uv run ruff check`
    - [ ] Requirements 1.1-5.3 acceptance criteria met

- [ ] 3. Clean up tests and final verification
  - [ ] 3.1 Delete or update legacy test files
    - Find test files for markdown parser: `grep -rn "spec.parser" tests/`
    - Find test files for validators: `grep -rn "spec.validators" tests/`
    - Find test files for verification_checklist: `grep -rn "verification_checklist" tests/`
    - Delete pure-legacy test files; update mixed ones
    - _Requirements: 6.2_

  - [ ] 3.2 Verify no stale imports in tests
    - Run `grep -rn "from agent_fox.spec.parser import\|from agent_fox.spec.validators" tests/`
    - Ensure zero matches
    - _Requirements: 6.2_

  - [ ] 3.3 Run full quality suite
    - Run `make check` to confirm zero failures
    - _Requirements: 6.1_

  - [ ] 3.V Verify task group 3
    - [ ] All spec tests pass: `uv run pytest -q tests/spec/test_136_legacy_removal.py`
    - [ ] All existing tests still pass: `uv run pytest -q`
    - [ ] No linter warnings introduced: `uv run ruff check`

- [ ] 4. Wiring verification
  - [ ] 4.1 Trace every execution path from design.md end-to-end
    - Verify types.py is imported correctly by all consumer modules
    - Verify parser.py, validators/, verification_checklist.py, ai_validation.py are gone
    - Verify lint-specs uses afspec validation, not deleted validators
    - _Requirements: all_

  - [ ] 4.2 Verify return values propagate correctly
    - TaskGroupDef from types.py is the same class used by parser_v12 and builder
    - No identity mismatch between imports from different paths
    - _Requirements: 1.2_

  - [ ] 4.3 Run the integration smoke tests
    - TS-136-SMOKE-1: all modules importable
    - TS-136-SMOKE-2: lint-specs works after deletion
    - _Test Spec: TS-136-SMOKE-1, TS-136-SMOKE-2_

  - [ ] 4.4 Stub / dead-code audit
    - Search all files touched for: return [], return None, pass, # TODO, NotImplementedError
    - Verify no stubs were introduced during rewiring
    - _Requirements: all_

  - [ ] 4.5 Reference audit
    - Run: `grep -rn "parser\.py\|validators/\|verification_checklist\|ai_validation" agent_fox/ --include="*.py" | grep -v __pycache__ | grep -v spec_gen`
    - Verify zero matches
    - _Requirements: 5.2_

  - [ ] 4.V Verify wiring group
    - [ ] All smoke tests pass
    - [ ] No unjustified stubs remain in touched files
    - [ ] All execution paths from design.md are live
    - [ ] All existing tests still pass: `uv run pytest -q`

## Traceability

| Requirement | Test Spec Entry | Implemented By Task | Verified By Test |
|-------------|-----------------|---------------------|------------------|
| 136-REQ-1.1 | TS-136-1 | 2.1 | test_136::test_types_exports |
| 136-REQ-1.2 | TS-136-2 | 2.2 | test_136::test_import_compatibility |
| 136-REQ-1.E1 | TS-136-E1 | 2.3 | test_136::test_parser_import_error |
| 136-REQ-2.1 | TS-136-3 | 2.3 | test_136::test_parser_deleted |
| 136-REQ-2.2 | TS-136-10 | 3.3 | test_136::test_full_suite |
| 136-REQ-2.E1 | TS-136-E2 | 3.1 | test_136::test_legacy_tests_cleaned |
| 136-REQ-3.1 | TS-136-4 | 2.4 | test_136::test_validators_deleted |
| 136-REQ-3.2 | TS-136-5 | 2.5 | test_136::test_checklist_deleted |
| 136-REQ-3.3 | TS-136-6 | 2.5 | test_136::test_ai_validation_deleted |
| 136-REQ-3.4 | TS-136-SMOKE-2 | 2.4 | test_136::test_lint_specs_after_deletion |
| 136-REQ-3.E1 | TS-136-4 | 2.4 | test_136::test_validators_deleted |
| 136-REQ-4.1 | TS-136-7 | 2.2 | test_136::test_engine_imports |
| 136-REQ-4.2 | TS-136-7 | 2.2 | test_136::test_engine_imports |
| 136-REQ-4.3 | TS-136-7 | 2.2 | test_136::test_engine_imports |
| 136-REQ-4.4 | TS-136-8 | 2.2 | test_136::test_graph_imports |
| 136-REQ-4.E1 | TS-136-E1 | 2.3 | test_136::test_parser_import_error |
| 136-REQ-5.1 | TS-136-9 | 2.6 | test_136::test_no_stale_refs |
| 136-REQ-5.2 | TS-136-9 | 2.6 | test_136::test_no_stale_refs |
| 136-REQ-5.3 | TS-136-9 | 2.6 | test_136::test_no_stale_refs |
| 136-REQ-5.E1 | TS-136-E3 | 2.6 | test_136::test_spec_gen_preserved |
| 136-REQ-6.1 | TS-136-10 | 3.3 | test_136::test_full_suite |
| 136-REQ-6.2 | TS-136-E2 | 3.1 | test_136::test_legacy_tests_cleaned |
| 136-REQ-6.E1 | TS-136-E2 | 3.1 | test_136::test_legacy_tests_cleaned |
| Property 2 | TS-136-P1 | 4.1 | test_136::test_package_importability |
| Property 3 | TS-136-P2 | 4.5 | test_136::test_no_old_format_refs |
