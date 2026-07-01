"""Tests for afspec data models, constructors, and lifecycle transitions.

TS-01 traceability: test_spec_model_fields (01-REQ-1.1),
test_criterion_ears_patterns (01-REQ-1.2), test_valid_transition (01-REQ-1.3),
test_sub_types (01-REQ-1.4), test_constructors (01-REQ-1.5),
test_ears_criterion_builders (01-REQ-1.6), test_with_return_contract (01-REQ-1.6),
test_subtask_transition_to (01-REQ-1.7), test_subtask_transition_to_illegal (01-REQ-1.7),
test_property_subtask_transitions (P3), test_property_constructor_completeness (P11).
"""

from __future__ import annotations

import json

import pytest
from hypothesis import given
from hypothesis.strategies import sampled_from

from afspec import (
    Criterion,
    EARSPattern,
    LifecycleError,
    PRDDocument,
    Requirements,
    Spec,
    Status,
    Subtask,
    SubtaskState,
    Tasks,
    TestSpec,
    VerificationSubtask,
    complex_event_criterion,
    create_spec,
    event_driven_criterion,
    optional_criterion,
    state_driven_criterion,
    ubiquitous_criterion,
    unwanted_criterion,
    valid_transition,
)

# ---------------------------------------------------------------------------
# Legal transitions lookup (10 edges in the subtask state machine)
# ---------------------------------------------------------------------------

LEGAL_TRANSITIONS: set[tuple[SubtaskState, SubtaskState]] = {
    (SubtaskState.PENDING, SubtaskState.QUEUED),
    (SubtaskState.PENDING, SubtaskState.DROPPED),
    (SubtaskState.QUEUED, SubtaskState.IN_PROGRESS),
    (SubtaskState.QUEUED, SubtaskState.PENDING),
    (SubtaskState.QUEUED, SubtaskState.DROPPED),
    (SubtaskState.IN_PROGRESS, SubtaskState.DONE),
    (SubtaskState.IN_PROGRESS, SubtaskState.PENDING_REEVALUATION),
    (SubtaskState.DONE, SubtaskState.PENDING_REEVALUATION),
    (SubtaskState.PENDING_REEVALUATION, SubtaskState.PENDING),
    (SubtaskState.PENDING_REEVALUATION, SubtaskState.DROPPED),
}


# ---------------------------------------------------------------------------
# TS-01-1  (01-REQ-1.1)
# ---------------------------------------------------------------------------


def test_spec_model_fields() -> None:
    """Construct a Spec with all four artifact sub-models and verify field types."""
    prd = PRDDocument()
    reqs = Requirements()
    ts = TestSpec()
    tasks = Tasks()
    spec = Spec(prd=prd, requirements=reqs, test_spec=ts, tasks=tasks)

    assert spec.prd is prd
    assert spec.requirements is reqs
    assert spec.test_spec is ts
    assert spec.tasks is tasks

    assert isinstance(spec.prd, PRDDocument)
    assert isinstance(spec.requirements, Requirements)
    assert isinstance(spec.test_spec, TestSpec)
    assert isinstance(spec.tasks, Tasks)


# ---------------------------------------------------------------------------
# TS-01-2  (01-REQ-1.2)
# ---------------------------------------------------------------------------


def test_criterion_ears_patterns() -> None:
    """Create one Criterion per EARS pattern and validate fields + JSON."""
    criteria = [
        Criterion(
            id="C-1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="the system",
            action="shall log",
        ),
        Criterion(
            id="C-2",
            ears_pattern=EARSPattern.EVENT_DRIVEN,
            trigger="user clicks save",
            system="the editor",
            action="shall persist",
        ),
        Criterion(
            id="C-3",
            ears_pattern=EARSPattern.COMPLEX_EVENT,
            trigger="file uploaded",
            condition="file size < 10MB",
            system="the uploader",
            action="shall accept",
        ),
        Criterion(
            id="C-4",
            ears_pattern=EARSPattern.STATE_DRIVEN,
            state="offline",
            system="the cache",
            action="shall serve local data",
        ),
        Criterion(
            id="C-5",
            ears_pattern=EARSPattern.UNWANTED,
            error_condition="disk full",
            system="the writer",
            action="shall alert",
        ),
        Criterion(
            id="C-6",
            ears_pattern=EARSPattern.OPTIONAL,
            feature="dark mode",
            system="the UI",
            action="shall render dark theme",
        ),
    ]

    expected_patterns = [
        EARSPattern.UBIQUITOUS,
        EARSPattern.EVENT_DRIVEN,
        EARSPattern.COMPLEX_EVENT,
        EARSPattern.STATE_DRIVEN,
        EARSPattern.UNWANTED,
        EARSPattern.OPTIONAL,
    ]

    for criterion, pattern in zip(criteria, expected_patterns):
        assert criterion.ears_pattern == pattern
        serialized = criterion.model_dump_json()
        parsed = json.loads(serialized)
        assert parsed["ears_pattern"] == pattern.value


