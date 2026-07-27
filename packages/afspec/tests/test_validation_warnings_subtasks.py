"""Tests for too-many-subtasks warning (group-level).

TS-08-15: afspec emits a ValidationWarning and returns valid: true when
           a task group contains more than 6 subtasks excluding the
           verification subtask.
TS-08-16: afspec excludes the verification subtask from the count when
           checking the 6-subtask ceiling.
TS-08-17: afspec does NOT emit a ValidationWarning for the subtask count
           rule when a task group has exactly 6 or fewer subtasks
           (excluding the verification subtask).
TS-08-E6: A task group with exactly 7 subtasks excluding the
           verification subtask emits exactly one ValidationWarning for
           the too-many-subtasks rule.

These tests are in RED PHASE — they will fail with AttributeError because
validate() currently returns a plain list, not a structured result with
.valid / .errors / .warnings attributes.

CRITICAL NOTE (reviewer finding): All fixtures include a final
kind: wiring_verification group to avoid triggering the pre-existing
_validate_task_group_structure error that fires when the last group is
not wiring_verification.
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
from afspec.validation import validate

# ---------------------------------------------------------------------------
# Helper — build spec with a configurable number of subtasks
# ---------------------------------------------------------------------------


def _build_spec_with_n_subtasks(n_subtasks: int) -> Spec:
    """Build a structurally valid spec where group 1 has *n_subtasks* non-verification subtasks.

    Each subtask has at most 1 test_spec_ref to stay well below the
    per-subtask and per-group ref thresholds, isolating the subtask count
    rule.  A verification subtask (``1.V``) is always appended.
    """
    criteria: list[Criterion] = []
    test_cases: list[TestCase] = []
    traceability: list[TraceabilityEntry] = []
    subtasks: list[Subtask] = []

    for i in range(1, n_subtasks + 1):
        cid = f"SC-REQ-1.{i}"
        tsid = f"TS-SC-{i}"
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
                task_id=f"1.{i}",
            )
        )
        subtasks.append(
            Subtask(
                id=f"1.{i}",
                title=f"Subtask {i}",
                test_spec_refs=[tsid],
                requirement_refs=["SC-REQ-1"],
            )
        )

    groups = [
        TaskGroup(
            id=1,
            kind=TaskGroupKind.TESTS,
            title="Tests group",
            subtasks=subtasks,
            verification=VerificationSubtask(id="1.V", checks=["pass"]),
        ),
        TaskGroup(
            id=2,
            kind=TaskGroupKind.WIRING_VERIFICATION,
            title="Wiring verification",
            subtasks=[
                Subtask(
                    id="2.1",
                    title="Trace execution paths and stub/dead-code audit",
                    test_spec_refs=["TS-SC-SMOKE-1"],
                    requirement_refs=["SC-REQ-1"],
                )
            ],
            verification=VerificationSubtask(id="2.V", checks=["done"]),
        ),
    ]

    req = Requirement(
        id="SC-REQ-1",
        title="Subtask count requirement",
        user_story=UserStory(role="dev", goal="test", benefit="coverage"),
        acceptance_criteria=criteria,
    )

    return Spec(
        prd=PRDDocument(
            frontmatter=PRDFrontmatter(
                spec_id="SC",
                spec_name="subtask_count_test",
                title="Subtask Count Test Spec",
                created_at="2024-01-01",
                updated_at="2024-01-01",
                owner="test",
                source="internal",
            ),
            body="Test spec for subtask count warning.",
        ),
        requirements=Requirements(
            spec_id="SC",
            spec_name="subtask_count_test",
            introduction="Test spec for subtask count warning.",
            requirements=[req],
            execution_paths=[ExecutionPath(id="SC-PATH-1", title="Main path", steps=[PathStep(actor="User", action="Invoke"), PathStep(actor="System", action="Run")])],
        ),
        test_spec=TestSpec(
            spec_id="SC",
            spec_name="subtask_count_test",
            test_cases=test_cases,
            smoke_tests=[SmokeTest(id="TS-SC-SMOKE-1", execution_path_id="SC-PATH-1", description="Wiring smoke test")],
        ),
        tasks=Tasks(
            spec_id="SC",
            spec_name="subtask_count_test",
            task_groups=groups,
            traceability=traceability,
        ),
    )


def _is_subtask_count_warning(warning: object, group_id: int = 1) -> bool:
    """Check if a warning is about too many subtasks for a specific group."""
    text = str(warning).lower()
    return "subtask" in text and str(group_id) in str(warning)


# ---------------------------------------------------------------------------
# TS-08-15: More than 6 subtasks → ValidationWarning, valid=True
# ---------------------------------------------------------------------------


class TestTooManySubtasksWarning:
    """TS-08-15: warning emitted when group has >6 non-verification subtasks."""

    def test_valid_is_true(self) -> None:
        """7 non-verification subtasks: valid remains True (warning, not error)."""
        spec = _build_spec_with_n_subtasks(7)
        result = validate(spec)
        assert result.valid is True

    def test_no_errors_emitted(self) -> None:
        """No ValidationErrors when only the subtask count warning triggers."""
        spec = _build_spec_with_n_subtasks(7)
        result = validate(spec)
        assert len(result.errors) == 0

    def test_at_least_one_warning(self) -> None:
        """At least one ValidationWarning about subtask count is emitted."""
        spec = _build_spec_with_n_subtasks(7)
        result = validate(spec)
        assert len(result.warnings) >= 1
        assert any(_is_subtask_count_warning(w) for w in result.warnings), (
            f"Expected a subtask count warning, got: {result.warnings}"
        )


# ---------------------------------------------------------------------------
# TS-08-16: Verification subtask excluded from count
# ---------------------------------------------------------------------------


class TestVerificationSubtaskExcluded:
    """TS-08-16: verification subtask is excluded from the 6-subtask ceiling."""

    def test_exactly_6_plus_verification_no_warning(self) -> None:
        """6 non-verification subtasks + 1 verification: no warning.

        The verification subtask (1.V) is excluded, so the count is 6,
        which is at the boundary but not over.
        """
        spec = _build_spec_with_n_subtasks(6)
        result = validate(spec)
        subtask_warnings = [w for w in result.warnings if _is_subtask_count_warning(w)]
        assert len(subtask_warnings) == 0, (
            f"Expected no subtask count warning for exactly 6 non-verification subtasks, got: {subtask_warnings}"
        )


# ---------------------------------------------------------------------------
# TS-08-17: No warning when subtask count <= 6
# ---------------------------------------------------------------------------


class TestNoWarningAtOrBelowSubtaskThreshold:
    """TS-08-17: no subtask count warning when count is exactly 6 or fewer."""

    def test_exactly_6_subtasks_no_warning(self) -> None:
        """Exactly 6 non-verification subtasks: no warning emitted."""
        spec = _build_spec_with_n_subtasks(6)
        result = validate(spec)
        subtask_warnings = [w for w in result.warnings if _is_subtask_count_warning(w)]
        assert len(subtask_warnings) == 0

    def test_5_subtasks_no_warning(self) -> None:
        """5 non-verification subtasks: well below threshold, no warning."""
        spec = _build_spec_with_n_subtasks(5)
        result = validate(spec)
        subtask_warnings = [w for w in result.warnings if _is_subtask_count_warning(w)]
        assert len(subtask_warnings) == 0

    def test_3_subtasks_no_warning(self) -> None:
        """3 non-verification subtasks: clearly below threshold, no warning."""
        spec = _build_spec_with_n_subtasks(3)
        result = validate(spec)
        subtask_warnings = [w for w in result.warnings if _is_subtask_count_warning(w)]
        assert len(subtask_warnings) == 0


# ---------------------------------------------------------------------------
# TS-08-E6: Exactly 7 subtasks → exactly one warning
# ---------------------------------------------------------------------------


class TestExactly7SubtasksOneWarning:
    """TS-08-E6: exactly 7 non-verification subtasks → exactly 1 warning."""

    def test_valid_is_true(self) -> None:
        """valid remains True with exactly 7 subtasks."""
        spec = _build_spec_with_n_subtasks(7)
        result = validate(spec)
        assert result.valid is True

    def test_exactly_one_subtask_count_warning(self) -> None:
        """Exactly one warning about too many subtasks for group 1."""
        spec = _build_spec_with_n_subtasks(7)
        result = validate(spec)
        subtask_warnings = [w for w in result.warnings if _is_subtask_count_warning(w)]
        assert len(subtask_warnings) == 1, (
            f"Expected exactly 1 subtask count warning for 7 subtasks, got {len(subtask_warnings)}: {subtask_warnings}"
        )
