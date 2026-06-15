# Requirements Document

## Introduction

This spec adds a v1.2 parsing pipeline that converts afspec Pydantic models
into the existing agent-fox dataclasses (`TaskGroupDef`, `SubtaskDef`,
`CrossSpecDep`) and updates the graph planner to route v1.2 specs through
the new parser.

## Glossary

| Term | Definition |
|------|-----------|
| afspec | Python library from af-core that provides models, validation, rendering, and discovery for the v1.2 spec format |
| TaskGroupDef | Frozen dataclass in `agent_fox/spec/parser.py` representing a parsed top-level task group |
| SubtaskDef | Frozen dataclass in `agent_fox/spec/parser.py` representing a single nested subtask |
| CrossSpecDep | Frozen dataclass in `agent_fox/spec/parser.py` representing a cross-spec dependency declaration |
| afspec.TaskGroup | Pydantic model from afspec representing a v1.2 task group |
| afspec.Subtask | Pydantic model from afspec representing a v1.2 subtask |
| afspec.TaskDependency | Pydantic model from afspec representing a v1.2 cross-spec dependency |
| SubtaskState | Enum from afspec indicating subtask lifecycle state (PENDING, DONE, DROPPED, etc.) |
| TaskGroupKind | Enum from afspec indicating the kind of task group (STANDARD, TESTS, CHECKPOINT, etc.) |
| SpecFormat | Enum from `agent_fox/spec/discovery.py` distinguishing v1 (markdown) from v1.2 (JSON) |
| SpecInfo | Dataclass from `agent_fox/spec/discovery.py` carrying metadata about a discovered spec folder |
| mapper | A function that converts one afspec Pydantic model instance to one agent-fox dataclass instance |

## Requirements

### Requirement 1: Subtask Mapping

**User Story:** As the graph builder, I want v1.2 subtasks converted to
`SubtaskDef` instances, so that task graphs are built identically regardless
of spec format.

#### Acceptance Criteria

1. [133-REQ-1.1] WHEN a v1.2 `Subtask` is mapped, THE system SHALL produce
   a `SubtaskDef` with `id` equal to the source `Subtask.id`, `title` equal
   to `Subtask.title`, and `completed` equal to `True` if and only if
   `Subtask.state == SubtaskState.DONE`.
2. [133-REQ-1.2] WHEN a v1.2 `Subtask` has `state` other than `DONE`, THE
   system SHALL set `SubtaskDef.completed` to `False`.

#### Edge Cases

1. [133-REQ-1.E1] IF a `Subtask` has `state == SubtaskState.DROPPED`, THEN
   THE mapper SHALL still produce a `SubtaskDef` with `completed = False`
   AND the subtask SHALL be excluded from the parent group's completion
   calculation.

### Requirement 2: Task Group Mapping

**User Story:** As the graph builder, I want v1.2 task groups converted to
`TaskGroupDef` instances, so that node creation works without changes.

#### Acceptance Criteria

1. [133-REQ-2.1] WHEN a v1.2 `TaskGroup` is mapped, THE system SHALL produce
   a `TaskGroupDef` with `number` equal to `TaskGroup.id`, `title` equal to
   `TaskGroup.title`, `optional` set to `False`, and `archetype` set to
   `None`.
2. [133-REQ-2.2] WHEN all non-dropped subtasks in a `TaskGroup` have
   `state == SubtaskState.DONE`, THE system SHALL set
   `TaskGroupDef.completed` to `True`.
3. [133-REQ-2.3] WHEN any non-dropped subtask in a `TaskGroup` has a state
   other than `DONE`, THE system SHALL set `TaskGroupDef.completed` to
   `False`.
4. [133-REQ-2.4] WHEN a `TaskGroup` is mapped, THE system SHALL populate
   `TaskGroupDef.body` with a markdown rendering of the task group content
   including subtask titles and details.

#### Edge Cases

1. [133-REQ-2.E1] IF a `TaskGroup` contains only dropped subtasks, THEN
   THE system SHALL set `TaskGroupDef.completed` to `True` (vacuously
   complete).

### Requirement 3: Cross-Spec Dependency Mapping

**User Story:** As the graph builder, I want v1.2 dependency declarations
converted to `CrossSpecDep` instances, so that cross-spec edges are created
correctly.

#### Acceptance Criteria

1. [133-REQ-3.1] WHEN a v1.2 `TaskDependency` is mapped for a spec named
   `current_spec`, THE system SHALL produce a `CrossSpecDep` with
   `from_spec` equal to `TaskDependency.depends_on_spec`, `to_spec` equal
   to `current_spec`, `from_group` equal to `TaskDependency.to_group`,
   and `to_group` equal to `TaskDependency.from_group`.

#### Edge Cases

1. [133-REQ-3.E1] IF a spec has no `TaskDependency` entries, THEN
   `parse_cross_deps_v12()` SHALL return an empty list.

### Requirement 4: Planner Routing

**User Story:** As the orchestrator, I want the planner to automatically
use the correct parser based on spec format, so that v1.2 specs are
integrated into the task graph.

#### Acceptance Criteria

1. [133-REQ-4.1] WHEN `build_plan()` encounters a `SpecInfo` with
   `format == SpecFormat.V1_2_JSON`, THE system SHALL call
   `parse_tasks_v12()` to parse task groups AND call
   `parse_cross_deps_v12()` to parse dependencies.
2. [133-REQ-4.2] WHEN `build_plan()` encounters a `SpecInfo` with
   `format == SpecFormat.V1_2_JSON`, THE system SHALL NOT call the
   markdown parser functions (`parse_tasks`, `parse_cross_deps`).

#### Edge Cases

1. [133-REQ-4.E1] IF `afspec.load_spec()` raises an error for a v1.2 spec,
   THEN `build_plan()` SHALL propagate the error to the caller without
   catching it.