# ---------------------------------------------------------------------------
# TS-01-3  (01-REQ-1.3)
# ---------------------------------------------------------------------------


def test_valid_transition() -> None:
    """Exhaustively test all 36 (current, target) SubtaskState pairs."""
    all_states = list(SubtaskState)
    assert len(all_states) == 6

    for current in all_states:
        for target in all_states:
            expected = (current, target) in LEGAL_TRANSITIONS
            result = valid_transition(current, target)
            assert result is expected, (
                f"valid_transition({current.value}, {target.value}) returned {result}, expected {expected}"
            )


# ---------------------------------------------------------------------------
# TS-01-4  (01-REQ-1.4)
# ---------------------------------------------------------------------------


def test_sub_types() -> None:
    """Verify all 17 sub-types are importable from afspec and have expected fields."""
    import afspec

    # Map each type to a key field it must declare per design.md.
    # Not all types have an 'id' field — UserStory, PathStep, Coverage,
    # TaskDependency, TraceabilityEntry, and TestCommands use other fields.
    expected_types_and_fields: dict[str, str] = {
        "Requirement": "id",
        "UserStory": "role",
        "CorrectnessProperty": "id",
        "ExecutionPath": "id",
        "PathStep": "actor",
        "ErrorHandlingEntry": "id",
        "TestCase": "id",
        "PropertyTest": "id",
        "EdgeCaseTest": "id",
        "SmokeTest": "id",
        "Coverage": "requirements_covered",
        "TaskGroup": "id",
        "Subtask": "id",
        "VerificationSubtask": "id",
        "TaskDependency": "depends_on_spec",
        "TraceabilityEntry": "requirement_id",
        "TestCommands": "spec_tests",
    }

    for name, key_field in expected_types_and_fields.items():
        attr = getattr(afspec, name, None)
        assert attr is not None, f"{name} not importable from afspec"
        assert callable(attr), f"{name} is not callable"
        # Each sub-type should declare model fields with the expected key field
        assert key_field in attr.model_fields, f"{name} should have a '{key_field}' field"


# ---------------------------------------------------------------------------
# TS-01-42  (01-REQ-1.5)
# ---------------------------------------------------------------------------


def test_constructors() -> None:
    """Test create_spec, Requirement, and VerificationSubtask construction."""
    from afspec import Requirement, UserStory

    spec = create_spec("01", "my_spec")

    assert isinstance(spec, Spec)
    assert spec.prd.frontmatter.spec_id == "01"
    assert spec.prd.frontmatter.spec_name == "my_spec"
    assert spec.prd.frontmatter.status == Status.DRAFT

    assert spec.requirements.spec_id == "01"
    assert spec.test_spec.spec_id == "01"
    assert spec.tasks.spec_id == "01"

    # Requirement construction: defaults for acceptance_criteria and edge_cases
    req = Requirement(
        id="01-REQ-1",
        title="Data Model",
        user_story=UserStory(role="dev", goal="types", benefit="safety"),
    )
    assert req.id == "01-REQ-1"
    assert req.title == "Data Model"
    assert req.user_story.role == "dev"
    assert len(req.acceptance_criteria) == 0
    assert len(req.edge_cases) == 0

    # VerificationSubtask construction
    vs = VerificationSubtask(id="V-1", checks=["lint", "test"])
    assert vs.id == "V-1"
    assert vs.checks == ["lint", "test"]


# ---------------------------------------------------------------------------
# TS-01-43  (01-REQ-1.6)
# ---------------------------------------------------------------------------


