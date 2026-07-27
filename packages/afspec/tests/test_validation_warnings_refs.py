"""Tests for oversized test_spec_refs warning (group-level, all kinds).

TS-08-12: afspec emits a ValidationWarning (not a ValidationError) and
           returns valid: true when a task group's total test_spec_refs
           across all subtasks exceeds 15.
TS-08-13: The oversized test_spec_refs check applies to all kinds
           (tests, standard, checkpoint), not just kind: tests.
TS-08-14: afspec does NOT emit a ValidationWarning for the
           test_spec_refs rule when a group's total count is exactly 15
           or fewer.
TS-08-E5: afspec treats a task group with no subtasks or no
           test_spec_refs defined as having a count of zero and does not
           emit a ValidationWarning for the test_spec_refs rule.

These tests are in RED PHASE — they will fail with AttributeError because
validate() currently returns a plain list, not a structured result with
.valid / .errors / .warnings attributes.

CRITICAL NOTE (reviewer finding): All fixtures include a final
kind: wiring_verification group to avoid triggering the pre-existing
_validate_task_group_structure error that fires when the last group is
not wiring_verification.
"""

from __future__ import annotations

import pytest

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
from afspec.validation import validate

# ---------------------------------------------------------------------------
# Helpers — build structurally valid specs with configurable ref counts
# ---------------------------------------------------------------------------


def _make_refs_and_artifacts(
    count: int,
    prefix: str = "R",
) -> tuple[list[str], list[Criterion], list[TestCase], list[TraceabilityEntry]]:
    """Create *count* matching test_spec_refs, criteria, test cases, and traceability entries.

    Returns (ref_ids, criteria, test_cases, traceability).
    """
    refs: list[str] = []
    criteria: list[Criterion] = []
    test_cases: list[TestCase] = []
    traceability: list[TraceabilityEntry] = []

    for i in range(1, count + 1):
        cid = f"{prefix}-REQ-1.{i}"
        tsid = f"TS-{prefix}-{i}"
        refs.append(tsid)
        criteria.append(
            Criterion(
                id=cid,
                ears_pattern=EARSPattern.UBIQUITOUS,
                system="the system",
                action=f"action {i}",
            )
        )
        test_cases.append(
            TestCase(
                id=tsid,
                requirement_id=cid,
                kind="unit",
                description=f"Test case {i}",
            )
        )
        traceability.append(
            TraceabilityEntry(
                requirement_id=cid,
                test_spec_id=tsid,
                task_id="1.1",
            )
        )

    return refs, criteria, test_cases, traceability


