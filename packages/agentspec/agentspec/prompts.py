"""Prompt templates for agent pipeline operations.

Centralised, parameterizable prompts for PRD assessment, refinement, and
artifact generation.  Each function constructs the system or user message
sent to the Anthropic messages API.

Requirements: 03-REQ-4.1, 03-REQ-4.2, 03-REQ-4.3, 03-REQ-4.E1
"""

from __future__ import annotations

import json
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from agentspec.session import Assessment

# ── helpers ──────────────────────────────────────────────────────────


def _require_non_empty(value: str, name: str) -> None:
    """Raise ``ValueError`` if *value* is empty or whitespace-only."""
    if not value or not value.strip():
        raise ValueError(f"{name} must not be empty")


# ── assessment ───────────────────────────────────────────────────────


def assessment_system_prompt() -> str:
    """Return the system prompt for PRD assessment.

    Instructs the model to evaluate PRD quality against spec-format
    expectations, explicitly checking for the Intent, Goals, Non-Goals,
    and Background sections.
    """
    return (
        "You are a senior requirements engineer evaluating a Product "
        "Requirements Document (PRD) for completeness and quality.\n\n"
        "Evaluate the PRD against the following spec-format expectations:\n\n"
        "1. **Intent** (required) — A clear, concise statement of what the "
        "product or feature aims to achieve. This section must be present "
        "and well-articulated.\n"
        "2. **Goals** — Measurable outcomes the product should deliver.\n"
        "3. **Non-Goals** — Explicit boundaries stating what is deliberately "
        "excluded from scope.\n"
        "4. **Background** — Context, motivation, and any prior art that "
        "informs the requirements.\n\n"
        "For each section, assess whether it is present, complete, and of "
        "sufficient quality. Identify gaps, ambiguities, and missing "
        "information.\n\n"
        "Use the submit_assessment tool to provide your structured "
        "evaluation. Set the quality field to one of:\n"
        '- "ready" — the PRD is complete and can proceed to artifact '
        "generation.\n"
        '- "needs_refinement" — the PRD has gaps that the user can address '
        "with targeted answers.\n"
        '- "incomplete" — the PRD is missing fundamental sections or is '
        "too vague to assess meaningfully.\n\n"
        'When the quality is not "ready", provide targeted questions the '
        "user can answer to improve the PRD."
    )


def assessment_user_prompt(prd_text: str, spec_name: str) -> str:
    """Return the user message for PRD assessment.

    Raises ``ValueError`` if *prd_text* is empty.
    """
    _require_non_empty(prd_text, "prd_text")

    return (
        f"Please assess the following PRD for the spec named "
        f'"{spec_name}".\n\n'
        f"---\n\n"
        f"{prd_text}\n\n"
        f"---\n\n"
        f"Provide your structured assessment using the submit_assessment tool."
    )


# ── refinement ───────────────────────────────────────────────────────


def refinement_system_prompt() -> str:
    """Return the system prompt for PRD refinement.

    Instructs the model to incorporate the user's answers into the PRD
    and re-assess the updated document.
    """
    return (
        "You are a senior requirements engineer helping to refine a Product "
        "Requirements Document (PRD).\n\n"
        "You will receive the original PRD, a previous assessment with "
        "questions, and the user's answers to those questions.\n\n"
        "Your tasks:\n"
        "1. Incorporate the user's answers into the PRD body, improving "
        "clarity and completeness. Return only the body content (no YAML "
        "frontmatter). The caller will re-attach frontmatter.\n"
        "2. Assess the updated PRD and evaluate whether it now meets "
        "spec-format quality standards.\n\n"
        "Use the submit_prd_update tool to provide the updated PRD body, "
        "and the submit_assessment tool to provide your new evaluation.\n\n"
        "Both tool calls are required in your response."
    )


