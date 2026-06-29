"""Complexity assessment for dynamic model selection.

Provides a lightweight LLM-driven complexity signal that can upgrade model
tier selection when task complexity warrants it.

Stub module — behavioural implementation in task groups 8+.

Requirements: 15-REQ-1.1, 15-REQ-1.2, 15-REQ-1.3, 15-REQ-1.4,
              15-REQ-3.1, 15-REQ-11.2
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Protocol, runtime_checkable

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
# ComplexityAssessor (15-REQ-1.3, 15-REQ-1.4)
# ---------------------------------------------------------------------------


class ComplexityAssessor:
    """Calls a lightweight LLM to assess task complexity.

    Accepts an existing Anthropic client instance, configurable assessor_model
    (default: 'claude-haiku-4-5'), and confidence_threshold (default: 0.6).

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

        Stub — raises NotImplementedError until task group 8.

        Requirement: 15-REQ-1.4
        """
        raise NotImplementedError("ComplexityAssessor.assess() not yet implemented")


# ---------------------------------------------------------------------------
# apply_assessment (15-REQ-3.1)
# ---------------------------------------------------------------------------


def apply_assessment(
    recommendation: ComplexityRecommendation,
    base_tier: str,
    base_variant: str | None,
    confidence_threshold: float,
) -> tuple[str, str | None]:
    """Apply upgrade-only semantics to a complexity recommendation.

    Returns (effective_tier, effective_variant) where neither is below
    the corresponding base value.

    Stub — raises NotImplementedError until task group 8.

    Requirement: 15-REQ-3.1
    """
    raise NotImplementedError("apply_assessment() not yet implemented")


# ---------------------------------------------------------------------------
# assessed_complexity_to_recommendation (15-REQ-11.2)
# ---------------------------------------------------------------------------


def assessed_complexity_to_recommendation(
    assessed_complexity: object,
) -> AssessmentResult:
    """Convert an AssessedComplexity to an AssessmentResult.

    Stub — raises NotImplementedError until task group 8.

    Requirement: 15-REQ-11.2
    """
    raise NotImplementedError(
        "assessed_complexity_to_recommendation() not yet implemented"
    )