def test_ears_criterion_builders() -> None:
    """Test all six EARS criterion builder functions."""
    # event_driven_criterion
    ed = event_driven_criterion("01-REQ-1.1", "user clicks", "the form", "validate")
    assert ed.ears_pattern == EARSPattern.EVENT_DRIVEN
    assert ed.trigger == "user clicks"
    assert ed.system == "the form"
    assert ed.action == "validate"
    assert ed.state is None
    assert ed.feature is None
    assert ed.error_condition is None
    assert ed.condition is None

    # state_driven_criterion
    sd = state_driven_criterion("01-REQ-1.2", "idle", "the scheduler", "flush queue")
    assert sd.ears_pattern == EARSPattern.STATE_DRIVEN
    assert sd.state == "idle"
    assert sd.system == "the scheduler"
    assert sd.action == "flush queue"
    assert sd.trigger is None
    assert sd.feature is None
    assert sd.error_condition is None
    assert sd.condition is None

    # ubiquitous_criterion
    ub = ubiquitous_criterion("01-REQ-1.3", "the logger", "log all events")
    assert ub.ears_pattern == EARSPattern.UBIQUITOUS
    assert ub.system == "the logger"
    assert ub.action == "log all events"
    assert ub.trigger is None
    assert ub.state is None
    assert ub.feature is None
    assert ub.error_condition is None
    assert ub.condition is None

    # complex_event_criterion
    ce = complex_event_criterion("01-REQ-1.4", "file arrives", "size < 10MB", "the ingestor", "process")
    assert ce.ears_pattern == EARSPattern.COMPLEX_EVENT
    assert ce.trigger == "file arrives"
    assert ce.condition == "size < 10MB"
    assert ce.system == "the ingestor"
    assert ce.action == "process"
    assert ce.state is None
    assert ce.feature is None
    assert ce.error_condition is None

    # unwanted_criterion
    uw = unwanted_criterion("01-REQ-1.5", "timeout", "the client", "retry")
    assert uw.ears_pattern == EARSPattern.UNWANTED
    assert uw.error_condition == "timeout"
    assert uw.system == "the client"
    assert uw.action == "retry"
    assert uw.trigger is None
    assert uw.state is None
    assert uw.feature is None
    assert uw.condition is None

    # optional_criterion
    op = optional_criterion("01-REQ-1.6", "export", "the report", "generate PDF")
    assert op.ears_pattern == EARSPattern.OPTIONAL
    assert op.feature == "export"
    assert op.system == "the report"
    assert op.action == "generate PDF"
    assert op.trigger is None
    assert op.state is None
    assert op.error_condition is None
    assert op.condition is None


# ---------------------------------------------------------------------------
# TS-01-44  (01-REQ-1.6)
# ---------------------------------------------------------------------------


def test_with_return_contract() -> None:
    """with_return_contract returns a new Criterion; original is unchanged."""
    original = event_driven_criterion("01-REQ-1.1", "user clicks", "the form", "validate")
    updated = original.with_return_contract("the list of items")

    assert updated.return_contract == "the list of items"
    assert original.return_contract is None
    assert updated is not original


# ---------------------------------------------------------------------------
# TS-01-45  (01-REQ-1.7)
# ---------------------------------------------------------------------------


def test_subtask_transition_to() -> None:
    """Subtask.transition_to returns a new Subtask in the target state."""
    sub = Subtask(id="1.1", title="Do thing")
    assert sub.state == SubtaskState.PENDING

    queued = sub.transition_to(SubtaskState.QUEUED)
    assert queued.state == SubtaskState.QUEUED
    assert queued is not sub


# ---------------------------------------------------------------------------
# TS-01-E24  (01-REQ-1.7)
# ---------------------------------------------------------------------------


def test_subtask_transition_to_illegal() -> None:
    """Illegal transition raises LifecycleError mentioning both states."""
    sub = Subtask(id="1.1", title="Do thing")
    assert sub.state == SubtaskState.PENDING

    with pytest.raises(LifecycleError, match="pending") as exc_info:
        sub.transition_to(SubtaskState.DONE)

    assert "done" in str(exc_info.value).lower()


# ---------------------------------------------------------------------------
# Property TS-01-P3
# ---------------------------------------------------------------------------


@given(
    current=sampled_from(list(SubtaskState)),
    target=sampled_from(list(SubtaskState)),
)
def test_property_subtask_transitions(current: SubtaskState, target: SubtaskState) -> None:
    """valid_transition matches the legal-transition set for all pairs."""
    expected = (current, target) in LEGAL_TRANSITIONS
    assert valid_transition(current, target) is expected


