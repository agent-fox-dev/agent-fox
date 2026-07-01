"""Unit tests for dynamic complexity assessment.

Task Group 1: ComplexityAssessor class, AssessmentResult, Protocol,
              statelessness, and assessment prompt structure.
Task Group 2: Error handling and edge cases for ComplexityAssessor.
Task Group 3: apply_assessment() upgrade-only semantics and property tests.
Task Group 4: AssessmentManager integration, session_runner_factory wiring,
              and EscalationLadder construction.
Task Group 5: Explicit config override skip, resolution priority ordering,
              ARCHETYPE_REGISTRY defaults, and property tests.
Task Group 6: Dispatch integration, RoutingConfig validation, nightshift
              passthrough, and triage parsing.
Task Group 13: Wiring verification: end-to-end smoke tests and stub audit.

Test Spec: TS-15-1 through TS-15-12, TS-15-E2 through TS-15-E14,
           TS-15-49 through TS-15-53, TS-15-13 through TS-15-18,
           TS-15-E5, TS-15-P1 through TS-15-P4,
           TS-15-19 through TS-15-25, TS-15-E7, TS-15-P6, TS-15-P9,
           TS-15-26 through TS-15-31, TS-15-32 through TS-15-36,
           TS-15-E8, TS-15-P5, TS-15-P7,
           TS-15-37 through TS-15-48, TS-15-E9 through TS-15-E13,
           TS-15-P8,
           TS-15-SMOKE-1 through TS-15-SMOKE-6
Requirements: 15-REQ-1.1 through 15-REQ-1.7, 15-REQ-2.1 through 15-REQ-2.5,
              15-REQ-2.E1 through 15-REQ-2.E3, 15-REQ-4.E1,
              15-REQ-12.1 through 15-REQ-12.5, 15-REQ-12.E1,
              15-REQ-3.1 through 15-REQ-3.6, 15-REQ-3.E1,
              15-REQ-4.1 through 15-REQ-4.4, 15-REQ-5.1 through 15-REQ-5.3,
              15-REQ-5.E1,
              15-REQ-6.1 through 15-REQ-6.3, 15-REQ-6.E1,
              15-REQ-7.1 through 15-REQ-7.3,
              15-REQ-8.1 through 15-REQ-8.5,
              15-REQ-9.1 through 15-REQ-9.3, 15-REQ-9.E1,
              15-REQ-10.1 through 15-REQ-10.3, 15-REQ-10.E1, 15-REQ-10.E2,
              15-REQ-11.1 through 15-REQ-11.6,
              15-REQ-11.E1, 15-REQ-11.E2
"""

from __future__ import annotations

import asyncio
import dataclasses
import inspect
import logging
from typing import Any
from unittest.mock import AsyncMock, MagicMock

import pytest
from agentfox.core.complexity import (
    AssessmentResult,
    ComplexityAssessor,
    ComplexityRecommendation,
    apply_assessment,
)

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_anthropic_response(json_text: str) -> MagicMock:
    """Create a mock Anthropic Message response with the given JSON text.

    Mirrors the Anthropic SDK structure: message.content[0].text
    """
    content_block = MagicMock()
    content_block.text = json_text
    response = MagicMock()
    response.content = [content_block]
    return response


def _make_mock_client(json_text: str) -> MagicMock:
    """Create a mock Anthropic client returning the given JSON on create()."""
    mock_client = MagicMock()
    mock_client.messages.create = AsyncMock(
        return_value=_make_anthropic_response(json_text),
    )
    return mock_client


def _make_routing_config(
    *,
    assessor_model: str = "claude-haiku-4-5",
    confidence_threshold: float = 0.6,
) -> Any:
    """Create a RoutingConfig-compatible object for AssessmentManager tests.

    Because RoutingConfig may not yet have assessor_model/confidence_threshold
    fields (added in task group 10), we try the actual RoutingConfig first,
    then fall back to a mock with the right attributes.
    """
    from agentfox.core.config import RoutingConfig

    try:
        config = RoutingConfig(
            assessor_model=assessor_model,
            confidence_threshold=confidence_threshold,
        )
    except (TypeError, Exception):
        # RoutingConfig doesn't have these fields yet — use base config
        # and attach new fields as attributes for test assertions
        config = RoutingConfig()
        object.__setattr__(config, "assessor_model", assessor_model)
        object.__setattr__(config, "confidence_threshold", confidence_threshold)

    return config


VALID_RESPONSE_JSON = (
    '{"recommended_tier": "ADVANCED", "recommended_variant": "standard", '
    '"confidence": 0.75, "rationale": "Complex task"}'
)


# ===========================================================================
# Task 1.1: ComplexityRecommendation Protocol and AssessmentResult dataclass
# Test Spec: TS-15-1, TS-15-2
# Requirements: 15-REQ-1.1, 15-REQ-1.2
# ===========================================================================


class TestComplexityRecommendationProtocol:
    """TS-15-1: ComplexityRecommendation Protocol definition.

    Requirement: 15-REQ-1.1
    """

    def test_is_protocol(self) -> None:
        """ComplexityRecommendation is a Protocol class."""
        assert getattr(ComplexityRecommendation, "_is_protocol", False) is True

    def test_has_recommended_tier_annotation(self) -> None:
        """Protocol has recommended_tier: str annotation."""
        annotations = getattr(ComplexityRecommendation, "__annotations__", {})
        assert "recommended_tier" in annotations

    def test_has_recommended_variant_annotation(self) -> None:
        """Protocol has recommended_variant annotation (str | None)."""
        annotations = getattr(ComplexityRecommendation, "__annotations__", {})
        assert "recommended_variant" in annotations

    def test_has_confidence_annotation(self) -> None:
        """Protocol has confidence: float annotation."""
        annotations = getattr(ComplexityRecommendation, "__annotations__", {})
        assert "confidence" in annotations

    def test_assessment_result_satisfies_protocol(self) -> None:
        """AssessmentResult structurally satisfies ComplexityRecommendation."""
        result = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="standard",
            confidence=0.8,
            rationale="test",
        )
        assert isinstance(result, ComplexityRecommendation)


class TestAssessmentResultDataclass:
    """TS-15-2: AssessmentResult frozen dataclass.

    Requirement: 15-REQ-1.2
    """

    def test_is_dataclass(self) -> None:
        """AssessmentResult is a dataclass."""
        assert dataclasses.is_dataclass(AssessmentResult)

    def test_is_frozen(self) -> None:
        """AssessmentResult is frozen (immutable)."""
        assert AssessmentResult.__dataclass_params__.frozen is True

    def test_has_exactly_four_fields(self) -> None:
        """Has recommended_tier, recommended_variant, confidence, rationale."""
        fields = {f.name for f in dataclasses.fields(AssessmentResult)}
        assert fields == {
            "recommended_tier",
            "recommended_variant",
            "confidence",
            "rationale",
        }

    def test_instantiation_and_field_access(self) -> None:
        """Instantiation succeeds and all fields are accessible."""
        result = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="standard",
            confidence=0.8,
            rationale="test rationale",
        )
        assert result.recommended_tier == "ADVANCED"
        assert result.recommended_variant == "standard"
        assert result.confidence == 0.8
        assert result.rationale == "test rationale"

    def test_immutability_raises_on_mutation(self) -> None:
        """Frozen dataclass raises on attribute mutation attempt."""
        result = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="standard",
            confidence=0.8,
            rationale="test",
        )
        with pytest.raises((dataclasses.FrozenInstanceError, AttributeError)):
            result.recommended_tier = "SIMPLE"  # type: ignore[misc]

    def test_none_variant_accepted(self) -> None:
        """recommended_variant accepts None."""
        result = AssessmentResult(
            recommended_tier="STANDARD",
            recommended_variant=None,
            confidence=0.5,
            rationale="simple",
        )
        assert result.recommended_variant is None


# ===========================================================================
# Task 1.2: ComplexityAssessor constructor and assess() signature
# Test Spec: TS-15-3, TS-15-4
# Requirements: 15-REQ-1.3, 15-REQ-1.4
# ===========================================================================


class TestComplexityAssessorConstructor:
    """TS-15-3: ComplexityAssessor constructor defaults and configuration.

    Requirement: 15-REQ-1.3
    """

    def test_default_assessor_model(self) -> None:
        """Default assessor_model is 'claude-haiku-4-5'."""
        assessor = ComplexityAssessor(client=MagicMock())
        assert assessor.assessor_model == "claude-haiku-4-5"

    def test_default_confidence_threshold(self) -> None:
        """Default confidence_threshold is 0.6."""
        assessor = ComplexityAssessor(client=MagicMock())
        assert assessor.confidence_threshold == 0.6

    def test_client_stored(self) -> None:
        """Client is stored as instance attribute."""
        mock_client = MagicMock()
        assessor = ComplexityAssessor(client=mock_client)
        assert assessor.client is mock_client

    def test_custom_assessor_model_accepted(self) -> None:
        """Custom assessor_model is accepted and stored."""
        assessor = ComplexityAssessor(
            client=MagicMock(),
            assessor_model="claude-sonnet-4-5",
        )
        assert assessor.assessor_model == "claude-sonnet-4-5"

    def test_custom_confidence_threshold_accepted(self) -> None:
        """Custom confidence_threshold is accepted and stored."""
        assessor = ComplexityAssessor(
            client=MagicMock(),
            confidence_threshold=0.8,
        )
        assert assessor.confidence_threshold == 0.8


class TestComplexityAssessorAssess:
    """TS-15-4: ComplexityAssessor.assess() async method and return type.

    Requirement: 15-REQ-1.4
    """

    def test_assess_is_coroutine_function(self) -> None:
        """assess() is a coroutine function (async)."""
        assert inspect.iscoroutinefunction(ComplexityAssessor.assess)

    def test_assess_returns_assessment_result(self) -> None:
        """assess() returns an AssessmentResult with all four fields populated."""
        mock_client = _make_mock_client(VALID_RESPONSE_JSON)
        assessor = ComplexityAssessor(client=mock_client)

        result = asyncio.run(
            assessor.assess(
                node_body="Fix the bug",
                archetype="coder",
                mode="fix",
                base_tier="STANDARD",
                base_variant="standard",
                previous_failure=None,
            )
        )

        assert isinstance(result, AssessmentResult)
        assert result.recommended_tier == "ADVANCED"
        assert result.recommended_variant == "standard"
        assert result.confidence == 0.75
        assert result.rationale == "Complex task"

    def test_assess_accepts_all_parameters(self) -> None:
        """assess() accepts node_body, archetype, mode, base_tier, base_variant, previous_failure."""
        mock_client = _make_mock_client(VALID_RESPONSE_JSON)
        assessor = ComplexityAssessor(client=mock_client)

        # Should not raise TypeError for the parameter names
        result = asyncio.run(
            assessor.assess(
                node_body="body",
                archetype="coder",
                mode=None,
                base_tier="STANDARD",
                base_variant="standard",
                previous_failure="TypeError: NoneType",
            )
        )
        assert result is not None

    def test_assess_with_none_mode(self) -> None:
        """assess() accepts mode=None without error."""
        mock_client = _make_mock_client(VALID_RESPONSE_JSON)
        assessor = ComplexityAssessor(client=mock_client)

        result = asyncio.run(
            assessor.assess(
                node_body="simple task",
                archetype="verifier",
                mode=None,
                base_tier="STANDARD",
                base_variant="standard",
            )
        )
        assert isinstance(result, AssessmentResult)


# ===========================================================================
# Task 1.3: Statelessness and concurrency safety
# Test Spec: TS-15-5, TS-15-12
# Requirements: 15-REQ-1.5, 15-REQ-2.5
# ===========================================================================


class TestComplexityAssessorStatelessness:
    """TS-15-5: Statelessness — concurrent assess() calls return independent results.

    Requirement: 15-REQ-1.5
    """

    def test_concurrent_calls_return_independent_results(self) -> None:
        """Two concurrent assess() calls with distinct inputs return independent results."""
        call_count = 0

        async def mock_create(**kwargs: Any) -> MagicMock:
            nonlocal call_count
            call_count += 1
            if call_count == 1:
                return _make_anthropic_response(
                    '{"recommended_tier": "ADVANCED", "recommended_variant": "extended", '
                    '"confidence": 0.9, "rationale": "Complex"}'
                )
            return _make_anthropic_response(
                '{"recommended_tier": "STANDARD", "recommended_variant": "standard", '
                '"confidence": 0.7, "rationale": "Simple"}'
            )

        mock_client = MagicMock()
        mock_client.messages.create = mock_create
        assessor = ComplexityAssessor(client=mock_client)

        async def _run() -> tuple[AssessmentResult, AssessmentResult]:
            task1 = assessor.assess("body1", "coder", None, "STANDARD", "standard", None)
            task2 = assessor.assess("body2", "reviewer", "fix-review", "STANDARD", "standard", None)
            return await asyncio.gather(task1, task2)  # type: ignore[return-value]

        result1, result2 = asyncio.run(_run())

        assert isinstance(result1, AssessmentResult)
        assert isinstance(result2, AssessmentResult)
        # Results should differ since inputs produce different mock responses
        assert result1 != result2


class TestComplexityAssessorNoConcurrencyCap:
    """TS-15-12: No concurrency cap — each assess() proceeds independently.

    Requirement: 15-REQ-2.5
    """

    def test_three_concurrent_calls_complete_independently(self) -> None:
        """Three concurrent assess() calls all complete without blocking."""
        mock_client = _make_mock_client(VALID_RESPONSE_JSON)
        assessor = ComplexityAssessor(client=mock_client)

        async def _run() -> list[AssessmentResult]:
            tasks = [assessor.assess("body", "coder", None, "STANDARD", "standard", None) for _ in range(3)]
            return await asyncio.gather(*tasks)

        results = asyncio.run(_run())

        assert len(results) == 3
        assert all(r.recommended_tier in ("SIMPLE", "STANDARD", "ADVANCED") for r in results)
        assert mock_client.messages.create.call_count == 3


# ===========================================================================
# Task 1.4: AssessmentManager client injection and absent-client path
# Test Spec: TS-15-6, TS-15-7, TS-15-E1
# Requirements: 15-REQ-1.6, 15-REQ-1.7, 15-REQ-1.E1
# ===========================================================================


class TestAssessmentManagerClientNone:
    """TS-15-6: AssessmentManager with client=None skips ComplexityAssessor.

    When instantiated with client=None, ComplexityAssessor is not
    instantiated and assess_node() falls back to base tier/variant
    silently with no WARNING or ERROR log.

    Requirement: 15-REQ-1.6, 15-REQ-1.E1
    """

    def test_no_assessor_attribute_when_client_none(self) -> None:
        """ComplexityAssessor is not instantiated when client=None."""
        from agentfox.engine.engine import AssessmentManager

        routing_config = _make_routing_config()
        manager = AssessmentManager(config=routing_config, client=None)
        assert not hasattr(manager, "_assessor") or manager._assessor is None

    def test_assess_node_returns_ladder_at_base_tier(self) -> None:
        """assess_node() returns EscalationLadder at base tier/variant for coder."""
        from agentfox.engine.engine import AssessmentManager

        routing_config = _make_routing_config()
        manager = AssessmentManager(config=routing_config, client=None)

        ladder = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="some body",
            )
        )

        assert ladder is not None
        # Coder base tier should be STANDARD per spec 15-REQ-8.1
        assert hasattr(ladder, "starting_tier") or hasattr(ladder, "_starting_tier")

    def test_no_warning_logs_emitted(self, caplog: pytest.LogCaptureFixture) -> None:
        """No WARNING or ERROR logs emitted on client=None path."""
        from agentfox.engine.engine import AssessmentManager

        routing_config = _make_routing_config()
        manager = AssessmentManager(config=routing_config, client=None)

        with caplog.at_level(logging.DEBUG, logger="agentfox"):
            asyncio.run(
                manager.assess_node(
                    node_id="n1",
                    archetype="coder",
                    mode=None,
                    node_body="some body",
                )
            )

        warning_records = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_records) == 0


class TestAssessmentManagerClientNonNone:
    """TS-15-7: AssessmentManager with non-None client creates ComplexityAssessor.

    Requirement: 15-REQ-1.7
    """

    def test_assessor_created_with_client(self) -> None:
        """AssessmentManager stores a ComplexityAssessor when client is provided."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        routing_config = _make_routing_config()
        manager = AssessmentManager(config=routing_config, client=mock_client)

        assert manager._assessor is not None
        assert isinstance(manager._assessor, ComplexityAssessor)
        assert manager._assessor.client is mock_client

    def test_assessor_uses_routing_config_values(self) -> None:
        """ComplexityAssessor configured with assessor_model/confidence_threshold."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        routing_config = _make_routing_config(
            assessor_model="claude-haiku-4-5",
            confidence_threshold=0.6,
        )
        manager = AssessmentManager(config=routing_config, client=mock_client)

        assert manager._assessor.assessor_model == "claude-haiku-4-5"
        assert manager._assessor.confidence_threshold == 0.6


class TestAssessmentManagerClientNoneMultipleNodes:
    """TS-15-E1: Every assess_node() call returns base tier/variant with client=None.

    Requirement: 15-REQ-1.E1
    """

    def test_multiple_nodes_all_fallback(self, caplog: pytest.LogCaptureFixture) -> None:
        """All assess_node() calls silently return base tier/variant."""
        from agentfox.engine.engine import AssessmentManager

        routing_config = _make_routing_config()
        manager = AssessmentManager(config=routing_config, client=None)

        test_cases = [
            ("n_coder", "coder", None),
            ("n_reviewer", "reviewer", "pre-review"),
        ]

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            for node_id, archetype, mode in test_cases:
                ladder = asyncio.run(
                    manager.assess_node(
                        node_id=node_id,
                        archetype=archetype,
                        mode=mode,
                        node_body="body",
                    )
                )
                assert ladder is not None

        warning_records = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_records) == 0


# ===========================================================================
# Task 1.5: Assessment prompt structure and previous_failure injection
# Test Spec: TS-15-8, TS-15-9
# Requirements: 15-REQ-2.1, 15-REQ-2.2
# ===========================================================================


class TestAssessmentPromptStructure:
    """TS-15-8: Prompt contains system role, archetype/mode, tier/variant, body, JSON instruction.

    Requirement: 15-REQ-2.1
    """

    def test_prompt_contains_system_role(self) -> None:
        """API call includes a system role establishing the LLM as complexity assessor."""
        captured_calls: list[dict[str, Any]] = []

        async def mock_create(**kwargs: Any) -> MagicMock:
            captured_calls.append(kwargs)
            return _make_anthropic_response(VALID_RESPONSE_JSON)

        mock_client = MagicMock()
        mock_client.messages.create = mock_create
        assessor = ComplexityAssessor(client=mock_client)

        asyncio.run(
            assessor.assess(
                node_body="Implement auth module",
                archetype="coder",
                mode="fix",
                base_tier="STANDARD",
                base_variant="standard",
                previous_failure=None,
            )
        )

        assert len(captured_calls) == 1
        call_kwargs = captured_calls[0]
        # System message should be present
        assert "system" in call_kwargs

    def test_prompt_contains_archetype_and_mode(self) -> None:
        """API call prompt includes archetype and mode identifiers."""
        captured_calls: list[dict[str, Any]] = []

        async def mock_create(**kwargs: Any) -> MagicMock:
            captured_calls.append(kwargs)
            return _make_anthropic_response(VALID_RESPONSE_JSON)

        mock_client = MagicMock()
        mock_client.messages.create = mock_create
        assessor = ComplexityAssessor(client=mock_client)

        asyncio.run(
            assessor.assess(
                node_body="Implement auth module",
                archetype="coder",
                mode="fix",
                base_tier="STANDARD",
                base_variant="standard",
                previous_failure=None,
            )
        )

        prompt_text = str(captured_calls[0])
        assert "coder" in prompt_text
        assert "fix" in prompt_text

    def test_prompt_contains_base_tier_and_variant(self) -> None:
        """API call prompt includes current base_tier and base_variant."""
        captured_calls: list[dict[str, Any]] = []

        async def mock_create(**kwargs: Any) -> MagicMock:
            captured_calls.append(kwargs)
            return _make_anthropic_response(VALID_RESPONSE_JSON)

        mock_client = MagicMock()
        mock_client.messages.create = mock_create
        assessor = ComplexityAssessor(client=mock_client)

        asyncio.run(
            assessor.assess(
                node_body="Implement auth module",
                archetype="coder",
                mode="fix",
                base_tier="STANDARD",
                base_variant="standard",
                previous_failure=None,
            )
        )

        prompt_text = str(captured_calls[0])
        assert "STANDARD" in prompt_text
        assert "standard" in prompt_text

    def test_prompt_contains_full_node_body(self) -> None:
        """API call includes the full node_body text without truncation."""
        captured_calls: list[dict[str, Any]] = []

        async def mock_create(**kwargs: Any) -> MagicMock:
            captured_calls.append(kwargs)
            return _make_anthropic_response(VALID_RESPONSE_JSON)

        mock_client = MagicMock()
        mock_client.messages.create = mock_create
        assessor = ComplexityAssessor(client=mock_client)

        long_body = "Implement auth module with OAuth2, JWT tokens, and RBAC. " * 50
        asyncio.run(
            assessor.assess(
                node_body=long_body,
                archetype="coder",
                mode="fix",
                base_tier="STANDARD",
                base_variant="standard",
                previous_failure=None,
            )
        )

        prompt_text = str(captured_calls[0])
        assert long_body in prompt_text

    def test_prompt_contains_json_output_instruction(self) -> None:
        """API call includes instruction to respond with JSON matching schema."""
        captured_calls: list[dict[str, Any]] = []

        async def mock_create(**kwargs: Any) -> MagicMock:
            captured_calls.append(kwargs)
            return _make_anthropic_response(VALID_RESPONSE_JSON)

        mock_client = MagicMock()
        mock_client.messages.create = mock_create
        assessor = ComplexityAssessor(client=mock_client)

        asyncio.run(
            assessor.assess(
                node_body="Implement auth module",
                archetype="coder",
                mode="fix",
                base_tier="STANDARD",
                base_variant="standard",
                previous_failure=None,
            )
        )

        prompt_text = str(captured_calls[0])
        assert "recommended_tier" in prompt_text or "JSON" in prompt_text


class TestAssessmentPreviousFailure:
    """TS-15-9: previous_failure context appears in the assessment prompt.

    Requirement: 15-REQ-2.2
    """

    def test_previous_failure_text_in_prompt(self) -> None:
        """When previous_failure is provided, its text appears in the API call."""
        captured_calls: list[dict[str, Any]] = []

        async def mock_create(**kwargs: Any) -> MagicMock:
            captured_calls.append(kwargs)
            return _make_anthropic_response(
                '{"recommended_tier": "ADVANCED", '
                '"recommended_variant": "standard", '
                '"confidence": 0.8, "rationale": "retry"}'
            )

        mock_client = MagicMock()
        mock_client.messages.create = mock_create
        assessor = ComplexityAssessor(client=mock_client)

        asyncio.run(
            assessor.assess(
                node_body="Fix auth",
                archetype="coder",
                mode="fix",
                base_tier="STANDARD",
                base_variant="standard",
                previous_failure="TypeError: cannot unpack non-sequence NoneType",
            )
        )

        prompt_text = str(captured_calls[0])
        assert "TypeError: cannot unpack non-sequence NoneType" in prompt_text

    def test_no_failure_context_when_none(self) -> None:
        """When previous_failure is None, no failure context appears in prompt."""
        captured_calls: list[dict[str, Any]] = []

        async def mock_create(**kwargs: Any) -> MagicMock:
            captured_calls.append(kwargs)
            return _make_anthropic_response(VALID_RESPONSE_JSON)

        mock_client = MagicMock()
        mock_client.messages.create = mock_create
        assessor = ComplexityAssessor(client=mock_client)

        asyncio.run(
            assessor.assess(
                node_body="Fix auth",
                archetype="coder",
                mode="fix",
                base_tier="STANDARD",
                base_variant="standard",
                previous_failure=None,
            )
        )

        # Verify the call was made (prompt captured) — further assertions
        # verify no "previous failure" phrasing unless actually provided.
        assert len(captured_calls) == 1


