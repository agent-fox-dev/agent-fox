# Implementation Plan: Legacy Format Removal

<!-- AGENT INSTRUCTIONS
- Implement exactly ONE top-level task group per session
- Task group 1 writes failing tests from test_spec.md — all subsequent groups
  implement code to make those tests pass
- Follow the git-flow: feature branch from develop -> implement -> test -> merge to develop
- Update checkbox states as you go: [-] in progress, [x] complete
-->

## Overview

Twelve implementation groups after writing tests. Each group is designed to
leave `make test` passing (no intentional breakage between groups):

1. Write failing tests
2. Create `types.py` (additive — nothing breaks)
3. Rewire spec-layer and graph-layer imports (low risk)
4. Rewire engine-layer imports (high risk — critical runtime paths)
5. Rewire test file imports (while old modules still exist, so both paths work)
6. Remove last legacy imports from production code (prerequisite for deletion)
7. Delete legacy source modules (safe — all production imports now rewired)
8. Delete v1 test files and fix edge-case tests
9. Simplify discovery and remove v1 code from lint/context
10. Remove v1 references from graph and spec modules
11. Update documentation
12. Wiring verification

**Critical API difference:** `parse_tasks(file_path)` takes a Path to
`tasks.md`, but `parse_tasks_v12(spec_dir)` takes a Path to the spec
directory. Every call site must change from
`parse_tasks(spec_path / "tasks.md")` to `parse_tasks_v12(spec_path)`.
Similarly, `parse_cross_deps(prd_path, spec_name)` becomes
`parse_cross_deps_v12(spec_dir, spec_name)`.

## Test Commands

- Spec tests: `uv run pytest -q tests/spec/test_137_legacy_removal.py`
- All tests: `uv run pytest -q`
- Linter: `uv run ruff check`

## Tasks

- [x] 1. Write failing spec tests
  - [x] 1.1 Create test file structure
    - Create `tests/spec/test_137_legacy_removal.py`
    - _Test Spec: TS-137-1 through TS-137-10_

  - [x] 1.2 Translate acceptance-criterion tests
    - Test types.py exports (TS-137-1, TS-137-2, TS-137-3)
    - Test file deletions (TS-137-4, TS-137-5, TS-137-6)
    - Test import rewiring (TS-137-7, TS-137-8, TS-137-9, TS-137-10)
    - _Test Spec: TS-137-1 through TS-137-10_

  - [x] 1.3 Translate edge-case and property tests
    - Test ImportError from deleted modules (TS-137-E1, TS-137-E2)
    - Test no deleted module imports in tests (TS-137-E3)
    - Test type identity preserved (TS-137-P1)
    - Test full package importability (TS-137-P2)
    - _Test Spec: TS-137-E1 through TS-137-E3, TS-137-P1, TS-137-P2_

  - [x] 1.4 Translate smoke tests
    - Test full test suite passes (TS-137-SMOKE-1)
    - Test lint-specs works after deletion (TS-137-SMOKE-2)
    - _Test Spec: TS-137-SMOKE-1, TS-137-SMOKE-2_

  - [x] 1.V Verify task group 1
    - [x] All spec tests exist and are syntactically valid
    - [x] All spec tests FAIL (red) — no implementation yet
    - [x] No linter warnings introduced: `uv run ruff check`

- [x] 2. Create `spec/types.py` with shared types
  - [x] 2.1 Create `agent_fox/spec/types.py`
    - Copy `TaskGroupDef`, `SubtaskDef`, `CrossSpecDep` dataclasses from
      `parser.py` with identical field signatures
    - Copy `Finding` dataclass from `validators/_helpers.py`
    - Copy `SEVERITY_ERROR`, `SEVERITY_WARNING`, `SEVERITY_HINT` constants
    - Copy `compute_exit_code()` and `sort_findings()` functions
    - Ensure all imports are self-contained (no dependency on parser.py
      or validators/)
    - _Requirements: 1.1, 1.2_

  - [x] 2.V Verify task group 2
    - [x] `types.py` exports are importable: `python -c "from agent_fox.spec.types import TaskGroupDef, Finding"`
    - [x] All existing tests still pass: `uv run pytest -q`
    - [x] No linter warnings introduced: `uv run ruff check`

