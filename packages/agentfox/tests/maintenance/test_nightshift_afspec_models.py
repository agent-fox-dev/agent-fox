"""Tests for nightshift afspec model construction and rendering.

Spec: 01_nightshift_afspec_models (task group 1)

Tests cover:
- TS-01-1 through TS-01-22: acceptance tests
- TS-01-E1 through TS-01-E4: edge case tests
- TS-01-P1 through TS-01-P4: property-based tests
- TS-01-SMOKE-1 through TS-01-SMOKE-3: smoke tests
"""

from __future__ import annotations

import inspect
from unittest.mock import patch

import pytest
from afspec.models import (
    PRDFrontmatter,
    Spec,
    SubtaskState,
)
from agentfox.maintenance.fix_pipeline import AcceptanceCriterion, TriageResult
from agentfox.maintenance.spec_builder import build_afspec_from_triage

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_criterion(
    idx: int,
    *,
    description: str | None = None,
    preconditions: str | None = None,
    expected: str | None = None,
    assertion: str | None = None,
) -> AcceptanceCriterion:
    """Build an AcceptanceCriterion with sensible defaults."""
    return AcceptanceCriterion(
        id=f"AC-{idx}",
        description=description if description is not None else f"d{idx}",
        preconditions=preconditions if preconditions is not None else f"pre{idx}",
        expected=expected if expected is not None else f"e{idx}",
        assertion=assertion if assertion is not None else f"a{idx}",
    )


def _make_triage(
    criteria: list[AcceptanceCriterion] | None = None,
    **kw,
) -> TriageResult:
    """Build a TriageResult.

    Extra keyword arguments (e.g. ``issue_body``) are forwarded to the
    TriageResult constructor.  When the implementation adds the
    ``issue_body`` field they will be accepted; until then the call will
    raise TypeError -- an acceptable failure mode for group 1 tests.
    """
    return TriageResult(
        criteria=criteria if criteria is not None else [],
        **kw,
    )


# ---------------------------------------------------------------------------
# TS-01-1: Spec with all four top-level fields populated
# ---------------------------------------------------------------------------


class TestBuildAfspecHappyPath:
    """TS-01-1, TS-01-3: basic function contract."""

    def test_ts_01_1_all_fields_populated(self):
        """TS-01-1: build_afspec_from_triage returns a Spec with all fields."""
        triage = _make_triage(
            [_make_criterion(1, description="System returns 200 on valid input")],
            issue_body="Fix the endpoint",
        )
        spec = build_afspec_from_triage(triage, 42)

        assert isinstance(spec, Spec)
        assert spec.requirements is not None
        assert spec.test_spec is not None
        assert spec.tasks is not None
        assert spec.prd is not None
        assert len(spec.requirements.requirements) == 1
        assert len(spec.test_spec.test_cases) == 1
        assert len(spec.tasks.task_groups) == 1

    def test_ts_01_3_importable_and_signature(self):
        """TS-01-3: function importable with correct signature."""
        sig = inspect.signature(build_afspec_from_triage)
        params = list(sig.parameters.keys())
        assert "triage_result" in params
        assert "issue_number" in params
        assert sig.parameters["issue_number"].annotation is int
        assert sig.return_annotation is Spec


# ---------------------------------------------------------------------------
# TS-01-4 through TS-01-8: Requirement mapping
# ---------------------------------------------------------------------------


