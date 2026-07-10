# afspec

Python library for the agent-fox specification format (v1.2). Load, validate,
mutate, and render spec packs — the structured artifact format used by
[agent-fox](https://github.com/agent-fox-dev/agent-fox) for spec-driven
development.

Requires Python 3.12+. Dependencies: `pydantic`, `PyYAML`, `jsonschema`.

## Installation

Install from the agent-fox monorepo via git:

```bash
pip install "afspec @ git+https://github.com/agent-fox-dev/agent-fox.git#subdirectory=packages/afspec"
```

Pin to a release tag:

```bash
pip install "afspec @ git+https://github.com/agent-fox-dev/agent-fox.git@v4.2.0#subdirectory=packages/afspec"
```

In `pyproject.toml`:

```toml
[project]
dependencies = [
    "afspec @ git+https://github.com/agent-fox-dev/agent-fox.git@v4.2.0#subdirectory=packages/afspec",
]
```

## Spec Format Overview

A spec pack is a directory (`NN_name/`) containing four required artifacts and
one optional:

| File | Format | Purpose |
|------|--------|---------|
| `prd.md` | Markdown with YAML frontmatter | Product requirements — intent, goals, tech stack, design |
| `requirements.json` | JSON (schema v1) | EARS-syntax acceptance criteria, correctness properties, execution paths, external API contracts, error handling, glossary |
| `test_spec.json` | JSON (schema v1) | Test contracts — unit, property, edge-case, and smoke tests |
| `tasks.json` | JSON (schema v1) | Implementation plan with subtask states, dependencies, test commands, traceability |
| `architecture.md` | Markdown (optional) | Detailed architecture for complex specs |

## Quick Start

```python
from pathlib import Path
from afspec import load_spec, validate, discover_specs, Status

# Load a single spec
spec = load_spec(Path(".agent-fox/specs/01_foundation"))
print(spec.prd.frontmatter.title)       # "Foundation"
print(spec.prd.frontmatter.status)      # Status.DRAFT
print(len(spec.requirements.requirements))  # number of requirements
print(len(spec.tasks.task_groups))       # number of task groups

# Validate a spec (schema + cross-file consistency)
result = validate(spec)
if result.errors:
    for err in result.errors:
        print(f"{err.file}:{err.path} — {err.message}")

# Discover all specs in a directory
metas = discover_specs(Path(".agent-fox/specs"))
for meta in metas:
    print(f"{meta.spec_id}: {meta.spec_name} ({meta.status})")
```

## API Reference

All symbols are importable from the top-level package: `from afspec import <symbol>`.

### I/O

| Symbol | Signature | Description |
|--------|-----------|-------------|
| `load_spec` | `(dir: str \| Path) -> Spec` | Load all artifacts from a spec directory. Raises `LoadError`. |
| `save` | `(spec: Spec, dir: str \| Path) -> None` | Atomic save with lifecycle guards. Raises `SaveError`, `LifecycleError`. |
| `marshal_json` | `(model: object) -> str` | Deterministic JSON serialization (2-space indent, sorted dicts). |

### Core Types (Pydantic Models)

| Type | Key Fields |
|------|------------|
| `Spec` | `prd: PRDDocument`, `requirements: Requirements`, `test_spec: TestSpec`, `tasks: Tasks`, `architecture: str \| None` |
| `PRDDocument` | `frontmatter: PRDFrontmatter`, `body: str` |
| `PRDFrontmatter` | `spec_id`, `spec_name`, `title`, `status: Status`, `created_at`, `updated_at`, `owner`, `source`, `tags`, `intent_hash` |
| `Requirements` | `spec_id`, `spec_name`, `introduction`, `glossary`, `requirements: list[Requirement]`, `correctness_properties`, `execution_paths`, `error_handling`, `external_apis` |
| `Requirement` | `id`, `title`, `user_story: UserStory`, `acceptance_criteria: list[Criterion]`, `edge_cases: list[Criterion]` |
| `Criterion` | `id`, `ears_pattern: EARSPattern`, `system`, `action`, `return_contract`, plus pattern-specific fields |
| `TestSpec` | `spec_id`, `spec_name`, `test_cases`, `property_tests`, `edge_case_tests`, `smoke_tests`, `coverage: Coverage` |
| `TestCase` | `id`, `requirement_id`, `kind`, `description`, `preconditions`, `input`, `expected`, `assertion_pseudocode` |
| `Tasks` | `spec_id`, `spec_name`, `test_commands: TestCommands`, `dependencies`, `task_groups: list[TaskGroup]`, `traceability` |
| `TaskGroup` | `id: int`, `kind: TaskGroupKind`, `title`, `subtasks: list[Subtask]`, `verification` |
| `Subtask` | `id`, `title`, `details`, `test_spec_refs`, `requirement_refs`, `state: SubtaskState`, `optional: bool` |
| `SpecMeta` | `spec_id`, `spec_name`, `status: Status`, `dir: Path` — lightweight metadata for discovery |
| `UserStory` | `role`, `goal`, `benefit` |
| `ExternalAPI` | `package`, `version`, `symbols: list[ExternalAPISymbol]` |
| `ExternalAPISymbol` | `name`, `import_path`, `signature`, `notes` |
| `CorrectnessProperty` | `id`, `title`, `for_any`, `invariant`, `validates` |
| `ExecutionPath` | `id`, `title`, `steps: list[PathStep]` |
| `PathStep` | `actor`, `action` |
| `ErrorHandlingEntry` | `id`, `condition`, `behavior`, `requirement_id` |
| `PropertyTest` | `id`, `property_id`, `validates`, `description`, `for_any_strategy`, `invariant_check` |
| `EdgeCaseTest` | `id`, `requirement_id`, `kind`, `description`, `preconditions`, `input`, `expected`, `assertion_pseudocode` |
| `SmokeTest` | `id`, `execution_path_id`, `description`, `trigger`, `real_components`, `mockable`, `expected_effects` |
| `VerificationSubtask` | `id`, `checks: list[str]` |
| `TaskDependency` | `depends_on_spec`, `from_group`, `to_group`, `relationship`, `sentinel` |
| `TraceabilityEntry` | `requirement_id`, `test_spec_id`, `task_id`, `test_path` |

### Enums

| Enum | Values |
|------|--------|
| `Status` | `draft`, `active`, `sealed`, `superseded`, `archived` |
| `EARSPattern` | `ubiquitous`, `event_driven`, `complex_event`, `state_driven`, `unwanted`, `optional` |
| `SubtaskState` | `pending`, `queued`, `in_progress`, `done`, `pending_reevaluation`, `dropped` |
| `TaskGroupKind` | `tests`, `standard`, `checkpoint`, `wiring_verification` |

### Validation

| Symbol | Signature | Description |
|--------|-----------|-------------|
| `validate` | `(spec: Spec) -> ValidationResult` | Full validation (schema + cross-file). |
| `validate_schema` | `(spec: Spec) -> ValidationResult` | Schema-only validation against bundled JSON Schemas. |
| `validate_cross_file` | `(spec: Spec) -> ValidationResult` | Cross-file consistency (dangling refs, coverage gaps). |
| `ValidationResult` | `.errors: list[ValidationError]`, `.warnings: list[ValidationWarning]` | Validation outcome. |
| `ValidationError` | `.file`, `.path`, `.message`, `.rule` | A validation failure. |
| `ValidationWarning` | `.message`, `.entity_id` | A non-blocking warning. |

### Lifecycle

| Symbol | Signature | Description |
|--------|-----------|-------------|
| `transition` | `(spec: Spec, target: Status) -> Spec` | Transition lifecycle state. Raises `LifecycleError` for invalid transitions. |
| `valid_transition` | `(current: Status, target: Status) -> bool` | Check if a transition is allowed. |
| `supersede` | `(old: Spec, new_spec: Spec) -> tuple[Spec, Spec]` | Supersede one spec with another. |
| `move_to_archive` | `(spec_dir: Path, archive_dir: Path) -> None` | Move a spec directory to the archive. |

### Discovery and Dependencies

| Symbol | Signature | Description |
|--------|-----------|-------------|
| `discover_specs` | `(specs_dir: Path) -> list[SpecMeta]` | Find all spec directories and return lightweight metadata. |
| `build_dependency_graph` | `(metas: list[SpecMeta], specs: dict[str, Spec]) -> DependencyGraph` | Build the inter-spec dependency graph. |
| `DependencyGraph` | `.topological_order() -> list[str]`, `.dependencies(spec_id) -> list[DependencyEdge]` | Queryable dependency graph. |

### Subtask Mutation

| Symbol | Signature | Description |
|--------|-----------|-------------|
| `transition_subtask` | `(spec: Spec, group_id: int, subtask_id: str, target: SubtaskState) -> Spec` | Transition a single subtask's state. |
| `complete_subtask_states` | `(spec: Spec, group_id: int) -> Spec` | Mark all subtasks in a group as `done`. |
| `reset_subtask_states` | `(spec: Spec, group_id: int) -> Spec` | Reset all subtasks in a group to `pending`. |

### Rendering

| Symbol | Signature | Description |
|--------|-----------|-------------|
| `render_requirements` | `(spec: Spec) -> str` | Render requirements as Markdown. |
| `render_test_spec` | `(spec: Spec) -> str` | Render test spec as Markdown. |
| `render_tasks` | `(spec: Spec) -> str` | Render tasks as Markdown. |
| `render_combined` | `(spec: Spec) -> str` | Render all artifacts as a single Markdown document. |
| `render_individual` | `(spec: Spec) -> dict[str, str]` | Render each artifact separately. |
| `render_ears_sentence` | `(criterion: Criterion) -> str` | Render a single EARS criterion as a natural-language sentence. |

### EARS Criterion Builders

Convenience constructors for creating `Criterion` objects:

`ubiquitous_criterion`, `event_driven_criterion`, `complex_event_criterion`,
`state_driven_criterion`, `unwanted_criterion`, `optional_criterion`

### Exceptions

| Exception | Base | When |
|-----------|------|------|
| `SpecError` | `Exception` | Base for all afspec errors |
| `LoadError` | `SpecError` | Missing files, malformed JSON/YAML |
| `SaveError` | `SpecError` | Write failures |
| `LifecycleError` | `SpecError` | Invalid lifecycle transitions, mutation guards |
| `IntentError` | `SpecError` | Intent hash mismatch on active specs |
| `BootstrapError` | `SpecError` | Spec bootstrapping failures |

### Other

| Symbol | Description |
|--------|-------------|
| `schemas` | `() -> dict[str, bytes]` — Returns bundled JSON Schema files for external validation. |
| `compute_intent_hash` | `(spec: Spec) -> str` — SHA-256 of the requirements content for change detection. |
| `compute_coverage` | `(spec: Spec) -> Coverage` — Compute test coverage metrics from traceability data. |
| `create_spec` | `(name, title, ...) -> Spec` — Create a new spec with default structure. |
| `BootstrapSpec` | Dataclass for bootstrapping new specs from templates. |