- [x] 3. Rewire spec-layer and graph-layer imports
  - [x] 3.1 Rewire spec-layer imports
    - Update `spec/parser_v12.py`: import `TaskGroupDef`, `SubtaskDef`,
      `CrossSpecDep` from `spec.types` instead of `spec.parser`
    - Update `spec/lint.py`: import `Finding`, `compute_exit_code`,
      `sort_findings` from `spec.types` instead of `spec.validators`
    - Update `cli/lint_specs.py`: import `Finding`, `SEVERITY_ERROR`,
      `SEVERITY_WARNING`, `SEVERITY_HINT` from `spec.types` instead of
      `spec.validators`
    - _Requirements: 1.3, 5.4_

  - [x] 3.2 Rewire graph-layer imports
    - Update `graph/builder.py`: import `TaskGroupDef`, `CrossSpecDep`
      from `spec.types` instead of `spec.parser`
    - Update `graph/planner.py`: import `CrossSpecDep` from `spec.types`,
      remove `parse_tasks` and `parse_cross_deps` imports from `spec.parser`,
      use `parse_tasks_v12(spec.path)` and `parse_cross_deps_v12(spec.path,
      spec_name)` exclusively (remove the format-routing `if` block)
    - NOTE: `parse_tasks_v12` takes a spec directory, NOT a tasks.md file
      path — update call sites accordingly
    - _Requirements: 5.1, 5.2_

  - [x] 3.V Verify task group 3
    - [x] All existing tests still pass: `uv run pytest -q`
    - [x] No linter warnings introduced: `uv run ruff check`

- [x] 4. Rewire engine-layer imports
  - [x] 4.1 Rewire `engine/engine.py`
    - In `build_summary_comment()`: replace late import of `parse_tasks`
      from `spec.parser` with `parse_tasks_v12` from `spec.parser_v12`
    - Change `parse_tasks(tasks_path)` to `parse_tasks_v12(spec_path)`
      where `spec_path` is the spec directory (parent of `tasks_path`)
    - Handle graceful fallback: if tasks.json doesn't exist, return empty
      group list (matching current behavior for missing tasks.md)
    - _Requirements: 5.3_

  - [x] 4.2 Rewire `engine/dispatch.py`
    - In `is_task_group_done_file()`: replace late import of `parse_tasks`
      from `spec.parser` with `parse_tasks_v12` from `spec.parser_v12`
    - Change `parse_tasks(tasks_path)` to `parse_tasks_v12(specs_dir /
      spec_name)` — the function receives `specs_dir` and `spec_name`
      separately, reconstruct the spec directory path
    - Update the file existence check from `tasks.md` to verify the spec
      directory exists
    - _Requirements: 5.3_

  - [x] 4.3 Rewire `engine/session_lifecycle.py`
    - Replace import of `_GROUP_PATTERN`, `_SUBTASK_PATTERN`, `parse_tasks`
      from `spec.parser` with `parse_tasks_v12` from `spec.parser_v12`
    - Adapt `extract_subtask_descriptions()`: instead of regex-parsing
      `tasks.md` with `_GROUP_PATTERN`/`_SUBTASK_PATTERN`, call
      `parse_tasks_v12(spec_path)` and extract descriptions from
      `TaskGroupDef.body` (which contains a markdown rendering of subtasks)
    - Preserve the function's return type and caller contract
    - _Requirements: 5.3_

  - [x] 4.4 Rewire `engine/hot_load.py`
    - Replace `from agent_fox.spec.parser import parse_cross_deps, parse_tasks`
      with imports from `spec.parser_v12`
    - Replace `from agent_fox.spec.validators import EXPECTED_FILES, Finding,
      validate_specs` with: `Finding` from `spec.types`, inline v1.2
      expected files list (`["prd.md", "requirements.json", "test_spec.json",
      "tasks.json"]`)
    - In `is_spec_complete()`: use the v1.2 file list instead of
      `EXPECTED_FILES`
    - In `lint_spec_gate()`: update SpecInfo construction to check
      `tasks.json` instead of `tasks.md` for `has_tasks`; replace
      `validate_specs()` call with `afspec.validate()` + finding mapping
      (reuse `_map_afspec_findings()` pattern from `lint.py`)
    - In `are_all_tasks_done()`: change `tasks.md` path to use
      `parse_tasks_v12(spec_path)` instead of `parse_tasks(tasks_path)`
    - In `_validate_and_parse_specs()`: change `tasks.md` references to
      use `parse_tasks_v12(spec_info.path)` and
      `parse_cross_deps_v12(spec_info.path, spec_info.name)`
    - _Requirements: 5.3, 3.4_

  - [x] 4.V Verify task group 4
    - [x] All existing tests still pass: `uv run pytest -q`
    - [x] No linter warnings introduced: `uv run ruff check`
    - [x] Grep confirms no engine module imports from `spec.parser`:
      `grep -rn "from agent_fox.spec.parser" agent_fox/engine/`

