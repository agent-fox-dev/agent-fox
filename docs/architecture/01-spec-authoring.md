# Spec Authoring and Spec Structure

## Purpose and Placement

Specifications are the single source of truth that drives everything agent-fox
does. Every downstream activity — planning, coding, reviewing, verifying — traces
back to a spec. A spec is not documentation written after the fact; it is the
input artifact that the system consumes. If the spec is wrong, the code will be
wrong. If the spec is incomplete, the plan will have gaps. This is by design:
agent-fox treats specs as contracts, not suggestions.

This document covers how specs are structured, how the system discovers and
validates them, and how automated fixing keeps specs machine-readable without
requiring the author to memorize formatting rules. For how specs become
executable task graphs, see [Part 2: Planning](02-planning.md).

---

## The Spec as a Unit of Work

A specification maps to a single coherent feature, capability, or change. It
lives in a numbered directory under `.agent-fox/specs/` — for example,
`.agent-fox/specs/03_session_and_workspace/`. The numeric prefix establishes creation
order and provides a stable namespace for cross-spec references. The name after
the prefix is a snake_case descriptor chosen by the author.

For a detailed treatment of the v1.2 format, see
[Part 6: Spec Format v1.2](06-spec-format-v12.md).

### Spec Artifacts

Specs use the v1.2 JSON-based format with four required files and one optional
file:

| Artifact | Role |
|---|---|
| `prd.md` | Product requirements document. Defines what the feature is, why it exists, and what cross-spec dependencies it has. This is the human-facing narrative. |
| `requirements.json` | Acceptance criteria with decomposed EARS fields, glossary, correctness properties, execution paths, and error handling. |
| `test_spec.json` | Structured test cases with typed entries and computed coverage. |
| `tasks.json` | Task groups with subtask state machine, dependencies, and traceability. |
| `architecture.md` | Optional free-form architecture documentation. Absorbs the role of the former `design.md`. |

The v1.2 format moves structured data into schema-validated JSON while keeping
narrative content in markdown. The `afspec` library (from af-core) provides the
data models, validation, and rendering. See
[Part 6](06-spec-format-v12.md) for details on format detection, parsing,
and validation.

### Traceability

The artifacts form a closed traceability chain: requirements define what must be
true, test specs define how to verify it, tasks define how to build it,
architecture defines the shape of the solution, and the PRD provides the
motivation. The validation system enforces this chain — untraced requirements,
orphaned test entries, and missing coverage are all flagged.

### Why Multiple Artifacts Instead of One

A single document would be simpler to author but harder to consume
programmatically. The planner only needs tasks and the PRD. The Coder
needs requirements, architecture, test specs, and tasks. The Verifier needs
requirements. Splitting by concern means each consumer reads only what it needs,
and validation rules can target specific artifacts without parsing a monolith.

The separation also enables independent evolution. An architecture change does
not require re-parsing the task list. A new requirement can be added and traced
through test specs and tasks without touching the PRD.

---

## Requirement Identifiers and Traceability

Every requirement carries a structured identifier of the form `NN-REQ-M.S`,
where `NN` is the spec's numeric prefix, `M` is the requirement number, and `S`
is the sub-requirement number. Error-handling requirements use the variant
`NN-REQ-M.EN`. These identifiers are the primary key for the traceability chain.

The chain works as follows:

1. `requirements.json` defines `NN-REQ-M.S` entries with acceptance criteria.
2. `test_spec.json` contains `TS-NN-N` entries that reference requirement IDs.
   Correctness properties are covered by `TS-NN-PN` entries.
3. `tasks.json` contains traceability entries mapping requirement IDs to task
   groups and test entries.
4. `test_spec.json` contains computed coverage mapping requirement IDs to test
   entries.

Validation enforces every link in this chain. An untraced requirement (present
in `requirements.json` but absent from `test_spec.json`) is flagged. An orphaned
test entry (present in `test_spec.json` but not referenced in `tasks.json`) is
flagged. A coverage gap that omits a requirement is flagged. These are
warnings, not errors — the system distinguishes between structural problems that
prevent planning (errors) and quality gaps that should be addressed (warnings).

---

## Task Groups and the Tasks Artifact

`tasks.json` is the artifact the planner consumes directly. It defines an ordered
list of task groups, each with a numeric index, title, completion state, and zero
or more subtasks. Groups execute sequentially within a spec by default — group 2
depends on group 1. Cross-spec dependencies are declared in `tasks.json` and
override this default ordering.

### Group 1 Convention

By convention, task group 1 writes failing tests from `test_spec.json` without
implementing any production code. Subsequent groups implement code to make those
tests pass. This test-first discipline is enforced by the Coder's prompt
template, not by the spec structure itself — the spec system is agnostic to
this convention.

### Optional Groups

A group can be marked optional with an asterisk prefix in its title. Optional
groups are included in normal plans but excluded in fast mode, where the planner
removes them and rewires their dependencies so predecessors connect directly to
successors.

### Archetype Tags

