"""Shared fixtures for agent pipeline tests.

Provides mock infrastructure and response builders used across
test_agent.py, test_session_agent.py, and smoke tests.

Since SpecAgent delegates to ``ai_call()`` from ``agentfox.core.client``,
tests mock ``ai_call`` rather than an Anthropic client directly.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from agentspec.session import Assessment, Question

# ---------------------------------------------------------------------------
# Fake Anthropic response types
# ---------------------------------------------------------------------------


@dataclass
class FakeToolUseBlock:
    """Simulates an anthropic.types.ToolUseBlock."""

    type: str = "tool_use"
    id: str = "tool_call_1"
    name: str = ""
    input: dict[str, Any] = field(default_factory=dict)


@dataclass
class FakeTextBlock:
    """Simulates an anthropic.types.TextBlock."""

    type: str = "text"
    text: str = ""


@dataclass
class FakeUsage:
    """Simulates an anthropic.types.Usage."""

    input_tokens: int = 100
    output_tokens: int = 200


@dataclass
class FakeMessage:
    """Simulates an anthropic.types.Message."""

    id: str = "msg_test"
    type: str = "message"
    role: str = "assistant"
    content: list[Any] = field(default_factory=list)
    model: str = "claude-sonnet-4-6"
    stop_reason: str = "tool_use"
    usage: FakeUsage = field(default_factory=FakeUsage)


# ---------------------------------------------------------------------------
# Response builders
# ---------------------------------------------------------------------------


def make_assessment_response(
    quality: str = "needs_refinement",
    summary: str = "Needs work",
    gaps: list[str] | None = None,
    questions: list[dict[str, Any]] | None = None,
) -> FakeMessage:
    """Build a fake API response containing a submit_assessment tool call."""
    if gaps is None:
        gaps = ["Missing Goals"]
    if questions is None:
        questions = [
            {
                "id": "q1",
                "text": "What are the goals?",
                "context": "Goals section is missing",
                "options": [],
                "required": True,
            }
        ]
    return FakeMessage(
        content=[
            FakeToolUseBlock(
                name="submit_assessment",
                input={
                    "quality": quality,
                    "summary": summary,
                    "gaps": gaps,
                    "questions": questions,
                },
            )
        ]
    )


def make_refinement_response(
    updated_prd: str = "# Updated PRD\n## Goals\n1. Build REST API",
    quality: str = "ready",
    summary: str = "PRD is now ready",
    gaps: list[str] | None = None,
    questions: list[dict[str, Any]] | None = None,
) -> FakeMessage:
    """Build a fake response with submit_prd_update and submit_assessment."""
    if gaps is None:
        gaps = []
    if questions is None:
        questions = []
    return FakeMessage(
        content=[
            FakeToolUseBlock(
                id="tool_call_1",
                name="submit_prd_update",
                input={"updated_prd": updated_prd},
            ),
            FakeToolUseBlock(
                id="tool_call_2",
                name="submit_assessment",
                input={
                    "quality": quality,
                    "summary": summary,
                    "gaps": gaps,
                    "questions": questions,
                },
            ),
        ]
    )


def make_artifact_response(
    artifact_name: str,
    content: dict[str, Any] | None = None,
) -> FakeMessage:
    """Build a fake API response containing a per-artifact tool call.

    The tool name is ``submit_{artifact_name}`` (e.g. ``submit_requirements``).
    """
    if content is None:
        content = {"placeholder": True}
    tool_name = f"submit_{artifact_name}"
    return FakeMessage(
        content=[
            FakeToolUseBlock(
                name=tool_name,
                input={
                    "content": content,
                },
            )
        ]
    )


def make_text_only_response(text: str = "I don't know how to use tools") -> FakeMessage:
    """Build a fake API response with only text content (no tool calls)."""
    return FakeMessage(
        content=[FakeTextBlock(text=text)],
        stop_reason="end_turn",
    )


# ---------------------------------------------------------------------------
# ai_call mock helper
# ---------------------------------------------------------------------------


def _make_ai_call_mock() -> AsyncMock:
    """Create an AsyncMock for ``agentfox.core.client.ai_call``.

    By default returns ``(None, FakeMessage())``. Tests set
    ``return_value`` or ``side_effect`` on the mock with tuples of
    ``(text_or_none, FakeMessage)``.
    """
    mock = AsyncMock()
    mock.return_value = (None, FakeMessage())
    return mock


def _ai_call_response(response: FakeMessage) -> tuple[None, FakeMessage]:
    """Wrap a FakeMessage in the ``(text, response)`` tuple that ``ai_call`` returns."""
    return (None, response)


def _ai_call_side_effects(responses_or_errors: list[Any]) -> list[Any]:
    """Convert a list of FakeMessages and/or Exceptions into side_effect entries for ai_call mock.

    FakeMessages are wrapped in ``(None, msg)`` tuples.
    Exceptions are left as-is (they will be raised by the mock).
    """
    result: list[Any] = []
    for item in responses_or_errors:
        if isinstance(item, BaseException):
            result.append(item)
        else:
            result.append((None, item))
    return result


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def mock_ai_call():
    """Provide a mock of ``agentfox.core.client.ai_call`` for SpecAgent tests.

    Patches ``ai_call`` at the import location used by ``SpecAgent._call_api``.
    Tests set ``return_value`` or ``side_effect`` on the returned mock.

    Usage::

        async def test_something(mock_ai_call):
            mock_ai_call.return_value = _ai_call_response(make_assessment_response())
            agent = SpecAgent("STANDARD")
            result = await agent.assess_prd("# PRD", "spec")
    """
    with patch("agentfox.core.client.ai_call", new_callable=AsyncMock) as mock:
        yield mock


@pytest.fixture
def mock_client() -> MagicMock:
    """Provide a mock Anthropic client (legacy, for backward compatibility).

    Prefer ``mock_ai_call`` for new tests.
    """
    return _make_mock_client()


def _make_mock_client() -> MagicMock:
    """Create a MagicMock Anthropic client with mock messages.stream.

    Tests can set ``return_value`` / ``side_effect`` on ``stream`` and
    assert against its ``call_count`` / ``call_args``.
    """
    client = MagicMock()
    client.messages = MagicMock()
    client.messages.stream = MagicMock()
    return client


@pytest.fixture
def sample_assessment() -> Assessment:
    """Provide a sample Assessment for tests."""
    return Assessment(
        quality="needs_refinement",
        summary="Needs work",
        gaps=["No goals"],
        questions=[
            Question(
                id="q1",
                text="What are the goals?",
                context="Goals section is missing",
                options=[],
                required=True,
            ),
        ],
    )


@pytest.fixture
def sample_questions() -> list[Question]:
    """Provide sample Question objects."""
    return [
        Question(
            id="q1",
            text="What are the goals?",
            context="Goals section is missing",
            options=[],
            required=True,
        ),
        Question(
            id="q2",
            text="Who are the users?",
            context="User persona not defined",
            options=["Developers", "End users"],
            required=False,
        ),
    ]


# ---------------------------------------------------------------------------
# Sample artifact JSON — valid for afspec Pydantic models
# ---------------------------------------------------------------------------

SAMPLE_REQUIREMENTS_JSON: dict[str, Any] = {
    "spec_id": "test-03",
    "spec_name": "agent_pipeline",
    "schema_version": 1,
    "introduction": "Requirements for test spec.",
    "glossary": {},
    "requirements": [],
    "correctness_properties": [],
    "execution_paths": [],
    "error_handling": [],
}

SAMPLE_TEST_SPEC_JSON: dict[str, Any] = {
    "spec_id": "test-03",
    "spec_name": "agent_pipeline",
    "schema_version": 1,
    "test_cases": [],
    "property_tests": [],
    "edge_case_tests": [],
    "smoke_tests": [],
    "coverage": {
        "requirements_covered": [],
        "properties_covered": [],
        "paths_covered": [],
        "gaps": [],
    },
}

SAMPLE_TASKS_JSON: dict[str, Any] = {
    "spec_id": "test-03",
    "spec_name": "agent_pipeline",
    "schema_version": 1,
    "test_commands": {
        "spec_tests": "pytest -q",
        "all_tests": "pytest -q",
        "linter": "ruff check",
    },
    "dependencies": [],
    "task_groups": [],
    "traceability": [],
}


# ---------------------------------------------------------------------------
# Anthropic API error factories
# ---------------------------------------------------------------------------


def _make_httpx_response(status_code: int) -> Any:
    """Create a real httpx.Response for constructing Anthropic SDK errors."""
    import httpx

    request = httpx.Request("POST", "https://api.anthropic.com/v1/messages")
    return httpx.Response(
        status_code=status_code,
        headers={"content-type": "application/json"},
        json={"error": {"message": "test error", "type": "test_error"}},
        request=request,
    )


def make_rate_limit_error() -> Exception:
    """Create an anthropic.RateLimitError (429)."""
    from anthropic import RateLimitError

    return RateLimitError(
        message="rate limited",
        response=_make_httpx_response(429),
        body=None,
    )


def make_internal_server_error() -> Exception:
    """Create an anthropic.InternalServerError (500)."""
    from anthropic import InternalServerError

    return InternalServerError(
        message="server error",
        response=_make_httpx_response(500),
        body=None,
    )


def make_bad_request_error() -> Exception:
    """Create an anthropic.BadRequestError (400)."""
    from anthropic import BadRequestError

    return BadRequestError(
        message="bad request",
        response=_make_httpx_response(400),
        body=None,
    )


def make_connection_error() -> Exception:
    """Create an anthropic.APIConnectionError."""
    import httpx
    from anthropic import APIConnectionError

    request = httpx.Request("POST", "https://api.anthropic.com/v1/messages")
    return APIConnectionError(
        message="Connection timed out",
        request=request,
    )


def make_overloaded_error() -> Exception:
    """Create an APIStatusError for 529 Overloaded."""
    from anthropic import APIStatusError

    return APIStatusError(
        message="overloaded",
        response=_make_httpx_response(529),
        body=None,
    )


def make_auth_error() -> Exception:
    """Create an anthropic.AuthenticationError (401)."""
    from anthropic import AuthenticationError

    return AuthenticationError(
        message="invalid api key",
        response=_make_httpx_response(401),
        body=None,
    )