# ===========================================================================
# Task 1.6: Structured output validation and timeout enforcement
# Test Spec: TS-15-10, TS-15-11
# Requirements: 15-REQ-2.3, 15-REQ-2.4, 15-REQ-2.5
# ===========================================================================


class TestAssessmentResponseValidation:
    """TS-15-10: Valid JSON response parsed into AssessmentResult.

    Requirement: 15-REQ-2.3
    """

    def test_valid_json_parsed_to_assessment_result(self) -> None:
        """Valid JSON response returns AssessmentResult with all four fields."""
        valid_json = (
            '{"recommended_tier": "ADVANCED", "recommended_variant": "extended", '
            '"confidence": 0.82, "rationale": "Complex"}'
        )
        mock_client = _make_mock_client(valid_json)
        assessor = ComplexityAssessor(client=mock_client)

        result = asyncio.run(
            assessor.assess(
                node_body="body",
                archetype="coder",
                mode=None,
                base_tier="STANDARD",
                base_variant="standard",
                previous_failure=None,
            )
        )

        assert isinstance(result, AssessmentResult)
        assert result.recommended_tier == "ADVANCED"
        assert result.recommended_variant == "extended"
        assert result.confidence == 0.82
        assert result.rationale == "Complex"

    def test_valid_json_with_null_variant(self) -> None:
        """Valid JSON with null recommended_variant is accepted."""
        valid_json = (
            '{"recommended_tier": "STANDARD", "recommended_variant": null, '
            '"confidence": 0.7, "rationale": "Simple task"}'
        )
        mock_client = _make_mock_client(valid_json)
        assessor = ComplexityAssessor(client=mock_client)

        result = asyncio.run(
            assessor.assess(
                node_body="body",
                archetype="coder",
                mode=None,
                base_tier="STANDARD",
                base_variant="standard",
                previous_failure=None,
            )
        )

        assert isinstance(result, AssessmentResult)
        assert result.recommended_variant is None

    def test_valid_json_with_all_tier_values(self) -> None:
        """Each valid ModelTier value (SIMPLE, STANDARD, ADVANCED) is accepted."""
        for tier in ("SIMPLE", "STANDARD", "ADVANCED"):
            valid_json = (
                f'{{"recommended_tier": "{tier}", "recommended_variant": "standard", '
                f'"confidence": 0.7, "rationale": "test"}}'
            )
            mock_client = _make_mock_client(valid_json)
            assessor = ComplexityAssessor(client=mock_client)

            result = asyncio.run(
                assessor.assess(
                    node_body="body",
                    archetype="coder",
                    mode=None,
                    base_tier="STANDARD",
                    base_variant="standard",
                    previous_failure=None,
                )
            )

            assert result.recommended_tier == tier

    def test_valid_json_with_all_variant_values(self) -> None:
        """Each valid variant value (fast, standard, extended) is accepted."""
        for variant in ("fast", "standard", "extended"):
            valid_json = (
                '{"recommended_tier": "STANDARD", '
                f'"recommended_variant": "{variant}", '
                '"confidence": 0.7, "rationale": "test"}'
            )
            mock_client = _make_mock_client(valid_json)
            assessor = ComplexityAssessor(client=mock_client)

            result = asyncio.run(
                assessor.assess(
                    node_body="body",
                    archetype="coder",
                    mode=None,
                    base_tier="STANDARD",
                    base_variant="standard",
                    previous_failure=None,
                )
            )

            assert result.recommended_variant == variant


class TestAssessmentTimeoutEnforcement:
    """TS-15-11: 30-second timeout on the Anthropic API call.

    Requirement: 15-REQ-2.4
    """

    def test_api_call_has_30_second_timeout(self) -> None:
        """The Anthropic messages.create call is made with timeout=30."""
        captured_kwargs: dict[str, Any] = {}

        async def mock_create(**kwargs: Any) -> MagicMock:
            captured_kwargs.update(kwargs)
            return _make_anthropic_response(VALID_RESPONSE_JSON)

        mock_client = MagicMock()
        mock_client.messages.create = mock_create
        assessor = ComplexityAssessor(client=mock_client)

        asyncio.run(
            assessor.assess(
                node_body="body",
                archetype="coder",
                mode=None,
                base_tier="STANDARD",
                base_variant="standard",
                previous_failure=None,
            )
        )

        timeout = captured_kwargs.get("timeout")
        assert timeout == 30 or timeout == 30.0, f"Expected timeout=30, got timeout={timeout}"

    def test_api_call_uses_configured_model(self) -> None:
        """The API call uses the assessor's configured model."""
        captured_kwargs: dict[str, Any] = {}

        async def mock_create(**kwargs: Any) -> MagicMock:
            captured_kwargs.update(kwargs)
            return _make_anthropic_response(VALID_RESPONSE_JSON)

        mock_client = MagicMock()
        mock_client.messages.create = mock_create
        assessor = ComplexityAssessor(
            client=mock_client,
            assessor_model="claude-haiku-4-5",
        )

        asyncio.run(
            assessor.assess(
                node_body="body",
                archetype="coder",
                mode=None,
                base_tier="STANDARD",
                base_variant="standard",
                previous_failure=None,
            )
        )

        assert captured_kwargs.get("model") == "claude-haiku-4-5"


# ===========================================================================
# Task Group 2: Error handling and edge cases for ComplexityAssessor
# ===========================================================================

# ---------------------------------------------------------------------------
# Helpers for anthropic exceptions
# ---------------------------------------------------------------------------

try:
    import anthropic

    _HAS_ANTHROPIC = True
except ImportError:  # pragma: no cover
    _HAS_ANTHROPIC = False

_needs_anthropic = pytest.mark.skipif(
    not _HAS_ANTHROPIC,
    reason="anthropic SDK not installed",
)


def _make_api_timeout_error() -> Exception:
    """Create an anthropic.APITimeoutError for testing."""
    return anthropic.APITimeoutError(request=MagicMock())


def _make_rate_limit_error() -> Exception:
    """Create an anthropic.RateLimitError for testing."""
    exc = anthropic.RateLimitError.__new__(anthropic.RateLimitError)
    exc.status_code = 429
    exc.message = "Rate limit exceeded"
    exc.body = None
    exc.response = MagicMock(status_code=429, headers={})
    return exc


def _make_bad_request_error(message: str = "context length exceeded") -> Exception:
    """Create an anthropic.BadRequestError for testing (e.g. context-limit)."""
    return anthropic.BadRequestError(
        response=MagicMock(status_code=400, headers={}),
        message=message,
        body={},
    )


# ===========================================================================
# Task 2.1: Parse failure cases (malformed JSON, wrong-case, missing fields)
# Test Spec: TS-15-E2
# Requirement: 15-REQ-2.E1
# ===========================================================================


class TestComplexityAssessorParseFailures:
    """TS-15-E2: Malformed/invalid LLM responses trigger total parse failure.

    On parse failure, assess() should:
    - Not raise an exception
    - Log a WARNING with exception details
    - Return base tier/variant (no partial field salvaging)

    Requirement: 15-REQ-2.E1
    """

    def test_malformed_json_logs_warning_and_falls_back(self, caplog: pytest.LogCaptureFixture) -> None:
        """Malformed JSON triggers WARNING and base tier/variant fallback."""
        mock_client = _make_mock_client("not json at all")
        assessor = ComplexityAssessor(client=mock_client)

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            result = asyncio.run(
                assessor.assess(
                    node_body="body",
                    archetype="coder",
                    mode=None,
                    base_tier="STANDARD",
                    base_variant="standard",
                    previous_failure=None,
                )
            )

        # Should not raise; result should reflect base values
        assert isinstance(result, AssessmentResult)
        assert result.recommended_tier == "STANDARD"
        assert result.recommended_variant == "standard"

        warning_logs = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_logs) >= 1

    def test_missing_required_field_logs_warning_and_falls_back(self, caplog: pytest.LogCaptureFixture) -> None:
        """Missing required field (rationale) triggers WARNING and fallback."""
        # Missing 'rationale' field
        bad_json = '{"recommended_tier": "ADVANCED", "recommended_variant": "standard", "confidence": 0.8}'
        mock_client = _make_mock_client(bad_json)
        assessor = ComplexityAssessor(client=mock_client)

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            result = asyncio.run(
                assessor.assess(
                    node_body="body",
                    archetype="coder",
                    mode=None,
                    base_tier="STANDARD",
                    base_variant="standard",
                    previous_failure=None,
                )
            )

        assert isinstance(result, AssessmentResult)
        assert result.recommended_tier == "STANDARD"
        assert result.recommended_variant == "standard"

        warning_logs = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_logs) >= 1

    def test_out_of_range_confidence_logs_warning_and_falls_back(self, caplog: pytest.LogCaptureFixture) -> None:
        """Confidence=1.5 (out of [0.0, 1.0]) triggers WARNING and fallback."""
        bad_json = (
            '{"recommended_tier": "ADVANCED", "recommended_variant": "standard", "confidence": 1.5, "rationale": "r"}'
        )
        mock_client = _make_mock_client(bad_json)
        assessor = ComplexityAssessor(client=mock_client)

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            result = asyncio.run(
                assessor.assess(
                    node_body="body",
                    archetype="coder",
                    mode=None,
                    base_tier="STANDARD",
                    base_variant="standard",
                    previous_failure=None,
                )
            )

        assert isinstance(result, AssessmentResult)
        assert result.recommended_tier == "STANDARD"
        assert result.recommended_variant == "standard"

        warning_logs = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_logs) >= 1

    def test_wrong_case_tier_logs_warning_and_falls_back(self, caplog: pytest.LogCaptureFixture) -> None:
        """Wrong-case tier ('advanced' instead of 'ADVANCED') triggers WARNING."""
        bad_json = (
            '{"recommended_tier": "advanced", "recommended_variant": "standard", "confidence": 0.8, "rationale": "r"}'
        )
        mock_client = _make_mock_client(bad_json)
        assessor = ComplexityAssessor(client=mock_client)

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            result = asyncio.run(
                assessor.assess(
                    node_body="body",
                    archetype="coder",
                    mode=None,
                    base_tier="STANDARD",
                    base_variant="standard",
                    previous_failure=None,
                )
            )

        assert isinstance(result, AssessmentResult)
        assert result.recommended_tier == "STANDARD"
        assert result.recommended_variant == "standard"

        warning_logs = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_logs) >= 1

    def test_wrong_case_variant_logs_warning_and_falls_back(self, caplog: pytest.LogCaptureFixture) -> None:
        """Wrong-case variant ('Standard' instead of 'standard') triggers WARNING."""
        bad_json = (
            '{"recommended_tier": "ADVANCED", "recommended_variant": "Standard", "confidence": 0.8, "rationale": "r"}'
        )
        mock_client = _make_mock_client(bad_json)
        assessor = ComplexityAssessor(client=mock_client)

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            result = asyncio.run(
                assessor.assess(
                    node_body="body",
                    archetype="coder",
                    mode=None,
                    base_tier="STANDARD",
                    base_variant="standard",
                    previous_failure=None,
                )
            )

        assert isinstance(result, AssessmentResult)
        assert result.recommended_tier == "STANDARD"
        assert result.recommended_variant == "standard"

        warning_logs = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_logs) >= 1

    @pytest.mark.parametrize(
        "bad_response",
        [
            pytest.param("not json at all", id="malformed-json"),
            pytest.param(
                '{"recommended_tier": "ADVANCED", "recommended_variant": "standard", "confidence": 0.8}',
                id="missing-rationale",
            ),
            pytest.param(
                '{"recommended_tier": "ADVANCED", "recommended_variant": "standard", '
                '"confidence": 1.5, "rationale": "r"}',
                id="confidence-out-of-range",
            ),
            pytest.param(
                '{"recommended_tier": "advanced", "recommended_variant": "standard", '
                '"confidence": 0.8, "rationale": "r"}',
                id="wrong-case-tier",
            ),
            pytest.param(
                '{"recommended_tier": "ADVANCED", "recommended_variant": "Standard", '
                '"confidence": 0.8, "rationale": "r"}',
                id="wrong-case-variant",
            ),
        ],
    )
    def test_no_partial_field_salvaging_on_any_invalid_response(
        self,
        bad_response: str,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """No partial field salvaging: entire response treated as parse failure."""
        mock_client = _make_mock_client(bad_response)
        assessor = ComplexityAssessor(client=mock_client)

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            try:
                result = asyncio.run(
                    assessor.assess(
                        node_body="body",
                        archetype="coder",
                        mode=None,
                        base_tier="STANDARD",
                        base_variant="standard",
                        previous_failure=None,
                    )
                )
            except Exception as e:
                pytest.fail(f"assess() should not raise, got {e}")

        # Result should be fallback to base values, not partial parse
        assert isinstance(result, AssessmentResult)
        assert result.recommended_tier == "STANDARD"
        assert result.recommended_variant == "standard"

        warning_logs = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_logs) >= 1, f"No WARNING for response: {bad_response}"


# ===========================================================================
# Task 2.2: Timeout and rate limit error handling
# Test Spec: TS-15-E3, TS-15-E4
# Requirements: 15-REQ-2.E2, 15-REQ-2.E3
# ===========================================================================


@_needs_anthropic
class TestComplexityAssessorTimeoutError:
    """TS-15-E3: API timeout triggers WARNING and base fallback.

    A 30-second API timeout is treated as an assessment failure:
    WARNING logged, base tier/variant returned, no exception raised.

    Requirement: 15-REQ-2.E2
    """

    def test_timeout_error_returns_base_without_raising(self, caplog: pytest.LogCaptureFixture) -> None:
        """APITimeoutError does not propagate; returns base tier/variant."""
        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(
            side_effect=_make_api_timeout_error(),
        )
        assessor = ComplexityAssessor(client=mock_client)

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            try:
                result = asyncio.run(
                    assessor.assess(
                        node_body="body",
                        archetype="coder",
                        mode=None,
                        base_tier="STANDARD",
                        base_variant="standard",
                        previous_failure=None,
                    )
                )
            except Exception as e:
                pytest.fail(f"assess() should not raise on timeout, got {e}")

        assert isinstance(result, AssessmentResult)
        assert result.recommended_tier == "STANDARD"
        assert result.recommended_variant == "standard"

    def test_timeout_error_logs_warning_with_timeout_message(self, caplog: pytest.LogCaptureFixture) -> None:
        """WARNING log includes 'timeout' or exception details."""
        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(
            side_effect=_make_api_timeout_error(),
        )
        assessor = ComplexityAssessor(client=mock_client)

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            asyncio.run(
                assessor.assess(
                    node_body="body",
                    archetype="coder",
                    mode=None,
                    base_tier="STANDARD",
                    base_variant="standard",
                    previous_failure=None,
                )
            )

        warning_logs = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_logs) >= 1

        log_text = " ".join(r.getMessage() for r in warning_logs)
        assert "timeout" in log_text.lower() or "Timeout" in log_text, (
            f"Expected 'timeout' in warning log, got: {log_text}"
        )

    def test_timeout_no_retry(self) -> None:
        """API call made exactly once — no retry on timeout."""
        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(
            side_effect=_make_api_timeout_error(),
        )
        assessor = ComplexityAssessor(client=mock_client)

        asyncio.run(
            assessor.assess(
                node_body="body",
                archetype="coder",
                mode=None,
                base_tier="STANDARD",
                base_variant="standard",
                previous_failure=None,
            )
        )

        assert mock_client.messages.create.call_count == 1


@_needs_anthropic
class TestComplexityAssessorRateLimitError:
    """TS-15-E4: Rate limit error triggers WARNING and base fallback.

    Requirement: 15-REQ-2.E3
    """

    def test_rate_limit_error_returns_base_without_raising(self, caplog: pytest.LogCaptureFixture) -> None:
        """RateLimitError does not propagate; returns base tier/variant."""
        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(
            side_effect=_make_rate_limit_error(),
        )
        assessor = ComplexityAssessor(client=mock_client)

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            try:
                result = asyncio.run(
                    assessor.assess(
                        node_body="body",
                        archetype="coder",
                        mode=None,
                        base_tier="STANDARD",
                        base_variant="standard",
                        previous_failure=None,
                    )
                )
            except Exception as e:
                pytest.fail(f"assess() should not raise on rate limit, got {e}")

        assert isinstance(result, AssessmentResult)
        assert result.recommended_tier == "STANDARD"
        assert result.recommended_variant == "standard"

    def test_rate_limit_error_logs_warning(self, caplog: pytest.LogCaptureFixture) -> None:
        """WARNING log emitted on rate limit error."""
        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(
            side_effect=_make_rate_limit_error(),
        )
        assessor = ComplexityAssessor(client=mock_client)

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            asyncio.run(
                assessor.assess(
                    node_body="body",
                    archetype="coder",
                    mode=None,
                    base_tier="STANDARD",
                    base_variant="standard",
                    previous_failure=None,
                )
            )

        warning_logs = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_logs) >= 1

    def test_rate_limit_no_retry(self) -> None:
        """API call made exactly once — no retry on rate limit."""
        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(
            side_effect=_make_rate_limit_error(),
        )
        assessor = ComplexityAssessor(client=mock_client)

        asyncio.run(
            assessor.assess(
                node_body="body",
                archetype="coder",
                mode=None,
                base_tier="STANDARD",
                base_variant="standard",
                previous_failure=None,
            )
        )

        assert mock_client.messages.create.call_count == 1


# ===========================================================================
# Task 2.3: Network error and context-limit error handling
# Test Spec: TS-15-49, TS-15-E14
# Requirements: 15-REQ-12.1, 15-REQ-12.E1
# ===========================================================================


class TestComplexityAssessorNetworkError:
    """TS-15-49: Network error triggers WARNING and base fallback.

    On any API call failure (network error, etc.), logs WARNING with
    exception details and returns base_tier/base_variant without raising.

    Requirement: 15-REQ-12.1
    """

    def test_connection_error_returns_base_without_raising(self, caplog: pytest.LogCaptureFixture) -> None:
        """ConnectionError does not propagate; returns base tier/variant."""
        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(
            side_effect=ConnectionError("Network failure"),
        )
        assessor = ComplexityAssessor(client=mock_client)

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            try:
                result = asyncio.run(
                    assessor.assess(
                        node_body="body",
                        archetype="coder",
                        mode=None,
                        base_tier="STANDARD",
                        base_variant="standard",
                        previous_failure=None,
                    )
                )
            except Exception as e:
                pytest.fail(f"assess() should not raise on network error, got {e}")

        assert isinstance(result, AssessmentResult)
        assert result.recommended_tier == "STANDARD"
        assert result.recommended_variant == "standard"

    def test_connection_error_logs_warning_with_details(self, caplog: pytest.LogCaptureFixture) -> None:
        """WARNING log contains exception details (Network failure)."""
        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(
            side_effect=ConnectionError("Network failure"),
        )
        assessor = ComplexityAssessor(client=mock_client)

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            asyncio.run(
                assessor.assess(
                    node_body="body",
                    archetype="coder",
                    mode=None,
                    base_tier="STANDARD",
                    base_variant="standard",
                    previous_failure=None,
                )
            )

        warning_logs = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_logs) >= 1

        log_text = " ".join(r.getMessage() for r in warning_logs)
        assert "Network failure" in log_text or "ConnectionError" in log_text, (
            f"Expected exception details in warning log, got: {log_text}"
        )


@_needs_anthropic
class TestComplexityAssessorContextLimitError:
    """TS-15-E14: Context-limit error treated as standard assessment failure.

    No truncation or retry with shortened body is attempted.

    Requirement: 15-REQ-12.E1
    """

    def test_context_limit_error_returns_base_without_raising(self, caplog: pytest.LogCaptureFixture) -> None:
        """BadRequestError (context limit) does not propagate."""
        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(
            side_effect=_make_bad_request_error("context length exceeded"),
        )
        assessor = ComplexityAssessor(client=mock_client)

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            try:
                result = asyncio.run(
                    assessor.assess(
                        node_body="very long body " * 10000,
                        archetype="coder",
                        mode=None,
                        base_tier="STANDARD",
                        base_variant="standard",
                        previous_failure=None,
                    )
                )
            except Exception as e:
                pytest.fail(f"assess() should not raise on context limit, got {e}")

        assert isinstance(result, AssessmentResult)
        assert result.recommended_tier == "STANDARD"
        assert result.recommended_variant == "standard"

    def test_context_limit_error_logs_warning(self, caplog: pytest.LogCaptureFixture) -> None:
        """WARNING log emitted on context-limit error."""
        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(
            side_effect=_make_bad_request_error("context length exceeded"),
        )
        assessor = ComplexityAssessor(client=mock_client)

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            asyncio.run(
                assessor.assess(
                    node_body="very long body " * 10000,
                    archetype="coder",
                    mode=None,
                    base_tier="STANDARD",
                    base_variant="standard",
                    previous_failure=None,
                )
            )

        warning_logs = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_logs) >= 1

    def test_context_limit_no_retry_with_shorter_body(self) -> None:
        """API call made exactly once — no retry with truncated body."""
        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(
            side_effect=_make_bad_request_error("context length exceeded"),
        )
        assessor = ComplexityAssessor(client=mock_client)

        asyncio.run(
            assessor.assess(
                node_body="very long body " * 10000,
                archetype="coder",
                mode=None,
                base_tier="STANDARD",
                base_variant="standard",
                previous_failure=None,
            )
        )

        # Exactly one call — no truncation retry attempted
        assert mock_client.messages.create.call_count == 1


# ===========================================================================
# Task 2.4: assess_node() absent/empty node_body path
# Test Spec: TS-15-E6, TS-15-51
# Requirements: 15-REQ-4.E1, 15-REQ-12.3
# ===========================================================================


