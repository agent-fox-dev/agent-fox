# Implementation Plan: Night-Shift Prior Attempt Context

<!-- AGENT INSTRUCTIONS
- Implement exactly ONE top-level task group per session
- Task group 1 writes failing tests from test_spec.md — all subsequent groups
  implement code to make those tests pass
- Follow the git-flow: feature branch from develop -> implement -> test -> merge to develop
- Update checkbox states as you go: [-] in progress, [x] complete
-->

## Overview

Small, focused spec: one new module (`prior_attempts.py`) with two functions
(query + format), and two call-site changes in `fix_pipeline.py`. Task group 1
writes failing tests, group 2 implements the new module, group 3 wires it into
the pipeline, group 4 verifies end-to-end.

## Test Commands

- Spec tests: `uv run pytest -q tests/unit/nightshift/test_prior_attempts.py`
- All tests: `uv run pytest -q`
- Linter: `uv run ruff check agent_fox/`

## Tasks

- [x] 1. Write failing spec tests
  - [x] 1.1 Create test file
    - Create `tests/unit/nightshift/test_prior_attempts.py`
    - Set up DuckDB test fixtures (in-memory DB with session_outcomes table)
    - _Test Spec: TS-128-1 through TS-128-9_

  - [x] 1.2 Translate acceptance-criterion tests
    - TS-128-1: query returns prior sessions, excludes current run
    - TS-128-2: groups by run, returns last session per run
    - TS-128-3: respects max_results limit
    - TS-128-4: PriorAttempt dataclass fields
    - TS-128-5: format produces markdown block
    - TS-128-6: format truncates long error messages
    - TS-128-7: context injected into task prompt
    - TS-128-8: empty context leaves prompt unchanged
    - TS-128-9: pipeline wires query into process_issue
    - _Test Spec: TS-128-1 through TS-128-9_

  - [x] 1.3 Translate edge-case and property tests
    - TS-128-E1: no prior sessions returns empty list
    - TS-128-E2: database query failure returns empty list
    - TS-128-E3: format with empty list returns empty string
    - TS-128-P1: current run always excluded
    - TS-128-P2: one entry per run
    - TS-128-P3: result bounded by max_results
    - TS-128-P4: empty in, empty out
    - TS-128-P5: fail-open on query error
    - _Test Spec: TS-128-E1, TS-128-E2, TS-128-E3, TS-128-P1, TS-128-P2, TS-128-P3, TS-128-P4, TS-128-P5_

  - [x] 1.4 Write integration smoke test
    - TS-128-SMOKE-1: full pipeline with prior attempts in prompt
    - _Test Spec: TS-128-SMOKE-1_

  - [x] 1.V Verify task group 1
    - [x] All spec tests exist and are syntactically valid
    - [x] All spec tests FAIL (red) -- no implementation yet
    - [x] No linter warnings introduced: `uv run ruff check agent_fox/`

- [ ] 2. Implement query and format functions
  - [ ] 2.1 Create `agent_fox/nightshift/prior_attempts.py`
    - Define `PriorAttempt` dataclass
    - Implement `query_prior_attempts(conn, spec_name, current_run_id, max_results=3)`
    - SQL: CTE with ROW_NUMBER() OVER (PARTITION BY run_id ORDER BY created_at DESC)
    - Filter: spec_name match, archetype='coder', run_id != current
    - Wrap in try/except, log warning on failure, return []
    - _Requirements: 128-REQ-1.1, 128-REQ-1.2, 128-REQ-1.3, 128-REQ-1.E1, 128-REQ-1.E2_

  - [ ] 2.2 Implement `format_prior_attempts(attempts)`
    - Return empty string for empty list
    - Render `## Prior Fix Attempts` heading
    - Numbered entries with date, status, model, truncated error
    - Truncate error_message to 500 chars with `...` marker
    - _Requirements: 128-REQ-2.1, 128-REQ-2.2, 128-REQ-2.3_

  - [ ] 2.V Verify task group 2
    - [ ] Spec tests TS-128-1 through TS-128-6, TS-128-E1 through TS-128-E3,
          TS-128-P1 through TS-128-P5 pass
    - [ ] All existing tests still pass: `uv run pytest -q`
    - [ ] No linter warnings introduced: `uv run ruff check agent_fox/`

