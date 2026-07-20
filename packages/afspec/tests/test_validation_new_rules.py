"""Tests for new validation rules: cross-spec-4, cross-spec-5, wiring-1, cross-file-9.

cross-spec-4: Interface contract mismatch along dependency edges.
cross-spec-5: Missing boundary coverage -- no execution path references
              an upstream actor.
wiring-1:     Wiring_verification group semantic checks (test_spec_refs,
              smoke refs, stub audit).
cross-file-9: Subtask requirement_refs must resolve to known IDs.
"""

from __future__ import annotations

from pathlib import Path

from afspec.discovery import DependencyGraph
from afspec.models import (
    Criterion,
    DependencyEdge,
    EARSPattern,
    ExecutionPath,
    ExternalAPI,
    PathStep,
    PRDDocument,
    PRDFrontmatter,
    Requirement,
    Requirements,
    SmokeTest,
    Spec,
    Subtask,
    TaskDependency,
    TaskGroup,
    TaskGroupKind,
    Tasks,
    TestCase,
    TestSpec,
    TraceabilityEntry,
    UserStory,
    VerificationSubtask,
)
from afspec.validation import validate, validate_cross_file, validate_cross_spec


def _make_spec(
    spec_id: str,
    glossary: dict[str, str] | None = None,
    external_apis: list[ExternalAPI] | None = None,
    dependencies: list[TaskDependency] | None = None,
    criteria: list[Criterion] | None = None,
    execution_paths: list[ExecutionPath] | None = None,
) -> Spec:
    reqs_list = []
    if criteria:
        reqs_list = [Requirement(id=f"{spec_id}-REQ-1", title="Test requirement", acceptance_criteria=criteria)]
    smoke_tests = []
    test_cases = []
    traceability = []
    ep = execution_paths or []
    for path in ep:
        st = SmokeTest(
            id=f"TS-{spec_id}-SMOKE-{path.id.split('-')[-1]}",
            execution_path_id=path.id,
            description=f"Smoke for {path.id}",
        )
        smoke_tests.append(st)
    if criteria:
        for c in criteria:
            tc_id = f"TS-{spec_id}-{c.id.split('.')[-1]}"
            test_cases.append(TestCase(id=tc_id, requirement_id=c.id, kind="unit", description="test"))
            traceability.append(TraceabilityEntry(requirement_id=c.id, test_spec_id=tc_id, task_id="1.1"))
    smoke_refs = [st.id for st in smoke_tests]
    reqs = Requirements(
        spec_id=spec_id,
        spec_name=spec_id,
        glossary=glossary or {},
        external_apis=external_apis or [],
        requirements=reqs_list,
        execution_paths=ep,
    )
    tasks = Tasks(
        spec_id=spec_id,
        spec_name=spec_id,
        dependencies=dependencies or [],
        task_groups=[
            TaskGroup(
                id=1,
                kind=TaskGroupKind.TESTS,
                title="Tests",
                subtasks=[Subtask(id="1.1", title="t", test_spec_refs=[], requirement_refs=[])],
                verification=VerificationSubtask(id="1.V", checks=["pass"]),
            ),
            TaskGroup(
                id=2,
                kind=TaskGroupKind.WIRING_VERIFICATION,
                title="Wiring verification",
                subtasks=[
                    Subtask(
                        id="2.1",
                        title="Trace paths and stub/dead-code audit",
                        test_spec_refs=smoke_refs or [f"TS-{spec_id}-SMOKE-1"],
                        requirement_refs=[],
                    )
                ],
                verification=VerificationSubtask(id="2.V", checks=["done"]),
            ),
        ],
        traceability=traceability,
    )
    test_spec = TestSpec(spec_id=spec_id, spec_name=spec_id, test_cases=test_cases, smoke_tests=smoke_tests)
    return Spec(requirements=reqs, test_spec=test_spec, tasks=tasks)


