"""Tests for updated validate() return contract.

TS-08-21: validate() returns both a list of ValidationError objects and a
           list of ValidationWarning objects.
TS-08-22: validate() reports valid: true when only ValidationWarning objects
           are produced and no ValidationError objects.
TS-08-E8: When validate() encounters both ValidationError and
           ValidationWarning conditions, it returns valid: false with both
           errors and warnings lists populated.

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
# Helpers — build structurally valid specs with configurable task groups
# ---------------------------------------------------------------------------


def _build_valid_spec_with_oversized_refs(total_refs: int = 16) -> Spec:
    """Build a structurally valid spec where one group has >15 test_spec_refs.

    The spec has no structural errors: first group is ``kind: tests``,
    last group is ``kind: wiring_verification``, all cross-file refs
    resolve, and spec_ids are consistent.  The only issue is the total
    ``test_spec_refs`` count across subtasks in group 1 exceeds the
    warning threshold (15).
    """
    # Create matching acceptance criteria and test cases so cross-file
    # validation passes (rule 1: requirement_id resolves, rule 2: every
    # criterion has a test case, rule 5: subtask test_spec_refs resolve).
    criteria: list[Criterion] = []
    test_cases: list[TestCase] = []
    refs_list: list[str] = []

    for i in range(1, total_refs + 1):
        cid = f"08-REQ-1.{i}"
        tsid = f"TS-08-{i}"
        criteria.append(
            Criterion(
                id=cid,
                ears_pattern=EARSPattern.UBIQUITOUS,
                system="the system",
                action=f"performs action {i}",
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
        refs_list.append(tsid)

    req = Requirement(
        id="08-REQ-1",
        title="Test requirement",
        user_story=UserStory(role="dev", goal="test", benefit="coverage"),
        acceptance_criteria=criteria,
    )

    # Split refs across two subtasks
    mid = len(refs_list) // 2
    subtask1 = Subtask(
        id="1.1",
        title="First batch",
        test_spec_refs=refs_list[:mid],
        requirement_refs=["08-REQ-1"],
    )
    subtask2 = Subtask(
        id="1.2",
        title="Second batch",
        test_spec_refs=refs_list[mid:],
        requirement_refs=["08-REQ-1"],
    )

    groups = [
        TaskGroup(
            id=1,
            kind=TaskGroupKind.TESTS,
            title="Write tests",
            subtasks=[subtask1, subtask2],
            verification=VerificationSubtask(id="1.V", checks=["All tests pass"]),
        ),
        TaskGroup(
            id=2,
            kind=TaskGroupKind.WIRING_VERIFICATION,
            title="Wiring verification",
            subtasks=[
                Subtask(
                    id="2.1",
                    title="Trace execution paths and stub/dead-code audit",
                    test_spec_refs=["TS-08-SMOKE-1"],
                    requirement_refs=["08-REQ-1"],
                ),
            ],
            verification=VerificationSubtask(id="2.V", checks=["Verified"]),
        ),
    ]

    traceability = [
        TraceabilityEntry(
            requirement_id=criteria[i].id,
            test_spec_id=test_cases[i].id,
            task_id="1.1" if i < mid else "1.2",
        )
        for i in range(total_refs)
    ]

    return Spec(
        prd=PRDDocument(
            frontmatter=PRDFrontmatter(
                spec_id="08",
                spec_name="test_validation_contract",
                title="Test Validation Contract",
                created_at="2024-01-01",
                updated_at="2024-01-01",
                owner="test",
                source="internal",
            ),
            body="Test spec for validation contract.",
        ),
        requirements=Requirements(
            spec_id="08",
            spec_name="test_validation_contract",
            introduction="Test spec for validation contract.",
            requirements=[req],
            execution_paths=[ExecutionPath(id="08-PATH-1", title="Main path", steps=[PathStep(actor="User", action="Invoke"), PathStep(actor="System", action="Run")])],
        ),
        test_spec=TestSpec(
            spec_id="08",
            spec_name="test_validation_contract",
            test_cases=test_cases,
            smoke_tests=[SmokeTest(id="TS-08-SMOKE-1", execution_path_id="08-PATH-1", description="Wiring smoke test")],
        ),
        tasks=Tasks(
            spec_id="08",
            spec_name="test_validation_contract",
            task_groups=groups,
            traceability=traceability,
        ),
    )


def _build_spec_with_error_and_warning() -> Spec:
    """Build a spec with both a structural error AND an oversized group.

    Structural error: first group has ``kind: standard`` instead of
    ``kind: tests``.
    Warning trigger: total ``test_spec_refs`` > 15 in group 1.
    """
    spec = _build_valid_spec_with_oversized_refs(total_refs=16)
    # Introduce structural error: first group must be kind: tests
    spec.tasks.task_groups[0].kind = TaskGroupKind.STANDARD
    return spec


# ---------------------------------------------------------------------------
# TS-08-21: validate() returns both errors and warnings lists
# ---------------------------------------------------------------------------


class TestValidateReturnContract:
    """TS-08-21: validate() returns a structured result with errors + warnings."""

    def test_result_has_errors_attribute(self) -> None:
        """validate() result has an 'errors' attribute that is a list."""
        spec = _build_valid_spec_with_oversized_refs(total_refs=16)
        result = validate(spec)
        assert hasattr(result, "errors"), "validate() result must have 'errors' attribute"
        assert isinstance(result.errors, list)

    def test_result_has_warnings_attribute(self) -> None:
        """validate() result has a 'warnings' attribute that is a list."""
        spec = _build_valid_spec_with_oversized_refs(total_refs=16)
        result = validate(spec)
        assert hasattr(result, "warnings"), "validate() result must have 'warnings' attribute"
        assert isinstance(result.warnings, list)

    def test_result_has_valid_attribute(self) -> None:
        """validate() result has a 'valid' boolean attribute."""
        spec = _build_valid_spec_with_oversized_refs(total_refs=16)
        result = validate(spec)
        assert hasattr(result, "valid"), "validate() result must have 'valid' attribute"
        assert isinstance(result.valid, bool)


# ---------------------------------------------------------------------------
# TS-08-22: validate() returns valid=True when only warnings are present
# ---------------------------------------------------------------------------


class TestValidateWarningsOnly:
    """TS-08-22: valid=True when only warnings are present, no errors."""

    def test_valid_true_with_warnings_only(self) -> None:
        """When only ValidationWarning objects and no errors, valid is True."""
        spec = _build_valid_spec_with_oversized_refs(total_refs=16)
        result = validate(spec)
        assert result.valid is True

    def test_empty_errors_list(self) -> None:
        """When only warnings are present, errors list is empty."""
        spec = _build_valid_spec_with_oversized_refs(total_refs=16)
        result = validate(spec)
        assert result.errors == []

    def test_warnings_list_populated(self) -> None:
        """When oversized group present, at least one warning is emitted."""
        spec = _build_valid_spec_with_oversized_refs(total_refs=16)
        result = validate(spec)
        assert len(result.warnings) >= 1


# ---------------------------------------------------------------------------
# TS-08-E8: validate() returns valid=False with both errors and warnings
# ---------------------------------------------------------------------------


class TestValidateMixedErrorsAndWarnings:
    """TS-08-E8: mixed errors + warnings → valid=False, both lists populated."""

    def test_valid_false_with_errors_and_warnings(self) -> None:
        """Structural error + oversized group → valid=False."""
        spec = _build_spec_with_error_and_warning()
        result = validate(spec)
        assert result.valid is False

    def test_errors_list_populated(self) -> None:
        """Structural error → errors list is non-empty."""
        spec = _build_spec_with_error_and_warning()
        result = validate(spec)
        assert len(result.errors) >= 1

    def test_warnings_list_populated_alongside_errors(self) -> None:
        """Oversized group → warnings list is non-empty even with errors."""
        spec = _build_spec_with_error_and_warning()
        result = validate(spec)
        assert len(result.warnings) >= 1
