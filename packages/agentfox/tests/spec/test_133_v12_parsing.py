"""Spec 133: v1.2 Parsing Pipeline tests.

Test Spec: TS-133-1 through TS-133-8, TS-133-E1 through TS-133-E3,
           TS-133-P1, TS-133-P2, TS-133-SMOKE-1
Requirements: 133-REQ-1.1, 133-REQ-1.2, 133-REQ-1.E1,
              133-REQ-2.1, 133-REQ-2.2, 133-REQ-2.3, 133-REQ-2.4,
              133-REQ-2.E1,
              133-REQ-3.1, 133-REQ-3.E1,
              133-REQ-4.1, 133-REQ-4.2, 133-REQ-4.E1
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest
from afspec import LoadError
from afspec.models import SubtaskState
from agentfox.spec.types import CrossSpecDep, SubtaskDef, TaskGroupDef
from hypothesis import given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# afspec model helpers
# ---------------------------------------------------------------------------


def _make_subtask(
    *,
    id: str = "1.1",
    title: str = "Test subtask",
    state: str = "pending",
    details: list[str] | None = None,
) -> object:
    """Build an afspec Subtask Pydantic model instance."""
    from afspec.models import Subtask, SubtaskState

    return Subtask(
        id=id,
        title=title,
        state=SubtaskState(state),
        details=details or [],
        test_spec_refs=[],
        requirement_refs=[],
    )


def _make_task_group(
    *,
    id: int = 1,
    title: str = "Test group",
    kind: str = "standard",
    subtasks: list | None = None,
) -> object:
    """Build an afspec TaskGroup Pydantic model instance."""
    from afspec.models import TaskGroup, TaskGroupKind, VerificationSubtask

    return TaskGroup(
        id=id,
        title=title,
        kind=TaskGroupKind(kind),
        subtasks=subtasks or [],
        verification=VerificationSubtask(id="", checks=[]),
    )


def _make_dependency(
    *,
    depends_on_spec: str = "other_spec",
    from_group: int = 1,
    to_group: int = 2,
    relationship: str = "uses",
) -> object:
    """Build an afspec TaskDependency Pydantic model instance."""
    from afspec.models import TaskDependency

    return TaskDependency(
        depends_on_spec=depends_on_spec,
        from_group=from_group,
        to_group=to_group,
        relationship=relationship,
    )


# ---------------------------------------------------------------------------
# v1.2 spec fixture content
# ---------------------------------------------------------------------------

PRD_MD_VALID = """\
---
spec_id: "test-133"
spec_name: "test_fixture"
title: "Test Fixture Spec"
status: "draft"
created_at: "2024-01-01T00:00:00Z"
updated_at: "2024-01-01T00:00:00Z"
owner: "test"
source: "test"
schema_version: 1
---
# Test PRD

Test PRD content.
"""

REQUIREMENTS_JSON_VALID = json.dumps(
    {
        "spec_id": "test-133",
        "spec_name": "test_fixture",
        "schema_version": 1,
        "introduction": "Test requirements",
        "glossary": {},
        "requirements": [],
        "correctness_properties": [],
        "execution_paths": [],
        "error_handling": [],
    },
    indent=2,
)

TEST_SPEC_JSON_VALID = json.dumps(
    {
        "spec_id": "test-133",
        "spec_name": "test_fixture",
        "schema_version": 1,
        "test_cases": [],
        "property_tests": [],
        "edge_case_tests": [],
        "smoke_tests": [],
        "coverage": {
            "requirements_covered": [],
            "properties_covered": [],
            "paths_covered": [],
            "gaps": [],
        },
    },
    indent=2,
)


def _tasks_json_with_groups(
    *,
    task_groups: list[dict] | None = None,
    dependencies: list[dict] | None = None,
) -> str:
    """Build valid tasks.json content with task groups and/or deps."""
    return json.dumps(
        {
            "spec_id": "test-133",
            "spec_name": "test_fixture",
            "schema_version": 1,
            "test_commands": {"spec_tests": "", "all_tests": "", "linter": ""},
            "dependencies": dependencies or [],
            "task_groups": task_groups or [],
            "traceability": [],
        },
        indent=2,
    )


def _write_spec(
    spec_dir: Path,
    *,
    task_groups: list[dict] | None = None,
    dependencies: list[dict] | None = None,
) -> None:
    """Populate a directory with valid v1.2 spec artifacts."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD_VALID)
    (spec_dir / "requirements.json").write_text(REQUIREMENTS_JSON_VALID)
    (spec_dir / "test_spec.json").write_text(TEST_SPEC_JSON_VALID)
    (spec_dir / "tasks.json").write_text(
        _tasks_json_with_groups(
            task_groups=task_groups,
            dependencies=dependencies,
        )
    )


