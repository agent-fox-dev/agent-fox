"""Tests for markdown rendering (TS-01-31 through TS-01-36, TS-01-E15, TS-01-P8, TS-01-SMOKE-6)."""

from __future__ import annotations

from pathlib import Path

from afspec import (
    Criterion,
    EARSPattern,
    load_spec,
    render_combined,
    render_ears_sentence,
    render_individual_scoped,
    render_requirements,
    render_tasks,
    render_test_spec,
)
from afspec.models import (
    CorrectnessProperty,
    ErrorHandlingEntry,
    ExecutionPath,
    ExternalAPI,
    ExternalAPISymbol,
    PathStep,
    PRDDocument,
    Requirement,
    Requirements,
    SmokeTest,
    Spec,
    Subtask,
    TaskGroup,
    Tasks,
    TestSpec,
    TraceabilityEntry,
    UserStory,
    VerificationSubtask,
)
from afspec.render import (
    render_requirements_scoped,
    render_tasks_scoped,
    render_test_spec_scoped,
)

# ---------------------------------------------------------------------------
# TS-01-31: render_requirements produces markdown (01-REQ-8.1)
# ---------------------------------------------------------------------------


def test_render_requirements(valid_spec_dir: Path) -> None:
    spec = load_spec(valid_spec_dir)
    md = render_requirements(spec.requirements)
    assert "## Glossary" in md
    assert "## Requirements" in md or "### " in md
    assert "## Correctness Properties" in md
    assert "## Execution Paths" in md
    assert "## Error Handling" in md
    assert "SHALL" in md


# ---------------------------------------------------------------------------
# TS-01-32: EARS sentences rendered from fields (01-REQ-8.2)
# ---------------------------------------------------------------------------


def test_render_ears_sentence() -> None:
    c_ubiq = Criterion(
        id="01-REQ-1.1",
        ears_pattern=EARSPattern.UBIQUITOUS,
        system="the system",
        action="log all requests",
        return_contract=None,
    )
    result = render_ears_sentence(c_ubiq)
    assert result == "THE the system SHALL log all requests"

    c_event = Criterion(
        id="01-REQ-1.2",
        ears_pattern=EARSPattern.EVENT_DRIVEN,
        trigger="the user clicks submit",
        system="the form",
        action="validate all fields",
        return_contract=None,
    )
    result = render_ears_sentence(c_event)
    assert result == "WHEN the user clicks submit, THE the form SHALL validate all fields"

    c_complex = Criterion(
        id="01-REQ-1.3",
        ears_pattern=EARSPattern.COMPLEX_EVENT,
        trigger="data arrives",
        condition="the buffer is full",
        system="the processor",
        action="flush the buffer",
        return_contract=None,
    )
    result = render_ears_sentence(c_complex)
    assert result == "WHEN data arrives AND the buffer is full, THE the processor SHALL flush the buffer"

    c_state = Criterion(
        id="01-REQ-1.4",
        ears_pattern=EARSPattern.STATE_DRIVEN,
        state="maintenance mode",
        system="the system",
        action="reject all write requests",
        return_contract=None,
    )
    result = render_ears_sentence(c_state)
    assert result == "WHILE maintenance mode, THE the system SHALL reject all write requests"

    c_unwanted = Criterion(
        id="01-REQ-1.5",
        ears_pattern=EARSPattern.UNWANTED,
        error_condition="the disk is full",
        system="the system",
        action="log an error and notify the operator",
        return_contract=None,
    )
    result = render_ears_sentence(c_unwanted)
    assert result == "IF the disk is full, THEN THE the system SHALL log an error and notify the operator"

    c_optional = Criterion(
        id="01-REQ-1.6",
        ears_pattern=EARSPattern.OPTIONAL,
        feature="debug logging is enabled",
        system="the system",
        action="write verbose logs",
        return_contract=None,
    )
    result = render_ears_sentence(c_optional)
    assert result == "WHERE debug logging is enabled, THE the system SHALL write verbose logs"


