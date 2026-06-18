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
        "output — these are injected automatically. The spec_id will be "
        "provided as context; use it as the prefix in all IDs.\n\n"
        "## ID format rules (mandatory)\n\n"
        "All IDs follow strict formats. Use the spec_id as prefix.\n\n"
        "| Entity | Format | Example (spec_id=05) |\n"
        "| Requirement | {spec_id}-REQ-{N} | 05-REQ-3 |\n"
        "| Acceptance criterion | {spec_id}-REQ-{N}.{C} | 05-REQ-3.2 |\n"
        "| Edge case | {spec_id}-REQ-{N}.E{C} | 05-REQ-3.E1 |\n"
        "| Correctness property | {spec_id}-PROP-{N} | 05-PROP-2 |\n"
        "| Execution path | {spec_id}-PATH-{N} | 05-PATH-1 |\n"
        "| Error handling entry | {spec_id}-ERR-{N} | 05-ERR-1 |\n"
        "| Test case | TS-{spec_id}-{N} | TS-05-3 |\n"
        "| Property test | TS-{spec_id}-P{N} | TS-05-P2 |\n"
        "| Edge case test | TS-{spec_id}-E{N} | TS-05-E1 |\n"
        "| Smoke test | TS-{spec_id}-SMOKE-{N} | TS-05-SMOKE-1 |\n"
        "| Subtask | {group_id}.{N} | 3.2 |\n"
        "| Verification subtask | {group_id}.V | 3.V |\n\n"
        "## Mandatory field rules\n\n"
        "- Every object with a `title` field MUST have a non-empty, "
        "human-readable title. Empty titles fail validation.\n"
        "- Every `description` field MUST be a non-empty, substantive "
        "sentence — not just the title restated.\n"
        "- Every string field with `minLength: 1` in the schema MUST be "
        "non-empty.\n"
        "- Every verification subtask MUST have a non-empty `checks` "
        "array with concrete, actionable verification criteria.\n\n"
        "## Guidelines\n\n"
        "- Follow the tool schema exactly; do not add extra fields.\n"
        "- Ensure all cross-references (requirement IDs, test IDs) are "
        "consistent across artifacts.\n"
        "- Write clear, specific, and testable requirements.\n"
        "- Each artifact must be self-contained and complete."
    )


