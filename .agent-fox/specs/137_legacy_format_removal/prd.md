# Legacy Spec Format Removal

## Intent

Remove all v1 markdown-only spec format support from the agent-fox codebase,
leaving only the v1.2 JSON-based format. Old specs remain on disk in their
original format but are no longer parsed, validated, or discovered by the
system.

## Background

Specs 132-135 introduced the v1.2 JSON spec format backed by the `afspec`
library. The system currently supports both formats through format detection
and routing at every pipeline stage: discovery, parsing, validation, context
assembly, and verification. This dual-format code adds complexity, increases
the test surface, and makes it harder to evolve the spec pipeline.

All active specs now use v1.2. V1 markdown specs are archived historical
records. The v1 code paths are dead code in practice.

## Goals

1. Extract shared types (`TaskGroupDef`, `SubtaskDef`, `CrossSpecDep`,
   `Finding`, severity constants) into `spec/types.py`.
2. Rewire all consumer imports to the new locations.
3. Delete all v1 markdown parsing code (`parser.py`, `validators/`,
   `ai_validation.py`).
4. Delete v1-specific test files and update import paths in remaining tests.
5. Remove v1 format-routing conditionals and v1 filename references.
6. Simplify `verification_checklist.py`, `discovery.py`, `lint.py`,
   `hot_load.py`, and engine modules by removing v1 code paths.
7. Update documentation in `docs/` and `docs/architecture/` to reflect the
   v1.2-only world.

## Non-Goals

- Migrating existing v1 specs to v1.2 format.
- Rewriting `ai_validation.py` for v1.2 (separate spec if needed).
- Adding new validation rules or features.

## Design Decisions

1. **Shared types location**: `Finding`, severity constants, `compute_exit_code`,
   and `sort_findings` move to `spec/types.py` alongside `TaskGroupDef`,
   `SubtaskDef`, and `CrossSpecDep`. This centralizes all shared spec-layer
   types in one module. Rationale: these types are consumed by many modules
   across the system; a single canonical location prevents circular imports
   and makes the dependency graph clear.

2. **verification_checklist.py**: Keep the file, strip v1 code paths. The v1.2
   checklist functionality is actively used by `session/context.py`. Moving
   it elsewhere would increase the diff without adding value. Rationale:
   pragmatic — minimize the blast radius while removing all v1 code.

3. **SpecFormat enum**: Keep the enum but remove the `V1_MARKDOWN` member.
   `SpecFormat.V1_2_JSON` remains as the only value. The `format` field on
   `SpecInfo` is kept (always `V1_2_JSON`). This avoids rewriting every
   consumer that accesses `spec.format` — instead, v1 branches are removed
   and v1.2 branches become the only path. Format-routing conditionals in
   `planner.py`, `lint.py`, etc. are simplified to always take the v1.2 path.
   Rationale: safer than removing the enum entirely — consumers that reference
   `SpecFormat` or `spec.format` continue to compile while v1 branches are
   surgically removed.

4. **ai_validation.py deletion**: Delete entirely. The module reads v1 filenames
   (`requirements.md`, `design.md`, `test_spec.md`) and imports v1 parser
   helpers (`_DEP_TABLE_HEADER_ALT`, `_parse_table_rows`). It has no v1.2
   support. If AI validation is needed for v1.2 specs, that's a separate spec.

5. **`_patterns.py`**: Keep the file. Only `extract_test_spec_ids()` is
   v1-only, and its only callers are in `validators/` (being deleted). The
   `REQ_ID_BARE` regex is used by `verification_checklist.py` (being kept).
   Remove `extract_test_spec_ids()` and the `test_spec.md` reference; keep
   all other patterns. Rationale: deleting the file would break
   verification_checklist.py.

6. **Test cleanup scope**: Included in this spec but as its own task group
   (after module deletion). Tests for deleted modules are deleted. Tests
   using shared types get import path updates. Tests asserting `V1_MARKDOWN`
   existence (test_132) are updated. This ensures `make test` passes after
   the change.

7. **hot_load.py completeness check**: Replace `EXPECTED_FILES` (v1 file list)
   with a v1.2 equivalent checking for `prd.md`, `requirements.json`,
   `test_spec.json`, `tasks.json`. Replace `validate_specs()` with
   `afspec.validate()`.

8. **session_lifecycle.py subtask extraction**: Adapt
   `extract_subtask_descriptions()` for v1.2 by loading task groups via
   `parse_tasks_v12()` and extracting descriptions from the `TaskGroupDef`
   body field. This avoids needing raw regex on JSON. The body field already
   contains a markdown rendering of subtasks.

9. **engine.py and dispatch.py task completion**: Replace `parse_tasks()` with
   `parse_tasks_v12()` to read task completion state from `tasks.json`.

10. **injection.py requirements check**: Update the `requirements.md` existence
    check in `build_review_only_graph()` to check for `requirements.json`
    instead. This is a subtle but critical change — without it, Verifier nodes
    would not be injected for v1.2 specs in review-only mode.

11. **Incremental task groups**: The implementation is split into 7 task groups
    (after the test-writing group) to minimize risk. Types are extracted
    first, then imports are rewired while old modules still exist, then
    modules are deleted, then tests are updated, then format-routing is
    cleaned up, then docs are updated, then wiring is verified. Each group
    can be independently verified with `make test`.

## Source

Source: Input provided by user via interactive prompt