# ---------------------------------------------------------------------------
# TS-01-33: render_test_spec produces markdown (01-REQ-8.3)
# ---------------------------------------------------------------------------


def test_render_test_spec(valid_spec_dir: Path) -> None:
    spec = load_spec(valid_spec_dir)
    md = render_test_spec(spec.test_spec)
    assert "## Test Cases" in md
    assert "## Property Tests" in md
    assert "## Edge Case Tests" in md
    assert "## Smoke Tests" in md
    assert "## Coverage" in md


# ---------------------------------------------------------------------------
# TS-01-34: render_tasks produces markdown (01-REQ-8.4)
# ---------------------------------------------------------------------------


def test_render_tasks(valid_spec_dir: Path) -> None:
    spec = load_spec(valid_spec_dir)
    md = render_tasks(spec.tasks)
    assert "## Test Commands" in md
    assert "## Tasks" in md
    assert "[ ]" in md or "[x]" in md
    assert "## Traceability" in md


# ---------------------------------------------------------------------------
# TS-01-35: render_combined concatenates all sections (01-REQ-8.5)
# ---------------------------------------------------------------------------


def test_render_combined(valid_spec_dir: Path) -> None:
    spec = load_spec(valid_spec_dir)
    md = render_combined(spec)

    # PRD body comes first (contains "## Intent" from the golden fixture)
    assert "## Intent" in md

    # All four sections must appear and in the correct order:
    # PRD body < requirements < test spec < tasks
    prd_marker = md.index("## Intent")

    # Requirements section (rendered heading)
    req_start = md.index("# Requirements")
    assert req_start > prd_marker, "Requirements section must come after PRD body"

    # Test Specification section
    ts_start = md.index("# Test Specification")
    assert ts_start > req_start, "Test Specification must come after Requirements"

    # Implementation Plan (tasks) section
    tasks_start = md.index("# Implementation Plan")
    assert tasks_start > ts_start, "Implementation Plan must come after Test Specification"


# ---------------------------------------------------------------------------
# TS-01-36: Deterministic rendering (01-REQ-8.6)
# ---------------------------------------------------------------------------


def test_render_deterministic(valid_spec_dir: Path) -> None:
    spec = load_spec(valid_spec_dir)
    out1 = render_combined(spec)
    out2 = render_combined(spec)
    assert out1 == out2


# ---------------------------------------------------------------------------
# TS-01-E15: Return contract appended to EARS sentence (01-REQ-8.E1)
# ---------------------------------------------------------------------------


def test_render_return_contract() -> None:
    c = Criterion(
        id="01-REQ-1.1",
        ears_pattern=EARSPattern.EVENT_DRIVEN,
        trigger="items are submitted",
        system="the system",
        action="process them",
        return_contract="the list of created items",
    )
    result = render_ears_sentence(c)
    assert result.endswith("AND return the list of created items")


# ---------------------------------------------------------------------------
# TS-01-P8: Deterministic rendering property test (Property 8)
# ---------------------------------------------------------------------------


def test_property_deterministic_render(valid_spec_dir: Path) -> None:
    spec = load_spec(valid_spec_dir)
    out1 = render_requirements(spec.requirements)
    out2 = render_requirements(spec.requirements)
    assert out1 == out2

    out3 = render_test_spec(spec.test_spec)
    out4 = render_test_spec(spec.test_spec)
    assert out3 == out4

    out5 = render_tasks(spec.tasks)
    out6 = render_tasks(spec.tasks)
    assert out5 == out6


# ---------------------------------------------------------------------------
# TS-01-SMOKE-6: Render markdown end-to-end (PATH-6)
# ---------------------------------------------------------------------------