- [x] 5. Rewire test file imports
  - [x] 5.1 Update test imports for shared types
    - In all test files that import `TaskGroupDef`, `SubtaskDef`, or
      `CrossSpecDep` from `agent_fox.spec.parser`, change to import from
      `agent_fox.spec.types`. Known files:
      - `tests/unit/graph/test_builder.py`
      - `tests/unit/graph/test_builder_archetypes.py`
      - `tests/unit/graph/test_builder_auditor.py`
      - `tests/unit/graph/test_no_coordinator.py`
      - `tests/unit/oracle/test_graph_builder.py`
      - `tests/property/oracle/test_oracle_props.py`
      - `tests/property/graph/test_fast_mode_props.py`
      - `tests/integration/oracle/test_store.py`
      - `tests/spec/test_133_v12_parsing.py`
    - _Requirements: 7.E1_

  - [x] 5.2 Update test imports for validation types
    - In all test files that import `Finding` or severity constants from
      `agent_fox.spec.validators`, change to import from
      `agent_fox.spec.types`. Known files:
      - `tests/unit/engine/test_hot_load_gates.py`
      - `tests/property/engine/test_hot_load_gate_props.py`
      - `tests/spec/test_135_v12_skill_validation.py`
    - _Requirements: 7.1_

  - [x] 5.3 Update SpecFormat-related tests
    - Update `tests/spec/test_132_afspec_integration.py`: remove tests
      that assert `SpecFormat.V1_MARKDOWN` existence, update tests that
      construct `SpecInfo` with `V1_MARKDOWN` format
    - _Requirements: 7.1_

  - [x] 5.V Verify task group 5
    - [x] All existing tests still pass: `uv run pytest -q`
    - [x] No linter warnings introduced: `uv run ruff check`
    - [x] Grep confirms no test file imports from `spec.parser`:
      `grep -rn "from agent_fox.spec.parser" tests/`

- [ ] 6. Remove last legacy imports from production code
  - [ ] 6.1 Remove v1 code paths from `spec/lint.py`
    - Remove `from agent_fox.spec.validators import validate_specs`
      (line 24)
    - Remove the `v1_specs`/`v12_specs` partitioning (lines 214-215)
      and the `if v1_specs: findings.extend(validate_specs(...))` block
      (lines 220-221) — keep only the `for spec in ...:
      _validate_v12_spec()` loop, iterating over all `discovered` specs
    - Remove the late import `from agent_fox.spec.parser import
      parse_tasks` inside `_is_spec_implemented()` (line 137) and
      replace with `from agent_fox.spec.parser_v12 import
      parse_tasks_v12`; update the call from `parse_tasks(tasks_path)`
      to `parse_tasks_v12(spec.path)` (spec directory, not tasks.md)
    - Remove the late import `from agent_fox.spec.ai_validation import
      run_ai_validation` (line 251) and its surrounding `try/except`
      block — or gate it behind `if importlib.util.find_spec(...)` so
      it degrades gracefully once the module is deleted
    - _Requirements: 3.2, 3.3, 6.3_

  - [ ] 6.2 Remove v1 code paths from `spec/verification_checklist.py`
    - Replace `from agent_fox.spec.parser import parse_tasks` (line 17)
      with `from agent_fox.spec.parser_v12 import parse_tasks_v12`
    - In `audit_task_checkboxes()` (~line 88): remove the v1/v1.2
      branching — call `_audit_task_checkboxes_v12()` unconditionally
    - Delete `_audit_task_checkboxes_v1()` (starts at line 129) — the
      entire function that reads `tasks.md` and calls `parse_tasks()`
    - Delete `_extract_requirement_ids_v1()` (starts at ~line 258) —
      the function that reads `requirements.md` via regex
    - Update any remaining callers of the deleted functions to use the
      v1.2 equivalents
    - _Requirements: 4.2, 4.3_

  - [ ] 6.3 Remove `extract_test_spec_ids()` from `spec/_patterns.py`
    - Delete the function `extract_test_spec_ids()` (starts at line 37)
      which reads `test_spec.md` — its only callers were in the
      `validators/` package being deleted
    - _Requirements: 4.3_

  - [ ] 6.V Verify task group 6
    - [ ] All tests pass: `uv run pytest -q`
    - [ ] Grep confirms no production code imports from legacy modules:
          `grep -rn "from agent_fox.spec.parser import\|from agent_fox.spec.validators import\|from agent_fox.spec.ai_validation import" agent_fox/ --include="*.py" | grep -v __pycache__ | grep -v "spec/parser.py\|spec/ai_validation.py\|spec/validators/"`
          (hits within the legacy modules themselves are OK — they're
          deleted next)
    - [ ] No linter warnings introduced: `uv run ruff check`

