# Spec Format v1.2: JSON-Based Specifications

## Purpose and Placement

This document describes the v1.2 spec format — a JSON-based specification
system that replaces the original all-markdown format (v1) — and the
infrastructure agent-fox uses to support both formats simultaneously. It
covers the new file structure, format detection, parsing pipeline, context
assembly, validation routing, and the dual-format coexistence model.

For how specs are structured and validated in general, see
[Part 1: Spec Authoring](01-spec-authoring.md). For how parsed specs become
task graphs, see [Part 2: Planning](02-planning.md). For the original
migration plan, see
[Spec Format v2 Implementation Plan](../spec-format-v2-implementation-plan.md).

---

## Why v1.2

The v1 markdown format served well for human authoring but created friction at
the machine boundary. Parsing relied on regex against markdown headings,
checkbox syntax, and table structures — fragile patterns that broke on
formatting variations and could not express richer semantics (subtask state
machines, typed EARS patterns, computed coverage). Validation required nearly
thirty hand-written rules to enforce structure that a schema could guarantee
by construction.

The v1.2 format addresses this by moving structured data into JSON files
validated against schemas, while keeping human-authored narrative content in
markdown. The `afspec` library (from af-core) provides the Pydantic data
models, schema validation, and rendering functions that agent-fox consumes
through a thin adapter layer.

---

## File Structure

A v1.2 spec directory contains four required files and one optional file:

| Artifact | Format | Role |
|---|---|---|
| `prd.md` | Markdown | Product requirements document. Same filename as v1; content is unchanged. |
| `requirements.json` | JSON | Acceptance criteria with decomposed EARS fields, glossary, correctness properties, execution paths, and error handling. Replaces `requirements.md`. |
| `test_spec.json` | JSON | Structured test cases with typed entries (unit, property, edge case, smoke) and computed coverage. Replaces `test_spec.md`. |
| `tasks.json` | JSON | Task groups with subtask state machine, dependencies, traceability, and verification. Replaces `tasks.md`. |
| `architecture.md` | Markdown | Optional free-form architecture documentation. Absorbs the role of the former `design.md`. |

### Comparison with v1

| Aspect | v1 (Markdown) | v1.2 (JSON) |
|---|---|---|
| Required files | 5 (`prd.md`, `requirements.md`, `design.md`, `test_spec.md`, `tasks.md`) | 4 (`prd.md`, `requirements.json`, `test_spec.json`, `tasks.json`) |
| Optional files | None | 1 (`architecture.md`) |
| Validation | ~30 regex-based rules | JSON Schema + cross-file integrity via `afspec` |
| Task state | Binary checkbox (`[x]` / `[ ]`) | State machine (`pending`, `queued`, `in_progress`, `done`, `dropped`) |
| Design content | `design.md` (required) | `architecture.md` (optional) + correctness properties moved to `requirements.json` |
| Parsing | Regex on markdown | JSON deserialization into Pydantic models |

### What Stays the Same

The spec folder naming convention (`NN_snake_case_name`), the spec root
directory (`.agent-fox/specs/`), EARS syntax (decomposed into fields but same
six patterns), requirement ID format (`NN-REQ-M.S`), test spec ID format
(`TS-NN-N`), the task group concept, and the cross-spec dependency model are
all unchanged between formats.

---

## The `afspec` Library

`afspec` is a library from af-core that provides the canonical data models and
operations for the v1.2 format. Agent-fox depends on it as a runtime
dependency and uses three entry points:

- **`afspec.load_spec(spec_dir)`** — Parses all JSON artifacts in a spec
  directory and returns a unified `Spec` object containing Pydantic models for
  requirements, test specs, tasks, and dependencies.

- **`afspec.validate(spec_dir)`** — Runs schema validation and cross-file
  referential integrity checks. Returns a list of `ValidationError` objects.

- **`afspec.render_individual(artifact)`** — Converts a loaded Pydantic model
  back to human-readable markdown for display and context injection.

Agent-fox does not use `afspec` models directly in its core data layer.
Instead, a mapper layer converts `afspec` types to agent-fox's own frozen
dataclasses (`TaskGroupDef`, `SubtaskDef`, `CrossSpecDep`), preserving
format invariance for all downstream consumers.

---

## Format Detection

Format detection is deterministic and based on a single discriminator: the
presence of `requirements.json` in the spec directory. This file is the most
structurally distinctive v1.2 artifact — if it exists, the spec is v1.2.

