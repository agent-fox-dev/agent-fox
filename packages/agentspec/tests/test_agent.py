"""Tests for agentspec.agent — SpecAgent core methods.

Covers TS-03-1 through TS-03-14 (assessment, refinement, generation),
TS-03-21 through TS-03-26 (retry and error handling),
TS-03-E1 through TS-03-E12 (edge cases),
TS-03-P1 through TS-03-P4 (property tests).

All tests mock ``ai_call()``; no real API calls are made.
"""

from __future__ import annotations

import pytest
from agentspec.agent import SpecAgent
from agentspec.errors import AgentError, AgentSpecError
from agentspec.session import Assessment
from conftest_agent import (
    SAMPLE_REQUIREMENTS_JSON,
    SAMPLE_TASKS_JSON,
    SAMPLE_TEST_SPEC_JSON,
    _ai_call_response,
    _ai_call_side_effects,
    make_artifact_response,
    make_assessment_response,
    make_auth_error,
    make_bad_request_error,
    make_connection_error,
    make_internal_server_error,
    make_overloaded_error,
    make_rate_limit_error,
    make_refinement_response,
    make_text_only_response,
)
from hypothesis import given, settings
from hypothesis import strategies as st

# ===================================================================
# TS-03-1: assess_prd returns Assessment with valid quality
# ===================================================================


@pytest.mark.asyncio
async def test_assess_prd_returns_assessment_with_valid_quality(mock_ai_call):
    """TS-03-1: assess_prd sends the PRD to the API and returns an
    Assessment with a valid quality value."""
    mock_ai_call.return_value = _ai_call_response(
        make_assessment_response(
            quality="needs_refinement",
            summary="Needs work",
            gaps=["Missing Goals"],
            questions=[
                {
                    "id": "q1",
                    "text": "What are the goals?",
                    "context": "Goals section is missing",
                    "options": [],
                    "required": True,
                }
            ],
        )
    )
    agent = SpecAgent("STANDARD")
    result = await agent.assess_prd("# My PRD\n\n## Intent\nDo things.", "my_spec")

    assert isinstance(result, Assessment)
    assert result.quality == "needs_refinement"
    assert mock_ai_call.call_count == 1


# ===================================================================
# TS-03-2: Assessment contains summary
# ===================================================================


@pytest.mark.asyncio
async def test_assessment_contains_summary(mock_ai_call):
    """TS-03-2: The returned Assessment has a non-empty summary."""
    mock_ai_call.return_value = _ai_call_response(
        make_assessment_response(summary="The PRD is incomplete.")
    )
    agent = SpecAgent("STANDARD")
    result = await agent.assess_prd("# PRD content", "test_spec")

    assert result.summary == "The PRD is incomplete."
    assert len(result.summary) > 0


# ===================================================================
# TS-03-3: Assessment contains gaps list
# ===================================================================


@pytest.mark.asyncio
async def test_assessment_contains_gaps(mock_ai_call):
    """TS-03-3: The returned Assessment has a gaps list."""
    mock_ai_call.return_value = _ai_call_response(
        make_assessment_response(gaps=["No Goals section", "Background is vague"])
    )
    agent = SpecAgent("STANDARD")
    result = await agent.assess_prd("# PRD content", "test_spec")

    assert result.gaps == ["No Goals section", "Background is vague"]


# ===================================================================
# TS-03-4: Non-ready assessment has questions
# ===================================================================


@pytest.mark.asyncio
async def test_non_ready_assessment_has_questions(mock_ai_call):
    """TS-03-4: When quality is not 'ready', questions is non-empty."""
    q1 = {
        "id": "q1",
        "text": "What are the goals?",
        "context": "Goals section is missing",
        "options": [],
        "required": True,
    }
    mock_ai_call.return_value = _ai_call_response(
        make_assessment_response(quality="needs_refinement", questions=[q1])
    )
    agent = SpecAgent("STANDARD")
    result = await agent.assess_prd("# PRD", "test_spec")

    assert len(result.questions) > 0
    assert result.questions[0].id == "q1"
    assert result.questions[0].text == "What are the goals?"
    assert result.questions[0].required is True


# ===================================================================
# TS-03-5: Ready assessment may have empty questions
# ===================================================================


@pytest.mark.asyncio
async def test_ready_assessment_empty_questions(mock_ai_call):
    """TS-03-5: When quality is 'ready', an empty questions list is valid."""
    mock_ai_call.return_value = _ai_call_response(
        make_assessment_response(
            quality="ready",
            summary="PRD is complete",
            gaps=[],
            questions=[],
        )
    )
    agent = SpecAgent("STANDARD")
    result = await agent.assess_prd("# PRD\n## Intent\n## Goals\n## Non-Goals\n## Background", "test_spec")

    assert result.quality == "ready"
    assert result.questions == []