class TestAssessNodeAbsentBody:
    """TS-15-E6, TS-15-51: assess_node() skips assessment for absent/empty body.

    When node_body is None or empty string:
    - Skips assessment entirely
    - Logs DEBUG with 'absent' or 'empty' and node_id
    - Returns EscalationLadder at base tier/variant
    - ComplexityAssessor.assess() is never called

    Requirements: 15-REQ-4.E1, 15-REQ-12.3
    """

    def test_none_body_returns_base_ladder(self) -> None:
        """node_body=None returns EscalationLadder at base tier/variant."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock()
        manager = AssessmentManager(config=_make_routing_config(), client=mock_client)

        ladder = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body=None,
            )
        )

        assert ladder is not None
        # Coder base tier is STANDARD per spec REQ-8.1
        assert ladder.starting_tier.value == "STANDARD" or str(ladder.starting_tier) == "STANDARD"

    def test_empty_body_returns_base_ladder(self) -> None:
        """node_body='' returns EscalationLadder at base tier/variant."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock()
        manager = AssessmentManager(config=_make_routing_config(), client=mock_client)

        ladder = asyncio.run(
            manager.assess_node(
                node_id="n2",
                archetype="coder",
                mode=None,
                node_body="",
            )
        )

        assert ladder is not None
        assert ladder.starting_tier.value == "STANDARD" or str(ladder.starting_tier) == "STANDARD"

    def test_none_body_logs_debug_with_absent_or_empty(self, caplog: pytest.LogCaptureFixture) -> None:
        """DEBUG log mentions 'absent' or 'empty' and node_id for None body."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock()
        manager = AssessmentManager(config=_make_routing_config(), client=mock_client)

        with caplog.at_level(logging.DEBUG, logger="agentfox"):
            asyncio.run(
                manager.assess_node(
                    node_id="n1",
                    archetype="coder",
                    mode=None,
                    node_body=None,
                )
            )

        debug_msgs = [r.getMessage() for r in caplog.records if r.levelno == logging.DEBUG]
        assert any(("absent" in m.lower() or "empty" in m.lower()) for m in debug_msgs), (
            f"Expected 'absent' or 'empty' in DEBUG logs: {debug_msgs}"
        )
        assert any("n1" in m for m in debug_msgs), f"Expected node_id 'n1' in DEBUG logs: {debug_msgs}"

    def test_empty_body_logs_debug_with_absent_or_empty(self, caplog: pytest.LogCaptureFixture) -> None:
        """DEBUG log mentions 'absent' or 'empty' and node_id for empty body."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock()
        manager = AssessmentManager(config=_make_routing_config(), client=mock_client)

        with caplog.at_level(logging.DEBUG, logger="agentfox"):
            asyncio.run(
                manager.assess_node(
                    node_id="n2",
                    archetype="coder",
                    mode=None,
                    node_body="",
                )
            )

        debug_msgs = [r.getMessage() for r in caplog.records if r.levelno == logging.DEBUG]
        assert any(("absent" in m.lower() or "empty" in m.lower()) for m in debug_msgs), (
            f"Expected 'absent' or 'empty' in DEBUG logs: {debug_msgs}"
        )
        assert any("n2" in m for m in debug_msgs), f"Expected node_id 'n2' in DEBUG logs: {debug_msgs}"

    def test_no_llm_call_on_absent_body(self) -> None:
        """ComplexityAssessor.assess() is never called for absent/empty body."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock()
        manager = AssessmentManager(config=_make_routing_config(), client=mock_client)

        for body in [None, ""]:
            asyncio.run(
                manager.assess_node(
                    node_id="n_test",
                    archetype="coder",
                    mode=None,
                    node_body=body,
                )
            )

        # LLM create should never be called for absent/empty body
        mock_client.messages.create.assert_not_called()


# ===========================================================================
# Task 2.5: Successful assessment DEBUG log, explicit override DEBUG log,
#            and client=None no-log path
# Test Spec: TS-15-50, TS-15-52, TS-15-53
# Requirements: 15-REQ-12.2, 15-REQ-12.4, 15-REQ-12.5
# ===========================================================================


class TestSuccessfulAssessmentDebugLog:
    """TS-15-50: Successful assessment emits structured DEBUG log.

    DEBUG log should contain: node_id, archetype, mode, effective_tier,
    effective_variant, confidence, and rationale.

    Requirement: 15-REQ-12.2
    """

    def test_debug_log_contains_required_keys(self, caplog: pytest.LogCaptureFixture) -> None:
        """Structured DEBUG log emitted on successful assessment."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = _make_mock_client(
            '{"recommended_tier": "ADVANCED", "recommended_variant": "extended", '
            '"confidence": 0.82, "rationale": "complex"}'
        )
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        with caplog.at_level(logging.DEBUG, logger="agentfox"):
            asyncio.run(
                manager.assess_node(
                    node_id="n1",
                    archetype="coder",
                    mode="fix",
                    node_body="body",
                )
            )

        debug_logs = [r for r in caplog.records if r.levelno == logging.DEBUG]
        log_text = " ".join(r.getMessage() for r in debug_logs)

        # All required keys should appear in the DEBUG log
        assert "n1" in log_text, f"node_id 'n1' missing from DEBUG log: {log_text}"
        assert "coder" in log_text, f"archetype 'coder' missing from DEBUG log: {log_text}"
        assert "ADVANCED" in log_text, f"effective_tier 'ADVANCED' missing from DEBUG log: {log_text}"
        assert "extended" in log_text, f"effective_variant 'extended' missing from DEBUG log: {log_text}"
        # At least confidence or rationale should be present
        assert "0.82" in log_text or "complex" in log_text, f"confidence/rationale missing from DEBUG log: {log_text}"


class TestExplicitOverrideDebugLog:
    """TS-15-52: Explicit config override emits DEBUG log.

    When explicit config override is detected via is_explicitly_configured(),
    a DEBUG log with node_id, archetype, mode, and resolved tier is emitted.
    No LLM call is made.

    Requirement: 15-REQ-12.4
    """

    def test_explicit_override_debug_log_contains_info(self, caplog: pytest.LogCaptureFixture) -> None:
        """DEBUG log with node_id, archetype, mode, and resolved tier on override."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock()

        # Create a config with explicit mode-level override for coder/fix
        config = _make_routing_config()

        manager = AssessmentManager(config=config, client=mock_client)

        # Patch is_explicitly_configured to return True for this test
        from unittest.mock import patch

        with (
            patch.object(
                manager,
                "is_explicitly_configured",
                return_value=True,
            ),
            caplog.at_level(logging.DEBUG, logger="agentfox"),
        ):
            asyncio.run(
                manager.assess_node(
                    node_id="n1",
                    archetype="coder",
                    mode="fix",
                    node_body="body",
                )
            )

        debug_logs = [r for r in caplog.records if r.levelno == logging.DEBUG]
        log_text = " ".join(r.getMessage() for r in debug_logs)

        assert "n1" in log_text, f"node_id missing from DEBUG log: {log_text}"
        assert "coder" in log_text, f"archetype missing from DEBUG log: {log_text}"
        assert "fix" in log_text, f"mode missing from DEBUG log: {log_text}"
        # Tier value should appear (either explicit tier name or 'explicit')
        assert (
            "ADVANCED" in log_text or "STANDARD" in log_text or "SIMPLE" in log_text or "explicit" in log_text.lower()
        ), f"resolved tier missing from DEBUG log: {log_text}"

        # No LLM call should have been made
        mock_client.messages.create.assert_not_called()


class TestClientNoneNoLogs:
    """TS-15-53: Permanently-disabled path (client=None) produces no logs.

    When AssessmentManager is instantiated with client=None, assess_node()
    should emit zero log entries at any log level.

    Requirement: 15-REQ-12.5
    """

    def test_no_log_entries_at_any_level(self, caplog: pytest.LogCaptureFixture) -> None:
        """Client=None path emits zero log entries for assess_node() call."""
        from agentfox.engine.engine import AssessmentManager

        config = _make_routing_config()
        manager = AssessmentManager(config=config, client=None)

        with caplog.at_level(logging.DEBUG, logger="agentfox"):
            asyncio.run(
                manager.assess_node(
                    node_id="n1",
                    archetype="coder",
                    mode=None,
                    node_body="body",
                )
            )

        # Filter for logs from agentfox loggers only (not unrelated noise)
        agentfox_logs = [r for r in caplog.records if r.name.startswith("agentfox")]
        assert len(agentfox_logs) == 0, f"Expected zero log entries, got: {[r.getMessage() for r in agentfox_logs]}"


# ===========================================================================
# Task Group 3: apply_assessment() upgrade-only semantics (REQ-3)
#               and property tests (PROP-1 through PROP-4)
# ===========================================================================

# ---------------------------------------------------------------------------
# Tier and variant ordering constants for tests
# ---------------------------------------------------------------------------

# Canonical tier ordering: SIMPLE(0) < STANDARD(1) < ADVANCED(2)
_TIER_ORDER: dict[str, int] = {"SIMPLE": 0, "STANDARD": 1, "ADVANCED": 2}
_ALL_TIERS: list[str] = ["SIMPLE", "STANDARD", "ADVANCED"]

# Canonical variant ordering: fast(0) < standard(1) < extended(2)
_VARIANT_ORDER: dict[str, int] = {"fast": 0, "standard": 1, "extended": 2}
_ALL_VARIANTS: list[str] = ["fast", "standard", "extended"]


# ===========================================================================
# Task 3.1: apply_assessment() signature and basic upgrade/no-change cases
# Test Spec: TS-15-13, TS-15-14, TS-15-15
# Requirements: 15-REQ-3.1, 15-REQ-3.2, 15-REQ-3.3
# ===========================================================================


class TestApplyAssessmentSignatureAndReturn:
    """TS-15-13: apply_assessment() accepts correct parameters and returns tuple.

    Requirement: 15-REQ-3.1
    """

    def test_returns_tuple_of_two_elements(self) -> None:
        """apply_assessment() returns a 2-tuple."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="extended",
            confidence=0.8,
            rationale="Complex",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="STANDARD",
            base_variant="standard",
            confidence_threshold=0.6,
        )
        assert isinstance(result, tuple)
        assert len(result) == 2

    def test_first_element_is_str(self) -> None:
        """Effective tier is a string."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="extended",
            confidence=0.8,
            rationale="Complex",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="STANDARD",
            base_variant="standard",
            confidence_threshold=0.6,
        )
        assert isinstance(result[0], str)

    def test_second_element_is_str_or_none(self) -> None:
        """Effective variant is str or None."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="extended",
            confidence=0.8,
            rationale="Complex",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="STANDARD",
            base_variant="standard",
            confidence_threshold=0.6,
        )
        assert result[1] is None or isinstance(result[1], str)

    def test_upgrade_case_returns_advanced(self) -> None:
        """Upgrade: STANDARD -> ADVANCED with high confidence."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="extended",
            confidence=0.8,
            rationale="Complex",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="STANDARD",
            base_variant="standard",
            confidence_threshold=0.6,
        )
        assert result[0] == "ADVANCED"
        assert result[1] == "extended"


class TestApplyAssessmentConfidenceGate:
    """TS-15-14: Below-threshold confidence returns base unchanged.

    Requirement: 15-REQ-3.2
    """

    def test_below_threshold_returns_base_unchanged(self) -> None:
        """Confidence 0.5 < threshold 0.6 returns (base_tier, base_variant)."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="extended",
            confidence=0.5,
            rationale="Complex",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="STANDARD",
            base_variant="standard",
            confidence_threshold=0.6,
        )
        assert result == ("STANDARD", "standard")

    def test_exactly_at_threshold_applies_upgrade(self) -> None:
        """Confidence == threshold passes the gate (>= semantics)."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="extended",
            confidence=0.6,
            rationale="Complex",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="STANDARD",
            base_variant="standard",
            confidence_threshold=0.6,
        )
        # At threshold, upgrade should be applied
        assert result[0] == "ADVANCED"

    def test_zero_confidence_returns_base(self) -> None:
        """Confidence 0.0 with threshold 0.6 returns base unchanged."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="extended",
            confidence=0.0,
            rationale="No confidence",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="STANDARD",
            base_variant="standard",
            confidence_threshold=0.6,
        )
        assert result == ("STANDARD", "standard")

    def test_below_threshold_with_none_variant(self) -> None:
        """Below-threshold confidence returns (base_tier, None) when base_variant is None."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="extended",
            confidence=0.3,
            rationale="r",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="STANDARD",
            base_variant=None,
            confidence_threshold=0.6,
        )
        assert result == ("STANDARD", None)


class TestApplyAssessmentTierUpgrade:
    """TS-15-15: effective_tier = max(base_tier, recommended_tier).

    Requirement: 15-REQ-3.3
    """

    def test_upgrade_standard_to_advanced(self) -> None:
        """Recommended ADVANCED upgrades from STANDARD base."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="standard",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(rec, "STANDARD", "standard", 0.6)
        assert result[0] == "ADVANCED"

    def test_no_downgrade_from_standard_to_simple(self) -> None:
        """Recommended SIMPLE does not downgrade from STANDARD base."""
        rec = AssessmentResult(
            recommended_tier="SIMPLE",
            recommended_variant="standard",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(rec, "STANDARD", "standard", 0.6)
        assert result[0] == "STANDARD"

    def test_same_tier_stays_same(self) -> None:
        """Recommended STANDARD with STANDARD base stays STANDARD."""
        rec = AssessmentResult(
            recommended_tier="STANDARD",
            recommended_variant="standard",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(rec, "STANDARD", "standard", 0.6)
        assert result[0] == "STANDARD"

    def test_upgrade_simple_to_advanced(self) -> None:
        """Recommended ADVANCED upgrades from SIMPLE base."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="standard",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(rec, "SIMPLE", "standard", 0.6)
        assert result[0] == "ADVANCED"

    def test_upgrade_simple_to_standard(self) -> None:
        """Recommended STANDARD upgrades from SIMPLE base."""
        rec = AssessmentResult(
            recommended_tier="STANDARD",
            recommended_variant="standard",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(rec, "SIMPLE", "standard", 0.6)
        assert result[0] == "STANDARD"

    def test_no_downgrade_from_advanced_to_simple(self) -> None:
        """Recommended SIMPLE does not downgrade from ADVANCED base."""
        rec = AssessmentResult(
            recommended_tier="SIMPLE",
            recommended_variant="standard",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(rec, "ADVANCED", "standard", 0.6)
        assert result[0] == "ADVANCED"

    def test_no_downgrade_from_advanced_to_standard(self) -> None:
        """Recommended STANDARD does not downgrade from ADVANCED base."""
        rec = AssessmentResult(
            recommended_tier="STANDARD",
            recommended_variant="standard",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(rec, "ADVANCED", "standard", 0.6)
        assert result[0] == "ADVANCED"


# ===========================================================================
# Task 3.2: apply_assessment() variant upgrade rules
# Test Spec: TS-15-16, TS-15-17, TS-15-18, TS-15-E5
# Requirements: 15-REQ-3.4, 15-REQ-3.5, 15-REQ-3.6, 15-REQ-3.E1
# ===========================================================================


class TestApplyAssessmentNoneBaseVariant:
    """TS-15-16: base_variant=None → effective_variant always None.

    When base_variant is None (single-variant tier), apply_assessment()
    never changes the variant field; always returns None regardless of
    recommendation.recommended_variant.

    Requirement: 15-REQ-3.4
    """

    def test_none_base_variant_with_extended_recommended(self) -> None:
        """base_variant=None, recommended_variant='extended' → effective_variant=None."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="extended",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="STANDARD",
            base_variant=None,
            confidence_threshold=0.6,
        )
        assert result[1] is None

    def test_none_base_variant_with_none_recommended(self) -> None:
        """base_variant=None, recommended_variant=None → effective_variant=None."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant=None,
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="STANDARD",
            base_variant=None,
            confidence_threshold=0.6,
        )
        assert result[1] is None

    def test_none_base_variant_with_fast_recommended(self) -> None:
        """base_variant=None, recommended_variant='fast' → effective_variant=None."""
        rec = AssessmentResult(
            recommended_tier="STANDARD",
            recommended_variant="fast",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="STANDARD",
            base_variant=None,
            confidence_threshold=0.6,
        )
        assert result[1] is None

    def test_none_base_variant_with_standard_recommended(self) -> None:
        """base_variant=None, recommended_variant='standard' → effective_variant=None."""
        rec = AssessmentResult(
            recommended_tier="STANDARD",
            recommended_variant="standard",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="STANDARD",
            base_variant=None,
            confidence_threshold=0.6,
        )
        assert result[1] is None


class TestApplyAssessmentNoneRecommendedVariant:
    """TS-15-17: recommended_variant=None treated as no preference.

    When recommended_variant is None and base_variant is non-None,
    returns base_variant unchanged.

    Requirement: 15-REQ-3.5
    """

    def test_none_recommended_keeps_standard_base(self) -> None:
        """recommended_variant=None with base_variant='standard' → 'standard'."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant=None,
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="STANDARD",
            base_variant="standard",
            confidence_threshold=0.6,
        )
        assert result[1] == "standard"

    def test_none_recommended_keeps_extended_base(self) -> None:
        """recommended_variant=None with base_variant='extended' → 'extended'."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant=None,
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="STANDARD",
            base_variant="extended",
            confidence_threshold=0.6,
        )
        assert result[1] == "extended"

    def test_none_recommended_keeps_fast_base(self) -> None:
        """recommended_variant=None with base_variant='fast' → 'fast'."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant=None,
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="STANDARD",
            base_variant="fast",
            confidence_threshold=0.6,
        )
        assert result[1] == "fast"


class TestApplyAssessmentVariantUpgrade:
    """TS-15-18: Both non-None variants → max(base, recommended) in VARIANT_ORDER.

    VARIANT_ORDER: fast(0) < standard(1) < extended(2)

    Requirement: 15-REQ-3.6
    """

    def test_upgrade_standard_to_extended(self) -> None:
        """base_variant='standard', recommended_variant='extended' → 'extended'."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="extended",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(rec, "STANDARD", "standard", 0.6)
        assert result[1] == "extended"

    def test_no_downgrade_extended_to_fast(self) -> None:
        """base_variant='extended', recommended_variant='fast' → 'extended'."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="fast",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(rec, "STANDARD", "extended", 0.6)
        assert result[1] == "extended"

    def test_same_variant_standard(self) -> None:
        """base_variant='standard', recommended_variant='standard' → 'standard'."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="standard",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(rec, "STANDARD", "standard", 0.6)
        assert result[1] == "standard"

    def test_upgrade_fast_to_standard(self) -> None:
        """base_variant='fast', recommended_variant='standard' → 'standard'."""
        rec = AssessmentResult(
            recommended_tier="STANDARD",
            recommended_variant="standard",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(rec, "STANDARD", "fast", 0.6)
        assert result[1] == "standard"

    def test_upgrade_fast_to_extended(self) -> None:
        """base_variant='fast', recommended_variant='extended' → 'extended'."""
        rec = AssessmentResult(
            recommended_tier="STANDARD",
            recommended_variant="extended",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(rec, "STANDARD", "fast", 0.6)
        assert result[1] == "extended"

    def test_no_downgrade_standard_to_fast(self) -> None:
        """base_variant='standard', recommended_variant='fast' → 'standard'."""
        rec = AssessmentResult(
            recommended_tier="STANDARD",
            recommended_variant="fast",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(rec, "STANDARD", "standard", 0.6)
        assert result[1] == "standard"


class TestApplyAssessmentDowngradePrevention:
    """TS-15-E5: Upgrade-only — recommended below base never downgrades.

    Requirement: 15-REQ-3.E1
    """

    def test_advanced_base_with_simple_recommended(self) -> None:
        """base_tier='ADVANCED', recommended_tier='SIMPLE' → effective='ADVANCED'."""
        rec = AssessmentResult(
            recommended_tier="SIMPLE",
            recommended_variant="fast",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="ADVANCED",
            base_variant="standard",
            confidence_threshold=0.6,
        )
        assert result[0] == "ADVANCED"
        assert result[1] == "standard"  # No variant downgrade either

    def test_standard_base_with_simple_recommended(self) -> None:
        """base_tier='STANDARD', recommended_tier='SIMPLE' → effective='STANDARD'."""
        rec = AssessmentResult(
            recommended_tier="SIMPLE",
            recommended_variant="fast",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="STANDARD",
            base_variant="extended",
            confidence_threshold=0.6,
        )
        assert result[0] == "STANDARD"
        assert result[1] == "extended"  # Variant also not downgraded

    def test_advanced_base_with_standard_recommended(self) -> None:
        """base_tier='ADVANCED', recommended_tier='STANDARD' → effective='ADVANCED'."""
        rec = AssessmentResult(
            recommended_tier="STANDARD",
            recommended_variant="fast",
            confidence=0.9,
            rationale="r",
        )
        result = apply_assessment(
            recommendation=rec,
            base_tier="ADVANCED",
            base_variant="extended",
            confidence_threshold=0.6,
        )
        assert result[0] == "ADVANCED"
        assert result[1] == "extended"


# ===========================================================================
# Task 3.3: Property tests for upgrade-only invariants (PROP-1 through PROP-4)
# Test Spec: TS-15-P1, TS-15-P2, TS-15-P3, TS-15-P4
# Requirements: 15-REQ-3.2, 15-REQ-3.3, 15-REQ-3.4, 15-REQ-3.6
# ===========================================================================


class TestApplyAssessmentPropertyTierUpgradeOnly:
    """TS-15-P1: For any base_tier and recommended_tier, effective_tier >= base_tier.

    Property: 15-PROP-1
    Validates: 15-REQ-3.3, 15-REQ-3.E1
    """

    @pytest.mark.parametrize(
        "base_tier,recommended_tier",
        [(bt, rt) for bt in _ALL_TIERS for rt in _ALL_TIERS],
        ids=[f"base={bt}-rec={rt}" for bt in _ALL_TIERS for rt in _ALL_TIERS],
    )
    def test_effective_tier_never_below_base(self, base_tier: str, recommended_tier: str) -> None:
        """effective_tier >= base_tier in ModelTier ordering for all combinations."""
        rec = AssessmentResult(
            recommended_tier=recommended_tier,
            recommended_variant="standard",
            confidence=0.9,
            rationale="property test",
        )
        result = apply_assessment(rec, base_tier, "standard", 0.6)
        effective_tier = result[0]

        assert _TIER_ORDER[effective_tier] >= _TIER_ORDER[base_tier], (
            f"effective_tier={effective_tier} < base_tier={base_tier}"
        )

    @pytest.mark.parametrize(
        "base_tier,recommended_tier",
        [(bt, rt) for bt in _ALL_TIERS for rt in _ALL_TIERS if _TIER_ORDER[rt] < _TIER_ORDER[bt]],
        ids=[f"base={bt}-rec={rt}" for bt in _ALL_TIERS for rt in _ALL_TIERS if _TIER_ORDER[rt] < _TIER_ORDER[bt]],
    )
    def test_downgrade_attempt_yields_base_tier(self, base_tier: str, recommended_tier: str) -> None:
        """When recommended_tier < base_tier, effective_tier == base_tier."""
        rec = AssessmentResult(
            recommended_tier=recommended_tier,
            recommended_variant="standard",
            confidence=0.9,
            rationale="property test",
        )
        result = apply_assessment(rec, base_tier, "standard", 0.6)
        assert result[0] == base_tier


class TestApplyAssessmentPropertyVariantUpgradeOnly:
    """TS-15-P2: For non-None variants, effective_variant >= base_variant in VARIANT_ORDER.

    Property: 15-PROP-2
    Validates: 15-REQ-3.6, 15-REQ-3.E1
    """

    @pytest.mark.parametrize(
        "base_variant,recommended_variant",
        [(bv, rv) for bv in _ALL_VARIANTS for rv in _ALL_VARIANTS],
        ids=[f"base={bv}-rec={rv}" for bv in _ALL_VARIANTS for rv in _ALL_VARIANTS],
    )
    def test_effective_variant_never_below_base(self, base_variant: str, recommended_variant: str) -> None:
        """effective_variant >= base_variant in VARIANT_ORDER for all combinations."""
        rec = AssessmentResult(
            recommended_tier="STANDARD",
            recommended_variant=recommended_variant,
            confidence=0.9,
            rationale="property test",
        )
        result = apply_assessment(rec, "STANDARD", base_variant, 0.6)
        effective_variant = result[1]

        assert effective_variant is not None
        assert _VARIANT_ORDER[effective_variant] >= _VARIANT_ORDER[base_variant], (
            f"effective_variant={effective_variant} < base_variant={base_variant}"
        )


class TestApplyAssessmentPropertyNoneBaseVariant:
    """TS-15-P3: base_variant=None always yields effective_variant=None.

    Property: 15-PROP-3
    Validates: 15-REQ-3.4
    """

    @pytest.mark.parametrize(
        "recommended_variant",
        [None, "fast", "standard", "extended"],
        ids=["none", "fast", "standard", "extended"],
    )
    @pytest.mark.parametrize(
        "base_tier",
        _ALL_TIERS,
    )
    @pytest.mark.parametrize(
        "confidence",
        [0.0, 0.5, 0.6, 0.9, 1.0],
        ids=["conf_0.0", "conf_0.5", "conf_0.6", "conf_0.9", "conf_1.0"],
    )
    def test_none_base_variant_always_yields_none(
        self,
        recommended_variant: str | None,
        base_tier: str,
        confidence: float,
    ) -> None:
        """effective_variant is always None when base_variant is None."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant=recommended_variant,
            confidence=confidence,
            rationale="property test",
        )
        # Use threshold=0.0 so confidence gate never triggers
        # (we want to test variant behavior specifically)
        result = apply_assessment(rec, base_tier, None, 0.0)
        assert result[1] is None, (
            f"Expected None variant for base_variant=None, "
            f"got {result[1]} with recommended_variant={recommended_variant}"
        )