def _build_spec_with_refs(
    total_refs: int,
    group_kind: TaskGroupKind = TaskGroupKind.TESTS,
) -> Spec:
    """Build a structurally valid spec with a group having *total_refs* test_spec_refs.

    When *group_kind* is not ``tests``, a small ``kind: tests`` group is
    prepended as group 1 (structural requirement) and the group under
    test becomes group 2.  A ``kind: wiring_verification`` group is
    always appended as the final group.
    """
    refs, criteria, test_cases, traceability = _make_refs_and_artifacts(total_refs, prefix="R")

    # Split refs across two subtasks for realism
    mid = max(1, len(refs) // 2)
    subtask1 = Subtask(
        id="1.1",
        title="First batch",
        test_spec_refs=refs[:mid],
        requirement_refs=["R-REQ-1"],
    )
    subtask2 = Subtask(
        id="1.2",
        title="Second batch",
        test_spec_refs=refs[mid:] if total_refs > 0 else [],
        requirement_refs=["R-REQ-1"],
    )

    # Update traceability task_ids to match split
    for i, entry in enumerate(traceability):
        traceability[i] = TraceabilityEntry(
            requirement_id=entry.requirement_id,
            test_spec_id=entry.test_spec_id,
            task_id="1.1" if i < mid else "1.2",
        )

    groups: list[TaskGroup] = []

    if group_kind == TaskGroupKind.TESTS:
        # The target group IS the first group
        target_group_id = 1
        groups.append(
            TaskGroup(
                id=target_group_id,
                kind=TaskGroupKind.TESTS,
                title="Tests group",
                subtasks=[subtask1, subtask2],
                verification=VerificationSubtask(id="1.V", checks=["pass"]),
            )
        )
    else:
        # Prepend a small tests group to satisfy structural rule
        baseline_cid = "R-REQ-1.0"
        baseline_tsid = "TS-R-0"
        criteria.append(
            Criterion(
                id=baseline_cid,
                ears_pattern=EARSPattern.UBIQUITOUS,
                system="the system",
                action="baseline action",
            )
        )
        test_cases.append(
            TestCase(
                id=baseline_tsid,
                requirement_id=baseline_cid,
                kind="unit",
                description="Baseline test",
            )
        )
        traceability.append(
            TraceabilityEntry(
                requirement_id=baseline_cid,
                test_spec_id=baseline_tsid,
                task_id="1.1",
            )
        )
        groups.append(
            TaskGroup(
                id=1,
                kind=TaskGroupKind.TESTS,
                title="Tests group (baseline)",
                subtasks=[
                    Subtask(
                        id="1.1",
                        title="Baseline subtask",
                        test_spec_refs=[baseline_tsid],
                        requirement_refs=["R-REQ-1"],
                    )
                ],
                verification=VerificationSubtask(id="1.V", checks=["pass"]),
            )
        )

        # Target group under test
        target_group_id = 2
        subtask1_target = Subtask(
            id="2.1",
            title="First batch",
            test_spec_refs=refs[:mid],
            requirement_refs=["R-REQ-1"],
        )
        subtask2_target = Subtask(
            id="2.2",
            title="Second batch",
            test_spec_refs=refs[mid:] if total_refs > 0 else [],
            requirement_refs=["R-REQ-1"],
        )
        # Fix traceability task_ids for group 2
        for i, entry in enumerate(traceability):
            if entry.task_id.startswith("1."):
                traceability[i] = TraceabilityEntry(
                    requirement_id=entry.requirement_id,
                    test_spec_id=entry.test_spec_id,
                    task_id=f"2.{entry.task_id.split('.')[1]}",
                )

        groups.append(
            TaskGroup(
                id=target_group_id,
                kind=group_kind,
                title=f"{group_kind.value} group",
                subtasks=[subtask1_target, subtask2_target],
                verification=VerificationSubtask(id=f"{target_group_id}.V", checks=["pass"]),
            )
        )

    # Append wiring_verification group
    wv_id = len(groups) + 1
    smoke_tsid = "TS-R-SMOKE-1"
    groups.append(
        TaskGroup(
            id=wv_id,
            kind=TaskGroupKind.WIRING_VERIFICATION,
            title="Wiring verification",
            subtasks=[
                Subtask(
                    id=f"{wv_id}.1",
                    title="Trace execution paths and stub/dead-code audit",
                    test_spec_refs=[smoke_tsid],
                    requirement_refs=["R-REQ-1"],
                )
            ],
            verification=VerificationSubtask(id=f"{wv_id}.V", checks=["done"]),
        )
    )

    req = Requirement(
        id="R-REQ-1",
        title="Refs test requirement",
        user_story=UserStory(role="dev", goal="test", benefit="coverage"),
        acceptance_criteria=criteria,
    )

    return Spec(
        prd=PRDDocument(
            frontmatter=PRDFrontmatter(
                spec_id="R",
                spec_name="refs_test",
                title="Refs Test Spec",
                created_at="2024-01-01",
                updated_at="2024-01-01",
                owner="test",
                source="internal",
            ),
            body="Test spec for test_spec_refs warning.",
        ),
        requirements=Requirements(
            spec_id="R",
            spec_name="refs_test",
            introduction="Test spec for test_spec_refs warning.",
            requirements=[req],
            execution_paths=[ExecutionPath(id="R-PATH-1", title="Main path", steps=[PathStep(actor="User", action="Invoke"), PathStep(actor="System", action="Run")])],
        ),
        test_spec=TestSpec(
            spec_id="R",
            spec_name="refs_test",
            test_cases=test_cases,
            smoke_tests=[SmokeTest(id="TS-R-SMOKE-1", execution_path_id="R-PATH-1", description="Wiring smoke test")],
        ),
        tasks=Tasks(
            spec_id="R",
            spec_name="refs_test",
            task_groups=groups,
            traceability=traceability,
        ),
    )


def _build_spec_with_only_verification_subtask() -> Spec:
    """Build a spec where the first group has only a verification subtask.

    This exercises the edge case where a group has no non-verification
    subtasks and therefore zero test_spec_refs.
    """
    baseline_cid = "EV-REQ-1.1"
    baseline_tsid = "TS-EV-1"
    criteria = [
        Criterion(
            id=baseline_cid,
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="the system",
            action="baseline action",
        )
    ]
    test_cases = [
        TestCase(
            id=baseline_tsid,
            requirement_id=baseline_cid,
            kind="unit",
            description="Baseline test",
        )
    ]

    smoke_tsid = "TS-EV-SMOKE-1"
    groups = [
        TaskGroup(
            id=1,
            kind=TaskGroupKind.TESTS,
            title="Empty tests group",
            subtasks=[],
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
                    test_spec_refs=[smoke_tsid],
                    requirement_refs=["EV-REQ-1"],
                )
            ],
            verification=VerificationSubtask(id="2.V", checks=["done"]),
        ),
    ]

    req = Requirement(
        id="EV-REQ-1",
        title="Edge case requirement",
        user_story=UserStory(role="dev", goal="test", benefit="coverage"),
        acceptance_criteria=criteria,
    )

    return Spec(
        prd=PRDDocument(
            frontmatter=PRDFrontmatter(
                spec_id="EV",
                spec_name="edge_validation",
                title="Edge Validation Spec",
                created_at="2024-01-01",
                updated_at="2024-01-01",
                owner="test",
                source="internal",
            ),
            body="Edge case spec.",
        ),
        requirements=Requirements(
            spec_id="EV",
            spec_name="edge_validation",
            introduction="Edge case spec.",
            requirements=[req],
            execution_paths=[ExecutionPath(id="EV-PATH-1", title="Main path", steps=[PathStep(actor="User", action="Invoke"), PathStep(actor="System", action="Run")])],
        ),
        test_spec=TestSpec(
            spec_id="EV",
            spec_name="edge_validation",
            test_cases=test_cases,
            smoke_tests=[SmokeTest(id=smoke_tsid, execution_path_id="EV-PATH-1", description="Wiring smoke test")],
        ),
        tasks=Tasks(
            spec_id="EV",
            spec_name="edge_validation",
            task_groups=groups,
            traceability=[
                TraceabilityEntry(
                    requirement_id=baseline_cid,
                    test_spec_id=baseline_tsid,
                    task_id="2.1",
                )
            ],
        ),
    )


