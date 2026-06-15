# Remove Legacy Markdown Spec Format Code

## Overview

After specs 132-135 land the v1.2 JSON spec format support, the old
markdown-based spec parsing, validation, and rendering code becomes dead
weight. This spec removes all legacy code that is no longer necessary,
cleaning up the codebase for the v4 release.

## Goals

1. Delete the markdown spec parser (`agent_fox/spec/parser.py`) after
   extracting its shared dataclasses to `agent_fox/spec/types.py`.
2. Delete the entire markdown validators directory
   (`agent_fox/spec/validators/`, 9 files, ~30 functions).
3. Delete the markdown-based verification checklist builder
   (`agent_fox/spec/verification_checklist.py`).
4. Delete the AI-powered markdown validation module
   (`agent_fox/spec/ai_validation.py`).
5. Update all consumers that imported from deleted modules to use
   the new v1.2 equivalents or the extracted types.
6. Remove stale markdown file constants and references across the
   codebase.

## Non-Goals

- **`fix/spec_gen.py`** is kept as-is. The fix/nightshift pipeline's
  spec generation is out of scope for this migration.
- Reimplementing features. This spec only removes and rewires — it does
  not add new functionality.
- Changing the graph builder's internal data model. It continues to
  use `TaskGroupDef` / `SubtaskDef` / `CrossSpecDep` (now from
  `agent_fox/spec/types.py`).

## Design Decisions

1. **Dataclass extraction:** `TaskGroupDef`, `SubtaskDef`, and
   `CrossSpecDep` move from `parser.py` to a new `agent_fox/spec/types.py`.
   These are structural types used by the graph builder, parser_v12, and
   engine modules — they are not format-specific.

2. **Engine module updates:** `session_lifecycle.py`, `hot_load.py`,
   `engine.py`, and `dispatch.py` currently import regex patterns and
   functions from `parser.py`. These imports are updated to use
   `parser_v12` (from spec 133) or `afspec` directly.

3. **Graph helper updates:** `spec_helpers.py`, `file_impacts.py`, and
   `injection.py` reference old markdown filenames. These are updated
   to reference JSON filenames or use afspec models.

4. **Validator `Finding` class:** The `Finding` dataclass from
   `validators/_helpers.py` is used by `lint.py` and potentially by
   spec 135's updated lint-specs. If spec 135 keeps using `Finding`,
   it should be extracted alongside the types. Otherwise, it is deleted
   with the validators.

## Scope: Files to Delete

| File | Lines (approx) | Purpose |
|------|---------------|---------|
| `agent_fox/spec/parser.py` | ~200 | Markdown tasks.md / prd.md parser |
| `agent_fox/spec/validators/__init__.py` | ~30 | Re-exports |
| `agent_fox/spec/validators/_helpers.py` | ~100 | Constants, Finding, regex patterns |
| `agent_fox/spec/validators/files.py` | ~30 | Missing-file checks |
| `agent_fox/spec/validators/tasks.py` | ~120 | tasks.md structure validation |
| `agent_fox/spec/validators/requirements.py` | ~100 | requirements.md validation |
| `agent_fox/spec/validators/dependencies.py` | ~100 | prd.md dependency validation |
| `agent_fox/spec/validators/schema.py` | ~80 | Markdown section validation |
| `agent_fox/spec/validators/traceability.py` | ~200 | Cross-file traceability |
| `agent_fox/spec/validators/runner.py` | ~80 | Validation orchestrator |
| `agent_fox/spec/verification_checklist.py` | ~180 | Verification checklist builder |
| `agent_fox/spec/ai_validation.py` | ~100 | AI semantic validation |
| **Total** | **~1,300** | |

## Scope: Files to Update (import rewiring)

| File | Change |
|------|--------|
| `agent_fox/spec/types.py` | **New** — receives dataclasses from parser.py |
| `agent_fox/graph/planner.py` | Import types from `spec/types.py` |
| `agent_fox/graph/builder.py` | Import types from `spec/types.py` |
| `agent_fox/spec/parser_v12.py` | Import types from `spec/types.py` |
| `agent_fox/engine/session_lifecycle.py` | Replace parser.py imports with v1.2 equivalents |
| `agent_fox/engine/hot_load.py` | Replace parser.py imports with v1.2 equivalents |
| `agent_fox/engine/engine.py` | Replace parser.py imports with v1.2 equivalents |
| `agent_fox/engine/dispatch.py` | Replace parser.py imports with v1.2 equivalents |
| `agent_fox/graph/spec_helpers.py` | Update file references from .md to .json |
| `agent_fox/graph/file_impacts.py` | Update file references from .md to .json |
| `agent_fox/graph/injection.py` | Update file references from .md to .json |
| `agent_fox/cli/lint_specs.py` | Remove validator imports (replaced in spec 135) |

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 132_afspec_integration | 2 | 2 | Uses afspec dependency and SpecFormat enum; group 2 is where discovery is updated |
| 133_v12_parsing_pipeline | 2 | 2 | parser_v12.py must exist before parser.py is deleted; group 2 implements the parser |
| 134_v12_context_rendering | 2 | 2 | Context assembly must use v1.2 rendering before _CORE_SPEC_FILES is removed; group 2 implements context changes |
| 135_v12_skill_and_validation | 2 | 2 | lint-specs must use afspec validation before validators/ is deleted; group 2 implements validation routing |

## Source

Source: Input provided by user via interactive prompt.