- [ ] 7. Delete legacy source modules
  - [ ] 7.1 Delete v1 source modules
    - `git rm agent_fox/spec/parser.py`
    - `git rm agent_fox/spec/ai_validation.py`
    - `git rm -r agent_fox/spec/validators/`
    - _Requirements: 2.1, 3.1, 4.1_

  - [ ] 7.2 Verify package importability after deletion
    - Run `python -c "import agent_fox"` to confirm no dangling imports
    - If any ImportError, grep for the symbol and fix the remaining
      reference — the likely source is a late import that was missed
    - _Requirements: 2.2_

  - [ ] 7.V Verify task group 7
    - [ ] All tests pass: `uv run pytest -q`
    - [ ] `parser.py`, `validators/`, `ai_validation.py` do not exist
    - [ ] No linter warnings introduced: `uv run ruff check`

- [ ] 8. Delete v1 test files and fix edge-case tests
  - [ ] 8.1 Delete v1-only test files
    - `git rm tests/unit/spec/test_parser.py`
    - `git rm tests/unit/spec/test_validator.py`
    - `git rm tests/unit/spec/test_validator_coverage_gaps.py`
    - `git rm tests/unit/spec/test_validator_plan_rules.py`
    - `git rm tests/unit/spec/test_validator_robustness_rules.py`
    - `git rm tests/unit/spec/test_ai_validator.py`
    - `git rm tests/unit/spec/test_stale_dependency.py`
    - `git rm tests/property/spec/test_validator_props.py`
    - _Requirements: 7.2, 7.3, 7.4_

  - [ ] 8.2 Fix `tests/unit/graph/test_builder_archetypes.py`
    - Three late imports of `parse_tasks` from `agent_fox.spec.parser`
      (lines 184, 200, 464) — change each to `from
      agent_fox.spec.parser_v12 import parse_tasks_v12` and update the
      call sites from `parse_tasks(path)` to `parse_tasks_v12(spec_dir)`
      (spec directory, not tasks.md path)
    - _Requirements: 7.E1_

  - [ ] 8.3 Fix `tests/unit/session/test_no_coordinator.py`
    - Delete class `TestParserKnownArchetypesExcludesCoordinator`
      (line 65) containing method
      `test_parser_known_archetypes_excludes_coordinator` (line 68) —
      it imports `_KNOWN_ARCHETYPES` from `spec.parser` which no longer
      exists. Keep all other test classes in this file.
    - _Requirements: 7.1_

  - [ ] 8.4 Fix `tests/property/test_no_coordinator_props.py`
    - Delete method `test_no_coordinator_in_known_archetypes` (line 31)
      and the related method at line 40 — both import
      `_KNOWN_ARCHETYPES` from `spec.parser`. Keep all other tests.
    - _Requirements: 7.1_

  - [ ] 8.5 Fix `tests/unit/spec/test_verification_checklist.py`
    - Remove any test fixtures or helpers that create v1 `tasks.md` or
      `requirements.md` files
    - Remove test methods that exercise the now-deleted
      `_audit_task_checkboxes_v1` or `_extract_requirement_ids_v1`
      functions
    - Keep tests that exercise v1.2 code paths (JSON-based fixtures)
    - _Requirements: 7.1_

  - [ ] 8.V Verify task group 8
    - [ ] All tests pass: `uv run pytest -q`
    - [ ] No test file imports from deleted modules:
          `grep -rn "from agent_fox.spec.parser import\|from agent_fox.spec.validators import\|from agent_fox.spec.ai_validation import" tests/ --include="*.py"`
    - [ ] No linter warnings introduced: `uv run ruff check`

