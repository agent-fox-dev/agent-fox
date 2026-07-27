"""Tests for single-subtask overload warning.

TS-08-18: afspec emits a ValidationWarning identifying the offending
           subtask ID and actual count when a single subtask references
           more than 8 test_spec_refs.
TS-08-19: afspec does NOT emit a ValidationWarning for the single-subtask
           overload rule when a subtask references exactly 8 or fewer
           test_spec_refs.
TS-08-E7: afspec treats a subtask with no test_spec_refs field or an
           empty list as having a count of zero and does not emit a
           ValidationWarning for single-subtask overload.

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
# Helper — build spec with a single subtask having N refs
# ---------------------------------------------------------------------------


def _build_spec_with_subtask_refs(
    subtask_ref_counts: list[int],
) -> Spec:
    """Build a structurally valid spec with subtasks having specified ref counts.

    Parameters
    ----------
    subtask_ref_counts:
        Number of ``test_spec_refs`` for each non-verification subtask.
        A count of ``-1`` means the subtask has no ``test_spec_refs``
        field set (defaults to empty list).
    """
    criteria: list[Criterion] = []
    test_cases: list[TestCase] = []
    traceability: list[TraceabilityEntry] = []
    subtasks: list[Subtask] = []
    ref_counter = 0

    for s_idx, ref_count in enumerate(subtask_ref_counts):
        subtask_id = f"1.{s_idx + 1}"
        refs: list[str] = []

        actual_count = max(0, ref_count)
        for _ in range(actual_count):
            ref_counter += 1
            cid = f"SO-REQ-1.{ref_counter}"
            tsid = f"TS-SO-{ref_counter}"
            criteria.append(
                Criterion(
                    id=cid,
                    ears_pattern=EARSPattern.UBIQUITOUS,
                    system="the system",
                    action=f"action {ref_counter}",
                )
            )
            test_cases.append(
                TestCase(
                    id=tsid,
                    requirement_id=cid,
                    kind="unit",
                    description=f"Test {ref_counter}",
                )
            )
            traceability.append(
                TraceabilityEntry(
                    requirement_id=cid,
                    test_spec_id=tsid,
                    task_id=subtask_id,
                )
            )
            refs.append(tsid)

        if ref_count == -1:
            # No test_spec_refs set — use default (empty list)
            subtasks.append(
                Subtask(
                    id=subtask_id,
                    title=f"Subtask {s_idx + 1}",
                    requirement_refs=["SO-REQ-1"],
                )
            )
        else:
            subtasks.append(
                Subtask(
                    id=subtask_id,
                    title=f"Subtask {s_idx + 1}",
                    test_spec_refs=refs,
                    requirement_refs=["SO-REQ-1"],
                )
            )

    # Add a baseline criterion+test if none were generated
    if not criteria:
        criteria.append(
            Criterion(
                id="SO-REQ-1.0",
                ears_pattern=EARSPattern.UBIQUITOUS,
                system="the system",
                action="baseline action",
            )
        )
        test_cases.append(
            TestCase(
                id="TS-SO-0",
                requirement_id="SO-REQ-1.0",
                kind="unit",
                description="Baseline test",
            )
        )
        traceability.append(
            TraceabilityEntry(
                requirement_id="SO-REQ-1.0",
                test_spec_id="TS-SO-0",
                task_id="1.1",
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
                    test_spec_refs=["TS-SO-SMOKE-1"],
                    requirement_refs=["SO-REQ-1"],
                )
            ],
            verification=VerificationSubtask(id="2.V", checks=["done"]),
        ),
    ]

    req = Requirement(
        id="SO-REQ-1",
        title="Subtask overload requirement",
        user_story=UserStory(role="dev", goal="test", benefit="coverage"),
        acceptance_criteria=criteria,
    )

    return Spec(
        prd=PRDDocument(
            frontmatter=PRDFrontmatter(
                spec_id="SO",
                spec_name="subtask_overload_test",
                title="Subtask Overload Test Spec",
                created_at="2024-01-01",
                updated_at="2024-01-01",
                owner="test",
                source="internal",
            ),
            body="Test spec for single subtask overload warning.",
        ),
        requirements=Requirements(
            spec_id="SO",
            spec_name="subtask_overload_test",
            introduction="Test spec for single subtask overload warning.",
            requirements=[req],
            execution_paths=[ExecutionPath(id="SO-PATH-1", title="Main path", steps=[PathStep(actor="User", action="Invoke"), PathStep(actor="System", action="Run")])],
        ),
        test_spec=TestSpec(
            spec_id="SO",
            spec_name="subtask_overload_test",
            test_cases=test_cases,
            smoke_tests=[SmokeTest(id="TS-SO-SMOKE-1", execution_path_id="SO-PATH-1", description="Wiring smoke test")],
        ),
        tasks=Tasks(
            spec_id="SO",
            spec_name="subtask_overload_test",
            task_groups=groups,
            traceability=traceability,
        ),
    )


# ---------------------------------------------------------------------------
# TS-08-18: Single subtask with >8 refs → warning identifying subtask
# ---------------------------------------------------------------------------


class TestSingleSubtaskOverloadWarning:
    """TS-08-18: warning emitted for subtask with >8 test_spec_refs."""

    def test_valid_is_true(self) -> None:
        """Subtask overload triggers warning, not error; valid remains True."""
        spec = _build_spec_with_subtask_refs([9])
        result = validate(spec)
        assert result.valid is True

    def test_no_errors_emitted(self) -> None:
        """No ValidationErrors when only the subtask overload warning triggers."""
        spec = _build_spec_with_subtask_refs([9])
        result = validate(spec)
        assert len(result.errors) == 0

    def test_at_least_one_warning(self) -> None:
        """At least one ValidationWarning is emitted for the overloaded subtask."""
        spec = _build_spec_with_subtask_refs([9])
        result = validate(spec)
        assert len(result.warnings) >= 1

    def test_warning_references_subtask_id(self) -> None:
        """The warning identifies subtask '1.1' as the offender."""
        spec = _build_spec_with_subtask_refs([9])
        result = validate(spec)
        assert any("1.1" in str(w) for w in result.warnings), (
            f"Expected warning referencing subtask '1.1', got: {result.warnings}"
        )

    def test_warning_mentions_count(self) -> None:
        """The warning mentions the actual ref count (9)."""
        spec = _build_spec_with_subtask_refs([9])
        result = validate(spec)
        assert any("9" in str(w) for w in result.warnings), (
            f"Expected warning mentioning count 9, got: {result.warnings}"
        )


# ---------------------------------------------------------------------------
# TS-08-19: No warning when subtask has exactly 8 or fewer refs
# ---------------------------------------------------------------------------


class TestNoWarningAtOrBelowSubtaskRefThreshold:
    """TS-08-19: no overload warning when subtask has <= 8 test_spec_refs."""

    def test_exactly_8_refs_no_warning(self) -> None:
        """Subtask with exactly 8 refs: at boundary, no warning."""
        spec = _build_spec_with_subtask_refs([8])
        result = validate(spec)
        overload_warnings = [w for w in result.warnings if "1.1" in str(w)]
        assert len(overload_warnings) == 0, f"Expected no overload warning for 8 refs, got: {overload_warnings}"

    def test_5_refs_no_warning(self) -> None:
        """Subtask with 5 refs: well below threshold, no warning."""
        spec = _build_spec_with_subtask_refs([5])
        result = validate(spec)
        overload_warnings = [w for w in result.warnings if "1.1" in str(w)]
        assert len(overload_warnings) == 0

    def test_1_ref_no_warning(self) -> None:
        """Subtask with 1 ref: minimal, no warning."""
        spec = _build_spec_with_subtask_refs([1])
        result = validate(spec)
        overload_warnings = [w for w in result.warnings if "1.1" in str(w)]
        assert len(overload_warnings) == 0


# ---------------------------------------------------------------------------
# TS-08-E7: No test_spec_refs or empty list → count=0, no warning
# ---------------------------------------------------------------------------


class TestEmptyRefsNoOverloadWarning:
    """TS-08-E7: missing or empty test_spec_refs treated as zero, no warning."""

    def test_no_test_spec_refs_field(self) -> None:
        """Subtask with no test_spec_refs set (defaults to []): no warning."""
        spec = _build_spec_with_subtask_refs([-1])  # -1 = no field set
        result = validate(spec)
        overload_warnings = [w for w in result.warnings if "1.1" in str(w)]
        assert len(overload_warnings) == 0, (
            f"Expected no overload warning for subtask with no test_spec_refs, got: {overload_warnings}"
        )

    def test_empty_test_spec_refs_list(self) -> None:
        """Subtask with explicitly empty test_spec_refs=[]: no warning."""
        spec = _build_spec_with_subtask_refs([0])
        result = validate(spec)
        overload_warnings = [w for w in result.warnings if "1.2" in str(w)]
        assert len(overload_warnings) == 0

    def test_mixed_no_field_and_empty_list(self) -> None:
        """Two subtasks: one with no field, one with empty list: no warnings."""
        spec = _build_spec_with_subtask_refs([-1, 0])
        result = validate(spec)
        overload_warnings = [w for w in result.warnings if "1.1" in str(w) or "1.2" in str(w)]
        assert len(overload_warnings) == 0, (
            f"Expected no overload warnings for subtasks with no/empty refs, got: {overload_warnings}"
        )