# ===================================================================
# TS-03-6: refine_prd returns updated PRD and new assessment
# ===================================================================


@pytest.mark.asyncio
async def test_refine_prd_returns_updated_prd_and_assessment(mock_ai_call, sample_assessment):
    """TS-03-6: refine_prd sends answers and returns an updated PRD
    with a new assessment."""
    mock_ai_call.return_value = _ai_call_response(
        make_refinement_response(
            updated_prd="# Updated PRD\n## Goals\n1. Build REST API",
            quality="ready",
        )
    )
    agent = SpecAgent("STANDARD")
    updated, assessment = await agent.refine_prd("# Original PRD", {"q1": "Build a REST API"}, sample_assessment)

    assert "REST API" in updated
    assert isinstance(assessment, Assessment)
    assert assessment.quality == "ready"


# ===================================================================
# TS-03-7: refine_prd answers dict maps question IDs to strings
# ===================================================================


@pytest.mark.asyncio
async def test_refine_prd_answers_in_user_message(mock_ai_call, sample_questions):
    """TS-03-7: The answers dict maps question IDs to string answers
    and these appear in the user message sent to the API."""
    prev = Assessment(
        quality="needs_refinement",
        summary="",
        gaps=[],
        questions=sample_questions,
    )
    mock_ai_call.return_value = _ai_call_response(
        make_refinement_response(updated_prd="Updated", quality="ready")
    )
    agent = SpecAgent("STANDARD")
    await agent.refine_prd("# PRD", {"q1": "A1", "q2": "A2"}, prev)

    call_kwargs = mock_ai_call.call_args.kwargs
    user_msg = call_kwargs["messages"][-1]["content"]
    assert "q1" in user_msg and "A1" in user_msg
    assert "q2" in user_msg and "A2" in user_msg


# ===================================================================
# TS-03-8: refine_prd preserves frontmatter
# ===================================================================


@pytest.mark.asyncio
async def test_refine_prd_returns_body_only(mock_ai_call, sample_assessment):
    """TS-03-8: The updated PRD from the agent contains body-only content.
    The caller (SpecSession) is responsible for re-attaching frontmatter."""
    mock_ai_call.return_value = _ai_call_response(
        make_refinement_response(
            updated_prd="## Intent\nUpdated body", quality="ready"
        )
    )
    agent = SpecAgent("STANDARD")
    updated, _ = await agent.refine_prd(
        "---\nspec_id: 01\n---\n## Intent\nOriginal",
        {"q1": "answer"},
        sample_assessment,
    )

    assert "Updated body" in updated


# ===================================================================
# TS-03-9: generate_artifacts produces three artifacts in order
# ===================================================================


@pytest.mark.asyncio
async def test_generate_three_artifacts_in_order(mock_ai_call):
    """TS-03-9: generate_artifacts makes three API calls and returns
    all three artifacts."""
    mock_ai_call.side_effect = _ai_call_side_effects([
        make_artifact_response("requirements", SAMPLE_REQUIREMENTS_JSON),
        make_artifact_response("test_spec", SAMPLE_TEST_SPEC_JSON),
        make_artifact_response("tasks", SAMPLE_TASKS_JSON),
    ])
    agent = SpecAgent("STANDARD")

    result = await agent.generate_artifacts("# Accepted PRD", "03", "agent_pipeline")

    assert set(result.keys()) == {"requirements", "test_spec", "tasks"}
    assert mock_ai_call.call_count == 3


# ===================================================================
# TS-03-10: generate_artifacts returns afspec model instances
# ===================================================================


@pytest.mark.asyncio
async def test_generate_returns_model_instances(mock_ai_call):
    """TS-03-10: Each artifact value is an afspec Pydantic model."""
    from afspec import Requirements, Tasks, TestSpec

    mock_ai_call.side_effect = _ai_call_side_effects([
        make_artifact_response("requirements", SAMPLE_REQUIREMENTS_JSON),
        make_artifact_response("test_spec", SAMPLE_TEST_SPEC_JSON),
        make_artifact_response("tasks", SAMPLE_TASKS_JSON),
    ])
    agent = SpecAgent("STANDARD")

    result = await agent.generate_artifacts("# PRD", "03", "test")

    assert isinstance(result["requirements"], Requirements)
    assert isinstance(result["test_spec"], TestSpec)
    assert isinstance(result["tasks"], Tasks)


# ===================================================================
# TS-03-11: Each artifact validated before next generation
# ===================================================================


