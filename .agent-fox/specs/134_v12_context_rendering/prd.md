# v1.2 Context Assembly and Rendering

## Overview

Update the session context assembly pipeline and spec helper functions to
render v1.2 JSON specs into markdown for prompt injection. Instead of reading
raw markdown files from disk, the updated pipeline detects v1.2 spec folders,
loads them via `afspec.load_spec()`, and uses `afspec.render_individual()` to
produce per-artifact markdown. The verification checklist and graph spec
helpers are also updated to extract structured data from v1.2 JSON artifacts
instead of parsing markdown with regex.

This spec depends on spec 132 (afspec integration and format detection) which
provides the `afspec` dependency, `SpecFormat` enum, and format detection in
discovery.

## Goals

1. Detect v1.2 spec folders in `assemble_context()` and branch to an
   afspec-based rendering path that replaces raw file reads.
2. Update `count_ts_entries()` in `agent_fox/graph/spec_helpers.py` to count
   test cases from `test_spec.json` via afspec models instead of parsing
   `### TS-` headings in markdown.
3. Update `spec_has_existing_code()` to check `architecture.md` for v1.2
   specs instead of `design.md`.
4. Update `agent_fox/spec/verification_checklist.py` to extract requirement
   IDs and subtask state from v1.2 JSON artifacts via afspec Pydantic models.

## Non-Goals

- Changing spec discovery or the `SpecFormat` enum (spec 132).
- Updating the graph builder or planner to use v1.2 data (spec 133).
- Updating the af-spec skill or lint-specs validation (spec 135).
- Changing the rendering output format of afspec itself.
- Supporting mixed v1/v1.2 artifacts within a single spec folder.

## Design Decisions

1. **Format detection in context assembly:** A spec folder is v1.2 if
   `requirements.json` exists in the folder. This matches the detection
   strategy established in spec 132. The detection is done locally in
   `context.py` rather than requiring a `SpecInfo` object, because
   `assemble_context()` receives a `Path`, not a `SpecInfo`.

2. **Per-artifact rendering:** Use `afspec.render_individual()` which returns
   a dict of artifact names to markdown strings. This produces the same
   section structure as the old raw file reads but from structured data.
   The `architecture.md` file (v1.2 equivalent of `design.md`) is still
   read from disk since it remains a markdown file.

3. **Graceful degradation:** If afspec fails to load a v1.2 spec (e.g.,
   malformed JSON), log a warning and fall back to raw file reads of
   whatever markdown files exist. This prevents a broken spec from
   crashing the entire pipeline.

4. **Helper function branching:** `count_ts_entries()` and
   `spec_has_existing_code()` detect v1.2 locally and branch. The
   verification checklist functions do the same. No shared "is v1.2"
   utility is needed since each call site has its own spec_dir Path.

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 132_afspec_integration | 2 | 1 | Uses SpecFormat enum and afspec dependency from group 2 |

## Source

Source: Input provided by user via interactive prompt.
