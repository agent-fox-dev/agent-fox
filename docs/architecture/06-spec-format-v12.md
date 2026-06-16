# Spec Format v1.2: JSON-Based Specifications

## Purpose and Placement

This document describes the v1.2 spec format — a JSON-based specification
system used by agent-fox. It covers the file structure, the `afspec` library,
the parsing pipeline, context assembly, validation, and the verification
checklist.

For how specs are structured and validated in general, see
[Part 1: Spec Authoring](01-spec-authoring.md). For how parsed specs become
task graphs, see [Part 2: Planning](02-planning.md). For the original
migration plan, see
[Spec Format v2 Implementation Plan](../spec-format-v2-implementation-plan.md).

---

## Why v1.2

The original v1 markdown format served well for human authoring but created
friction at the machine boundary. Parsing relied on regex against markdown
headings, checkbox syntax, and table structures — fragile patterns that broke
on formatting variations and could not express richer semantics (subtask state
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

A spec directory contains four required files and one optional file:

| Artifact | Format | Role |
|---|---|---|
| `prd.md` | Markdown | Product requirements document. |
| `requirements.json` | JSON | Acceptance criteria with decomposed EARS fields, glossary, correctness properties, execution paths, and error handling. |
| `test_spec.json` | JSON | Structured test cases with typed entries (unit, property, edge case, smoke) and computed coverage. |
| `tasks.json` | JSON | Task groups with subtask state machine, dependencies, traceability, and verification. |
| `architecture.md` | Markdown | Optional free-form architecture documentation. |

### Key Properties

| Aspect | v1.2 (JSON) |
|---|---|
| Required files | 4 (`prd.md`, `requirements.json`, `test_spec.json`, `tasks.json`) |
| Optional files | 1 (`architecture.md`) |
| Validation | JSON Schema + cross-file integrity via `afspec` |
| Task state | State machine (`pending`, `queued`, `in_progress`, `done`, `dropped`) |
| Design content | `architecture.md` (optional) + correctness properties in `requirements.json` |
| Parsing | JSON deserialization into Pydantic models |

### What Stays the Same

The spec folder naming convention (`NN_snake_case_name`), the spec root
directory (`.agent-fox/specs/`), EARS syntax (decomposed into fields but same
six patterns), requirement ID format (`NN-REQ-M.S`), test spec ID format
(`TS-NN-N`), the task group concept, and the cross-spec dependency model are
all unchanged.

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

## Parsing Pipeline

The parsing pipeline converts `afspec` Pydantic models into agent-fox's
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

`parse_cross_deps_v12()` loads dependencies from `tasks.json` and maps each
`TaskDependency` to a `CrossSpecDep`. The direction convention is preserved:
`from_spec` is the current spec (the one declaring the dependency), `to_spec`
is the upstream spec being depended on.

### Format Invariance

The critical property of the parsing pipeline is format invariance: the graph
builder receives identical `TaskGroupDef` and `CrossSpecDep` types regardless
of the source spec format. No downstream consumer needs to know which format
was parsed. This is enforced by the mapper layer, which normalizes all
format-specific details into the common type.

---

## Context Assembly

When the engine prepares a coding session, it assembles spec content into the
agent's context window. For v1.2 specs, this means converting JSON artifacts
back to human-readable markdown — agents work with natural language, not raw
JSON.

The context assembly pipeline loads the spec via `afspec.load_spec()`, renders
each artifact to markdown via `afspec.render_individual()`, and wraps each
rendered block in a section header. If `architecture.md` exists, it is read
directly from disk (it is already markdown). The system falls back to raw file
reads on `afspec` load errors, providing graceful degradation.

### Helper Functions

Several context helper functions operate on the structured data:

- **Test entry counting**: Counts test entries from the loaded
  `test_spec.json` model (array length).

- **Existing code detection**: Checks `architecture.md` for file path
  references. This determines whether drift-review should run.

---

## Validation

Validation delegates to `afspec.validate()`, which runs JSON Schema validation
and cross-file referential integrity checks. The results are `ValidationError`
objects that are mapped to agent-fox `Finding` objects with identical fields
(file, line, rule, message, severity), so the CLI output format is
unchanged — findings from `afspec` are indistinguishable from findings
produced by internal validators.

If `afspec` load or validation fails with an unexpected error, the system
emits a single error-severity `Finding` with rule `afspec-error` and
continues processing. Validation failures do not crash the pipeline.

---

## Verification Checklist

The verification checklist extracts structured data from spec artifacts to
build a checklist of task completion states and requirement coverage. This
extraction uses `afspec` models instead of regex parsing:

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

*Previous: [Knowledge System Architecture](05-knowledge-system-architecture.md)*