@pytest.mark.asyncio
async def test_validate_before_next_generation(mock_ai_call):
    """TS-03-11: Each artifact is validated (via Pydantic construction)
    before the next artifact is generated."""
    call_log: list[str] = []
    artifact_order = ["requirements", "test_spec", "tasks"]
    generate_counter = 0

    samples = {
        "requirements": SAMPLE_REQUIREMENTS_JSON,
        "test_spec": SAMPLE_TEST_SPEC_JSON,
        "tasks": SAMPLE_TASKS_JSON,
    }

    async def tracking_ai_call(**kwargs):
        nonlocal generate_counter
        name = artifact_order[generate_counter]
        generate_counter += 1
        call_log.append(f"generate:{name}")
        return _ai_call_response(make_artifact_response(name, samples[name]))

    mock_ai_call.side_effect = tracking_ai_call
    agent = SpecAgent("STANDARD")

    await agent.generate_artifacts("# PRD", "03", "test")

    # All three generated in order
    assert call_log == [
        "generate:requirements",
        "generate:test_spec",
        "generate:tasks",
    ]
    assert generate_counter == 3


# ===================================================================
# TS-03-12: Validation failure aborts generation
# ===================================================================


@pytest.mark.asyncio
async def test_validation_failure_aborts_generation(mock_ai_call):
    """TS-03-12: Generation stops and raises AgentError if an artifact
    fails Pydantic validation."""
    # Return content with a field of the wrong type
    invalid_content = {"glossary": "not_a_dict"}
    mock_ai_call.return_value = _ai_call_response(
        make_artifact_response("requirements", invalid_content)
    )
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError, match="requirements.*validation"):
        await agent.generate_artifacts("# PRD", "03", "test")

    assert mock_ai_call.call_count == 1


# ===================================================================
# TS-03-13: test_spec generation includes requirements context
# ===================================================================


@pytest.mark.asyncio
async def test_test_spec_includes_requirements_context(mock_ai_call):
    """TS-03-13: The test_spec generation prompt includes the generated
    requirements content."""
    mock_ai_call.side_effect = _ai_call_side_effects([
        make_artifact_response("requirements", SAMPLE_REQUIREMENTS_JSON),
        make_artifact_response("test_spec", SAMPLE_TEST_SPEC_JSON),
        make_artifact_response("tasks", SAMPLE_TASKS_JSON),
    ])
    agent = SpecAgent("STANDARD")

    await agent.generate_artifacts("# PRD", "03", "test")

    # The second API call's user message should contain requirements content
    second_call = mock_ai_call.call_args_list[1]
    user_msg = second_call.kwargs["messages"][-1]["content"]
    assert "requirements" in user_msg.lower()


# ===================================================================
# TS-03-14: tasks generation includes both prior artifacts
# ===================================================================


@pytest.mark.asyncio
async def test_tasks_includes_both_prior_artifacts(mock_ai_call):
    """TS-03-14: The tasks generation prompt includes both requirements
    and test_spec content."""
    mock_ai_call.side_effect = _ai_call_side_effects([
        make_artifact_response("requirements", SAMPLE_REQUIREMENTS_JSON),
        make_artifact_response("test_spec", SAMPLE_TEST_SPEC_JSON),
        make_artifact_response("tasks", SAMPLE_TASKS_JSON),
    ])
    agent = SpecAgent("STANDARD")

    await agent.generate_artifacts("# PRD", "03", "test")

    # The third API call's user message should contain both prior artifacts
    third_call = mock_ai_call.call_args_list[2]
    user_msg = third_call.kwargs["messages"][-1]["content"]
    assert "requirements" in user_msg.lower()
    assert "test_spec" in user_msg.lower()


# ===================================================================
# TS-03-21: _call_api delegates to ai_call and wraps errors
# ===================================================================


@pytest.mark.asyncio
async def test_call_api_delegates_to_ai_call(mock_ai_call):
    """TS-03-21: _call_api delegates to ai_call with correct parameters."""
    mock_ai_call.return_value = _ai_call_response(make_assessment_response())
    agent = SpecAgent("STANDARD")

    await agent._call_api(
        messages=[{"role": "user", "content": "test"}],
        tools=[{"name": "test_tool"}],
        system="system prompt",
    )

    mock_ai_call.assert_called_once()
    call_kwargs = mock_ai_call.call_args.kwargs
    assert call_kwargs["model_tier"] == "STANDARD"
    assert call_kwargs["messages"] == [{"role": "user", "content": "test"}]
    assert call_kwargs["system"] == "system prompt"
    assert call_kwargs["context"] == "spec-generation"
    assert call_kwargs["tools"] == [{"name": "test_tool"}]
    assert call_kwargs["tool_choice"] == {"type": "any"}