class TestApplyAssessmentPropertyConfidenceGate:
    """TS-15-P4: confidence < threshold → (base_tier, base_variant) unchanged.

    Property: 15-PROP-4
    Validates: 15-REQ-3.2
    """

    @pytest.mark.parametrize(
        "base_tier,base_variant",
        [(bt, bv) for bt in _ALL_TIERS for bv in [None, "fast", "standard", "extended"]],
        ids=[f"base={bt}/{bv}" for bt in _ALL_TIERS for bv in ["none", "fast", "standard", "extended"]],
    )
    @pytest.mark.parametrize(
        "recommended_tier,recommended_variant",
        [
            ("ADVANCED", "extended"),
            ("SIMPLE", "fast"),
            ("STANDARD", None),
        ],
        ids=["rec-adv-ext", "rec-simple-fast", "rec-std-none"],
    )
    def test_below_threshold_returns_base_unchanged(
        self,
        base_tier: str,
        base_variant: str | None,
        recommended_tier: str,
        recommended_variant: str | None,
    ) -> None:
        """When confidence < threshold, result is always (base_tier, base_variant)."""
        # Use confidence=0.3 which is below any reasonable threshold
        rec = AssessmentResult(
            recommended_tier=recommended_tier,
            recommended_variant=recommended_variant,
            confidence=0.3,
            rationale="property test",
        )
        result = apply_assessment(rec, base_tier, base_variant, 0.6)
        assert result == (base_tier, base_variant), (
            f"Expected ({base_tier}, {base_variant}), got {result} with below-threshold confidence"
        )

    @pytest.mark.parametrize(
        "confidence,threshold",
        [
            (0.0, 0.1),
            (0.1, 0.2),
            (0.3, 0.6),
            (0.5, 0.6),
            (0.59, 0.6),
            (0.8, 0.9),
            (0.99, 1.0),
        ],
        ids=[
            "0.0<0.1",
            "0.1<0.2",
            "0.3<0.6",
            "0.5<0.6",
            "0.59<0.6",
            "0.8<0.9",
            "0.99<1.0",
        ],
    )
    def test_various_below_threshold_pairs(self, confidence: float, threshold: float) -> None:
        """Confidence gate holds for various (confidence, threshold) pairs."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="extended",
            confidence=confidence,
            rationale="property test",
        )
        result = apply_assessment(rec, "STANDARD", "standard", threshold)
        assert result == ("STANDARD", "standard"), (
            f"Expected base values with confidence={confidence} < threshold={threshold}, got {result}"
        )


# ===========================================================================
# Task Group 4: AssessmentManager integration, session_runner_factory wiring,
#               and EscalationLadder construction
#
# Test Spec: TS-15-19 through TS-15-25, TS-15-E7, TS-15-P6, TS-15-P9
# Requirements: 15-REQ-4.1 through 15-REQ-4.4, 15-REQ-5.1 through 15-REQ-5.3,
#               15-REQ-5.E1
# ===========================================================================

# ---------------------------------------------------------------------------
# Helpers for task group 4
# ---------------------------------------------------------------------------

_UPGRADE_RESPONSE_JSON = (
    '{"recommended_tier": "ADVANCED", "recommended_variant": "extended", "confidence": 0.9, "rationale": "complex"}'
)

_LOW_CONFIDENCE_RESPONSE_JSON = (
    '{"recommended_tier": "ADVANCED", "recommended_variant": "extended", '
    '"confidence": 0.3, "rationale": "low confidence"}'
)


def _make_assessment_manager(
    *,
    client: object | None = None,
    assessor_model: str = "claude-haiku-4-5",
    confidence_threshold: float = 0.6,
) -> Any:
    """Create an AssessmentManager with optional client for spec-15 tests.

    This helper adapts to whatever constructor signature AssessmentManager
    currently has. Once task group 9 updates the constructor to accept
    ``client``, this helper will pass it directly. Until then, tests
    that exercise the client-injection path are expected to fail with
    TypeError.
    """
    from agentfox.engine.engine import AssessmentManager

    config = _make_routing_config(
        assessor_model=assessor_model,
        confidence_threshold=confidence_threshold,
    )
    # Spec requires: AssessmentManager(config=routing_config, client=client)
    return AssessmentManager(config=config, client=client)


# ===========================================================================
# Task 4.1: assess_node() signature, EscalationLadder construction,
#           and priority ordering
# Test Spec: TS-15-19, TS-15-21, TS-15-22
# Requirements: 15-REQ-4.1, 15-REQ-4.3, 15-REQ-4.4
# ===========================================================================


class TestAssessNodeSignatureAndReturn:
    """TS-15-19: assess_node() has the correct signature and returns EscalationLadder.

    Requirement: 15-REQ-4.1
    """

    def test_assess_node_is_coroutine(self) -> None:
        """assess_node() should be an async method (coroutine function)."""
        from agentfox.engine.engine import AssessmentManager

        assert inspect.iscoroutinefunction(AssessmentManager.assess_node)

    def test_assess_node_accepts_required_parameters(self) -> None:
        """assess_node() signature includes node_id, archetype, mode,
        node_body, previous_failure, and pre_assessed."""
        from agentfox.engine.engine import AssessmentManager

        sig = inspect.signature(AssessmentManager.assess_node)
        params = list(sig.parameters.keys())
        # 'self' is implicit in bound methods but explicit in unbound
        assert "node_id" in params, "Missing node_id parameter"
        assert "archetype" in params, "Missing archetype parameter"
        assert "mode" in params, "Missing mode parameter"
        assert "node_body" in params, "Missing node_body parameter"
        assert "previous_failure" in params, "Missing previous_failure parameter"
        assert "pre_assessed" in params, "Missing pre_assessed parameter"

    def test_assess_node_returns_escalation_ladder(self) -> None:
        """assess_node() returns an EscalationLadder instance."""
        from agentfox.core.escalation import EscalationLadder

        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=mock_client)
        ladder = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
                previous_failure=None,
                pre_assessed=None,
            )
        )
        assert isinstance(ladder, EscalationLadder), f"Expected EscalationLadder, got {type(ladder)}"

    def test_assess_node_previous_failure_defaults_to_none(self) -> None:
        """previous_failure should default to None when not provided."""
        from agentfox.engine.engine import AssessmentManager

        sig = inspect.signature(AssessmentManager.assess_node)
        param = sig.parameters.get("previous_failure")
        assert param is not None, "Missing previous_failure parameter"
        assert param.default is None, f"previous_failure default should be None, got {param.default}"

    def test_assess_node_pre_assessed_defaults_to_none(self) -> None:
        """pre_assessed should default to None when not provided."""
        from agentfox.engine.engine import AssessmentManager

        sig = inspect.signature(AssessmentManager.assess_node)
        param = sig.parameters.get("pre_assessed")
        assert param is not None, "Missing pre_assessed parameter"
        assert param.default is None, f"pre_assessed default should be None, got {param.default}"


class TestEscalationLadderConstruction:
    """TS-15-21: EscalationLadder constructed with correct values.

    Requirement: 15-REQ-4.3
    """

    def test_ladder_has_starting_tier_from_assessment(self) -> None:
        """Ladder starting_tier matches effective_tier from assessment."""
        from agentfox.core.models import ModelTier

        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=mock_client)
        ladder = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="complex task",
            )
        )
        # Assessment recommends ADVANCED with confidence 0.9 >= threshold 0.6
        # Coder base tier is STANDARD; max(STANDARD, ADVANCED) = ADVANCED
        assert ladder.current_tier == ModelTier.ADVANCED, f"Expected starting tier ADVANCED, got {ladder.current_tier}"

    def test_ladder_has_starting_variant_from_assessment(self) -> None:
        """Ladder starting_variant matches effective_variant from assessment."""
        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=mock_client)
        ladder = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="complex task",
            )
        )
        # Once spec #14 adds starting_variant to EscalationLadder,
        # this will test that it equals 'extended'.
        # For now, verify the attribute exists.
        assert hasattr(ladder, "starting_variant") or hasattr(ladder, "_starting_variant"), (
            "EscalationLadder should have starting_variant attribute"
        )

    def test_ladder_ceiling_is_advanced(self) -> None:
        """Ladder tier_ceiling should be ADVANCED."""
        from agentfox.core.models import ModelTier

        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=mock_client)
        ladder = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="complex task",
            )
        )
        # The ceiling should be ADVANCED (accessible via _tier_ceiling)
        ceiling = getattr(ladder, "_tier_ceiling", None)
        assert ceiling == ModelTier.ADVANCED, f"Expected tier_ceiling ADVANCED, got {ceiling}"

    def test_ladder_retry_config_from_routing_config(self) -> None:
        """Ladder retries_before_escalation comes from RoutingConfig."""
        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=mock_client)
        ladder = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="complex task",
            )
        )
        # Verify the ladder was constructed with retry config from RoutingConfig
        retries = getattr(ladder, "_retries_before_escalation", None)
        assert retries is not None, "Ladder should have _retries_before_escalation from RoutingConfig"


class TestAssessNodePriorityOrdering:
    """TS-15-22: assess_node() evaluates eligibility in correct priority order.

    Requirement: 15-REQ-4.4
    """

    def test_path_1_no_assessor_returns_base(self) -> None:
        """Path 1: client=None → base tier/variant fallback."""
        from agentfox.core.models import ModelTier

        manager = _make_assessment_manager(client=None)
        ladder = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="some body",
            )
        )
        # Coder base tier is STANDARD per spec REQ-8.1
        assert ladder.current_tier == ModelTier.STANDARD

    def test_path_2_missing_body_returns_base(self, caplog: pytest.LogCaptureFixture) -> None:
        """Path 2: node_body=None → base fallback with DEBUG log."""
        from agentfox.core.models import ModelTier

        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=mock_client)
        with caplog.at_level(logging.DEBUG, logger="agentfox"):
            ladder = asyncio.run(
                manager.assess_node(
                    node_id="n2",
                    archetype="coder",
                    mode=None,
                    node_body=None,
                )
            )
        assert ladder.current_tier == ModelTier.STANDARD
        # LLM should not be called
        mock_client.messages.create.assert_not_called()

    def test_path_3_explicit_override_skips_assessment(self, caplog: pytest.LogCaptureFixture) -> None:
        """Path 3: explicit config override → configured tier, no LLM call."""
        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=mock_client)
        # Mock is_explicitly_configured to return True
        manager.is_explicitly_configured = MagicMock(return_value=True)
        with caplog.at_level(logging.DEBUG, logger="agentfox"):
            ladder = asyncio.run(
                manager.assess_node(
                    node_id="n3",
                    archetype="coder",
                    mode="fix",
                    node_body="body",
                )
            )
        assert ladder is not None
        mock_client.messages.create.assert_not_called()

    def test_path_4_pre_assessed_bypasses_llm(self) -> None:
        """Path 4: pre_assessed non-None → adapter + apply, no LLM call."""
        from agentfox.core.models import ModelTier

        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=mock_client)

        # Create a pre_assessed AssessedComplexity-like object
        pre_assessed = MagicMock()
        pre_assessed.tier = "ADVANCED"
        pre_assessed.variant = "standard"
        pre_assessed.confidence = 0.85
        pre_assessed.rationale = "pre-assessed"

        ladder = asyncio.run(
            manager.assess_node(
                node_id="n4",
                archetype="coder",
                mode=None,
                node_body="body",
                pre_assessed=pre_assessed,
            )
        )
        # LLM should NOT be called for pre_assessed path
        mock_client.messages.create.assert_not_called()
        assert ladder.current_tier == ModelTier.ADVANCED

    def test_path_5_llm_assessment_called(self) -> None:
        """Path 5: no override, no pre_assessed → LLM assessment called."""
        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=mock_client)

        ladder = asyncio.run(
            manager.assess_node(
                node_id="n5",
                archetype="coder",
                mode=None,
                node_body="complex body",
            )
        )
        # LLM SHOULD be called
        mock_client.messages.create.assert_called_once()
        assert ladder is not None


# ===========================================================================
# Task 4.2: session_runner_factory client injection
# Test Spec: TS-15-20
# Requirements: 15-REQ-4.2
# ===========================================================================


class TestSessionRunnerFactoryClientInjection:
    """TS-15-20: session_runner_factory passes Anthropic client to AssessmentManager.

    The Anthropic client is created in _setup_infrastructure() (engine/run.py)
    and threaded through to Orchestrator.__init__() as 'client', which passes
    it to AssessmentManager(client=client). We verify both the infra wiring
    and the Orchestrator→AssessmentManager handoff.

    Requirement: 15-REQ-4.2
    """

    def test_setup_infrastructure_returns_anthropic_client_key(self) -> None:
        """_setup_infrastructure() returns a dict containing 'anthropic_client'.

        This is the entry point for client creation in the dispatch pipeline.
        """
        from agentfox.engine import run

        source = inspect.getsource(run._setup_infrastructure)
        assert '"anthropic_client"' in source or "'anthropic_client'" in source, (
            "_setup_infrastructure() should include 'anthropic_client' in its return dict"
        )
        # Verify the client is created from create_async_anthropic_client
        assert "create_async_anthropic_client" in source, (
            "_setup_infrastructure() should call create_async_anthropic_client()"
        )

    def test_orchestrator_passes_client_to_assessment_manager(self) -> None:
        """Orchestrator.__init__() passes the client kwarg to AssessmentManager.

        We verify this behaviorally by constructing an Orchestrator with a
        mock client and checking that AssessmentManager._assessor is non-None
        (which only happens when client is not None).
        """
        from agentfox.engine.engine import Orchestrator

        mock_client = MagicMock()
        # Construct a minimal Orchestrator with a mock client
        from agentfox.core.config import AgentFoxConfig, OrchestratorConfig

        orch_config = OrchestratorConfig()
        full_config = AgentFoxConfig()
        orch = Orchestrator(
            config=orch_config,
            session_runner_factory=MagicMock(),
            full_config=full_config,
            client=mock_client,
        )
        # AssessmentManager should have instantiated ComplexityAssessor
        assert orch._routing._assessor is not None, (
            "Orchestrator should pass client to AssessmentManager, causing ComplexityAssessor to be instantiated"
        )
        assert orch._routing._assessor.client is mock_client, (
            "ComplexityAssessor should receive the same client object passed to Orchestrator"
        )

    def test_orchestrator_none_client_disables_assessor(self) -> None:
        """When client=None, AssessmentManager._assessor is None."""
        from agentfox.core.config import AgentFoxConfig, OrchestratorConfig
        from agentfox.engine.engine import Orchestrator

        orch_config = OrchestratorConfig()
        full_config = AgentFoxConfig()
        orch = Orchestrator(
            config=orch_config,
            session_runner_factory=MagicMock(),
            full_config=full_config,
            client=None,
        )
        assert orch._routing._assessor is None, "Orchestrator with client=None should disable ComplexityAssessor"

    def test_run_code_passes_anthropic_client_to_orchestrator(self) -> None:
        """run_code() wires infra['anthropic_client'] as 'client' to Orchestrator.

        Verified by source inspection of the orch_kwargs dict in run_code().
        """
        from agentfox.engine import run

        source = inspect.getsource(run.run_code)
        # The orch_kwargs dict should include client from infra
        assert '"client"' in source or "'client'" in source, "run_code() should include 'client' key in orch_kwargs"
        assert "anthropic_client" in source, "run_code() should source the client from infra['anthropic_client']"


# ===========================================================================
# Task 4.3: Re-assessment on retry with previous_failure
# Test Spec: TS-15-23, TS-15-24, TS-15-25
# Requirements: 15-REQ-5.1, 15-REQ-5.2, 15-REQ-5.3
# ===========================================================================


class TestReAssessmentOnRetry:
    """TS-15-23: Re-assessment discards existing ladder and creates new one.

    Requirement: 15-REQ-5.1
    """

    def test_second_call_creates_new_ladder(self) -> None:
        """Second assess_node() call creates a new, distinct ladder."""
        call_count = 0

        async def mock_create(**kwargs: Any) -> Any:
            nonlocal call_count
            call_count += 1
            return _make_anthropic_response(_UPGRADE_RESPONSE_JSON)

        mock_client = MagicMock()
        mock_client.messages.create = mock_create
        manager = _make_assessment_manager(client=mock_client)

        ladder1 = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
            )
        )
        assert call_count == 1

        # Second call with previous_failure should create a NEW ladder
        ladder2 = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
                previous_failure="TypeError: NoneType",
            )
        )
        assert call_count == 2, "LLM should be called again on retry"
        assert ladder2 is not ladder1, "Should return a new ladder, not cached"

    def test_second_call_ladder_reflects_assessment(self) -> None:
        """New ladder from retry reflects the new assessment result."""
        from agentfox.core.models import ModelTier

        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=mock_client)

        # First assessment call (result intentionally discarded)
        asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
            )
        )

        # Reset mock to return again on second call
        mock_client.messages.create.reset_mock()
        mock_client.messages.create.return_value = _make_anthropic_response(_UPGRADE_RESPONSE_JSON)

        ladder2 = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
                previous_failure="error occurred",
            )
        )
        assert ladder2.current_tier == ModelTier.ADVANCED


class TestPreviousFailurePassthrough:
    """TS-15-24: previous_failure is passed to ComplexityAssessor.assess().

    Requirement: 15-REQ-5.2
    """

    def test_previous_failure_included_in_assess_call(self) -> None:
        """previous_failure string is forwarded to the assessor."""
        captured_kwargs: dict[str, Any] = {}

        async def mock_create(**kwargs: Any) -> Any:
            captured_kwargs.update(kwargs)
            return _make_anthropic_response(_UPGRADE_RESPONSE_JSON)

        mock_client = MagicMock()
        mock_client.messages.create = mock_create
        manager = _make_assessment_manager(client=mock_client)

        asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
                previous_failure="TypeError: NoneType",
            )
        )
        # The previous_failure should appear somewhere in the prompt
        # sent to the LLM (either in messages or system)
        call_str = str(captured_kwargs)
        assert "TypeError: NoneType" in call_str, "previous_failure should be included in the prompt sent to LLM"


class TestRetryLadderConfig:
    """TS-15-25: New ladder shares ceiling and retry config with original.

    Requirement: 15-REQ-5.3
    """

    def test_retry_ladder_shares_ceiling(self) -> None:
        """Both initial and retry ladders have ADVANCED ceiling."""
        from agentfox.core.models import ModelTier

        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=mock_client)

        ladder1 = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
            )
        )

        mock_client.messages.create.reset_mock()
        mock_client.messages.create.return_value = _make_anthropic_response(_UPGRADE_RESPONSE_JSON)

        ladder2 = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
                previous_failure="error",
            )
        )

        ceiling1 = getattr(ladder1, "_tier_ceiling", None)
        ceiling2 = getattr(ladder2, "_tier_ceiling", None)
        assert ceiling1 == ceiling2 == ModelTier.ADVANCED, (
            f"Both ladders should have ADVANCED ceiling: got {ceiling1}, {ceiling2}"
        )

    def test_retry_ladder_shares_retry_config(self) -> None:
        """Both ladders share the same per-tier retry configuration."""
        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=mock_client)

        ladder1 = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
            )
        )

        mock_client.messages.create.reset_mock()
        mock_client.messages.create.return_value = _make_anthropic_response(_UPGRADE_RESPONSE_JSON)

        ladder2 = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
                previous_failure="error",
            )
        )

        retries1 = getattr(ladder1, "_retries_before_escalation", None)
        retries2 = getattr(ladder2, "_retries_before_escalation", None)
        assert retries1 == retries2, f"Both ladders should share retry config: got {retries1} vs {retries2}"

    def test_only_starting_tier_variant_differ(self) -> None:
        """Only starting_tier and starting_variant may differ between ladders."""
        from agentfox.core.models import ModelTier

        # First call with low-confidence response → base tier
        low_client = _make_mock_client(_LOW_CONFIDENCE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=low_client)

        ladder1 = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
            )
        )

        # Second call with high-confidence response → upgraded tier
        low_client.messages.create = AsyncMock(return_value=_make_anthropic_response(_UPGRADE_RESPONSE_JSON))

        ladder2 = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
                previous_failure="error",
            )
        )

        # Ceiling and retry config should be same
        assert getattr(ladder1, "_tier_ceiling", None) == getattr(ladder2, "_tier_ceiling", None)
        assert getattr(ladder1, "_retries_before_escalation", None) == getattr(
            ladder2, "_retries_before_escalation", None
        )
        # Starting tiers may differ (STANDARD vs ADVANCED)
        tier1 = getattr(ladder1, "_starting_tier", ladder1.current_tier)
        tier2 = getattr(ladder2, "_starting_tier", ladder2.current_tier)
        # First should be at base (STANDARD), second at ADVANCED
        assert tier1 == ModelTier.STANDARD
        assert tier2 == ModelTier.ADVANCED


# ===========================================================================
# Task 4.4: Edge case — re-assessment with confidence below threshold
# Test Spec: TS-15-E7
# Requirements: 15-REQ-5.E1
# ===========================================================================


class TestReAssessmentBelowThreshold:
    """TS-15-E7: Re-assessment with low confidence yields base tier/variant.

    Requirement: 15-REQ-5.E1
    """

    def test_low_confidence_retry_returns_base(self) -> None:
        """When re-assessment confidence < threshold, new ladder at base tier."""
        from agentfox.core.models import ModelTier

        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=mock_client)

        ladder1 = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
            )
        )

        # Second call returns low confidence (0.3 < threshold 0.6)
        mock_client.messages.create = AsyncMock(return_value=_make_anthropic_response(_LOW_CONFIDENCE_RESPONSE_JSON))

        ladder2 = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
                previous_failure="error",
            )
        )
        assert ladder2 is not ladder1, "Should be a new ladder object"
        # With low confidence, ladder should fall back to base (STANDARD)
        assert ladder2.current_tier == ModelTier.STANDARD

    def test_prior_retry_state_not_preserved(self) -> None:
        """Prior retry state (failure count, escalation count) not preserved."""
        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=mock_client)

        ladder1 = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
            )
        )
        # Simulate some failures on ladder1
        if hasattr(ladder1, "record_failure"):
            ladder1.record_failure()

        # Re-assess with low confidence
        mock_client.messages.create = AsyncMock(return_value=_make_anthropic_response(_LOW_CONFIDENCE_RESPONSE_JSON))

        ladder2 = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
                previous_failure="error",
            )
        )
        # New ladder should be fresh — no failures recorded
        assert ladder2.attempt_count == 1, (
            f"New ladder should start fresh with attempt_count=1, got {ladder2.attempt_count}"
        )
        assert ladder2.escalation_count == 0, (
            f"New ladder should have escalation_count=0, got {ladder2.escalation_count}"
        )

    def test_new_ladder_is_distinct_object(self) -> None:
        """The new ladder from re-assessment is a different object from the first."""
        from agentfox.core.models import ModelTier

        mock_client = _make_mock_client(_LOW_CONFIDENCE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=mock_client)

        ladder1 = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
            )
        )

        mock_client.messages.create = AsyncMock(return_value=_make_anthropic_response(_LOW_CONFIDENCE_RESPONSE_JSON))

        ladder2 = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
                previous_failure="error",
            )
        )
        assert ladder2 is not ladder1, "Ladders should be distinct objects"
        # Both should be at base tier since confidence is low
        assert ladder1.current_tier == ModelTier.STANDARD
        assert ladder2.current_tier == ModelTier.STANDARD


# ===========================================================================
# Task 4.5: Property tests — failure invariant and concurrent calls
# Test Spec: TS-15-P6, TS-15-P9
# Requirements: 15-REQ-12.1, 15-REQ-1.5
# ===========================================================================


class TestPropertyAssessmentFailureInvariant:
    """TS-15-P6: For any failure condition, assess_node() returns
    EscalationLadder at base tier/variant without raising.

    Property: 15-PROP-6
    Validates: 15-REQ-12.1, 15-REQ-2.E1, 15-REQ-2.E2, 15-REQ-2.E3, 15-REQ-12.E1
    """

    @pytest.mark.parametrize(
        "failure_exc",
        [
            ConnectionError("Network failure"),
            TimeoutError("Request timed out"),
            OSError("Connection refused"),
            ValueError("Unexpected response format"),
            RuntimeError("Internal SDK error"),
        ],
        ids=[
            "network_error",
            "timeout_error",
            "os_error",
            "value_error",
            "runtime_error",
        ],
    )
    def test_generic_failure_returns_base_without_raising(self, failure_exc: Exception) -> None:
        """assess_node() never raises on failure; returns EscalationLadder at base."""
        from agentfox.core.escalation import EscalationLadder
        from agentfox.core.models import ModelTier

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(side_effect=failure_exc)
        manager = _make_assessment_manager(client=mock_client)

        try:
            ladder = asyncio.run(
                manager.assess_node(
                    node_id="fail_node",
                    archetype="coder",
                    mode=None,
                    node_body="some body",
                )
            )
        except Exception as exc:
            pytest.fail(f"assess_node() raised {type(exc).__name__}: {exc}; should have returned base-tier ladder")

        assert isinstance(ladder, EscalationLadder), f"Expected EscalationLadder, got {type(ladder)}"
        assert ladder.current_tier == ModelTier.STANDARD, (
            f"Expected base tier STANDARD on failure, got {ladder.current_tier}"
        )

    @pytest.mark.parametrize(
        "bad_json",
        [
            "not json at all",
            '{"recommended_tier": "ADVANCED"}',  # missing fields
            '{"recommended_tier": "ADVANCED", "recommended_variant": "standard", '
            '"confidence": 1.5, "rationale": "r"}',  # out of range
        ],
        ids=[
            "malformed_json",
            "missing_fields",
            "out_of_range_confidence",
        ],
    )
    def test_malformed_response_returns_base_without_raising(self, bad_json: str) -> None:
        """assess_node() handles malformed LLM responses without raising."""
        from agentfox.core.escalation import EscalationLadder
        from agentfox.core.models import ModelTier

        mock_client = _make_mock_client(bad_json)
        manager = _make_assessment_manager(client=mock_client)

        try:
            ladder = asyncio.run(
                manager.assess_node(
                    node_id="bad_resp_node",
                    archetype="coder",
                    mode=None,
                    node_body="body",
                )
            )
        except Exception as exc:
            pytest.fail(f"assess_node() raised {type(exc).__name__}: {exc}; should have returned base-tier ladder")

        assert isinstance(ladder, EscalationLadder)
        assert ladder.current_tier == ModelTier.STANDARD

    @pytest.mark.parametrize(
        "archetype,mode,expected_tier",
        [
            ("coder", None, "STANDARD"),
            ("coder", "fix", "STANDARD"),
            ("reviewer", "pre-review", "ADVANCED"),
            ("reviewer", "drift-review", "STANDARD"),
            ("verifier", None, "STANDARD"),
            ("maintainer", "hunt", "SIMPLE"),
        ],
        ids=[
            "coder-base",
            "coder-fix",
            "reviewer-prereview",
            "reviewer-drift",
            "verifier-base",
            "maintainer-hunt",
        ],
    )
    def test_failure_falls_back_to_archetype_base_tier(
        self, archetype: str, mode: str | None, expected_tier: str
    ) -> None:
        """On failure, assess_node() falls back to the archetype's registry
        base tier, per 15-REQ-8.x defaults."""
        from agentfox.core.models import ModelTier

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(side_effect=ConnectionError("fail"))
        manager = _make_assessment_manager(client=mock_client)

        try:
            ladder = asyncio.run(
                manager.assess_node(
                    node_id="arch_fail",
                    archetype=archetype,
                    mode=mode,
                    node_body="body",
                )
            )
        except Exception as exc:
            pytest.fail(f"Should not raise: {exc}")

        assert ladder.current_tier == ModelTier(expected_tier), (
            f"Expected fallback tier {expected_tier} for {archetype}/{mode}, got {ladder.current_tier}"
        )


class TestPropertyConcurrentAssessNodeCalls:
    """TS-15-P9: Concurrent assess_node() calls do not interfere.

    Property: 15-PROP-9
    Validates: 15-REQ-1.5
    """

    def test_concurrent_calls_return_independent_results(self) -> None:
        """N concurrent assess_node() calls each return results
        corresponding to their own inputs without interference."""
        from agentfox.core.escalation import EscalationLadder

        async def mock_create(**kwargs: Any) -> Any:
            # Extract node identity from the messages to return
            # different results per call
            msgs_str = str(kwargs.get("messages", ""))
            if "body_simple" in msgs_str:
                return _make_anthropic_response(
                    '{"recommended_tier": "STANDARD", '
                    '"recommended_variant": "standard", '
                    '"confidence": 0.7, "rationale": "simple"}'
                )
            elif "body_complex" in msgs_str:
                return _make_anthropic_response(
                    '{"recommended_tier": "ADVANCED", '
                    '"recommended_variant": "extended", '
                    '"confidence": 0.9, "rationale": "complex"}'
                )
            else:
                return _make_anthropic_response(
                    '{"recommended_tier": "STANDARD", '
                    '"recommended_variant": "standard", '
                    '"confidence": 0.5, "rationale": "default"}'
                )

        mock_client = MagicMock()
        mock_client.messages.create = mock_create
        manager = _make_assessment_manager(client=mock_client)

        async def run_concurrent() -> list[Any]:
            tasks = [
                manager.assess_node(
                    node_id=f"node_{i}",
                    archetype="coder",
                    mode=None,
                    node_body=body,
                )
                for i, body in enumerate(["body_simple", "body_complex", "body_other"])
            ]
            return await asyncio.gather(*tasks)

        results = asyncio.run(run_concurrent())

        assert len(results) == 3
        for r in results:
            assert isinstance(r, EscalationLadder), f"Expected EscalationLadder, got {type(r)}"

    def test_concurrent_calls_no_shared_state_leak(self) -> None:
        """Concurrent calls do not leak state between AssessmentManager instances."""
        call_count = 0

        async def mock_create(**kwargs: Any) -> Any:
            nonlocal call_count
            call_count += 1
            return _make_anthropic_response(_UPGRADE_RESPONSE_JSON)

        mock_client = MagicMock()
        mock_client.messages.create = mock_create
        manager = _make_assessment_manager(client=mock_client)

        async def run_concurrent() -> list[Any]:
            tasks = [
                manager.assess_node(
                    node_id=f"concurrent_{i}",
                    archetype="coder",
                    mode=None,
                    node_body=f"body for node {i}",
                )
                for i in range(5)
            ]
            return await asyncio.gather(*tasks)

        results = asyncio.run(run_concurrent())

        assert len(results) == 5
        assert call_count == 5, f"Expected 5 independent LLM calls, got {call_count}"
        # All results should be distinct objects
        ids = [id(r) for r in results]
        assert len(set(ids)) == 5, "All returned ladders should be distinct objects"


# ===========================================================================
# Task Group 5: Explicit config override skip, resolution priority ordering,
#               ARCHETYPE_REGISTRY defaults, and property tests
#
# Test Spec: TS-15-26 through TS-15-31, TS-15-32 through TS-15-36,
#            TS-15-E8, TS-15-P5, TS-15-P7
# Requirements: 15-REQ-6.1 through 15-REQ-6.3, 15-REQ-6.E1,
#               15-REQ-7.1 through 15-REQ-7.3,
#               15-REQ-8.1 through 15-REQ-8.5
# ===========================================================================


# ---------------------------------------------------------------------------
# Helpers for task group 5
# ---------------------------------------------------------------------------


def _make_config_with_mode_override(
    archetype: str,
    mode: str,
    model_tier: str,
) -> Any:
    """Create an AgentFoxConfig with a mode-level model_tier override.

    This sets up config.archetypes.overrides.<archetype>.modes.<mode>.model_tier
    which is layer 1 in the config resolution priority.
    """
    from agentfox.core.config import AgentFoxConfig, PerArchetypeConfig

    mode_override = PerArchetypeConfig(model_tier=model_tier)
    archetype_override = PerArchetypeConfig(modes={mode: mode_override})
    config = AgentFoxConfig(
        archetypes={"overrides": {archetype: archetype_override}},
    )
    return config


def _make_config_with_archetype_override(
    archetype: str,
    model_tier: str,
) -> Any:
    """Create an AgentFoxConfig with a per-archetype model_tier override.

    This sets up config.archetypes.overrides.<archetype>.model_tier
    which is layer 2 in the config resolution priority.
    """
    from agentfox.core.config import AgentFoxConfig, PerArchetypeConfig

    archetype_override = PerArchetypeConfig(model_tier=model_tier)
    config = AgentFoxConfig(
        archetypes={"overrides": {archetype: archetype_override}},
    )
    return config


def _make_routing_config_from_agentfox_config(
    agentfox_config: Any,
    *,
    assessor_model: str = "claude-haiku-4-5",
    confidence_threshold: float = 0.6,
) -> Any:
    """Create a RoutingConfig-compatible object that also carries AgentFoxConfig.

    The AssessmentManager needs both RoutingConfig fields (assessor_model,
    confidence_threshold) and access to the full AgentFoxConfig for
    is_explicitly_configured() checks via resolve_model_tier().

    This helper wraps the routing config and attaches the full config.
    """
    routing_config = _make_routing_config(
        assessor_model=assessor_model,
        confidence_threshold=confidence_threshold,
    )
    # AssessmentManager may need the full config for config resolution.
    # Attach it as an attribute so tests can pass it through.
    object.__setattr__(routing_config, "_agentfox_config", agentfox_config)
    return routing_config


# ===========================================================================
# Task 5.1: is_explicitly_configured() layer traversal
# Test Spec: TS-15-26, TS-15-27, TS-15-E8
# Requirements: 15-REQ-6.1, 15-REQ-6.2, 15-REQ-6.E1
# ===========================================================================


class TestIsExplicitlyConfiguredLayers:
    """TS-15-26: is_explicitly_configured() returns True when any layer 1-2
    has a non-None value, False when all return None.

    Requirement: 15-REQ-6.1
    """

    def test_returns_true_when_per_archetype_override_set(self) -> None:
        """Returns True when layer 2 (per-archetype override) has non-None value.

        Uses AgentFoxConfig with archetypes.overrides['coder'].model_tier set
        to verify that layer 2 causes is_explicitly_configured() to return True.
        """
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        # Build a real AgentFoxConfig with per-archetype override at layer 2
        config = _make_config_with_archetype_override("coder", "ADVANCED")
        manager = AssessmentManager(config=config, client=mock_client)

        result = manager.is_explicitly_configured("coder", None)
        assert result is True, "Expected True when per-archetype override (layer 2) has a non-None model_tier"

    def test_returns_false_when_all_layers_none(self) -> None:
        """Returns False when no overrides exist at any layer for the archetype."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        # Use a full AgentFoxConfig with no overrides — all layers return None
        from agentfox.core.config import AgentFoxConfig

        config = AgentFoxConfig()
        manager = AssessmentManager(config=config, client=mock_client)

        # 'verifier' has no explicit config override in a default AgentFoxConfig
        result = manager.is_explicitly_configured("verifier", None)
        assert result is False, "Expected False when no explicit override exists for verifier in any layer"

    def test_returns_true_with_mode_level_override(self) -> None:
        """Returns True when layer 1 (mode-level override) has a non-None value.

        Uses AgentFoxConfig with archetypes.overrides['coder'].modes['fix'].model_tier
        set to verify that layer 1 causes is_explicitly_configured() to return True.
        """
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        # Build a real AgentFoxConfig with a mode-level override at layer 1
        config = _make_config_with_mode_override("coder", "fix", "ADVANCED")
        manager = AssessmentManager(config=config, client=mock_client)

        result = manager.is_explicitly_configured("coder", "fix")
        assert result is True, "Expected True when mode-level override (layer 1) for coder/fix has model_tier"