def test_smoke_render(valid_spec_dir: Path) -> None:
    spec = load_spec(valid_spec_dir)
    md = render_combined(spec)
    assert len(md) > 0
    # Contains PRD body content
    assert "## Intent" in md or "Intent" in md
    # Contains EARS-rendered sentences
    assert "SHALL" in md
    # Contains rendered task groups with checkboxes
    assert "[ ]" in md or "[x]" in md


# ---------------------------------------------------------------------------
# Scoped rendering (issue #717)
# ---------------------------------------------------------------------------


class TestRenderRequirementsScoped:
    """Scoped requirements rendering filters to matching refs."""

    def test_includes_matching_requirement(self, valid_spec_dir: Path) -> None:
        """Requirements with matching acceptance criteria IDs are included."""
        spec = load_spec(valid_spec_dir)
        md = render_requirements_scoped(spec.requirements, {"01-REQ-1.1"})
        assert "### 01-REQ-1:" in md
        assert "## Requirements" in md

    def test_spec_overview_lists_included_requirements(self, valid_spec_dir: Path) -> None:
        """Spec Overview lists included requirement IDs and total count."""
        spec = load_spec(valid_spec_dir)
        md = render_requirements_scoped(spec.requirements, {"01-REQ-1.1"})
        assert "## Spec Overview" in md
        assert "01-REQ-1" in md

    def test_includes_design_context_sections(self, valid_spec_dir: Path) -> None:
        """Correctness properties, execution paths, error handling always included."""
        spec = load_spec(valid_spec_dir)
        md = render_requirements_scoped(spec.requirements, {"01-REQ-1.1"})
        assert "## Correctness Properties" in md
        assert "## Execution Paths" in md
        assert "## Error Handling" in md

    def test_excludes_non_matching_requirements(self, valid_spec_dir: Path) -> None:
        """Requirements not matching the ref set are excluded from full rendering."""
        spec = load_spec(valid_spec_dir)
        # Filter with a non-existent ref
        md = render_requirements_scoped(spec.requirements, {"NONEXISTENT-REQ"})
        # Should still have the overview with 0-of-N count
        assert "## Spec Overview" in md
        assert "0 of" in md


# ---------------------------------------------------------------------------
# Scoped rendering — correctness/error filtering (issue #736)
# ---------------------------------------------------------------------------


def _make_multi_req_fixture() -> Requirements:
    """Build a Requirements with two requirements and cross-linked design context.

    REQ-1 has AC 01-REQ-1.1 and edge case 01-REQ-1.E1.
    REQ-2 has AC 01-REQ-2.1.
    Correctness property PROP-1 validates REQ-1; PROP-2 validates REQ-2.
    Error handling ERR-1 is for REQ-1.E1; ERR-2 is for REQ-2.
    One execution path and one external API are present.
    """
    req1 = Requirement(
        id="01-REQ-1",
        title="First requirement",
        user_story=UserStory(role="user", goal="do thing 1", benefit="benefit 1"),
        acceptance_criteria=[
            Criterion(
                id="01-REQ-1.1",
                ears_pattern=EARSPattern.UBIQUITOUS,
                system="the system",
                action="do thing 1 using some-lib",
            ),
        ],
        edge_cases=[
            Criterion(
                id="01-REQ-1.E1",
                ears_pattern=EARSPattern.UNWANTED,
                error_condition="bad input",
                system="the system",
                action="reject",
            ),
        ],
    )
    req2 = Requirement(
        id="01-REQ-2",
        title="Second requirement",
        user_story=UserStory(role="admin", goal="do thing 2", benefit="benefit 2"),
        acceptance_criteria=[
            Criterion(
                id="01-REQ-2.1",
                ears_pattern=EARSPattern.UBIQUITOUS,
                system="the system",
                action="do thing 2",
            ),
        ],
    )
    return Requirements(
        spec_id="01",
        spec_name="Test Spec",
        introduction="Intro",
        glossary={"term": "definition"},
        requirements=[req1, req2],
        correctness_properties=[
            CorrectnessProperty(
                id="01-PROP-1",
                title="Prop for REQ-1",
                for_any="valid input",
                invariant="thing 1 holds",
                validates=["01-REQ-1.1"],
            ),
            CorrectnessProperty(
                id="01-PROP-2",
                title="Prop for REQ-2",
                for_any="valid input",
                invariant="thing 2 holds",
                validates=["01-REQ-2.1"],
            ),
        ],
        execution_paths=[
            ExecutionPath(
                id="01-PATH-1",
                title="Happy path",
                steps=[PathStep(actor="user", action="clicks button")],
            ),
        ],
        error_handling=[
            ErrorHandlingEntry(
                id="01-ERR-1",
                condition="Bad input",
                behavior="Reject",
                requirement_id="01-REQ-1.E1",
            ),
            ErrorHandlingEntry(
                id="01-ERR-2",
                condition="Missing field",
                behavior="Return error",
                requirement_id="01-REQ-2.1",
            ),
        ],
        external_apis=[
            ExternalAPI(
                package="some-lib",
                version="1.0",
                symbols=[
                    ExternalAPISymbol(
                        name="func",
                        import_path="some_lib",
                        signature="func() -> None",
                    ),
                ],
            ),
        ],
    )