- [ ] 9. Simplify discovery and remove v1 code from lint/context
  - [ ] 9.1 Simplify `spec/discovery.py`
    - Remove `V1_MARKDOWN` from `SpecFormat` enum (keep `V1_2_JSON`)
    - Change `SpecInfo.format` default from `SpecFormat.V1_MARKDOWN` to
      `SpecFormat.V1_2_JSON`
    - Simplify `_detect_format()` — always return `V1_2_JSON`, or remove
      entirely and inline the check
    - Remove v1 filtering from `discover_specs()` — return all valid specs
    - Ensure specs without `requirements.json` are still excluded (they
      are not valid v1.2 specs)
    - Update `SpecInfo.has_tasks` to check `tasks.json` instead of
      `tasks.md`
    - _Requirements: 6.1, 6.2, 6.E1_

  - [ ] 9.2 Remove v1 code from `session/context.py`
    - Delete the `_CORE_SPEC_FILES` constant (line 48) which lists
      `requirements.md`, `design.md`, `test_spec.md`
    - Remove the v1 file-reading loop at ~line 411 that iterates over
      `_CORE_SPEC_FILES` — replace with v1.2 context assembly (or
      remove if already superseded by `afspec.render_individual()`)
    - Remove the comment block listing v1 filenames (~lines 337-339)
    - _Requirements: 3.2, 3.3, 6.3_

  - [ ] 9.V Verify task group 9
    - [ ] All tests pass: `uv run pytest -q`
    - [ ] No `V1_MARKDOWN` in `discovery.py`
    - [ ] No `_CORE_SPEC_FILES` in `context.py`
    - [ ] No linter warnings introduced: `uv run ruff check`

- [ ] 10. Remove v1 references from graph and spec modules
  - [ ] 10.1 Remove v1 references from graph modules
    - In `graph/injection.py` line 426: change
      `(spec_dir / "requirements.md").exists()` to
      `(spec_dir / "requirements.json").exists()` — HIGH RISK: without
      this, Verifier nodes won't be injected for v1.2 specs in
      review-only mode
    - In `graph/spec_helpers.py` line 71: remove the v1 branch that
      checks `test_spec.md`, keep only `test_spec.json` path
    - In `graph/spec_helpers.py` line 108: remove the `design.md`
      fallback, keep only `architecture.md`
    - In `graph/file_impacts.py` line 67: change `design.md` reference
      to `architecture.md`
    - _Requirements: 6.4_

  - [ ] 10.2 Remove v1 branch from `spec/lint.py`
    - In `_is_spec_implemented()`: if the v1 branch (checking
      `tasks.md`) was not fully removed in group 6.1, remove it now
    - _Requirements: 6.3_

  - [ ] 10.V Verify task group 10
    - [ ] All tests pass: `uv run pytest -q`
    - [ ] Grep confirms no v1 filename strings in source (excluding
          `fix/spec_gen.py`, comments, and docstrings):
          `grep -rn "requirements\.md\|design\.md\|test_spec\.md" agent_fox/ --include="*.py" | grep -v spec_gen | grep -v __pycache__`
    - [ ] No linter warnings introduced: `uv run ruff check`

- [ ] 11. Update documentation
  - [ ] 11.1 Update architecture docs
    - Update `docs/architecture/06-spec-format-v12.md`: describe v1.2 as
      the sole format, remove "Dual-Format Coexistence" section, remove
      "Migration Status" framing, simplify to describe the current state
    - Update `docs/architecture/01-spec-authoring.md`: remove v1 artifact
      table, describe only v1.2 artifacts as the spec structure
    - Update `docs/architecture/02-planning.md`: remove dual-format
      references in Phase 1 and Phase 4 descriptions
    - Update `docs/architecture/03-execution-and-archetypes.md`: simplify
      context assembly description to v1.2-only
    - Update `docs/architecture/README.md`: update Part 6 summary
    - _Requirements: 8.1, 8.2_

  - [ ] 11.2 Update top-level docs
    - Update `docs/architecture.md`: remove dual-format spec artifacts
      table, list only v1.2 files
    - Update `docs/README.md`: simplify spec format description to
      v1.2-only
    - Update `docs/spec-format-v2-implementation-plan.md`: mark migration
      as complete, note that legacy code has been removed
    - _Requirements: 8.3, 8.4_

  - [ ] 11.V Verify task group 11
    - [ ] All tests pass: `uv run pytest -q`
    - [ ] Documentation renders correctly (no broken links)
    - [ ] No linter warnings introduced: `uv run ruff check`