# ---------------------------------------------------------------------------
# Standard task group/subtask fixtures for v1.2 specs
# ---------------------------------------------------------------------------

SAMPLE_SUBTASKS = [
    {
        "id": "1.1",
        "title": "Write tests",
        "state": "done",
        "details": ["Detail line 1"],
        "test_spec_refs": [],
        "requirement_refs": [],
        "optional": False,
    },
    {
        "id": "1.2",
        "title": "Implement feature",
        "state": "pending",
        "details": ["Step 1", "Step 2"],
        "test_spec_refs": [],
        "requirement_refs": [],
        "optional": False,
    },
]

SAMPLE_TASK_GROUPS = [
    {
        "id": 1,
        "kind": "standard",
        "title": "Write failing tests",
        "subtasks": SAMPLE_SUBTASKS,
        "verification": {"id": "", "checks": []},
    },
    {
        "id": 2,
        "kind": "standard",
        "title": "Implement parser",
        "subtasks": [
            {
                "id": "2.1",
                "title": "Create module",
                "state": "pending",
                "details": [],
                "test_spec_refs": [],
                "requirement_refs": [],
                "optional": False,
            }
        ],
        "verification": {"id": "", "checks": []},
    },
]

SAMPLE_DEPENDENCIES = [
    {
        "depends_on_spec": "132_afspec_integration",
        "from_group": 2,
        "to_group": 1,
        "relationship": "uses afspec models",
        "sentinel": False,
    },
]


# ---------------------------------------------------------------------------
# Pytest fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def v12_spec_dir_with_groups(tmp_path: Path) -> Path:
    """A v1.2 spec directory with task groups and subtasks."""
    spec_dir = tmp_path / "01_test_spec"
    _write_spec(spec_dir, task_groups=SAMPLE_TASK_GROUPS)
    return spec_dir


@pytest.fixture
def v12_spec_dir_with_deps(tmp_path: Path) -> Path:
    """A v1.2 spec directory with task dependencies."""
    spec_dir = tmp_path / "01_test_spec"
    _write_spec(
        spec_dir,
        task_groups=SAMPLE_TASK_GROUPS,
        dependencies=SAMPLE_DEPENDENCIES,
    )
    return spec_dir


@pytest.fixture
def v12_spec_dir_no_deps(tmp_path: Path) -> Path:
    """A v1.2 spec directory with no dependencies."""
    spec_dir = tmp_path / "01_test_spec"
    _write_spec(spec_dir, task_groups=SAMPLE_TASK_GROUPS, dependencies=[])
    return spec_dir


@pytest.fixture
def v12_spec_dir_all_done(tmp_path: Path) -> Path:
    """A v1.2 spec directory where all subtasks are DONE."""
    groups = [
        {
            "id": 1,
            "kind": "standard",
            "title": "Completed group",
            "subtasks": [
                {
                    "id": "1.1",
                    "title": "First task",
                    "state": "done",
                    "details": [],
                    "test_spec_refs": [],
                    "requirement_refs": [],
                    "optional": False,
                },
                {
                    "id": "1.2",
                    "title": "Second task",
                    "state": "done",
                    "details": [],
                    "test_spec_refs": [],
                    "requirement_refs": [],
                    "optional": False,
                },
            ],
            "verification": {"id": "", "checks": []},
        }
    ]
    spec_dir = tmp_path / "01_test_spec"
    _write_spec(spec_dir, task_groups=groups)
    return spec_dir


@pytest.fixture
def v12_specs_root_for_smoke(tmp_path: Path) -> Path:
    """A specs root with one v1.2 spec for smoke testing the full pipeline."""
    root = tmp_path / "specs"
    root.mkdir()
    _write_spec(
        root / "01_test_spec",
        task_groups=SAMPLE_TASK_GROUPS,
        dependencies=[],
    )
    return root