class TestScopedCorrectnessPropertyFiltering:
    """TS-NS-1 / NS-REQ-1: Correctness properties scoped to filtered_ids."""

    def test_includes_in_scope_property(self) -> None:
        """Property whose validates intersects in-scope IDs is included."""
        req = _make_multi_req_fixture()
        md = render_requirements_scoped(req, {"01-REQ-1.1"})
        assert "01-PROP-1" in md
        assert "Prop for REQ-1" in md

    def test_excludes_out_of_scope_property(self) -> None:
        """Property whose validates only references out-of-scope IDs is excluded."""
        req = _make_multi_req_fixture()
        md = render_requirements_scoped(req, {"01-REQ-1.1"})
        assert "01-PROP-2" not in md
        assert "Prop for REQ-2" not in md

    def test_property_without_validates_is_kept(self) -> None:
        """A correctness property with no validates field is always included."""
        req = _make_multi_req_fixture()
        req.correctness_properties.append(
            CorrectnessProperty(
                id="01-PROP-3",
                title="Universal prop",
                for_any="anything",
                invariant="always true",
                validates=[],
            ),
        )
        md = render_requirements_scoped(req, {"01-REQ-1.1"})
        assert "01-PROP-3" in md


class TestScopedErrorHandlingFiltering:
    """TS-NS-2 / NS-REQ-2: Error handling entries scoped to filtered_ids."""

    def test_includes_in_scope_error(self) -> None:
        """Error entry whose requirement_id is in scope is included."""
        req = _make_multi_req_fixture()
        md = render_requirements_scoped(req, {"01-REQ-1.1"})
        assert "01-ERR-1" in md
        assert "Bad input" in md

    def test_excludes_out_of_scope_error(self) -> None:
        """Error entry whose requirement_id is out of scope is excluded."""
        req = _make_multi_req_fixture()
        md = render_requirements_scoped(req, {"01-REQ-1.1"})
        assert "01-ERR-2" not in md
        assert "Missing field" not in md

    def test_error_without_requirement_id_is_kept(self) -> None:
        """An error handling entry with no requirement_id is always included."""
        req = _make_multi_req_fixture()
        req.error_handling.append(
            ErrorHandlingEntry(
                id="01-ERR-3",
                condition="Unknown",
                behavior="Log and continue",
                requirement_id="",
            ),
        )
        md = render_requirements_scoped(req, {"01-REQ-1.1"})
        assert "01-ERR-3" in md


