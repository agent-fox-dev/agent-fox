# Implementation Plan: Config Dead Code Removal

<!-- AGENT INSTRUCTIONS
- Implement exactly ONE top-level task group per session
- Task group 1 writes failing tests from test_spec.md — all subsequent groups
  implement code to make those tests pass
- Follow the git-flow: feature branch from develop -> implement -> test -> merge to develop
- Update checkbox states as you go: [-] in progress, [x] complete
-->

## Overview

Pure deletion spec — three task groups total. Group 1 writes spec tests that
will initially fail (asserting field absence when fields still exist). Group 2
performs all deletions across source, config_gen, audit, docs, and existing
tests. Group 3 is wiring verification.

## Test Commands

- Spec tests: `uv run pytest -q tests/unit/core/test_config_dead_code_removal.py`
- Unit tests: `uv run pytest -q tests/unit/core/`
- Property tests: `uv run pytest -q tests/property/core/`
- All tests: `uv run pytest -q`
- Linter: `uv run ruff check agent_fox/ tests/`

## Tasks

- [x] 1. Write failing spec tests
  - [x] 1.1 Create `tests/unit/core/test_config_dead_code_removal.py`
    - Unit tests TS-130-1 through TS-130-13 (field absence, metadata absence, template checks)
    - Edge case tests TS-130-E1 through TS-130-E4 (old configs parse silently)
    - Integration smoke tests TS-130-SMOKE-1 and TS-130-SMOKE-2
    - _Test Spec: TS-130-1 through TS-130-13, TS-130-E1 through TS-130-E4, TS-130-SMOKE-1, TS-130-SMOKE-2_

  - [x] 1.2 Create `tests/property/core/test_config_dead_code_props.py`
    - Property test TS-130-P1 (silent ignore of old config keys)
    - Property test TS-130-P2 (metadata keys match real fields)
    - _Test Spec: TS-130-P1, TS-130-P2_

  - [x] 1.V Verify task group 1
    - [x] All spec tests exist and are syntactically valid
    - [x] All spec tests FAIL (red) — no implementation yet
    - [x] No linter warnings introduced: `uv run ruff check tests/unit/core/test_config_dead_code_removal.py tests/property/core/test_config_dead_code_props.py`