# ===================================================================
# TS-03-22: _call_api omits tools/tool_choice when tools is empty
# ===================================================================


@pytest.mark.asyncio
async def test_call_api_omits_tools_when_empty(mock_ai_call):
    """TS-03-22: _call_api does not pass tools/tool_choice when tools is empty."""
    mock_ai_call.return_value = _ai_call_response(make_assessment_response())
    agent = SpecAgent("STANDARD")

    await agent._call_api(
        messages=[{"role": "user", "content": "test"}],
        tools=[],
    )

    call_kwargs = mock_ai_call.call_args.kwargs
    assert "tools" not in call_kwargs
    assert "tool_choice" not in call_kwargs


# ===================================================================
# TS-03-23: AgentError after ai_call raises retryable error
# ===================================================================


@pytest.mark.asyncio
async def test_agent_error_on_rate_limit(mock_ai_call):
    """TS-03-23: AgentError is raised when ai_call raises RateLimitError
    (after its own retries are exhausted)."""
    mock_ai_call.side_effect = make_rate_limit_error()
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError) as exc_info:
        await agent._call_api(
            messages=[{"role": "user", "content": "test"}],
            tools=[],
        )

    assert exc_info.value.category == "rate_limit"
    assert exc_info.value.retryable is True
    assert exc_info.value.http_status == 429
    assert exc_info.value.__cause__ is not None


# ===================================================================
# TS-03-24: No retry on 4xx (non-429) — wrapped as AgentError
# ===================================================================


@pytest.mark.asyncio
async def test_agent_error_on_bad_request(mock_ai_call):
    """TS-03-24: AgentError is raised with correct category for 4xx errors."""
    mock_ai_call.side_effect = make_bad_request_error()
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError) as exc_info:
        await agent._call_api(
            messages=[{"role": "user", "content": "test"}],
            tools=[],
        )

    assert exc_info.value.category == "input"
    assert exc_info.value.retryable is False
    assert exc_info.value.http_status == 400


# ===================================================================
# TS-03-25: AgentError inherits AgentSpecError
# ===================================================================


def test_agent_error_inherits_agentspec_error():
    """TS-03-25: AgentError is a subclass of AgentSpecError with __cause__."""
    assert issubclass(AgentError, AgentSpecError)
    original = ValueError("bad response")
    err = AgentError("parsing failed")
    err.__cause__ = original
    assert err.__cause__ is original
    assert err.detail == "parsing failed"


# ===================================================================
# TS-03-26: AgentError on unparseable response
# ===================================================================


@pytest.mark.asyncio
async def test_agent_error_on_unparseable_response(mock_ai_call):
    """TS-03-26: AgentError is raised when the response has no tool_use blocks."""
    mock_ai_call.return_value = _ai_call_response(
        make_text_only_response("I don't know how to use tools")
    )
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError, match="structured output"):
        await agent.assess_prd("# PRD", "test")


# ===================================================================
# TS-03-E1: Empty PRD raises AgentError
# ===================================================================


@pytest.mark.asyncio
async def test_empty_prd_raises_agent_error(mock_ai_call):
    """TS-03-E1: assess_prd raises AgentError for empty PRD without API call."""
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError):
        await agent.assess_prd("", "test")

    with pytest.raises(AgentError):
        await agent.assess_prd("   ", "test")

    assert mock_ai_call.call_count == 0


# ===================================================================
# TS-03-E2: Malformed assessment tool response
# ===================================================================


@pytest.mark.asyncio
async def test_malformed_assessment_tool_response(mock_ai_call):
    """TS-03-E2: AgentError when tool response is missing required fields."""
    from conftest_agent import FakeMessage, FakeToolUseBlock

    # Return tool_use with missing summary, gaps, questions
    mock_ai_call.return_value = _ai_call_response(
        FakeMessage(
            content=[
                FakeToolUseBlock(
                    name="submit_assessment",
                    input={"quality": "ready"},  # missing summary, gaps, questions
                )
            ]
        )
    )
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError, match="summary|fields|missing|invalid"):
        await agent.assess_prd("# PRD", "test")


# ===================================================================
# TS-03-E3: No tool_use in response
# ===================================================================


@pytest.mark.asyncio
async def test_no_tool_use_in_response(mock_ai_call):
    """TS-03-E3: AgentError when model returns only text, no tool call."""
    mock_ai_call.return_value = _ai_call_response(
        make_text_only_response("Here is my assessment...")
    )
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError, match="structured output"):
        await agent.assess_prd("# PRD", "test")


