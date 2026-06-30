"""Complexity assessment for dynamic model selection.

Provides a lightweight LLM-driven complexity signal that can upgrade model
tier selection when task complexity warrants it.

Requirements: 15-REQ-1.1, 15-REQ-1.2, 15-REQ-1.3, 15-REQ-1.4,
              15-REQ-1.5, 15-REQ-2.1, 15-REQ-2.2, 15-REQ-2.3, 15-REQ-2.4,
              15-REQ-2.E1, 15-REQ-2.E2, 15-REQ-2.E3,
              15-REQ-3.1, 15-REQ-3.2, 15-REQ-3.3, 15-REQ-3.4, 15-REQ-3.5,
              15-REQ-3.6, 15-REQ-11.2, 15-REQ-12.1, 15-REQ-12.E1
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import Protocol, runtime_checkable

from agentfox.core.json_extraction import extract_json_object
from agentfox.core.models import VARIANT_ORDER, ModelTier

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Valid values for case-sensitive validation
# ---------------------------------------------------------------------------

_VALID_TIERS: frozenset[str] = frozenset(t.value for t in ModelTier)
_VALID_VARIANTS: frozenset[str | None] = frozenset(VARIANT_ORDER.keys())

# ---------------------------------------------------------------------------
# Protocol and dataclass definitions (15-REQ-1.1, 15-REQ-1.2)
# ---------------------------------------------------------------------------


@runtime_checkable
class ComplexityRecommendation(Protocol):
    """Protocol for complexity recommendations accepted by apply_assessment().

    Satisfied by both AssessmentResult (from ComplexityAssessor) and
    AssessedComplexity (from nightshift triage, via adapter).

    Requirement: 15-REQ-1.1
    """

    recommended_tier: str
    recommended_variant: str | None
    confidence: float


@dataclass(frozen=True)
class AssessmentResult:
    """Structured result from complexity assessment.

    Requirement: 15-REQ-1.2
    """

    recommended_tier: str
    recommended_variant: str | None
    confidence: float
    rationale: str


# ---------------------------------------------------------------------------
# ComplexityAssessor (15-REQ-1.3, 15-REQ-1.4, 15-REQ-1.5)
# ---------------------------------------------------------------------------


class ComplexityAssessor:
    """Calls a lightweight LLM to assess task complexity.

    Accepts an existing Anthropic client instance, configurable assessor_model
    (default: 'claude-haiku-4-5'), and confidence_threshold (default: 0.6).

    Maintains no shared mutable state; concurrent assess() calls are safe
    without additional locking.

    Requirements: 15-REQ-1.3, 15-REQ-1.4, 15-REQ-1.5
    """

    def __init__(
        self,
        client: object,
        assessor_model: str = "claude-haiku-4-5",
        confidence_threshold: float = 0.6,
    ) -> None:
        self.client = client
        self.assessor_model = assessor_model
        self.confidence_threshold = confidence_threshold

    async def assess(
        self,
        node_body: str,
        archetype: str,
        mode: str | None,
        base_tier: str,
        base_variant: str | None,
        previous_failure: str | None = None,
    ) -> AssessmentResult:
        """Assess task complexity and return a structured recommendation.

        On any failure (network, timeout, rate limit, parse error), logs a
        WARNING and returns a fallback AssessmentResult at base_tier/base_variant
        without raising.

        Requirements: 15-REQ-1.4, 15-REQ-2.1, 15-REQ-2.2, 15-REQ-2.3,
                      15-REQ-2.4, 15-REQ-2.E1, 15-REQ-2.E2, 15-REQ-2.E3,
                      15-REQ-12.1, 15-REQ-12.E1
        """
        # Build the assessment prompt (15-REQ-2.1)
        mode_label = mode if mode is not None else "default"
        system_prompt = (
            "You are a complexity assessor. Your role is to evaluate the "
            "complexity of a coding task and recommend the most appropriate "
            "AI model tier and variant."
        )

        user_message = (
            f"## Agent Role\n"
            f"Archetype: {archetype}\n"
            f"Mode: {mode_label}\n\n"
            f"## Current Base Configuration\n"
            f"Base tier: {base_tier}\n"
            f"Base variant: {base_variant}\n\n"
            f"## Task Body\n"
            f"{node_body}\n\n"
        )

        # Append previous failure context if provided (15-REQ-2.2)
        if previous_failure is not None:
            user_message += (
                f"## Previous Attempt Failure\n"
                f"The previous attempt failed with the following error:\n"
                f"{previous_failure}\n\n"
            )

        user_message += (
            "## Output Instruction\n"
            "Respond with a JSON object matching this schema:\n"
            "{\n"
            '  "recommended_tier": "SIMPLE" | "STANDARD" | "ADVANCED",\n'
            '  "recommended_variant": null | "fast" | "standard" | "extended",\n'
            '  "confidence": <float between 0.0 and 1.0>,\n'
            '  "rationale": "<brief explanation>"\n'
            "}\n"
            "Use EXACT case as shown. Respond with JSON only, no other text."
        )

        messages = [{"role": "user", "content": user_message}]

        response_text = None  # initialised before try so the except handler can always access it
        try:
            # Call the Anthropic API with 30-second timeout (15-REQ-2.4)
            response = await self.client.messages.create(
                model=self.assessor_model,
                max_tokens=1024,
                system=system_prompt,
                messages=messages,
                timeout=30,
            )

            # Extract response text (15-REQ-2.3)
            response_text = response.content[0].text
            if not response_text or not response_text.strip():
                raise ValueError("LLM returned empty response text")

            # Parse and validate JSON response
            data = extract_json_object(response_text)

            # Validate all four required fields are present
            for field_name in ("recommended_tier", "recommended_variant", "confidence", "rationale"):
                if field_name not in data:
                    raise ValueError(f"Missing required field: {field_name!r}")

            rec_tier: str = data["recommended_tier"]
            rec_variant: str | None = data["recommended_variant"]
            confidence: float = data["confidence"]
            rationale: str = data["rationale"]

            # Validate confidence is a float in [0.0, 1.0]
            if not isinstance(confidence, (int, float)) or not (0.0 <= confidence <= 1.0):
                raise ValueError(f"confidence must be a float in [0.0, 1.0], got {confidence!r}")
            confidence = float(confidence)

            # Validate recommended_tier is a recognised ModelTier value (case-sensitive)
            if rec_tier not in _VALID_TIERS:
                raise ValueError(f"recommended_tier must be one of {sorted(_VALID_TIERS)!r}, got {rec_tier!r}")

            # Validate recommended_variant (case-sensitive)
            if rec_variant is not None and rec_variant not in _VALID_VARIANTS:
                raise ValueError(
                    f"recommended_variant must be null, 'fast', 'standard', or 'extended', got {rec_variant!r}"
                )

            return AssessmentResult(
                recommended_tier=rec_tier,
                recommended_variant=rec_variant,
                confidence=confidence,
                rationale=rationale,
            )

        except Exception as exc:
            # Any failure: log WARNING with exception details and fall back silently
            # (15-REQ-2.E1, 15-REQ-2.E2, 15-REQ-2.E3, 15-REQ-12.1, 15-REQ-12.E1)
            response_repr = repr(response_text) if response_text is not None else "<unavailable>"
            logger.warning(
                "Complexity assessment failed (%s: %s); falling back to base tier=%s variant=%s; response_text=%s",
                type(exc).__name__,
                exc,
                base_tier,
                base_variant,
                response_repr,
                exc_info=True,
            )
            return AssessmentResult(
                recommended_tier=base_tier,
                recommended_variant=base_variant,
                confidence=0.0,
                rationale="fallback",
            )


# ---------------------------------------------------------------------------
# apply_assessment (15-REQ-3.1 through 15-REQ-3.6, 15-REQ-3.E1)
# ---------------------------------------------------------------------------

# Tier ordering for upgrade comparisons
_TIER_ORDER: dict[str, int] = {
    ModelTier.SIMPLE.value: 0,
    ModelTier.STANDARD.value: 1,
    ModelTier.ADVANCED.value: 2,
}


def apply_assessment(
    recommendation: ComplexityRecommendation,
    base_tier: str,
    base_variant: str | None,
    confidence_threshold: float,
) -> tuple[str, str | None]:
    """Apply upgrade-only semantics to a complexity recommendation.

    Returns (effective_tier, effective_variant) where neither is below
    the corresponding base value.

    Rules:
    - If confidence < threshold, return (base_tier, base_variant) unchanged.
    - effective_tier = max(base_tier, recommended_tier) in ModelTier ordering.
    - If base_variant is None, effective_variant is always None.
    - If recommended_variant is None, effective_variant = base_variant.
    - Otherwise, effective_variant = max(base_variant, recommended_variant).

    Requirements: 15-REQ-3.1, 15-REQ-3.2, 15-REQ-3.3, 15-REQ-3.4,
                  15-REQ-3.5, 15-REQ-3.6, 15-REQ-3.E1
    """
    # Confidence gate (15-REQ-3.2, 15-PROP-4)
    if recommendation.confidence < confidence_threshold:
        return (base_tier, base_variant)

    # Tier upgrade: max(base_tier, recommended_tier) (15-REQ-3.3, 15-PROP-1)
    base_tier_order = _TIER_ORDER.get(base_tier, 0)
    rec_tier_order = _TIER_ORDER.get(recommendation.recommended_tier, 0)
    effective_tier = base_tier if base_tier_order >= rec_tier_order else recommendation.recommended_tier

    # Variant upgrade (15-REQ-3.4, 15-REQ-3.5, 15-REQ-3.6, 15-PROP-2, 15-PROP-3)
    if base_variant is None:
        # 15-REQ-3.4: base_variant is None → always return None
        effective_variant: str | None = None
    elif recommendation.recommended_variant is None:
        # 15-REQ-3.5: no preference from assessor → keep base_variant
        effective_variant = base_variant
    else:
        # 15-REQ-3.6: both non-None → max in VARIANT_ORDER
        base_var_order = VARIANT_ORDER.get(base_variant, 0)
        rec_var_order = VARIANT_ORDER.get(recommendation.recommended_variant, 0)
        effective_variant = base_variant if base_var_order >= rec_var_order else recommendation.recommended_variant

    return (effective_tier, effective_variant)


# ---------------------------------------------------------------------------
# assessed_complexity_to_recommendation (15-REQ-11.2)
# ---------------------------------------------------------------------------


def assessed_complexity_to_recommendation(
    assessed_complexity: object,
) -> AssessmentResult:
    """Convert an AssessedComplexity to an AssessmentResult.

    Maps: tier → recommended_tier, variant → recommended_variant,
    confidence → confidence, rationale → rationale.

    Requirement: 15-REQ-11.2
    """
    return AssessmentResult(
        recommended_tier=assessed_complexity.tier,  # type: ignore[attr-defined]
        recommended_variant=assessed_complexity.variant,  # type: ignore[attr-defined]
        confidence=assessed_complexity.confidence,  # type: ignore[attr-defined]
        rationale=assessed_complexity.rationale,  # type: ignore[attr-defined]
    )
