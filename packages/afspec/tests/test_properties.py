"""Property-based tests for validation invariants.

TS-08-P1: For any spec input processed by afspec, if validate() returns
zero ValidationError objects, the result's valid field is True regardless
of how many ValidationWarning objects are present.
TS-08-P2: For any task group whose total test_spec_refs count across all
subtasks exceeds 15, afspec must emit at least one ValidationWarning
referencing that group.
TS-08-P3: For any task group where the number of non-verification
subtasks exceeds 6, afspec must emit at least one ValidationWarning
referencing that group.
TS-08-P4: For any subtask whose test_spec_refs list length exceeds 8,
afspec must emit at least one ValidationWarning referencing that subtask.

These tests are in RED PHASE — they will fail with AttributeError because
validate() currently returns a plain list, not a structured result with
.valid / .errors / .warnings attributes.

CRITICAL NOTE (reviewer finding): All generated specs include a final
kind: wiring_verification group and first group kind: tests to avoid
pre-existing structural ValidationErrors from _validate_task_group_structure.
"""

from __future__ import annotations

from hypothesis import given, settings
from hypothesis import strategies as st

from afspec.models import (
    Criterion,
    EARSPattern,
    PRDDocument,
    PRDFrontmatter,
    Requirement,
    Requirements,
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
# Hypothesis strategies and spec builder
# ---------------------------------------------------------------------------


def _build_structurally_valid_spec(
    num_groups: int,
    subtask_counts: list[int],
    refs_per_subtask: list[list[int]],
) -> Spec:
    """Build a Spec that passes all structural checks (no errors).

    Parameters
    ----------
    num_groups:
        Number of non-wiring-verification groups (a wiring_verification
        group is always appended automatically).
    subtask_counts:
        Number of subtasks per group (length must equal *num_groups*).
    refs_per_subtask:
        For each group, a list of ref counts per subtask.  Each subtask
        gets ``refs_per_subtask[g][s]`` unique test_spec_ref IDs.

    The builder guarantees:
    - First group is ``kind: tests``.
    - Last group is ``kind: wiring_verification``.
    - All ``test_spec_refs`` resolve to entries in ``test_spec.test_cases``.
    - All ``requirement_id`` references in ``test_cases`` resolve to
      acceptance criteria in requirements.
    - spec_id / spec_name are consistent across artifacts.
    """
    all_criteria: list[Criterion] = []
    all_test_cases: list[TestCase] = []
    all_traceability: list[TraceabilityEntry] = []
    ref_counter = 0

    groups: list[TaskGroup] = []

    for g_idx in range(num_groups):
        subtasks: list[Subtask] = []
        group_id = g_idx + 1
        # First group is always tests; others are standard
        kind = TaskGroupKind.TESTS if g_idx == 0 else TaskGroupKind.STANDARD

        for s_idx in range(subtask_counts[g_idx]):
            num_refs = refs_per_subtask[g_idx][s_idx] if s_idx < len(refs_per_subtask[g_idx]) else 0
            subtask_refs: list[str] = []

            for _ in range(num_refs):
                ref_counter += 1
                cid = f"P-REQ-1.{ref_counter}"
                tsid = f"TS-P-{ref_counter}"
                all_criteria.append(
                    Criterion(
                        id=cid,
                        ears_pattern=EARSPattern.UBIQUITOUS,
                        system="the system",
                        action=f"action {ref_counter}",
                    )
                )
                all_test_cases.append(
                    TestCase(
                        id=tsid,
                        requirement_id=cid,
                        kind="unit",
                        description=f"Test {ref_counter}",
                    )
                )
                all_traceability.append(
                    TraceabilityEntry(
                        requirement_id=cid,
                        test_spec_id=tsid,
                        task_id=f"{group_id}.{s_idx + 1}",
                    )
                )
                subtask_refs.append(tsid)

            subtasks.append(
                Subtask(
                    id=f"{group_id}.{s_idx + 1}",
                    title=f"Subtask {group_id}.{s_idx + 1}",
                    test_spec_refs=subtask_refs,
                    requirement_refs=["P-REQ-1"],
                )
            )

        groups.append(
            TaskGroup(
                id=group_id,
                kind=kind,
                title=f"Group {group_id}",
                subtasks=subtasks,
                verification=VerificationSubtask(
                    id=f"{group_id}.V",
                    checks=["check"],
                ),
            )
        )

    # Add a baseline criterion+test if none were generated (empty refs)
    if not all_criteria:
        all_criteria.append(
            Criterion(
                id="P-REQ-1.0",
                ears_pattern=EARSPattern.UBIQUITOUS,
                system="the system",
                action="baseline action",
            )
        )
        all_test_cases.append(
            TestCase(
                id="TS-P-0",
                requirement_id="P-REQ-1.0",
                kind="unit",
                description="Baseline test",
            )
        )
        all_traceability.append(
            TraceabilityEntry(
                requirement_id="P-REQ-1.0",
                test_spec_id="TS-P-0",
                task_id="1.1",
            )
        )

    # Always append a wiring_verification group as the last group
    wv_id = len(groups) + 1
    groups.append(
        TaskGroup(
            id=wv_id,
            kind=TaskGroupKind.WIRING_VERIFICATION,
            title="Wiring verification",
            subtasks=[
                Subtask(
                    id=f"{wv_id}.1",
                    title="Verify wiring",
                    requirement_refs=["P-REQ-1"],
                ),
            ],
            verification=VerificationSubtask(
                id=f"{wv_id}.V",
                checks=["All wired"],
            ),
        ),
    )

    req = Requirement(
        id="P-REQ-1",
        title="Property test requirement",
        user_story=UserStory(role="dev", goal="test", benefit="coverage"),
        acceptance_criteria=all_criteria,
    )

    return Spec(
        prd=PRDDocument(
            frontmatter=PRDFrontmatter(
                spec_id="P",
                spec_name="property_test",
                title="Property Test Spec",
                created_at="2024-01-01",
                updated_at="2024-01-01",
                owner="test",
                source="internal",
            ),
            body="Property test spec.",
        ),
        requirements=Requirements(
            spec_id="P",
            spec_name="property_test",
            introduction="Property test spec.",
            requirements=[req],
        ),
        test_spec=TestSpec(
            spec_id="P",
            spec_name="property_test",
            test_cases=all_test_cases,
        ),
        tasks=Tasks(
            spec_id="P",
            spec_name="property_test",
            task_groups=groups,
            traceability=all_traceability,
        ),
    )


# Strategy for refs-per-subtask counts (0–25 refs each)
_ref_count_strategy = st.integers(min_value=0, max_value=25)


# ---------------------------------------------------------------------------
# TS-08-P1: Warnings never block validity
# ---------------------------------------------------------------------------


class TestWarningsNeverBlockValidity:
    """TS-08-P1: zero errors ⇒ valid is True, regardless of warning count.

    For any spec input processed by afspec, if validate() returns zero
    ValidationError objects, the result's valid field is True regardless
    of how many ValidationWarning objects are present.
    """

    @given(
        num_groups=st.integers(min_value=1, max_value=5),
        data=st.data(),
    )
    @settings(max_examples=50, deadline=None)
    def test_no_errors_implies_valid_true(
        self,
        num_groups: int,
        data: st.DataObject,
    ) -> None:
        """If validate() produces zero errors, valid must be True."""
        subtask_counts = [
            data.draw(st.integers(min_value=1, max_value=6), label=f"subtasks_g{i}") for i in range(num_groups)
        ]
        refs_per_subtask = [
            [data.draw(_ref_count_strategy, label=f"refs_g{g}s{s}") for s in range(subtask_counts[g])]
            for g in range(num_groups)
        ]

        spec = _build_structurally_valid_spec(num_groups, subtask_counts, refs_per_subtask)
        result = validate(spec)

        # The invariant: zero errors ⇒ valid is True
        if len(result.errors) == 0:
            assert result.valid is True, (
                f"validate() returned zero errors but valid={result.valid}. Warnings count: {len(result.warnings)}"
            )

    @given(
        num_refs=st.integers(min_value=0, max_value=25),
    )
    @settings(max_examples=30, deadline=None)
    def test_single_group_varying_refs(self, num_refs: int) -> None:
        """Single-group spec with varying ref counts: zero errors ⇒ valid."""
        spec = _build_structurally_valid_spec(
            num_groups=1,
            subtask_counts=[2],
            refs_per_subtask=[[num_refs, 0]],
        )
        result = validate(spec)

        if len(result.errors) == 0:
            assert result.valid is True, f"Single group with {num_refs} refs: zero errors but valid={result.valid}"

    def test_valid_true_with_zero_warnings(self) -> None:
        """Spec with no warnings at all: valid must be True if no errors."""
        spec = _build_structurally_valid_spec(
            num_groups=1,
            subtask_counts=[2],
            refs_per_subtask=[[1, 1]],
        )
        result = validate(spec)

        if len(result.errors) == 0:
            assert result.valid is True
            # With small ref counts, there should be no warnings either
            assert len(result.warnings) == 0

    def test_valid_true_with_many_warnings(self) -> None:
        """Spec with many warnings: valid must still be True if no errors."""
        # Build a spec that should trigger multiple warnings:
        # - group with >15 total refs
        # - subtask with >8 refs
        spec = _build_structurally_valid_spec(
            num_groups=1,
            subtask_counts=[3],
            refs_per_subtask=[[10, 10, 10]],  # 30 total refs, each subtask >8
        )
        result = validate(spec)

        if len(result.errors) == 0:
            assert result.valid is True, (
                f"Spec with many warning triggers but zero errors: valid should be True, got {result.valid}"
            )


# ---------------------------------------------------------------------------
# TS-08-P2: Oversized group always warned
# ---------------------------------------------------------------------------


def _warning_references_group(warning: object, group_id: int) -> bool:
    """Check whether a warning references a specific group ID."""
    text = str(warning)
    # Check for group ID in the warning text — various formats accepted:
    # "Group 1 has ...", entity_id="1", etc.
    return str(group_id) in text


class TestOversizedGroupAlwaysWarned:
    """TS-08-P2: any group with total refs > 15 always gets a warning.

    For any task group whose total test_spec_refs count across all
    subtasks exceeds 15, afspec must emit at least one ValidationWarning
    referencing that group.
    """

    @given(
        total_refs=st.integers(min_value=16, max_value=50),
        num_subtasks=st.integers(min_value=1, max_value=6),
        data=st.data(),
    )
    @settings(max_examples=30, deadline=None)
    def test_oversized_refs_always_warned(
        self,
        total_refs: int,
        num_subtasks: int,
        data: st.DataObject,
    ) -> None:
        """Groups with >15 total refs always trigger a warning."""
        # Distribute refs across subtasks
        refs_distribution: list[int] = []
        remaining = total_refs
        for i in range(num_subtasks):
            if i == num_subtasks - 1:
                refs_distribution.append(remaining)
            else:
                portion = data.draw(
                    st.integers(min_value=0, max_value=remaining),
                    label=f"refs_s{i}",
                )
                refs_distribution.append(portion)
                remaining -= portion

        spec = _build_structurally_valid_spec(
            num_groups=1,
            subtask_counts=[num_subtasks],
            refs_per_subtask=[refs_distribution],
        )
        result = validate(spec)

        # Group 1 has >15 total refs — must have a warning
        assert any(_warning_references_group(w, 1) for w in result.warnings), (
            f"Group 1 has {total_refs} total refs (>15) but no warning was emitted. Warnings: {result.warnings}"
        )

    @given(
        kind_idx=st.integers(min_value=0, max_value=2),
    )
    @settings(max_examples=10, deadline=None)
    def test_oversized_refs_all_kinds(self, kind_idx: int) -> None:
        """Oversized refs warning applies to all kinds."""
        kinds = [TaskGroupKind.TESTS, TaskGroupKind.STANDARD, TaskGroupKind.CHECKPOINT]
        kind = kinds[kind_idx]

        if kind == TaskGroupKind.TESTS:
            # First group is tests — target group is group 1
            spec = _build_structurally_valid_spec(
                num_groups=1,
                subtask_counts=[2],
                refs_per_subtask=[[10, 8]],  # 18 total > 15
            )
            target_group_id = 1
        else:
            # Need tests group first, then target group with the kind
            spec = _build_structurally_valid_spec(
                num_groups=2,
                subtask_counts=[1, 2],
                refs_per_subtask=[[1], [10, 8]],  # group 2 has 18 refs
            )
            # Override the kind of the second group
            spec.tasks.task_groups[1].kind = kind
            target_group_id = 2

        result = validate(spec)
        assert any(_warning_references_group(w, target_group_id) for w in result.warnings), (
            f"Group {target_group_id} (kind={kind.value}) has >15 refs but no warning. Warnings: {result.warnings}"
        )


# ---------------------------------------------------------------------------
# TS-08-P3: Too many subtasks always warned
# ---------------------------------------------------------------------------


class TestTooManySubtasksAlwaysWarned:
    """TS-08-P3: any group with >6 non-verification subtasks gets a warning.

    For any task group where the number of non-verification subtasks
    exceeds 6, afspec must emit at least one ValidationWarning referencing
    that group.
    """

    @given(
        num_subtasks=st.integers(min_value=7, max_value=15),
        data=st.data(),
    )
    @settings(max_examples=30, deadline=None)
    def test_too_many_subtasks_always_warned(
        self,
        num_subtasks: int,
        data: st.DataObject,
    ) -> None:
        """Groups with >6 non-verification subtasks always trigger a warning."""
        # Each subtask gets 0-2 refs to stay below the per-subtask threshold
        refs_per = [
            data.draw(
                st.integers(min_value=0, max_value=2),
                label=f"refs_s{i}",
            )
            for i in range(num_subtasks)
        ]

        spec = _build_structurally_valid_spec(
            num_groups=1,
            subtask_counts=[num_subtasks],
            refs_per_subtask=[refs_per],
        )
        result = validate(spec)

        assert any(_warning_references_group(w, 1) for w in result.warnings), (
            f"Group 1 has {num_subtasks} subtasks (>6) but no warning was emitted. Warnings: {result.warnings}"
        )

    @given(
        kind_idx=st.integers(min_value=0, max_value=2),
    )
    @settings(max_examples=10, deadline=None)
    def test_too_many_subtasks_all_kinds(self, kind_idx: int) -> None:
        """Too-many-subtasks warning applies to all kinds."""
        kinds = [TaskGroupKind.TESTS, TaskGroupKind.STANDARD, TaskGroupKind.CHECKPOINT]
        kind = kinds[kind_idx]

        if kind == TaskGroupKind.TESTS:
            spec = _build_structurally_valid_spec(
                num_groups=1,
                subtask_counts=[8],
                refs_per_subtask=[[1] * 8],
            )
            target_group_id = 1
        else:
            spec = _build_structurally_valid_spec(
                num_groups=2,
                subtask_counts=[1, 8],
                refs_per_subtask=[[1], [1] * 8],
            )
            spec.tasks.task_groups[1].kind = kind
            target_group_id = 2

        result = validate(spec)
        assert any(_warning_references_group(w, target_group_id) for w in result.warnings), (
            f"Group {target_group_id} (kind={kind.value}) has 8 subtasks but no warning. Warnings: {result.warnings}"
        )


# ---------------------------------------------------------------------------
# TS-08-P4: Single subtask overload always warned
# ---------------------------------------------------------------------------


class TestSubtaskOverloadAlwaysWarned:
    """TS-08-P4: any subtask with >8 test_spec_refs gets a warning.

    For any subtask whose test_spec_refs list length exceeds 8, afspec
    must emit at least one ValidationWarning referencing that subtask.
    """

    @given(
        num_refs=st.integers(min_value=9, max_value=30),
    )
    @settings(max_examples=30, deadline=None)
    def test_overloaded_subtask_always_warned(self, num_refs: int) -> None:
        """Subtasks with >8 refs always trigger a warning."""
        spec = _build_structurally_valid_spec(
            num_groups=1,
            subtask_counts=[1],
            refs_per_subtask=[[num_refs]],
        )
        result = validate(spec)

        # Subtask 1.1 should be referenced in a warning
        assert any("1.1" in str(w) for w in result.warnings), (
            f"Subtask 1.1 has {num_refs} refs (>8) but no warning referencing it. Warnings: {result.warnings}"
        )

    @given(
        data=st.data(),
    )
    @settings(max_examples=20, deadline=None)
    def test_multiple_overloaded_subtasks_all_warned(
        self,
        data: st.DataObject,
    ) -> None:
        """When multiple subtasks exceed 8 refs, each gets a warning."""
        num_subtasks = data.draw(
            st.integers(min_value=2, max_value=4),
            label="num_subtasks",
        )
        refs = [
            data.draw(
                st.integers(min_value=9, max_value=20),
                label=f"refs_s{i}",
            )
            for i in range(num_subtasks)
        ]

        spec = _build_structurally_valid_spec(
            num_groups=1,
            subtask_counts=[num_subtasks],
            refs_per_subtask=[refs],
        )
        result = validate(spec)

        for s_idx in range(num_subtasks):
            subtask_id = f"1.{s_idx + 1}"
            assert any(subtask_id in str(w) for w in result.warnings), (
                f"Subtask {subtask_id} has {refs[s_idx]} refs (>8) but "
                f"no warning referencing it. Warnings: {result.warnings}"
            )

    @given(
        num_refs=st.integers(min_value=0, max_value=8),
    )
    @settings(max_examples=20, deadline=None)
    def test_subtask_at_or_below_threshold_no_warning(self, num_refs: int) -> None:
        """Subtasks with <= 8 refs should NOT trigger an overload warning."""
        spec = _build_structurally_valid_spec(
            num_groups=1,
            subtask_counts=[1],
            refs_per_subtask=[[num_refs]],
        )
        result = validate(spec)

        overload_warnings = [w for w in result.warnings if "1.1" in str(w)]
        assert len(overload_warnings) == 0, (
            f"Subtask 1.1 has {num_refs} refs (<=8) but got a warning: {overload_warnings}"
        )
