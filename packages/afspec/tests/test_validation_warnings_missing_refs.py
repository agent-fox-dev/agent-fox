"""Tests for missing subtask refs validation warning (issue #752).

Validates that afspec.validate() emits a ValidationWarning when subtasks
have empty requirement_refs or test_spec_refs, which prevents scoped
rendering from activating.
"""

from __future__ import annotations

from afspec.models import (
    Criterion,
    EARSPattern,
    ExecutionPath,
    PRDDocument,
    PRDFrontmatter,
    PathStep,
    Requirement,
    Requirements,
    SmokeTest,
    Spec,
    Subtask,
    TaskGroup,
    TaskGroupKind,
    Tasks,
    TestCase,
    TestSpec,
    TraceabilityEntry,
    UserStory,
    VerificationSubtask,
)
from afspec.validation import _check_missing_subtask_refs, validate


def _make_spec_with_subtask(
    *,
    req_refs: list[str] | None = None,
    ts_refs: list[str] | None = None,
    group_kind: TaskGroupKind = TaskGroupKind.TESTS,
) -> Spec:
    """Build a structurally valid spec with a configurable subtask."""
    crit = Criterion(
        id="M-REQ-1.1",
        ears_pattern=EARSPattern.UBIQUITOUS,
        system="the system",
        action="do action",
    )
    tc = TestCase(id="TS-M-1", requirement_id="M-REQ-1.1", kind="unit", description="Test")
    req = Requirement(
        id="M-REQ-1",
        title="Test requirement",
        user_story=UserStory(role="dev", goal="test", benefit="coverage"),
        acceptance_criteria=[crit],
    )

    subtask = Subtask(
        id="1.1",
        title="Implement feature",
        test_spec_refs=ts_refs if ts_refs is not None else [],
        requirement_refs=req_refs if req_refs is not None else [],
    )

    groups = [
        TaskGroup(
            id=1,
            kind=group_kind,
            title="Tests group",
            subtasks=[subtask],
            verification=VerificationSubtask(id="1.V", checks=["pass"]),
        ),
        TaskGroup(
            id=2,
            kind=TaskGroupKind.WIRING_VERIFICATION,
            title="Wiring verification",
            subtasks=[
                Subtask(
                    id="2.1",
                    title="Verify wiring and stub/dead-code audit",
                    test_spec_refs=["TS-M-SMOKE-1"],
                    requirement_refs=["M-REQ-1"],
                )
            ],
            verification=VerificationSubtask(id="2.V", checks=["done"]),
        ),
    ]

    return Spec(
        prd=PRDDocument(
            frontmatter=PRDFrontmatter(
                spec_id="M",
                spec_name="missing_refs_test",
                title="Missing Refs Test",
                created_at="2024-01-01",
                updated_at="2024-01-01",
                owner="test",
                source="internal",
            ),
            body="Test spec.",
        ),
        requirements=Requirements(
            spec_id="M",
            spec_name="missing_refs_test",
            introduction="Test spec.",
            requirements=[req],
            execution_paths=[
                ExecutionPath(
                    id="M-PATH-1",
                    title="Main path",
                    steps=[PathStep(actor="User", action="Go"), PathStep(actor="System", action="Run")],
                )
            ],
        ),
        test_spec=TestSpec(
            spec_id="M",
            spec_name="missing_refs_test",
            test_cases=[tc],
            smoke_tests=[SmokeTest(id="TS-M-SMOKE-1", execution_path_id="M-PATH-1", description="Smoke")],
        ),
        tasks=Tasks(
            spec_id="M",
            spec_name="missing_refs_test",
            task_groups=groups,
            traceability=[
                TraceabilityEntry(requirement_id="M-REQ-1.1", test_spec_id="TS-M-1", task_id="1.1"),
            ],
        ),
    )