class TestRequirementMapping:
    """TS-01-4-8: AcceptanceCriterion -> Requirement mapping."""

    def test_ts_01_4_requirement_ids(self):
        """TS-01-4: Requirement.id follows NS-REQ-{N}."""
        criteria = [_make_criterion(i) for i in range(1, 4)]
        triage = _make_triage(criteria, issue_body="body")
        spec = build_afspec_from_triage(triage, 7)

        reqs = spec.requirements.requirements
        assert reqs[0].id == "NS-REQ-1"
        assert reqs[1].id == "NS-REQ-2"
        assert reqs[2].id == "NS-REQ-3"

    def test_ts_01_5_requirement_title(self):
        """TS-01-5: Requirement.title equals criterion description."""
        triage = _make_triage(
            [
                _make_criterion(
                    1,
                    description="System handles large payloads gracefully",
                )
            ],
            issue_body="body",
        )
        spec = build_afspec_from_triage(triage, 5)
        assert spec.requirements.requirements[0].title == "System handles large payloads gracefully"

    def test_ts_01_6_requirement_user_story_verbatim(self):
        """TS-01-6: Requirement.user_story is criterion description verbatim.

        Note: afspec Requirement.user_story is a UserStory model, not a plain
        string.  The description is stored verbatim in user_story.goal.
        See docs/errata/01_user_story_model_type.md for details.
        """
        triage = _make_triage(
            [
                _make_criterion(
                    1,
                    description="User can log in with valid credentials",
                )
            ],
            issue_body="body",
        )
        spec = build_afspec_from_triage(triage, 10)
        assert spec.requirements.requirements[0].user_story.goal == "User can log in with valid credentials"

    def test_ts_01_7_acceptance_criteria_entry(self):
        """TS-01-7: acceptance_criteria has exactly one entry."""
        triage = _make_triage(
            [
                _make_criterion(
                    1,
                    description="desc",
                    preconditions="Server running, DB seeded",
                    expected="200 OK",
                )
            ],
            issue_body="body",
        )
        spec = build_afspec_from_triage(triage, 3)
        ac_list = spec.requirements.requirements[0].acceptance_criteria
        assert len(ac_list) == 1
        ac_entry = ac_list[0]
        # entry must incorporate precondition and expected content
        assert "Server running" in str(ac_entry) or "200 OK" in str(ac_entry)

    def test_ts_01_8_edge_cases_empty(self):
        """TS-01-8: Requirement.edge_cases is empty list."""
        criteria = [_make_criterion(i) for i in range(1, 3)]
        triage = _make_triage(criteria, issue_body="body")
        spec = build_afspec_from_triage(triage, 9)
        for req in spec.requirements.requirements:
            assert req.edge_cases == []


# ---------------------------------------------------------------------------
# TS-01-9 through TS-01-13: TestCase derivation
# ---------------------------------------------------------------------------


class TestTestCaseDerivation:
    """TS-01-9-13: AcceptanceCriterion -> TestCase mapping."""

    def test_ts_01_9_one_test_case_per_criterion(self):
        """TS-01-9: each criterion produces exactly one TestCase."""
        criteria = [_make_criterion(i) for i in range(1, 4)]
        triage = _make_triage(criteria, issue_body="body")
        spec = build_afspec_from_triage(triage, 11)
        assert len(spec.test_spec.test_cases) == 3

    def test_ts_01_10_test_case_ids(self):
        """TS-01-10: TestCase.id follows TS-NS-{N}."""
        criteria = [_make_criterion(i) for i in range(1, 3)]
        triage = _make_triage(criteria, issue_body="body")
        spec = build_afspec_from_triage(triage, 20)
        assert spec.test_spec.test_cases[0].id == "TS-NS-1"
        assert spec.test_spec.test_cases[1].id == "TS-NS-2"

    def test_ts_01_11_test_case_requirement_id(self):
        """TS-01-11: TestCase.requirement_id matches NS-REQ-{N}."""
        criteria = [_make_criterion(i) for i in range(1, 3)]
        triage = _make_triage(criteria, issue_body="body")
        spec = build_afspec_from_triage(triage, 21)
        assert spec.test_spec.test_cases[0].requirement_id == "NS-REQ-1"
        assert spec.test_spec.test_cases[1].requirement_id == "NS-REQ-2"

    def test_ts_01_12_test_case_field_mapping(self):
        """TS-01-12: TestCase fields mapped from AcceptanceCriterion."""
        triage = _make_triage(
            [
                _make_criterion(
                    1,
                    description="Login succeeds for valid user",
                    preconditions="User exists in DB, Service is up",
                    expected="Session token returned",
                    assertion="assert token is not None",
                )
            ],
            issue_body="body",
        )
        spec = build_afspec_from_triage(triage, 33)
        tc = spec.test_spec.test_cases[0]
        assert tc.description == "Login succeeds for valid user"
        assert tc.expected == "Session token returned"
        assert tc.assertion_pseudocode == "assert token is not None"

    def test_ts_01_13_test_case_input_and_kind(self):
        """TS-01-13: TestCase.input is '' and kind is 'acceptance'."""
        criteria = [_make_criterion(i) for i in range(1, 3)]
        triage = _make_triage(criteria, issue_body="body")
        spec = build_afspec_from_triage(triage, 15)
        for tc in spec.test_spec.test_cases:
            assert tc.input == ""
            assert tc.kind == "acceptance"