class TestScopedOmissionNotes:
    """TS-NS-3 / NS-REQ-3: Omitted items counted and noted."""

    def test_correctness_omission_note(self) -> None:
        """When correctness properties are filtered out, an omission note appears."""
        req = _make_multi_req_fixture()
        md = render_requirements_scoped(req, {"01-REQ-1.1"})
        assert "1 additional correctness properties omitted" in md

    def test_error_omission_note(self) -> None:
        """When error handling entries are filtered out, an omission note appears."""
        req = _make_multi_req_fixture()
        md = render_requirements_scoped(req, {"01-REQ-1.1"})
        assert "1 additional error handling entries omitted" in md

    def test_no_omission_note_when_all_included(self) -> None:
        """When all items match, no omission note appears."""
        req = _make_multi_req_fixture()
        # Both REQ-1 and REQ-2 in scope
        md = render_requirements_scoped(req, {"01-REQ-1.1", "01-REQ-2.1"})
        assert "omitted" not in md


class TestScopedExternalAPIsFiltered:
    """TS-NS-3 / NS-REQ-3: External APIs filtered by package name in AC text."""

    def test_in_scope_api_included(self) -> None:
        """API whose package name appears in in-scope AC text is included."""
        req = _make_multi_req_fixture()
        # REQ-1 action includes "some-lib" → API included
        md = render_requirements_scoped(req, {"01-REQ-1.1"})
        assert "## External APIs" in md
        assert "some-lib" in md
        assert "func" in md

    def test_out_of_scope_api_omitted(self) -> None:
        """API whose package name does not appear in AC text is omitted."""
        req = _make_multi_req_fixture()
        req.external_apis.append(
            ExternalAPI(
                package="other-lib",
                version="2.0",
                symbols=[
                    ExternalAPISymbol(
                        name="other_func",
                        import_path="other_lib",
                        signature="other_func() -> None",
                    ),
                ],
            ),
        )
        md = render_requirements_scoped(req, {"01-REQ-1.1"})
        assert "some-lib" in md
        assert "other-lib" not in md

    def test_no_matching_reqs_omits_all_apis(self) -> None:
        """When no requirements match, all APIs are omitted with a note."""
        req = _make_multi_req_fixture()
        md = render_requirements_scoped(req, {"NONEXISTENT"})
        assert "## External APIs" in md
        assert "some-lib" not in md
        assert "external API packages omitted" in md

    def test_omission_note_shown(self) -> None:
        """Omission note appears when APIs are filtered out."""
        req = _make_multi_req_fixture()
        req.external_apis.append(
            ExternalAPI(
                package="unrelated-lib",
                version="3.0",
                symbols=[],
            ),
        )
        md = render_requirements_scoped(req, {"01-REQ-1.1"})
        assert "1 external API packages omitted" in md


class TestScopedExecutionPathsFiltered:
    """TS-NS-1 / NS-REQ-1, TS-NS-2 / NS-REQ-2: Execution paths filtered by ID set."""

    def test_in_scope_path_rendered(self) -> None:
        """Execution path whose ID is in execution_path_ids is rendered in full."""
        req = _make_multi_req_fixture()
        md = render_requirements_scoped(req, {"01-REQ-1.1"}, execution_path_ids={"01-PATH-1"})
        assert "## Execution Paths" in md
        assert "01-PATH-1" in md
        assert "Happy path" in md
        assert "clicks button" in md

    def test_out_of_scope_path_omitted(self) -> None:
        """TS-NS-1: Out-of-scope path omitted with summary note."""
        req = _make_multi_req_fixture()
        req.execution_paths.append(
            ExecutionPath(
                id="01-PATH-2",
                title="Error path",
                steps=[PathStep(actor="system", action="returns error")],
            ),
        )
        md = render_requirements_scoped(req, {"01-REQ-1.1"}, execution_path_ids={"01-PATH-1"})
        assert "01-PATH-1" in md
        assert "01-PATH-2" not in md
        assert "execution paths defined" in md

    def test_no_linkage_shows_summary(self) -> None:
        """TS-NS-2: Empty execution_path_ids → summary only, no step content."""
        req = _make_multi_req_fixture()
        md = render_requirements_scoped(req, {"01-REQ-1.1"}, execution_path_ids=set())
        assert "## Execution Paths" in md
        assert "clicks button" not in md
        assert "1 execution paths defined (see full spec for details)" in md

    def test_none_renders_all_paths(self) -> None:
        """When execution_path_ids is None (backward compat), all paths rendered."""
        req = _make_multi_req_fixture()
        md = render_requirements_scoped(req, {"01-REQ-1.1"})
        assert "## Execution Paths" in md
        assert "01-PATH-1" in md
        assert "Happy path" in md


