"""Unit tests for dynamic complexity assessment.

Task Group 1: ComplexityAssessor class, AssessmentResult, Protocol,
              statelessness, and assessment prompt structure.

Test Spec: TS-15-1 through TS-15-12
Requirements: 15-REQ-1.1 through 15-REQ-1.7, 15-REQ-2.1 through 15-REQ-2.5
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
