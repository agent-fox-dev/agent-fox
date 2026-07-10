"""Tests for spec landscape awareness -- _format_spec_landscape and prompt layer changes.

Covers:
- TS-01-9 through TS-01-12: unit tests for _format_spec_landscape formatting
- TS-01-13, TS-01-14: unit tests for assessment_user_prompt spec_landscape parameter
- TS-01-15, TS-01-16: unit tests for refinement_user_prompt spec_landscape parameter
- TS-01-17, TS-01-18, TS-01-19: unit tests for template file content
- TS-01-P3, TS-01-P6: property tests for prompt backward compatibility
"""

from __future__ import annotations

import pytest
from agentspec.prompt_loader import _DEFAULT_PROMPTS_DIR
from agentspec.prompts import (
    _format_spec_landscape,
    assessment_user_prompt,
    refinement_user_prompt,
)
from agentspec.session import Assessment, Question

# ===========================================================================
# TS-01-9: _format_spec_landscape returns a markdown block with both
# Active Specs and Archived Specs tables when both types are present.
# (01-REQ-3.1)
# ===========================================================================


class TestFormatSpecLandscapeBothTypes:
    """TS-01-9: Mixed active+archived landscape produces both table sections."""

    def test_both_active_and_archived_tables(self) -> None:
        landscape = [
            {
                "spec_id": "01",
                "spec_name": "core_foundation",
                "title": "Core Foundation",
                "status": "implemented",
                "intent": "Establish the base...",
                "archived": False,
            },
            {
                "spec_id": "08",
                "spec_name": "old_spec",
                "title": "Old Spec",
                "status": "archived",
                "archived": True,
            },
        ]

        result = _format_spec_landscape(landscape)

        assert "## Existing Spec Landscape" in result
        assert "### Active Specs" in result
        assert "### Archived Specs" in result
        assert "Core Foundation" in result
        assert "Old Spec" in result
        assert "Establish the base" in result


# ===========================================================================
# TS-01-10: _format_spec_landscape returns an empty string when called with
# None or an empty list.  (01-REQ-3.2)
# ===========================================================================


class TestFormatSpecLandscapeEmpty:
    """TS-01-10: Returns '' for None and [] inputs."""

    def test_returns_empty_for_none(self) -> None:
        assert _format_spec_landscape(None) == ""

    def test_returns_empty_for_empty_list(self) -> None:
        assert _format_spec_landscape([]) == ""


# ===========================================================================
# TS-01-11: _format_spec_landscape omits the ### Archived Specs table when
# the landscape contains only active specs.  (01-REQ-3.3)
# ===========================================================================


class TestFormatSpecLandscapeActiveOnly:
    """TS-01-11: Active-only landscape omits '### Archived Specs'."""

    def test_active_only_omits_archived_section(self) -> None:
        landscape = [
            {
                "spec_id": "01",
                "spec_name": "core_foundation",
                "title": "Core Foundation",
                "status": "implemented",
                "intent": "Establish the base...",
                "archived": False,
            },
        ]

        result = _format_spec_landscape(landscape)

        assert "### Active Specs" in result
        assert "### Archived Specs" not in result


# ===========================================================================
# TS-01-12: _format_spec_landscape omits the ### Active Specs table when
# the landscape contains only archived specs.  (01-REQ-3.4)
# ===========================================================================


class TestFormatSpecLandscapeArchivedOnly:
    """TS-01-12: Archived-only landscape omits '### Active Specs'."""

    def test_archived_only_omits_active_section(self) -> None:
        landscape = [
            {
                "spec_id": "08",
                "spec_name": "old_spec",
                "title": "Old Spec",
                "status": "archived",
                "archived": True,
            },
        ]

        result = _format_spec_landscape(landscape)

        assert "### Active Specs" not in result
        assert "### Archived Specs" in result