# ===================================================================
# TS-03-E4: Empty answers in refine_prd
# ===================================================================


@pytest.mark.asyncio
async def test_empty_answers_raises_agent_error(mock_ai_call, sample_assessment):
    """TS-03-E4: AgentError when answers dict is empty."""
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError, match="no answers|empty|answers"):
        await agent.refine_prd("# PRD", {}, sample_assessment)

    assert mock_ai_call.call_count == 0


# ===================================================================
# TS-03-E5: Unrecognized question IDs in answers
# ===================================================================


@pytest.mark.asyncio
async def test_unrecognized_question_ids_raises_agent_error(mock_ai_call, sample_assessment):
    """TS-03-E5: AgentError when answer IDs don't match assessment questions."""
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError, match="q99"):
        await agent.refine_prd("# PRD", {"q99": "answer"}, sample_assessment)


# ===================================================================
# TS-03-E6: Missing assessment in refinement response
# ===================================================================


@pytest.mark.asyncio
async def test_missing_assessment_in_refinement_response(mock_ai_call, sample_assessment):
    """TS-03-E6: AgentError when agent returns PRD update but no assessment."""
    from conftest_agent import FakeMessage, FakeToolUseBlock

    # Return submit_prd_update but NOT submit_assessment
    mock_ai_call.return_value = _ai_call_response(
        FakeMessage(
            content=[
                FakeToolUseBlock(
                    name="submit_prd_update",
                    input={"updated_prd": "new prd"},
                )
            ]
        )
    )
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError):
        await agent.refine_prd("# PRD", {"q1": "a"}, sample_assessment)


# ===================================================================
# TS-03-E7: Empty PRD for generation
# ===================================================================


@pytest.mark.asyncio
async def test_empty_prd_generate_raises_agent_error(mock_ai_call):
    """TS-03-E7: generate_artifacts raises AgentError for empty PRD."""
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError):
        await agent.generate_artifacts("", "03", "test")

    assert mock_ai_call.call_count == 0


# ===================================================================
# TS-03-E8: Artifact tool not invoked by model
# ===================================================================


@pytest.mark.asyncio
async def test_artifact_tool_not_invoked(mock_ai_call):
    """TS-03-E8: AgentError when the model doesn't call submit_artifact."""
    mock_ai_call.return_value = _ai_call_response(
        make_text_only_response("Here is the artifact content...")
    )
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError):
        await agent.generate_artifacts("# PRD", "03", "test")


# ===================================================================
# TS-03-E9: Validation failure with detailed error
# ===================================================================


@pytest.mark.asyncio
async def test_schema_validation_error_detail(mock_ai_call):
    """TS-03-E9: AgentError includes artifact name and validation details."""
    # Provide content that will fail Pydantic validation
    invalid_content = {"introduction": 42}  # wrong type
    mock_ai_call.return_value = _ai_call_response(
        make_artifact_response("requirements", invalid_content)
    )
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError, match="requirements.*validation"):
        await agent.generate_artifacts("# PRD", "03", "test")


# ===================================================================
# TS-03-E11: Connection error wrapped as AgentError
# ===================================================================


@pytest.mark.asyncio
async def test_connection_error_wrapped(mock_ai_call):
    """TS-03-E11: Connection errors from ai_call are wrapped as AgentError."""
    mock_ai_call.side_effect = make_connection_error()
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError) as exc_info:
        await agent._call_api(
            messages=[{"role": "user", "content": "test"}],
            tools=[],
        )

    assert exc_info.value.category == "transient"
    assert exc_info.value.retryable is True


# ===================================================================
# TS-03-E12: Server error wrapped as AgentError
# ===================================================================


@pytest.mark.asyncio
async def test_server_error_wrapped(mock_ai_call):
    """TS-03-E12: Server errors from ai_call are wrapped as AgentError."""
    mock_ai_call.side_effect = make_internal_server_error()
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError) as exc_info:
        await agent._call_api(
            messages=[{"role": "user", "content": "test"}],
            tools=[],
        )

    assert exc_info.value.category == "transient"
    assert exc_info.value.retryable is True


# ===================================================================
# TS-03-P1: Property — Assessment quality enum is valid
# ===================================================================