def _make_graph(edges: list[DependencyEdge] | None = None, spec_ids: list[str] | None = None) -> DependencyGraph:
    return DependencyGraph(edge_list=edges or [], all_spec_ids=spec_ids or [])


class TestCrossSpec4InterfaceContractMismatch:
    def test_mismatched_return_contract_produces_error(self) -> None:
        crit_a = Criterion(
            id="01-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="invoke `NewClient()` to create a connection",
            return_contract="*http.Client",
        )
        crit_b = Criterion(
            id="02-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="call `NewClient()` and use the returned client",
            return_contract="*sdk.Client",
        )
        spec_a = _make_spec("01", criteria=[crit_a])
        spec_b = _make_spec("02", criteria=[crit_b], dependencies=[TaskDependency(depends_on_spec="01")])
        errors = validate_cross_spec(
            {"01": spec_a, "02": spec_b},
            _make_graph(edges=[DependencyEdge(from_spec="01", to_spec="02")], spec_ids=["01", "02"]),
        )
        cs4 = [e for e in errors if e.rule == "cross-spec-4"]
        assert len(cs4) == 1
        assert "NewClient()" in cs4[0].message

    def test_matching_return_contract_no_error(self) -> None:
        crit_a = Criterion(
            id="01-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="invoke `NewClient()`",
            return_contract="*http.Client",
        )
        crit_b = Criterion(
            id="02-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="call `NewClient()`",
            return_contract="*http.Client",
        )
        spec_a = _make_spec("01", criteria=[crit_a])
        spec_b = _make_spec("02", criteria=[crit_b], dependencies=[TaskDependency(depends_on_spec="01")])
        errors = validate_cross_spec(
            {"01": spec_a, "02": spec_b},
            _make_graph(edges=[DependencyEdge(from_spec="01", to_spec="02")], spec_ids=["01", "02"]),
        )
        assert [e for e in errors if e.rule == "cross-spec-4"] == []

    def test_no_return_contract_no_error(self) -> None:
        crit_a = Criterion(
            id="01-REQ-1.1", ears_pattern=EARSPattern.UBIQUITOUS, system="system", action="invoke `DoWork()`"
        )
        crit_b = Criterion(
            id="02-REQ-1.1", ears_pattern=EARSPattern.UBIQUITOUS, system="system", action="call `DoWork()`"
        )
        spec_a = _make_spec("01", criteria=[crit_a])
        spec_b = _make_spec("02", criteria=[crit_b], dependencies=[TaskDependency(depends_on_spec="01")])
        errors = validate_cross_spec(
            {"01": spec_a, "02": spec_b},
            _make_graph(edges=[DependencyEdge(from_spec="01", to_spec="02")], spec_ids=["01", "02"]),
        )
        assert [e for e in errors if e.rule == "cross-spec-4"] == []

    def test_no_dependency_edge_no_check(self) -> None:
        crit_a = Criterion(
            id="01-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="invoke `Func()`",
            return_contract="int",
        )
        crit_b = Criterion(
            id="02-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="call `Func()`",
            return_contract="string",
        )
        errors = validate_cross_spec(
            {"01": _make_spec("01", criteria=[crit_a]), "02": _make_spec("02", criteria=[crit_b])},
            _make_graph(spec_ids=["01", "02"]),
        )
        assert [e for e in errors if e.rule == "cross-spec-4"] == []


