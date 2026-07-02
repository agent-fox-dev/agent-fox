"""Collection mutation methods and ID generation helpers.

Since Pydantic models are immutable by convention, mutation methods return
new model instances rather than modifying the originals in place.
"""

from __future__ import annotations

import re
from typing import Optional

from afspec.models import (
    CorrectnessProperty,
    Criterion,
    EdgeCaseTest,
    ErrorHandlingEntry,
    ExecutionPath,
    PropertyTest,
    Requirement,
    Requirements,
    SmokeTest,
    Subtask,
    SubtaskState,
    TaskDependency,
    TaskGroup,
    Tasks,
    TestCase,
    TestSpec,
    TraceabilityEntry,
)

# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------


def _extract_trailing_number(id_str: str, prefix: str, suffix: str = "") -> int | None:
    """Extract the trailing numeric part from an ID string.

    For example, _extract_trailing_number("01-REQ-3", "01-REQ-") returns 3.
    With suffix: _extract_trailing_number("01-REQ-1.E2", "01-REQ-1.E") returns 2.
    """
    pattern = re.escape(prefix) + r"(\d+)" + re.escape(suffix) + "$"
    m = re.search(pattern, id_str)
    if m:
        return int(m.group(1))
    return None


def _max_id_number(ids: list[str], prefix: str, suffix: str = "") -> int:
    """Find the maximum numeric ID from a list of ID strings with a given prefix."""
    max_n = 0
    for id_str in ids:
        n = _extract_trailing_number(id_str, prefix, suffix)
        if n is not None and n > max_n:
            max_n = n
    return max_n


# ---------------------------------------------------------------------------
# Requirements mutations
# ---------------------------------------------------------------------------


def add_requirement(req: Requirements, r: Requirement) -> Requirements:
    """Add a requirement to the collection. Raises ValueError on duplicate ID."""
    for existing in req.requirements:
        if existing.id == r.id:
            raise ValueError(f"Duplicate requirement ID: {r.id}")
    return req.model_copy(update={"requirements": [*req.requirements, r]})


def get_requirement(req: Requirements, id: str) -> Optional[Requirement]:
    """Get a requirement by ID, or None if not found."""
    for r in req.requirements:
        if r.id == id:
            return r
    return None


def remove_requirement(req: Requirements, id: str) -> tuple[Requirements, bool]:
    """Remove a requirement by ID. Returns (updated_model, was_found)."""
    filtered = [r for r in req.requirements if r.id != id]
    found = len(filtered) < len(req.requirements)
    return req.model_copy(update={"requirements": filtered}), found


def set_glossary_entry(req: Requirements, term: str, definition: str) -> Requirements:
    """Insert or overwrite a glossary entry."""
    new_glossary = {**req.glossary, term: definition}
    return req.model_copy(update={"glossary": new_glossary})


def remove_glossary_entry(req: Requirements, term: str) -> tuple[Requirements, bool]:
    """Remove a glossary entry. Returns (updated_model, was_found)."""
    if term not in req.glossary:
        return req, False
    new_glossary = {k: v for k, v in req.glossary.items() if k != term}
    return req.model_copy(update={"glossary": new_glossary}), True


def add_correctness_property(req: Requirements, p: CorrectnessProperty) -> Requirements:
    """Add a correctness property. Raises ValueError on duplicate ID."""
    for existing in req.correctness_properties:
        if existing.id == p.id:
            raise ValueError(f"Duplicate correctness property ID: {p.id}")
    return req.model_copy(update={"correctness_properties": [*req.correctness_properties, p]})


def add_execution_path(req: Requirements, p: ExecutionPath) -> Requirements:
    """Add an execution path. Raises ValueError on duplicate ID."""
    for existing in req.execution_paths:
        if existing.id == p.id:
            raise ValueError(f"Duplicate execution path ID: {p.id}")
    return req.model_copy(update={"execution_paths": [*req.execution_paths, p]})


def add_error_handling(req: Requirements, e: ErrorHandlingEntry) -> Requirements:
    """Add an error handling entry. Raises ValueError on duplicate ID."""
    for existing in req.error_handling:
        if existing.id == e.id:
            raise ValueError(f"Duplicate error handling ID: {e.id}")
    return req.model_copy(update={"error_handling": [*req.error_handling, e]})


# ---------------------------------------------------------------------------
# Requirement mutations (criterion-level)
# ---------------------------------------------------------------------------


def add_criterion(r: Requirement, c: Criterion) -> Requirement:
    """Add a criterion to a requirement. Raises ValueError on duplicate ID."""
    for existing in r.acceptance_criteria:
        if existing.id == c.id:
            raise ValueError(f"Duplicate criterion ID: {c.id}")
    return r.model_copy(update={"acceptance_criteria": [*r.acceptance_criteria, c]})


def add_edge_case(r: Requirement, c: Criterion) -> Requirement:
    """Add an edge case to a requirement. Raises ValueError on duplicate ID."""
    for existing in r.edge_cases:
        if existing.id == c.id:
            raise ValueError(f"Duplicate edge case ID: {c.id}")
    return r.model_copy(update={"edge_cases": [*r.edge_cases, c]})