@pytest.fixture
def malformed_spec_dir(tmp_path: Path) -> Path:
    """A spec directory with malformed JSON in tasks.json."""
    spec_dir = tmp_path / "01_malformed"
    spec_dir.mkdir(parents=True)
    (spec_dir / "prd.md").write_text(PRD_MD_VALID)
    (spec_dir / "requirements.json").write_text(REQUIREMENTS_JSON_VALID)
    (spec_dir / "test_spec.json").write_text(TEST_SPEC_JSON_VALID)
    (spec_dir / "tasks.json").write_text("{invalid json!!!")
    return spec_dir


# ===========================================================================
# TS-133-1: Subtask mapping sets completed from state
# ===========================================================================


class TestMapSubtaskCompletedFromState:
    """TS-133-1: Verify _map_subtask maps id, title, and completed correctly.

    Requirements: 133-REQ-1.1, 133-REQ-1.2
    """

    def test_done_subtask_completed_true(self) -> None:
        """Subtask with state=DONE maps to SubtaskDef with completed=True."""
        from agentfox.spec.parser import _map_subtask

        subtask = _make_subtask(id="1.1", title="Write tests", state="done")
        result = _map_subtask(subtask)

        assert isinstance(result, SubtaskDef)
        assert result.id == "1.1"
        assert result.title == "Write tests"
        assert result.completed is True

    def test_pending_subtask_completed_false(self) -> None:
        """Subtask with state=PENDING maps to SubtaskDef with completed=False."""
        from agentfox.spec.parser import _map_subtask

        subtask = _make_subtask(id="1.2", title="Implement feature", state="pending")
        result = _map_subtask(subtask)

        assert isinstance(result, SubtaskDef)
        assert result.id == "1.2"
        assert result.title == "Implement feature"
        assert result.completed is False

    def test_in_progress_subtask_completed_false(self) -> None:
        """Subtask with state=IN_PROGRESS maps to completed=False."""
        from agentfox.spec.parser import _map_subtask

        subtask = _make_subtask(id="1.3", title="Working on it", state="in_progress")
        result = _map_subtask(subtask)

        assert result.completed is False

    def test_queued_subtask_completed_false(self) -> None:
        """Subtask with state=QUEUED maps to completed=False."""
        from agentfox.spec.parser import _map_subtask

        subtask = _make_subtask(id="1.4", title="Queued task", state="queued")
        result = _map_subtask(subtask)

        assert result.completed is False


# ===========================================================================
# TS-133-2: Task group mapping produces correct TaskGroupDef
# ===========================================================================


class TestMapTaskGroupFields:
    """TS-133-2: Verify _map_task_group maps id, title, optional, archetype.

    Requirement: 133-REQ-2.1
    """

    def test_group_number_from_id(self) -> None:
        """TaskGroupDef.number equals TaskGroup.id."""
        from agentfox.spec.parser import _map_task_group

        group = _make_task_group(
            id=2,
            title="Implement parser",
            kind="standard",
            subtasks=[_make_subtask(state="pending")],
        )
        result = _map_task_group(group)

        assert result.number == 2

    def test_group_title(self) -> None:
        """TaskGroupDef.title equals TaskGroup.title."""
        from agentfox.spec.parser import _map_task_group

        group = _make_task_group(
            id=2,
            title="Implement parser",
            subtasks=[_make_subtask(state="pending")],
        )
        result = _map_task_group(group)

        assert result.title == "Implement parser"

    def test_group_optional_always_false(self) -> None:
        """TaskGroupDef.optional is always False for v1.2 (no optional groups)."""
        from agentfox.spec.parser import _map_task_group

        group = _make_task_group(
            id=1,
            subtasks=[_make_subtask(state="pending")],
        )
        result = _map_task_group(group)

        assert result.optional is False

    def test_group_archetype_always_none(self) -> None:
        """TaskGroupDef.archetype is always None for v1.2 (no archetype tags)."""
        from agentfox.spec.parser import _map_task_group

        group = _make_task_group(
            id=1,
            subtasks=[_make_subtask(state="pending")],
        )
        result = _map_task_group(group)

        assert result.archetype is None

    def test_group_completed_false_with_pending_subtask(self) -> None:
        """TaskGroupDef.completed is False when subtask is PENDING."""
        from agentfox.spec.parser import _map_task_group

        group = _make_task_group(
            id=2,
            title="Implement parser",
            subtasks=[_make_subtask(state="pending")],
        )
        result = _map_task_group(group)

        assert isinstance(result, TaskGroupDef)
        assert result.completed is False

    def test_group_subtasks_mapped(self) -> None:
        """TaskGroupDef.subtasks contains mapped SubtaskDef instances."""
        from agentfox.spec.parser import _map_task_group

        group = _make_task_group(
            id=1,
            subtasks=[_make_subtask(id="1.1", state="done")],
        )
        result = _map_task_group(group)

        assert len(result.subtasks) == 1
        assert isinstance(result.subtasks[0], SubtaskDef)


