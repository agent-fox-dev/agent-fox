# Implementation Plan: v1.2 Skill Template and Validation Migration

<!-- AGENT INSTRUCTIONS
- Implement exactly ONE top-level task group per session
- Task group 1 writes failing tests from test_spec.md — all subsequent groups
  implement code to make those tests pass
- Follow the git-flow: feature branch from develop -> implement -> test -> merge to develop
- Update checkbox states as you go: [-] in progress, [x] complete
-->

## Overview

This plan updates lint-specs to route v1.2 specs to `afspec.validate()` and
rewrites the af-spec skill template for v1.2 artifact production. Four task
groups: write failing tests, update lint-specs routing, update skill template,
and wiring verification.

## Test Commands

- Spec tests: `uv run pytest -q tests/spec/test_135_v12_skill_validation.py`
- All tests: `uv run pytest -q`
- Linter: `uv run ruff check`

## Tasks

- [x] 1. Write failing spec tests
  - [x] 1.1 Create test file structure
    - Create `tests/spec/test_135_v12_skill_validation.py`
    - Create test fixtures with v1.2 spec directories (valid JSON artifacts)
    - Create test fixtures with v1 markdown spec directories
    - Create mock/patch helpers for `afspec.validate` and `ValidationError`
    - _Test Spec: TS-135-1 through TS-135-10_

  - [x] 1.2 Translate acceptance-criterion tests
    - Test v1.2 routed to afspec.validate (TS-135-1)
    - Test v1 routed to custom validators (TS-135-2)
    - Test mixed format validation (TS-135-3)
    - Test ValidationError to Finding mapping (TS-135-4)
    - Test unknown severity defaults to error (TS-135-5)
    - Test CLI flags unchanged (TS-135-6)
    - Test skill template references v1.2 artifacts (TS-135-7)
    - Test skill template references v1.2 ID formats (TS-135-8)
    - Test skill template describes EARS JSON structure (TS-135-9)
    - Test skill template describes tasks JSON structure (TS-135-10)
    - _Test Spec: TS-135-1 through TS-135-10_

  - [x] 1.3 Translate edge-case tests
    - Test afspec.validate exception handling (TS-135-E1)
    - Test empty validation result (TS-135-E2)
    - Test unknown severity mapping (TS-135-E3)
    - _Test Spec: TS-135-E1 through TS-135-E3_

  - [x] 1.4 Translate property tests
    - Test Finding mapping preserves all fields (TS-135-P1)
    - Test format routing is exhaustive (TS-135-P2)
    - _Test Spec: TS-135-P1, TS-135-P2_

  - [x] 1.V Verify task group 1
    - [x] All spec tests exist and are syntactically valid
    - [x] All spec tests FAIL (red) -- no implementation yet
    - [x] No linter warnings introduced: `uv run ruff check`

- [x] 2. Update lint-specs format routing
  - [x] 2.1 Add _map_afspec_findings function to lint.py
    - Implement `_map_afspec_findings(spec_name, errors)` that maps each
      `afspec.ValidationError` to a `Finding`
    - Handle unknown severity by defaulting to "error"
    - _Requirements: 2.1, 2.2_

  - [x] 2.2 Add _validate_v12_spec function to lint.py
    - Implement `_validate_v12_spec(spec)` that calls `afspec.validate()`
      and maps the result via `_map_afspec_findings()`
    - Catch exceptions and return a single error Finding with rule
      `afspec-error`
    - _Requirements: 1.1, 1.E1_

  - [x] 2.3 Update run_lint_specs to partition by format
    - Import `SpecFormat` from discovery module
    - Partition discovered specs into v1 and v1.2 lists by `spec.format`
    - Route v1 specs to `validate_specs()`, v1.2 specs to
      `_validate_v12_spec()`
    - Merge findings from both validators and sort
    - _Requirements: 1.1, 1.2, 1.3_

  - [x] 2.4 Update _is_spec_implemented for v1.2 format
    - For v1.2 specs, check `tasks.json` instead of `tasks.md`
    - Use `afspec.load_spec()` to parse tasks if available
    - _Requirements: 3.2_

  - [x] 2.V Verify task group 2
    - [x] Spec tests for routing pass: TS-135-1 through TS-135-5, TS-135-E1
      through TS-135-E3
    - [x] All existing tests still pass: `uv run pytest -q`
    - [x] No linter warnings introduced: `uv run ruff check`
    - [x] Requirements 1.1-2.2 acceptance criteria met

- [x] 3. Update af-spec skill template
  - [x] 3.1 Update artifact references
    - Replace `requirements.md` with `requirements.json` in output instructions
    - Replace `design.md` with `architecture.md` (optional) in output
      instructions
    - Replace `test_spec.md` with `test_spec.json` in output instructions
    - Replace `tasks.md` with `tasks.json` in output instructions
    - Keep `prd.md` but add YAML frontmatter instructions
    - _Requirements: 4.1_

  - [x] 3.2 Update ID format references
    - Replace `[{NN}-REQ-{N}.{C}]` with `{spec_id}-REQ-{N}` format
    - Add `{spec_id}-PROP-{N}` for property references
    - Add `{spec_id}-TS-{N}` for test case references
    - Update all examples and templates throughout the skill file
    - _Requirements: 4.2_

  - [x] 3.3 Add EARS JSON structure documentation
    - Document the discriminated union on `ears_pattern`
    - Document each pattern type (ubiquitous, event_driven, complex_event,
      state_driven, unwanted, optional) with their fields
    - Document the `action` field (SHALL clause)
    - Replace markdown EARS examples with JSON examples
    - _Requirements: 6.1, 4.3_

  - [x] 3.4 Add tasks JSON structure documentation
    - Document task groups with subtasks array
    - Document state machine (not_started, in_progress, completed, queued,
      optional) replacing checkboxes
    - Document dependency and traceability fields in JSON format
    - _Requirements: 6.2, 4.3_

  - [x] 3.5 Update validation step
    - Update Step 7 to reference v1.2 validation
    - Add reference to `afspec`'s format specification as authoritative source
    - _Requirements: 5.1, 5.2_

  - [x] 3.V Verify task group 3
    - [x] Skill template content tests pass: TS-135-7 through TS-135-10,
      TS-135-SMOKE-2
    - [x] All existing tests still pass: `uv run pytest -q`
    - [x] No linter warnings introduced: `uv run ruff check`
    - [x] Requirements 4.1-6.2 acceptance criteria met