def get_criterion(r: Requirement, id: str) -> Optional[Criterion]:
    """Get a criterion by ID from acceptance_criteria or edge_cases."""
    for c in r.acceptance_criteria:
        if c.id == id:
            return c
    for c in r.edge_cases:
        if c.id == id:
            return c
    return None


# ---------------------------------------------------------------------------
# TestSpec mutations
# ---------------------------------------------------------------------------


def add_test_case(ts: TestSpec, tc: TestCase) -> TestSpec:
    """Add a test case. Raises ValueError on duplicate ID."""
    for existing in ts.test_cases:
        if existing.id == tc.id:
            raise ValueError(f"Duplicate test case ID: {tc.id}")
    return ts.model_copy(update={"test_cases": [*ts.test_cases, tc]})


def add_property_test(ts: TestSpec, pt: PropertyTest) -> TestSpec:
    """Add a property test. Raises ValueError on duplicate ID."""
    for existing in ts.property_tests:
        if existing.id == pt.id:
            raise ValueError(f"Duplicate property test ID: {pt.id}")
    return ts.model_copy(update={"property_tests": [*ts.property_tests, pt]})


def add_edge_case_test(ts: TestSpec, et: EdgeCaseTest) -> TestSpec:
    """Add an edge case test. Raises ValueError on duplicate ID."""
    for existing in ts.edge_case_tests:
        if existing.id == et.id:
            raise ValueError(f"Duplicate edge case test ID: {et.id}")
    return ts.model_copy(update={"edge_case_tests": [*ts.edge_case_tests, et]})


def add_smoke_test(ts: TestSpec, st: SmokeTest) -> TestSpec:
    """Add a smoke test. Raises ValueError on duplicate ID."""
    for existing in ts.smoke_tests:
        if existing.id == st.id:
            raise ValueError(f"Duplicate smoke test ID: {st.id}")
    return ts.model_copy(update={"smoke_tests": [*ts.smoke_tests, st]})


# ---------------------------------------------------------------------------
# Tasks mutations
# ---------------------------------------------------------------------------


def add_task_group(t: Tasks, g: TaskGroup) -> Tasks:
    """Add a task group. Raises ValueError on duplicate ID."""
    for existing in t.task_groups:
        if existing.id == g.id:
            raise ValueError(f"Duplicate task group ID: {g.id}")
    return t.model_copy(update={"task_groups": [*t.task_groups, g]})


def add_subtask(g: TaskGroup, s: Subtask) -> TaskGroup:
    """Add a subtask to a task group. Raises ValueError on duplicate ID."""
    for existing in g.subtasks:
        if existing.id == s.id:
            raise ValueError(f"Duplicate subtask ID: {s.id}")
    return g.model_copy(update={"subtasks": [*g.subtasks, s]})


def add_traceability_entry(t: Tasks, e: TraceabilityEntry) -> Tasks:
    """Add a traceability entry.

    Raises ValueError if a (requirement_id, test_spec_id) pair already exists.
    """
    for existing in t.traceability:
        if existing.requirement_id == e.requirement_id and existing.test_spec_id == e.test_spec_id:
            raise ValueError(f"duplicate traceability pair: ({e.requirement_id}, {e.test_spec_id})")
    return t.model_copy(update={"traceability": [*t.traceability, e]})


def add_dependency(t: Tasks, d: TaskDependency) -> Tasks:
    """Add a dependency entry."""
    return t.model_copy(update={"dependencies": [*t.dependencies, d]})


def transition_subtask(tasks: Tasks, subtask_id: str, target: SubtaskState) -> Tasks:
    """Transition a subtask to a new state.

    Finds the subtask by ID across all task groups, validates the
    transition via the state machine, and returns a new Tasks with
    the updated state.

    Raises ``KeyError`` if *subtask_id* is not found.
    Raises ``LifecycleError`` if the transition is illegal.
    """
    for group in tasks.task_groups:
        for i, subtask in enumerate(group.subtasks):
            if subtask.id == subtask_id:
                updated = subtask.transition_to(target)
                new_subtasks = [*group.subtasks[:i], updated, *group.subtasks[i + 1 :]]
                new_group = group.model_copy(update={"subtasks": new_subtasks})
                new_groups = [new_group if g.id == group.id else g for g in tasks.task_groups]
                return tasks.model_copy(update={"task_groups": new_groups})
    raise KeyError(f"Subtask not found: {subtask_id}")


def complete_subtask_states(tasks: Tasks, group_ids: list[int]) -> Tasks:
    """Mark all subtasks in the specified groups as done.

    Skips subtasks that are already done or dropped.
    Bypasses the state machine — intended for orchestrator use
    when a session has completed successfully.
    Returns a new Tasks with the updated states.
    """
    target_set = set(group_ids)
    new_groups = []
    for group in tasks.task_groups:
        if group.id not in target_set:
            new_groups.append(group)
            continue
        new_subtasks = []
        for subtask in group.subtasks:
            if subtask.state in (SubtaskState.DONE, SubtaskState.DROPPED):
                new_subtasks.append(subtask)
            else:
                new_subtasks.append(subtask.model_copy(update={"state": SubtaskState.DONE}))
        new_groups.append(group.model_copy(update={"subtasks": new_subtasks}))
    return tasks.model_copy(update={"task_groups": new_groups})


