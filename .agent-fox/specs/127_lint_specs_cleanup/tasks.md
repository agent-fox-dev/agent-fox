# Implementation Plan: Lint-Specs Cleanup

<!-- AGENT INSTRUCTIONS
- Implement exactly ONE top-level task group per session
- Task group 1 writes failing tests from test_spec.md — all subsequent groups
  implement code to make those tests pass
- Follow the git-flow: feature branch from develop -> implement -> test -> merge to develop
- Update checkbox states as you go: [-] in progress, [x] complete
-->

## Overview

The plan is structured in five groups: (1) write failing tests, (2) remove
fix code, (3) add progress display, (4) update skill template and docs,
(5) wiring verification. The bulk of the work is in group 2 (subtractive)
and group 3 (additive).

## Test Commands

- Spec tests: `uv run pytest -q tests/unit/cli/test_lint_specs_cleanup.py tests/unit/spec/test_lint_cleanup.py`
- All tests: `uv run pytest -q`
- Linter: `uv run ruff check agent_fox/`

## Tasks

- [x] 1. Write failing spec tests
  - [x] 1.1 Create test file for CLI changes
    - Create `tests/unit/cli/test_lint_specs_cleanup.py`
    - Tests for TS-127-1 (--fix rejected), TS-127-E1 (error message)
    - Tests for TS-127-5 (no git operations in source)
    - Tests for TS-127-SMOKE-1, TS-127-SMOKE-2 (integration smoke tests)
    - _Test Spec: TS-127-1, TS-127-E1, TS-127-5, TS-127-SMOKE-1, TS-127-SMOKE-2_

  - [x] 1.2 Create test file for backing module changes
    - Create `tests/unit/spec/test_lint_cleanup.py`
    - Tests for TS-127-2 (no fix parameter), TS-127-3 (no fix_results)
    - Tests for TS-127-4 (fixers package deleted)
    - Tests for TS-127-6 (no fix dispatch in lint.py)
    - Tests for TS-127-7, TS-127-8 (progress callback)
    - Tests for TS-127-E2 (progress callback None edge case)
    - _Test Spec: TS-127-2, TS-127-3, TS-127-4, TS-127-6, TS-127-7, TS-127-8, TS-127-E2_

  - [x] 1.3 Create property and remaining unit tests
    - Tests for TS-127-P1 (no fixer imports in any tracked file)
    - Tests for TS-127-P2 (CLI rejects --fix)
    - Tests for TS-127-P3 (LintResult no fix_results)
    - Tests for TS-127-P4 (progress callback optional)
    - _Test Spec: TS-127-P1, TS-127-P2, TS-127-P3, TS-127-P4_

  - [x] 1.4 Create test for documentation
    - Tests for TS-127-9 (docs updated)
    - _Test Spec: TS-127-9_

  - [x] 1.V Verify task group 1
    - [x] All spec tests exist and are syntactically valid
    - [x] All spec tests FAIL (red) -- no implementation yet
    - [x] No linter warnings introduced: `uv run ruff check agent_fox/`

