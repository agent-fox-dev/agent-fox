# Implementation Plan: Night-Shift Fix-Only Mode

<!-- AGENT INSTRUCTIONS
- Implement exactly ONE top-level task group per session
- Task group 1 writes failing tests from test_spec.md — all subsequent groups
  implement code to make those tests pass
- Follow the git-flow: feature branch from develop -> implement -> test -> merge to develop
- Update checkbox states as you go: [-] in progress, [x] complete
-->

## Overview

This spec removes the hunt-scan stream, the spec-executor stream, and all
supporting modules, tests, and config fields from night-shift. The work is
ordered so that tests are written first (group 1), then source code is
modified (groups 2-4), then tests for deleted modules are removed (group 5),
and finally documentation is updated and wiring is verified (group 6).

## Test Commands

- Spec tests: `uv run pytest -q tests/unit/nightshift/test_nightshift_fix_only.py`
- Unit tests: `uv run pytest -q tests/unit/`
- All tests: `uv run pytest -q`
- Linter: `uv run ruff check agent_fox/ tests/`

## Tasks

- [x] 1. Write failing spec tests
  - [x] 1.1 Create test file `tests/unit/nightshift/test_nightshift_fix_only.py`
    - Add tests for TS-125-1 through TS-125-12 as test functions
    - Add property tests for TS-125-P1, TS-125-P2, TS-125-P3
    - Add edge case tests for TS-125-E1 through TS-125-E4
    - Tests MUST fail initially (deleted modules still exist, etc.)
    - _Test Spec: TS-125-1 through TS-125-12, TS-125-P1 through TS-125-P3,
      TS-125-E1 through TS-125-E4_

  - [x] 1.V Verify task group 1
    - [x] All spec tests exist and are syntactically valid
    - [x] Spec tests that check for deleted files/classes FAIL (files still exist)
    - [x] No linter warnings introduced: `uv run ruff check tests/unit/nightshift/test_nightshift_fix_only.py`

- [ ] 2. Delete hunt-scan modules and categories directory
  - [ ] 2.1 Delete hunt source modules
    - Delete `agent_fox/nightshift/hunt.py`
    - Delete `agent_fox/nightshift/critic.py`
    - Delete `agent_fox/nightshift/dedup.py`
    - Delete `agent_fox/nightshift/finding.py`
    - Delete `agent_fox/nightshift/ignore_filter.py`
    - Delete `agent_fox/nightshift/ignore.py`
    - Delete `agent_fox/nightshift/categories/` directory (all files)
    - _Requirements: 1.1, 1.2_

  - [ ] 2.2 Remove hunt imports from engine.py
    - Remove `from agent_fox.nightshift.critic import consolidate_findings`
    - Remove `from agent_fox.nightshift.dedup import filter_known_duplicates`
    - Remove `from agent_fox.nightshift.finding import create_issues_from_groups`
    - Remove `from agent_fox.nightshift.ignore_filter import filter_ignored`
    - Remove `_run_hunt_scan()` and `_run_hunt_scan_inner()` methods
    - Remove `auto_fix` parameter from `__init__`
    - Remove `_hunt_scan_in_progress` attribute
    - Remove `embedder` parameter from `__init__`
    - _Requirements: 2.1, 2.2, 2.3_

  - [ ] 2.3 Remove hunt import from init_project.py
    - Remove `from agent_fox.nightshift.ignore import NIGHTSHIFT_IGNORE_FILENAME, NIGHTSHIFT_IGNORE_SEED`
    - Remove code that creates the `.night-shift` file
    - _Requirements: 6.1, 6.2_

  - [ ] 2.V Verify task group 2
    - [ ] Spec tests TS-125-1, TS-125-2, TS-125-3, TS-125-4, TS-125-5 pass
    - [ ] `uv run ruff check agent_fox/nightshift/engine.py agent_fox/workspace/init_project.py`
    - [ ] Requirements 1.1, 1.2, 2.1, 2.2, 2.3, 6.1, 6.2 met