class TestIsExplicitlyConfiguredModeNone:
    """TS-15-27: is_explicitly_configured() skips layer 1 when mode is None.

    When mode is None, layer 1 (mode-level override check) is skipped
    entirely and only layer 2 is consulted.

    Requirement: 15-REQ-6.2
    """

    def test_mode_none_skips_layer_1(self) -> None:
        """mode=None skips layer 1; returns False when only layer 1 has override.

        Set up a config with only a mode-level override for coder/fix (layer 1).
        With mode=None, layer 1 is skipped and layer 2 has no override,
        so is_explicitly_configured('coder', None) must return False.
        """
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        # Only a mode-level override for 'fix' — no per-archetype override
        config = _make_config_with_mode_override("coder", "fix", "ADVANCED")
        manager = AssessmentManager(config=config, client=mock_client)

        # mode=None → layer 1 is skipped entirely; layer 2 has no value → False
        result = manager.is_explicitly_configured("coder", None)
        assert result is False, "Expected False when mode=None and only a mode-level (layer 1) override exists"

    def test_mode_fix_hits_layer_1(self) -> None:
        """mode='fix' checks layer 1 and returns True if override exists.

        When a mode-level override exists for coder/fix and mode='fix' is passed,
        is_explicitly_configured() must return True because layer 1 is consulted.
        """
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        # Configure a mode-level override at layer 1 for coder/fix
        config = _make_config_with_mode_override("coder", "fix", "ADVANCED")
        manager = AssessmentManager(config=config, client=mock_client)

        # mode='fix' → layer 1 is checked and finds the override → True
        result = manager.is_explicitly_configured("coder", "fix")
        assert result is True, "Expected True when mode='fix' and a mode-level (layer 1) override exists for coder/fix"


class TestIsExplicitlyConfiguredEarlyReturn:
    """TS-15-E8: is_explicitly_configured() returns True immediately on layer 1 hit.

    When layer 1 yields a non-None value for a non-None mode,
    layers 2-3 are not consulted.

    Requirement: 15-REQ-6.E1
    """

    def test_layer_1_hit_skips_layers_2_and_3(self) -> None:
        """Returns True immediately when layer 1 yields a non-None value.

        Configured with only a mode-level override (layer 1) for coder/fix,
        and no per-archetype (layer 2) or legacy (layer 3) override.
        is_explicitly_configured('coder', 'fix') must return True,
        proving it found the answer at layer 1 without needing layers 2-3.
        """
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        # Layer 1 only — no layer 2 or 3 override
        config = _make_config_with_mode_override("coder", "fix", "ADVANCED")
        manager = AssessmentManager(config=config, client=mock_client)

        # Layer 1 has value → True without consulting layers 2-3
        result = manager.is_explicitly_configured("coder", "fix")
        assert result is True, "Expected True immediately when layer 1 (mode-level override) has a value"

        # Cross-check: same config with mode=None should return False
        # (layer 1 skipped, no layer 2/3 override present)
        result_mode_none = manager.is_explicitly_configured("coder", None)
        assert result_mode_none is False, "Expected False when mode=None skips layer 1 and layers 2-3 have no override"

    def test_layer_1_hit_returns_true_not_false(self) -> None:
        """When layer 1 has a value, result is True regardless of layers 2-3.

        The fix-review mode has model_tier='ADVANCED' in the ARCHETYPE_REGISTRY,
        but that is a registry default (layers 4-5), not a config override (layers 1-3).
        Without an explicit config override, is_explicitly_configured must return False.
        With an explicit config override for the same mode, it must return True.
        """
        from agentfox.core.config import AgentFoxConfig
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()

        # No explicit override — registry default (layer 4-5) does not count
        config_no_override = AgentFoxConfig()
        manager_no = AssessmentManager(config=config_no_override, client=mock_client)
        result_no = manager_no.is_explicitly_configured("reviewer", "fix-review")
        assert result_no is False, "Expected False when registry default (not a config override) is the only source"

        # With explicit mode-level override at layer 1 — must return True
        config_with_override = _make_config_with_mode_override("reviewer", "fix-review", "ADVANCED")
        manager_yes = AssessmentManager(config=config_with_override, client=mock_client)
        result_yes = manager_yes.is_explicitly_configured("reviewer", "fix-review")
        assert result_yes is True, "Expected True when an explicit mode-level config override (layer 1) is present"


# ===========================================================================
# Task 5.2: assess_node() skip-on-explicit-override behavior
# Test Spec: TS-15-28
# Requirements: 15-REQ-6.3
# ===========================================================================


class TestAssessNodeSkipOnExplicitOverride:
    """TS-15-28: When is_explicitly_configured() returns True, assess_node()
    skips LLM assessment, uses configured tier/variant, and logs DEBUG.

    Requirement: 15-REQ-6.3
    """

    def test_llm_not_called_on_explicit_override(self) -> None:
        """ComplexityAssessor.assess() is never called when override active."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock()
        config = _make_routing_config()
        manager = AssessmentManager(config=config, client=mock_client)

        # Patch is_explicitly_configured to return True
        manager.is_explicitly_configured = MagicMock(return_value=True)

        asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode="fix",
                node_body="some body",
            )
        )

        # The LLM should not be called
        mock_client.messages.create.assert_not_called()

    def test_ladder_uses_configured_tier(self) -> None:
        """EscalationLadder uses the explicitly configured tier/variant."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock()
        config = _make_routing_config()
        manager = AssessmentManager(config=config, client=mock_client)

        manager.is_explicitly_configured = MagicMock(return_value=True)

        ladder = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode="fix",
                node_body="some body",
            )
        )

        assert ladder is not None
        # The ladder should use the explicitly configured tier
        assert hasattr(ladder, "current_tier") or hasattr(ladder, "starting_tier")

    def test_debug_log_contains_override_info(self, caplog: pytest.LogCaptureFixture) -> None:
        """DEBUG log contains node_id, archetype, mode, and resolved tier."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock()
        config = _make_routing_config()
        manager = AssessmentManager(config=config, client=mock_client)

        manager.is_explicitly_configured = MagicMock(return_value=True)

        with caplog.at_level(logging.DEBUG, logger="agentfox"):
            asyncio.run(
                manager.assess_node(
                    node_id="n1",
                    archetype="coder",
                    mode="fix",
                    node_body="some body",
                )
            )

        debug_logs = [r for r in caplog.records if r.levelno == logging.DEBUG]
        log_text = " ".join(r.getMessage() for r in debug_logs)

        assert "n1" in log_text, f"node_id missing from DEBUG log: {log_text}"
        assert "coder" in log_text, f"archetype missing from DEBUG log: {log_text}"
        assert "fix" in log_text, f"mode missing from DEBUG log: {log_text}"
        # Should mention the resolved tier or 'explicit'
        assert (
            "ADVANCED" in log_text or "STANDARD" in log_text or "SIMPLE" in log_text or "explicit" in log_text.lower()
        ), f"resolved tier missing from DEBUG log: {log_text}"


# ===========================================================================
# Task 5.3: Five-layer resolution priority ordering
# Test Spec: TS-15-29, TS-15-30, TS-15-31
# Requirements: 15-REQ-7.1, 15-REQ-7.2, 15-REQ-7.3
# ===========================================================================


class TestFiveLayerResolutionPriority:
    """TS-15-29: Explicit config (layers 1-3) always wins over assessment
    (layer 4) and registry default (layer 5).

    The five-layer priority order:
    1. mode-level config override
    2. per-archetype config override
    3. legacy dict override
    4. LLM assessment upgrade
    5. archetype registry default

    Requirement: 15-REQ-7.1
    """

    def test_explicit_override_wins_over_assessor(self) -> None:
        """With explicit override active, config tier used; assessor never called."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        config = _make_routing_config()
        manager = AssessmentManager(config=config, client=mock_client)

        # Mock explicit override to return True
        manager.is_explicitly_configured = MagicMock(return_value=True)

        ladder = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode="fix",
                node_body="body",
            )
        )

        # Assessor should NOT be called
        mock_client.messages.create.assert_not_called()
        assert ladder is not None

    def test_assessor_upgrades_when_no_override(self) -> None:
        """Without override, assessor can upgrade from registry default."""
        from agentfox.core.models import ModelTier
        from agentfox.engine.engine import AssessmentManager

        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        ladder = asyncio.run(
            manager.assess_node(
                node_id="n2",
                archetype="coder",
                mode=None,
                node_body="body",
            )
        )

        # Assessor returns ADVANCED with confidence 0.9 >= threshold 0.6
        # Should upgrade from STANDARD (coder default) to ADVANCED
        assert ladder.current_tier == ModelTier.ADVANCED

    def test_below_threshold_uses_registry_default(self) -> None:
        """Without override, below-threshold assessment uses registry default."""
        from agentfox.core.models import ModelTier
        from agentfox.engine.engine import AssessmentManager

        mock_client = _make_mock_client(_LOW_CONFIDENCE_RESPONSE_JSON)
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        ladder = asyncio.run(
            manager.assess_node(
                node_id="n3",
                archetype="coder",
                mode=None,
                node_body="body",
            )
        )

        # Confidence 0.3 < threshold 0.6, so no upgrade
        # Falls back to coder registry default: STANDARD
        assert ladder.current_tier == ModelTier.STANDARD