- [ ] 12. Wiring verification

  - [ ] 12.1 Trace every execution path from design.md end-to-end
    - Path 1 (lint validates spec): trace from `lint_specs.py` through
      `lint.py` → `_validate_v12_spec()` → `afspec.validate()` →
      `_map_afspec_findings()` → `compute_exit_code()`. Verify each
      function call exists in production code.
    - Path 2 (planner parses spec): trace from `planner.py` through
      `parse_tasks_v12()` → `TaskGroupDef` → `builder.py`. Verify the
      format-routing `if` block has been removed and v1.2 is the only path.
    - Path 3 (context assembles spec): trace from `context.py` through
      `afspec.load_spec()` → `afspec.render_individual()` →
      `verification_checklist.py`. Verify no v1 fallback path remains.
    - _Requirements: all_

  - [ ] 12.2 Verify hot-load pipeline end-to-end
    - Trace `discover_new_specs_gated()` → `is_spec_complete()` (v1.2
      file list) → `lint_spec_gate()` (`afspec.validate()`) →
      `are_all_tasks_done()` (`parse_tasks_v12`) →
      `_validate_and_parse_specs()` (`parse_tasks_v12`,
      `parse_cross_deps_v12`)
    - Verify every function uses v1.2 file paths (tasks.json, not
      tasks.md)
    - _Requirements: all_

  - [ ] 12.3 Verify review-only graph injection
    - Confirm `build_review_only_graph()` in `injection.py` checks for
      `requirements.json` (not `requirements.md`)
    - Verify a v1.2 spec directory with `requirements.json` would
      produce a Verifier node
    - _Requirements: all_

  - [ ] 12.4 Run the integration smoke tests
    - All `TS-137-SMOKE-*` tests pass using real components
    - _Test Spec: TS-137-SMOKE-1, TS-137-SMOKE-2_

  - [ ] 12.5 Stub / dead-code audit
    - Search all files touched by this spec for: `return []`, `return None`
      on non-Optional returns, `pass` in non-abstract methods, `# TODO`,
      `# stub`, `NotImplementedError`
    - Each hit must be either justified or replaced
    - _Requirements: all_

  - [ ] 12.V Verify wiring group
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
| 137-REQ-1.E1 | TS-137-E1 | 7.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-2.1 | TS-137-4 | 7.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-2.2 | TS-137-P2 | 3-4, 7.2 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-3.1 | TS-137-5 | 7.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-3.2 | TS-137-7 | 6.1, 9.2 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-3.3 | TS-137-7 | 6.1, 9.2 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-3.4 | TS-137-8 | 4.4 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-3.E1 | TS-137-E2 | 7.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-4.1 | TS-137-6 | 7.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-4.2 | TS-137-9 | 6.2 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-4.3 | TS-137-9 | 6.2, 6.3 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-5.1 | TS-137-3 | 3.2 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-5.2 | TS-137-8 | 3.2 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-5.3 | TS-137-8 | 4.1-4.4 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-5.4 | TS-137-7 | 3.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-5.E1 | TS-137-9 | 3-4, 6.1-6.3, 9-10 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-6.1 | TS-137-9 | 9.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-6.2 | TS-137-SMOKE-2 | 9.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-6.3 | TS-137-10 | 6.1, 9.2 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-6.4 | TS-137-9 | 6.2, 6.3, 10.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-6.E1 | TS-137-SMOKE-2 | 9.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-7.1 | TS-137-SMOKE-1 | 5, 7-8 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-7.2 | TS-137-E3 | 5.1, 8.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-7.3 | TS-137-E3 | 5.2, 8.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-7.4 | TS-137-E3 | 8.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-7.E1 | TS-137-E3 | 5.1 | tests/spec/test_137_legacy_removal.py |
| 137-REQ-8.1 | TS-137-SMOKE-1 | 11.1 | manual review |
| 137-REQ-8.2 | TS-137-SMOKE-1 | 11.1 | manual review |
| 137-REQ-8.3 | TS-137-SMOKE-1 | 11.2 | manual review |
| 137-REQ-8.4 | TS-137-SMOKE-1 | 11.2 | manual review |