- [ ] 3. Simplify streams, CLI, and config
  - [ ] 3.1 Simplify streams.py
    - Delete `SpecExecutorStream` class
    - Remove hunt-scan `EngineWorkStream` from `build_streams()`
    - Remove parameters: `no_specs`, `no_hunts`, `auto`, `discover_fn`, `orch_factory`
    - Remove `_CONFIG_TO_STREAM` entries for "specs" and "hunts"
    - Keep only fix-pipeline EngineWorkStream in `build_streams()`
    - Simplify platform degradation logic (only fix-pipeline)
    - _Requirements: 3.1, 3.2, 3.3, 3.4, 3.E1_

  - [ ] 3.2 Simplify CLI nightshift.py
    - Remove `--auto`, `--no-specs`, `--no-hunts`, `--specs-dir` options
    - Remove `_SpecBatchRunner` class
    - Remove spec discovery setup (`_discover_fn`, `_known_specs`, `_specs_dir`,
      `_db_conn`, `_orch_factory`)
    - Remove `auto` parameter from `night_shift_cmd`
    - Remove imports: `resolve_spec_root`, `discover_new_specs_gated`,
      `build_graph`, `resolve_order`, `save_plan`, `parse_cross_deps`,
      `parse_tasks`
    - Simplify `build_streams()` call to pass only `config`, `no_fixes`,
      `engine`, `budget`
    - Remove `auto_fix=auto` from NightShiftEngine constructor
    - _Requirements: 4.1, 4.2, 4.3, 4.4_

  - [ ] 3.3 Remove unused config fields
    - Delete `NightShiftCategoryConfig` class from `agent_fox/core/config.py`
    - Remove fields from `NightShiftConfig`: `hunt_scan_interval`,
      `categories`, `quality_gate_timeout`, `spec_interval`,
      `enabled_streams`, `similarity_threshold`
    - Remove validators: `clamp_spec_interval`, `default_empty_enabled_streams`,
      `clamp_similarity_threshold`
    - Keep `issue_check_interval`, `push_fix_branch`, and their validators
    - _Requirements: 5.1, 5.2, 5.3, 5.E1_

  - [ ] 3.V Verify task group 3
    - [ ] Spec tests TS-125-7, TS-125-8, TS-125-9, TS-125-10, TS-125-11 pass
    - [ ] Spec tests TS-125-E1, TS-125-E2, TS-125-E3, TS-125-E4 pass
    - [ ] `uv run ruff check agent_fox/nightshift/streams.py agent_fox/cli/nightshift.py agent_fox/core/config.py`
    - [ ] Requirements 3.1-3.4, 4.1-4.4, 5.1-5.4 met

- [ ] 4. Delete hunt-related tests and fix remaining test imports
  - [ ] 4.1 Delete test files for deleted modules
    - Delete `tests/unit/nightshift/test_critic.py`
    - Delete `tests/unit/nightshift/test_dedup.py`
    - Delete `tests/unit/nightshift/test_finding.py`
    - Delete `tests/unit/nightshift/test_hunt.py`
    - Delete `tests/unit/nightshift/test_quality_gate.py`
    - Delete `tests/unit/test_nightshift_ignore.py`
    - Delete `tests/integration/nightshift/test_critic.py`
    - Delete `tests/integration/nightshift/test_dedup.py`
    - Delete `tests/integration/nightshift/test_hunt_scan.py`
    - Delete `tests/integration/test_nightshift_ignore_smoke.py`
    - Delete `tests/property/nightshift/test_critic_props.py`
    - Delete `tests/property/nightshift/test_dedup_props.py`
    - Delete `tests/property/nightshift/test_quality_gate_props.py`
    - Delete `tests/property/test_nightshift_ignore_props.py`
    - Delete `tests/test_critic_false_positives.py`
    - Delete `tests/test_hunt_dedup_similarity.py`
    - Delete `tests/test_ignore_filter.py`
    - _Requirements: 7.1_

  - [ ] 4.2 Fix remaining test files that import from deleted modules
    - `tests/property/nightshift/test_nightshift_props.py`: remove tests
      importing from `finding` and `hunt` modules; keep tests for fix-pipeline
    - `tests/integration/test_daemon_lifecycle.py`: remove test importing
      `SpecExecutorStream` (line ~247); update other tests as needed
    - `tests/unit/nightshift/test_fix_pipeline.py`: remove imports of
      `Finding`/`FindingGroup` from `finding.py`; replace with minimal local
      stubs or remove affected tests
    - `tests/unit/nightshift/test_streams.py`: remove `SpecExecutorStream`
      tests and hunt stream tests; update `build_streams()` tests
    - `tests/unit/nightshift/test_config.py`: remove `NightShiftCategoryConfig`
      tests and removed-field tests
    - `tests/unit/cli/test_init_labels.py`: remove import of
      `FINGERPRINT_LABEL` from `dedup` and update affected test
    - _Requirements: 7.2_

  - [ ] 4.3 Run full test suite and fix any remaining breakage
    - Run `uv run pytest -q` and fix any collection errors or test failures
      caused by dangling imports or references to deleted code
    - _Requirements: 7.3_

  - [ ] 4.V Verify task group 4
    - [ ] All spec tests pass: `uv run pytest -q tests/unit/nightshift/test_nightshift_fix_only.py`
    - [ ] All existing tests still pass: `uv run pytest -q`
    - [ ] No linter warnings introduced: `uv run ruff check agent_fox/ tests/`
    - [ ] Requirements 7.1, 7.2, 7.3 met