class TestRenderTestSpecScoped:
    """Scoped test spec rendering filters to matching IDs."""

    def test_includes_matching_test_case(self, valid_spec_dir: Path) -> None:
        """Test cases with matching IDs are included."""
        spec = load_spec(valid_spec_dir)
        all_ids = {tc.id for tc in spec.test_spec.test_cases}
        if all_ids:
            target_id = next(iter(all_ids))
            md = render_test_spec_scoped(spec.test_spec, {target_id})
            assert target_id in md

    def test_excludes_non_matching_test_cases(self, valid_spec_dir: Path) -> None:
        """Test cases not in the filter set are excluded."""
        spec = load_spec(valid_spec_dir)
        md = render_test_spec_scoped(spec.test_spec, {"NONEXISTENT-TS"})
        # Section headers present but no test case content
        assert "## Test Cases" in md
        assert "## Coverage" in md

    def test_coverage_always_included(self, valid_spec_dir: Path) -> None:
        """Coverage section is always included regardless of filter."""
        spec = load_spec(valid_spec_dir)
        md = render_test_spec_scoped(spec.test_spec, set())
        assert "## Coverage" in md


class TestRenderTasksScoped:
    """Scoped tasks rendering shows target group in full, others as summaries."""

    def test_target_group_has_subtasks(self, valid_spec_dir: Path) -> None:
        """Target group renders with full subtask detail."""
        spec = load_spec(valid_spec_dir)
        md = render_tasks_scoped(spec.tasks, target_group=1)
        assert "1.1" in md  # subtask ID

    def test_other_groups_are_summaries(self, valid_spec_dir: Path) -> None:
        """Non-target groups render as one-line summaries (no subtask detail)."""
        spec = load_spec(valid_spec_dir)
        md = render_tasks_scoped(spec.tasks, target_group=1)
        # Group 2 should be a summary — its subtask 2.1 should NOT appear
        assert "2.1" not in md

    def test_summary_includes_completion_count(self, valid_spec_dir: Path) -> None:
        """Non-target group summaries include subtask completion count."""
        spec = load_spec(valid_spec_dir)
        md = render_tasks_scoped(spec.tasks, target_group=1)
        assert "subtasks done)" in md

    def test_test_commands_always_included(self, valid_spec_dir: Path) -> None:
        """Test Commands section is always included."""
        spec = load_spec(valid_spec_dir)
        md = render_tasks_scoped(spec.tasks, target_group=1)
        assert "## Test Commands" in md

    def test_traceability_always_included(self, valid_spec_dir: Path) -> None:
        """Traceability section is always included."""
        spec = load_spec(valid_spec_dir)
        md = render_tasks_scoped(spec.tasks, target_group=1)
        assert "## Traceability" in md


