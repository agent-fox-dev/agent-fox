# afspec Library Integration and Format Detection

## Overview

Integrate the `afspec` library from af-core as a dependency of agent-fox and
create the foundation layer for v1.2 spec format support. This spec adds the
library dependency, introduces format version detection in spec discovery, and
adapts `SpecInfo` to carry format metadata.

This is the foundation spec for the v4 transition. All other v1.2 migration
specs depend on it.

## Goals

1. Add `afspec` as a local path dependency in `pyproject.toml`.
2. Create a `SpecFormat` enum distinguishing v1 (markdown) from v1.2 (JSON).
3. Update `agent_fox/spec/discovery.py` to detect which format a spec folder
   uses and populate `SpecInfo.format`.
4. Skip old-format (v1 markdown) specs during discovery — they remain in the
   archive but are invisible to the pipeline.
5. Verify that `afspec.load_spec()` can load v1.2 spec folders discovered by
   the updated discovery module.

## Non-Goals

- Changing the graph builder or planner (spec 133).
- Changing context assembly or prompt rendering (spec 134).
- Changing the af-spec skill or lint-specs (spec 135).
- Supporting campaigns or the coordination-layer operational store.
- Migrating or rewriting existing archived specs.

## Design Decisions

1. **Dependency mechanism:** `afspec` is added as a local path dependency
   (`afspec = {path = "../af-core/packages/afspec"}`). This supports active
   co-development across both repos.

2. **Format detection strategy:** A spec folder is v1.2 if it contains
   `requirements.json`. A folder with `requirements.md` (or no requirements
   file) is v1 markdown. The presence of `requirements.json` is the single
   discriminator because it is the most structurally distinctive artifact
   (JSON vs markdown, and absorbs content from the old `design.md`).

3. **Old format handling:** v1 markdown specs are skipped during discovery.
   They stay in the archive for historical reference but are not loaded into
   the pipeline. No backward-compatibility code is maintained.

4. **SpecInfo changes:** The existing `SpecInfo` dataclass gains a `format`
   field of type `SpecFormat`. The `has_tasks` and `has_prd` booleans remain
   but now reflect the v1.2 file names (`tasks.json`, `prd.md` with
   frontmatter).

## Source

Source: Input provided by user via interactive prompt.