# ===========================================================================
# TS-133-3: Group completed when all non-dropped subtasks are DONE
# ===========================================================================


class TestGroupCompletedAllDone:
    """TS-133-3: Verify TaskGroupDef.completed is True when all non-dropped
    subtasks have state DONE.

    Requirement: 133-REQ-2.2
    """

    def test_all_done_plus_dropped_is_completed(self) -> None:
        """Group with one DONE and one DROPPED subtask is completed."""
        from agentfox.spec.parser import _map_task_group

        group = _make_task_group(
            id=1,
            subtasks=[
                _make_subtask(id="1.1", state="done"),
                _make_subtask(id="1.2", state="dropped"),
            ],
        )
        result = _map_task_group(group)

        assert result.completed is True

    def test_all_done_is_completed(self) -> None:
        """Group with all DONE subtasks is completed."""
        from agentfox.spec.parser import _map_task_group

        group = _make_task_group(
            id=1,
            subtasks=[
                _make_subtask(id="1.1", state="done"),
                _make_subtask(id="1.2", state="done"),
            ],
        )
        result = _map_task_group(group)

        assert result.completed is True


# ===========================================================================
# TS-133-4: Group not completed when any non-dropped subtask is not DONE
# ===========================================================================


class TestGroupNotCompleted:
    """TS-133-4: Verify TaskGroupDef.completed is False when a non-dropped
    subtask is not DONE.

    Requirement: 133-REQ-2.3
    """

    def test_done_and_in_progress_is_not_completed(self) -> None:
        """Group with one DONE and one IN_PROGRESS subtask is not completed."""
        from agentfox.spec.parser import _map_task_group

        group = _make_task_group(
            id=1,
            subtasks=[
                _make_subtask(id="1.1", state="done"),
                _make_subtask(id="1.2", state="in_progress"),
            ],
        )
        result = _map_task_group(group)

        assert result.completed is False

    def test_done_and_pending_is_not_completed(self) -> None:
        """Group with one DONE and one PENDING subtask is not completed."""
        from agentfox.spec.parser import _map_task_group

        group = _make_task_group(
            id=1,
            subtasks=[
                _make_subtask(id="1.1", state="done"),
                _make_subtask(id="1.2", state="pending"),
            ],
        )
        result = _map_task_group(group)

        assert result.completed is False


# ===========================================================================
# TS-133-5: Group body contains markdown content
# ===========================================================================


class TestGroupBodyMarkdown:
    """TS-133-5: Verify TaskGroupDef.body is a non-empty markdown string
    containing subtask information.

    Requirement: 133-REQ-2.4
    """

    def test_body_is_nonempty(self) -> None:
        """TaskGroupDef.body is a non-empty string."""
        from agentfox.spec.parser import _map_task_group

        group = _make_task_group(
            id=1,
            subtasks=[
                _make_subtask(id="1.1", title="Write tests", details=["Detail line"]),
            ],
        )
        result = _map_task_group(group)

        assert len(result.body) > 0

    def test_body_contains_subtask_title(self) -> None:
        """TaskGroupDef.body contains subtask titles."""
        from agentfox.spec.parser import _map_task_group

        group = _make_task_group(
            id=1,
            subtasks=[
                _make_subtask(id="1.1", title="Write tests", details=["Detail line"]),
            ],
        )
        result = _map_task_group(group)

        assert "Write tests" in result.body

    def test_body_is_string(self) -> None:
        """TaskGroupDef.body is a string."""
        from agentfox.spec.parser import _map_task_group

        group = _make_task_group(
            id=1,
            subtasks=[_make_subtask(id="1.1", title="A task")],
        )
        result = _map_task_group(group)

        assert isinstance(result.body, str)