# ---------------------------------------------------------------------------
# TS-01-14, TS-01-15: TaskGroup and Subtask construction
# ---------------------------------------------------------------------------


class TestTaskGroupConstruction:
    """TS-01-14, TS-01-15: TaskGroup and Subtask construction."""

    def test_ts_01_14_single_task_group(self):
        """TS-01-14: single TaskGroup with correct id, kind, title."""
        triage = _make_triage(
            [_make_criterion(1)],
            issue_body="body",
        )
        spec = build_afspec_from_triage(triage, 99)
        groups = spec.tasks.task_groups
        assert len(groups) == 1
        g = groups[0]
        assert g.id == 1
        assert g.kind == "tests"
        assert g.title == "Fix issue #99"

    def test_ts_01_15_subtask_mapping(self):
        """TS-01-15: Subtask fields mapped from criteria."""
        triage = _make_triage(
            [
                _make_criterion(
                    1,
                    description="First fix",
                    preconditions="pre1",
                    expected="exp1",
                    assertion="assert exp1",
                ),
                _make_criterion(
                    2,
                    description="Second fix",
                    preconditions="pre2",
                    expected="exp2",
                    assertion="assert exp2",
                ),
            ],
            issue_body="body",
        )
        spec = build_afspec_from_triage(triage, 50)
        subtasks = spec.tasks.task_groups[0].subtasks

        assert subtasks[0].id == "1.1"
        assert subtasks[0].title == "First fix"
        assert "pre1" in str(subtasks[0].details)
        assert subtasks[0].state == SubtaskState.PENDING
        assert subtasks[0].test_spec_refs == ["TS-NS-1"]
        assert subtasks[0].requirement_refs == ["NS-REQ-1"]

        assert subtasks[1].id == "1.2"
        assert subtasks[1].test_spec_refs == ["TS-NS-2"]
        assert subtasks[1].requirement_refs == ["NS-REQ-2"]


# ---------------------------------------------------------------------------
# TS-01-16, TS-01-17: PRDDocument construction
# ---------------------------------------------------------------------------


class TestPRDDocumentConstruction:
    """TS-01-16, TS-01-17: PRDDocument construction."""

    def test_ts_01_16_prd_frontmatter_and_body(self):
        """TS-01-16: PRDDocument frontmatter and body fields."""
        triage = _make_triage(
            [],
            issue_body="The endpoint crashes on empty payload.",
        )
        spec = build_afspec_from_triage(triage, 42)
        assert spec.prd.frontmatter.spec_id == "fix-42"
        assert spec.prd.frontmatter.spec_name == "fix_issue_42"
        assert spec.prd.body == "The endpoint crashes on empty payload."

    def test_ts_01_17_prd_optional_fields_at_defaults(self):
        """TS-01-17: optional PRDDocument fields at Pydantic defaults."""
        triage = _make_triage([], issue_body="body")
        spec = build_afspec_from_triage(triage, 7)
        default_fm = PRDFrontmatter(spec_id="fix-7", spec_name="fix_issue_7")
        assert spec.prd.frontmatter.status == default_fm.status
        assert spec.prd.frontmatter.created_at == default_fm.created_at


# ---------------------------------------------------------------------------
# TS-01-18, TS-01-19, TS-01-20: render_inmemory_spec_sections
# ---------------------------------------------------------------------------