def reset_subtask_states(tasks: Tasks, group_ids: list[int]) -> Tasks:
    """Reset all subtasks in the specified groups to pending.

    Skips subtasks that are already pending or dropped.
    Returns a new Tasks with the updated states.
    """
    target_set = set(group_ids)
    new_groups = []
    for group in tasks.task_groups:
        if group.id not in target_set:
            new_groups.append(group)
            continue
        new_subtasks = []
        for subtask in group.subtasks:
            if subtask.state in (SubtaskState.PENDING, SubtaskState.DROPPED):
                new_subtasks.append(subtask)
            else:
                new_subtasks.append(subtask.model_copy(update={"state": SubtaskState.PENDING}))
        new_groups.append(group.model_copy(update={"subtasks": new_subtasks}))
    return tasks.model_copy(update={"task_groups": new_groups})


# ---------------------------------------------------------------------------
# ID generation helpers
# ---------------------------------------------------------------------------


def next_requirement_id(req: Requirements) -> str:
    """Return the next sequential requirement ID.

    Format: ``{spec_id}-REQ-{n}`` where n is one more than the current max.
    """
    ids = [r.id for r in req.requirements]
    prefix = f"{req.spec_id}-REQ-"
    n = _max_id_number(ids, prefix) + 1
    return f"{prefix}{n}"


def next_criterion_id(r: Requirement) -> str:
    """Return the next sequential criterion ID.

    Format: ``{requirement_id}.{n}`` where n is one more than the current max
    among acceptance_criteria.
    """
    ids = [c.id for c in r.acceptance_criteria]
    prefix = f"{r.id}."
    n = _max_id_number(ids, prefix) + 1
    return f"{prefix}{n}"


def next_edge_case_id(r: Requirement) -> str:
    """Return the next sequential edge case ID.

    Format: ``{requirement_id}.E{n}`` where n is one more than the current max
    among edge_cases.
    """
    ids = [c.id for c in r.edge_cases]
    prefix = f"{r.id}.E"
    n = _max_id_number(ids, prefix) + 1
    return f"{prefix}{n}"


def next_correctness_property_id(req: Requirements) -> str:
    """Return the next sequential correctness property ID.

    Format: ``{spec_id}-PROP-{n}``.
    """
    ids = [p.id for p in req.correctness_properties]
    prefix = f"{req.spec_id}-PROP-"
    n = _max_id_number(ids, prefix) + 1
    return f"{prefix}{n}"


def next_execution_path_id(req: Requirements) -> str:
    """Return the next sequential execution path ID.

    Format: ``{spec_id}-PATH-{n}``.
    """
    ids = [p.id for p in req.execution_paths]
    prefix = f"{req.spec_id}-PATH-"
    n = _max_id_number(ids, prefix) + 1
    return f"{prefix}{n}"


def next_error_handling_id(req: Requirements) -> str:
    """Return the next sequential error handling ID.

    Format: ``{spec_id}-ERR-{n}``.
    """
    ids = [e.id for e in req.error_handling]
    prefix = f"{req.spec_id}-ERR-"
    n = _max_id_number(ids, prefix) + 1
    return f"{prefix}{n}"


def next_test_case_id(ts: TestSpec) -> str:
    """Return the next sequential test case ID.

    Format: ``TS-{spec_id}-{n}``.
    """
    ids = [tc.id for tc in ts.test_cases]
    prefix = f"TS-{ts.spec_id}-"
    n = _max_id_number(ids, prefix) + 1
    return f"{prefix}{n}"


def next_property_test_id(ts: TestSpec) -> str:
    """Return the next sequential property test ID.

    Format: ``TS-{spec_id}-P{n}``.
    """
    ids = [pt.id for pt in ts.property_tests]
    prefix = f"TS-{ts.spec_id}-P"
    n = _max_id_number(ids, prefix) + 1
    return f"{prefix}{n}"


def next_edge_case_test_id(ts: TestSpec) -> str:
    """Return the next sequential edge case test ID.

    Format: ``TS-{spec_id}-E{n}``.
    """
    ids = [et.id for et in ts.edge_case_tests]
    prefix = f"TS-{ts.spec_id}-E"
    n = _max_id_number(ids, prefix) + 1
    return f"{prefix}{n}"


def next_smoke_test_id(ts: TestSpec) -> str:
    """Return the next sequential smoke test ID.

    Format: ``TS-{spec_id}-SMOKE-{n}``.
    """
    ids = [st.id for st in ts.smoke_tests]
    prefix = f"TS-{ts.spec_id}-SMOKE-"
    n = _max_id_number(ids, prefix) + 1
    return f"{prefix}{n}"