# ---------------------------------------------------------------------------
# TS-08-12: Oversized test_spec_refs emits ValidationWarning, valid=True
# ---------------------------------------------------------------------------


class TestOversizedTestSpecRefsWarning:
    """TS-08-12: ValidationWarning emitted when total test_spec_refs > 15."""

    def test_valid_is_true(self) -> None:
        """Oversized refs triggers warning, not error; valid remains True."""
        spec = _build_spec_with_refs(total_refs=17)
        result = validate(spec)
        assert result.valid is True

    def test_no_errors_emitted(self) -> None:
        """No ValidationErrors when only the refs warning triggers."""
        spec = _build_spec_with_refs(total_refs=17)
        result = validate(spec)
        assert len(result.errors) == 0

    def test_at_least_one_warning(self) -> None:
        """At least one ValidationWarning is emitted for the oversized group."""
        spec = _build_spec_with_refs(total_refs=17)
        result = validate(spec)
        assert len(result.warnings) >= 1

    def test_warning_references_group(self) -> None:
        """The warning message references the offending group."""
        spec = _build_spec_with_refs(total_refs=17)
        result = validate(spec)
        warning_texts = [str(w).lower() for w in result.warnings]
        assert any("test_spec_refs" in text or "17" in text for text in warning_texts), (
            f"Expected a warning mentioning test_spec_refs or count 17, got: {result.warnings}"
        )