def generation_user_prompt(
    prd_text: str,
    artifact_name: str,
    prior_artifacts: dict[str, Any] | None = None,
    *,
    spec_id: str = "",
) -> str:
    """Return the user message for generating one artifact.

    *prior_artifacts* is a dict of already-generated artifacts
    (e.g., ``{"requirements": {...}}``) to provide as context.
    *spec_id* is the spec identifier used as prefix in all IDs.

    Raises ``ValueError`` if *prd_text* is empty.
    """
    _require_non_empty(prd_text, "prd_text")

    parts: list[str] = [
        f"Generate the **{artifact_name}** artifact from the following PRD.\n",
    ]
    if spec_id:
        parts.append(
            f"The spec_id for this spec is `{spec_id}`. Use it as the "
            f"prefix in all IDs (e.g. `{spec_id}-REQ-1`, "
            f"`TS-{spec_id}-1`).\n"
        )
    parts.append(f"## PRD\n\n{prd_text}\n")

    # Include prior artifacts as context
    if prior_artifacts:
        parts.append("## Previously Generated Artifacts\n")
        for name, content in prior_artifacts.items():
            parts.append(f"### {name}\n\n```json\n{json.dumps(content, indent=2)}\n```\n")

    if artifact_name == "requirements":
        parts.append(
            "## Additional Instructions\n\n"
            "### Introduction\n"
            "The `introduction` field is required — write a brief "
            "(1-2 sentence) description of the system being specified.\n\n"
            "### Titles\n"
            "Every requirement, correctness property, and execution path "
            "MUST have a non-empty `title` — a short human-readable label "
            '(e.g. "Event ingestion endpoint", "Bearer token '
            'authentication"). Empty titles fail validation.\n\n'
            "### Glossary completeness\n"
            "The `glossary` defines project-specific terms that a developer "
            "unfamiliar with this system would not know from general "
            "knowledge. Only use backticks around terms that genuinely need "
            "a contextual definition — every backtick-delimited term in "
            "`action`, `trigger`, `condition`, `state`, `error_condition`, "
            "`for_any`, and `invariant` fields MUST have a glossary entry. "
            "Missing entries fail validation.\n\n"
            "**Include** (backtick + define): project-specific identifiers "
            "like table or column names (`events`, `received_at`), "
            "environment variables (`AUTH_BEARER_TOKEN`), custom API "
            "endpoints (`POST /v1/events`), domain concepts with meaning "
            "specific to this system, and configuration values whose "
            "purpose is not self-evident.\n\n"
            "**Exclude** (use plain prose, no backticks): standard HTTP "
            "status codes (200, 404, 500), well-known protocols and "
            "formats (JSON, HTTP, UUID), standard ports, generic error "
            "response shapes, language keywords, file path conventions, "
            "log levels, and any term a working developer would already "
            "know. Write these in plain text without backticks.\n\n"
            "### Error handling\n"
            "The `error_handling` array maps error conditions to system "
            "behavior. Each entry needs:\n"
            "- `id`: format `{spec_id}-ERR-{N}`\n"
            "- `condition`: the error condition\n"
            "- `behavior`: what the system does in response\n"
            "- `requirement_id`: the requirement or edge case ID that "
            "specifies this behavior (e.g. `05-REQ-2.E1`)\n\n"
            "### Execution paths\n"
            "Each execution path traces a user-visible feature from entry "
            "point to observable side effect using logical actors (not "
            "module names). Every path must start at a user action and "
            "end at a concrete side effect. Steps need `actor` and "
            "`action` fields. At least two steps per path.\n\n"
            "### Return contracts\n"
            "Set `return_contract` to a non-null string on every criterion "
            "whose action produces an observable response — HTTP status "
            "codes, return values, response bodies, error messages. "
            "Only use null when the action has no caller-visible output "
            "(e.g. a background side effect). Concrete return contracts "
            "make implementation and testing significantly easier.\n\n"
            "### Correctness properties\n"
            "Each property's `validates` array must reference acceptance "
            "criterion IDs that exist in `requirements`.\n"
        )
    elif artifact_name == "test_spec":
        parts.append(
            "## Additional Instructions\n\n"
            "### Complete 1:1 coverage (mandatory)\n"
            "Cross-file validation enforces strict coverage. You MUST "
            "generate:\n"
            "- One `test_case` per acceptance criterion (requirement_id "
            "= the criterion ID, e.g. `05-REQ-1.1`)\n"
            "- One `edge_case_test` per edge case (requirement_id = the "
            "edge case ID, e.g. `05-REQ-1.E1`)\n"
            "- One `property_test` per correctness property (property_id "
            "= the property ID, e.g. `05-PROP-1`)\n"
            "- One `smoke_test` per execution path (execution_path_id "
            "= the path ID, e.g. `05-PATH-1`)\n\n"
            "Cross-check against the requirements artifact before "
            "submitting. Any missing coverage fails validation.\n\n"
            "### Test quality\n"
            "- Every test entry MUST have a non-empty `description` — "
            "a one-sentence explanation of what is being verified.\n"
            "- `assertion_pseudocode` must be concrete enough that a "
            "developer can translate it directly to test code. Include "
            "specific function calls, expected values, and assertions. "
            "Use language-agnostic pseudocode, not language-specific "
            "syntax.\n"
            "- `preconditions` must list all system state required before "
            "the test runs (database state, config, running services).\n"
            "- `expected` must describe concrete observable outcomes, not "
            "vague statements.\n\n"
            "### Coverage object\n"
            "The `coverage` object is computed by the validation library. "
            "Submit it with empty arrays: "
            '`{"requirements_covered": [], "properties_covered": [], '
            '"paths_covered": [], "gaps": []}`\n'
        )
    elif artifact_name == "tasks":
        parts.append(
            "## Additional Instructions\n\n"
            "### Titles\n"
            "Every task group and subtask MUST have a non-empty `title`. "
            "Empty titles fail validation.\n\n"
            "### Task group structure\n"
            "- The first task group (id=1) MUST have "
            '`kind: "tests"` — writes spec tests before implementation.\n'
            "- The last task group MUST have "
            '`kind: "wiring_verification"` — verifies end-to-end '
            "integration.\n"
            '- Groups in between use `kind: "standard"` or '
            '`kind: "checkpoint"` (for intermediate verification gates).\n'
            "- Exactly one wiring_verification group, always last.\n\n"
            "### Subtask IDs and verification\n"
            "- Subtask IDs use format `{group_id}.{N}` (e.g. `2.1`, "
            "`2.2`). Sequential within each group. Target 3-6 subtasks "
            "per group.\n"
            "- Every group MUST have exactly one verification subtask "
            "with ID `{group_id}.V` (e.g. `2.V`). The verification "
            "subtask MUST have a non-empty `checks` array with concrete "
            "criteria, for example:\n"
            '  - "Spec tests for this group pass: pytest -q tests/..."\n'
            '  - "All existing tests still pass: pytest -q"\n'
            '  - "No linter warnings introduced: ruff check"\n'
            '  - "Requirements 05-REQ-1.1, 05-REQ-1.2 acceptance '
            'criteria met"\n\n'
            "### Dependencies\n"
            "The `dependencies` array declares cross-spec dependencies "
            "only. Set `depends_on_spec` to the spec_id of the other "
            "spec. Intra-spec ordering is implicit from task group IDs — "
            "do not add self-referencing dependencies. Leave "
            "`dependencies` empty if the spec has no cross-spec "
            "dependencies.\n\n"
            "### Traceability\n"
            "The `traceability` array links requirements to test specs "
            "and tasks. One entry per (requirement_id, test_spec_id) "
            "pair. Set `test_path` to null (filled in at implementation "
            "time).\n\n"
            "Reference both requirement IDs and test IDs from the "
            "previously generated artifacts in subtask `requirement_refs` "
            "and `test_spec_refs` fields.\n\n"
            "### Wiring verification (last group)\n"
            "The final wiring_verification group must include subtasks "
            "that cover:\n"
            "1. Trace execution paths — verify each path's entry point "
            "calls the next function in the chain, no stubs remain.\n"
            "2. Verify return value propagation — confirm callers receive "
            "and use return values.\n"
            "3. Run smoke tests — all SMOKE tests pass with real "
            "components.\n"
            "4. Stub/dead-code audit — search for return None, pass in "
            "non-abstract methods, TODO, NotImplementedError.\n"
            "5. Cross-spec entry point verification — if paths start in "
            "another spec, confirm they are called from production code.\n"
        )

    parts.append(f"Use the submit_{artifact_name} tool to return the generated artifact.")

    return "\n".join(parts)


# ── repair ──────────────────────────────────────────────────────────


def repair_user_prompt(
    artifact_name: str,
    original_content: dict[str, Any],
    errors: list[str],
) -> str:
    """Return a user message asking the LLM to fix validation errors.

    Sends the original artifact content and a list of errors, asking
    the model to resubmit with corrections.
    """
    error_list = "\n".join(f"- {e}" for e in errors)
    return (
        f"The **{artifact_name}** artifact you generated has validation "
        f"errors. Fix them and resubmit using the same tool.\n\n"
        f"## Validation errors\n\n{error_list}\n\n"
        f"## Original artifact\n\n"
        f"```json\n{json.dumps(original_content, indent=2)}\n```\n\n"
        f"Fix all listed errors and resubmit using the "
        f"submit_{artifact_name} tool."
    )