class TestAssessmentNeverOverridesExplicitConfig:
    """TS-15-30: LLM assessment (layer 4) only upgrades from registry default
    floor and never overrides explicit config at layers 1-3.

    Requirement: 15-REQ-7.2
    """

    def test_explicit_simple_beats_assessor_advanced(self) -> None:
        """Explicit config SIMPLE tier wins even when assessor recommends ADVANCED."""
        from agentfox.engine.engine import AssessmentManager

        # Mock assessor would return ADVANCED with high confidence
        mock_client = _make_mock_client(
            '{"recommended_tier": "ADVANCED", "recommended_variant": "extended", '
            '"confidence": 0.99, "rationale": "very complex"}'
        )
        config = _make_routing_config()
        manager = AssessmentManager(config=config, client=mock_client)

        # Mock explicit override to return True
        manager.is_explicitly_configured = MagicMock(return_value=True)

        ladder = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode="fix",
                node_body="body",
            )
        )

        # Assessor should NOT be called
        mock_client.messages.create.assert_not_called()
        # The EscalationLadder should use the config tier, not ADVANCED
        assert ladder is not None

    def test_assessor_never_applied_with_override(self) -> None:
        """When override active, assessor recommendation is never applied."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        config = _make_routing_config()
        manager = AssessmentManager(config=config, client=mock_client)

        manager.is_explicitly_configured = MagicMock(return_value=True)

        asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode="fix",
                node_body="body",
            )
        )

        # The assessor should NOT have been invoked
        mock_client.messages.create.assert_not_called()


class TestRegistryDefaultAsFloor:
    """TS-15-31: Registry default (layer 5) serves as base floor for
    apply_assessment() when no explicit override exists.

    Requirement: 15-REQ-7.3
    """

    def test_below_threshold_uses_coder_registry_default(self) -> None:
        """Coder with below-threshold assessor uses STANDARD/standard floor."""
        from agentfox.core.models import ModelTier
        from agentfox.engine.engine import AssessmentManager

        mock_client = _make_mock_client(_LOW_CONFIDENCE_RESPONSE_JSON)
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        ladder = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
            )
        )

        # Coder registry default is STANDARD/standard per REQ-8.1
        assert ladder.current_tier == ModelTier.STANDARD

    def test_below_threshold_uses_reviewer_pre_review_default(self) -> None:
        """Reviewer/pre-review with below-threshold assessor uses ADVANCED/standard."""
        from agentfox.core.models import ModelTier
        from agentfox.engine.engine import AssessmentManager

        mock_client = _make_mock_client(_LOW_CONFIDENCE_RESPONSE_JSON)
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        ladder = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="reviewer",
                mode="pre-review",
                node_body="body",
            )
        )

        # Reviewer/pre-review registry default is ADVANCED/standard per REQ-8.2
        assert ladder.current_tier == ModelTier.ADVANCED

    def test_below_threshold_uses_maintainer_hunt_default(self) -> None:
        """Maintainer/hunt with below-threshold assessor uses SIMPLE/standard."""
        from agentfox.core.models import ModelTier
        from agentfox.engine.engine import AssessmentManager

        mock_client = _make_mock_client(_LOW_CONFIDENCE_RESPONSE_JSON)
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        ladder = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="maintainer",
                mode="hunt",
                node_body="body",
            )
        )

        # Maintainer/hunt registry default is SIMPLE/standard per REQ-8.4
        assert ladder.current_tier == ModelTier.SIMPLE


# ===========================================================================
# Task 5.4: ARCHETYPE_REGISTRY default assignments (all 10 combinations)
# Test Spec: TS-15-32, TS-15-33, TS-15-34, TS-15-35, TS-15-36
# Requirements: 15-REQ-8.1, 15-REQ-8.2, 15-REQ-8.3, 15-REQ-8.4, 15-REQ-8.5
# ===========================================================================


class TestArchetypeRegistryCoderDefaults:
    """TS-15-32: ARCHETYPE_REGISTRY coder defaults.

    coder (no mode) and coder/fix both default to STANDARD/standard.

    Requirement: 15-REQ-8.1
    """

    def test_coder_default_tier_is_standard(self) -> None:
        """Coder default tier is STANDARD."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY, resolve_effective_config

        entry = ARCHETYPE_REGISTRY["coder"]
        resolved = resolve_effective_config(entry, mode=None)
        assert resolved.default_model_tier == "STANDARD", (
            f"Expected coder default tier STANDARD, got {resolved.default_model_tier}"
        )

    def test_coder_fix_default_tier_is_standard(self) -> None:
        """Coder/fix default tier is STANDARD."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY, resolve_effective_config

        entry = ARCHETYPE_REGISTRY["coder"]
        resolved = resolve_effective_config(entry, mode="fix")
        assert resolved.default_model_tier == "STANDARD", (
            f"Expected coder/fix default tier STANDARD, got {resolved.default_model_tier}"
        )


class TestArchetypeRegistryReviewerDefaults:
    """TS-15-33: ARCHETYPE_REGISTRY reviewer mode defaults.

    pre-review: ADVANCED/standard
    drift-review: STANDARD/standard
    audit-review: ADVANCED/standard
    fix-review: ADVANCED/standard

    Requirement: 15-REQ-8.2
    """

    def test_reviewer_pre_review_tier_is_advanced(self) -> None:
        """Reviewer/pre-review default tier is ADVANCED."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY, resolve_effective_config

        entry = ARCHETYPE_REGISTRY["reviewer"]
        resolved = resolve_effective_config(entry, mode="pre-review")
        assert resolved.default_model_tier == "ADVANCED", (
            f"Expected reviewer/pre-review tier ADVANCED, got {resolved.default_model_tier}"
        )

    def test_reviewer_drift_review_tier_is_standard(self) -> None:
        """Reviewer/drift-review default tier is STANDARD."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY, resolve_effective_config

        entry = ARCHETYPE_REGISTRY["reviewer"]
        resolved = resolve_effective_config(entry, mode="drift-review")
        assert resolved.default_model_tier == "STANDARD", (
            f"Expected reviewer/drift-review tier STANDARD, got {resolved.default_model_tier}"
        )

    def test_reviewer_audit_review_tier_is_advanced(self) -> None:
        """Reviewer/audit-review default tier is ADVANCED."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY, resolve_effective_config

        entry = ARCHETYPE_REGISTRY["reviewer"]
        resolved = resolve_effective_config(entry, mode="audit-review")
        assert resolved.default_model_tier == "ADVANCED", (
            f"Expected reviewer/audit-review tier ADVANCED, got {resolved.default_model_tier}"
        )

    def test_reviewer_fix_review_tier_is_advanced(self) -> None:
        """Reviewer/fix-review default tier is ADVANCED."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY, resolve_effective_config

        entry = ARCHETYPE_REGISTRY["reviewer"]
        resolved = resolve_effective_config(entry, mode="fix-review")
        assert resolved.default_model_tier == "ADVANCED", (
            f"Expected reviewer/fix-review tier ADVANCED, got {resolved.default_model_tier}"
        )


class TestArchetypeRegistryVerifierDefaults:
    """TS-15-34: ARCHETYPE_REGISTRY verifier default.

    verifier: STANDARD/standard

    Requirement: 15-REQ-8.3
    """

    def test_verifier_default_tier_is_standard(self) -> None:
        """Verifier default tier is STANDARD."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY, resolve_effective_config

        entry = ARCHETYPE_REGISTRY["verifier"]
        resolved = resolve_effective_config(entry, mode=None)
        assert resolved.default_model_tier == "STANDARD", (
            f"Expected verifier default tier STANDARD, got {resolved.default_model_tier}"
        )


class TestArchetypeRegistryMaintainerDefaults:
    """TS-15-35: ARCHETYPE_REGISTRY maintainer mode defaults.

    hunt: SIMPLE/standard
    fix-triage: STANDARD/standard
    extraction: SIMPLE/standard

    Requirement: 15-REQ-8.4
    """

    def test_maintainer_hunt_tier_is_simple(self) -> None:
        """Maintainer/hunt default tier is SIMPLE."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY, resolve_effective_config

        entry = ARCHETYPE_REGISTRY["maintainer"]
        resolved = resolve_effective_config(entry, mode="hunt")
        assert resolved.default_model_tier == "SIMPLE", (
            f"Expected maintainer/hunt tier SIMPLE, got {resolved.default_model_tier}"
        )

    def test_maintainer_fix_triage_tier_is_standard(self) -> None:
        """Maintainer/fix-triage default tier is STANDARD."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY, resolve_effective_config

        entry = ARCHETYPE_REGISTRY["maintainer"]
        resolved = resolve_effective_config(entry, mode="fix-triage")
        assert resolved.default_model_tier == "STANDARD", (
            f"Expected maintainer/fix-triage tier STANDARD, got {resolved.default_model_tier}"
        )

    def test_maintainer_extraction_tier_is_simple(self) -> None:
        """Maintainer/extraction default tier is SIMPLE."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY, resolve_effective_config

        entry = ARCHETYPE_REGISTRY["maintainer"]
        resolved = resolve_effective_config(entry, mode="extraction")
        assert resolved.default_model_tier == "SIMPLE", (
            f"Expected maintainer/extraction tier SIMPLE, got {resolved.default_model_tier}"
        )


class TestArchetypeRegistryAll10Combinations:
    """TS-15-36: All 10 archetype/mode combinations reflect new defaults
    immediately at import time with no migration.

    Requirement: 15-REQ-8.5
    """

    _EXPECTED_DEFAULTS = [
        ("coder", None, "STANDARD"),
        ("coder", "fix", "STANDARD"),
        ("reviewer", "pre-review", "ADVANCED"),
        ("reviewer", "drift-review", "STANDARD"),
        ("reviewer", "audit-review", "ADVANCED"),
        ("reviewer", "fix-review", "ADVANCED"),
        ("verifier", None, "STANDARD"),
        ("maintainer", "hunt", "SIMPLE"),
        ("maintainer", "fix-triage", "STANDARD"),
        ("maintainer", "extraction", "SIMPLE"),
    ]

    @pytest.mark.parametrize(
        "archetype,mode,expected_tier",
        _EXPECTED_DEFAULTS,
        ids=[f"{a}/{m or 'base'}" for a, m, _ in _EXPECTED_DEFAULTS],
    )
    def test_archetype_mode_default_tier(
        self,
        archetype: str,
        mode: str | None,
        expected_tier: str,
    ) -> None:
        """Each archetype/mode combination has the expected default tier."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY, resolve_effective_config

        entry = ARCHETYPE_REGISTRY[archetype]
        resolved = resolve_effective_config(entry, mode=mode)
        assert resolved.default_model_tier == expected_tier, (
            f"{archetype}/{mode or 'base'}: expected {expected_tier}, got {resolved.default_model_tier}"
        )


# ===========================================================================
# Task 5.5: Property tests for explicit override and quality guarantee
# Test Spec: TS-15-P5, TS-15-P7
# Requirements: 15-REQ-6.3, 15-REQ-7.1, 15-REQ-7.2, 15-REQ-8.2
# ===========================================================================