# ===========================================================================
# TS-01-13: assessment_user_prompt accepts spec_landscape parameter, formats
# it via _format_spec_landscape, and substitutes into the template at
# $spec_landscape_block.  (01-REQ-4.1)
# ===========================================================================


class TestAssessmentUserPromptLandscape:
    """TS-01-13: assessment_user_prompt with non-empty landscape."""

    def test_landscape_injected_into_prompt(self) -> None:
        landscape = [
            {
                "spec_id": "01",
                "spec_name": "core",
                "title": "Core",
                "status": "implemented",
                "intent": "Base layer...",
                "archived": False,
            },
        ]

        result = assessment_user_prompt(
            "# My PRD\n...",
            "new_spec",
            spec_landscape=landscape,
        )

        assert "## Existing Spec Landscape" in result
        assert "$spec_landscape_block" not in result
        assert "My PRD" in result


# ===========================================================================
# TS-01-14: assessment_user_prompt substitutes an empty string for
# $spec_landscape_block when spec_landscape is None or empty, producing
# no landscape section.  (01-REQ-4.2)
# ===========================================================================


class TestAssessmentUserPromptNoLandscape:
    """TS-01-14: assessment_user_prompt with None/empty landscape."""

    def test_none_landscape_produces_no_section(self) -> None:
        result = assessment_user_prompt(
            "# My PRD\n...",
            "new_spec",
            spec_landscape=None,
        )

        assert "## Existing Spec Landscape" not in result
        assert "$spec_landscape_block" not in result

    def test_empty_landscape_produces_no_section(self) -> None:
        result = assessment_user_prompt(
            "# My PRD\n...",
            "new_spec",
            spec_landscape=[],
        )

        assert "## Existing Spec Landscape" not in result


# ===========================================================================
# TS-01-15: refinement_user_prompt accepts spec_landscape parameter and
# substitutes the formatted landscape at $spec_landscape_block after
# the QA block.  (01-REQ-5.1)
# ===========================================================================


class TestRefinementUserPromptLandscape:
    """TS-01-15: refinement_user_prompt with non-empty landscape."""

    def test_landscape_injected_into_refinement_prompt(self) -> None:
        landscape = [
            {
                "spec_id": "01",
                "spec_name": "core",
                "title": "Core",
                "status": "implemented",
                "intent": "Base layer...",
                "archived": False,
            },
        ]
        prev = Assessment(
            quality="draft",
            summary="Needs work",
            gaps=[],
            questions=[
                Question(id="q1", text="What?", context="Ctx", options=[], required=True),
            ],
        )

        result = refinement_user_prompt(
            "# My PRD\n...",
            {"q1": "answer1"},
            prev,
            spec_landscape=landscape,
        )

        assert "## Existing Spec Landscape" in result
        assert "$spec_landscape_block" not in result


# ===========================================================================
# TS-01-16: refinement_user_prompt substitutes an empty string for
# $spec_landscape_block when spec_landscape is None or empty.  (01-REQ-5.2)
# ===========================================================================


class TestRefinementUserPromptNoLandscape:
    """TS-01-16: refinement_user_prompt with None/empty landscape."""

    def test_none_landscape_produces_no_section(self) -> None:
        prev = Assessment(
            quality="draft",
            summary="Needs work",
            gaps=[],
            questions=[
                Question(id="q1", text="What?", context="Ctx", options=[], required=True),
            ],
        )

        result = refinement_user_prompt(
            "# My PRD\n...",
            {"q1": "answer1"},
            prev,
            spec_landscape=None,
        )

        assert "## Existing Spec Landscape" not in result
        assert "$spec_landscape_block" not in result


# ===========================================================================
# TS-01-17: assessment_system.md contains a ## Cross-spec awareness section
# with instructions for overlap detection, historical precedent, and
# dependency suggestion.  (01-REQ-6.1)
# ===========================================================================


