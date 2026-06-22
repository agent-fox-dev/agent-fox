"""Prompt templates for agent pipeline operations.

Centralised, parameterizable prompts for PRD assessment, refinement, and
artifact generation.  Each function constructs the system or user message
sent to the Anthropic messages API.

Prompt content is loaded from markdown template files under
``_templates/prompts/``, with project-level overrides in
``.agent-fox/prompts/`` taking precedence.

Requirements: 03-REQ-4.1, 03-REQ-4.2, 03-REQ-4.3, 03-REQ-4.E1
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import TYPE_CHECKING, Any

from agentspec.prompt_loader import load_prompt, load_prompt_template

if TYPE_CHECKING:
    from agentspec.session import Assessment

# ── helpers ──────────────────────────────────────────────────────────


def _require_non_empty(value: str, name: str) -> None:
    """Raise ``ValueError`` if *value* is empty or whitespace-only."""
    if not value or not value.strip():
        raise ValueError(f"{name} must not be empty")


def _format_assessment_block(previous_assessment: Assessment) -> str:
    """Format an assessment's quality, summary, and gaps into a text block."""
    block = f"Quality: {previous_assessment.quality}\nSummary: {previous_assessment.summary}\n"
    if previous_assessment.gaps:
        block += "Gaps:\n"
        for gap in previous_assessment.gaps:
            block += f"  - {gap}\n"
    return block


def _format_qa_block(
    previous_assessment: Assessment,
    answers: dict[str, str],
) -> str:
    """Format questions and answers into a text block."""
    parts: list[str] = []
    for q in previous_assessment.questions:
        answer_text = answers.get(q.id, "(no answer provided)")
        parts.append(f"- {q.id}: {q.text}\n  Context: {q.context}\n  Answer: {answer_text}")
    return "\n".join(parts)


def _format_prior_artifacts(prior_artifacts: dict[str, Any] | None) -> str:
    """Format previously generated artifacts as a context section."""
    if not prior_artifacts:
        return ""
    parts = ["## Previously Generated Artifacts\n"]
    for name, content in prior_artifacts.items():
        parts.append(f"### {name}\n\n```json\n{json.dumps(content, indent=2)}\n```\n")
    return "\n".join(parts)


# ── assessment ───────────────────────────────────────────────────────


def assessment_system_prompt(*, project_dir: Path | None = None) -> str:
    """Return the system prompt for PRD assessment.

    Instructs the model to evaluate PRD quality against spec-format
    expectations, explicitly checking for the Intent, Goals, Non-Goals,
    and Background sections.
    """
    return load_prompt("assessment_system", project_dir=project_dir)


def assessment_user_prompt(
    prd_text: str,
    spec_name: str,
    *,
    project_dir: Path | None = None,
) -> str:
    """Return the user message for PRD assessment.

    Raises ``ValueError`` if *prd_text* is empty.
    """
    _require_non_empty(prd_text, "prd_text")

    return load_prompt_template(
        "assessment_user",
        project_dir=project_dir,
        prd_text=prd_text,
        spec_name=spec_name,
    )


# ── refinement ───────────────────────────────────────────────────────


def refinement_system_prompt(*, project_dir: Path | None = None) -> str:
    """Return the system prompt for PRD refinement.

    Instructs the model to incorporate the user's answers into the PRD
    and re-assess the updated document.
    """
    return load_prompt("refinement_system", project_dir=project_dir)


def refinement_user_prompt(
    prd_text: str,
    answers: dict[str, str],
    previous_assessment: Assessment,
    *,
    project_dir: Path | None = None,
) -> str:
    """Return the user message for PRD refinement.

    Formats the original PRD, the user's answers (keyed by question ID),
    and the previous assessment into a single user message.
    """
    assessment_block = _format_assessment_block(previous_assessment)
    qa_block = _format_qa_block(previous_assessment, answers)

    return load_prompt_template(
        "refinement_user",
        project_dir=project_dir,
        prd_text=prd_text,
        assessment_block=assessment_block,
        qa_block=qa_block,
    )


# ── generation ───────────────────────────────────────────────────────


def generation_system_prompt(*, project_dir: Path | None = None) -> str:
    """Return the system prompt for artifact generation.

    Instructs the model to produce a single artifact at a time in the
    correct JSON schema, conforming to spec-format v1.2.
    """
    return load_prompt("generation_system", project_dir=project_dir)


def generation_user_prompt(
    prd_text: str,
    artifact_name: str,
    prior_artifacts: dict[str, Any] | None = None,
    *,
    spec_id: str = "",
    project_dir: Path | None = None,
) -> str:
    """Return the user message for generating one artifact.

    *prior_artifacts* is a dict of already-generated artifacts
    (e.g., ``{"requirements": {...}}``) to provide as context.
    *spec_id* is the spec identifier used as prefix in all IDs.

    Raises ``ValueError`` if *prd_text* is empty.
    """
    _require_non_empty(prd_text, "prd_text")

    spec_id_block = ""
    if spec_id:
        spec_id_block = (
            f"The spec_id for this spec is `{spec_id}`. Use it as the "
            f"prefix in all IDs (e.g. `{spec_id}-REQ-1`, "
            f"`TS-{spec_id}-1`).\n"
        )

    prior_artifacts_block = _format_prior_artifacts(prior_artifacts)

    additional_instructions = ""
    try:
        additional_instructions = load_prompt(
            f"generation_user_{artifact_name}",
            project_dir=project_dir,
        )
    except (FileNotFoundError, ValueError):
        pass

    return load_prompt_template(
        "generation_user_base",
        project_dir=project_dir,
        artifact_name=artifact_name,
        spec_id_block=spec_id_block,
        prd_text=prd_text,
        prior_artifacts_block=prior_artifacts_block,
        additional_instructions=additional_instructions,
    )


# ── repair ──────────────────────────────────────────────────────────


def repair_user_prompt(
    artifact_name: str,
    original_content: dict[str, Any],
    errors: list[str],
    *,
    project_dir: Path | None = None,
) -> str:
    """Return a user message asking the LLM to fix validation errors.

    Sends the original artifact content and a list of errors, asking
    the model to resubmit with corrections.
    """
    error_list = "\n".join(f"- {e}" for e in errors)
    original_json = json.dumps(original_content, indent=2)

    return load_prompt_template(
        "repair_user",
        project_dir=project_dir,
        artifact_name=artifact_name,
        error_list=error_list,
        original_json=original_json,
    )