class TestPropertyQualityEnum:
    """Property test: quality field must be one of the valid enum values."""

    @given(quality=st.sampled_from(["ready", "needs_refinement", "incomplete"]))
    @settings(max_examples=10)
    def test_property_valid_quality_accepted(self, quality: str) -> None:
        """TS-03-P1: Valid quality values are accepted by _parse_assessment."""
        agent = SpecAgent.__new__(SpecAgent)
        tool_input = {
            "quality": quality,
            "summary": "ok",
            "gaps": [],
            "questions": (
                []
                if quality == "ready"
                else [
                    {
                        "id": "q1",
                        "text": "Q?",
                        "context": "ctx",
                        "options": [],
                        "required": True,
                    }
                ]
            ),
        }
        assessment = agent._parse_assessment(tool_input)
        assert assessment.quality == quality

    @given(
        quality=st.text(min_size=1, max_size=30).filter(lambda q: q not in ["ready", "needs_refinement", "incomplete"])
    )
    @settings(max_examples=10)
    def test_property_invalid_quality_rejected(self, quality: str) -> None:
        """TS-03-P1: Invalid quality values are rejected by _parse_assessment."""
        agent = SpecAgent.__new__(SpecAgent)
        tool_input = {
            "quality": quality,
            "summary": "ok",
            "gaps": [],
            "questions": [],
        }
        with pytest.raises(AgentError):
            agent._parse_assessment(tool_input)


# ===================================================================
# TS-03-P2: Property — Non-ready assessments have questions
# ===================================================================


class TestPropertyNonReadyQuestions:
    """Property test: non-ready assessments must have questions."""

    @given(quality=st.sampled_from(["needs_refinement", "incomplete"]))
    @settings(max_examples=10)
    def test_property_non_ready_with_questions_accepted(self, quality: str) -> None:
        """TS-03-P2: Non-ready quality with questions is accepted."""
        agent = SpecAgent.__new__(SpecAgent)
        tool_input = {
            "quality": quality,
            "summary": "s",
            "gaps": [],
            "questions": [
                {
                    "id": "q1",
                    "text": "Q?",
                    "context": "ctx",
                    "options": [],
                    "required": True,
                }
            ],
        }
        assessment = agent._parse_assessment(tool_input)
        assert len(assessment.questions) > 0

    @given(quality=st.sampled_from(["needs_refinement", "incomplete"]))
    @settings(max_examples=10)
    def test_property_non_ready_empty_questions_rejected(self, quality: str) -> None:
        """TS-03-P2: Non-ready quality with empty questions is rejected."""
        agent = SpecAgent.__new__(SpecAgent)
        tool_input = {
            "quality": quality,
            "summary": "s",
            "gaps": [],
            "questions": [],
        }
        with pytest.raises(AgentError):
            agent._parse_assessment(tool_input)


# ===================================================================
# TS-03-P3: Property — Artifact generation order is deterministic
# ===================================================================


class TestPropertyGenerationOrder:
    """Property test: artifact generation order is always deterministic."""

    @pytest.mark.asyncio
    async def test_property_generation_order_deterministic(self, mock_ai_call) -> None:
        """TS-03-P3: Artifacts are always generated in the order
        requirements, test_spec, tasks."""
        call_order: list[str] = []
        artifact_order = ["requirements", "test_spec", "tasks"]
        samples = {
            "requirements": SAMPLE_REQUIREMENTS_JSON,
            "test_spec": SAMPLE_TEST_SPEC_JSON,
            "tasks": SAMPLE_TASKS_JSON,
        }
        generate_counter = 0

        async def tracking_ai_call(**kwargs):
            nonlocal generate_counter
            name = artifact_order[generate_counter]
            generate_counter += 1
            call_order.append(name)
            return _ai_call_response(make_artifact_response(name, samples[name]))

        mock_ai_call.side_effect = tracking_ai_call
        agent = SpecAgent("STANDARD")

        await agent.generate_artifacts("# PRD text", "03", "test")

        assert call_order == ["requirements", "test_spec", "tasks"]


# ===================================================================
# TS-03-P4: Property — Errors from ai_call are wrapped as AgentError
# ===================================================================


class TestPropertyErrorWrapping:
    """Property test: API errors from ai_call are always wrapped as AgentError."""

    @pytest.mark.asyncio
    @pytest.mark.parametrize(
        "error_factory,expected_category",
        [
            (make_rate_limit_error, "rate_limit"),
            (make_internal_server_error, "transient"),
            (make_connection_error, "transient"),
            (make_overloaded_error, "overloaded"),
            (make_bad_request_error, "input"),
            (make_auth_error, "auth"),
        ],
    )
    async def test_error_wrapping(self, mock_ai_call, error_factory, expected_category) -> None:
        """Errors from ai_call are wrapped as AgentError with correct category."""
        mock_ai_call.side_effect = error_factory()
        agent = SpecAgent("STANDARD")

        with pytest.raises(AgentError) as exc_info:
            await agent._call_api(
                messages=[{"role": "user", "content": "test"}],
                tools=[],
            )

        assert exc_info.value.category == expected_category


