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
from afspec.validation import (
    _extract_backtick_terms,
    validate,
    validate_cross_file,
    validate_cross_spec,
)


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
    crit = Criterion(id="99-REQ-1.1", ears_pattern=EARSPattern.UBIQUITOUS, system="system", action="do something")
    tc = TestCase(id="TS-99-1", requirement_id="99-REQ-1.1", kind="unit", description="t")
    req = Requirement(
        id="99-REQ-1",
        title="Test",
        user_story=UserStory(role="dev", goal="test", benefit="value"),
        acceptance_criteria=[crit],
    )
    groups = [
        TaskGroup(
            id=1,
            kind=TaskGroupKind.TESTS,
            title="Tests",
            subtasks=[Subtask(id="1.1", title="t", test_spec_refs=["TS-99-1"], requirement_refs=["99-REQ-1"])],
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
                    requirement_refs=["99-REQ-1"],
                    details=wiring_details or [],
                )
            ],
            verification=VerificationSubtask(id="2.V", checks=verification_checks or ["done"]),
        ),
    ]
    return Spec(
        prd=PRDDocument(
            frontmatter=PRDFrontmatter(
                spec_id="99",
                spec_name="99",
                title="Wiring Test Spec",
                created_at="2024-01-01",
                updated_at="2024-01-01",
                owner="test",
                source="internal",
            ),
            body="Wiring test spec.",
        ),
        requirements=Requirements(
            spec_id="99",
            spec_name="99",
            introduction="Wiring test.",
            requirements=[req],
            execution_paths=list(execution_paths_list or []),
        ),
        test_spec=TestSpec(
            spec_id="99", spec_name="99", test_cases=[tc], smoke_tests=list(smoke_tests_list or [])
        ),
        tasks=Tasks(
            spec_id="99",
            spec_name="99",
            task_groups=groups,
            traceability=[TraceabilityEntry(requirement_id="99-REQ-1.1", test_spec_id="TS-99-1", task_id="1.1")],
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
        result = validate(_build_wiring_spec(wiring_test_spec_refs=["TS-99-1"], wiring_title="Stub/dead-code audit"))
        assert any("smoke" in e.message.lower() for e in result.errors if e.rule == "wiring-1")


class TestWiring1NoStubAudit:
    def test_no_stub_audit_produces_error(self) -> None:
        smoke = SmokeTest(id="TS-99-SMOKE-1", execution_path_id="99-PATH-1", description="s")
        path = ExecutionPath(id="99-PATH-1", title="p", steps=[PathStep(actor="User", action="invoke"), PathStep(actor="S", action="a")])
        result = validate(
            _build_wiring_spec(
                wiring_test_spec_refs=["TS-99-SMOKE-1"],
                wiring_title="Trace execution paths",
                smoke_tests_list=[smoke],
                execution_paths_list=[path],
            )
        )
        assert any("stub" in e.message.lower() for e in result.errors if e.rule == "wiring-1")


class TestWiring1FullyValid:
    def test_valid_wiring_no_errors(self) -> None:
        smoke = SmokeTest(id="TS-99-SMOKE-1", execution_path_id="99-PATH-1", description="s")
        path = ExecutionPath(id="99-PATH-1", title="p", steps=[PathStep(actor="User", action="invoke"), PathStep(actor="S", action="a")])
        result = validate(
            _build_wiring_spec(
                wiring_test_spec_refs=["TS-99-SMOKE-1"],
                wiring_title="Trace paths and stub/dead-code audit",
                smoke_tests_list=[smoke],
                execution_paths_list=[path],
            )
        )
        assert [e for e in result.errors if e.rule == "wiring-1"] == []
        assert result.valid is True

    def test_stub_in_verification_checks_passes(self) -> None:
        smoke = SmokeTest(id="TS-99-SMOKE-1", execution_path_id="99-PATH-1", description="s")
        path = ExecutionPath(id="99-PATH-1", title="p", steps=[PathStep(actor="User", action="invoke"), PathStep(actor="S", action="a")])
        result = validate(
            _build_wiring_spec(
                wiring_test_spec_refs=["TS-99-SMOKE-1"],
                wiring_title="Trace paths",
                verification_checks=["No unjustified stubs remain"],
                smoke_tests_list=[smoke],
                execution_paths_list=[path],
            )
        )
        assert [e for e in result.errors if e.rule == "wiring-1"] == []

    def test_stub_in_details_passes(self) -> None:
        smoke = SmokeTest(id="TS-99-SMOKE-1", execution_path_id="99-PATH-1", description="s")
        path = ExecutionPath(id="99-PATH-1", title="p", steps=[PathStep(actor="User", action="invoke"), PathStep(actor="S", action="a")])
        result = validate(
            _build_wiring_spec(
                wiring_test_spec_refs=["TS-99-SMOKE-1"],
                wiring_title="Trace paths",
                wiring_details=["Run stub/dead-code audit"],
                smoke_tests_list=[smoke],
                execution_paths_list=[path],
            )
        )
        assert [e for e in result.errors if e.rule == "wiring-1"] == []


class TestWiring1DeadCodeVariant:
    def test_dead_code_keyword_passes(self) -> None:
        smoke = SmokeTest(id="TS-99-SMOKE-1", execution_path_id="99-PATH-1", description="s")
        path = ExecutionPath(id="99-PATH-1", title="p", steps=[PathStep(actor="User", action="invoke"), PathStep(actor="S", action="a")])
        result = validate(
            _build_wiring_spec(
                wiring_test_spec_refs=["TS-99-SMOKE-1"],
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


# ===========================================================================
# Error-path return_contract warning
# ===========================================================================


class TestErrorPathReturnContractWarning:
    """Warning for error-path criteria with null return_contract."""

    def test_error_action_null_contract_produces_warning(self) -> None:
        crit = Criterion(
            id="99-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="return an error when the input is invalid",
            return_contract=None,
        )
        spec = _build_wiring_spec(wiring_title="Stub audit")
        spec.requirements.requirements[0].acceptance_criteria = [crit]
        result = validate(spec)
        rc_warnings = [w for w in result.warnings if "return_contract" in w.message]
        assert len(rc_warnings) >= 1
        assert "99-REQ-1.1" in rc_warnings[0].entity_id

    def test_error_action_with_contract_no_warning(self) -> None:
        crit = Criterion(
            id="99-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="return an error when the input is invalid",
            return_contract="returns HTTP 400 with JSON body {error: string}",
        )
        spec = _build_wiring_spec(wiring_title="Stub audit")
        spec.requirements.requirements[0].acceptance_criteria = [crit]
        result = validate(spec)
        rc_warnings = [w for w in result.warnings if "return_contract" in w.message]
        assert rc_warnings == []

    def test_non_error_action_null_contract_no_warning(self) -> None:
        crit = Criterion(
            id="99-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="log the event to the audit trail",
            return_contract=None,
        )
        spec = _build_wiring_spec(wiring_title="Stub audit")
        spec.requirements.requirements[0].acceptance_criteria = [crit]
        result = validate(spec)
        rc_warnings = [w for w in result.warnings if "return_contract" in w.message]
        assert rc_warnings == []

    def test_edge_case_error_null_contract_produces_warning(self) -> None:
        edge = Criterion(
            id="99-REQ-1.E1",
            ears_pattern=EARSPattern.UNWANTED,
            error_condition="request is unauthorized",
            system="system",
            action="reject the request with an unauthorized error",
            return_contract=None,
        )
        spec = _build_wiring_spec(wiring_title="Stub audit")
        spec.requirements.requirements[0].edge_cases = [edge]
        result = validate(spec)
        rc_warnings = [w for w in result.warnings if "return_contract" in w.message]
        assert len(rc_warnings) >= 1

    def test_warning_does_not_block_validity(self) -> None:
        crit = Criterion(
            id="99-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="fail with a timeout error",
            return_contract=None,
        )
        smoke = SmokeTest(id="TS-99-SMOKE-1", execution_path_id="99-PATH-1", description="s")
        path = ExecutionPath(
            id="99-PATH-1", title="p", steps=[PathStep(actor="User", action="invoke"), PathStep(actor="S", action="a")]
        )
        spec = _build_wiring_spec(
            wiring_test_spec_refs=["TS-99-SMOKE-1"],
            wiring_title="Stub audit",
            smoke_tests_list=[smoke],
            execution_paths_list=[path],
        )
        spec.requirements.requirements[0].acceptance_criteria = [crit]
        result = validate(spec)
        assert result.valid is True
        assert len(result.warnings) >= 1


# ===========================================================================
# Backtick term extraction: non-domain-term filtering
# ===========================================================================


class TestExtractBacktickTerms:
    """_extract_backtick_terms filters out non-domain patterns."""

    def test_domain_terms_pass_through(self) -> None:
        text = "return a populated `SpaceManager` from `get_org`"
        terms = _extract_backtick_terms(text)
        assert terms == {"SpaceManager", "get_org"}

    def test_pure_numerics_excluded(self) -> None:
        text = "appends `-1`, `-2`, `42`, `3.14` to the list"
        terms = _extract_backtick_terms(text)
        assert terms == set()

    def test_quoted_strings_excluded(self) -> None:
        text = """returns `"user has no personal organization; contact an administrator"`"""
        terms = _extract_backtick_terms(text)
        assert terms == set()

    def test_single_quoted_strings_excluded(self) -> None:
        text = "sets status to `'active'`"
        terms = _extract_backtick_terms(text)
        assert terms == set()

    def test_single_characters_excluded(self) -> None:
        text = "separates fields with `|` and `:`"
        terms = _extract_backtick_terms(text)
        assert terms == set()

    def test_long_strings_excluded(self) -> None:
        long_term = "a" * 81
        text = f"calls `{long_term}` to process"
        terms = _extract_backtick_terms(text)
        assert terms == set()

    def test_80_char_term_included(self) -> None:
        term_80 = "a" * 80
        text = f"calls `{term_80}` to process"
        terms = _extract_backtick_terms(text)
        assert terms == {term_80}

    def test_mixed_domain_and_non_domain(self) -> None:
        text = "return `OrgConfig` with priority `-1` and status `active`"
        terms = _extract_backtick_terms(text)
        assert "OrgConfig" in terms
        assert "active" in terms
        assert "-1" not in terms


# ===========================================================================
# cross-file-10: unwanted-pattern criteria must have return_contract
# ===========================================================================


class TestUnwantedReturnContractError:
    """Unwanted criteria with null return_contract produce a cross-file error."""

    def test_unwanted_null_contract_produces_error(self) -> None:
        crit = Criterion(
            id="99-REQ-1.E1",
            ears_pattern=EARSPattern.UNWANTED,
            error_condition="request is unauthorized",
            system="system",
            action="does not process the request",
            return_contract=None,
        )
        spec = _build_wiring_spec(wiring_title="Stub audit")
        spec.requirements.requirements[0].edge_cases = [crit]
        errors = validate_cross_file(spec)
        cf10 = [e for e in errors if e.rule == "cross-file-10"]
        assert len(cf10) >= 1
        assert "99-REQ-1.E1" in cf10[0].message

    def test_unwanted_with_contract_no_error(self) -> None:
        crit = Criterion(
            id="99-REQ-1.E1",
            ears_pattern=EARSPattern.UNWANTED,
            error_condition="request is unauthorized",
            system="system",
            action="does not process the request",
            return_contract="returns HTTP 401 with JSON body {error: string}",
        )
        spec = _build_wiring_spec(wiring_title="Stub audit")
        spec.requirements.requirements[0].edge_cases = [crit]
        errors = validate_cross_file(spec)
        cf10 = [e for e in errors if e.rule == "cross-file-10"]
        assert cf10 == []

    def test_non_unwanted_null_contract_no_error(self) -> None:
        crit = Criterion(
            id="99-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="logs the event to the audit trail",
            return_contract=None,
        )
        spec = _build_wiring_spec(wiring_title="Stub audit")
        spec.requirements.requirements[0].acceptance_criteria = [crit]
        errors = validate_cross_file(spec)
        cf10 = [e for e in errors if e.rule == "cross-file-10"]
        assert cf10 == []


# ===========================================================================
# Error-condition field triggers return_contract warning
# ===========================================================================


class TestErrorConditionReturnContractWarning:
    """Non-empty error_condition with null return_contract triggers warning."""

    def test_error_condition_no_error_keywords_still_warns(self) -> None:
        crit = Criterion(
            id="99-REQ-1.E1",
            ears_pattern=EARSPattern.UNWANTED,
            error_condition="slug contains uppercase characters",
            system="system",
            action="does not split the group",
            return_contract=None,
        )
        spec = _build_wiring_spec(wiring_title="Stub audit")
        spec.requirements.requirements[0].edge_cases = [crit]
        result = validate(spec)
        rc_warnings = [w for w in result.warnings if "return_contract" in w.message]
        assert len(rc_warnings) >= 1
        assert "99-REQ-1.E1" in rc_warnings[0].entity_id

    def test_empty_error_condition_no_extra_warning(self) -> None:
        crit = Criterion(
            id="99-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="logs the event to the audit trail",
            error_condition="",
            return_contract=None,
        )
        spec = _build_wiring_spec(wiring_title="Stub audit")
        spec.requirements.requirements[0].acceptance_criteria = [crit]
        result = validate(spec)
        rc_warnings = [w for w in result.warnings if "return_contract" in w.message]
        assert rc_warnings == []


# ===========================================================================
# Golden spec regression: clean spec still passes all rules
# ===========================================================================


class TestGoldenSpecNoFalsePositives:
    def test_validate_golden_spec_still_valid(self, valid_spec_dir: Path) -> None:
        from afspec import load_spec

        result = validate(load_spec(valid_spec_dir))
        assert result.valid is True, f"Golden spec should be valid, got errors: {result.errors}"


# ===========================================================================
# ID format validation
# ===========================================================================


class TestIdFormatValidation:
    """Validate ID format, spec_id prefix, and duplicate detection."""

    def test_valid_ids_no_errors(self) -> None:
        """A spec with correctly formatted IDs should produce no id-format errors."""
        crit = Criterion(
            id="01-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="do something",
        )
        spec = _make_spec("01", criteria=[crit])
        result = validate(spec)
        id_errors = [e for e in result.errors if e.rule == "id-format"]
        assert id_errors == [], f"Expected no id-format errors, got: {id_errors}"

    def test_malformed_requirement_id(self) -> None:
        """A requirement ID without spec_id prefix should be flagged."""
        crit = Criterion(
            id="01-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="do something",
        )
        spec = _make_spec("01", criteria=[crit])
        # Mutate the requirement ID to a malformed value
        spec.requirements.requirements[0].id = "REQ-1"
        result = validate(spec)
        id_errors = [e for e in result.errors if e.rule == "id-format"]
        assert len(id_errors) >= 1
        assert any("REQ-1" in e.message for e in id_errors)

    def test_wrong_spec_id_prefix(self) -> None:
        """A criterion ID with wrong spec_id prefix should be flagged."""
        crit = Criterion(
            id="99-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="do something",
        )
        spec = _make_spec("01", criteria=[crit])
        result = validate(spec)
        id_errors = [e for e in result.errors if e.rule == "id-format"]
        prefix_errors = [e for e in id_errors if "spec_id prefix" in e.message]
        assert len(prefix_errors) >= 1
        assert any("99" in e.message and "'01'" in e.message for e in prefix_errors)

    def test_duplicate_requirement_ids(self) -> None:
        """Two requirements with the same ID should produce a duplicate error."""
        crit1 = Criterion(
            id="01-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="do something",
        )
        crit2 = Criterion(
            id="01-REQ-2.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="do another thing",
        )
        spec = _make_spec("01", criteria=[crit1])
        # Add a second requirement with the same ID as the first
        req2 = Requirement(
            id="01-REQ-1",
            title="Duplicate requirement",
            acceptance_criteria=[crit2],
        )
        spec.requirements.requirements.append(req2)
        # Also need corresponding test infrastructure
        tc2 = TestCase(id="TS-01-3", requirement_id="01-REQ-2.1", kind="unit", description="test2")
        spec.test_spec.test_cases.append(tc2)
        spec.tasks.traceability.append(
            TraceabilityEntry(requirement_id="01-REQ-2.1", test_spec_id="TS-01-3", task_id="1.1")
        )
        result = validate(spec)
        id_errors = [e for e in result.errors if e.rule == "id-format"]
        dup_errors = [e for e in id_errors if "Duplicate" in e.message]
        assert len(dup_errors) >= 1
        assert any("01-REQ-1" in e.message for e in dup_errors)


# ===========================================================================
# Vague language warning
# ===========================================================================


class TestVagueLanguageWarning:
    """Warning for vague language in criterion fields."""

    def test_vague_action_produces_warning(self) -> None:
        crit = Criterion(
            id="99-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="handle the request properly",
        )
        spec = _build_wiring_spec(wiring_title="Stub audit")
        spec.requirements.requirements[0].acceptance_criteria = [crit]
        result = validate(spec)
        vague_warnings = [w for w in result.warnings if "vague language" in w.message]
        assert len(vague_warnings) >= 1
        assert any("properly" in w.message for w in vague_warnings)

    def test_concrete_action_no_warning(self) -> None:
        crit = Criterion(
            id="99-REQ-1.1",
            ears_pattern=EARSPattern.UBIQUITOUS,
            system="system",
            action="return HTTP 200 with the resource",
        )
        spec = _build_wiring_spec(wiring_title="Stub audit")
        spec.requirements.requirements[0].acceptance_criteria = [crit]
        result = validate(spec)
        vague_warnings = [w for w in result.warnings if "vague language" in w.message]
        assert vague_warnings == []


# ===========================================================================
# Scope limit warning
# ===========================================================================


class TestScopeLimitWarning:
    """Warning when spec has too many requirements."""

    def test_11_requirements_produces_warning(self) -> None:
        spec = _build_wiring_spec(wiring_title="Stub audit")
        # Build 11 requirements
        reqs = []
        for i in range(1, 12):
            crit = Criterion(
                id=f"99-REQ-{i}.1",
                ears_pattern=EARSPattern.UBIQUITOUS,
                system="system",
                action=f"do thing {i}",
            )
            reqs.append(Requirement(id=f"99-REQ-{i}", title=f"Req {i}", acceptance_criteria=[crit]))
        spec.requirements.requirements = reqs
        # Add matching test cases and traceability
        spec.test_spec.test_cases = [
            TestCase(id=f"TS-99-{i}", requirement_id=f"99-REQ-{i}.1", kind="unit", description="t")
            for i in range(1, 12)
        ]
        spec.tasks.traceability = [
            TraceabilityEntry(requirement_id=f"99-REQ-{i}.1", test_spec_id=f"TS-99-{i}", task_id="1.1")
            for i in range(1, 12)
        ]
        result = validate(spec)
        scope_warnings = [w for w in result.warnings if "requirements" in w.message and "maximum" in w.message]
        assert len(scope_warnings) == 1
        assert "11" in scope_warnings[0].message

    def test_10_requirements_no_warning(self) -> None:
        spec = _build_wiring_spec(wiring_title="Stub audit")
        # Build exactly 10 requirements
        reqs = []
        for i in range(1, 11):
            crit = Criterion(
                id=f"99-REQ-{i}.1",
                ears_pattern=EARSPattern.UBIQUITOUS,
                system="system",
                action=f"do thing {i}",
            )
            reqs.append(Requirement(id=f"99-REQ-{i}", title=f"Req {i}", acceptance_criteria=[crit]))
        spec.requirements.requirements = reqs
        # Add matching test cases and traceability
        spec.test_spec.test_cases = [
            TestCase(id=f"TS-99-{i}", requirement_id=f"99-REQ-{i}.1", kind="unit", description="t")
            for i in range(1, 11)
        ]
        spec.tasks.traceability = [
            TraceabilityEntry(requirement_id=f"99-REQ-{i}.1", test_spec_id=f"TS-99-{i}", task_id="1.1")
            for i in range(1, 11)
        ]
        result = validate(spec)
        scope_warnings = [w for w in result.warnings if "requirements" in w.message and "maximum" in w.message]
        assert scope_warnings == []
