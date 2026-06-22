"""Tests for nightshift prompt alignment (spec 02).

Validates that _build_coder_prompt() and _build_reviewer_prompt() use
afspec-rendered context (## Requirements, ## Test Specification, ## Tasks)
instead of ad-hoc flat markdown criteria, and that fallback / triage-comment
/ verdict parsing behaviour is preserved.

Test Spec: TS-02-1 through TS-02-11, TS-02-E1, TS-02-E2,
           TS-02-P1 through TS-02-P5, TS-02-SMOKE-1 through TS-02-SMOKE-5
Requirements: 02-REQ-1, 02-REQ-2, 02-REQ-3, 02-REQ-4, 02-REQ-5
"""

from __future__ import annotations

import ast
import json
import logging
import re
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest
from agentfox.nightshift.fix_pipeline import (
    AcceptanceCriterion,
    FixPipeline,
    FixReviewResult,
    TriageResult,
)
from agentfox.nightshift.spec_builder import InMemorySpec

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

RENDERED_SECTIONS = (
    "## Requirements\nRequirement content\n\n## Test Specification\nTest spec content\n\n## Tasks\n- [ ] Task 1\n"
)

SUBTASK_PHRASE = "Refer to the tasks subtask list in the context above"

EMPTY_TRIAGE_FALLBACK = (
    "No acceptance criteria were produced by triage. Verify the fix based on the issue description above."
)


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture()
def issue_body() -> str:
    return "Issue body text describing the bug"


@pytest.fixture()
def fake_spec(issue_body: str) -> InMemorySpec:
    return InMemorySpec(
        issue_number=42,
        title="Fix the bug",
        task_prompt=f"Fix the issue: Fix the bug\n\nIssue #42\n\n{issue_body}",
        system_context=issue_body,
        branch_name="fix/42-fix-the-bug",
    )


def _make_criteria(count: int = 2) -> list[AcceptanceCriterion]:
    return [
        AcceptanceCriterion(
            id=f"AC-{i + 1}",
            description=f"Criterion {i + 1} description",
            preconditions=f"Precondition {i + 1}",
            expected=f"Expected {i + 1}",
            assertion=f"Assertion {i + 1}",
        )
        for i in range(count)
    ]


@pytest.fixture()
def valid_triage() -> TriageResult:
    return TriageResult(
        summary="Root cause analysis",
        affected_files=["agentfox/engine.py"],
        criteria=_make_criteria(2),
    )


@pytest.fixture()
def empty_triage() -> TriageResult:
    return TriageResult()


@pytest.fixture()
def pipeline() -> FixPipeline:
    config = MagicMock()
    platform = MagicMock()
    return FixPipeline(config=config, platform=platform)


# ---------------------------------------------------------------------------
# TS-02-1: _build_coder_prompt() uses afspec-rendered context
# Requirement: 02-REQ-1.1
# ---------------------------------------------------------------------------


