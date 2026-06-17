"""Tests for agentspec.prompts — prompt template content and validation.

Covers TS-03-15 through TS-03-17, TS-03-32, and TS-03-E10.
"""

from __future__ import annotations

import pytest
from agentspec.prompts import (
    assessment_system_prompt,
    assessment_user_prompt,
    generation_system_prompt,
    generation_user_prompt,
    refinement_system_prompt,
    refinement_user_prompt,
)
from agentspec.session import Assessment, Question

# ===================================================================
# TS-03-15: Assessment prompt template content
# ===================================================================


def test_assessment_system_prompt_references_sections():
    """TS-03-15: The assessment system prompt references spec-format
    expectations including Intent, Goals, Non-Goals, Background."""
    prompt = assessment_system_prompt()

    assert len(prompt) > 0
    assert "Intent" in prompt
    assert "Goals" in prompt
    assert "Non-Goals" in prompt
    assert "Background" in prompt


# ===================================================================
# TS-03-16: Refinement prompt template content
# ===================================================================


def test_refinement_system_prompt_instructs_incorporation():
    """TS-03-16: The refinement system prompt instructs the model to
    incorporate answers and re-assess."""
    prompt = refinement_system_prompt()

    assert len(prompt) > 0
    # Must mention incorporating answers
    lower = prompt.lower()
    assert "answer" in lower or "incorporate" in lower
    # Must mention assessment/evaluation
    assert "assess" in lower or "evaluat" in lower


# ===================================================================
# TS-03-17: Generation prompt template content
# ===================================================================


def test_generation_system_prompt_references_json():
    """TS-03-17: The generation system prompt instructs the model to
    produce a single artifact in the correct JSON schema."""
    prompt = generation_system_prompt()

    assert len(prompt) > 0
    lower = prompt.lower()
    assert "json" in lower or "schema" in lower
    assert "artifact" in lower


# ===================================================================
# TS-03-32: Generation prompt includes glossary instruction
# ===================================================================


def test_generation_prompt_glossary_instruction():
    """TS-03-32: The generation prompt for requirements instructs the
    agent to populate the glossary with backtick-wrapped domain terms."""
    prompt = generation_user_prompt(
        prd_text="# Test PRD\n## Intent\nBuild something",
        artifact_name="requirements",
        prior_artifacts={},
    )

    lower = prompt.lower()
    assert "glossary" in lower
    assert "backtick" in lower or "domain" in lower


# ===================================================================
# TS-03-E10: Missing prompt parameter raises ValueError
# ===================================================================


def test_assessment_user_prompt_empty_prd_raises():
    """TS-03-E10: assessment_user_prompt raises ValueError for empty prd_text."""
    with pytest.raises(ValueError):
        assessment_user_prompt("", "test")


def test_generation_user_prompt_empty_prd_raises():
    """TS-03-E10: generation_user_prompt raises ValueError for empty prd_text."""
    with pytest.raises(ValueError):
        generation_user_prompt("", "requirements")


# ===================================================================
# Additional: user prompt functions produce non-empty output
# ===================================================================


def test_assessment_user_prompt_contains_prd():
    """Verify assessment_user_prompt includes the PRD text."""
    prompt = assessment_user_prompt("# My PRD content", "my_spec")
    assert "My PRD content" in prompt
    assert "my_spec" in prompt


def test_refinement_user_prompt_contains_answers():
    """Verify refinement_user_prompt includes answers and previous assessment."""
    prev = Assessment(
        quality="needs_refinement",
        summary="Needs work",
        gaps=[],
        questions=[Question(id="q1", text="What?", context="Ctx", options=[], required=True)],
    )
    prompt = refinement_user_prompt(
        prd_text="# Original PRD",
        answers={"q1": "My answer"},
        previous_assessment=prev,
    )
    assert "My answer" in prompt
    assert "q1" in prompt


def test_generation_user_prompt_contains_artifact_name():
    """Verify generation_user_prompt includes the artifact name."""
    prompt = generation_user_prompt(prd_text="# Test PRD", artifact_name="test_spec", prior_artifacts={})
    assert "test_spec" in prompt