The discovery system assigns each spec a `SpecFormat` value:

- **`V1_2_JSON`** — `requirements.json` is present.
- **`V1_MARKDOWN`** — `requirements.json` is absent (falls back to v1).

This format tag is carried on the `SpecInfo` record returned by
`discover_specs()` and is used throughout the system to route specs to the
appropriate parser, validator, and context renderer.

### Discovery Filtering

By default, `discover_specs()` returns only v1.2 specs. V1 markdown specs are
silently excluded from the active spec set. This reflects the transition
state: new specs are created in v1.2 format, and archived/completed v1 specs
are read-only historical records that do not need active processing.

The filtering is a deliberate design choice, not a limitation. V1 specs
remain on disk and in git history for reference. If a v1 spec needs to be
continued, it should be migrated to v1.2 format rather than processed through
the legacy pipeline.

---

## Parsing Pipeline

The v1.2 parsing pipeline converts `afspec` Pydantic models into agent-fox's
internal dataclasses. This mapping is the bridge between the `afspec` data
model and the format-agnostic graph builder.

### Task Parsing

`parse_tasks_v12()` loads `tasks.json` via `afspec.load_spec()` and maps each
`TaskGroup` to a `TaskGroupDef`:

- **Subtask mapping**: Each `Subtask` becomes a `SubtaskDef`. The `state`
  enum is collapsed to a boolean: `DONE` maps to `completed=True`, all other
  states map to `completed=False`.

- **Group completion**: A `TaskGroupDef` is marked completed when all
  non-dropped subtasks are in the `DONE` state. A group where every subtask
  is dropped is vacuously complete.

- **Body rendering**: The group body is rendered as a markdown checklist of
  subtasks, matching the format the graph builder and context system expect.

- **Archetype**: Set to `None` for v1.2 specs. The v1.2 format does not use
  inline archetype tags.

### Dependency Parsing

`parse_cross_deps_v12()` loads dependencies from `tasks.json` (not `prd.md`
as in v1) and maps each `TaskDependency` to a `CrossSpecDep`. The direction
convention is preserved: `from_spec` is the current spec (the one declaring
the dependency), `to_spec` is the upstream spec being depended on.

### Format Invariance

The critical property of the parsing pipeline is format invariance: the graph
builder receives identical `TaskGroupDef` and `CrossSpecDep` types regardless
of whether the source spec is v1 or v1.2. No downstream consumer needs to
know which format was parsed. This is enforced by the mapper layer, which
normalizes all format-specific details into the common type.

### Planner Routing

The planner routes parsing calls based on the spec's detected format. For
v1.2 specs, it calls `parse_tasks_v12()` and `parse_cross_deps_v12()`. For
v1 specs (if any remain in the active set), it falls back to the original
`parse_tasks()` and `parse_cross_deps()`. The routing is a simple conditional
on `spec.format` — no abstraction layer or strategy pattern.

---

## Context Assembly

When the engine prepares a coding session, it assembles spec content into the
agent's context window. For v1.2 specs, this means converting JSON artifacts
back to human-readable markdown — agents work with natural language, not raw
JSON.

The context assembly pipeline detects the spec format and branches:

- **v1.2 path**: Loads the spec via `afspec.load_spec()`, renders each
  artifact to markdown via `afspec.render_individual()`, and wraps each
  rendered block in a section header. If `architecture.md` exists, it is
  read directly from disk (it is already markdown). The system falls back to
  raw file reads on `afspec` load errors, providing graceful degradation.

- **v1 path**: Reads the markdown files directly from disk, as before.

### Helper Functions

Several context helper functions are format-aware:

- **Test entry counting**: For v1.2 specs, counts test entries from the
  loaded `test_spec.json` model (array length). For v1, counts `### TS-`
  headings in `test_spec.md`.

- **Existing code detection**: For v1.2 specs, checks `architecture.md` for
  file path references. For v1, checks `design.md`. This determines whether
  drift-review should run.

---

## Validation

The lint system routes spec validation by format, running the appropriate
validator for each discovered spec.

### v1.2 Validation

For v1.2 specs, validation delegates to `afspec.validate()`, which runs JSON
Schema validation and cross-file referential integrity checks. The results
are `ValidationError` objects that are mapped to agent-fox `Finding` objects
with identical fields (file, line, rule, message, severity), so the CLI
output format is unchanged — findings from `afspec` are indistinguishable
from findings produced by the legacy validators.