class TestCheckMissingSubtaskRefs:
    """_check_missing_subtask_refs warns on empty refs."""

    def test_warns_when_both_refs_empty(self) -> None:
        group = TaskGroup(
            id=1,
            kind=TaskGroupKind.TESTS,
            title="Tests",
            subtasks=[Subtask(id="1.1", title="Do thing")],
            verification=VerificationSubtask(id="1.V", checks=["pass"]),
        )
        warnings = _check_missing_subtask_refs(group)
        assert len(warnings) == 1
        assert "requirement_refs" in warnings[0].message
        assert "test_spec_refs" in warnings[0].message

    def test_warns_when_only_requirement_refs_empty(self) -> None:
        group = TaskGroup(
            id=1,
            kind=TaskGroupKind.TESTS,
            title="Tests",
            subtasks=[Subtask(id="1.1", title="Do thing", test_spec_refs=["TS-X-1"])],
            verification=VerificationSubtask(id="1.V", checks=["pass"]),
        )
        warnings = _check_missing_subtask_refs(group)
        assert len(warnings) == 1
        assert "requirement_refs" in warnings[0].message
        assert "test_spec_refs" not in warnings[0].message

    def test_warns_when_only_test_spec_refs_empty(self) -> None:
        group = TaskGroup(
            id=1,
            kind=TaskGroupKind.TESTS,
            title="Tests",
            subtasks=[Subtask(id="1.1", title="Do thing", requirement_refs=["X-REQ-1"])],
            verification=VerificationSubtask(id="1.V", checks=["pass"]),
        )
        warnings = _check_missing_subtask_refs(group)
        assert len(warnings) == 1
        assert "test_spec_refs" in warnings[0].message
        assert "requirement_refs" not in warnings[0].message

    def test_no_warning_when_both_populated(self) -> None:
        group = TaskGroup(
            id=1,
            kind=TaskGroupKind.TESTS,
            title="Tests",
            subtasks=[
                Subtask(id="1.1", title="Do thing", requirement_refs=["X-REQ-1"], test_spec_refs=["TS-X-1"])
            ],
            verification=VerificationSubtask(id="1.V", checks=["pass"]),
        )
        warnings = _check_missing_subtask_refs(group)
        assert len(warnings) == 0

    def test_skips_wiring_verification_groups(self) -> None:
        group = TaskGroup(
            id=3,
            kind=TaskGroupKind.WIRING_VERIFICATION,
            title="Wiring",
            subtasks=[Subtask(id="3.1", title="Verify")],
            verification=VerificationSubtask(id="3.V", checks=["done"]),
        )
        warnings = _check_missing_subtask_refs(group)
        assert len(warnings) == 0

    def test_warns_per_subtask(self) -> None:
        group = TaskGroup(
            id=1,
            kind=TaskGroupKind.TESTS,
            title="Tests",
            subtasks=[
                Subtask(id="1.1", title="First"),
                Subtask(id="1.2", title="Second"),
            ],
            verification=VerificationSubtask(id="1.V", checks=["pass"]),
        )
        warnings = _check_missing_subtask_refs(group)
        assert len(warnings) == 2
        ids = {w.entity_id for w in warnings}
        assert ids == {"1.1", "1.2"}


class TestValidateIncludesMissingRefsWarning:
    """validate() includes missing refs warnings in its output."""

    def test_missing_refs_warning_in_validate_result(self) -> None:
        spec = _make_spec_with_subtask()
        result = validate(spec)
        assert result.valid is True
        missing_warnings = [w for w in result.warnings if "scoped rendering" in w.message]
        assert len(missing_warnings) >= 1

    def test_no_warning_when_refs_populated(self) -> None:
        spec = _make_spec_with_subtask(req_refs=["M-REQ-1.1"], ts_refs=["TS-M-1"])
        result = validate(spec)
        missing_warnings = [w for w in result.warnings if "scoped rendering" in w.message]
        assert len(missing_warnings) == 0

    def test_wiring_group_missing_refs_not_warned(self) -> None:
        """Wiring verification subtask with empty req_refs doesn't trigger warning."""
        spec = _make_spec_with_subtask(req_refs=["M-REQ-1.1"], ts_refs=["TS-M-1"])
        wiring_group = spec.tasks.task_groups[1]
        wiring_subtask = wiring_group.subtasks[0]
        assert wiring_subtask.requirement_refs == ["M-REQ-1"]
        warnings = _check_missing_subtask_refs(wiring_group)
        assert len(warnings) == 0