class TestRenderIndividualScoped:
    """Integration: render_individual_scoped scopes all artifacts."""

    def test_scoped_output_has_all_sections(self, valid_spec_dir: Path) -> None:
        """Scoped rendering produces all expected keys."""
        spec = load_spec(valid_spec_dir)
        result = render_individual_scoped(spec, target_group=1)
        assert "requirements" in result
        assert "test_spec" in result
        assert "tasks" in result

    def test_scoped_requirements_contain_overview(self, valid_spec_dir: Path) -> None:
        """Scoped requirements include Spec Overview."""
        spec = load_spec(valid_spec_dir)
        result = render_individual_scoped(spec, target_group=1)
        assert "## Spec Overview" in result["requirements"]

    def test_scoped_tasks_show_target_detail(self, valid_spec_dir: Path) -> None:
        """Scoped tasks show target group subtasks."""
        spec = load_spec(valid_spec_dir)
        result = render_individual_scoped(spec, target_group=1)
        assert "1.1" in result["tasks"]

    def test_nonexistent_group_falls_back_to_unscoped(self, valid_spec_dir: Path) -> None:
        """Non-existent target group falls back to unscoped rendering."""
        from afspec import render_individual

        spec = load_spec(valid_spec_dir)
        scoped = render_individual_scoped(spec, target_group=999)
        unscoped = render_individual(spec)
        assert scoped["requirements"] == unscoped["requirements"]
        assert scoped["test_spec"] == unscoped["test_spec"]


# ---------------------------------------------------------------------------
# TS-NS-4: Unscoped render_requirements unaffected (NS-REQ-4)
# ---------------------------------------------------------------------------


class TestUnscopedRenderUnaffected:
    """TS-NS-4 / NS-REQ-4: render_requirements renders all paths and APIs."""

    def test_all_execution_paths_rendered(self) -> None:
        """Unscoped rendering includes all execution paths."""
        req = _make_multi_req_fixture()
        req.execution_paths.append(
            ExecutionPath(
                id="01-PATH-2",
                title="Error path",
                steps=[PathStep(actor="system", action="returns error")],
            ),
        )
        md = render_requirements(req)
        assert "01-PATH-1" in md
        assert "01-PATH-2" in md
        assert "execution paths defined" not in md

    def test_all_external_apis_rendered(self) -> None:
        """Unscoped rendering includes all external APIs."""
        req = _make_multi_req_fixture()
        req.external_apis.append(
            ExternalAPI(
                package="other-lib",
                version="2.0",
                symbols=[
                    ExternalAPISymbol(
                        name="other_func",
                        import_path="other_lib",
                        signature="other_func() -> None",
                    ),
                ],
            ),
        )
        md = render_requirements(req)
        assert "some-lib" in md
        assert "other-lib" in md
        assert "omitted" not in md


# ---------------------------------------------------------------------------
# TS-NS-5: render_individual_scoped passes execution_path_ids (NS-REQ-5)
# ---------------------------------------------------------------------------