class TestBuildCoderPromptAfspec:
    """Unit tests for _build_coder_prompt() afspec integration."""

    def test_ts_02_1_coder_uses_afspec_rendered_context(
        self,
        pipeline: FixPipeline,
        fake_spec: InMemorySpec,
        valid_triage: TriageResult,
        issue_body: str,
    ) -> None:
        """_build_coder_prompt calls build_afspec_from_triage + render_inmemory_spec_sections.

        Requirement: 02-REQ-1.1
        """
        fake_afspec = MagicMock(name="fake_afspec")

        with (
            patch(
                "agentfox.nightshift.fix_pipeline.build_afspec_from_triage",
                return_value=fake_afspec,
                create=True,
            ) as mock_build,
            patch(
                "agentfox.nightshift.fix_pipeline.render_inmemory_spec_sections",
                return_value=RENDERED_SECTIONS,
                create=True,
            ) as mock_render,
            patch(
                "agentfox.session.prompt.build_system_prompt",
                return_value="mocked-system-prompt",
            ) as mock_bsp,
        ):
            system_prompt, _task_prompt = pipeline._build_coder_prompt(fake_spec, valid_triage)

        # Assert afspec pipeline was invoked
        mock_build.assert_called_once()
        mock_render.assert_called_once_with(fake_afspec)

        # Assert context passed to build_system_prompt has afspec sections
        captured_context = mock_bsp.call_args.kwargs["context"]
        assert captured_context.startswith(issue_body)
        assert "## Requirements" in captured_context
        assert "## Test Specification" in captured_context
        assert "## Tasks" in captured_context
        assert captured_context.index(issue_body) < captured_context.index("## Requirements")

        assert isinstance(system_prompt, str) and len(system_prompt) > 0

    # -----------------------------------------------------------------------
    # TS-02-2: Task prompt contains subtask list phrase
    # Requirement: 02-REQ-1.2
    # -----------------------------------------------------------------------

    def test_ts_02_2_coder_task_prompt_contains_subtask_reference(
        self,
        pipeline: FixPipeline,
        fake_spec: InMemorySpec,
        valid_triage: TriageResult,
    ) -> None:
        """Task prompt contains the subtask list reference phrase.

        Requirement: 02-REQ-1.2
        """
        with (
            patch(
                "agentfox.nightshift.fix_pipeline.build_afspec_from_triage",
                return_value=MagicMock(),
                create=True,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.render_inmemory_spec_sections",
                return_value=RENDERED_SECTIONS,
                create=True,
            ),
            patch(
                "agentfox.session.prompt.build_system_prompt",
                return_value="mock",
            ),
        ):
            _, task_prompt = pipeline._build_coder_prompt(fake_spec, valid_triage)

        assert SUBTASK_PHRASE in task_prompt

    # -----------------------------------------------------------------------
    # TS-02-3: review_feedback appended to task prompt
    # Requirement: 02-REQ-1.3
    # -----------------------------------------------------------------------

    def test_ts_02_3_review_feedback_appended_to_task_prompt(
        self,
        pipeline: FixPipeline,
        fake_spec: InMemorySpec,
        valid_triage: TriageResult,
    ) -> None:
        """review_feedback text appears after base task instructions.

        Requirement: 02-REQ-1.3
        """
        review_feedback = "Please fix the null pointer issue in line 42"

        with (
            patch(
                "agentfox.nightshift.fix_pipeline.build_afspec_from_triage",
                return_value=MagicMock(),
                create=True,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.render_inmemory_spec_sections",
                return_value=RENDERED_SECTIONS,
                create=True,
            ),
            patch(
                "agentfox.session.prompt.build_system_prompt",
                return_value="mock",
            ),
        ):
            _, task_prompt = pipeline._build_coder_prompt(
                fake_spec,
                valid_triage,
                review_feedback=review_feedback,
            )

        assert review_feedback in task_prompt
        base_end = task_prompt.index(SUBTASK_PHRASE) + len(SUBTASK_PHRASE)
        feedback_position = task_prompt.index(review_feedback)
        assert feedback_position > base_end

    # -----------------------------------------------------------------------
    # TS-02-4: prior_context prepended to task prompt
    # Requirement: 02-REQ-1.4
    # -----------------------------------------------------------------------

    def test_ts_02_4_prior_context_prepended_to_task_prompt(
        self,
        pipeline: FixPipeline,
        fake_spec: InMemorySpec,
        valid_triage: TriageResult,
    ) -> None:
        """prior_context text appears before base task instructions.

        Requirement: 02-REQ-1.4
        """
        prior_context = "Previous attempt context: attempted fix in commit abc123"

        with (
            patch(
                "agentfox.nightshift.fix_pipeline.build_afspec_from_triage",
                return_value=MagicMock(),
                create=True,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.render_inmemory_spec_sections",
                return_value=RENDERED_SECTIONS,
                create=True,
            ),
            patch(
                "agentfox.session.prompt.build_system_prompt",
                return_value="mock",
            ),
        ):
            _, task_prompt = pipeline._build_coder_prompt(
                fake_spec,
                valid_triage,
                prior_context=prior_context,
            )

        assert prior_context in task_prompt
        prior_position = task_prompt.index(prior_context)
        base_instruction_position = task_prompt.index(SUBTASK_PHRASE)
        assert prior_position < base_instruction_position