# ===========================================================================
# TS-133-6: Cross-spec dependency mapping
# ===========================================================================


class TestMapDependency:
    """TS-133-6: Verify _map_dependency maps fields correctly with proper
    field assignment.

    Requirement: 133-REQ-3.1

    NOTE: The spec's REQ-3.1 has from_spec/to_spec reversed vs. the
    project's CrossSpecDep convention. The correct mapping follows the
    existing codebase convention confirmed by builder.py (lines 216-219)
    and parser.py (alt format, lines 293-299):
      - from_spec = current_spec (the spec declaring the dependency)
      - to_spec = depends_on_spec (the spec being depended on)
      - from_group = dep.to_group (group in current/declaring spec)
      - to_group = dep.from_group (group in dependency spec)
    """

    def test_from_spec_is_current_spec(self) -> None:
        """CrossSpecDep.from_spec is the current spec (declaring the dep)."""
        from agentfox.spec.parser import _map_dependency

        dep = _make_dependency(
            depends_on_spec="132_afspec_integration",
            from_group=2,
            to_group=1,
        )
        result = _map_dependency(dep, "133_v12_parsing_pipeline")

        assert result.from_spec == "133_v12_parsing_pipeline"

    def test_to_spec_is_depends_on_spec(self) -> None:
        """CrossSpecDep.to_spec is the dependency spec."""
        from agentfox.spec.parser import _map_dependency

        dep = _make_dependency(
            depends_on_spec="132_afspec_integration",
            from_group=2,
            to_group=1,
        )
        result = _map_dependency(dep, "133_v12_parsing_pipeline")

        assert result.to_spec == "132_afspec_integration"

    def test_from_group_is_dep_to_group(self) -> None:
        """CrossSpecDep.from_group equals TaskDependency.to_group."""
        from agentfox.spec.parser import _map_dependency

        dep = _make_dependency(
            depends_on_spec="132_afspec_integration",
            from_group=2,
            to_group=1,
        )
        result = _map_dependency(dep, "133_v12_parsing_pipeline")

        assert result.from_group == 1

    def test_to_group_is_dep_from_group(self) -> None:
        """CrossSpecDep.to_group equals TaskDependency.from_group."""
        from agentfox.spec.parser import _map_dependency

        dep = _make_dependency(
            depends_on_spec="132_afspec_integration",
            from_group=2,
            to_group=1,
        )
        result = _map_dependency(dep, "133_v12_parsing_pipeline")

        assert result.to_group == 2

    def test_result_is_cross_spec_dep(self) -> None:
        """_map_dependency returns a CrossSpecDep instance."""
        from agentfox.spec.parser import _map_dependency

        dep = _make_dependency(depends_on_spec="other_spec")
        result = _map_dependency(dep, "current_spec")

        assert isinstance(result, CrossSpecDep)


# ===========================================================================
# TS-133-7: parse_tasks returns list of TaskGroupDef
# ===========================================================================


class TestParseTasksV12:
    """TS-133-7: Verify that parse_tasks loads a v1.2 spec and returns
    TaskGroupDef instances.

    Requirement: 133-REQ-4.1
    """

    def test_returns_nonempty_list(self, v12_spec_dir_with_groups: Path) -> None:
        """parse_tasks returns a non-empty list."""
        from agentfox.spec.parser import parse_tasks

        groups = parse_tasks(v12_spec_dir_with_groups)

        assert len(groups) > 0

    def test_returns_task_group_def_instances(self, v12_spec_dir_with_groups: Path) -> None:
        """All returned elements are TaskGroupDef instances."""
        from agentfox.spec.parser import parse_tasks

        groups = parse_tasks(v12_spec_dir_with_groups)

        assert all(isinstance(g, TaskGroupDef) for g in groups)

    def test_group_number_populated(self, v12_spec_dir_with_groups: Path) -> None:
        """Returned groups have non-zero numbers."""
        from agentfox.spec.parser import parse_tasks

        groups = parse_tasks(v12_spec_dir_with_groups)

        assert groups[0].number > 0

    def test_group_title_populated(self, v12_spec_dir_with_groups: Path) -> None:
        """Returned groups have non-empty titles."""
        from agentfox.spec.parser import parse_tasks

        groups = parse_tasks(v12_spec_dir_with_groups)

        assert groups[0].title != ""

    def test_multiple_groups_parsed(self, v12_spec_dir_with_groups: Path) -> None:
        """Multiple task groups are parsed from the spec."""
        from agentfox.spec.parser import parse_tasks

        groups = parse_tasks(v12_spec_dir_with_groups)

        assert len(groups) == 2