If `afspec` load or validation fails with an unexpected error, the system
emits a single error-severity `Finding` with rule `afspec-error` and
continues processing. Validation failures do not crash the pipeline.

### v1 Validation

For v1 markdown specs, the existing static validators continue to run
unchanged: structural checks, task structure, requirements format,
dependencies, completeness, traceability, and section schema. The two
validation paths are independent — v1 specs never touch `afspec`, and v1.2
specs never touch the markdown validators.

### AI Validation

The AI validation layer (vague criteria detection, stale dependency checking)
operates on the v1 markdown format. It is orthogonal to the format routing
and remains available for v1 specs via the `--ai` flag.

---

## Verification Checklist

The verification checklist extracts structured data from spec artifacts to
build a checklist of task completion states and requirement coverage. For
v1.2 specs, this extraction uses `afspec` models instead of regex parsing:

- **Task auditing**: Loads `tasks.json` via `afspec`, maps each subtask's
  `state` enum to a `checked`/`skipped` boolean, and produces
  `SubtaskAuditEntry` records.

- **Requirement coverage**: Loads `requirements.json` via `afspec`, extracts
  requirement IDs directly from the model's `requirements[*].id` field,
  and maps each ID to test file coverage.

This approach is more reliable than regex parsing because the structured data
has already been validated by `afspec` — there are no formatting ambiguities
to handle.

---

## Dual-Format Coexistence

The system supports both formats simultaneously through format detection and
routing at every pipeline stage. This dual-format model has three transition
phases:

1. **Dual-read** (current state) — The system reads both formats. New specs
   are created in v1.2 format. Existing completed or archived specs remain
   in v1 markdown and are not migrated.

2. **New-only** — All new spec creation uses v1.2. Old v1 specs are
   read-only. Discovery filters them from the active set.

3. **Cleanup** — Once no active specs use v1, the legacy markdown parsing
   code can be removed (corresponding to the planned Spec M in the
   migration plan). This phase has not been reached.

### What Happens to v1 Specs

V1 specs are not migrated. Completed and archived specs remain on disk in
their original format. They are excluded from `discover_specs()` by default,
so they do not appear in planning or validation runs. Their historical
content remains accessible through git history and direct file reads.

If a v1 spec needs to be reopened for additional work, it should be migrated
to v1.2 format. The system does not support extending v1 specs with new task
groups or requirements.

---

## Migration Status

The v1.2 migration was implemented through four specs (132-135), which
correspond to portions of the original 13-spec migration plan:

| agent-fox Spec | Covers | What it did |
|---|---|---|
| 132: afspec Integration | Spec F (partial) | Added `afspec` dependency, format detection, `SpecFormat` enum, `SpecInfo.format` field |
| 133: v1.2 Parsing Pipeline | Spec K (partial) | Added `parser_v12.py` mapper, planner format routing |
| 134: v1.2 Context Rendering | Spec K (partial), Spec J (partial) | Updated context assembly, verification checklist, and helper functions for v1.2 |
| 135: v1.2 Skill and Validation | Spec H (partial), Spec L (partial) | Added format-aware validation routing in lint, updated af-spec skill template |

### What Remains

The original plan described thirteen specs across six phases. The implemented
specs cover the essential integration layer — format detection, parsing,
context assembly, validation routing, and skill template updates. The
remaining work from the original plan includes:

- **JSON Schema definitions** (Spec A) — Deferred; `afspec` owns the schemas.
- **prd.md frontmatter and lifecycle** (Spec B) — Not yet implemented.
- **JSON data model parsers** (Specs C, D, E) — Deferred; `afspec` provides these.
- **Cross-file integrity validation** (Spec G) — Handled by `afspec.validate()`.
- **Mutation engine** (Spec I) — Not yet implemented.
- **Full renderer** (Spec J) — Partially covered by `afspec.render_individual()`.
- **Legacy code removal** (Spec M / agent-fox Spec 136) — Deferred; v1 code
  paths remain for backward compatibility.

The decision to use `afspec` as the foundation rather than building schemas,
parsers, and validators in-house significantly reduced the scope. Several
planned specs became unnecessary because `afspec` already provides the
functionality.

---

*Previous: [Knowledge System Architecture](05-knowledge-system-architecture.md)*