# ---------------------------------------------------------------------------
# TS-02-5, TS-02-6, TS-02-7: _build_reviewer_prompt()
# Requirements: 02-REQ-2.1, 02-REQ-2.2, 02-REQ-2.3
# ---------------------------------------------------------------------------


class TestBuildReviewerPromptAfspec:
    """Unit tests for _build_reviewer_prompt() afspec integration."""

    def test_ts_02_5_reviewer_uses_afspec_rendered_context(
        self,
        pipeline: FixPipeline,
        fake_spec: InMemorySpec,
        valid_triage: TriageResult,
        issue_body: str,
    ) -> None:
        """_build_reviewer_prompt uses build_afspec_from_triage pipeline.

        Requirement: 02-REQ-2.1
        """
        fake_afspec = MagicMock(name="fake_afspec")

        with (
            patch(
                "agentfox.nightshift.fix_pipeline.build_afspec_from_triage",
                return_value=fake_afspec,
                create=True,
            ) as mock_build,
            patch(
                "agentfox.nightshift.fix_pipeline.render_inmemory_spec_sections",
                return_value=RENDERED_SECTIONS,
                create=True,
            ) as mock_render,
            patch(
                "agentfox.session.prompt.build_system_prompt",
                return_value="mocked-system-prompt",
            ) as mock_bsp,
        ):
            system_prompt, _task_prompt = pipeline._build_reviewer_prompt(fake_spec, valid_triage)

        mock_build.assert_called_once()
        mock_render.assert_called_once_with(fake_afspec)

        captured_context = mock_bsp.call_args.kwargs["context"]
        assert captured_context.startswith(issue_body)
        assert "## Requirements" in captured_context
        assert captured_context.index(issue_body) < captured_context.index("## Requirements")

        assert isinstance(system_prompt, str) and len(system_prompt) > 0

    def test_ts_02_6_reviewer_task_prompt_instructs_verification(
        self,
        pipeline: FixPipeline,
        fake_spec: InMemorySpec,
        valid_triage: TriageResult,
    ) -> None:
        """Reviewer task prompt instructs to verify each acceptance criterion.

        Requirement: 02-REQ-2.2
        """
        with patch("agentfox.session.prompt.build_system_prompt", return_value="mock"):
            _, task_prompt = pipeline._build_reviewer_prompt(fake_spec, valid_triage)

        assert "verify" in task_prompt.lower()

    def test_ts_02_7_empty_triage_fallback_in_task_prompt(
        self,
        pipeline: FixPipeline,
        fake_spec: InMemorySpec,
        empty_triage: TriageResult,
    ) -> None:
        """Empty triage produces fallback message in task_prompt.

        Requirement: 02-REQ-2.3
        """
        with patch("agentfox.session.prompt.build_system_prompt", return_value="mock"):
            _, task_prompt = pipeline._build_reviewer_prompt(fake_spec, empty_triage)

        assert EMPTY_TRIAGE_FALLBACK in task_prompt


# ---------------------------------------------------------------------------
# TS-02-E1, TS-02-E2: Fallback on build_afspec_from_triage() failure
# Requirements: 02-REQ-1.E1, 02-REQ-2.E1
# ---------------------------------------------------------------------------