# ===========================================================================
# TS-133-8: parse_cross_deps returns list of CrossSpecDep
# ===========================================================================


class TestParseCrossDepsV12:
    """TS-133-8: Verify that parse_cross_deps loads a v1.2 spec and
    returns CrossSpecDep instances, or an empty list if no dependencies.

    Requirements: 133-REQ-3.1, 133-REQ-3.E1
    """

    def test_with_dependencies(self, v12_spec_dir_with_deps: Path) -> None:
        """parse_cross_deps returns non-empty list when deps exist."""
        from agentfox.spec.parser import parse_cross_deps

        deps = parse_cross_deps(v12_spec_dir_with_deps, "test_spec")

        assert len(deps) > 0
        assert all(isinstance(d, CrossSpecDep) for d in deps)

    def test_without_dependencies(self, v12_spec_dir_no_deps: Path) -> None:
        """parse_cross_deps returns empty list when no deps exist."""
        from agentfox.spec.parser import parse_cross_deps

        deps = parse_cross_deps(v12_spec_dir_no_deps, "test_spec")

        assert deps == []


# ===========================================================================
# TS-133-E1: Dropped subtask excluded from completion check
# ===========================================================================


class TestDroppedSubtaskCompletion:
    """TS-133-E1: Verify that dropped subtasks are excluded from the group
    completion check and that all-dropped groups are vacuously complete.

    Requirements: 133-REQ-1.E1, 133-REQ-2.E1
    """

    def test_all_dropped_vacuously_complete(self) -> None:
        """A group with only DROPPED subtasks is vacuously complete."""
        from agentfox.spec.parser import _map_task_group

        group = _make_task_group(
            id=1,
            subtasks=[
                _make_subtask(id="1.1", state="dropped"),
                _make_subtask(id="1.2", state="dropped"),
            ],
        )
        result = _map_task_group(group)

        assert result.completed is True

    def test_dropped_subtask_completed_false(self) -> None:
        """DROPPED subtasks map to SubtaskDef with completed=False."""
        from agentfox.spec.parser import _map_subtask

        subtask = _make_subtask(id="1.1", state="dropped")
        result = _map_subtask(subtask)

        assert result.completed is False

    def test_all_dropped_subtasks_not_completed(self) -> None:
        """All SubtaskDefs in an all-dropped group have completed=False."""
        from agentfox.spec.parser import _map_task_group

        group = _make_task_group(
            id=1,
            subtasks=[
                _make_subtask(id="1.1", state="dropped"),
                _make_subtask(id="1.2", state="dropped"),
            ],
        )
        result = _map_task_group(group)

        assert all(not st.completed for st in result.subtasks)


# ===========================================================================
# TS-133-E2: Spec with no dependencies returns empty list
# ===========================================================================


class TestNoDepsEmptyList:
    """TS-133-E2: parse_cross_deps returns [] when tasks.json has no
    dependency entries.

    Requirement: 133-REQ-3.E1
    """

    def test_empty_deps(self, v12_spec_dir_no_deps: Path) -> None:
        """Spec with no dependencies yields an empty list."""
        from agentfox.spec.parser import parse_cross_deps

        deps = parse_cross_deps(v12_spec_dir_no_deps, "test_spec")

        assert deps == []


# ===========================================================================
# TS-133-E3: afspec.load_spec error propagates from parse_tasks
# ===========================================================================


class TestLoadErrorPropagates:
    """TS-133-E3: When afspec.load_spec raises LoadError, it propagates
    through parse_tasks uncaught.

    Requirement: 133-REQ-4.E1
    """

    def test_malformed_json_raises(self, malformed_spec_dir: Path) -> None:
        """Malformed tasks.json causes a LoadError that propagates uncaught."""
        from agentfox.spec.parser import parse_tasks

        with pytest.raises(LoadError):
            parse_tasks(malformed_spec_dir)