# ---------------------------------------------------------------------------
# TS-08-13: Oversized test_spec_refs check applies to all kinds
# ---------------------------------------------------------------------------


class TestOversizedRefsAllKinds:
    """TS-08-13: check applies to standard and checkpoint, not just tests."""

    @pytest.mark.parametrize(
        "kind",
        [TaskGroupKind.STANDARD, TaskGroupKind.CHECKPOINT],
        ids=["standard", "checkpoint"],
    )
    def test_warning_emitted_for_non_tests_kind(
        self,
        kind: TaskGroupKind,
    ) -> None:
        """Oversized refs in a non-tests group also triggers a warning."""
        spec = _build_spec_with_refs(total_refs=16, group_kind=kind)
        result = validate(spec)
        assert len(result.warnings) >= 1, (
            f"Expected at least one warning for kind={kind.value} with 16 refs, got {result.warnings}"
        )
        assert any("test_spec_refs" in str(w).lower() or "16" in str(w) for w in result.warnings), (
            f"Expected warning about test_spec_refs for kind={kind.value}"
        )


# ---------------------------------------------------------------------------
# TS-08-14: No warning when total refs <= 15
# ---------------------------------------------------------------------------


class TestNoWarningAtOrBelowThreshold:
    """TS-08-14: no test_spec_refs warning when total is exactly 15 or fewer."""

    def test_exactly_15_refs_no_warning(self) -> None:
        """Exactly 15 total refs across subtasks: no warning emitted."""
        spec = _build_spec_with_refs(total_refs=15)
        result = validate(spec)
        refs_warnings = [w for w in result.warnings if "test_spec_refs" in str(w).lower()]
        assert len(refs_warnings) == 0, f"Expected no test_spec_refs warning for exactly 15 refs, got: {refs_warnings}"

    def test_below_15_refs_no_warning(self) -> None:
        """Well below the threshold (5 refs): no warning emitted."""
        spec = _build_spec_with_refs(total_refs=5)
        result = validate(spec)
        refs_warnings = [w for w in result.warnings if "test_spec_refs" in str(w).lower()]
        assert len(refs_warnings) == 0


# ---------------------------------------------------------------------------
# TS-08-E5: Empty group (no subtasks or no test_spec_refs) → count=0
# ---------------------------------------------------------------------------


class TestEmptyGroupNoWarning:
    """TS-08-E5: group with no subtasks/refs treated as zero, no warning."""

    def test_group_with_only_verification_subtask(self) -> None:
        """Group containing only the verification subtask: no warning."""
        spec = _build_spec_with_only_verification_subtask()
        result = validate(spec)
        refs_warnings = [
            w
            for w in result.warnings
            if "test_spec_refs" in str(w).lower() and ("group 1" in str(w).lower() or "group" in str(w).lower())
        ]
        assert len(refs_warnings) == 0, (
            f"Expected no test_spec_refs warning for group with only verification subtask, got: {refs_warnings}"
        )

    def test_group_with_zero_refs_subtasks(self) -> None:
        """Group with subtasks that have zero test_spec_refs: no warning."""
        spec = _build_spec_with_refs(total_refs=0)
        result = validate(spec)
        refs_warnings = [w for w in result.warnings if "test_spec_refs" in str(w).lower()]
        assert len(refs_warnings) == 0