class TestFallbackOnAfspecFailure:
    """Edge case tests for afspec construction failure fallback."""

    def test_ts_02_e1_coder_fallback_on_afspec_failure(
        self,
        pipeline: FixPipeline,
        fake_spec: InMemorySpec,
        valid_triage: TriageResult,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """Coder prompt falls back to _render_criteria_context on failure.

        Requirement: 02-REQ-1.E1
        """
        with (
            patch(
                "agentfox.nightshift.fix_pipeline.build_afspec_from_triage",
                side_effect=ValueError("malformed triage"),
                create=True,
            ),
            patch.object(
                FixPipeline,
                "_render_criteria_context",
                wraps=pipeline._render_criteria_context,
            ) as mock_fallback,
            patch(
                "agentfox.session.prompt.build_system_prompt",
                return_value="mocked-system-prompt",
            ) as mock_bsp,
            caplog.at_level(logging.WARNING),
        ):
            # Should NOT raise
            system_prompt, task_prompt = pipeline._build_coder_prompt(fake_spec, valid_triage)

        mock_fallback.assert_called_once()
        assert any(logging.WARNING == r.levelno for r in caplog.records)
        assert isinstance(system_prompt, str) and len(system_prompt) > 0

        captured_context = mock_bsp.call_args.kwargs["context"]
        assert "## Acceptance Criteria from Triage" in captured_context

    def test_ts_02_e2_reviewer_fallback_on_afspec_failure(
        self,
        pipeline: FixPipeline,
        fake_spec: InMemorySpec,
        valid_triage: TriageResult,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """Reviewer prompt falls back to _render_criteria_context on failure.

        Requirement: 02-REQ-2.E1
        """
        with (
            patch(
                "agentfox.nightshift.fix_pipeline.build_afspec_from_triage",
                side_effect=ValueError("malformed triage"),
                create=True,
            ),
            patch.object(
                FixPipeline,
                "_render_criteria_context",
                wraps=pipeline._render_criteria_context,
            ) as mock_fallback,
            patch(
                "agentfox.session.prompt.build_system_prompt",
                return_value="mocked-system-prompt",
            ),
            caplog.at_level(logging.WARNING),
        ):
            system_prompt, task_prompt = pipeline._build_reviewer_prompt(fake_spec, valid_triage)

        mock_fallback.assert_called_once()
        assert any(logging.WARNING == r.levelno for r in caplog.records)
        assert isinstance(system_prompt, str) and len(system_prompt) > 0


# ---------------------------------------------------------------------------
# TS-02-8, TS-02-9: Static analysis of fix_pipeline.py
# Requirements: 02-REQ-3.1, 02-REQ-3.2
# ---------------------------------------------------------------------------


class TestStaticAnalysis:
    """Static inspection of fix_pipeline.py source code."""

    @pytest.mark.xfail(reason="Pending spec 02: no fallback comment, called on happy path")
    def test_ts_02_8_render_criteria_context_is_fallback_only(self) -> None:
        """_render_criteria_context has fallback comment, only in except blocks.

        Requirement: 02-REQ-3.1
        """
        import agentfox.nightshift.fix_pipeline as fp_module

        source = Path(fp_module.__file__).read_text()

        # 1. Function is defined
        assert "def _render_criteria_context" in source

        # 2. Fallback comment near the definition
        lines = source.splitlines()
        found_fallback_comment = False
        for i, line in enumerate(lines):
            if "def _render_criteria_context" in line:
                nearby = "\n".join(lines[max(0, i - 5) : i + 10])
                if "fallback" in nearby.lower():
                    found_fallback_comment = True
        assert found_fallback_comment, "_render_criteria_context should have a 'fallback' comment"

        # 3. All call sites are inside except blocks
        tree = ast.parse(source)

        class _ExceptCallChecker(ast.NodeVisitor):
            def __init__(self) -> None:
                self.in_except = False
                self.violations: list[int] = []

            def visit_ExceptHandler(self, node: ast.ExceptHandler) -> None:
                old = self.in_except
                self.in_except = True
                self.generic_visit(node)
                self.in_except = old

            def visit_Call(self, node: ast.Call) -> None:
                func = node.func
                if isinstance(func, ast.Attribute) and func.attr == "_render_criteria_context":
                    if not self.in_except:
                        self.violations.append(node.lineno)
                self.generic_visit(node)

        checker = _ExceptCallChecker()
        checker.visit(tree)
        assert not checker.violations, (
            f"_render_criteria_context called outside except blocks at lines: {checker.violations}"
        )

    def test_ts_02_9_render_criteria_section_retained_for_triage_comment(
        self,
    ) -> None:
        """_render_criteria_section defined and called from _format_triage_comment.

        Requirement: 02-REQ-3.2
        """
        import agentfox.nightshift.fix_pipeline as fp_module

        source = Path(fp_module.__file__).read_text()

        # Function is defined
        assert "def _render_criteria_section" in source

        # Called from _format_triage_comment
        tree = ast.parse(source)
        found = False
        for node in ast.walk(tree):
            if isinstance(node, ast.FunctionDef) and node.name == "_format_triage_comment":
                body_source = ast.get_source_segment(source, node)
                if body_source and "_render_criteria_section" in body_source:
                    found = True
        assert found, "_format_triage_comment should call _render_criteria_section"


# ---------------------------------------------------------------------------
# TS-02-10: _format_triage_comment format
# Requirement: 02-REQ-4.1
# ---------------------------------------------------------------------------


class TestTriageCommentFormat:
    """Verify _format_triage_comment uses bold criteria, not afspec."""

    def test_ts_02_10_format_triage_comment_uses_bold_criteria(
        self,
        pipeline: FixPipeline,
        valid_triage: TriageResult,
    ) -> None:
        """_format_triage_comment calls _render_criteria_section(bold=True).

        Requirement: 02-REQ-4.1
        """
        with (
            patch.object(
                FixPipeline,
                "_render_criteria_section",
                wraps=FixPipeline._render_criteria_section,
            ) as mock_render_section,
            patch(
                "agentfox.nightshift.fix_pipeline.render_inmemory_spec_sections",
                create=True,
            ) as mock_afspec_render,
        ):
            comment = pipeline._format_triage_comment(valid_triage)

        mock_render_section.assert_called_once()
        assert mock_render_section.call_args.kwargs.get("bold") is True
        mock_afspec_render.assert_not_called()
        assert isinstance(comment, str) and len(comment) > 0


# ---------------------------------------------------------------------------
# TS-02-11: Verdict parsing unchanged
# Requirement: 02-REQ-5.1
# ---------------------------------------------------------------------------


class TestVerdictParsing:
    """Verify parse_fix_review_output parses JSON verdict reports."""

    def test_ts_02_11_parse_fix_review_output_with_afspec_criterion_id(
        self,
    ) -> None:
        """parse_fix_review_output parses verdict with afspec criterion_id.

        Requirement: 02-REQ-5.1
        """
        from agentfox.session.review_parser import parse_fix_review_output

        verdict_json = json.dumps(
            {
                "verdicts": [
                    {
                        "criterion_id": "NS-REQ-1",
                        "verdict": "PASS",
                        "evidence": "The fix correctly handles the null case.",
                    }
                ],
                "overall_verdict": "PASS",
                "summary": "All good",
            }
        )

        result = parse_fix_review_output(verdict_json, "fix-issue-42", "fix-issue-42:0:reviewer")

        assert isinstance(result, FixReviewResult)
        assert len(result.verdicts) == 1
        assert result.verdicts[0].criterion_id == "NS-REQ-1"
        assert result.verdicts[0].verdict == "PASS"
        assert result.verdicts[0].evidence == ("The fix correctly handles the null case.")


# ---------------------------------------------------------------------------
# TS-02-P1: Property - _render_criteria_context not called on happy path
# Requirement: 02-PROP-1
# ---------------------------------------------------------------------------


class TestPropertyAfspec:
    """Property tests for afspec rendering integration."""

    @pytest.mark.parametrize("criteria_count", [1, 3, 5, 10, 20])
    def test_ts_02_p1_render_criteria_context_not_called_on_happy_path(
        self,
        pipeline: FixPipeline,
        fake_spec: InMemorySpec,
        criteria_count: int,
    ) -> None:
        """build_afspec_from_triage success -> _render_criteria_context not called.

        Property: 02-PROP-1
        Validates: 02-REQ-1.1, 02-REQ-2.1
        """
        triage = TriageResult(
            summary="Analysis",
            criteria=_make_criteria(criteria_count),
        )

        with (
            patch(
                "agentfox.nightshift.fix_pipeline.build_afspec_from_triage",
                return_value=MagicMock(),
                create=True,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.render_inmemory_spec_sections",
                return_value=RENDERED_SECTIONS,
                create=True,
            ),
            patch(
                "agentfox.session.prompt.build_system_prompt",
                return_value="mock",
            ),
            patch.object(
                FixPipeline,
                "_render_criteria_context",
                wraps=pipeline._render_criteria_context,
            ) as mock_fallback,
        ):
            pipeline._build_coder_prompt(fake_spec, triage)
            assert mock_fallback.call_count == 0, "_render_criteria_context should not be called (coder)"

            mock_fallback.reset_mock()
            pipeline._build_reviewer_prompt(fake_spec, triage)
            assert mock_fallback.call_count == 0, "_render_criteria_context should not be called (reviewer)"

    # -----------------------------------------------------------------------
    # TS-02-P2: Issue body precedes sections in context
    # Property: 02-PROP-2
    # -----------------------------------------------------------------------

    @pytest.mark.parametrize(
        ("body", "use_fallback"),
        [
            ("Simple issue body", False),
            (
                "A more detailed body with\nmultiple lines\nand ## markdown headers",
                False,
            ),
            ("Body with special chars: <>&\"'", False),
            ("Fallback path issue body", True),
        ],
    )
    def test_ts_02_p2_issue_body_precedes_sections(
        self,
        pipeline: FixPipeline,
        body: str,
        use_fallback: bool,
    ) -> None:
        """Issue body always appears before the first spec section header.

        Uses a regex to find known spec section headers (## Requirements,
        ## Test Specification, ## Tasks, ## Acceptance Criteria) so that
        bodies containing generic '##' markdown don't produce false positives.

        Also includes cases where build_afspec_from_triage raises (fallback
        path) to verify the invariant holds regardless of which path is taken.

        Property: 02-PROP-2
        Validates: 02-REQ-1.1, 02-REQ-2.1
        """
        # Regex matching known spec section headers produced by afspec rendering
        # or the _render_criteria_context fallback.
        _SECTION_RE = re.compile(
            r"^## (?:Requirements|Test Specification|Tasks|Acceptance Criteria)",
            re.MULTILINE,
        )

        spec = InMemorySpec(
            issue_number=42,
            title="Test",
            task_prompt="Fix it",
            system_context=body,
            branch_name="fix/42-test",
        )
        triage = TriageResult(summary="Analysis", criteria=_make_criteria(2))

        afspec_kwargs: dict = (
            {"side_effect": ValueError("malformed triage")} if use_fallback else {"return_value": MagicMock()}
        )

        with (
            patch(
                "agentfox.nightshift.fix_pipeline.build_afspec_from_triage",
                create=True,
                **afspec_kwargs,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.render_inmemory_spec_sections",
                return_value=RENDERED_SECTIONS,
                create=True,
            ),
            patch(
                "agentfox.session.prompt.build_system_prompt",
                return_value="mock",
            ) as mock_bsp,
        ):
            pipeline._build_coder_prompt(spec, triage)
            ctx = mock_bsp.call_args.kwargs["context"]
            section_match = _SECTION_RE.search(ctx)
            assert section_match is not None, "Expected a spec section header in context"
            assert ctx.index(body) < section_match.start(), "Issue body must precede first spec section header (coder)"

            mock_bsp.reset_mock()
            pipeline._build_reviewer_prompt(spec, triage)
            ctx = mock_bsp.call_args.kwargs["context"]
            section_match = _SECTION_RE.search(ctx)
            assert section_match is not None, "Expected a spec section header in context"
            assert ctx.index(body) < section_match.start(), (
                "Issue body must precede first spec section header (reviewer)"
            )

    # -----------------------------------------------------------------------
    # TS-02-P3: prior_context < review_feedback in task prompt
    # Property: 02-PROP-3
    # -----------------------------------------------------------------------

    @pytest.mark.parametrize(
        ("prior", "feedback", "use_fallback"),
        [
            ("Prior attempt 1", "Feedback: fix the NPE", False),
            ("Context from commit abc", "Review: coverage insufficient", False),
            ("Long prior " * 20, "Short feedback", False),
            ("Fallback prior context", "Fallback feedback text", True),
        ],
    )
    def test_ts_02_p3_prior_context_before_review_feedback(
        self,
        pipeline: FixPipeline,
        fake_spec: InMemorySpec,
        valid_triage: TriageResult,
        prior: str,
        feedback: str,
        use_fallback: bool,
    ) -> None:
        """prior_context always precedes review_feedback in task prompt.

        Includes both afspec-happy and fallback paths (malformed triage) to
        verify the ordering invariant holds regardless of context-rendering
        path.

        Property: 02-PROP-3
        Validates: 02-REQ-1.3, 02-REQ-1.4
        """
        afspec_kwargs: dict = (
            {"side_effect": ValueError("malformed triage")} if use_fallback else {"return_value": MagicMock()}
        )

        with (
            patch(
                "agentfox.nightshift.fix_pipeline.build_afspec_from_triage",
                create=True,
                **afspec_kwargs,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.render_inmemory_spec_sections",
                return_value=RENDERED_SECTIONS,
                create=True,
            ),
            patch(
                "agentfox.session.prompt.build_system_prompt",
                return_value="mock",
            ),
        ):
            _, task_prompt = pipeline._build_coder_prompt(
                fake_spec,
                valid_triage,
                review_feedback=feedback,
                prior_context=prior,
            )

        assert prior in task_prompt
        assert feedback in task_prompt
        assert task_prompt.index(prior) < task_prompt.index(feedback)


# ---------------------------------------------------------------------------
# TS-02-P4: _format_triage_comment always uses bold=True
# Property: 02-PROP-4
# ---------------------------------------------------------------------------


class TestPropertyTriageComment:
    """Property tests for triage comment formatting."""

    @pytest.mark.parametrize("criteria_count", [1, 3, 5, 10])
    def test_ts_02_p4_triage_comment_always_uses_bold(
        self,
        pipeline: FixPipeline,
        criteria_count: int,
    ) -> None:
        """_format_triage_comment calls _render_criteria_section(bold=True).

        Property: 02-PROP-4
        Validates: 02-REQ-4.1
        """
        triage = TriageResult(
            summary="Analysis",
            criteria=_make_criteria(criteria_count),
        )

        with (
            patch.object(
                FixPipeline,
                "_render_criteria_section",
                wraps=FixPipeline._render_criteria_section,
            ) as mock_section,
            patch(
                "agentfox.nightshift.fix_pipeline.render_inmemory_spec_sections",
                create=True,
            ) as mock_afspec,
        ):
            pipeline._format_triage_comment(triage)

        assert mock_section.call_args.kwargs.get("bold") is True
        mock_afspec.assert_not_called()


# ---------------------------------------------------------------------------
# TS-02-P5: parse_fix_review_output accepts arbitrary criterion_ids
# Property: 02-PROP-5
# ---------------------------------------------------------------------------


class TestPropertyVerdictParsing:
    """Property tests for verdict parsing flexibility."""

    @pytest.mark.parametrize(
        ("criterion_id", "verdict", "evidence"),
        [
            ("NS-REQ-1", "PASS", "Fix is correct"),
            ("AC-1", "FAIL", "Regression in test suite"),
            ("REQ-42", "PASS", "Edge case handled"),
            ("some-arbitrary-id", "FAIL", "Missing null check"),
            ("02-REQ-1.1", "PASS", "Criterion met"),
        ],
    )
    def test_ts_02_p5_verdict_accepts_arbitrary_criterion_ids(
        self,
        criterion_id: str,
        verdict: str,
        evidence: str,
    ) -> None:
        """parse_fix_review_output parses any criterion_id string.

        Property: 02-PROP-5
        Validates: 02-REQ-5.1
        """
        from agentfox.session.review_parser import parse_fix_review_output

        verdict_json = json.dumps(
            {
                "verdicts": [
                    {
                        "criterion_id": criterion_id,
                        "verdict": verdict,
                        "evidence": evidence,
                    }
                ],
                "overall_verdict": verdict,
                "summary": "Test",
            }
        )

        result = parse_fix_review_output(verdict_json, "test-spec", "test-session")

        assert isinstance(result, FixReviewResult)
        assert result.verdicts[0].criterion_id == criterion_id
        assert result.verdicts[0].verdict == verdict
        assert result.verdicts[0].evidence == evidence


# ---------------------------------------------------------------------------
# Smoke Tests: TS-02-SMOKE-1 through TS-02-SMOKE-5
# ---------------------------------------------------------------------------


class TestSmokeTests:
    """End-to-end smoke tests for prompt alignment."""

    @pytest.mark.smoke
    def test_ts_02_smoke_1_coder_prompt_with_real_afspec(
        self,
        pipeline: FixPipeline,
        fake_spec: InMemorySpec,
        valid_triage: TriageResult,
        issue_body: str,
    ) -> None:
        """End-to-end: coder prompt built with real afspec rendering.

        Execution Path: 02-PATH-1
        """
        # Import real spec 01 functions (fails if spec 01 not landed)
        from agentfox.nightshift.spec_builder import (  # noqa: F401
            build_afspec_from_triage,  # type: ignore[attr-defined]
            render_inmemory_spec_sections,  # type: ignore[attr-defined]
        )

        with patch(
            "agentfox.session.prompt.build_system_prompt",
            return_value="mock",
        ) as mock_bsp:
            system_prompt, task_prompt = pipeline._build_coder_prompt(fake_spec, valid_triage)

        captured_context = mock_bsp.call_args.kwargs["context"]
        assert captured_context.startswith(issue_body)
        assert "## Requirements" in captured_context
        assert "## Test Specification" in captured_context
        assert "## Tasks" in captured_context
        assert SUBTASK_PHRASE in task_prompt

    @pytest.mark.smoke
    def test_ts_02_smoke_2_coder_fallback_path(
        self,
        pipeline: FixPipeline,
        fake_spec: InMemorySpec,
        valid_triage: TriageResult,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """End-to-end: coder prompt falls back on afspec failure.

        Execution Path: 02-PATH-2
        """
        with (
            patch(
                "agentfox.nightshift.fix_pipeline.build_afspec_from_triage",
                side_effect=ValueError("malformed"),
                create=True,
            ),
            patch(
                "agentfox.session.prompt.build_system_prompt",
                return_value="mock",
            ) as mock_bsp,
            caplog.at_level(logging.WARNING),
        ):
            system_prompt, task_prompt = pipeline._build_coder_prompt(fake_spec, valid_triage)

        assert any(logging.WARNING == r.levelno for r in caplog.records)
        captured_context = mock_bsp.call_args.kwargs["context"]
        assert "## Acceptance Criteria from Triage" in captured_context
        assert isinstance(system_prompt, str) and len(system_prompt) > 0

    @pytest.mark.smoke
    def test_ts_02_smoke_3_reviewer_prompt_with_real_afspec(
        self,
        pipeline: FixPipeline,
        fake_spec: InMemorySpec,
        valid_triage: TriageResult,
        issue_body: str,
    ) -> None:
        """End-to-end: reviewer prompt built with real afspec rendering.

        Execution Path: 02-PATH-3
        """
        with patch(
            "agentfox.session.prompt.build_system_prompt",
            return_value="mock",
        ) as mock_bsp:
            system_prompt, task_prompt = pipeline._build_reviewer_prompt(fake_spec, valid_triage)

        captured_context = mock_bsp.call_args.kwargs["context"]
        assert captured_context.startswith(issue_body)
        assert "## Requirements" in captured_context
        assert "## Test Specification" in captured_context
        assert "## Tasks" in captured_context

    @pytest.mark.smoke
    def test_ts_02_smoke_4_reviewer_empty_triage_fallback(
        self,
        pipeline: FixPipeline,
        fake_spec: InMemorySpec,
        empty_triage: TriageResult,
    ) -> None:
        """End-to-end: reviewer fallback message in task_prompt for empty triage.

        Execution Path: 02-PATH-4
        """
        with patch(
            "agentfox.session.prompt.build_system_prompt",
            return_value="mock",
        ):
            system_prompt, task_prompt = pipeline._build_reviewer_prompt(fake_spec, empty_triage)

        assert EMPTY_TRIAGE_FALLBACK in task_prompt
        assert isinstance(system_prompt, str) and len(system_prompt) > 0

    @pytest.mark.smoke
    def test_ts_02_smoke_5_triage_comment_format_unchanged(
        self,
        pipeline: FixPipeline,
        valid_triage: TriageResult,
    ) -> None:
        """End-to-end: _format_triage_comment produces bold-formatted comment.

        Execution Path: 02-PATH-5
        """
        with patch(
            "agentfox.nightshift.fix_pipeline.render_inmemory_spec_sections",
            create=True,
        ) as mock_afspec:
            comment = pipeline._format_triage_comment(valid_triage)

        mock_afspec.assert_not_called()
        assert isinstance(comment, str) and len(comment) > 0
        # Verify bold formatting is present (from bold=True)
        assert "**" in comment