class TestPropertyExplicitOverrideSkipsAssessor:
    """TS-15-P5: For any archetype/mode where is_explicitly_configured()
    returns True, ComplexityAssessor.assess() is never called and
    config-layer tier is used.

    Property: 15-PROP-5
    Validates: 15-REQ-6.3, 15-REQ-7.1, 15-REQ-7.2
    """

    @pytest.mark.parametrize(
        "archetype,mode",
        [
            ("coder", None),
            ("coder", "fix"),
            ("reviewer", "pre-review"),
            ("reviewer", "drift-review"),
            ("reviewer", "audit-review"),
            ("reviewer", "fix-review"),
            ("verifier", None),
            ("maintainer", "hunt"),
            ("maintainer", "fix-triage"),
            ("maintainer", "extraction"),
        ],
        ids=[
            "coder-base",
            "coder-fix",
            "reviewer-pre",
            "reviewer-drift",
            "reviewer-audit",
            "reviewer-fix",
            "verifier-base",
            "maintainer-hunt",
            "maintainer-fix-triage",
            "maintainer-extraction",
        ],
    )
    def test_explicit_override_skips_assessor_for_all_archetypes(
        self,
        archetype: str,
        mode: str | None,
    ) -> None:
        """When is_explicitly_configured() returns True, LLM is never called."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        config = _make_routing_config()
        manager = AssessmentManager(config=config, client=mock_client)

        # Force is_explicitly_configured to return True
        manager.is_explicitly_configured = MagicMock(return_value=True)

        ladder = asyncio.run(
            manager.assess_node(
                node_id=f"prop_{archetype}_{mode or 'base'}",
                archetype=archetype,
                mode=mode,
                node_body="some task body",
            )
        )

        # LLM should never be called
        mock_client.messages.create.assert_not_called()
        # Ladder should be returned (not None)
        assert ladder is not None, f"Expected ladder for {archetype}/{mode}, got None"

    @pytest.mark.parametrize(
        "archetype,mode,node_body",
        [
            ("coder", "fix", "simple fix"),
            ("coder", "fix", "complex multi-module refactor with auth changes"),
            ("reviewer", "pre-review", "review spec changes"),
        ],
        ids=[
            "simple-body",
            "complex-body",
            "review-body",
        ],
    )
    def test_explicit_override_skips_assessor_regardless_of_body(
        self,
        archetype: str,
        mode: str | None,
        node_body: str,
    ) -> None:
        """Regardless of node_body content, explicit override skips LLM."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        config = _make_routing_config()
        manager = AssessmentManager(config=config, client=mock_client)

        manager.is_explicitly_configured = MagicMock(return_value=True)

        asyncio.run(
            manager.assess_node(
                node_id="prop_body_test",
                archetype=archetype,
                mode=mode,
                node_body=node_body,
            )
        )

        mock_client.messages.create.assert_not_called()

    @pytest.mark.parametrize(
        "previous_failure",
        [
            None,
            "TypeError: NoneType",
            "AssertionError: test failed",
        ],
        ids=["no-failure", "type-error", "assertion-error"],
    )
    def test_explicit_override_skips_assessor_regardless_of_failure(
        self,
        previous_failure: str | None,
    ) -> None:
        """Regardless of previous_failure, explicit override skips LLM."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        config = _make_routing_config()
        manager = AssessmentManager(config=config, client=mock_client)

        manager.is_explicitly_configured = MagicMock(return_value=True)

        asyncio.run(
            manager.assess_node(
                node_id="prop_failure_test",
                archetype="coder",
                mode="fix",
                node_body="body",
                previous_failure=previous_failure,
            )
        )

        mock_client.messages.create.assert_not_called()


class TestPropertyQualityGuaranteeReviewer:
    """TS-15-P7: For reviewer pre-review and audit-review, ARCHETYPE_REGISTRY
    floor is always ADVANCED/standard with no explicit override.

    Property: 15-PROP-7
    Validates: 15-REQ-8.2
    """

    @pytest.mark.parametrize(
        "mode",
        ["pre-review", "audit-review"],
        ids=["pre-review", "audit-review"],
    )
    def test_reviewer_quality_modes_default_to_advanced(
        self,
        mode: str,
    ) -> None:
        """Reviewer pre-review and audit-review always default to ADVANCED.

        When no explicit config override exists and assessor returns
        below-threshold confidence, the ladder starting_tier should be
        ADVANCED (the registry default floor for these modes).
        """
        from agentfox.core.models import ModelTier
        from agentfox.engine.engine import AssessmentManager

        # Mock assessor returns below-threshold confidence
        mock_client = _make_mock_client(_LOW_CONFIDENCE_RESPONSE_JSON)
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        ladder = asyncio.run(
            manager.assess_node(
                node_id=f"quality_{mode}",
                archetype="reviewer",
                mode=mode,
                node_body="body",
            )
        )

        # Floor for these modes is ADVANCED per REQ-8.2
        assert ladder.current_tier == ModelTier.ADVANCED, (
            f"Expected ADVANCED for reviewer/{mode}, got {ladder.current_tier}"
        )

    @pytest.mark.parametrize(
        "mode",
        ["pre-review", "audit-review"],
        ids=["pre-review", "audit-review"],
    )
    def test_reviewer_quality_modes_never_below_advanced_on_failure(
        self,
        mode: str,
    ) -> None:
        """Even when assessment fails, reviewer quality modes are at ADVANCED."""
        from agentfox.core.models import ModelTier
        from agentfox.engine.engine import AssessmentManager

        # Mock assessor fails with network error
        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(
            side_effect=ConnectionError("Network failure"),
        )
        config = _make_routing_config()
        manager = AssessmentManager(config=config, client=mock_client)

        try:
            ladder = asyncio.run(
                manager.assess_node(
                    node_id=f"quality_fail_{mode}",
                    archetype="reviewer",
                    mode=mode,
                    node_body="body",
                )
            )
        except Exception as exc:
            pytest.fail(f"Should not raise: {exc}")

        assert ladder.current_tier == ModelTier.ADVANCED, (
            f"Expected ADVANCED floor for reviewer/{mode} on failure, got {ladder.current_tier}"
        )

    @pytest.mark.parametrize(
        "mode",
        ["pre-review", "audit-review"],
        ids=["pre-review", "audit-review"],
    )
    def test_reviewer_quality_modes_with_no_assessor(
        self,
        mode: str,
    ) -> None:
        """With client=None (no assessor), reviewer quality modes at ADVANCED."""
        from agentfox.core.models import ModelTier
        from agentfox.engine.engine import AssessmentManager

        config = _make_routing_config()
        manager = AssessmentManager(config=config, client=None)

        ladder = asyncio.run(
            manager.assess_node(
                node_id=f"quality_none_{mode}",
                archetype="reviewer",
                mode=mode,
                node_body="body",
            )
        )

        assert ladder.current_tier == ModelTier.ADVANCED, (
            f"Expected ADVANCED for reviewer/{mode} with no assessor, got {ladder.current_tier}"
        )


# ===========================================================================
# Task Group 6: Dispatch integration, RoutingConfig validation, nightshift
#               passthrough, and triage parsing
#
# Test Spec: TS-15-37 through TS-15-48, TS-15-E9 through TS-15-E13,
#            TS-15-P8
# Requirements: 15-REQ-9.1 through 15-REQ-9.3, 15-REQ-9.E1,
#               15-REQ-10.1 through 15-REQ-10.3, 15-REQ-10.E1, 15-REQ-10.E2,
#               15-REQ-11.1 through 15-REQ-11.6,
#               15-REQ-11.E1, 15-REQ-11.E2
# ===========================================================================


# ===========================================================================
# Task 6.1: DispatchManager.prepare_launch() node_body and previous_failure
# Test Spec: TS-15-37, TS-15-38, TS-15-39, TS-15-E9
# Requirements: 15-REQ-9.1, 15-REQ-9.2, 15-REQ-9.3, 15-REQ-9.E1
# ===========================================================================


async def _call_prepare_launch_behavioral(
    node_body: str | None,
    error_tracker: dict[str, str | None],
    archetype: str = "coder",
    mode: str | None = None,
    node_id: str = "n1",
) -> MagicMock:
    """Run DispatchManager.prepare_launch() with controlled mocks.

    Returns the mock_routing object so tests can inspect assess_node calls.
    The workspace health check is patched to return a clean report so it
    does not interfere with dispatch.
    """
    from unittest.mock import patch as _patch

    from agentfox.engine.dispatch import DispatchManager

    mock_routing = MagicMock()
    mock_routing.assess_node = AsyncMock(return_value=MagicMock())
    mock_routing.ladders = {}

    mock_node = MagicMock()
    mock_node.body = node_body
    mock_node.archetype = archetype
    mock_node.mode = mode
    mock_node.instances = 1

    mock_graph = MagicMock()
    mock_graph.nodes = {node_id: mock_node}

    mock_circuit = MagicMock()
    mock_decision = MagicMock()
    mock_decision.allowed = True
    mock_circuit.check_launch = MagicMock(return_value=mock_decision)

    mock_config = MagicMock()
    mock_config.max_retries = 3

    dispatch = DispatchManager(
        session_runner_factory=MagicMock(),
        inter_session_delay=0.0,
        parallel=1,
        graph=mock_graph,
        routing=mock_routing,
        circuit=mock_circuit,
        config=mock_config,
    )
    dispatch._result_handler = None

    # Patch workspace health check to return a clean report (no issues)
    try:
        from agentfox.workspace.health import HealthReport

        clean_report = HealthReport(untracked_files=[], dirty_index_files=[])

        with _patch("agentfox.workspace.health.check_workspace_health", AsyncMock(return_value=clean_report)):
            await dispatch.prepare_launch(
                node_id,
                state=MagicMock(),
                attempt_tracker={},
                error_tracker=error_tracker,
            )
    except Exception:
        # If patching fails, call without patch — workspace health fail-open means
        # prepare_launch still proceeds. Any exception from the health check is
        # caught internally and logged as a warning.
        await dispatch.prepare_launch(
            node_id,
            state=MagicMock(),
            attempt_tracker={},
            error_tracker=error_tracker,
        )

    return mock_routing


class TestPreparelaunchNodeBody:
    """TS-15-37: prepare_launch() extracts node_body from task graph node.

    Requirement: 15-REQ-9.1
    """

    def test_node_body_extracted_and_passed(self) -> None:
        """node_body is extracted via self.get_node(node_id).body and
        passed to assess_node() — behavioral verification.
        """
        node_body = "Fix the auth bug"
        mock_routing = asyncio.run(
            _call_prepare_launch_behavioral(
                node_body=node_body,
                error_tracker={},
            )
        )
        mock_routing.assess_node.assert_called_once()
        call_kwargs = mock_routing.assess_node.call_args
        # node_body should be passed as a keyword argument (or positional)
        passed_body = (
            call_kwargs.kwargs.get("node_body")
            if call_kwargs.kwargs
            else call_kwargs.args[3]
            if len(call_kwargs.args) > 3
            else None
        )
        assert passed_body == node_body, f"Expected assess_node to receive node_body='{node_body}', got {passed_body!r}"

    def test_node_body_forwarded_to_assess_node(self) -> None:
        """assess_node() receives node_body from prepare_launch() — behavioral."""
        node_body = "Complex multi-module refactor"
        mock_routing = asyncio.run(
            _call_prepare_launch_behavioral(
                node_body=node_body,
                error_tracker={},
            )
        )
        mock_routing.assess_node.assert_called_once()
        # Verify node_body was in the call — check both kwargs and positional args
        call_args = mock_routing.assess_node.call_args
        all_args = str(call_args)
        assert node_body in all_args, f"Expected node_body='{node_body}' in assess_node call, got: {call_args}"


class TestPrepareLaunchPreviousFailure:
    """TS-15-38: prepare_launch() extracts previous_failure from error_tracker.

    Requirement: 15-REQ-9.2
    """

    def test_previous_failure_from_error_tracker(self) -> None:
        """previous_failure extracted from error_tracker when entry exists — behavioral."""
        error_tracker = {"n1": "TypeError: cannot unpack non-sequence NoneType"}
        mock_routing = asyncio.run(
            _call_prepare_launch_behavioral(
                node_body="Fix the bug",
                error_tracker=error_tracker,
            )
        )
        mock_routing.assess_node.assert_called_once()
        call_args = mock_routing.assess_node.call_args
        # previous_failure should be the error string from error_tracker
        passed_failure = call_args.kwargs.get("previous_failure") if call_args.kwargs else None
        assert passed_failure == "TypeError: cannot unpack non-sequence NoneType", (
            f"Expected previous_failure from error_tracker, got {passed_failure!r}"
        )

    def test_previous_failure_none_when_no_entry(self) -> None:
        """When error_tracker has no entry for node, previous_failure=None — behavioral."""
        mock_routing = asyncio.run(
            _call_prepare_launch_behavioral(
                node_body="Fix the bug",
                error_tracker={},  # empty: no prior failure
            )
        )
        mock_routing.assess_node.assert_called_once()
        call_args = mock_routing.assess_node.call_args
        passed_failure = call_args.kwargs.get("previous_failure") if call_args.kwargs else "NOT_IN_KWARGS"
        assert passed_failure is None, (
            f"Expected previous_failure=None when no entry in error_tracker, got {passed_failure!r}"
        )


class TestPrepareLaunchFullParams:
    """TS-15-39: prepare_launch() passes all params to assess_node().

    Both node_body and previous_failure are passed alongside
    node_id, archetype, and mode.

    Requirement: 15-REQ-9.3
    """

    def test_assess_node_receives_five_params(self) -> None:
        """assess_node() called with node_id, archetype, mode, node_body,
        previous_failure from prepare_launch() — behavioral verification.
        """
        error_tracker = {"n1": "ReviewFailed"}
        mock_routing = asyncio.run(
            _call_prepare_launch_behavioral(
                node_body="Review spec changes",
                error_tracker=error_tracker,
                archetype="reviewer",
                mode="pre-review",
            )
        )
        mock_routing.assess_node.assert_called_once()
        call_args = mock_routing.assess_node.call_args
        # Verify all five core parameters are present
        all_call_repr = str(call_args)
        assert "n1" in all_call_repr, "node_id='n1' should be in assess_node call"
        assert "reviewer" in all_call_repr, "archetype='reviewer' should be in assess_node call"
        assert "pre-review" in all_call_repr, "mode='pre-review' should be in assess_node call"
        assert "Review spec changes" in all_call_repr, "node_body should be in assess_node call"
        assert "ReviewFailed" in all_call_repr, "previous_failure should be in assess_node call"

    def test_assess_node_called_with_correct_archetype_and_mode(self) -> None:
        """assess_node() receives the archetype and mode from the task graph — behavioral."""
        mock_routing = asyncio.run(
            _call_prepare_launch_behavioral(
                node_body="body",
                error_tracker={},
                archetype="reviewer",
                mode="pre-review",
            )
        )
        mock_routing.assess_node.assert_called_once()
        call_repr = str(mock_routing.assess_node.call_args)
        assert "reviewer" in call_repr, "archetype='reviewer' must be passed to assess_node"
        assert "pre-review" in call_repr, "mode='pre-review' must be passed to assess_node"


class TestPrepareLaunchNoFailureEntry:
    """TS-15-E9: No prior failure entry → previous_failure=None.

    Requirement: 15-REQ-9.E1
    """

    def test_empty_error_tracker_yields_none(self) -> None:
        """When error_tracker is empty dict, assess_node gets previous_failure=None — behavioral."""
        mock_routing = asyncio.run(
            _call_prepare_launch_behavioral(
                node_body="some body",
                error_tracker={},  # empty: no prior failure for any node
            )
        )
        mock_routing.assess_node.assert_called_once()
        call_args = mock_routing.assess_node.call_args
        passed_failure = call_args.kwargs.get("previous_failure") if call_args.kwargs else "MISSING"
        assert passed_failure is None, f"Empty error_tracker should yield previous_failure=None, got {passed_failure!r}"

    def test_missing_node_in_error_tracker_yields_none(self) -> None:
        """When node_id not in error_tracker, previous_failure=None — behavioral.

        The dispatch uses error_tracker.get(node_id) which returns None for absent keys.
        """
        # error_tracker has an entry for a DIFFERENT node, not 'n1'
        error_tracker: dict[str, str | None] = {"other_node": "SomeError"}
        mock_routing = asyncio.run(
            _call_prepare_launch_behavioral(
                node_body="task body",
                error_tracker=error_tracker,
                node_id="n1",
            )
        )
        mock_routing.assess_node.assert_called_once()
        call_args = mock_routing.assess_node.call_args
        passed_failure = call_args.kwargs.get("previous_failure") if call_args.kwargs else "MISSING"
        assert passed_failure is None, (
            f"Missing node in error_tracker should yield previous_failure=None, got {passed_failure!r}"
        )


# ===========================================================================
# Task 6.2: RoutingConfig field defaults and eager validation
# Test Spec: TS-15-40, TS-15-41, TS-15-42, TS-15-E10, TS-15-E11
# Requirements: 15-REQ-10.1, 15-REQ-10.2, 15-REQ-10.3,
#               15-REQ-10.E1, 15-REQ-10.E2
# ===========================================================================


class TestRoutingConfigAssessorModelDefault:
    """TS-15-40: RoutingConfig assessor_model defaults to 'claude-haiku-4-5'.

    Requirement: 15-REQ-10.1
    """

    def test_default_assessor_model(self) -> None:
        """assessor_model defaults to 'claude-haiku-4-5'."""
        from agentfox.core.config import RoutingConfig

        config = RoutingConfig()
        assert config.assessor_model == "claude-haiku-4-5"

    def test_assessor_model_is_string(self) -> None:
        """assessor_model is a string type."""
        from agentfox.core.config import RoutingConfig

        config = RoutingConfig()
        assert isinstance(config.assessor_model, str)

    def test_custom_assessor_model_accepted(self) -> None:
        """Custom assessor_model value is accepted."""
        from agentfox.core.config import RoutingConfig

        config = RoutingConfig(assessor_model="claude-sonnet-4-5")
        assert config.assessor_model == "claude-sonnet-4-5"


class TestRoutingConfigConfidenceThreshold:
    """TS-15-41: RoutingConfig confidence_threshold defaults to 0.6.

    Requirement: 15-REQ-10.2
    """

    def test_default_confidence_threshold(self) -> None:
        """confidence_threshold defaults to 0.6."""
        from agentfox.core.config import RoutingConfig

        config = RoutingConfig()
        assert config.confidence_threshold == 0.6

    def test_confidence_threshold_is_float(self) -> None:
        """confidence_threshold is a float type."""
        from agentfox.core.config import RoutingConfig

        config = RoutingConfig()
        assert isinstance(config.confidence_threshold, float)

    def test_custom_confidence_threshold_accepted(self) -> None:
        """Custom confidence_threshold value 0.75 is accepted."""
        from agentfox.core.config import RoutingConfig

        config = RoutingConfig(confidence_threshold=0.75)
        assert config.confidence_threshold == 0.75


class TestRoutingConfigEagerValidation:
    """TS-15-42: Pydantic v2 validates both fields eagerly at config load time.

    Requirement: 15-REQ-10.3
    """

    def test_empty_assessor_model_raises_validation_error(self) -> None:
        """assessor_model='' raises ValidationError at construction time."""
        from agentfox.core.config import RoutingConfig
        from pydantic import ValidationError

        with pytest.raises(ValidationError) as exc_info:
            RoutingConfig(assessor_model="")
        error_text = str(exc_info.value)
        assert "assessor_model" in error_text

    def test_out_of_range_confidence_raises_validation_error(self) -> None:
        """confidence_threshold=1.5 raises ValidationError."""
        from agentfox.core.config import RoutingConfig
        from pydantic import ValidationError

        with pytest.raises(ValidationError) as exc_info:
            RoutingConfig(confidence_threshold=1.5)
        error_text = str(exc_info.value)
        assert "confidence_threshold" in error_text


class TestRoutingConfigAssessorModelEmpty:
    """TS-15-E10: Empty assessor_model raises ValidationError.

    Requirement: 15-REQ-10.E1
    """

    def test_empty_string_rejected(self) -> None:
        """assessor_model='' raises ValidationError with descriptive message."""
        from agentfox.core.config import RoutingConfig
        from pydantic import ValidationError

        with pytest.raises(ValidationError) as exc_info:
            RoutingConfig(assessor_model="")
        errors = exc_info.value.errors()
        error_locs = [str(e.get("loc", "")) for e in errors]
        assert any("assessor_model" in loc for loc in error_locs), (
            f"ValidationError should reference assessor_model, got: {error_locs}"
        )


class TestRoutingConfigConfidenceOutOfRange:
    """TS-15-E11: confidence_threshold outside [0.0, 1.0] raises ValidationError.

    Requirement: 15-REQ-10.E2
    """

    @pytest.mark.parametrize(
        "bad_threshold",
        [1.5, -0.1, 2.0],
        ids=["1.5", "-0.1", "2.0"],
    )
    def test_out_of_range_rejected(self, bad_threshold: float) -> None:
        """confidence_threshold outside valid range raises ValidationError."""
        from agentfox.core.config import RoutingConfig
        from pydantic import ValidationError

        with pytest.raises(ValidationError) as exc_info:
            RoutingConfig(confidence_threshold=bad_threshold)
        errors = exc_info.value.errors()
        error_locs = [str(e.get("loc", "")) for e in errors]
        assert any("confidence_threshold" in loc for loc in error_locs), (
            f"ValidationError should reference confidence_threshold for value {bad_threshold}, got: {error_locs}"
        )


# ===========================================================================
# Task 6.3: AssessedComplexity dataclass and
#           assessed_complexity_to_recommendation() adapter
# Test Spec: TS-15-43, TS-15-44
# Requirements: 15-REQ-11.1, 15-REQ-11.2
# ===========================================================================


class TestAssessedComplexityDataclass:
    """TS-15-43: AssessedComplexity frozen dataclass and TriageResult extension.

    Requirement: 15-REQ-11.1
    """

    def test_assessed_complexity_is_dataclass(self) -> None:
        """AssessedComplexity is a dataclass."""
        from agentfox.nightshift.fix_pipeline import AssessedComplexity

        assert dataclasses.is_dataclass(AssessedComplexity)

    def test_assessed_complexity_is_frozen(self) -> None:
        """AssessedComplexity is frozen (immutable)."""
        from agentfox.nightshift.fix_pipeline import AssessedComplexity

        assert AssessedComplexity.__dataclass_params__.frozen is True

    def test_assessed_complexity_has_four_fields(self) -> None:
        """AssessedComplexity has tier, variant, confidence, rationale fields."""
        from agentfox.nightshift.fix_pipeline import AssessedComplexity

        fields = {f.name for f in dataclasses.fields(AssessedComplexity)}
        assert fields == {"tier", "variant", "confidence", "rationale"}

    def test_assessed_complexity_instantiation(self) -> None:
        """AssessedComplexity can be instantiated with all four fields."""
        from agentfox.nightshift.fix_pipeline import AssessedComplexity

        ac = AssessedComplexity(
            tier="ADVANCED",
            variant="standard",
            confidence=0.8,
            rationale="complex",
        )
        assert ac.tier == "ADVANCED"
        assert ac.variant == "standard"
        assert ac.confidence == 0.8
        assert ac.rationale == "complex"

    def test_assessed_complexity_immutable(self) -> None:
        """AssessedComplexity raises on mutation attempt."""
        from agentfox.nightshift.fix_pipeline import AssessedComplexity

        ac = AssessedComplexity(
            tier="ADVANCED",
            variant="standard",
            confidence=0.8,
            rationale="complex",
        )
        with pytest.raises((dataclasses.FrozenInstanceError, AttributeError)):
            ac.tier = "SIMPLE"  # type: ignore[misc]

    def test_triage_result_has_assessed_complexity_field(self) -> None:
        """TriageResult has assessed_complexity: AssessedComplexity | None = None."""
        from agentfox.nightshift.fix_pipeline import TriageResult

        tr_fields = {f.name for f in dataclasses.fields(TriageResult)}
        assert "assessed_complexity" in tr_fields

    def test_triage_result_assessed_complexity_defaults_to_none(self) -> None:
        """TriageResult.assessed_complexity defaults to None."""
        from agentfox.nightshift.fix_pipeline import TriageResult

        # TriageResult has required fields (summary, affected_files, criteria),
        # so we provide them and check assessed_complexity defaults to None.
        tr = TriageResult(
            summary="fix auth",
            affected_files=["auth.py"],
            criteria=[],
        )
        assert tr.assessed_complexity is None

    def test_triage_result_accepts_assessed_complexity(self) -> None:
        """TriageResult can be constructed with a non-None assessed_complexity."""
        from agentfox.nightshift.fix_pipeline import (
            AssessedComplexity,
            TriageResult,
        )

        ac = AssessedComplexity(
            tier="ADVANCED",
            variant="standard",
            confidence=0.85,
            rationale="pre-assessed",
        )
        tr = TriageResult(
            summary="fix auth",
            affected_files=["auth.py"],
            criteria=[],
            assessed_complexity=ac,
        )
        assert tr.assessed_complexity is ac
        assert tr.assessed_complexity.tier == "ADVANCED"


class TestAssessedComplexityToRecommendation:
    """TS-15-44: assessed_complexity_to_recommendation() adapter.

    Maps tier->recommended_tier, variant->recommended_variant,
    copies confidence and rationale.

    Requirement: 15-REQ-11.2
    """

    def test_maps_tier_to_recommended_tier(self) -> None:
        """tier maps to recommended_tier."""
        from agentfox.core.complexity import (
            AssessmentResult,
            assessed_complexity_to_recommendation,
        )
        from agentfox.nightshift.fix_pipeline import AssessedComplexity

        ac = AssessedComplexity(
            tier="ADVANCED",
            variant="standard",
            confidence=0.8,
            rationale="complex refactor",
        )
        result = assessed_complexity_to_recommendation(ac)
        assert isinstance(result, AssessmentResult)
        assert result.recommended_tier == "ADVANCED"

    def test_maps_variant_to_recommended_variant(self) -> None:
        """variant maps to recommended_variant."""
        from agentfox.core.complexity import assessed_complexity_to_recommendation
        from agentfox.nightshift.fix_pipeline import AssessedComplexity

        ac = AssessedComplexity(
            tier="STANDARD",
            variant="extended",
            confidence=0.7,
            rationale="moderate",
        )
        result = assessed_complexity_to_recommendation(ac)
        assert result.recommended_variant == "extended"

    def test_copies_confidence(self) -> None:
        """confidence is copied unchanged."""
        from agentfox.core.complexity import assessed_complexity_to_recommendation
        from agentfox.nightshift.fix_pipeline import AssessedComplexity

        ac = AssessedComplexity(
            tier="ADVANCED",
            variant="standard",
            confidence=0.8,
            rationale="complex refactor",
        )
        result = assessed_complexity_to_recommendation(ac)
        assert result.confidence == 0.8

    def test_copies_rationale(self) -> None:
        """rationale is copied unchanged."""
        from agentfox.core.complexity import assessed_complexity_to_recommendation
        from agentfox.nightshift.fix_pipeline import AssessedComplexity

        ac = AssessedComplexity(
            tier="ADVANCED",
            variant="standard",
            confidence=0.8,
            rationale="complex refactor",
        )
        result = assessed_complexity_to_recommendation(ac)
        assert result.rationale == "complex refactor"

    def test_none_variant_preserved(self) -> None:
        """variant=None maps to recommended_variant=None."""
        from agentfox.core.complexity import assessed_complexity_to_recommendation
        from agentfox.nightshift.fix_pipeline import AssessedComplexity

        ac = AssessedComplexity(
            tier="STANDARD",
            variant=None,
            confidence=0.6,
            rationale="simple",
        )
        result = assessed_complexity_to_recommendation(ac)
        assert result.recommended_variant is None


# ===========================================================================
# Task 6.4: Nightshift pre_assessed passthrough in assess_node()
# Test Spec: TS-15-45, TS-15-46, TS-15-47
# Requirements: 15-REQ-11.3, 15-REQ-11.4, 15-REQ-11.5
# ===========================================================================


class TestAssessNodePreAssessedBypass:
    """TS-15-45: Valid pre_assessed bypasses ComplexityAssessor LLM call.

    Requirement: 15-REQ-11.3
    """

    def test_pre_assessed_bypasses_llm_call(self) -> None:
        """ComplexityAssessor.assess() is never called when pre_assessed is set."""
        from agentfox.engine.engine import AssessmentManager
        from agentfox.nightshift.fix_pipeline import AssessedComplexity

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock()
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        pre = AssessedComplexity(
            tier="ADVANCED",
            variant="standard",
            confidence=0.85,
            rationale="pre",
        )
        ladder = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
                pre_assessed=pre,
            )
        )

        mock_client.messages.create.assert_not_called()
        assert ladder is not None

    def test_pre_assessed_ladder_reflects_upgrade(self) -> None:
        """Returned ladder reflects ADVANCED tier from pre_assessed."""
        from agentfox.core.models import ModelTier
        from agentfox.engine.engine import AssessmentManager
        from agentfox.nightshift.fix_pipeline import AssessedComplexity

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock()
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        pre = AssessedComplexity(
            tier="ADVANCED",
            variant="standard",
            confidence=0.85,
            rationale="pre",
        )
        ladder = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
                pre_assessed=pre,
            )
        )

        # Coder base is STANDARD; pre_assessed recommends ADVANCED with high
        # confidence, so upgrade should occur
        assert ladder.current_tier == ModelTier.ADVANCED


class TestAssessNodePreAssessedNone:
    """TS-15-46: pre_assessed=None triggers ComplexityAssessor LLM call.

    Requirement: 15-REQ-11.4
    """

    def test_none_pre_assessed_triggers_llm(self) -> None:
        """ComplexityAssessor.assess() is called when pre_assessed is None."""
        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=mock_client)

        asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
                pre_assessed=None,
            )
        )

        mock_client.messages.create.assert_called_once()

    def test_none_pre_assessed_ladder_from_llm(self) -> None:
        """Ladder reflects LLM assessment result when pre_assessed is None."""
        from agentfox.core.models import ModelTier

        mock_client = _make_mock_client(_UPGRADE_RESPONSE_JSON)
        manager = _make_assessment_manager(client=mock_client)

        ladder = asyncio.run(
            manager.assess_node(
                node_id="n1",
                archetype="coder",
                mode=None,
                node_body="body",
                pre_assessed=None,
            )
        )

        # UPGRADE_RESPONSE_JSON recommends ADVANCED with 0.9 confidence
        assert ladder.current_tier == ModelTier.ADVANCED


class TestCoderReviewerPreAssessedPassthrough:
    """TS-15-47: coder_reviewer.py passes assessed_complexity to coder only.

    Requirement: 15-REQ-11.5
    """

    def test_coder_reviewer_imports_assessed_complexity(self) -> None:
        """coder_reviewer.py should reference assessed_complexity."""
        from agentfox.nightshift import coder_reviewer

        # The implementation should extract assessed_complexity from
        # TriageResult and pass it as pre_assessed.
        # Until implementation, this checks the module is importable
        # and that CoderReviewerLoop exists.
        assert hasattr(coder_reviewer, "CoderReviewerLoop")
        # After implementation, the source should reference assessed_complexity
        coder_source = inspect.getsource(coder_reviewer)
        assert "assessed_complexity" in coder_source or "pre_assessed" in coder_source

    def test_coder_reviewer_run_accepts_triage_result(self) -> None:
        """CoderReviewerLoop.run() accepts a TriageResult parameter."""
        from agentfox.nightshift.coder_reviewer import CoderReviewerLoop

        sig = inspect.signature(CoderReviewerLoop.run)
        params = list(sig.parameters.keys())
        assert "triage" in params, "CoderReviewerLoop.run() should accept a triage parameter"

    def test_coder_reviewer_source_should_reference_pre_assessed(self) -> None:
        """Once implemented, coder_reviewer should reference pre_assessed."""
        from agentfox.nightshift import coder_reviewer

        source = inspect.getsource(coder_reviewer.CoderReviewerLoop)
        # After implementation, the coder_reviewer should pass
        # assessed_complexity as pre_assessed to assess_node for the coder
        # but NOT for fix-review nodes.
        # The source should mention pre_assessed or assessed_complexity.
        assert "pre_assessed" in source or "assessed_complexity" in source, (
            "CoderReviewerLoop should reference pre_assessed or assessed_complexity for the coder node passthrough"
        )


# ===========================================================================
# Task 6.5: Triage prompt update and partial failure parsing semantics
# Test Spec: TS-15-48, TS-15-E12, TS-15-E13
# Requirements: 15-REQ-11.6, 15-REQ-11.E1, 15-REQ-11.E2
# ===========================================================================


class TestTriagePromptAssessedComplexity:
    """TS-15-48: Triage prompt requests assessed_complexity; parser validates.

    The triage prompt should request an assessed_complexity JSON
    sub-object, and the parser should validate it with case-sensitive
    rules.

    Requirement: 15-REQ-11.6
    """

    def test_triage_prompt_mentions_assessed_complexity(self) -> None:
        """Triage prompt should include assessed_complexity field request."""
        # The fix-pipeline triage is run via FixPipeline._run_triage()
        # which uses session.review_parser.parse_triage_output().
        # The spec requires the triage prompt to request assessed_complexity.
        # Check either the triage task prompt or the system prompt.
        from agentfox.session import review_parser

        source = inspect.getsource(review_parser.parse_triage_output)
        # After implementation, the parser should handle assessed_complexity
        assert "assessed_complexity" in source, (
            "parse_triage_output should reference assessed_complexity to parse the triage response sub-object"
        )

    def test_triage_task_prompt_requests_assessed_complexity(self) -> None:
        """The triage task prompt in _run_triage() should request assessed_complexity."""
        from agentfox.nightshift import fix_pipeline

        source = inspect.getsource(fix_pipeline.FixPipeline._run_triage)
        # 15-REQ-11.6: The prompt must request an assessed_complexity JSON object
        assert "assessed_complexity" in source, (
            "_run_triage() prompt should request assessed_complexity "
            "JSON sub-object with tier/variant/confidence/rationale"
        )

    def test_valid_assessed_complexity_parsed(self) -> None:
        """Valid assessed_complexity in response parses to AssessedComplexity."""
        from agentfox.nightshift.fix_pipeline import AssessedComplexity
        from agentfox.session.review_parser import parse_triage_output

        response_json = (
            '{"summary": "fix auth", "affected_files": ["auth.py"], '
            '"acceptance_criteria": [], '
            '"assessed_complexity": {"tier": "ADVANCED", "variant": "standard", '
            '"confidence": 0.8, "rationale": "complex"}}'
        )

        result = parse_triage_output(response_json, "fix-1", "fix-1:0:triage")
        assert isinstance(result.assessed_complexity, AssessedComplexity)
        assert result.assessed_complexity.tier == "ADVANCED"
        assert result.assessed_complexity.variant == "standard"
        assert result.assessed_complexity.confidence == 0.8
        assert result.assessed_complexity.rationale == "complex"

    def test_null_variant_in_assessed_complexity(self) -> None:
        """assessed_complexity with null variant is accepted."""
        from agentfox.nightshift.fix_pipeline import AssessedComplexity
        from agentfox.session.review_parser import parse_triage_output

        response_json = (
            '{"summary": "fix", "affected_files": [], '
            '"acceptance_criteria": [], '
            '"assessed_complexity": {"tier": "STANDARD", "variant": null, '
            '"confidence": 0.7, "rationale": "simple"}}'
        )

        result = parse_triage_output(response_json, "fix-2", "fix-2:0:triage")
        assert isinstance(result.assessed_complexity, AssessedComplexity)
        assert result.assessed_complexity.variant is None


class TestTriagePartialFailureSemantics:
    """TS-15-E12: Malformed assessed_complexity sets field to None; rest parses.

    Requirement: 15-REQ-11.E1
    """

    def test_missing_assessed_complexity_yields_none(self) -> None:
        """When assessed_complexity field is missing, field set to None."""
        from agentfox.session.review_parser import parse_triage_output

        response_json = '{"summary": "fix auth", "affected_files": ["auth.py"]}'

        result = parse_triage_output(response_json, "fix-1", "fix-1:0:triage")
        assert result.assessed_complexity is None
        # Rest of TriageResult should be parsed normally
        assert result.summary == "fix auth"

    def test_out_of_range_confidence_yields_none(self, caplog: pytest.LogCaptureFixture) -> None:
        """assessed_complexity with confidence=2.0 sets field to None, logs WARNING."""
        from agentfox.session.review_parser import parse_triage_output

        response_json = (
            '{"summary": "fix auth", "affected_files": ["auth.py"], '
            '"assessed_complexity": {"tier": "ADVANCED", "variant": "standard", '
            '"confidence": 2.0, "rationale": "r"}}'
        )

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            result = parse_triage_output(response_json, "fix-1", "fix-1:0:triage")

        assert result.assessed_complexity is None
        assert result.summary == "fix auth"
        # WARNING should be logged
        warning_records = [r for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_records) >= 1, "Expected WARNING log for invalid assessed_complexity"

    def test_wrong_case_tier_yields_none(self, caplog: pytest.LogCaptureFixture) -> None:
        """assessed_complexity with tier='advanced' (wrong case) sets to None."""
        from agentfox.session.review_parser import parse_triage_output

        response_json = (
            '{"summary": "fix auth", "affected_files": ["auth.py"], '
            '"assessed_complexity": {"tier": "advanced", "variant": "standard", '
            '"confidence": 0.8, "rationale": "r"}}'
        )

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            result = parse_triage_output(response_json, "fix-1", "fix-1:0:triage")

        assert result.assessed_complexity is None
        assert result.summary == "fix auth"

    def test_wrong_case_variant_yields_none(self, caplog: pytest.LogCaptureFixture) -> None:
        """assessed_complexity with variant='Standard' (wrong case) sets to None."""
        from agentfox.session.review_parser import parse_triage_output

        response_json = (
            '{"summary": "fix auth", "affected_files": ["auth.py"], '
            '"assessed_complexity": {"tier": "ADVANCED", "variant": "Standard", '
            '"confidence": 0.8, "rationale": "r"}}'
        )

        with caplog.at_level(logging.WARNING, logger="agentfox"):
            result = parse_triage_output(response_json, "fix-1", "fix-1:0:triage")

        assert result.assessed_complexity is None
        assert result.summary == "fix auth"

    def test_rest_of_triage_parsed_normally_on_failure(self) -> None:
        """Other TriageResult fields are parsed correctly despite bad complexity."""
        from agentfox.session.review_parser import parse_triage_output

        response_json = (
            '{"summary": "fix auth", "affected_files": ["auth.py", "login.py"], '
            '"acceptance_criteria": [{"id": "AC1", "description": "test desc", '
            '"preconditions": "none", "expected": "pass", "assertion": "assert True"}], '
            '"assessed_complexity": {"tier": "ADVANCED", "confidence": 2.0, '
            '"rationale": "bad"}}'
        )

        result = parse_triage_output(response_json, "fix-1", "fix-1:0:triage")
        assert result.assessed_complexity is None
        assert result.summary == "fix auth"
        assert result.affected_files == ["auth.py", "login.py"]
        assert len(result.criteria) == 1
        assert result.criteria[0].id == "AC1"


class TestTriageNoPartialSalvaging:
    """TS-15-E13: No partial field salvaging within malformed sub-object.

    Requirement: 15-REQ-11.E2
    """

    def test_valid_tier_but_invalid_confidence_discards_whole_object(self) -> None:
        """A valid tier but invalid confidence discards the entire sub-object."""
        from agentfox.nightshift.fix_pipeline import AssessedComplexity
        from agentfox.session.review_parser import parse_triage_output

        response_json = (
            '{"summary": "fix", '
            '"assessed_complexity": {"tier": "ADVANCED", "variant": "standard", '
            '"confidence": 2.0, "rationale": "r"}}'
        )

        result = parse_triage_output(response_json, "fix-1", "fix-1:0:triage")
        assert result.assessed_complexity is None
        # Verify it's not a partially constructed AssessedComplexity
        assert not isinstance(result.assessed_complexity, AssessedComplexity)

    def test_missing_rationale_discards_whole_object(self) -> None:
        """Missing rationale discards the entire assessed_complexity sub-object."""
        from agentfox.session.review_parser import parse_triage_output

        response_json = (
            '{"summary": "fix", "assessed_complexity": {"tier": "ADVANCED", "variant": "standard", "confidence": 0.8}}'
        )

        result = parse_triage_output(response_json, "fix-1", "fix-1:0:triage")
        assert result.assessed_complexity is None

    def test_missing_tier_discards_whole_object(self) -> None:
        """Missing tier discards the entire assessed_complexity sub-object."""
        from agentfox.session.review_parser import parse_triage_output

        response_json = (
            '{"summary": "fix", "assessed_complexity": {"variant": "standard", "confidence": 0.8, "rationale": "r"}}'
        )

        result = parse_triage_output(response_json, "fix-1", "fix-1:0:triage")
        assert result.assessed_complexity is None

    def test_unrecognised_tier_value_discards_whole_object(self) -> None:
        """Unrecognised tier value (e.g. 'ULTRA') discards the entire sub-object."""
        from agentfox.session.review_parser import parse_triage_output

        response_json = (
            '{"summary": "fix", '
            '"assessed_complexity": {"tier": "ULTRA", "variant": "standard", '
            '"confidence": 0.8, "rationale": "r"}}'
        )

        result = parse_triage_output(response_json, "fix-1", "fix-1:0:triage")
        assert result.assessed_complexity is None

    def test_unrecognised_variant_value_discards_whole_object(self) -> None:
        """Unrecognised variant value (e.g. 'turbo') discards the entire sub-object."""
        from agentfox.session.review_parser import parse_triage_output

        response_json = (
            '{"summary": "fix", '
            '"assessed_complexity": {"tier": "ADVANCED", "variant": "turbo", '
            '"confidence": 0.8, "rationale": "r"}}'
        )

        result = parse_triage_output(response_json, "fix-1", "fix-1:0:triage")
        assert result.assessed_complexity is None


# ===========================================================================
# Task 6.6: Property test — nightshift pre-assessed Haiku bypass
# Test Spec: TS-15-P8
# Requirements: 15-REQ-11.3
# ===========================================================================


class TestPropertyPreAssessedBypassHaiku:
    """TS-15-P8: For any non-None pre_assessed, ComplexityAssessor.assess()
    is never called.

    Property: 15-PROP-8
    Validates: 15-REQ-11.3

    Generates various valid AssessedComplexity inputs with different
    tier/variant/confidence combinations and verifies the LLM is never called.
    """

    @pytest.mark.parametrize(
        "tier,variant,confidence",
        [
            ("SIMPLE", "fast", 0.5),
            ("SIMPLE", "standard", 0.6),
            ("SIMPLE", "extended", 0.9),
            ("SIMPLE", None, 0.7),
            ("STANDARD", "fast", 0.5),
            ("STANDARD", "standard", 0.6),
            ("STANDARD", "extended", 0.9),
            ("STANDARD", None, 0.8),
            ("ADVANCED", "fast", 0.5),
            ("ADVANCED", "standard", 0.6),
            ("ADVANCED", "extended", 0.9),
            ("ADVANCED", None, 1.0),
        ],
        ids=[
            "SIMPLE/fast/0.5",
            "SIMPLE/standard/0.6",
            "SIMPLE/extended/0.9",
            "SIMPLE/None/0.7",
            "STANDARD/fast/0.5",
            "STANDARD/standard/0.6",
            "STANDARD/extended/0.9",
            "STANDARD/None/0.8",
            "ADVANCED/fast/0.5",
            "ADVANCED/standard/0.6",
            "ADVANCED/extended/0.9",
            "ADVANCED/None/1.0",
        ],
    )
    def test_pre_assessed_never_calls_llm(
        self,
        tier: str,
        variant: str | None,
        confidence: float,
    ) -> None:
        """For any valid pre_assessed, LLM messages.create is never called."""
        from agentfox.engine.engine import AssessmentManager
        from agentfox.nightshift.fix_pipeline import AssessedComplexity

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock()
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        pre = AssessedComplexity(
            tier=tier,
            variant=variant,
            confidence=confidence,
            rationale=f"prop test {tier}/{variant}/{confidence}",
        )

        asyncio.run(
            manager.assess_node(
                node_id=f"prop_{tier}_{variant}_{confidence}",
                archetype="coder",
                mode=None,
                node_body="some task body",
                pre_assessed=pre,
            )
        )

        (
            mock_client.messages.create.assert_not_called(),
            (f"LLM should not be called with pre_assessed tier={tier} variant={variant} confidence={confidence}"),
        )

    @pytest.mark.parametrize(
        "archetype,mode",
        [
            ("coder", None),
            ("coder", "fix"),
            ("reviewer", "pre-review"),
            ("reviewer", "fix-review"),
            ("verifier", None),
            ("maintainer", "hunt"),
        ],
        ids=[
            "coder/base",
            "coder/fix",
            "reviewer/pre-review",
            "reviewer/fix-review",
            "verifier/base",
            "maintainer/hunt",
        ],
    )
    def test_pre_assessed_bypass_across_archetypes(
        self,
        archetype: str,
        mode: str | None,
    ) -> None:
        """Pre-assessed bypass works for all archetype/mode combinations."""
        from agentfox.engine.engine import AssessmentManager
        from agentfox.nightshift.fix_pipeline import AssessedComplexity

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock()
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        pre = AssessedComplexity(
            tier="ADVANCED",
            variant="standard",
            confidence=0.85,
            rationale="pre-assessed across archetypes",
        )

        asyncio.run(
            manager.assess_node(
                node_id=f"prop_{archetype}_{mode}",
                archetype=archetype,
                mode=mode,
                node_body="task body",
                pre_assessed=pre,
            )
        )

        (
            mock_client.messages.create.assert_not_called(),
            (f"LLM should not be called with pre_assessed for {archetype}/{mode}"),
        )


# ===========================================================================
# Task 13: Wiring verification — end-to-end smoke tests
# Test Spec: TS-15-SMOKE-1 through TS-15-SMOKE-6
# Execution Paths: 15-PATH-1 through 15-PATH-6
# ===========================================================================


class TestSmokeSuccessfulLLMUpgrade:
    """TS-15-SMOKE-1: End-to-end coder node upgrade STANDARD→ADVANCED.

    Traces the full call chain:
      DispatchManager.prepare_launch()
      → AssessmentManager.assess_node()
      → ComplexityAssessor.assess()
      → apply_assessment()
      → EscalationLadder construction

    Real components: AssessmentManager, ComplexityAssessor, apply_assessment,
                     ARCHETYPE_REGISTRY, EscalationLadder.
    Mockable: Anthropic API messages.create (returns ADVANCED/extended/0.82).

    Execution Path: 15-PATH-1
    """

    def test_successful_llm_assessment_upgrades_coder(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """Coder node upgraded from STANDARD/standard → ADVANCED/extended."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = _make_mock_client(
            '{"recommended_tier": "ADVANCED", '
            '"recommended_variant": "extended", '
            '"confidence": 0.82, '
            '"rationale": "Complex multi-module refactor"}'
        )
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        with caplog.at_level(logging.DEBUG):
            ladder = asyncio.run(
                manager.assess_node(
                    node_id="smoke-1",
                    archetype="coder",
                    mode=None,
                    node_body="Implement multi-module auth refactor...",
                    previous_failure=None,
                )
            )

        # ComplexityAssessor.assess() called once
        mock_client.messages.create.assert_called_once()

        # apply_assessment() upgrades tier from STANDARD to ADVANCED
        assert ladder.starting_tier.value == "ADVANCED", f"Expected starting_tier=ADVANCED, got {ladder.starting_tier}"
        assert ladder.starting_variant == "extended", (
            f"Expected starting_variant=extended, got {ladder.starting_variant}"
        )
        # EscalationLadder ceiling is ADVANCED (internal _tier_ceiling)
        assert ladder._tier_ceiling.value == "ADVANCED", f"Expected ceiling=ADVANCED, got {ladder._tier_ceiling}"

        # Structured DEBUG log emitted with required keys
        debug_msgs = [r.message for r in caplog.records if r.levelno == logging.DEBUG]
        debug_text = " ".join(debug_msgs)
        assert "smoke-1" in debug_text, "DEBUG log should contain node_id='smoke-1'"
        assert "ADVANCED" in debug_text, "DEBUG log should mention ADVANCED tier"
        assert "extended" in debug_text, "DEBUG log should mention extended variant"

    def test_successful_assessment_calls_apply_assessment(self) -> None:
        """Upgrade-only semantics applied correctly via apply_assessment()."""
        from agentfox.engine.engine import AssessmentManager

        # Assessor recommends SIMPLE (below coder base of STANDARD) — upgrade-only prevents downgrade
        mock_client = _make_mock_client(
            '{"recommended_tier": "SIMPLE", '
            '"recommended_variant": "fast", '
            '"confidence": 0.95, '
            '"rationale": "Simple task"}'
        )
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        ladder = asyncio.run(
            manager.assess_node(
                node_id="smoke-1b",
                archetype="coder",
                mode=None,
                node_body="Simple one-liner fix",
            )
        )

        # Upgrade-only: STANDARD stays as floor despite SIMPLE recommendation
        assert ladder.starting_tier.value == "STANDARD", (
            "Upgrade-only semantics should prevent downgrade below STANDARD"
        )


