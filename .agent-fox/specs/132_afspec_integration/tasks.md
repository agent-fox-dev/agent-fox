# Implementation Plan: afspec Library Integration

<!-- AGENT INSTRUCTIONS
- Implement exactly ONE top-level task group per session
- Task group 1 writes failing tests from test_spec.md — all subsequent groups
  implement code to make those tests pass
- Follow the git-flow: feature branch from develop -> implement -> test -> merge to develop
- Update checkbox states as you go: [-] in progress, [x] complete
-->

## Overview

This plan adds the `afspec` dependency and updates spec discovery to detect
v1.2 format. Three task groups: write tests, implement changes, verify wiring.

## Test Commands

- Spec tests: `uv run pytest -q tests/spec/test_132_afspec_integration.py`
- All tests: `uv run pytest -q`
- Linter: `uv run ruff check`

## Tasks

- [x] 1. Write failing spec tests
  - [x] 1.1 Create test file structure
    - Create `tests/spec/test_132_afspec_integration.py`
    - Create test fixtures with v1.2 spec directories (valid JSON artifacts)
    - Create test fixtures with v1 spec directories (markdown artifacts)
    - _Test Spec: TS-132-1 through TS-132-9_

  - [x] 1.2 Translate acceptance-criterion tests
    - Test afspec import (TS-132-1)
    - Test afspec load (TS-132-2)
    - Test SpecFormat enum values (TS-132-3)
    - Test SpecInfo format field (TS-132-4)
    - Test format detection for v1.2 (TS-132-5)
    - Test format detection for v1 (TS-132-6)
    - Test discovery excludes v1 (TS-132-7)
    - Test has_tasks checks tasks.json (TS-132-8)
    - Test render_combined output (TS-132-9)
    - _Test Spec: TS-132-1 through TS-132-9_

  - [x] 1.3 Translate edge-case tests
    - Test folder with no requirements file skipped (TS-132-E1)
    - Test JSON precedence over markdown (TS-132-E2)
    - Test malformed JSON raises LoadError (TS-132-E3)
    - _Test Spec: TS-132-E1 through TS-132-E3_

  - [x] 1.4 Translate property tests
    - Test format detection determinism (TS-132-P1)
    - Test discovery returns only v1.2 (TS-132-P2)
    - _Test Spec: TS-132-P1, TS-132-P2_

  - [x] 1.V Verify task group 1
    - [x] All spec tests exist and are syntactically valid
    - [x] All spec tests FAIL (red) — no implementation yet
    - [x] No linter warnings introduced: `uv run ruff check`

- [x] 2. Add afspec dependency and update discovery
  - [x] 2.1 Add afspec to pyproject.toml
    - Add `afspec = {path = "../af-core/packages/afspec"}` to dependencies
    - Run `uv sync` to install
    - Verify `import afspec` works
    - _Requirements: 1.1, 1.2_

  - [x] 2.2 Add SpecFormat enum to discovery.py
    - Add `SpecFormat` enum with `V1_MARKDOWN` and `V1_2_JSON` values
    - Add `format` field to `SpecInfo` dataclass
    - _Requirements: 2.1, 2.2_

  - [x] 2.3 Implement format detection
    - Add `_detect_format(spec_dir: Path) -> SpecFormat` function
    - Detection based on presence of `requirements.json`
    - _Requirements: 3.1, 3.2_

  - [x] 2.4 Update discover_specs to filter by format
    - Call `_detect_format()` for each candidate folder
    - Skip `V1_MARKDOWN` folders
    - Update `has_tasks` to check `tasks.json` for v1.2 specs
    - Update `has_prd` to still check `prd.md`
    - _Requirements: 3.3, 3.4_

  - [x] 2.V Verify task group 2
    - [x] Spec tests pass: `uv run pytest -q tests/spec/test_132_afspec_integration.py`
    - [x] All existing tests still pass: `uv run pytest -q`
    - [x] No linter warnings introduced: `uv run ruff check`
    - [x] Requirements 1.1-3.4 acceptance criteria met

- [x] 3. Wiring verification
  - [x] 3.1 Trace every execution path from design.md end-to-end
    - Verify discover_specs actually calls _detect_format
    - Verify the result list excludes v1 folders
    - Verify afspec.load_spec can load a spec found by discover_specs
    - _Requirements: all_

  - [x] 3.2 Verify return values propagate correctly
    - discover_specs returns list[SpecInfo] with format field populated
    - afspec.load_spec returns Spec with all artifacts
    - _Requirements: all_

  - [x] 3.3 Run the integration smoke tests
    - TS-132-SMOKE-1 passes using real components
    - _Test Spec: TS-132-SMOKE-1_

  - [x] 3.4 Stub / dead-code audit
    - Search all files touched for: return [], return None, pass, # TODO, NotImplementedError
    - Each hit must be justified or replaced
    - _Requirements: all_

  - [x] 3.V Verify wiring group
    - [x] All smoke tests pass
    - [x] No unjustified stubs remain in touched files
    - [x] All execution paths from design.md are live
    - [x] All existing tests still pass: `uv run pytest -q`

## Traceability

| Requirement | Test Spec Entry | Implemented By Task | Verified By Test |
|-------------|-----------------|---------------------|------------------|
| 132-REQ-1.2 | TS-132-1 | 2.1 | tests/spec/test_132_afspec_integration.py::test_afspec_importable |
| 132-REQ-1.3 | TS-132-2 | 2.1 | tests/spec/test_132_afspec_integration.py::test_afspec_load_spec |
| 132-REQ-2.1 | TS-132-3 | 2.2 | tests/spec/test_132_afspec_integration.py::test_spec_format_enum |
| 132-REQ-2.2 | TS-132-4 | 2.2 | tests/spec/test_132_afspec_integration.py::test_spec_info_format_field |
| 132-REQ-3.1 | TS-132-5 | 2.3 | tests/spec/test_132_afspec_integration.py::test_detect_format_json |
| 132-REQ-3.2 | TS-132-6 | 2.3 | tests/spec/test_132_afspec_integration.py::test_detect_format_markdown |
| 132-REQ-3.3 | TS-132-7 | 2.4 | tests/spec/test_132_afspec_integration.py::test_discover_excludes_v1 |
| 132-REQ-3.4 | TS-132-8 | 2.4 | tests/spec/test_132_afspec_integration.py::test_has_tasks_json |
| 132-REQ-4.1 | TS-132-2 | 2.1 | tests/spec/test_132_afspec_integration.py::test_afspec_load_spec |
| 132-REQ-4.2 | TS-132-9 | 2.1 | tests/spec/test_132_afspec_integration.py::test_render_combined |
| 132-REQ-2.E1 | TS-132-E1 | 2.4 | tests/spec/test_132_afspec_integration.py::test_no_requirements_skipped |
| 132-REQ-3.E1 | TS-132-E2 | 2.3 | tests/spec/test_132_afspec_integration.py::test_json_precedence |
| 132-REQ-4.E1 | TS-132-E3 | 2.1 | tests/spec/test_132_afspec_integration.py::test_malformed_json_error |
| Property 1 | TS-132-P1 | 2.3 | tests/spec/test_132_afspec_integration.py::test_format_detection_determinism |
| Property 2 | TS-132-P2 | 2.4 | tests/spec/test_132_afspec_integration.py::test_discovery_only_v12 |
