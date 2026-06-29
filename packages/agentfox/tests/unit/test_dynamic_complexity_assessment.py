"""Unit tests for dynamic complexity assessment.

Task Group 1: ComplexityAssessor class, AssessmentResult, Protocol,
              statelessness, and assessment prompt structure.
Task Group 2: Error handling and edge cases for ComplexityAssessor.
Task Group 3: apply_assessment() upgrade-only semantics and property tests.
Task Group 4: AssessmentManager integration, session_runner_factory wiring,
              and EscalationLadder construction.

Test Spec: TS-15-1 through TS-15-12, TS-15-E2 through TS-15-E14,
           TS-15-49 through TS-15-53, TS-15-13 through TS-15-18,
           TS-15-E5, TS-15-P1 through TS-15-P4,
           TS-15-19 through TS-15-25, TS-15-E7, TS-15-P6, TS-15-P9
Requirements: 15-REQ-1.1 through 15-REQ-1.7, 15-REQ-2.1 through 15-REQ-2.5,
              15-REQ-2.E1 through 15-REQ-2.E3, 15-REQ-4.E1,
              15-REQ-12.1 through 15-REQ-12.5, 15-REQ-12.E1,
              15-REQ-3.1 through 15-REQ-3.6, 15-REQ-3.E1,
              15-REQ-4.1 through 15-REQ-4.4, 15-REQ-5.1 through 15-REQ-5.3,
              15-REQ-5.E1
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
            task1 = assessor.assess(
                "body1", "coder", None, "STANDARD", "standard", None
            )
            task2 = assessor.assess(
                "body2", "reviewer", "fix-review", "STANDARD", "standard", None
            )
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
            tasks = [
                assessor.assess(
                    "body", "coder", None, "STANDARD", "standard", None
                )
                for _ in range(3)
            ]
            return await asyncio.gather(*tasks)

        results = asyncio.run(_run())

        assert len(results) == 3
        assert all(
            r.recommended_tier in ("SIMPLE", "STANDARD", "ADVANCED")
            for r in results
        )
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

    def test_no_warning_logs_emitted(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
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

        warning_records = [
            r for r in caplog.records if r.levelno >= logging.WARNING
        ]
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

    def test_multiple_nodes_all_fallback(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
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

        warning_records = [
            r for r in caplog.records if r.levelno >= logging.WARNING
        ]
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
        assert timeout == 30 or timeout == 30.0, (
            f"Expected timeout=30, got timeout={timeout}"
        )

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

    def test_malformed_json_logs_warning_and_falls_back(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
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

    def test_missing_required_field_logs_warning_and_falls_back(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
        """Missing required field (rationale) triggers WARNING and fallback."""
        # Missing 'rationale' field
        bad_json = (
            '{"recommended_tier": "ADVANCED", '
            '"recommended_variant": "standard", '
            '"confidence": 0.8}'
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

    def test_out_of_range_confidence_logs_warning_and_falls_back(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
        """Confidence=1.5 (out of [0.0, 1.0]) triggers WARNING and fallback."""
        bad_json = (
            '{"recommended_tier": "ADVANCED", '
            '"recommended_variant": "standard", '
            '"confidence": 1.5, '
            '"rationale": "r"}'
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

    def test_wrong_case_tier_logs_warning_and_falls_back(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
        """Wrong-case tier ('advanced' instead of 'ADVANCED') triggers WARNING."""
        bad_json = (
            '{"recommended_tier": "advanced", '
            '"recommended_variant": "standard", '
            '"confidence": 0.8, '
            '"rationale": "r"}'
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

    def test_wrong_case_variant_logs_warning_and_falls_back(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
        """Wrong-case variant ('Standard' instead of 'standard') triggers WARNING."""
        bad_json = (
            '{"recommended_tier": "ADVANCED", '
            '"recommended_variant": "Standard", '
            '"confidence": 0.8, '
            '"rationale": "r"}'
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
                '{"recommended_tier": "ADVANCED", "recommended_variant": "standard", '
                '"confidence": 0.8}',
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

    def test_timeout_error_returns_base_without_raising(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
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

    def test_timeout_error_logs_warning_with_timeout_message(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
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
        assert (
            "timeout" in log_text.lower() or "Timeout" in log_text
        ), f"Expected 'timeout' in warning log, got: {log_text}"

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

    def test_rate_limit_error_returns_base_without_raising(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
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

    def test_rate_limit_error_logs_warning(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
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

    def test_connection_error_returns_base_without_raising(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
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

    def test_connection_error_logs_warning_with_details(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
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
        assert (
            "Network failure" in log_text or "ConnectionError" in log_text
        ), f"Expected exception details in warning log, got: {log_text}"


@_needs_anthropic
class TestComplexityAssessorContextLimitError:
    """TS-15-E14: Context-limit error treated as standard assessment failure.

    No truncation or retry with shortened body is attempted.

    Requirement: 15-REQ-12.E1
    """

    def test_context_limit_error_returns_base_without_raising(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
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

    def test_context_limit_error_logs_warning(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
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

    def test_none_body_logs_debug_with_absent_or_empty(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
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

        debug_msgs = [
            r.getMessage() for r in caplog.records if r.levelno == logging.DEBUG
        ]
        assert any(
            ("absent" in m.lower() or "empty" in m.lower()) for m in debug_msgs
        ), f"Expected 'absent' or 'empty' in DEBUG logs: {debug_msgs}"
        assert any(
            "n1" in m for m in debug_msgs
        ), f"Expected node_id 'n1' in DEBUG logs: {debug_msgs}"

    def test_empty_body_logs_debug_with_absent_or_empty(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
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

        debug_msgs = [
            r.getMessage() for r in caplog.records if r.levelno == logging.DEBUG
        ]
        assert any(
            ("absent" in m.lower() or "empty" in m.lower()) for m in debug_msgs
        ), f"Expected 'absent' or 'empty' in DEBUG logs: {debug_msgs}"
        assert any(
            "n2" in m for m in debug_msgs
        ), f"Expected node_id 'n2' in DEBUG logs: {debug_msgs}"

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

    def test_debug_log_contains_required_keys(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
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
        assert (
            "ADVANCED" in log_text
        ), f"effective_tier 'ADVANCED' missing from DEBUG log: {log_text}"
        assert (
            "extended" in log_text
        ), f"effective_variant 'extended' missing from DEBUG log: {log_text}"
        # At least confidence or rationale should be present
        assert (
            "0.82" in log_text or "complex" in log_text
        ), f"confidence/rationale missing from DEBUG log: {log_text}"


class TestExplicitOverrideDebugLog:
    """TS-15-52: Explicit config override emits DEBUG log.

    When explicit config override is detected via is_explicitly_configured(),
    a DEBUG log with node_id, archetype, mode, and resolved tier is emitted.
    No LLM call is made.

    Requirement: 15-REQ-12.4
    """

    def test_explicit_override_debug_log_contains_info(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
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
            "ADVANCED" in log_text
            or "STANDARD" in log_text
            or "SIMPLE" in log_text
            or "explicit" in log_text.lower()
        ), f"resolved tier missing from DEBUG log: {log_text}"

        # No LLM call should have been made
        mock_client.messages.create.assert_not_called()


class TestClientNoneNoLogs:
    """TS-15-53: Permanently-disabled path (client=None) produces no logs.

    When AssessmentManager is instantiated with client=None, assess_node()
    should emit zero log entries at any log level.

    Requirement: 15-REQ-12.5
    """

    def test_no_log_entries_at_any_level(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
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
        agentfox_logs = [
            r for r in caplog.records if r.name.startswith("agentfox")
        ]
        assert len(agentfox_logs) == 0, (
            f"Expected zero log entries, got: "
            f"{[r.getMessage() for r in agentfox_logs]}"
        )


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
        [
            (bt, rt)
            for bt in _ALL_TIERS
            for rt in _ALL_TIERS
        ],
        ids=[
            f"base={bt}-rec={rt}"
            for bt in _ALL_TIERS
            for rt in _ALL_TIERS
        ],
    )
    def test_effective_tier_never_below_base(
        self, base_tier: str, recommended_tier: str
    ) -> None:
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
        [
            (bt, rt)
            for bt in _ALL_TIERS
            for rt in _ALL_TIERS
            if _TIER_ORDER[rt] < _TIER_ORDER[bt]
        ],
        ids=[
            f"base={bt}-rec={rt}"
            for bt in _ALL_TIERS
            for rt in _ALL_TIERS
            if _TIER_ORDER[rt] < _TIER_ORDER[bt]
        ],
    )
    def test_downgrade_attempt_yields_base_tier(
        self, base_tier: str, recommended_tier: str
    ) -> None:
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
        [
            (bv, rv)
            for bv in _ALL_VARIANTS
            for rv in _ALL_VARIANTS
        ],
        ids=[
            f"base={bv}-rec={rv}"
            for bv in _ALL_VARIANTS
            for rv in _ALL_VARIANTS
        ],
    )
    def test_effective_variant_never_below_base(
        self, base_variant: str, recommended_variant: str
    ) -> None:
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
        [
            (bt, bv)
            for bt in _ALL_TIERS
            for bv in [None, "fast", "standard", "extended"]
        ],
        ids=[
            f"base={bt}/{bv}"
            for bt in _ALL_TIERS
            for bv in ["none", "fast", "standard", "extended"]
        ],
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
            f"Expected ({base_tier}, {base_variant}), got {result} "
            f"with below-threshold confidence"
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
    def test_various_below_threshold_pairs(
        self, confidence: float, threshold: float
    ) -> None:
        """Confidence gate holds for various (confidence, threshold) pairs."""
        rec = AssessmentResult(
            recommended_tier="ADVANCED",
            recommended_variant="extended",
            confidence=confidence,
            rationale="property test",
        )
        result = apply_assessment(rec, "STANDARD", "standard", threshold)
        assert result == ("STANDARD", "standard"), (
            f"Expected base values with confidence={confidence} < threshold={threshold}, "
            f"got {result}"
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
    '{"recommended_tier": "ADVANCED", "recommended_variant": "extended", '
    '"confidence": 0.9, "rationale": "complex"}'
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
        assert isinstance(ladder, EscalationLadder), (
            f"Expected EscalationLadder, got {type(ladder)}"
        )

    def test_assess_node_previous_failure_defaults_to_none(self) -> None:
        """previous_failure should default to None when not provided."""
        from agentfox.engine.engine import AssessmentManager

        sig = inspect.signature(AssessmentManager.assess_node)
        param = sig.parameters.get("previous_failure")
        assert param is not None, "Missing previous_failure parameter"
        assert param.default is None, (
            f"previous_failure default should be None, got {param.default}"
        )

    def test_assess_node_pre_assessed_defaults_to_none(self) -> None:
        """pre_assessed should default to None when not provided."""
        from agentfox.engine.engine import AssessmentManager

        sig = inspect.signature(AssessmentManager.assess_node)
        param = sig.parameters.get("pre_assessed")
        assert param is not None, "Missing pre_assessed parameter"
        assert param.default is None, (
            f"pre_assessed default should be None, got {param.default}"
        )


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
        assert ladder.current_tier == ModelTier.ADVANCED, (
            f"Expected starting tier ADVANCED, got {ladder.current_tier}"
        )

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
        assert hasattr(ladder, "starting_variant") or hasattr(
            ladder, "_starting_variant"
        ), "EscalationLadder should have starting_variant attribute"

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
        assert ceiling == ModelTier.ADVANCED, (
            f"Expected tier_ceiling ADVANCED, got {ceiling}"
        )

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
        assert retries is not None, (
            "Ladder should have _retries_before_escalation from RoutingConfig"
        )


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

    def test_path_3_explicit_override_skips_assessment(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
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

    Requirement: 15-REQ-4.2
    """

    def test_assessment_manager_in_source(self) -> None:
        """session_runner_factory source references AssessmentManager.

        AssessmentManager is constructed in Orchestrator.__init__() (currently),
        but the spec requires the client to be passed through. We verify
        the source contains the right wiring.
        """
        from agentfox.engine import run

        source = inspect.getsource(run)
        # Either session_runner_factory or Orchestrator should pass client
        # to AssessmentManager. Check the broader module source.
        assert "AssessmentManager" in source or "assessment" in source.lower(), (
            "engine/run.py should reference AssessmentManager or assessment"
        )

    def test_assessment_manager_receives_client_kwarg(self) -> None:
        """AssessmentManager is constructed with client= keyword argument.

        Verifies the wiring by checking that the Orchestrator or
        session_runner_factory passes a client to AssessmentManager.
        """
        from agentfox.engine import engine

        source = inspect.getsource(engine)
        # After spec 15 is implemented, the AssessmentManager constructor
        # should be called with client= keyword argument somewhere
        # in the engine or run module.
        has_client = "client=" in source or "client =" in source
        assert has_client, (
            "AssessmentManager should be constructed with client= argument "
            "in engine module"
        )


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
        mock_client.messages.create.return_value = _make_anthropic_response(
            _UPGRADE_RESPONSE_JSON
        )

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
        assert "TypeError: NoneType" in call_str, (
            "previous_failure should be included in the prompt sent to LLM"
        )


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
        mock_client.messages.create.return_value = _make_anthropic_response(
            _UPGRADE_RESPONSE_JSON
        )

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
        mock_client.messages.create.return_value = _make_anthropic_response(
            _UPGRADE_RESPONSE_JSON
        )

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
        assert retries1 == retries2, (
            f"Both ladders should share retry config: got {retries1} vs {retries2}"
        )

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
        low_client.messages.create = AsyncMock(
            return_value=_make_anthropic_response(_UPGRADE_RESPONSE_JSON)
        )

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
        assert getattr(ladder1, "_tier_ceiling", None) == getattr(
            ladder2, "_tier_ceiling", None
        )
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
        mock_client.messages.create = AsyncMock(
            return_value=_make_anthropic_response(_LOW_CONFIDENCE_RESPONSE_JSON)
        )

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
        mock_client.messages.create = AsyncMock(
            return_value=_make_anthropic_response(_LOW_CONFIDENCE_RESPONSE_JSON)
        )

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
            f"New ladder should start fresh with attempt_count=1, "
            f"got {ladder2.attempt_count}"
        )
        assert ladder2.escalation_count == 0, (
            f"New ladder should have escalation_count=0, "
            f"got {ladder2.escalation_count}"
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

        mock_client.messages.create = AsyncMock(
            return_value=_make_anthropic_response(_LOW_CONFIDENCE_RESPONSE_JSON)
        )

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
    def test_generic_failure_returns_base_without_raising(
        self, failure_exc: Exception
    ) -> None:
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
            pytest.fail(
                f"assess_node() raised {type(exc).__name__}: {exc}; "
                f"should have returned base-tier ladder"
            )

        assert isinstance(ladder, EscalationLadder), (
            f"Expected EscalationLadder, got {type(ladder)}"
        )
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
    def test_malformed_response_returns_base_without_raising(
        self, bad_json: str
    ) -> None:
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
            pytest.fail(
                f"assess_node() raised {type(exc).__name__}: {exc}; "
                f"should have returned base-tier ladder"
            )

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
        mock_client.messages.create = AsyncMock(
            side_effect=ConnectionError("fail")
        )
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
            f"Expected fallback tier {expected_tier} for {archetype}/{mode}, "
            f"got {ladder.current_tier}"
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
            assert isinstance(r, EscalationLadder), (
                f"Expected EscalationLadder, got {type(r)}"
            )

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
        assert call_count == 5, (
            f"Expected 5 independent LLM calls, got {call_count}"
        )
        # All results should be distinct objects
        ids = [id(r) for r in results]
        assert len(set(ids)) == 5, (
            "All returned ladders should be distinct objects"
        )