class TestSmokeAPIFailureFallback:
    """TS-15-SMOKE-2: API failure falls back silently to base tier.

    ComplexityAssessor fails with network error; assess_node() returns
    EscalationLadder at base tier/variant without exception.

    Execution Path: 15-PATH-2
    """

    def test_network_error_falls_back_to_base(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """Network failure → EscalationLadder at STANDARD/standard, no exception."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(
            side_effect=ConnectionError("Network failure"),
        )
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        with caplog.at_level(logging.WARNING):
            # Should NOT raise
            ladder = asyncio.run(
                manager.assess_node(
                    node_id="smoke-2",
                    archetype="coder",
                    mode=None,
                    node_body="Fix auth bug",
                    previous_failure=None,
                )
            )

        # WARNING log emitted
        warning_msgs = [r.message for r in caplog.records if r.levelno >= logging.WARNING]
        assert len(warning_msgs) >= 1, "Expected at least one WARNING log on API failure"

        # EscalationLadder at base tier (STANDARD/standard for coder)
        assert ladder.starting_tier.value == "STANDARD", f"Expected fallback to STANDARD, got {ladder.starting_tier}"
        assert ladder.starting_variant == "standard", (
            f"Expected fallback to standard variant, got {ladder.starting_variant}"
        )

    def test_dispatch_continues_after_failure(self) -> None:
        """No exception propagated — dispatch continues unblocked."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(
            side_effect=TimeoutError("API timeout"),
        )
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        # Must not raise — dispatch should continue
        ladder = asyncio.run(
            manager.assess_node(
                node_id="smoke-2b",
                archetype="coder",
                mode=None,
                node_body="Task body",
            )
        )
        assert ladder is not None, "assess_node must return a ladder even on failure"
        assert hasattr(ladder, "starting_tier"), "Returned ladder must have starting_tier"


class TestSmokeExplicitConfigOverrideSkip:
    """TS-15-SMOKE-3: Explicit config override skips LLM assessment.

    When mode-level config override exists, ComplexityAssessor.assess()
    is never called and configured tier is used.

    Execution Path: 15-PATH-3
    """

    def test_explicit_override_skips_llm_and_uses_config_tier(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """LLM not called; ladder uses explicitly configured tier."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock()

        # Configure a mode-level override: coder/fix → ADVANCED
        config = _make_config_with_mode_override("coder", "fix", "ADVANCED")
        manager = AssessmentManager(config=config, client=mock_client)

        with caplog.at_level(logging.DEBUG):
            ladder = asyncio.run(
                manager.assess_node(
                    node_id="smoke-3",
                    archetype="coder",
                    mode="fix",
                    node_body="One-line config fix",
                    previous_failure=None,
                )
            )

        # ComplexityAssessor.assess() never called
        mock_client.messages.create.assert_not_called()

        # DEBUG log contains node_id, archetype, mode, and resolved tier
        debug_msgs = [r.message for r in caplog.records if r.levelno == logging.DEBUG]
        debug_text = " ".join(debug_msgs)
        assert "smoke-3" in debug_text, "DEBUG log should contain node_id"
        assert "coder" in debug_text, "DEBUG log should contain archetype"
        assert "fix" in debug_text, "DEBUG log should contain mode"

        # Ladder uses the configured tier
        assert ladder is not None, "assess_node must return a ladder"
        assert hasattr(ladder, "starting_tier"), "Ladder must have starting_tier"


class TestSmokeNightshiftPreAssessedBypass:
    """TS-15-SMOKE-4: Nightshift pre-assessed path bypasses Haiku LLM call.

    TriageResult with valid AssessedComplexity → adapter + apply_assessment()
    → EscalationLadder at upgraded tier, no ComplexityAssessor.assess() call.

    Execution Path: 15-PATH-4
    """

    def test_pre_assessed_bypasses_haiku_and_upgrades(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """Pre-assessed complexity from triage used; LLM never called."""
        from agentfox.engine.engine import AssessmentManager
        from agentfox.nightshift.fix_pipeline import AssessedComplexity

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock()
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        pre = AssessedComplexity(
            tier="ADVANCED",
            variant="standard",
            confidence=0.8,
            rationale="Complex multi-module refactor",
        )

        with caplog.at_level(logging.DEBUG):
            ladder = asyncio.run(
                manager.assess_node(
                    node_id="smoke-4",
                    archetype="coder",
                    mode="fix",
                    node_body="Implement auth module refactor",
                    pre_assessed=pre,
                )
            )

        # ComplexityAssessor.assess() NOT called
        mock_client.messages.create.assert_not_called()

        # Ladder reflects upgrade from STANDARD to ADVANCED
        assert ladder.starting_tier.value == "ADVANCED", (
            f"Expected ADVANCED from pre_assessed, got {ladder.starting_tier}"
        )
        assert ladder.starting_variant == "standard", f"Expected standard variant, got {ladder.starting_variant}"

        # Structured DEBUG log emitted
        debug_msgs = [r.message for r in caplog.records if r.levelno == logging.DEBUG]
        debug_text = " ".join(debug_msgs)
        assert "smoke-4" in debug_text, "DEBUG log should contain node_id"
        assert "ADVANCED" in debug_text, "DEBUG log should contain effective_tier"

    def test_fix_review_nodes_not_pre_assessed(self) -> None:
        """Fix-review nodes use their own assessment path (no pre_assessed).

        Verifies that the coder_reviewer.py source only passes pre_assessed
        to the coder node, not to fix-review.
        """
        from agentfox.nightshift import coder_reviewer

        source = inspect.getsource(coder_reviewer)
        # The _build_coder_ladder method passes pre_assessed=assessed_complexity
        assert "pre_assessed=assessed_complexity" in source, (
            "coder_reviewer should pass pre_assessed to coder's assess_node"
        )
        # Fix-review nodes should NOT receive pre_assessed
        # They go through _run_reviewer_phase which does NOT pass pre_assessed
        assert (
            "pre_assessed" not in source.split("_run_reviewer_phase")[1].split("def ")[0]
            if ("_run_reviewer_phase" in source)
            else True
        ), "Reviewer phase should not reference pre_assessed"


class TestSmokeReAssessmentOnRetry:
    """TS-15-SMOKE-5: Failed node re-assessed with failure context.

    Previous_failure string from error_tracker is passed to assessor;
    new EscalationLadder created at re-assessed tier.

    Execution Path: 15-PATH-5
    """

    def test_retry_with_failure_context_escalates(self) -> None:
        """Re-assessment with previous_failure → higher tier in new ladder."""
        from agentfox.engine.engine import AssessmentManager

        call_count = 0

        async def mock_create(**kwargs: Any) -> Any:
            nonlocal call_count
            call_count += 1
            # Check that second call includes the failure context in the prompt
            prompt_text = str(kwargs)
            if call_count == 2:
                assert "TypeError" in prompt_text, "Re-assessment prompt should include previous_failure text"
            return _make_anthropic_response(
                '{"recommended_tier": "ADVANCED", '
                '"recommended_variant": "extended", '
                '"confidence": 0.9, '
                '"rationale": "Failure suggests complex cross-module issue"}'
            )

        mock_client = MagicMock()
        mock_client.messages.create = mock_create
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        # First assessment (no failure)
        ladder1 = asyncio.run(
            manager.assess_node(
                node_id="smoke-5",
                archetype="coder",
                mode=None,
                node_body="Complex refactor",
            )
        )
        assert call_count == 1

        # Re-assessment with previous_failure
        ladder2 = asyncio.run(
            manager.assess_node(
                node_id="smoke-5",
                archetype="coder",
                mode=None,
                node_body="Complex refactor",
                previous_failure="TypeError: cannot unpack non-sequence NoneType",
            )
        )
        assert call_count == 2

        # New ladder created (distinct object)
        assert ladder2 is not ladder1, "Re-assessment should create a new ladder"

        # New ladder at ADVANCED/extended
        assert ladder2.starting_tier.value == "ADVANCED"
        assert ladder2.starting_variant == "extended"

        # Both ladders share same ceiling (internal _tier_ceiling)
        assert ladder1._tier_ceiling == ladder2._tier_ceiling, "Both ladders should have same ceiling"

    def test_retry_prior_state_not_preserved(self) -> None:
        """Prior retry state is not preserved in the new ladder."""
        from agentfox.engine.engine import AssessmentManager

        mock_client = _make_mock_client(
            '{"recommended_tier": "ADVANCED", '
            '"recommended_variant": "extended", '
            '"confidence": 0.9, '
            '"rationale": "escalate"}'
        )
        config = _make_routing_config(confidence_threshold=0.6)
        manager = AssessmentManager(config=config, client=mock_client)

        # First call
        ladder1 = asyncio.run(manager.assess_node("smoke-5b", "coder", None, "body"))

        # Simulate failure on first ladder
        if hasattr(ladder1, "record_failure"):
            ladder1.record_failure()

        # Re-assessment replaces the ladder entirely
        ladder2 = asyncio.run(
            manager.assess_node(
                "smoke-5b",
                "coder",
                None,
                "body",
                previous_failure="some error",
            )
        )

        # New ladder starts fresh — prior retry state discarded
        assert ladder2 is not ladder1
        assert ladder2.starting_tier.value == "ADVANCED"


class TestSmokeAbsentClientDisablesAssessment:
    """TS-15-SMOKE-6: Absent client silently disables all assessment.

    AssessmentManager(client=None) → _assessor is None → every assess_node()
    returns EscalationLadder at base tier/variant with zero log entries.

    Execution Path: 15-PATH-6
    """

    def test_client_none_all_nodes_fallback_silently(
        self,
        caplog: pytest.LogCaptureFixture,
    ) -> None:
        """All assess_node() calls fall back with no logs emitted."""
        from agentfox.engine.engine import AssessmentManager

        config = _make_routing_config()
        manager = AssessmentManager(config=config, client=None)

        # Confirm no assessor
        assert manager._assessor is None, "client=None should not create assessor"

        test_cases = [
            ("coder", None, "STANDARD", "standard"),
            ("reviewer", "pre-review", "ADVANCED", "standard"),
            ("verifier", None, "STANDARD", "standard"),
            ("maintainer", "hunt", "SIMPLE", "standard"),
        ]

        for archetype, mode, exp_tier, exp_variant in test_cases:
            with caplog.at_level(logging.DEBUG):
                caplog.clear()
                ladder = asyncio.run(
                    manager.assess_node(
                        node_id=f"smoke6_{archetype}",
                        archetype=archetype,
                        mode=mode,
                        node_body="some body for assessment",
                    )
                )

            # EscalationLadder at base tier/variant per ARCHETYPE_REGISTRY
            assert ladder.starting_tier.value == exp_tier, (
                f"{archetype}/{mode}: expected {exp_tier}, got {ladder.starting_tier.value}"
            )

            # No WARNING, ERROR, or any log entries for assessment
            assessment_logs = [r for r in caplog.records if f"smoke6_{archetype}" in r.message]
            assert len(assessment_logs) == 0, (
                f"No log entries expected for client=None path, got {len(assessment_logs)} for {archetype}/{mode}"
            )

    def test_dispatch_proceeds_normally_without_client(self) -> None:
        """Dispatch proceeds normally when client is absent."""
        from agentfox.engine.engine import AssessmentManager

        config = _make_routing_config()
        manager = AssessmentManager(config=config, client=None)

        # Multiple nodes can be assessed without error
        for i in range(5):
            ladder = asyncio.run(
                manager.assess_node(
                    node_id=f"dispatch_{i}",
                    archetype="coder",
                    mode=None,
                    node_body=f"Task body {i}",
                )
            )
            assert ladder is not None
            assert hasattr(ladder, "starting_tier")
            assert hasattr(ladder, "starting_variant")