A task group can carry an archetype tag — for example, `[archetype: skeptic]` —
that overrides the default assignment of "coder" for that group. This is useful
when a group represents a review or validation step that should be handled by a
specific agent type. The tag is the highest-priority assignment mechanism,
overriding both the builder's defaults and the automatic injection rules.

### Verification Subtasks

Each group is expected to contain a verification subtask (conventionally
numbered `N.V`). This subtask signals to the Coder that it should run
the quality gate and confirm the group's work before marking it complete.
Missing verification subtasks are flagged during validation and can be
auto-fixed.

---

## Dependency Declarations

Cross-spec dependencies are declared in `tasks.json` using structured dependency
entries. Two granularity levels are supported:

The **standard format** declares spec-level dependencies: "this spec depends on
that spec." It uses sentinel group numbers that resolve to the first or last
group during graph construction. This format is simple but coarse — it forces
full serialization between specs.

The **group-level format** declares precise group-to-group dependencies: "group
3 of this spec depends on group 2 of that spec." This enables finer-grained
parallelism because only the specific dependent groups are sequenced, not entire
specs.

Validation encourages the group-level format. If a spec uses the standard
format, a warning is emitted recommending conversion to group-level
declarations.

### Dependency Identifiers

The group-level format includes a "relationship" field where authors describe
what the dependency is about, often referencing specific identifiers (function
names, interfaces, data types) in backtick-quoted code spans.

---

## Spec Discovery

Discovery is the entry point for both planning and linting. The system scans
`.agent-fox/specs/` for subdirectories matching the `NN_name` pattern (numeric prefix,
underscore, descriptive name). Each matching directory becomes a `SpecInfo`
record carrying the spec's name, numeric prefix, path, and which core artifacts
are present.

Discovery is deterministic: specs are sorted by numeric prefix, producing a
stable ordering across runs. An optional filter can restrict operations to a
single spec by name.

The system requires at least one discoverable spec. If the `.agent-fox/specs/` directory
is empty or contains no matching subdirectories, a hard error is raised. Specs
without a `tasks.json` are discovered but cannot be planned — they may exist as
reference material or work-in-progress.

---

## Validation

Validation delegates to `afspec.validate()`, which runs JSON Schema validation
and cross-file referential integrity checks. Results are mapped to agent-fox
`Finding` objects so the CLI output format is unchanged. See
[Part 6: Spec Format v1.2](06-spec-format-v12.md#validation) for details.

### Severity Model

Findings have three severity levels:

- **Error**: Structural problems that prevent planning or execution. Missing
  core files, broken dependency references, circular dependencies. Any error
  causes the lint command to exit with a non-zero code.
- **Warning**: Quality gaps that should be addressed but do not block execution.
  Missing verification subtasks, untraced requirements, coarse dependencies.
- **Hint**: Stylistic or minor suggestions. Inconsistent formatting, missing
  optional sections.

---

## Auto-Fixing

The fixer system can mechanically correct many validation findings. Fixable
rules fall into two categories:

**Structural fixers** add missing sections, tables, and subtasks. Groups
without verification subtasks get one appended.

**Normalization fixers** correct formatting inconsistencies.

After all fixes are applied, validation runs again to confirm the fixes resolved
the findings. This re-validation pass ensures fixers do not introduce new
problems.

The fix pipeline is idempotent by design. Running the fixer twice on the same
spec produces the same result as running it once. Fixers read the current file
state, compute the necessary change, and write back — they do not accumulate
state across invocations.

---

## The Lint Command

The `agent-fox lint-specs` command ties discovery, validation, and fixing into
a single workflow. It discovers specs, filters out fully-implemented ones
(all task groups marked complete) unless `--all` is specified, runs validation,
and optionally applies fixes with `--fix`.

The exit code reflects the worst finding: zero if no errors, non-zero if any
error-severity finding remains after fixing. This makes the lint command usable
as a CI gate — a spec with structural problems blocks the pipeline.

Fully-implemented specs are excluded from linting by default because their
specs have served their purpose and may contain stale references to code that
has since evolved. The `--all` flag overrides this for auditing purposes.

---

## Authoring Workflow

The typical authoring workflow is:

1. Create a numbered directory under `.agent-fox/specs/` with the spec artifact
   files. The `/af-spec` skill generates the full v1.2 package (PRD + three
   JSON files) from a PRD, a GitHub issue URL, or a plain-English description.
2. Run `agent-fox lint-specs` to validate. This runs `afspec` schema and
   integrity checks. Fix errors manually or with `--fix`. Address warnings as
   appropriate.
3. Run `agent-fox plan` to build the task graph (see
   [Part 2: Planning](02-planning.md)).

Specs are immutable once planning begins. If implementation reveals that a spec
is wrong, the convention is to create an erratum in `docs/errata/` rather than
modifying the spec directly. This preserves the spec as a historical record of
intent and makes divergences explicit.

---

*Next: [Planning — From Specs to Task Graphs](02-planning.md)*