# ===================================================================
# TS-03-27: Retry on 529 OverloadedError — wrapped as AgentError
# ===================================================================


@pytest.mark.asyncio
async def test_overloaded_error_wrapped(mock_ai_call):
    """TS-03-27: 529 OverloadedError from ai_call is wrapped as AgentError."""
    mock_ai_call.side_effect = make_overloaded_error()
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError) as exc_info:
        await agent._call_api(
            messages=[{"role": "user", "content": "test"}],
            tools=[],
        )

    assert exc_info.value.category == "overloaded"
    assert exc_info.value.retryable is True
    assert exc_info.value.http_status == 529


# ===================================================================
# TS-03-28: 529 exhaustion raises with correct category
# ===================================================================


@pytest.mark.asyncio
async def test_529_exhaustion_raises_with_category(mock_ai_call):
    """TS-03-28: AgentError after 529 from ai_call has category='overloaded'."""
    mock_ai_call.side_effect = make_overloaded_error()
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError) as exc_info:
        await agent._call_api(
            messages=[{"role": "user", "content": "test"}],
            tools=[],
        )

    assert exc_info.value.category == "overloaded"
    assert exc_info.value.retryable is True
    assert exc_info.value.http_status == 529


# ===================================================================
# TS-03-29: Auth error has correct category
# ===================================================================


@pytest.mark.asyncio
async def test_auth_error_has_category(mock_ai_call):
    """TS-03-29: AgentError from 401 has category='auth' and is not retryable."""
    mock_ai_call.side_effect = make_auth_error()
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError) as exc_info:
        await agent._call_api(
            messages=[{"role": "user", "content": "test"}],
            tools=[],
        )

    assert exc_info.value.category == "auth"
    assert exc_info.value.http_status == 401
    assert exc_info.value.retryable is False
    assert mock_ai_call.call_count == 1


# ===================================================================
# TS-03-30: Rate limit raises with correct category
# ===================================================================


@pytest.mark.asyncio
async def test_rate_limit_raises_with_category(mock_ai_call):
    """TS-03-30: AgentError from 429 has category='rate_limit'."""
    mock_ai_call.side_effect = make_rate_limit_error()
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError) as exc_info:
        await agent._call_api(
            messages=[{"role": "user", "content": "test"}],
            tools=[],
        )

    assert exc_info.value.category == "rate_limit"
    assert exc_info.value.retryable is True
    assert exc_info.value.http_status == 429


# ===================================================================
# TS-03-31: AgentError structured fields defaults
# ===================================================================


def test_agent_error_structured_fields():
    """TS-03-31: AgentError carries structured metadata fields."""
    err = AgentError("test", category="auth", retryable=False, http_status=401)
    assert err.detail == "test"
    assert err.category == "auth"
    assert err.retryable is False
    assert err.http_status == 401


def test_agent_error_defaults():
    """AgentError defaults to internal/non-retryable when no kwargs given."""
    err = AgentError("test")
    assert err.category == "internal"
    assert err.retryable is False
    assert err.http_status is None


# ===================================================================
# TS-NS-1: stop_reason='refusal' raises AgentError with refusal message
# ===================================================================


@pytest.mark.asyncio
async def test_refusal_stop_reason_raises_agent_error(mock_ai_call):
    """TS-NS-1: When the API response has stop_reason='refusal',
    _call_api raises AgentError with a message naming the refusal cause."""
    from conftest_agent import FakeMessage

    mock_ai_call.return_value = _ai_call_response(
        FakeMessage(
            content=[],
            stop_reason="refusal",
        )
    )
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError, match="refusal") as exc_info:
        await agent._call_api(
            messages=[{"role": "user", "content": "test"}],
            tools=[],
        )

    assert exc_info.value.category == "refusal"
    assert "tool was not called" not in str(exc_info.value)


# ===================================================================
# TS-NS-2: stop_reason='model_context_window_exceeded' raises AgentError
# ===================================================================


@pytest.mark.asyncio
async def test_context_window_exceeded_stop_reason_raises_agent_error(mock_ai_call):
    """TS-NS-2: When the API response has stop_reason='model_context_window_exceeded',
    _call_api raises AgentError with a message naming the context window cause."""
    from conftest_agent import FakeMessage

    mock_ai_call.return_value = _ai_call_response(
        FakeMessage(
            content=[],
            stop_reason="model_context_window_exceeded",
        )
    )
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError, match="[Cc]ontext window") as exc_info:
        await agent._call_api(
            messages=[{"role": "user", "content": "test"}],
            tools=[],
        )

    assert exc_info.value.category == "context_window"
    assert "tool was not called" not in str(exc_info.value)