- [ ] 5. Update documentation
  - [ ] 5.1 Rewrite `docs/architecture/04-night-shift.md`
    - Remove "The Hunt Phase" section entirely
    - Remove "Conceptual Model" two-phase description
    - Remove references to hunt categories, critic, dedup, ignore
    - Remove spec-executor references
    - Update "Engine Lifecycle" to reflect fix-only behavior
    - Keep "The Fix Phase", "Labels", "Staleness Detection"
    - _Requirements: 8.1_

  - [ ] 5.2 Update CLI reference and config reference
    - Remove `--auto`, `--no-specs`, `--no-hunts`, `--specs-dir` from
      `docs/cli-reference.md`
    - Update command description to reflect fix-only behavior
    - Remove `hunt_scan_interval`, `categories`, `quality_gate_timeout`,
      `spec_interval`, `enabled_streams`, `similarity_threshold` from
      `docs/config-reference.md`
    - _Requirements: 8.2, 8.3_

  - [ ] 5.3 Update project README and docs README
    - Update `README.md` to describe night-shift as fix-only
    - Remove references to hunt scans, `.night-shift` ignore file,
      spec-executor stream
    - Update `docs/README.md` similarly
    - _Requirements: 8.4, 8.5_

  - [ ] 5.4 Update remaining architecture docs
    - Update `docs/architecture.md`, `docs/architecture/README.md`,
      `docs/architecture/prd.md`
    - Update `docs/architecture/02-planning.md`,
      `docs/architecture/03-execution-and-archetypes.md`,
      `docs/architecture/05-knowledge-system-architecture.md`
    - Remove or correct references to hunt scans, hunt categories,
      critic, dedup, spec-executor stream, `.night-shift` file
    - Do NOT modify ADRs, audit reports, or errata (historical records)
    - _Requirements: 8.5, 8.6_

  - [ ] 5.V Verify task group 5
    - [ ] All documentation updated accurately
    - [ ] No docs outside `docs/adr/`, `docs/audits/`, `docs/errata/` reference
          hunt scans, hunt categories, or spec-executor as current functionality
    - [ ] All existing tests still pass: `uv run pytest -q`
    - [ ] No linter warnings introduced: `uv run ruff check agent_fox/ tests/`