class TestAssessmentSystemTemplate:
    """TS-01-17: assessment_system.md has ## Cross-spec awareness section."""

    def test_cross_spec_awareness_section_exists(self) -> None:
        content = (_DEFAULT_PROMPTS_DIR / "assessment_system.md").read_text()

        assert "## Cross-spec awareness" in content
        assert "gap" in content.lower()
        assert "archived" in content.lower()
        assert "dependency" in content.lower()


# ===========================================================================
# TS-01-18: assessment_user.md template contains $spec_landscape_block
# positioned before the PRD text block.  (01-REQ-6.2)
# ===========================================================================


class TestAssessmentUserTemplate:
    """TS-01-18: $spec_landscape_block appears before $prd_text in template."""

    def test_landscape_block_before_prd_text(self) -> None:
        content = (_DEFAULT_PROMPTS_DIR / "assessment_user.md").read_text()

        assert "$spec_landscape_block" in content
        assert "$prd_text" in content
        assert content.index("$spec_landscape_block") < content.index("$prd_text")


# ===========================================================================
# TS-01-19: refinement_user.md template contains $spec_landscape_block
# positioned after the QA block variable.  (01-REQ-6.3)
# ===========================================================================


class TestRefinementUserTemplate:
    """TS-01-19: $spec_landscape_block appears after $qa_block in template."""

    def test_landscape_block_after_qa_block(self) -> None:
        content = (_DEFAULT_PROMPTS_DIR / "refinement_user.md").read_text()

        assert "$qa_block" in content
        assert "$spec_landscape_block" in content
        assert content.index("$spec_landscape_block") > content.index("$qa_block")


# ===========================================================================
# Property Tests
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-01-P3: For any call to _format_spec_landscape with None or [],
# the function returns exactly ''.  (01-PROP-3)
# ---------------------------------------------------------------------------


class TestFormatSpecLandscapeEmptyProperty:
    """TS-01-P3: _format_spec_landscape(None) and _format_spec_landscape([]) always return ''."""

    @pytest.mark.parametrize("input_val", [None, []])
    def test_empty_input_returns_empty_string(self, input_val: list | None) -> None:
        result = _format_spec_landscape(input_val)
        assert result == ""


# ---------------------------------------------------------------------------
# TS-01-P6: Backward compatibility -- calling assessment_user_prompt and
# refinement_user_prompt without spec_landscape produces the same output
# as calling with spec_landscape=None.  (01-PROP-6)
# ---------------------------------------------------------------------------


class TestPromptBackwardCompatibility:
    """TS-01-P6: Omitting spec_landscape matches spec_landscape=None output."""

    @pytest.mark.parametrize(
        "prd_text,spec_name",
        [
            ("# Simple PRD\nSome content", "simple_spec"),
            ("# Complex PRD\n## Intent\nDo something\n## Goals\n- Goal 1", "complex_spec"),
            ("# Minimal\nX", "min_spec"),
        ],
    )
    def test_assessment_default_matches_none(self, prd_text: str, spec_name: str) -> None:
        result_default = assessment_user_prompt(prd_text, spec_name)
        result_none = assessment_user_prompt(prd_text, spec_name, spec_landscape=None)

        assert result_default == result_none
        assert "## Existing Spec Landscape" not in result_default

    @pytest.mark.parametrize(
        "prd_text",
        [
            "# Simple PRD\nSome content",
            "# Complex PRD\n## Intent\nDo something",
        ],
    )
    def test_refinement_default_matches_none(self, prd_text: str) -> None:
        prev = Assessment(
            quality="draft",
            summary="Test",
            gaps=[],
            questions=[
                Question(id="q1", text="Q?", context="C", options=[], required=True),
            ],
        )
        answers = {"q1": "A"}

        result_default = refinement_user_prompt(prd_text, answers, prev)
        result_none = refinement_user_prompt(prd_text, answers, prev, spec_landscape=None)

        assert result_default == result_none
        assert "## Existing Spec Landscape" not in result_default