class TestRenderInmemorySpecSections:
    """TS-01-18-20: render_inmemory_spec_sections and _render_spec_sections."""

    def _build_spec(self) -> Spec:
        """Build a minimal Spec for render tests."""
        triage = _make_triage(
            [_make_criterion(1)],
            issue_body="body",
        )
        return build_afspec_from_triage(triage, 1)

    def test_ts_01_18_returns_list_of_strings(self):
        """TS-01-18: render_inmemory_spec_sections returns list[str]."""
        from agentfox.session.context import render_inmemory_spec_sections

        spec = self._build_spec()
        result = render_inmemory_spec_sections(spec)
        assert isinstance(result, list)
        assert len(result) > 0
        for section in result:
            assert isinstance(section, str)
            assert len(section) > 0

    def test_ts_01_19_no_file_io(self):
        """TS-01-19: no file I/O during render_inmemory_spec_sections."""
        from agentfox.session.context import render_inmemory_spec_sections

        spec = self._build_spec()
        with patch("builtins.open") as mock_open:
            result = render_inmemory_spec_sections(spec)
            assert mock_open.call_count == 0
        assert isinstance(result, list)

    def test_ts_01_20_render_spec_sections_delegates(self):
        """TS-01-20: _render_spec_sections delegates to render_inmemory."""
        from pathlib import Path

        from agentfox.session.context import (
            _render_spec_sections,
            render_inmemory_spec_sections,
        )

        # Track the actual return value since wraps doesn't set return_value
        captured = {}

        def tracking_wrapper(spec, **kwargs):
            result = render_inmemory_spec_sections(spec, **kwargs)
            captured["result"] = result
            return result

        with patch(
            "agentfox.session.context.render_inmemory_spec_sections",
            side_effect=tracking_wrapper,
        ) as mock_render:
            # Use any spec dir that exists; will fail if fixture is missing
            spec_path = Path(__file__).parent / "fixtures" / "specs" / "01_test"
            result = _render_spec_sections(spec_path)
            assert mock_render.call_count == 1
            called_spec = mock_render.call_args[0][0]
            assert isinstance(called_spec, Spec)
            assert result == captured["result"]


# ---------------------------------------------------------------------------
# TS-01-21, TS-01-22: ID cross-reference consistency
# ---------------------------------------------------------------------------


class TestIDCrossReferenceConsistency:
    """TS-01-21, TS-01-22, TS-01-2: ID consistency and no file I/O."""

    def test_ts_01_21_all_ids_consistent(self):
        """TS-01-21: Requirement, TestCase, Subtask IDs all consistent."""
        criteria = [_make_criterion(i) for i in range(1, 4)]
        triage = _make_triage(criteria, issue_body="body")
        spec = build_afspec_from_triage(triage, 5)

        for n, criterion in enumerate(criteria, start=1):
            req = spec.requirements.requirements[n - 1]
            tc = spec.test_spec.test_cases[n - 1]
            sub = spec.tasks.task_groups[0].subtasks[n - 1]
            assert req.id == f"NS-REQ-{n}"
            assert tc.id == f"TS-NS-{n}"
            assert sub.id == f"1.{n}"
            assert req.title == criterion.description
            assert tc.description == criterion.description
            assert sub.title == criterion.description

    def test_ts_01_22_subtask_refs(self):
        """TS-01-22: Subtask test_spec_refs and requirement_refs."""
        criteria = [_make_criterion(i) for i in range(1, 4)]
        triage = _make_triage(criteria, issue_body="body")
        spec = build_afspec_from_triage(triage, 6)

        subtasks = spec.tasks.task_groups[0].subtasks
        for n, sub in enumerate(subtasks, start=1):
            assert sub.test_spec_refs == [f"TS-NS-{n}"]
            assert sub.requirement_refs == [f"NS-REQ-{n}"]

    def test_ts_01_2_no_file_io(self):
        """TS-01-2: no file I/O during build_afspec_from_triage."""
        triage = _make_triage(
            [_make_criterion(1)],
            issue_body="body",
        )
        with (
            patch("builtins.open") as mock_open,
            patch("afspec.load_spec") as mock_load,
            patch("afspec.save") as mock_save,
        ):
            spec = build_afspec_from_triage(triage, 1)
            assert mock_open.call_count == 0
            assert mock_load.call_count == 0
            assert mock_save.call_count == 0
            assert spec is not None