- [ ] 3. Wire into fix pipeline
  - [ ] 3.1 Add `prior_context` parameter to `_build_coder_prompt()`
    - Add `prior_context: str = ""` parameter
    - Prepend non-empty prior_context to task_prompt before issue description
    - _Requirements: 128-REQ-3.1, 128-REQ-3.2_

  - [ ] 3.2 Call query in `process_issue()` flow
    - Import `query_prior_attempts`, `format_prior_attempts`
    - Call before entering coder-reviewer loop
    - Pass `self._conn`, `spec_name`, `self._run_id`
    - Thread `prior_context` string through to `_build_coder_prompt()`
    - _Requirements: 128-REQ-4.1, 128-REQ-4.2_

  - [ ] 3.V Verify task group 3
    - [ ] Spec tests TS-128-7 through TS-128-9, TS-128-SMOKE-1 pass
    - [ ] All existing tests still pass: `uv run pytest -q`
    - [ ] No linter warnings introduced: `uv run ruff check agent_fox/`

- [ ] 4. Wiring verification

  - [ ] 4.1 Trace every execution path from design.md end-to-end
    - Path 1: process_issue -> query_prior_attempts -> format -> _build_coder_prompt
    - Path 2: process_issue -> query_prior_attempts returns [] -> format returns "" -> prompt unchanged
    - Path 3: process_issue -> query_prior_attempts catches error -> returns [] -> prompt unchanged
    - _Requirements: all_

  - [ ] 4.2 Verify return values propagate correctly
    - query_prior_attempts returns list[PriorAttempt] consumed by format_prior_attempts
    - format_prior_attempts returns str consumed by _build_coder_prompt
    - _Requirements: all_

  - [ ] 4.3 Run the integration smoke tests
    - TS-128-SMOKE-1 passes with real DuckDB and real pipeline code
    - _Test Spec: TS-128-SMOKE-1_

  - [ ] 4.4 Stub / dead-code audit
    - Verify no stubs in prior_attempts.py
    - Verify no orphaned imports
    - _Requirements: all_

  - [ ] 4.5 Cross-spec entry point verification
    - Verify query_prior_attempts is called from fix_pipeline.py process_issue()
    - Verify format_prior_attempts is called from fix_pipeline.py
    - _Requirements: all_

  - [ ] 4.V Verify wiring group
    - [ ] All smoke tests pass
    - [ ] No unjustified stubs remain in touched files
    - [ ] All execution paths from design.md are live
    - [ ] All cross-spec entry points are called from production code
    - [ ] All existing tests still pass: `uv run pytest -q`

## Traceability

| Requirement | Test Spec Entry | Implemented By Task | Verified By Test |
|-------------|-----------------|---------------------|------------------|
| 128-REQ-1.1 | TS-128-1 | 2.1 | `test_prior_attempts.py::test_query_returns_prior_sessions` |
| 128-REQ-1.2 | TS-128-2, TS-128-3 | 2.1 | `test_prior_attempts.py::test_query_groups_by_run` |
| 128-REQ-1.3 | TS-128-4 | 2.1 | `test_prior_attempts.py::test_prior_attempt_fields` |
| 128-REQ-1.E1 | TS-128-E1 | 2.1 | `test_prior_attempts.py::test_no_prior_sessions` |
| 128-REQ-1.E2 | TS-128-E2 | 2.1 | `test_prior_attempts.py::test_query_failure` |
| 128-REQ-2.1 | TS-128-5 | 2.2 | `test_prior_attempts.py::test_format_markdown` |
| 128-REQ-2.2 | TS-128-5, TS-128-6 | 2.2 | `test_prior_attempts.py::test_format_truncation` |
| 128-REQ-2.3 | TS-128-E3 | 2.2 | `test_prior_attempts.py::test_format_empty` |
| 128-REQ-3.1 | TS-128-7 | 3.1 | `test_prior_attempts.py::test_context_in_prompt` |
| 128-REQ-3.2 | TS-128-8 | 3.1 | `test_prior_attempts.py::test_empty_context_unchanged` |
| 128-REQ-4.1 | TS-128-9 | 3.2 | `test_prior_attempts.py::test_pipeline_wiring` |
| 128-REQ-4.2 | TS-128-9 | 3.2 | `test_prior_attempts.py::test_pipeline_wiring` |

## Notes

- The `session_outcomes` table already exists and is populated by the fix
  pipeline. No schema migration needed.
- The `spec_name` convention for fix sessions is `fix-issue-{issue_number}`,
  established in `fix_pipeline.py` line 239.
- The `conn` parameter already flows from the CLI handler through to
  `FixPipeline`, so no new plumbing is needed for the DB connection.
- The query uses a CTE with `ROW_NUMBER()` window function, which DuckDB
  supports natively.
