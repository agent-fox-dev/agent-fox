"""Tests for scoped rendering inference fallback (issue #752).

Validates that render_individual_scoped() infers refs from traceability
and subtask text when subtask requirement_refs/test_spec_refs are empty,
instead of falling back to full unscoped rendering.
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
from afspec.render import (
    _infer_refs_from_subtask_text,
    _infer_refs_from_traceability,
    render_individual_scoped,
)


def _make_spec(
    *,
    subtask_req_refs: list[str] | None = None,
    subtask_ts_refs: list[str] | None = None,
    traceability: list[TraceabilityEntry] | None = None,
    subtask_title: str = "Implement feature",
    subtask_details: list[str] | None = None,
) -> Spec:
    """Build a minimal spec with configurable refs and traceability."""
    crit = Criterion(
        id="X-REQ-1.1",
        ears_pattern=EARSPattern.UBIQUITOUS,
        system="the system",
        action="do something",
    )
    edge = Criterion(
        id="X-REQ-1.E1",
        ears_pattern=EARSPattern.UBIQUITOUS,
        system="the system",
        action="handle error",
    )
    tc = TestCase(id="TS-X-1", requirement_id="X-REQ-1.1", kind="unit", description="Test 1")
    et = TestCase(id="TS-X-E1", requirement_id="X-REQ-1.E1", kind="unit", description="Edge test")

    req = Requirement(
        id="X-REQ-1",
        title="Feature requirement",
        user_story=UserStory(role="dev", goal="test", benefit="coverage"),
        acceptance_criteria=[crit],
        edge_cases=[edge],
    )

    subtask = Subtask(
        id="1.1",
        title=subtask_title,
        details=subtask_details or ["Write the code"],
        test_spec_refs=subtask_ts_refs or [],
        requirement_refs=subtask_req_refs or [],
    )

    default_traceability = [
        TraceabilityEntry(requirement_id="X-REQ-1.1", test_spec_id="TS-X-1", task_id="1.1"),
        TraceabilityEntry(requirement_id="X-REQ-1.E1", test_spec_id="TS-X-E1", task_id="1.1"),
    ]

    groups = [
        TaskGroup(
            id=1,
            kind=TaskGroupKind.TESTS,
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
                    test_spec_refs=["TS-X-SMOKE-1"],
                    requirement_refs=["X-REQ-1"],
                )
            ],
            verification=VerificationSubtask(id="2.V", checks=["done"]),
        ),
    ]

    return Spec(
        prd=PRDDocument(
            frontmatter=PRDFrontmatter(
                spec_id="X",
                spec_name="inference_test",
                title="Inference Test Spec",
                created_at="2024-01-01",
                updated_at="2024-01-01",
                owner="test",
                source="internal",
            ),
            body="Test spec for inference fallback.",
        ),
        requirements=Requirements(
            spec_id="X",
            spec_name="inference_test",
            introduction="Test spec.",
            requirements=[req],
            execution_paths=[
                ExecutionPath(
                    id="X-PATH-1",
                    title="Main path",
                    steps=[PathStep(actor="User", action="Invoke"), PathStep(actor="System", action="Run")],
                )
            ],
        ),
        test_spec=TestSpec(
            spec_id="X",
            spec_name="inference_test",
            test_cases=[tc, et],
            smoke_tests=[SmokeTest(id="TS-X-SMOKE-1", execution_path_id="X-PATH-1", description="Smoke")],
        ),
        tasks=Tasks(
            spec_id="X",
            spec_name="inference_test",
            task_groups=groups,
            traceability=traceability if traceability is not None else default_traceability,
        ),
    )


class TestInferRefsFromTraceability:
    """_infer_refs_from_traceability uses the traceability table."""

    def test_infers_refs_from_matching_task_ids(self) -> None:
        spec = _make_spec()
        req_ids, ts_ids = _infer_refs_from_traceability(spec, target_group=1)
        assert req_ids == {"X-REQ-1.1", "X-REQ-1.E1"}
        assert ts_ids == {"TS-X-1", "TS-X-E1"}

    def test_empty_when_no_matching_task_ids(self) -> None:
        spec = _make_spec(traceability=[
            TraceabilityEntry(requirement_id="X-REQ-1.1", test_spec_id="TS-X-1", task_id="3.1"),
        ])
        req_ids, ts_ids = _infer_refs_from_traceability(spec, target_group=1)
        assert req_ids == set()
        assert ts_ids == set()

    def test_empty_when_no_traceability(self) -> None:
        spec = _make_spec(traceability=[])
        req_ids, ts_ids = _infer_refs_from_traceability(spec, target_group=1)
        assert req_ids == set()
        assert ts_ids == set()


class TestInferRefsFromSubtaskText:
    """_infer_refs_from_subtask_text scans title and details."""

    def test_infers_from_title(self) -> None:
        spec = _make_spec(subtask_title="Implement X-REQ-1.1 and TS-X-1")
        group = spec.tasks.task_groups[0]
        req_ids, ts_ids = _infer_refs_from_subtask_text(spec, group)
        assert "X-REQ-1.1" in req_ids
        assert "TS-X-1" in ts_ids

    def test_infers_from_details(self) -> None:
        spec = _make_spec(subtask_details=["Handle X-REQ-1.E1 edge case", "Test with TS-X-E1"])
        group = spec.tasks.task_groups[0]
        req_ids, ts_ids = _infer_refs_from_subtask_text(spec, group)
        assert "X-REQ-1.E1" in req_ids
        assert "TS-X-E1" in ts_ids

    def test_ignores_unknown_ids(self) -> None:
        spec = _make_spec(subtask_title="See FAKE-REQ-99.1 and TS-FAKE-99")
        group = spec.tasks.task_groups[0]
        req_ids, ts_ids = _infer_refs_from_subtask_text(spec, group)
        assert req_ids == set()
        assert ts_ids == set()

    def test_empty_when_no_ids_in_text(self) -> None:
        spec = _make_spec(subtask_title="Write the code", subtask_details=["No references here"])
        group = spec.tasks.task_groups[0]
        req_ids, ts_ids = _infer_refs_from_subtask_text(spec, group)
        assert req_ids == set()
        assert ts_ids == set()


class TestRenderIndividualScopedInference:
    """render_individual_scoped uses inference when subtask refs are empty."""

    def test_scoped_via_traceability_inference(self) -> None:
        """When subtask refs are empty but traceability exists, scoped rendering activates."""
        spec = _make_spec()
        result = render_individual_scoped(spec, target_group=1)
        assert "## Spec Overview" in result["requirements"]
        assert "1 of" in result["requirements"]

    def test_scoped_via_text_inference(self) -> None:
        """When traceability is empty but subtask text contains IDs, scoped rendering activates."""
        spec = _make_spec(
            traceability=[],
            subtask_title="Implement X-REQ-1.1",
            subtask_details=["Test with TS-X-1"],
        )
        result = render_individual_scoped(spec, target_group=1)
        assert "## Spec Overview" in result["requirements"]

    def test_falls_back_when_all_inference_fails(self) -> None:
        """When no refs, traceability, or text IDs exist, falls back to unscoped."""
        spec = _make_spec(
            traceability=[],
            subtask_title="Write the code",
            subtask_details=["Just implement it"],
        )
        from afspec import render_individual

        scoped = render_individual_scoped(spec, target_group=1)
        unscoped = render_individual(spec)
        assert scoped["requirements"] == unscoped["requirements"]
        assert scoped["test_spec"] == unscoped["test_spec"]

    def test_direct_refs_take_precedence(self) -> None:
        """When subtask refs are populated, inference is not needed."""
        spec = _make_spec(
            subtask_req_refs=["X-REQ-1.1"],
            subtask_ts_refs=["TS-X-1"],
        )
        result = render_individual_scoped(spec, target_group=1)
        assert "## Spec Overview" in result["requirements"]
