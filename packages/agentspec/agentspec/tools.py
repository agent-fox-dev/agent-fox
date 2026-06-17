"""Tool definitions for structured output via Anthropic tool use.

Defines the JSON-schema-based tool specs (submit_assessment,
submit_prd_update, submit_artifact) that constrain the model's output
shape when using tool_use for structured output.
"""

from __future__ import annotations

import copy
from typing import Any

from afspec import Requirements, Tasks, TestSpec  # type: ignore[import-untyped]

# ---------------------------------------------------------------------------
# Tool definition constants
# ---------------------------------------------------------------------------

SUBMIT_ASSESSMENT_TOOL: dict[str, Any] = {
    "name": "submit_assessment",
    "description": "Submit a structured assessment of the PRD quality.",
    "input_schema": {
        "type": "object",
        "required": ["quality", "summary", "gaps", "questions"],
        "properties": {
            "quality": {
                "type": "string",
                "enum": ["ready", "needs_refinement", "incomplete"],
                "description": "Overall quality verdict for the PRD.",
            },
            "summary": {
                "type": "string",
                "description": ("Brief summary of the PRD quality and main findings."),
            },
            "gaps": {
                "type": "array",
                "items": {"type": "string"},
                "description": ("List of identified gaps or weaknesses in the PRD."),
            },
            "questions": {
                "type": "array",
                "items": {
                    "type": "object",
                    "required": ["id", "text", "context", "options", "required"],
                    "properties": {
                        "id": {
                            "type": "string",
                            "description": "Unique identifier for the question.",
                        },
                        "text": {
                            "type": "string",
                            "description": "The question text.",
                        },
                        "context": {
                            "type": "string",
                            "description": "Why this question matters.",
                        },
                        "options": {
                            "type": "array",
                            "items": {"type": "string"},
                            "description": ("Suggested answer options (may be empty)."),
                        },
                        "required": {
                            "type": "boolean",
                            "description": ("Whether the user must answer this question."),
                        },
                    },
                },
                "description": ("Questions for the user to improve the PRD."),
            },
        },
    },
}

SUBMIT_PRD_UPDATE_TOOL: dict[str, Any] = {
    "name": "submit_prd_update",
    "description": ("Submit the updated PRD text incorporating user answers."),
    "input_schema": {
        "type": "object",
        "required": ["updated_prd"],
        "properties": {
            "updated_prd": {
                "type": "string",
                "description": ("The full updated PRD text (body only, no frontmatter)."),
            },
        },
    },
}

# ---------------------------------------------------------------------------
# Artifact model mapping
# ---------------------------------------------------------------------------

_ARTIFACT_MODELS: dict[str, Any] = {
    "requirements": Requirements,
    "test_spec": TestSpec,
    "tasks": Tasks,
}


# ---------------------------------------------------------------------------
# JSON Schema inlining
# ---------------------------------------------------------------------------


def _inline_refs(schema: dict[str, Any]) -> dict[str, Any]:
    """Resolve all ``$ref`` / ``$defs`` in a JSON Schema to produce a
    self-contained schema suitable for the Anthropic tool-use API
    (which does not support ``$ref``).
    """
    schema = copy.deepcopy(schema)
    defs = schema.pop("$defs", {})

    def _resolve(node: Any) -> Any:
        if isinstance(node, dict):
            if "$ref" in node:
                ref_path = node["$ref"]  # e.g. "#/$defs/Criterion"
                ref_name = ref_path.rsplit("/", 1)[-1]
                resolved = copy.deepcopy(defs[ref_name])
                return _resolve(resolved)
            return {k: _resolve(v) for k, v in node.items()}
        if isinstance(node, list):
            return [_resolve(item) for item in node]
        return node

    result: dict[str, Any] = _resolve(schema)
    return result


def _clean_schema(schema: dict[str, Any]) -> dict[str, Any]:
    """Remove Pydantic metadata noise while preserving field docs.

    Strips:
    - Pydantic-generated ``title`` labels (class names like "Requirement")
    - ``default`` values (the LLM should fill everything explicitly)
    - The ``$schema`` alias property

    Preserves:
    - ``description`` fields (critical for the LLM to understand field purpose)
    - ``title`` when it's a data property definition (dict value, not string label)
    """
    schema = _inline_refs(schema)

    def _strip(node: Any) -> Any:
        if isinstance(node, dict):
            if isinstance(node.get("title"), str) and "type" in node:
                node.pop("title", None)
            node.pop("default", None)
            props = node.get("properties", {})
            props.pop("$schema", None)
            for v in node.values():
                _strip(v)
        elif isinstance(node, list):
            for item in node:
                _strip(item)

    _strip(schema)
    return schema


# ---------------------------------------------------------------------------
# Public functions
# ---------------------------------------------------------------------------


def assessment_tools() -> list[dict[str, Any]]:
    """Return tool definitions for PRD assessment.

    Defines the submit_assessment tool.
    """
    return [SUBMIT_ASSESSMENT_TOOL]


def refinement_tools() -> list[dict[str, Any]]:
    """Return tool definitions for PRD refinement.

    Defines submit_prd_update and submit_assessment tools.
    """
    return [SUBMIT_PRD_UPDATE_TOOL, SUBMIT_ASSESSMENT_TOOL]


def artifact_tool(artifact_name: str) -> list[dict[str, Any]]:
    """Return tool definition for generating one artifact.

    Produces a per-artifact tool (``submit_requirements``,
    ``submit_test_spec``, or ``submit_tasks``) whose ``content``
    property embeds the Pydantic model's JSON schema so the LLM
    is structurally constrained.

    Args:
        artifact_name: One of ``"requirements"``, ``"test_spec"``,
            ``"tasks"``.
    """
    model_cls = _ARTIFACT_MODELS[artifact_name]
    content_schema = _clean_schema(model_cls.model_json_schema())  # type: ignore[union-attr]

    tool_name = f"submit_{artifact_name}"
    return [
        {
            "name": tool_name,
            "description": (f"Submit the generated {artifact_name} artifact content."),
            "input_schema": {
                "type": "object",
                "required": ["content"],
                "properties": {
                    "content": content_schema,
                },
            },
        }
    ]