- [x] 4. Wiring verification
  - [x] 4.1 Trace every execution path from design.md end-to-end
    - For each path (1-3), verify the entry point actually calls the next
      function in the chain (read the calling code, do not assume)
    - Confirm no function in the chain is a stub (`return []`, `return None`,
      `pass`, `raise NotImplementedError`) that was never replaced
    - Every path must be live in production code -- errata or deferrals do
      not satisfy this check
    - _Requirements: all_

  - [x] 4.2 Verify return values propagate correctly
    - For `_map_afspec_findings`: confirm callers use the returned
      `list[Finding]`
    - For `_validate_v12_spec`: confirm `run_lint_specs` uses the returned
      findings
    - Grep for callers of each function; confirm none discards the return
    - _Requirements: all_

  - [x] 4.3 Run the integration smoke tests
    - TS-135-SMOKE-1 passes (mixed format lint end-to-end)
    - TS-135-SMOKE-2 passes (skill template content validation)
    - _Test Spec: TS-135-SMOKE-1, TS-135-SMOKE-2_

  - [x] 4.4 Stub / dead-code audit
    - Search all files touched by this spec for: `return []`, `return None`
      on non-Optional returns, `pass` in non-abstract methods, `# TODO`,
      `# stub`, `override point`, `NotImplementedError`
    - Each hit must be either: (a) justified with a comment explaining why it
      is intentional, or (b) replaced with a real implementation
    - Document any intentional stubs here with rationale
    - _Requirements: all_

  - [x] 4.5 Cross-spec entry point verification
    - Verify that `afspec.validate()` is callable from agent-fox (dependency
      from spec 132 is live)
    - Verify that `SpecInfo.format` field is populated by discovery (spec 132)
    - Verify that the skill template is loadable by the skills framework
    - _Requirements: all_

  - [x] 4.V Verify wiring group
    - [x] All smoke tests pass
    - [x] No unjustified stubs remain in touched files
    - [x] All execution paths from design.md are live (traceable in code)
    - [x] All cross-spec entry points are called from production code
    - [x] All existing tests still pass: `uv run pytest -q`

## Traceability

| Requirement | Test Spec Entry | Implemented By Task | Verified By Test |
|-------------|-----------------|---------------------|------------------|
| 135-REQ-1.1 | TS-135-1 | 2.3 | tests/spec/test_135_v12_skill_validation.py::test_v12_routed_to_afspec |
| 135-REQ-1.2 | TS-135-2 | 2.3 | tests/spec/test_135_v12_skill_validation.py::test_v1_routed_to_custom |
| 135-REQ-1.3 | TS-135-3 | 2.3 | tests/spec/test_135_v12_skill_validation.py::test_mixed_format_validation |
| 135-REQ-1.E1 | TS-135-E1 | 2.2 | tests/spec/test_135_v12_skill_validation.py::test_afspec_validate_exception |
| 135-REQ-2.1 | TS-135-4 | 2.1 | tests/spec/test_135_v12_skill_validation.py::test_validation_error_mapping |
| 135-REQ-2.2 | TS-135-5 | 2.1 | tests/spec/test_135_v12_skill_validation.py::test_unknown_severity_defaults |
| 135-REQ-2.E1 | TS-135-E2 | 2.1 | tests/spec/test_135_v12_skill_validation.py::test_empty_validation_result |
| 135-REQ-3.1 | TS-135-6 | 2.3 | tests/spec/test_135_v12_skill_validation.py::test_cli_flags_unchanged |
| 135-REQ-3.2 | TS-135-3 | 2.3 | tests/spec/test_135_v12_skill_validation.py::test_mixed_format_validation |
| 135-REQ-4.1 | TS-135-7 | 3.1 | tests/spec/test_135_v12_skill_validation.py::test_skill_template_v12_artifacts |
| 135-REQ-4.2 | TS-135-8 | 3.2 | tests/spec/test_135_v12_skill_validation.py::test_skill_template_v12_ids |
| 135-REQ-4.3 | TS-135-9 | 3.3 | tests/spec/test_135_v12_skill_validation.py::test_skill_template_ears_json |
| 135-REQ-5.1 | TS-135-SMOKE-2 | 3.5 | tests/spec/test_135_v12_skill_validation.py::test_skill_template_validation_step |
| 135-REQ-5.2 | TS-135-SMOKE-2 | 3.5 | tests/spec/test_135_v12_skill_validation.py::test_skill_template_afspec_reference |
| 135-REQ-6.1 | TS-135-9 | 3.3 | tests/spec/test_135_v12_skill_validation.py::test_skill_template_ears_json |
| 135-REQ-6.2 | TS-135-10 | 3.4 | tests/spec/test_135_v12_skill_validation.py::test_skill_template_tasks_json |
| Property 1 | TS-135-P2 | 2.3 | tests/spec/test_135_v12_skill_validation.py::test_format_routing_exhaustive |
| Property 2 | TS-135-P1 | 2.1 | tests/spec/test_135_v12_skill_validation.py::test_finding_mapping_preserves_fields |