# ---------------------------------------------------------------------------
# Property TS-01-P11
# ---------------------------------------------------------------------------


@given(
    spec_id=sampled_from(["01", "02", "99", "AB"]),
    spec_name=sampled_from(["alpha", "beta", "gamma", "my_spec"]),
)
def test_property_constructor_completeness(spec_id: str, spec_name: str) -> None:
    """create_spec produces a Spec where sub-artifacts share spec_id and spec_name.

    Also verifies EARS criterion builders populate the correct pattern fields
    and leave non-pattern fields at their defaults.
    """
    # Test create_spec
    spec = create_spec(spec_id, spec_name)

    assert isinstance(spec, Spec)
    assert spec.prd.frontmatter.spec_id == spec_id
    assert spec.prd.frontmatter.spec_name == spec_name
    assert spec.requirements.spec_id == spec_id
    assert spec.test_spec.spec_id == spec_id
    assert spec.tasks.spec_id == spec_id

    # Test EARS criterion builders — each must set the correct pattern and fields
    cid = f"{spec_id}-REQ-1.1"

    ub = ubiquitous_criterion(cid, "the system", "do it")
    assert ub.ears_pattern == EARSPattern.UBIQUITOUS
    assert ub.system == "the system"
    assert ub.action == "do it"
    assert ub.trigger is None
    assert ub.state is None

    ed = event_driven_criterion(cid, "click", "the form", "submit")
    assert ed.ears_pattern == EARSPattern.EVENT_DRIVEN
    assert ed.trigger == "click"
    assert ed.condition is None

    ce = complex_event_criterion(cid, "click", "valid", "the form", "submit")
    assert ce.ears_pattern == EARSPattern.COMPLEX_EVENT
    assert ce.trigger == "click"
    assert ce.condition == "valid"
    assert ce.state is None

    sd = state_driven_criterion(cid, "idle", "the sched", "flush")
    assert sd.ears_pattern == EARSPattern.STATE_DRIVEN
    assert sd.state == "idle"
    assert sd.trigger is None

    uw = unwanted_criterion(cid, "timeout", "the client", "retry")
    assert uw.ears_pattern == EARSPattern.UNWANTED
    assert uw.error_condition == "timeout"
    assert uw.feature is None

    op = optional_criterion(cid, "export", "the report", "gen PDF")
    assert op.ears_pattern == EARSPattern.OPTIONAL
    assert op.feature == "export"
    assert op.error_condition is None


# ---------------------------------------------------------------------------
# Subtask.details string coercion (fixes #658)
# ---------------------------------------------------------------------------


class TestSubtaskDetailsCoercion:
    """Subtask.details coerces a bare string to a single-element list."""

    def test_string_coerced_to_list(self) -> None:
        sub = Subtask(id="1.1", title="Write tests", details="Write unit tests in test_foo.py")
        assert sub.details == ["Write unit tests in test_foo.py"]

    def test_list_unchanged(self) -> None:
        sub = Subtask(id="1.1", title="Write tests", details=["step one", "step two"])
        assert sub.details == ["step one", "step two"]

    def test_empty_list_unchanged(self) -> None:
        sub = Subtask(id="1.1", title="Write tests", details=[])
        assert sub.details == []

    def test_default_is_empty_list(self) -> None:
        sub = Subtask(id="1.1", title="Write tests")
        assert sub.details == []

    def test_model_validate_with_string_details(self) -> None:
        data = {
            "id": "1.1",
            "title": "Write tests",
            "details": "Write unit tests in test_foo.py covering edge cases",
        }
        sub = Subtask.model_validate(data)
        assert sub.details == ["Write unit tests in test_foo.py covering edge cases"]

    def test_model_validate_nested_in_task_group(self) -> None:
        from afspec import TaskGroup

        data = {
            "id": 1,
            "title": "Implementation",
            "subtasks": [
                {"id": "1.1", "title": "Do thing", "details": "A single string detail"},
                {"id": "1.2", "title": "Other thing", "details": ["Already", "a list"]},
            ],
        }
        tg = TaskGroup.model_validate(data)
        assert tg.subtasks[0].details == ["A single string detail"]
        assert tg.subtasks[1].details == ["Already", "a list"]
