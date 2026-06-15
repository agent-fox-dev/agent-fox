# v1.2 Parsing Pipeline

## Overview

Replace the markdown-based spec parser with a new parser module that uses
`afspec.load_spec()` and the afspec Pydantic models to parse v1.2 format specs.
Update the graph planner to route v1.2 specs through the new parser while
keeping the graph builder untouched.

This spec builds on spec 132 (afspec integration and format detection), which
added the `afspec` dependency, `SpecFormat` enum, and format-aware `SpecInfo`.
Spec 132 gives us the ability to detect v1.2 specs; this spec gives us the
ability to parse them into the same dataclasses the graph builder already
consumes.

## Goals

1. Create `agent_fox/spec/parser_v12.py` containing mapper functions that
   convert afspec Pydantic models to the existing `TaskGroupDef`,
   `SubtaskDef`, and `CrossSpecDep` dataclasses from `parser.py`.
2. Add `parse_tasks_v12()` and `parse_cross_deps_v12()` entry points that
   load a v1.2 spec via `afspec.load_spec()` and return the same types as
   the existing markdown parser.
3. Update `agent_fox/graph/planner.py` (`build_plan()`) to check
   `spec.format` and route v1.2 specs through the new parser functions.
4. Preserve full backward compatibility: the graph builder, resolver, and
   all downstream consumers see the same `TaskGroupDef` / `CrossSpecDep`
   types regardless of source format.

## Non-Goals

- Changing the graph builder (`builder.py`) or resolver.
- Changing the existing markdown parser (`parser.py`).
- Changing context assembly or prompt rendering (spec 134).
- Supporting the `archetype` field in v1.2 tasks (deferred; set to None).
- Migrating existing specs to v1.2 format.

## Design Decisions

1. **Separate parser module:** The v1.2 parser lives in its own module
   (`parser_v12.py`) rather than extending `parser.py`. This keeps the
   markdown parser untouched and allows clean removal later.

2. **Mapper pattern:** Individual mapper functions (`_map_subtask`,
   `_map_task_group`, `_map_dependency`) convert one afspec model to one
   agent-fox dataclass. This makes the mapping testable at the unit level.

3. **Completion semantics:** `SubtaskDef.completed` maps from
   `Subtask.state == SubtaskState.DONE`. `TaskGroupDef.completed` is True
   when all non-dropped subtasks are DONE. Dropped subtasks are excluded
   from the completion check.

4. **Body rendering:** `TaskGroupDef.body` is constructed by joining
   subtask details into a markdown representation, preserving the contract
   that `body` contains human-readable content for context assembly.

5. **Planner routing:** `build_plan()` checks `spec.format` from the
   `SpecInfo` returned by discovery. For `V1_2_JSON` specs it calls the
   new parser; the graph builder receives the same types either way.

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 132_afspec_integration | 2 | 1 | Uses SpecFormat enum and updated SpecInfo from group 2 |

## Source

Source: Input provided by user via interactive prompt.