class TestCrossSpec5BoundaryCoverage:
    def test_missing_upstream_actor_produces_error(self) -> None:
        path_a = ExecutionPath(
            id="01-PATH-1",
            title="Auth",
            steps=[PathStep(actor="Auth Service", action="validate"), PathStep(actor="Token Store", action="lookup")],
        )
        path_b = ExecutionPath(
            id="02-PATH-1",
            title="API",
            steps=[PathStep(actor="API Handler", action="process"), PathStep(actor="Database", action="query")],
        )
        spec_a = _make_spec("01", execution_paths=[path_a])
        spec_b = _make_spec("02", execution_paths=[path_b], dependencies=[TaskDependency(depends_on_spec="01")])
        errors = validate_cross_spec(
            {"01": spec_a, "02": spec_b},
            _make_graph(edges=[DependencyEdge(from_spec="01", to_spec="02")], spec_ids=["01", "02"]),
        )
        cs5 = [e for e in errors if e.rule == "cross-spec-5"]
        assert len(cs5) == 1

    def test_downstream_references_upstream_actor_no_error(self) -> None:
        path_a = ExecutionPath(id="01-PATH-1", title="Auth", steps=[PathStep(actor="Auth Service", action="validate")])
        path_b = ExecutionPath(
            id="02-PATH-1",
            title="API",
            steps=[PathStep(actor="API Handler", action="process"), PathStep(actor="Auth Service", action="check")],
        )
        spec_a = _make_spec("01", execution_paths=[path_a])
        spec_b = _make_spec("02", execution_paths=[path_b], dependencies=[TaskDependency(depends_on_spec="01")])
        errors = validate_cross_spec(
            {"01": spec_a, "02": spec_b},
            _make_graph(edges=[DependencyEdge(from_spec="01", to_spec="02")], spec_ids=["01", "02"]),
        )
        assert [e for e in errors if e.rule == "cross-spec-5"] == []

    def test_case_insensitive_actor_match(self) -> None:
        path_a = ExecutionPath(id="01-PATH-1", title="F", steps=[PathStep(actor="Config Manager", action="load")])
        path_b = ExecutionPath(id="02-PATH-1", title="F", steps=[PathStep(actor="config manager", action="read")])
        spec_a = _make_spec("01", execution_paths=[path_a])
        spec_b = _make_spec("02", execution_paths=[path_b], dependencies=[TaskDependency(depends_on_spec="01")])
        errors = validate_cross_spec(
            {"01": spec_a, "02": spec_b},
            _make_graph(edges=[DependencyEdge(from_spec="01", to_spec="02")], spec_ids=["01", "02"]),
        )
        assert [e for e in errors if e.rule == "cross-spec-5"] == []

    def test_upstream_no_execution_paths_skipped(self) -> None:
        path_b = ExecutionPath(id="02-PATH-1", title="F", steps=[PathStep(actor="Handler", action="process")])
        spec_a = _make_spec("01")
        spec_b = _make_spec("02", execution_paths=[path_b], dependencies=[TaskDependency(depends_on_spec="01")])
        errors = validate_cross_spec(
            {"01": spec_a, "02": spec_b},
            _make_graph(edges=[DependencyEdge(from_spec="01", to_spec="02")], spec_ids=["01", "02"]),
        )
        assert [e for e in errors if e.rule == "cross-spec-5"] == []


def _build_wiring_spec(
    *,
    wiring_test_spec_refs=None,
    wiring_title="Wiring",
    wiring_details=None,
    verification_checks=None,
    smoke_tests_list=None,
    execution_paths_list=None,
):
    crit = Criterion(id="W-REQ-1.1", ears_pattern=EARSPattern.UBIQUITOUS, system="system", action="do something")
    tc = TestCase(id="TS-W-1", requirement_id="W-REQ-1.1", kind="unit", description="t")
    req = Requirement(
        id="W-REQ-1",
        title="Test",
        user_story=UserStory(role="dev", goal="test", benefit="value"),
        acceptance_criteria=[crit],
    )
    groups = [
        TaskGroup(
            id=1,
            kind=TaskGroupKind.TESTS,
            title="Tests",
            subtasks=[Subtask(id="1.1", title="t", test_spec_refs=["TS-W-1"], requirement_refs=["W-REQ-1"])],
            verification=VerificationSubtask(id="1.V", checks=["pass"]),
        ),
        TaskGroup(
            id=2,
            kind=TaskGroupKind.WIRING_VERIFICATION,
            title="Wiring",
            subtasks=[
                Subtask(
                    id="2.1",
                    title=wiring_title,
                    test_spec_refs=wiring_test_spec_refs or [],
                    requirement_refs=["W-REQ-1"],
                    details=wiring_details or [],
                )
            ],
            verification=VerificationSubtask(id="2.V", checks=verification_checks or ["done"]),
        ),
    ]
    return Spec(
        prd=PRDDocument(
            frontmatter=PRDFrontmatter(
                spec_id="W",
                spec_name="wiring_test",
                title="Wiring Test Spec",
                created_at="2024-01-01",
                updated_at="2024-01-01",
                owner="test",
                source="internal",
            ),
            body="Wiring test spec.",
        ),
        requirements=Requirements(
            spec_id="W",
            spec_name="wiring_test",
            introduction="Wiring test.",
            requirements=[req],
            execution_paths=list(execution_paths_list or []),
        ),
        test_spec=TestSpec(
            spec_id="W", spec_name="wiring_test", test_cases=[tc], smoke_tests=list(smoke_tests_list or [])
        ),
        tasks=Tasks(
            spec_id="W",
            spec_name="wiring_test",
            task_groups=groups,
            traceability=[TraceabilityEntry(requirement_id="W-REQ-1.1", test_spec_id="TS-W-1", task_id="1.1")],
        ),
    )