# ===========================================================================
# TS-133-P1: Subtask completion is a function of state alone
# ===========================================================================


class TestCompletionIsFunctionOfState:
    """TS-133-P1: For any subtask state, completed is True iff state is DONE.

    Property 1 from design.md.
    Validates: 133-REQ-1.1, 133-REQ-1.2
    """

    @pytest.mark.property
    @settings(max_examples=20, deadline=None)
    @given(
        state=st.sampled_from(list(SubtaskState)),
    )
    def test_completed_equals_state_is_done(self, state: SubtaskState) -> None:
        """_map_subtask(s).completed == (s.state == SubtaskState.DONE)."""
        from agentfox.spec.parser import _map_subtask

        subtask = _make_subtask(id="1.1", title="task", state=state.value)
        result = _map_subtask(subtask)

        assert result.completed == (state == SubtaskState.DONE)


# ===========================================================================
# TS-133-P2: Group completion is consistent with subtask states
# ===========================================================================


class TestGroupCompletionConsistent:
    """TS-133-P2: A group is completed iff all non-dropped subtasks are DONE.

    Property 2 from design.md.
    Validates: 133-REQ-2.2, 133-REQ-2.3, 133-REQ-2.E1
    """

    @pytest.mark.property
    @settings(max_examples=50, deadline=None)
    @given(
        states=st.lists(
            st.sampled_from(list(SubtaskState)),
            min_size=1,
            max_size=6,
        ),
    )
    def test_group_completion_invariant(self, states: list[SubtaskState]) -> None:
        """TaskGroupDef.completed matches the expected invariant."""
        from agentfox.spec.parser import _map_task_group

        subtasks = [
            _make_subtask(id=f"1.{j + 1}", title=f"task {j + 1}", state=s.value)
            for j, s in enumerate(states)
        ]

        group = _make_task_group(id=1, subtasks=subtasks)
        result = _map_task_group(group)

        non_dropped = [s for s in states if s != SubtaskState.DROPPED]
        if len(non_dropped) == 0:
            # All dropped -> vacuously complete
            assert result.completed is True
        else:
            expected = all(s == SubtaskState.DONE for s in non_dropped)
            assert result.completed == expected


# ===========================================================================
# TS-133-SMOKE-1: Full pipeline from discovery through planner
# ===========================================================================


class TestSmokeFullPipeline:
    """TS-133-SMOKE-1: Discover a v1.2 spec, parse it through the new
    pipeline, and verify the graph builder receives correct TaskGroupDef
    instances.

    Execution Path: Path 1 + Path 2 + Path 3 from design.md.
    Must NOT satisfy with: no mocking of parser_v12, afspec.load_spec,
    or build_graph.
    """

    def test_build_plan_with_spec(self, v12_specs_root_for_smoke: Path) -> None:
        """build_plan produces a TaskGraph from a v1.2 spec directory."""
        from agentfox.core.config import AgentFoxConfig
        from agentfox.graph.planner import build_plan

        config = AgentFoxConfig()
        graph = build_plan(v12_specs_root_for_smoke, None, False, config)

        assert len(graph.nodes) > 0

    def test_spec_name_in_graph_nodes(self, v12_specs_root_for_smoke: Path) -> None:
        """Graph contains nodes with the correct spec name."""
        from agentfox.core.config import AgentFoxConfig
        from agentfox.graph.planner import build_plan

        config = AgentFoxConfig()
        graph = build_plan(v12_specs_root_for_smoke, None, False, config)

        spec_name = "01_test_spec"
        assert any(n.spec_name == spec_name for n in graph.nodes.values())

    def test_coder_nodes_present(self, v12_specs_root_for_smoke: Path) -> None:
        """Graph contains at least one coder archetype node."""
        from agentfox.core.config import AgentFoxConfig
        from agentfox.graph.planner import build_plan

        config = AgentFoxConfig()
        graph = build_plan(v12_specs_root_for_smoke, None, False, config)

        coder_nodes = [n for n in graph.nodes.values() if n.archetype == "coder"]
        assert len(coder_nodes) >= 1