def refinement_user_prompt(
    prd_text: str,
    answers: dict[str, str],
    previous_assessment: Assessment,
) -> str:
    """Return the user message for PRD refinement.

    Formats the original PRD, the user's answers (keyed by question ID),
    and the previous assessment into a single user message.
    """
    # Format previous assessment summary
    assessment_block = f"Quality: {previous_assessment.quality}\nSummary: {previous_assessment.summary}\n"
    if previous_assessment.gaps:
        assessment_block += "Gaps:\n"
        for gap in previous_assessment.gaps:
            assessment_block += f"  - {gap}\n"

    # Format questions and answers together
    qa_block = ""
    for q in previous_assessment.questions:
        answer_text = answers.get(q.id, "(no answer provided)")
        qa_block += f"- {q.id}: {q.text}\n  Context: {q.context}\n  Answer: {answer_text}\n"

    return (
        f"## Original PRD\n\n"
        f"{prd_text}\n\n"
        f"## Previous Assessment\n\n"
        f"{assessment_block}\n"
        f"## Questions and Answers\n\n"
        f"{qa_block}\n"
        f"Please incorporate the answers into the PRD and provide an "
        f"updated assessment."
    )


# ── generation ───────────────────────────────────────────────────────


def generation_system_prompt() -> str:
    """Return the system prompt for artifact generation.

    Instructs the model to produce a single artifact at a time in the
    correct JSON schema, conforming to spec-format v1.2.
    """
    return (
        "You are a senior requirements engineer generating spec artifacts "
        "from an accepted Product Requirements Document (PRD).\n\n"
        "You will generate one artifact at a time. The tool schema "
        "defines the exact structure — fill in the content fields "
        "according to that schema.\n\n"
        "Do NOT include spec_id, spec_name, or schema_version in your "
        "output — these are injected automatically.\n\n"
        "Important guidelines:\n"
        "- Follow the tool schema exactly; do not add extra fields.\n"
        "- Ensure all cross-references (requirement IDs, test IDs) are "
        "consistent.\n"
        "- Write clear, specific, and testable requirements.\n"
        "- Each artifact must be self-contained and complete."
    )


def generation_user_prompt(
    prd_text: str,
    artifact_name: str,
    prior_artifacts: dict[str, Any] | None = None,
) -> str:
    """Return the user message for generating one artifact.

    *prior_artifacts* is a dict of already-generated artifacts
    (e.g., ``{"requirements": {...}}``) to provide as context.

    Raises ``ValueError`` if *prd_text* is empty.
    """
    _require_non_empty(prd_text, "prd_text")

    parts: list[str] = [
        f"Generate the **{artifact_name}** artifact from the following PRD.\n",
        f"## PRD\n\n{prd_text}\n",
    ]

    # Include prior artifacts as context
    if prior_artifacts:
        parts.append("## Previously Generated Artifacts\n")
        for name, content in prior_artifacts.items():
            parts.append(f"### {name}\n\n```json\n{json.dumps(content, indent=2)}\n```\n")

    # Artifact-specific instructions
    if artifact_name == "requirements":
        parts.append(
            "## Additional Instructions\n\n"
            "Populate the `glossary` field with definitions for all "
            "domain-specific terms used in backtick-delimited references "
            "within acceptance criteria, edge cases, and correctness "
            "properties. Every backtick-wrapped term that carries domain "
            "meaning must have a glossary entry.\n"
        )
    elif artifact_name == "test_spec":
        parts.append(
            "## Additional Instructions\n\n"
            "Reference requirement IDs from the previously generated "
            "requirements artifact in your test case entries.\n"
        )
    elif artifact_name == "tasks":
        parts.append(
            "## Additional Instructions\n\n"
            "Reference both requirement IDs and test IDs from the "
            "previously generated requirements and test_spec artifacts "
            "in your task entries.\n"
        )

    parts.append(f"Use the submit_{artifact_name} tool to return the generated artifact.")

    return "\n".join(parts)