# ---------------------------------------------------------------------------
# TS-01-E1 through TS-01-E4: Edge cases
# ---------------------------------------------------------------------------


class TestEdgeCases:
    """TS-01-E1-E4: edge case tests."""

    def test_ts_01_e1_empty_criteria(self):
        """TS-01-E1: empty criteria produces fallback Spec."""
        triage = _make_triage(
            [],
            issue_body="This issue has no structured criteria.",
        )
        spec = build_afspec_from_triage(triage, 88)
        assert spec.requirements.requirements == []
        assert spec.test_spec.test_cases == []
        groups = spec.tasks.task_groups
        assert len(groups) == 1
        subtasks = groups[0].subtasks
        assert len(subtasks) == 1
        assert subtasks[0].title == "Fix the issue"
        assert subtasks[0].state == SubtaskState.PENDING

    def test_ts_01_e2_none_and_empty_fields(self):
        """TS-01-E2: None/empty fields substituted with empty strings."""
        criterion = AcceptanceCriterion(
            id="AC-1",
            description=None,  # type: ignore[arg-type]
            preconditions=None,  # type: ignore[arg-type]
            expected="",
            assertion=None,  # type: ignore[arg-type]
        )
        triage = _make_triage([criterion], issue_body="body")
        # Must not raise
        spec = build_afspec_from_triage(triage, 13)
        assert isinstance(spec, Spec)
        req = spec.requirements.requirements[0]
        assert req.title == ""
        tc = spec.test_spec.test_cases[0]
        assert tc.description == ""
        assert tc.assertion_pseudocode == ""
        assert tc.expected == ""

    def test_ts_01_e3_fallback_subtask_details(self):
        """TS-01-E3: fallback Subtask has empty details and refs."""
        triage = _make_triage([], issue_body="body")
        spec = build_afspec_from_triage(triage, 77)
        sub = spec.tasks.task_groups[0].subtasks[0]
        assert sub.title == "Fix the issue"
        assert not sub.details  # empty list or falsy
        assert sub.state == SubtaskState.PENDING
        assert not sub.test_spec_refs  # empty list or falsy
        assert not sub.requirement_refs  # empty list or falsy

    def test_ts_01_e4_render_propagates_exception(self):
        """TS-01-E4: render_inmemory_spec_sections propagates exceptions."""
        from agentfox.session.context import render_inmemory_spec_sections

        spec = Spec()
        with patch(
            "afspec.render_individual",
            side_effect=RuntimeError("render failed"),
        ):
            with pytest.raises(RuntimeError, match="render failed"):
                render_inmemory_spec_sections(spec)


# ---------------------------------------------------------------------------
# TS-01-P1 through TS-01-P4: Property-based tests
# ---------------------------------------------------------------------------