class TestWiring1NoTestSpecRefs:
    def test_no_refs_produces_error(self) -> None:
        result = validate(_build_wiring_spec(wiring_test_spec_refs=[], wiring_title="Stub/dead-code audit"))
        assert any("test_spec_refs" in e.message for e in result.errors if e.rule == "wiring-1")

    def test_valid_is_false(self) -> None:
        result = validate(_build_wiring_spec(wiring_test_spec_refs=[], wiring_title="Stub/dead-code audit"))
        assert result.valid is False


class TestWiring1NoSmokeRef:
    def test_no_smoke_ref_produces_error(self) -> None:
        result = validate(_build_wiring_spec(wiring_test_spec_refs=["TS-W-1"], wiring_title="Stub/dead-code audit"))
        assert any("smoke" in e.message.lower() for e in result.errors if e.rule == "wiring-1")


class TestWiring1NoStubAudit:
    def test_no_stub_audit_produces_error(self) -> None:
        smoke = SmokeTest(id="TS-W-SMOKE-1", execution_path_id="W-PATH-1", description="s")
        path = ExecutionPath(id="W-PATH-1", title="p", steps=[PathStep(actor="S", action="a")])
        result = validate(
            _build_wiring_spec(
                wiring_test_spec_refs=["TS-W-SMOKE-1"],
                wiring_title="Trace execution paths",
                smoke_tests_list=[smoke],
                execution_paths_list=[path],
            )
        )
        assert any("stub" in e.message.lower() for e in result.errors if e.rule == "wiring-1")


class TestWiring1FullyValid:
    def test_valid_wiring_no_errors(self) -> None:
        smoke = SmokeTest(id="TS-W-SMOKE-1", execution_path_id="W-PATH-1", description="s")
        path = ExecutionPath(id="W-PATH-1", title="p", steps=[PathStep(actor="S", action="a")])
        result = validate(
            _build_wiring_spec(
                wiring_test_spec_refs=["TS-W-SMOKE-1"],
                wiring_title="Trace paths and stub/dead-code audit",
                smoke_tests_list=[smoke],
                execution_paths_list=[path],
            )
        )
        assert [e for e in result.errors if e.rule == "wiring-1"] == []
        assert result.valid is True

    def test_stub_in_verification_checks_passes(self) -> None:
        smoke = SmokeTest(id="TS-W-SMOKE-1", execution_path_id="W-PATH-1", description="s")
        path = ExecutionPath(id="W-PATH-1", title="p", steps=[PathStep(actor="S", action="a")])
        result = validate(
            _build_wiring_spec(
                wiring_test_spec_refs=["TS-W-SMOKE-1"],
                wiring_title="Trace paths",
                verification_checks=["No unjustified stubs remain"],
                smoke_tests_list=[smoke],
                execution_paths_list=[path],
            )
        )
        assert [e for e in result.errors if e.rule == "wiring-1"] == []

    def test_stub_in_details_passes(self) -> None:
        smoke = SmokeTest(id="TS-W-SMOKE-1", execution_path_id="W-PATH-1", description="s")
        path = ExecutionPath(id="W-PATH-1", title="p", steps=[PathStep(actor="S", action="a")])
        result = validate(
            _build_wiring_spec(
                wiring_test_spec_refs=["TS-W-SMOKE-1"],
                wiring_title="Trace paths",
                wiring_details=["Run stub/dead-code audit"],
                smoke_tests_list=[smoke],
                execution_paths_list=[path],
            )
        )
        assert [e for e in result.errors if e.rule == "wiring-1"] == []


