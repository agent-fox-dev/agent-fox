# Spec Format v1.3: JSON-Based Specifications

## Purpose and Placement

This document describes the v1.3 spec format — a JSON-based specification
system used by agent-fox. It covers the file structure, the `afspec` library,
the parsing pipeline, context assembly, validation, and the verification
checklist.

For the authoritative format specification, see
[Spec Format Specification v1.3](../spec-format_v1.3.md). For how specs are
structured and validated in general, see
[Part 1: Spec Authoring](01-spec-authoring.md). For how parsed specs become
task graphs, see [Part 2: Planning](02-planning.md).

---

## Why v1.3

The v1.2 format moved structured data from markdown into JSON files validated
against schemas, while keeping human-authored narrative content in markdown.
The v1.3 format evolves this foundation with several additions:

- **PRD frontmatter with lifecycle management.** The PRD now carries
  structured YAML frontmatter with fields for spec identity (`spec_id`,
  `spec_name`), lifecycle state (`status`), provenance (`source`, `owner`),
  and an `intent_hash` that protects the PRD's Intent section from
  accidental modification after the spec transitions from draft to active.

- **Lifecycle states.** Specs progress through `draft → active → sealed →
  superseded/archived`, with mutation rules enforced per state. The library
  rejects modifications to sealed or superseded specs.

- **External APIs section.** `requirements.json` gains an optional
  `external_apis` array for verified function/method signatures from
  external library dependencies, preventing requirements from being
  written against assumed signatures.

- **Expanded subtask state machine.** Subtasks now support five states
  (`pending`, `queued`, `in_progress`, `done`, `pending_reevaluation`,
  `dropped`) with enforced transitions, replacing the simpler
  checked/unchecked model.

- **Task group kinds.** Task groups carry a `kind` field (`tests`,
  `standard`, `checkpoint`, `wiring_verification`) that drives archetype
  assignment — `checkpoint` groups receive the Gate archetype, and the
  final `wiring_verification` group enforces integration checks.

- **Structured verification.** Each task group has an explicit
  `verification` subtask with enumerated checks, replacing the convention
  of a final subtask with a verification label.

- **Traceability entries.** `tasks.json` includes a `traceability` array
  that provides bidirectional links from requirements through test specs
  and tasks to executable test file paths.

---

## File Structure

A spec directory contains four required files and one optional file:

| Artifact | Format | Role |
|---|---|---|
| `prd.md` | Markdown with YAML frontmatter | Product requirements document with lifecycle metadata. |
| `requirements.json` | JSON | Acceptance criteria with decomposed EARS fields, glossary, correctness properties, execution paths, error handling, and optional external APIs. |
| `test_spec.json` | JSON | Structured test cases with typed entries (unit, integration, property, edge case, smoke) and computed coverage. |
| `tasks.json` | JSON | Task groups with subtask state machine, dependencies, traceability, and verification. |
| `architecture.md` | Markdown | Optional free-form architecture documentation. |

### Key Properties

| Aspect | v1.3 (JSON) |
|---|---|
| Required files | 4 (`prd.md`, `requirements.json`, `test_spec.json`, `tasks.json`) |
| Optional files | 1 (`architecture.md`) |
| PRD metadata | YAML frontmatter with lifecycle state, intent hash, provenance |
| Validation | JSON Schema + cross-file integrity via `afspec` |
| Task state | State machine (`pending`, `queued`, `in_progress`, `done`, `pending_reevaluation`, `dropped`) |
| Task group kinds | `tests`, `standard`, `checkpoint`, `wiring_verification` |
| Design content | `architecture.md` (optional) + correctness properties and execution paths in `requirements.json` |
| Parsing | JSON deserialization into Pydantic models; YAML frontmatter parsing for PRD |

### What Stays the Same

The spec folder naming convention (`NN_snake_case_name`), the spec root
directory (`.agent-fox/specs/`), EARS syntax (decomposed into fields but
same six patterns), requirement ID format (`NN-REQ-M.S`), test spec ID
format (`TS-NN-N`), the task group concept, and the cross-spec dependency
model are all unchanged from v1.2.

---

## The `afspec` Library

`afspec` is the library that provides the canonical data models and
operations for the v1.3 format. It lives in `packages/afspec/` within
the monorepo. Agent-fox depends on it as a runtime dependency and uses
three entry points:

- **`afspec.load_spec(spec_dir)`** — Parses all JSON artifacts and the
  PRD frontmatter in a spec directory and returns a unified `Spec` object
  containing Pydantic models for requirements, test specs, tasks, and
  dependencies.

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
  states (`pending`, `queued`, `in_progress`, `pending_reevaluation`) map to
  `completed=False`. `dropped` subtasks are excluded from completion
  calculation.

- **Group completion**: A `TaskGroupDef` is marked completed when all
  non-dropped subtasks are in the `DONE` state. A group where every subtask
  is dropped is vacuously complete.

- **Group kind**: The `kind` field is preserved and used by the graph builder
  to assign archetypes — `checkpoint` groups receive the `gate` archetype,
  and `wiring_verification` groups enforce integration checks.

- **Body rendering**: The group body is rendered as a markdown checklist of
  subtasks, matching the format the graph builder and context system expect.

- **Archetype**: Set to `None` for v1.3 specs. The v1.3 format does not use
  inline archetype tags; kind-based assignment handles archetype selection.

### Dependency Parsing

`parse_cross_deps_v12()` loads dependencies from `tasks.json` and maps each
`TaskDependency` to a `CrossSpecDep`. The direction convention is preserved:
`from_spec` is the current spec (the one declaring the dependency), `to_spec`
is the upstream spec being depended on. Sentinel dependencies (where
`from_group` is 0, indicating the upstream spec is not yet planned) are
flagged for validation warnings.

### Format Invariance

The critical property of the parsing pipeline is format invariance: the graph
builder receives identical `TaskGroupDef` and `CrossSpecDep` types regardless
of the source spec format. No downstream consumer needs to know which format
was parsed. This is enforced by the mapper layer, which normalizes all
format-specific details into the common type.

---

## Context Assembly

When the engine prepares a coding session, it assembles spec content into the
agent's context window. For v1.3 specs, this means converting JSON artifacts
back to human-readable markdown — agents work with natural language, not raw
JSON.

The context assembly pipeline loads the spec via `afspec.load_spec()` and
renders each artifact to markdown. When a `task_group` is provided, the
pipeline uses `afspec.render_individual_scoped()` to filter content to the
active group: only requirements and test cases referenced by the group's
subtasks (via `requirement_refs` and `test_spec_refs`) are rendered in full,
non-target groups appear as one-line summaries with completion counts, and a
`## Spec Overview` section lists all requirement IDs for orientation.
Without a `task_group`, the pipeline falls back to `afspec.render_individual()`
for unscoped (full) rendering. Each rendered block is wrapped in a section
header. If `architecture.md` exists, it is read directly from disk (it is
already markdown). The system falls back to raw file reads on `afspec` load
errors, providing graceful degradation.

### Helper Functions

Several context helper functions operate on the structured data:

- **Test entry counting**: Counts test entries from the loaded
  `test_spec.json` model (array length). Used to determine whether
  audit-review should be injected (requires a minimum number of test
  entries).

- **Existing code detection**: Checks `architecture.md` for file path
  references. This determines whether drift-review should run — if the spec
  references no files that currently exist in the repository, drift-review
  is skipped.

---

## Validation

Validation delegates to `afspec.validate()`, which runs JSON Schema validation
and cross-file referential integrity checks. The results are `ValidationError`
objects that are mapped to agent-fox `Finding` objects with identical fields
(file, line, rule, message, severity), so the CLI output format is
unchanged — findings from `afspec` are indistinguishable from findings
produced by internal validators.

Cross-file integrity rules include:

- Every `requirement_id` referenced in test specs, task traceability, and
  error handling must exist in `requirements.json`.
- Every requirement and edge case must be covered by a test case.
- Every correctness property must be referenced by a property test.
- Every execution path must be referenced by a smoke test.
- `spec_id` and `spec_name` must be consistent across all four required files
  and the PRD frontmatter.
- Glossary terms (backtick-quoted tokens in requirements fields) must have
  glossary entries.
- Traceability entries must not have duplicate `(requirement_id,
  test_spec_id)` pairs.

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

## Night-Shift In-Memory Specs

The night-shift fix pipeline generates lightweight in-memory specs from
platform issues rather than writing spec files to disk. When triage produces
acceptance criteria, `build_afspec_from_triage()` constructs a full `afspec`
`Spec` object with requirements, test specs, and a single task group. This
object is rendered via `afspec.render_individual()` and injected into coder
and reviewer prompts, giving fix sessions the same structured context that
spec-driven sessions receive. See
[Part 4: Night-Shift Mode](04-night-shift.md) for details.

---

*Previous: [Knowledge System Architecture](05-knowledge-system-architecture.md)*
