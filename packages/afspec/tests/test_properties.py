"""Property-based tests for validation invariants.

TS-08-P1: For any spec input processed by afspec, if validate() returns
zero ValidationError objects, the result's valid field is True regardless
of how many ValidationWarning objects are present.

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
            data.draw(st.integers(min_value=1, max_value=6), label=f"subtasks_g{i}")
            for i in range(num_groups)
        ]
        refs_per_subtask = [
            [
                data.draw(_ref_count_strategy, label=f"refs_g{g}s{s}")
                for s in range(subtask_counts[g])
            ]
            for g in range(num_groups)
        ]

        spec = _build_structurally_valid_spec(num_groups, subtask_counts, refs_per_subtask)
        result = validate(spec)

        # The invariant: zero errors ⇒ valid is True
        if len(result.errors) == 0:
            assert result.valid is True, (
                f"validate() returned zero errors but valid={result.valid}. "
                f"Warnings count: {len(result.warnings)}"
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
            assert result.valid is True, (
                f"Single group with {num_refs} refs: zero errors but valid={result.valid}"
            )

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
                f"Spec with many warning triggers but zero errors: "
                f"valid should be True, got {result.valid}"
            )