class TestWiring1DeadCodeVariant:
    def test_dead_code_keyword_passes(self) -> None:
        smoke = SmokeTest(id="TS-W-SMOKE-1", execution_path_id="W-PATH-1", description="s")
        path = ExecutionPath(id="W-PATH-1", title="p", steps=[PathStep(actor="S", action="a")])
        result = validate(
            _build_wiring_spec(
                wiring_test_spec_refs=["TS-W-SMOKE-1"],
                wiring_title="dead-code audit and path tracing",
                smoke_tests_list=[smoke],
                execution_paths_list=[path],
            )
        )
        assert [e for e in result.errors if e.rule == "wiring-1"] == []


class TestCrossFile9RequirementRefs:
    def test_dangling_requirement_ref_produces_error(self, valid_spec_dir: Path) -> None:
        from afspec import load_spec

        spec = load_spec(valid_spec_dir)
        spec.tasks.task_groups[0].subtasks[0].requirement_refs = ["99-REQ-99.99"]
        cf9 = [e for e in validate_cross_file(spec) if e.rule == "cross-file-9"]
        assert len(cf9) == 1 and "99-REQ-99.99" in cf9[0].message

    def test_valid_requirement_ref_no_error(self, valid_spec_dir: Path) -> None:
        from afspec import load_spec

        assert [e for e in validate_cross_file(load_spec(valid_spec_dir)) if e.rule == "cross-file-9"] == []

    def test_criterion_id_as_requirement_ref(self, valid_spec_dir: Path) -> None:
        from afspec import load_spec

        spec = load_spec(valid_spec_dir)
        spec.tasks.task_groups[0].subtasks[0].requirement_refs = ["01-REQ-1.1"]
        assert [e for e in validate_cross_file(spec) if e.rule == "cross-file-9"] == []

    def test_requirement_level_id_as_ref(self, valid_spec_dir: Path) -> None:
        from afspec import load_spec

        spec = load_spec(valid_spec_dir)
        spec.tasks.task_groups[0].subtasks[0].requirement_refs = ["01-REQ-1"]
        assert [e for e in validate_cross_file(spec) if e.rule == "cross-file-9"] == []

    def test_multiple_dangling_refs(self, valid_spec_dir: Path) -> None:
        from afspec import load_spec

        spec = load_spec(valid_spec_dir)
        spec.tasks.task_groups[0].subtasks[0].requirement_refs = ["BAD-1", "BAD-2"]
        assert len([e for e in validate_cross_file(spec) if e.rule == "cross-file-9"]) == 2

    def test_empty_requirement_refs_no_error(self, valid_spec_dir: Path) -> None:
        from afspec import load_spec

        spec = load_spec(valid_spec_dir)
        spec.tasks.task_groups[0].subtasks[0].requirement_refs = []
        assert [e for e in validate_cross_file(spec) if e.rule == "cross-file-9"] == []


class TestGoldenSpecNoFalsePositives:
    def test_validate_golden_spec_still_valid(self, valid_spec_dir: Path) -> None:
        from afspec import load_spec

        result = validate(load_spec(valid_spec_dir))
        assert result.valid is True, f"Golden spec should be valid, got errors: {result.errors}"