def _make_spec_with_smoke_tests() -> Spec:
    """Build a Spec with two execution paths and smoke tests referencing them.

    Execution paths: 01-PATH-1 (referenced by SMOKE-1, in scope)
                     01-PATH-2 (referenced by SMOKE-2, out of scope)
    Smoke tests: TS-01-SMOKE-1 → 01-PATH-1 (in scope via subtask refs)
                 TS-01-SMOKE-2 → 01-PATH-2 (out of scope)
    External APIs: lib-a (referenced in REQ-1 AC text), lib-b (not referenced)
    """
    req1 = Requirement(
        id="01-REQ-1",
        title="First requirement",
        user_story=UserStory(role="user", goal="do thing 1", benefit="benefit 1"),
        acceptance_criteria=[
            Criterion(
                id="01-REQ-1.1",
                ears_pattern=EARSPattern.UBIQUITOUS,
                system="the system",
                action="do thing 1 using lib-a",
            ),
        ],
    )
    req2 = Requirement(
        id="01-REQ-2",
        title="Second requirement",
        user_story=UserStory(role="admin", goal="do thing 2", benefit="benefit 2"),
        acceptance_criteria=[
            Criterion(
                id="01-REQ-2.1",
                ears_pattern=EARSPattern.UBIQUITOUS,
                system="the system",
                action="do thing 2 using lib-b",
            ),
        ],
    )
    requirements = Requirements(
        spec_id="01",
        spec_name="Test Spec",
        introduction="Intro",
        glossary={"term": "definition"},
        requirements=[req1, req2],
        execution_paths=[
            ExecutionPath(
                id="01-PATH-1",
                title="Happy path",
                steps=[PathStep(actor="user", action="clicks button")],
            ),
            ExecutionPath(
                id="01-PATH-2",
                title="Error path",
                steps=[PathStep(actor="system", action="returns error")],
            ),
        ],
        external_apis=[
            ExternalAPI(
                package="lib-a",
                version="1.0",
                symbols=[
                    ExternalAPISymbol(
                        name="func_a",
                        import_path="lib_a",
                        signature="func_a() -> None",
                    ),
                ],
            ),
            ExternalAPI(
                package="lib-b",
                version="2.0",
                symbols=[
                    ExternalAPISymbol(
                        name="func_b",
                        import_path="lib_b",
                        signature="func_b() -> None",
                    ),
                ],
            ),
        ],
    )
    test_spec = TestSpec(
        spec_id="01",
        spec_name="Test Spec",
        smoke_tests=[
            SmokeTest(
                id="TS-01-SMOKE-1",
                execution_path_id="01-PATH-1",
                description="Happy path smoke test",
            ),
            SmokeTest(
                id="TS-01-SMOKE-2",
                execution_path_id="01-PATH-2",
                description="Error path smoke test",
            ),
        ],
    )
    tasks = Tasks(
        spec_id="01",
        spec_name="Test Spec",
        task_groups=[
            TaskGroup(
                id=1,
                title="Group 1",
                subtasks=[
                    Subtask(
                        id="1.1",
                        title="Subtask 1.1",
                        requirement_refs=["01-REQ-1.1"],
                        test_spec_refs=["TS-01-SMOKE-1"],
                    ),
                ],
                verification=VerificationSubtask(id="1.V", checks=["verify"]),
            ),
            TaskGroup(
                id=2,
                title="Group 2",
                subtasks=[
                    Subtask(
                        id="2.1",
                        title="Subtask 2.1",
                        requirement_refs=["01-REQ-2.1"],
                        test_spec_refs=["TS-01-SMOKE-2"],
                    ),
                ],
                verification=VerificationSubtask(id="2.V", checks=["verify"]),
            ),
        ],
        traceability=[
            TraceabilityEntry(
                requirement_id="01-REQ-1.1",
                test_spec_id="TS-01-SMOKE-1",
                task_id="1.1",
            ),
            TraceabilityEntry(
                requirement_id="01-REQ-2.1",
                test_spec_id="TS-01-SMOKE-2",
                task_id="2.1",
            ),
        ],
    )
    return Spec(
        prd=PRDDocument(body="# PRD\n\n## Intent\n\nTest PRD."),
        requirements=requirements,
        test_spec=test_spec,
        tasks=tasks,
    )


class TestRenderIndividualScopedExecutionPaths:
    """TS-NS-5 / NS-REQ-5: render_individual_scoped computes execution_path_ids."""

    def test_in_scope_smoke_test_path_rendered(self) -> None:
        """Execution path referenced by in-scope smoke test is rendered."""
        spec = _make_spec_with_smoke_tests()
        result = render_individual_scoped(spec, target_group=1)
        md = result["requirements"]
        assert "01-PATH-1" in md
        assert "clicks button" in md

    def test_out_of_scope_smoke_test_path_omitted(self) -> None:
        """Execution path referenced only by out-of-scope smoke test is omitted."""
        spec = _make_spec_with_smoke_tests()
        result = render_individual_scoped(spec, target_group=1)
        md = result["requirements"]
        assert "01-PATH-2" not in md
        assert "returns error" not in md
        assert "execution paths defined" in md

    def test_in_scope_api_rendered_out_of_scope_omitted(self) -> None:
        """External API referenced in in-scope AC text is rendered; others omitted."""
        spec = _make_spec_with_smoke_tests()
        result = render_individual_scoped(spec, target_group=1)
        md = result["requirements"]
        assert "lib-a" in md
        assert "lib-b" not in md
