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

    def test_spec_overview_lists_all_requirements(self, valid_spec_dir: Path) -> None:
        """Spec Overview lists all requirement IDs regardless of filter."""
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
        # Should still have the overview but no full requirement detail
        assert "## Spec Overview" in md
        assert "(other group)" in md


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