- [ ] 6. Wiring verification

  - [ ] 6.1 Trace every execution path from design.md end-to-end
    - For Path 1 (fix-pipeline drain loop), verify the entry point actually
      calls the next function in the chain (read the calling code)
    - Confirm no function in the chain is a stub
    - Every path must be live in production code
    - _Requirements: all_

  - [ ] 6.2 Verify return values propagate correctly
    - For every function in this spec that returns data consumed by a caller,
      confirm the caller receives and uses the return value
    - _Requirements: all_

  - [ ] 6.3 Run the integration smoke tests
    - All `TS-125-SMOKE-*` tests pass using real components
    - _Test Spec: TS-125-SMOKE-1_

  - [ ] 6.4 Stub / dead-code audit
    - Search all files touched by this spec for: `return []`, `return None`
      on non-Optional returns, `pass` in non-abstract methods, `# TODO`,
      `# stub`, `NotImplementedError`
    - Each hit must be either justified or replaced
    - _Requirements: all_

  - [ ] 6.5 Cross-spec entry point verification
    - Verify the fix-pipeline stream is actually invoked from DaemonRunner
    - Verify `_drain_issues` is called via EngineWorkStream.run_once()
    - _Requirements: all_

  - [ ] 6.V Verify wiring group
    - [ ] All smoke tests pass
    - [ ] No unjustified stubs remain in touched files
    - [ ] All execution paths from design.md are live
    - [ ] All existing tests still pass: `uv run pytest -q`

## Traceability

| Requirement | Test Spec Entry | Implemented By Task | Verified By Test |
|-------------|-----------------|---------------------|------------------|
| 125-REQ-1.1 | TS-125-1 | 2.1 | test_hunt_source_modules_deleted |
| 125-REQ-1.2 | TS-125-2 | 2.1 | test_categories_directory_deleted |
| 125-REQ-1.3 | TS-125-3, TS-125-P1 | 2.2 | test_no_dangling_imports_nightshift |
| 125-REQ-1.E1 | TS-125-P1 | 2.3 | test_no_dangling_imports_anywhere |
| 125-REQ-2.1 | TS-125-4 | 2.2 | test_engine_no_hunt_methods |
| 125-REQ-2.2 | TS-125-5 | 2.2 | test_engine_rejects_removed_params |
| 125-REQ-2.4 | TS-125-6 | 2.2 | test_engine_retains_fix_methods |
| 125-REQ-3.1 | TS-125-7 | 3.1 | test_spec_executor_stream_deleted |
| 125-REQ-3.3 | TS-125-8, TS-125-P3 | 3.1 | test_build_streams_single_fix |
| 125-REQ-3.E1 | TS-125-9 | 3.1 | test_build_streams_no_fixes |
| 125-REQ-4.1 | TS-125-E1 to E4 | 3.2 | test_cli_rejects_auto/specs/hunts/dir |
| 125-REQ-5.1 | TS-125-10 | 3.3 | test_config_backward_compat |
| 125-REQ-5.2 | TS-125-11 | 3.3 | test_category_config_deleted |
| 125-REQ-5.4 | TS-125-P2 | 3.3 | test_config_ignores_removed_fields |
| 125-REQ-6.1 | TS-125-3 | 2.3 | test_no_dangling_imports_nightshift |
| 125-REQ-6.2 | TS-125-12 | 2.3 | test_init_no_nightshift_file |
| 125-REQ-7.1 | TS-125-1 (test files) | 4.1 | (file deletion verified) |
| 125-REQ-7.2 | TS-125-P1 | 4.2 | test_no_dangling_imports_anywhere |
| 125-REQ-7.3 | TS-125-SMOKE-1 | 4.3 | (full test suite run) |
| 125-REQ-8.1 | — | 5.1 | (manual review) |
| 125-REQ-8.2 | — | 5.2 | (manual review) |
| 125-REQ-8.3 | — | 5.2 | (manual review) |
| 125-REQ-8.4 | — | 5.3 | (manual review) |
| 125-REQ-8.5 | — | 5.4 | (manual review) |
| 125-REQ-8.6 | — | 5.4 | (manual review) |

## Notes

- This is a deletion-heavy spec. Most work is removing files and updating
  imports, not writing new code.
- Task group order matters: delete source before deleting tests (group 2
  before group 4) so that failing-import collection errors in group 4's
  test cleanup are visible and fixable.
- The `nightshift/__init__.py` file may need updating if it re-exports
  symbols from deleted modules.
- The `make check` equivalent is `uv run ruff check agent_fox/ tests/ && uv run pytest -q`.