- [x] 2. Remove --fix flag and all fix code
  - [x] 2.1 Delete the fixers package
    - Delete the entire `agent_fox/spec/fixers/` directory (8 modules)
    - _Requirements: 127-REQ-1.4_

  - [x] 2.2 Remove fix code from backing module
    - Remove `fix` parameter from `run_lint_specs()` signature and body
    - Remove `fix_results` field from `LintResult`
    - Remove `_apply_ai_fixes`, `_apply_ai_fixes_async`, `_build_known_specs`
    - Remove `_MAX_REWRITE_BATCH`, `_MAX_UNTRACED_BATCH` constants
    - Remove fixer-related imports
    - _Requirements: 127-REQ-1.2, 127-REQ-1.3, 127-REQ-3.1, 127-REQ-3.2_

  - [x] 2.3 Remove fix code from CLI handler
    - Remove `--fix` Click option
    - Remove `fix` parameter from `lint_specs_cmd()`
    - Remove `_format_fix_summary`, `_git_current_branch`,
      `_create_fix_branch`, `_commit_fixes` functions
    - Remove `run_git_sync` import and `Counter`/`datetime` imports if unused
    - Remove fix-related output logic (git branch handling)
    - _Requirements: 127-REQ-1.1, 127-REQ-2.1, 127-REQ-2.2_

  - [x] 2.4 Remove fix-related tests
    - Delete `tests/integration/test_lint_fix.py`
    - Update `tests/unit/cli/test_backing_modules.py` to remove `fix=True`
      from `run_lint_specs()` calls
    - Remove any test that references `fix_results`, `apply_fixes`, or
      `--fix` flag
    - _Requirements: 127-REQ-1.1 through 127-REQ-3.2_

  - [x] 2.V Verify task group 2
    - [x] Spec tests TS-127-1 through TS-127-6, TS-127-E1, TS-127-P1 pass
    - [x] All existing tests still pass: `uv run pytest -q`
    - [x] No linter warnings introduced: `uv run ruff check agent_fox/`
    - [x] 127-REQ-1.1 through 127-REQ-3.2 acceptance criteria met

- [x] 3. Add progress display
  - [x] 3.1 Add progress callback to run_lint_specs
    - Add `progress_callback: Callable[[str], None] | None = None` parameter
    - Call callback at phase boundaries: discovery, validation, AI validation
    - Guard each call with `if progress_callback is not None:`
    - _Requirements: 127-REQ-4.2, 127-REQ-4.3, 127-REQ-4.E1_

  - [x] 3.2 Wire ProgressDisplay into CLI handler
    - Import `ProgressDisplay`, `create_theme`
    - Create progress display (suppressed in JSON/quiet mode)
    - Pass `progress.print_status` as progress callback
    - Wrap execution in try/finally with progress.stop()
    - _Requirements: 127-REQ-4.1, 127-REQ-4.4_

  - [x] 3.V Verify task group 3
    - [x] Spec tests TS-127-7, TS-127-8, TS-127-SMOKE-2 pass
    - [x] All existing tests still pass: `uv run pytest -q`
    - [x] No linter warnings introduced: `uv run ruff check agent_fox/`
    - [x] 127-REQ-4.1 through 127-REQ-4.E1 acceptance criteria met

- [x] 4. Update skill template and documentation
  - [x] 4.1 Update af-spec skill template
    - Edit `agent_fox/_templates/skills/af-spec` to add lint-specs validation
      step after all documents are generated
    - Mark manual-only checklist items with "(manual check)"
    - _Requirements: 127-REQ-5.1, 127-REQ-5.2, 127-REQ-5.3_

  - [x] 4.2 Update installed skill copy
    - Edit `.claude/skills/af-spec/SKILL.md` with same changes
    - _Requirements: 127-REQ-5.1, 127-REQ-5.2, 127-REQ-5.3_

  - [x] 4.3 Update CLI reference documentation
    - Remove `--fix` from lint-specs options table in `docs/cli-reference.md`
    - Remove all mentions of auto-fix, git branch creation, criteria rewriting
    - Add note about progress spinner display
    - _Requirements: 127-REQ-6.1, 127-REQ-6.2_

  - [x] 4.V Verify task group 4
    - [x] Spec test TS-127-9 passes
    - [x] All existing tests still pass: `uv run pytest -q`
    - [x] 127-REQ-5.1 through 127-REQ-6.2 acceptance criteria met

