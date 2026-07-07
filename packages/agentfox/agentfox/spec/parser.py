"""v1.2 spec parser: map afspec Pydantic models to agent-fox dataclasses.

Converts afspec ``Subtask``, ``TaskGroup``, and ``TaskDependency`` models
into the existing ``SubtaskDef``, ``TaskGroupDef``, and ``CrossSpecDep``
dataclasses consumed by the graph builder.

Requirements: 133-REQ-1.1, 133-REQ-1.2, 133-REQ-1.E1,
              133-REQ-2.1, 133-REQ-2.2, 133-REQ-2.3, 133-REQ-2.4,
              133-REQ-2.E1,
              133-REQ-3.1, 133-REQ-3.E1,
              133-REQ-4.1, 133-REQ-4.E1
"""

from __future__ import annotations

from pathlib import Path

import afspec
from afspec.models import Subtask, SubtaskState, TaskDependency, TaskGroup

from agentfox.spec.types import CrossSpecDep, SubtaskDef, TaskGroupDef

# ---------------------------------------------------------------------------
# Internal mapper functions
# ---------------------------------------------------------------------------


def _map_subtask(subtask: Subtask) -> SubtaskDef:
    """Map one afspec Subtask to one SubtaskDef.

    133-REQ-1.1: completed is True iff state is DONE.
    133-REQ-1.2: all other states map to completed=False.
    133-REQ-1.E1: DROPPED maps to completed=False.
    """
    return SubtaskDef(
        id=subtask.id,
        title=subtask.title,
        completed=subtask.state == SubtaskState.DONE,
    )


def _render_group_body(group: TaskGroup) -> str:
    """Render a task group as markdown body text.

    Produces a markdown representation including subtask titles and details,
    suitable for the ``TaskGroupDef.body`` field.

    133-REQ-2.4: body contains markdown rendering of task group content.
    """
    lines: list[str] = []
    for subtask in group.subtasks:
        checkbox = "x" if subtask.state == SubtaskState.DONE else " "
        lines.append(f"- [{checkbox}] {subtask.id} {subtask.title}")
        for detail in subtask.details:
            lines.append(f"  - {detail}")
    return "\n".join(lines)


def _map_task_group(group: TaskGroup) -> TaskGroupDef:
    """Map one afspec TaskGroup to one TaskGroupDef.

    133-REQ-2.1: number, title, optional=False, archetype=None.
    133-REQ-2.2: completed=True when all non-dropped subtasks are DONE.
    133-REQ-2.3: completed=False when any non-dropped subtask is not DONE.
    133-REQ-2.E1: all-dropped group is vacuously complete.
    """
    mapped_subtasks = tuple(_map_subtask(st) for st in group.subtasks)

    # Completion: consider only non-dropped subtasks
    non_dropped = [st for st in group.subtasks if st.state != SubtaskState.DROPPED]
    if len(non_dropped) == 0:
        # 133-REQ-2.E1: vacuously complete
        completed = True
    else:
        completed = all(st.state == SubtaskState.DONE for st in non_dropped)

    return TaskGroupDef(
        number=group.id,
        title=group.title,
        optional=False,
        completed=completed,
        subtasks=mapped_subtasks,
        body=_render_group_body(group),
        archetype=None,
        kind=group.kind.value if group.kind is not None else None,
    )


def _map_dependency(dep: TaskDependency, current_spec: str) -> CrossSpecDep:
    """Map one afspec TaskDependency to one CrossSpecDep.

    133-REQ-3.1: field mapping follows codebase convention (see erratum
    docs/errata/133_cross_spec_dep_direction.md):
      - from_spec = current_spec (the spec declaring the dependency)
      - to_spec = dep.depends_on_spec (the spec being depended on)
      - from_group = dep.to_group (group in the declaring spec)
      - to_group = dep.from_group (group in the dependency spec)
    """
    return CrossSpecDep(
        from_spec=current_spec,
        from_group=dep.to_group,
        to_spec=dep.depends_on_spec,
        to_group=dep.from_group,
    )


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------


def parse_tasks(spec_dir: Path) -> list[TaskGroupDef]:
    """Load a v1.2 spec and return task groups as TaskGroupDef list.

    Calls ``afspec.load_spec()`` to parse the spec directory and maps
    each ``TaskGroup`` to a ``TaskGroupDef``.

    133-REQ-4.1: called by build_plan for V1_2_JSON specs.
    133-REQ-4.E1: afspec.LoadError propagates uncaught.

    Args:
        spec_dir: Path to the v1.2 spec directory.

    Returns:
        List of TaskGroupDef in spec order.

    Raises:
        afspec.LoadError: If the spec files are missing or malformed.
    """
    spec = afspec.load_spec(spec_dir)
    return [_map_task_group(group) for group in spec.tasks.task_groups]


def parse_cross_deps(
    spec_dir: Path,
    spec_name: str,
) -> list[CrossSpecDep]:
    """Load a v1.2 spec and return cross-spec dependencies.

    Calls ``afspec.load_spec()`` to parse the spec directory and maps
    each ``TaskDependency`` to a ``CrossSpecDep``.

    133-REQ-3.E1: returns empty list when no dependencies exist.

    Args:
        spec_dir: Path to the v1.2 spec directory.
        spec_name: Name of the current spec (declaring the dependencies).

    Returns:
        List of CrossSpecDep declarations.

    Raises:
        afspec.LoadError: If the spec files are missing or malformed.
    """
    spec = afspec.load_spec(spec_dir)
    return [_map_dependency(dep, spec_name) for dep in spec.tasks.dependencies]