- [x] 2. Remove dead config code, metadata, and documentation
  - [x] 2.1 Remove fields from `agent_fox/core/config.py`
    - Delete `quality_gate` and `quality_gate_timeout` fields from `OrchestratorConfig`
    - Delete entire `ModelConfig` class (lines 180–203)
    - Delete `models` field from `AgentFoxConfig`
    - Delete `_handle_archetype_config_keys` model validator from `ArchetypesConfig` (lines ~500–541)
    - _Requirements: 130-REQ-1.1, 130-REQ-1.2, 130-REQ-2.1, 130-REQ-2.2, 130-REQ-3.1, 130-REQ-3.2, 130-REQ-3.3_

  - [x] 2.2 Clean up `agent_fox/core/config_gen.py`
    - Delete phantom `_BOUNDS_MAP` entries for `training_threshold`, `accuracy_threshold`, `retrain_interval`
    - Delete `("orchestrator", "quality_gate")` from `_PROMOTED_DEFAULTS`
    - Delete `("orchestrator", "quality_gate")` from `_PROMOTED_DEFAULTS_OVERRIDES`
    - Delete `_SCHEMA_DEPRECATED_FIELDS` set entirely (no remaining entries)
    - Remove all references to `_SCHEMA_DEPRECATED_FIELDS` in `_collect_active_fields` and elsewhere
    - Delete `_DEFAULT_DESCRIPTIONS` entries for `ModelConfig.*` and `OrchestratorConfig.quality_gate`
    - Remove `"models"` from `_VISIBLE_SECTIONS`
    - Fix `drift_review_block_threshold` bounds: `">=1"` → `">=1 or None"`
    - _Requirements: 130-REQ-1.3, 130-REQ-1.4, 130-REQ-1.5, 130-REQ-2.3, 130-REQ-2.4, 130-REQ-2.5, 130-REQ-4.1, 130-REQ-4.2, 130-REQ-5.1_

  - [x] 2.3 Remove `QUALITY_GATE_RESULT` from `agent_fox/knowledge/audit.py`
    - Delete the enum member
    - _Requirements: 130-REQ-6.1_

  - [x] 2.4 Update `docs/config-reference.md`
    - Remove `quality_gate` and `quality_gate_timeout` rows from `## orchestrator` table
    - Remove `quality_gate` from the TOML example in `## orchestrator`
    - Remove entire `## models` section (lines ~136–163)
    - Remove `- [models](#models)` from table of contents
    - Remove "Obsolete keys" paragraph from `## archetypes` (lines ~323–326)
    - Remove `# replaces deprecated [models] coding` comment from `## archetypes.overrides` example
    - Update "General behavior" paragraph about `[archetypes]` rejecting unknown keys — change to say it silently ignores unknown keys like other sections
    - _Requirements: 130-REQ-7.1, 130-REQ-7.2, 130-REQ-7.3, 130-REQ-7.4, 130-REQ-7.5_

  - [x] 2.5 Update existing tests that reference removed items
    - `tests/unit/core/test_config_simplification.py`: Remove `("orchestrator", "quality_gate")` from `_EXPECTED_PROMOTED_FIELDS`, remove `test_quality_gate_active_in_template` and `test_quality_gate_line_is_not_commented` tests
    - `tests/property/core/test_config_props.py`: Remove assertions for `config.models.coding` and `config.models.memory_extraction`
    - `tests/unit/routing/test_simplify_routing.py`: Remove `test_prediction_config_fields_removed` (now vacuously true — fields and phantom metadata are both gone)
    - `tests/unit/core/test_reviewer_consolidation.py`: Remove `TestOldConfigKeyRejected` class (tests that old keys raise errors — now they're silently ignored)
    - `tests/unit/nightshift/test_triage_migration.py`: Remove `TestOldTriageConfigKey` class (tests that triage key doesn't fail — still true but validator is gone)
    - `tests/unit/knowledge/test_audit.py`: Remove `"quality_gate.result"` from expected event type list
    - `tests/property/engine/test_config_reload_props.py`: Remove `"quality_gate"` and `"quality_gate_timeout"` from any field lists
    - `tests/property/nightshift/test_cost_tracking_props.py`: Remove `"quality_gate"` from any field lists
    - _Requirements: 130-REQ-8.1_

  - [x] 2.V Verify task group 2
    - [x] Spec tests for this group pass: `uv run pytest -q tests/unit/core/test_config_dead_code_removal.py tests/property/core/test_config_dead_code_props.py`
    - [x] All existing tests still pass: `make check`
    - [x] No linter warnings introduced: `uv run ruff check agent_fox/ tests/`
    - [x] Requirements 130-REQ-1.* through 130-REQ-8.1 acceptance criteria met

- [ ] 3. Wiring verification

  - [ ] 3.1 Trace every execution path from design.md end-to-end
    - For each path, verify the entry point actually calls the next function
      in the chain (read the calling code, do not assume)
    - Confirm no function in the chain is a stub that was never replaced
    - Every path must be live in production code
    - _Requirements: all_

  - [ ] 3.2 Verify return values propagate correctly
    - For every function in this spec that returns data consumed by a caller,
      confirm the caller receives and uses the return value
    - _Requirements: all_

  - [ ] 3.3 Run the integration smoke tests
    - All `TS-130-SMOKE-*` tests pass using real components (no stub bypass)
    - _Test Spec: TS-130-SMOKE-1, TS-130-SMOKE-2_

  - [ ] 3.4 Stub / dead-code audit
    - Search all files touched by this spec for: `return []`, `return None`
      on non-Optional returns, `pass` in non-abstract methods, `# TODO`,
      `# stub`, `NotImplementedError`
    - Each hit must be either justified or replaced
    - Document any intentional stubs here with rationale

  - [ ] 3.5 Cross-spec entry point verification
    - Verify that no other spec references `ModelConfig`, `quality_gate`,
      `quality_gate_timeout`, or `QUALITY_GATE_RESULT` in their task or
      requirement files
    - _Requirements: all_

  - [ ] 3.V Verify wiring group
    - [ ] All smoke tests pass
    - [ ] No unjustified stubs remain in touched files
    - [ ] All execution paths from design.md are live (traceable in code)
    - [ ] All existing tests still pass: `make check`

## Traceability

| Requirement | Test Spec Entry | Implemented By Task | Verified By Test |
|-------------|-----------------|---------------------|------------------|
| 130-REQ-1.1 | TS-130-1 | 2.1 | `test_config_dead_code_removal.py::test_quality_gate_absent` |
| 130-REQ-1.2 | TS-130-2 | 2.1 | `test_config_dead_code_removal.py::test_quality_gate_timeout_absent` |
| 130-REQ-1.3 | TS-130-6 | 2.2 | `test_config_dead_code_removal.py::test_promoted_defaults_no_quality_gate` |
| 130-REQ-1.4 | TS-130-12 | 2.2 | `test_config_dead_code_removal.py::test_template_no_quality_gate` |
| 130-REQ-1.5 | TS-130-11 | 2.2 | `test_config_dead_code_removal.py::test_no_model_config_descriptions` |
| 130-REQ-1.E1 | TS-130-E1 | 2.1 | `test_config_dead_code_removal.py::test_old_quality_gate_silently_ignored` |
| 130-REQ-2.1 | TS-130-3 | 2.1 | `test_config_dead_code_removal.py::test_model_config_absent` |
| 130-REQ-2.2 | TS-130-4 | 2.1 | `test_config_dead_code_removal.py::test_agent_fox_config_no_models` |
| 130-REQ-2.3 | TS-130-5, TS-130-13 | 2.2 | `test_config_dead_code_removal.py::test_visible_sections_no_models` |
| 130-REQ-2.4 | TS-130-5 | 2.2 | `test_config_dead_code_removal.py::test_schema_deprecated_fields_gone` |
| 130-REQ-2.5 | TS-130-11 | 2.2 | `test_config_dead_code_removal.py::test_no_model_config_descriptions` |
| 130-REQ-2.E1 | TS-130-E2 | 2.1 | `test_config_dead_code_removal.py::test_old_models_section_silently_ignored` |
| 130-REQ-3.1 | TS-130-E4 | 2.1 | `test_config_dead_code_removal.py::test_old_triage_silently_ignored` |
| 130-REQ-3.2 | TS-130-E3 | 2.1 | `test_config_dead_code_removal.py::test_old_skeptic_silently_ignored` |
| 130-REQ-3.3 | TS-130-E3 | 2.1 | `test_config_dead_code_removal.py::test_old_skeptic_silently_ignored` |
| 130-REQ-3.E1 | TS-130-E3 | 2.1 | `test_config_dead_code_removal.py::test_old_skeptic_silently_ignored` |
| 130-REQ-4.1 | TS-130-7 | 2.2 | `test_config_dead_code_removal.py::test_phantom_routing_bounds_absent` |
| 130-REQ-4.2 | TS-130-8 | 2.2 | `test_config_dead_code_removal.py::test_phantom_routing_descriptions_absent` |
| 130-REQ-5.1 | TS-130-9 | 2.2 | `test_config_dead_code_removal.py::test_drift_bounds_include_none` |
| 130-REQ-6.1 | TS-130-10 | 2.3 | `test_config_dead_code_removal.py::test_quality_gate_result_event_absent` |
| 130-REQ-7.1 | (doc review) | 2.4 | Visual inspection |
| 130-REQ-7.2 | (doc review) | 2.4 | Visual inspection |
| 130-REQ-7.3 | (doc review) | 2.4 | Visual inspection |
| 130-REQ-7.4 | (doc review) | 2.4 | Visual inspection |
| 130-REQ-7.5 | (doc review) | 2.4 | Visual inspection |
| 130-REQ-8.1 | TS-130-SMOKE-1, TS-130-SMOKE-2 | 2.5 | `make check` |
| Property 2 | TS-130-P1 | 2.1 | `test_config_dead_code_props.py::test_silent_ignore` |
| Property 3 | TS-130-P2 | 2.2 | `test_config_dead_code_props.py::test_metadata_keys_match_fields` |

## Notes

- This is a deletion-only spec. No new runtime code is introduced.
- `NightShiftConfig.quality_gate_timeout` is a **different** config class
  (`NightShiftConfig`, not `OrchestratorConfig`) and already uses
  `extra='ignore'` to handle removed fields. Its tests in
  `test_nightshift_fix_only.py` should NOT be modified.
- `quality_gates` in `fix/improve.py` is a verdict field ("PASS"/"FAIL"), not
  a config parameter. It is out of scope.
- ADRs, errata, and audit docs that mention old archetype names or
  `quality_gate.result` are historical records and are not modified.
