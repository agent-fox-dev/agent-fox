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


def _format_spec_landscape(landscape: list[dict[str, Any]] | None) -> str:
    """Format a landscape list into a markdown section for LLM prompt injection.

    Returns an empty string when *landscape* is ``None`` or empty, ensuring
    backward-compatible prompt output.  Otherwise builds a
    ``## Existing Spec Landscape`` section with tables for active and/or
    archived specs.
    """
    if not landscape:
        return ""

    active = [e for e in landscape if not e.get("archived", False)]
    archived = [e for e in landscape if e.get("archived", False)]

    parts: list[str] = [
        "## Existing Spec Landscape\n",
        "The following specs already exist in this project. "
        "Check for overlaps, historical precedent, and potential dependencies.\n",
    ]

    if active:
        parts.append("### Active Specs\n")
        parts.append("| Spec | Title | Status | Intent |")
        parts.append("|------|-------|--------|--------|")
        for entry in active:
            spec = entry.get("spec_name", entry.get("spec_id", ""))
            title = entry.get("title", "")
            status = entry.get("status", "")
            intent = entry.get("intent", "")
            glossary_terms = entry.get("glossary_terms", [])
            if glossary_terms:
                terms_str = ", ".join(glossary_terms[:10])
                intent = f"{intent} (Terms: {terms_str})" if intent else f"Terms: {terms_str}"
            parts.append(f"| {spec} | {title} | {status} | {intent} |")
        parts.append("")

    if archived:
        parts.append("### Archived Specs\n")
        parts.append("| Spec | Title | Status |")
        parts.append("|------|-------|--------|")
        for entry in archived:
            spec = entry.get("spec_name", entry.get("spec_id", ""))
            title = entry.get("title", "")
            status = entry.get("status", "")
            parts.append(f"| {spec} | {title} | {status} |")
        parts.append("")

    return "\n".join(parts)


def _format_dependent_interfaces(dependent_interfaces: list[dict[str, Any]] | None) -> str:
    """Format dependent spec interfaces into a markdown section."""
    if not dependent_interfaces:
        return ""

    parts: list[str] = [
        "## Dependent Spec Interfaces\n",
        "The following interfaces are defined by upstream specs that this spec depends on. "
        "Use the exact names, types, and signatures below.\n",
    ]

    for iface in dependent_interfaces:
        parts.append(f"### Spec {iface['spec_id']} — {iface.get('spec_name', '')}\n")

        glossary = iface.get("glossary", {})
        if glossary:
            parts.append("**Glossary:**\n")
            for term, definition in glossary.items():
                parts.append(f"- **{term}**: {definition}")
            parts.append("")

        external_apis = iface.get("external_apis", [])
        if external_apis:
            parts.append("**External APIs:**\n")
            parts.append(f"```json\n{json.dumps(external_apis, indent=2)}\n```\n")

        symbols = iface.get("interface_symbols", [])
        if symbols:
            parts.append("**Interface Symbols:**\n")
            parts.append("| Criterion ID | Action | Return Contract |")
            parts.append("|---|---|---|")
            for sym in symbols:
                cid = sym.get("criterion_id", "")
                action = sym.get("action", "").replace("\n", " ")
                rc = sym.get("return_contract", "").replace("\n", " ")
                parts.append(f"| {cid} | {action} | {rc} |")
            parts.append("")

    return "\n".join(parts)


_LANGUAGE_MARKERS: list[tuple[str, str, str]] = [
    ("go.mod", "Go", "go test ./... -count=1, go vet ./..."),
    ("Cargo.toml", "Rust", "cargo test, cargo clippy"),
    ("package.json", "TypeScript/JavaScript", "npm test, eslint ."),
    ("pyproject.toml", "Python", "pytest, ruff check"),
    ("setup.py", "Python", "pytest, ruff check"),
    ("build.gradle", "Java/Kotlin", "gradle test, gradle check"),
    ("pom.xml", "Java", "mvn test, mvn checkstyle:check"),
    ("mix.exs", "Elixir", "mix test, mix credo"),
    ("Gemfile", "Ruby", "bundle exec rspec, rubocop"),
]


def _detect_project_language(project_dir: Path | None) -> tuple[str, str] | None:
    """Detect the project's primary language from manifest files.

    Returns a ``(language, tooling_hint)`` tuple, or ``None`` if no
    marker file is found.
    """
    if not project_dir:
        return None
    for filename, language, tooling in _LANGUAGE_MARKERS:
        if (project_dir / filename).exists():
            return language, tooling
    return None


def _format_project_context(project_dir: Path | None) -> str:
    """Build a ``## Project Context`` block from detected language."""
    detected = _detect_project_language(project_dir)
    if not detected:
        return ""
    language, tooling = detected
    return (
        f"## Project Context\n\n"
        f"This is a **{language}** project. All test commands, verification "
        f"checks, file paths, code patterns, and stub/dead-code markers MUST "
        f"use {language} conventions (e.g. {tooling}).\n"
    )


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
    spec_landscape: list[dict[str, Any]] | None = None,
) -> str:
    """Return the user message for PRD assessment.

    When *spec_landscape* is provided, the formatted landscape markdown
    is injected into the prompt via ``$spec_landscape_block``.

    Raises ``ValueError`` if *prd_text* is empty.
    """
    _require_non_empty(prd_text, "prd_text")

    spec_landscape_block = _format_spec_landscape(spec_landscape)

    return load_prompt_template(
        "assessment_user",
        project_dir=project_dir,
        prd_text=prd_text,
        spec_name=spec_name,
        spec_landscape_block=spec_landscape_block,
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
    spec_landscape: list[dict[str, Any]] | None = None,
) -> str:
    """Return the user message for PRD refinement.

    Formats the original PRD, the user's answers (keyed by question ID),
    and the previous assessment into a single user message.  When
    *spec_landscape* is provided, the formatted landscape markdown is
    injected via ``$spec_landscape_block``.
    """
    assessment_block = _format_assessment_block(previous_assessment)
    qa_block = _format_qa_block(previous_assessment, answers)
    spec_landscape_block = _format_spec_landscape(spec_landscape)

    return load_prompt_template(
        "refinement_user",
        project_dir=project_dir,
        prd_text=prd_text,
        assessment_block=assessment_block,
        qa_block=qa_block,
        spec_landscape_block=spec_landscape_block,
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
    dependent_interfaces: list[dict[str, Any]] | None = None,
    spec_landscape: list[dict[str, Any]] | None = None,
) -> str:
    """Return the user message for generating one artifact.

    *prior_artifacts* is a dict of already-generated artifacts
    (e.g., ``{"requirements": {...}}``) to provide as context.
    *spec_id* is the spec identifier used as prefix in all IDs.
    *dependent_interfaces* is a list of interface summaries from
    upstream dependency specs.
    *spec_landscape* is an optional list of existing spec metadata
    for cross-spec awareness during generation.

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
    project_context_block = _format_project_context(project_dir)
    dependent_interfaces_block = _format_dependent_interfaces(dependent_interfaces)
    spec_landscape_block = _format_spec_landscape(spec_landscape)

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
        project_context_block=project_context_block,
        prd_text=prd_text,
        dependent_interfaces_block=dependent_interfaces_block,
        spec_landscape_block=spec_landscape_block,
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