class TestPropertyBased:
    """TS-01-P1-P4: property-based tests using parametrize."""

    @pytest.mark.parametrize("n", [1, 2, 5, 10, 20, 50])
    def test_ts_01_p1_count_invariant(self, n: int):
        """TS-01-P1: N criteria -> N Requirements, N TestCases, N Subtasks."""
        criteria = [_make_criterion(i) for i in range(1, n + 1)]
        triage = _make_triage(criteria, issue_body="body")
        spec = build_afspec_from_triage(triage, 1)
        assert len(spec.requirements.requirements) == n
        assert len(spec.test_spec.test_cases) == n
        assert len(spec.tasks.task_groups[0].subtasks) == n

    @pytest.mark.parametrize("n", [1, 3, 10, 30])
    def test_ts_01_p2_cross_reference_consistency(self, n: int):
        """TS-01-P2: all cross-reference IDs are consistent."""
        criteria = [_make_criterion(i) for i in range(1, n + 1)]
        triage = _make_triage(criteria, issue_body="body")
        spec = build_afspec_from_triage(triage, 1)
        for i in range(1, n + 1):
            assert spec.requirements.requirements[i - 1].id == f"NS-REQ-{i}"
            assert spec.test_spec.test_cases[i - 1].id == f"TS-NS-{i}"
            tc = spec.test_spec.test_cases[i - 1]
            assert tc.requirement_id == f"NS-REQ-{i}"
            sub = spec.tasks.task_groups[0].subtasks[i - 1]
            assert sub.id == f"1.{i}"
            assert sub.test_spec_refs == [f"TS-NS-{i}"]
            assert sub.requirement_refs == [f"NS-REQ-{i}"]

    @pytest.mark.parametrize("n", [0, 1, 5, 20])
    def test_ts_01_p3_no_file_io(self, n: int):
        """TS-01-P3: no file I/O in build or render."""
        criteria = [_make_criterion(i) for i in range(1, n + 1)]
        triage = _make_triage(criteria, issue_body="body")
        with (
            patch("builtins.open") as mock_open,
            patch("afspec.load_spec") as mock_load,
            patch("afspec.save") as mock_save,
        ):
            spec = build_afspec_from_triage(triage, 1)

            from agentfox.session.context import render_inmemory_spec_sections

            render_inmemory_spec_sections(spec)
            assert mock_open.call_count == 0
            assert mock_load.call_count == 0
            assert mock_save.call_count == 0

    def test_ts_01_p4_render_output_equivalence(self):
        """TS-01-P4: render_inmemory output == _render_spec_sections output."""
        from pathlib import Path

        import afspec
        from agentfox.session.context import render_inmemory_spec_sections

        # Load spec from a fixture on-disk spec directory
        spec_path = Path(__file__).parent / "fixtures" / "specs" / "01_test"
        spec = afspec.load_spec(spec_path)

        direct_output = render_inmemory_spec_sections(spec)

        from agentfox.session.context import _render_spec_sections

        disk_output = _render_spec_sections(spec_path)
        assert direct_output == disk_output


# ---------------------------------------------------------------------------
# TS-01-SMOKE-1 through TS-01-SMOKE-3: Smoke tests
# ---------------------------------------------------------------------------


@pytest.mark.smoke
class TestSmokeTests:
    """TS-01-SMOKE-1-3: end-to-end smoke tests."""

    def test_ts_01_smoke_1_happy_path(self):
        """TS-01-SMOKE-1: triage -> Spec -> rendered markdown sections."""
        triage = _make_triage(
            [_make_criterion(1), _make_criterion(2)],
            issue_body="Fix the endpoint",
        )
        spec = build_afspec_from_triage(triage, 42)
        assert len(spec.requirements.requirements) == 2
        assert len(spec.test_spec.test_cases) == 2
        assert len(spec.tasks.task_groups[0].subtasks) == 2

        from agentfox.session.context import render_inmemory_spec_sections

        result = render_inmemory_spec_sections(spec)
        assert isinstance(result, list)
        assert len(result) > 0
        for section in result:
            assert isinstance(section, str)
            assert len(section) > 0

    def test_ts_01_smoke_2_empty_criteria_fallback(self):
        """TS-01-SMOKE-2: empty criteria -> fallback Spec renders OK."""
        triage = _make_triage([], issue_body="No criteria")
        spec = build_afspec_from_triage(triage, 88)
        assert spec.requirements.requirements == []
        assert spec.test_spec.test_cases == []
        assert len(spec.tasks.task_groups[0].subtasks) == 1
        assert spec.tasks.task_groups[0].subtasks[0].title == "Fix the issue"

        from agentfox.session.context import render_inmemory_spec_sections

        result = render_inmemory_spec_sections(spec)
        assert isinstance(result, list)

    def test_ts_01_smoke_3_disk_rendering_delegation(self):
        """TS-01-SMOKE-3: _render_spec_sections delegates to render_inmemory."""
        from pathlib import Path

        from agentfox.session.context import (
            _render_spec_sections,
            render_inmemory_spec_sections,
        )

        with patch(
            "agentfox.session.context.render_inmemory_spec_sections",
            wraps=render_inmemory_spec_sections,
        ) as mock_render:
            spec_path = Path(__file__).parent / "fixtures" / "specs" / "01_test"
            result = _render_spec_sections(spec_path)
            assert mock_render.call_count == 1
            assert isinstance(result, list)