# ===================================================================
# TS-NS-3: stop_reason='pause_turn' raises AgentError
# ===================================================================


@pytest.mark.asyncio
async def test_pause_turn_stop_reason_raises_agent_error(mock_ai_call):
    """TS-NS-3: When the API response has stop_reason='pause_turn',
    _call_api raises AgentError with a message naming the iteration limit cause."""
    from conftest_agent import FakeMessage

    mock_ai_call.return_value = _ai_call_response(
        FakeMessage(
            content=[],
            stop_reason="pause_turn",
        )
    )
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError, match="pause") as exc_info:
        await agent._call_api(
            messages=[{"role": "user", "content": "test"}],
            tools=[],
        )

    assert exc_info.value.category == "pause_turn"
    assert "tool was not called" not in str(exc_info.value)


# ===================================================================
# TS-NS-4: Normal end_turn responses are unaffected
# ===================================================================


@pytest.mark.asyncio
async def test_end_turn_stop_reason_passes_through(mock_ai_call):
    """TS-NS-4: Normal end_turn responses are unaffected — _call_api
    returns the response for downstream tool extraction."""
    response = make_assessment_response()
    response.stop_reason = "end_turn"
    mock_ai_call.return_value = _ai_call_response(response)
    agent = SpecAgent("STANDARD")

    result = await agent._call_api(
        messages=[{"role": "user", "content": "test"}],
        tools=[],
    )

    assert result is not None
    assert result.stop_reason == "end_turn"
    # Verify _extract_tool_call still works on this response
    tool_input = agent._extract_tool_call(result, "submit_assessment")
    assert "quality" in tool_input


# ===================================================================
# TS-NS-5: Non-end_turn stop reasons carry distinct messages
# ===================================================================


@pytest.mark.parametrize(
    "stop_reason,expected_keyword",
    [
        ("refusal", "refusal"),
        ("model_context_window_exceeded", "context window"),
        ("pause_turn", "pause"),
    ],
)
@pytest.mark.asyncio
async def test_stop_reason_error_messages_are_distinct(mock_ai_call, stop_reason, expected_keyword):
    """TS-NS-5: Each non-end_turn stop reason carries a distinct,
    human-readable message that does NOT contain the generic
    'tool was not called' phrasing."""
    from conftest_agent import FakeMessage

    mock_ai_call.return_value = _ai_call_response(
        FakeMessage(
            content=[],
            stop_reason=stop_reason,
        )
    )
    agent = SpecAgent("STANDARD")

    with pytest.raises(AgentError) as exc_info:
        await agent._call_api(
            messages=[{"role": "user", "content": "test"}],
            tools=[],
        )

    error_message = str(exc_info.value)
    assert "tool was not called" not in error_message
    assert expected_keyword.lower() in error_message.lower()


# ===================================================================
# TS-NS-6: ai_call receives tools and tool_choice kwargs
# ===================================================================


@pytest.mark.asyncio
async def test_ai_call_receives_tools_kwargs(mock_ai_call):
    """TS-NS-6: When tools are provided, ai_call receives tools and tool_choice kwargs."""
    mock_ai_call.return_value = _ai_call_response(make_assessment_response())
    agent = SpecAgent("STANDARD")

    tools = [{"name": "submit_assessment", "input_schema": {}}]
    await agent._call_api(
        messages=[{"role": "user", "content": "test"}],
        tools=tools,
    )

    call_kwargs = mock_ai_call.call_args.kwargs
    assert call_kwargs["tools"] == tools
    assert call_kwargs["tool_choice"] == {"type": "any"}


# ===================================================================
# TS-NS-7: SpecAgent constructor accepts only model_tier
# ===================================================================


def test_spec_agent_constructor_accepts_model_tier():
    """TS-NS-7: SpecAgent can be constructed with just a model tier string."""
    agent = SpecAgent("STANDARD")
    assert agent._model == "STANDARD"
    assert not hasattr(agent, "_client")


# ===================================================================
# TS-NS-8: SpecAgent works with direct model IDs too
# ===================================================================


@pytest.mark.asyncio
async def test_spec_agent_with_direct_model_id(mock_ai_call):
    """TS-NS-8: SpecAgent can be constructed with a direct model ID."""
    mock_ai_call.return_value = _ai_call_response(make_assessment_response())
    agent = SpecAgent("claude-sonnet-4-6")

    await agent._call_api(
        messages=[{"role": "user", "content": "test"}],
        tools=[],
    )

    call_kwargs = mock_ai_call.call_args.kwargs
    assert call_kwargs["model_tier"] == "claude-sonnet-4-6"