- [ ] 5. Wiring verification

  - [ ] 5.1 Trace every execution path from design.md end-to-end
    - For each path, verify the entry point actually calls the next function
      in the chain (read the calling code, do not assume)
    - Confirm no function in the chain is a stub
    - Every path must be live in production code
    - _Requirements: all_

  - [ ] 5.2 Verify return values propagate correctly
    - For every function in this spec that returns data consumed by a caller,
      confirm the caller receives and uses the return value
    - _Requirements: all_

  - [ ] 5.3 Run the integration smoke tests
    - All `TS-127-SMOKE-*` tests pass using real components
    - _Test Spec: TS-127-SMOKE-1, TS-127-SMOKE-2_

  - [ ] 5.4 Stub / dead-code audit
    - Search all files touched by this spec for dead code, stubs, TODO markers
    - Verify no orphaned imports from deleted fixers package
    - _Requirements: all_

  - [ ] 5.5 Cross-spec entry point verification
    - Verify `run_lint_specs()` is called from `lint_specs_cmd()`
    - Verify `ProgressDisplay` is wired in the CLI
    - _Requirements: all_

  - [ ] 5.V Verify wiring group
    - [ ] All smoke tests pass
    - [ ] No unjustified stubs remain in touched files
    - [ ] All execution paths from design.md are live
    - [ ] All existing tests still pass: `uv run pytest -q`

## Traceability

| Requirement | Test Spec Entry | Implemented By Task | Verified By Test |
|-------------|-----------------|---------------------|------------------|
| 127-REQ-1.1 | TS-127-1 | 2.3 | `test_lint_specs_cleanup.py::test_cli_rejects_fix` |
| 127-REQ-1.2 | TS-127-2 | 2.2 | `test_lint_cleanup.py::test_no_fix_parameter` |
| 127-REQ-1.3 | TS-127-3 | 2.2 | `test_lint_cleanup.py::test_no_fix_results` |
| 127-REQ-1.4 | TS-127-4 | 2.1 | `test_lint_cleanup.py::test_fixers_deleted` |
| 127-REQ-1.E1 | TS-127-E1 | 2.3 | `test_lint_specs_cleanup.py::test_fix_error_message` |
| 127-REQ-2.1 | TS-127-5 | 2.3 | `test_lint_specs_cleanup.py::test_no_git_operations` |
| 127-REQ-2.2 | TS-127-5 | 2.3 | `test_lint_specs_cleanup.py::test_no_git_operations` |
| 127-REQ-3.1 | TS-127-6 | 2.2 | `test_lint_cleanup.py::test_no_fix_dispatch` |
| 127-REQ-3.2 | TS-127-6 | 2.2 | `test_lint_cleanup.py::test_no_fix_dispatch` |
| 127-REQ-4.1 | TS-127-SMOKE-2 | 3.2 | `test_lint_specs_cleanup.py::test_progress_display` |
| 127-REQ-4.2 | TS-127-7 | 3.1 | `test_lint_cleanup.py::test_progress_callback` |
| 127-REQ-4.3 | TS-127-7 | 3.1 | `test_lint_cleanup.py::test_progress_callback` |
| 127-REQ-4.4 | TS-127-SMOKE-2 | 3.2 | `test_lint_specs_cleanup.py::test_progress_display` |
| 127-REQ-4.E1 | TS-127-8 | 3.1 | `test_lint_cleanup.py::test_progress_none_safe` |
| 127-REQ-5.1 | TS-127-9 | 4.1, 4.2 | `test_lint_specs_cleanup.py::test_docs_updated` |
| 127-REQ-5.2 | TS-127-9 | 4.1, 4.2 | `test_lint_specs_cleanup.py::test_docs_updated` |
| 127-REQ-5.3 | TS-127-9 | 4.1, 4.2 | `test_lint_specs_cleanup.py::test_docs_updated` |
| 127-REQ-6.1 | TS-127-9 | 4.3 | `test_lint_specs_cleanup.py::test_docs_updated` |
| 127-REQ-6.2 | TS-127-9 | 4.3 | `test_lint_specs_cleanup.py::test_docs_updated` |

## Notes

- The biggest risk is missing a reference to the fixers package in some
  obscure test or import. The TS-127-P1 property test (grep all tracked files)
  catches this comprehensively.
- The progress display follows the exact same pattern as `cli/code.py` and
  `cli/nightshift.py` — create theme, instantiate ProgressDisplay, start/stop
  in try/finally, pass callback.
- The af-spec skill template changes are text-only (markdown editing), not
  code changes.
