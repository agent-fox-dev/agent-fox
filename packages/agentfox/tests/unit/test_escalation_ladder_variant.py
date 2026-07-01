"""EscalationLadder variant preservation tests.

Test Spec: TS-14-28, TS-14-29, TS-14-30, TS-14-31, TS-14-32, TS-14-33,
           TS-14-E6, TS-14-P4
Requirements: 14-REQ-8.1, 14-REQ-8.2, 14-REQ-8.3, 14-REQ-8.4,
              14-REQ-8.5, 14-REQ-8.6, 14-REQ-8.E1
"""

from __future__ import annotations

import inspect
import logging
from unittest.mock import patch

import pytest
from agentfox.core.escalation import EscalationLadder
from agentfox.core.models import ModelTier

# ---------------------------------------------------------------------------
# TS-14-28: EscalationLadder accepts starting_variant as keyword-only parameter
# Requirement: 14-REQ-8.1
# ---------------------------------------------------------------------------


class TestStartingVariantKeywordOnly:
    """Verify starting_variant is a keyword-only parameter in __init__."""

    def test_starting_variant_is_keyword_only(self) -> None:
        """TS-14-28: starting_variant parameter kind is KEYWORD_ONLY."""
        sig = inspect.signature(EscalationLadder.__init__)
        param = sig.parameters["starting_variant"]
        assert param.kind == inspect.Parameter.KEYWORD_ONLY

    def test_constructs_with_starting_variant(self) -> None:
        """TS-14-28: EscalationLadder constructs without error with starting_variant."""
        ladder = EscalationLadder(
            ModelTier.STANDARD,
            ModelTier.ADVANCED,
            retries_before_escalation=1,
            starting_variant="extended",
        )
        assert ladder is not None


# ---------------------------------------------------------------------------
# TS-14-29: current_variant returns starting_variant value
# Requirement: 14-REQ-8.2
# ---------------------------------------------------------------------------


class TestCurrentVariantReturnsStartingVariant:
    """Verify current_variant is a read-only property returning starting_variant."""

    def test_current_variant_returns_extended(self) -> None:
        """TS-14-29: current_variant property returns 'extended'."""
        ladder = EscalationLadder(
            ModelTier.STANDARD,
            ModelTier.ADVANCED,
            retries_before_escalation=1,
            starting_variant="extended",
        )
        assert ladder.current_variant == "extended"


# ---------------------------------------------------------------------------
# TS-14-30: current_variant has no setter — AttributeError on assignment
# Requirement: 14-REQ-8.3
# ---------------------------------------------------------------------------


class TestCurrentVariantReadOnly:
    """Verify current_variant raises AttributeError on assignment."""

    def test_current_variant_no_setter(self) -> None:
        """TS-14-30: Assigning to current_variant raises AttributeError."""
        ladder = EscalationLadder(
            ModelTier.STANDARD,
            ModelTier.ADVANCED,
            retries_before_escalation=1,
            starting_variant="extended",
        )
        with pytest.raises(AttributeError):
            ladder.current_variant = "standard"  # type: ignore[misc]


# ---------------------------------------------------------------------------
# TS-14-33: Existing call sites work without starting_variant
# Requirement: 14-REQ-8.6
# ---------------------------------------------------------------------------


class TestBackwardCompatNoStartingVariant:
    """Verify existing positional arg patterns work without starting_variant."""

    def test_no_starting_variant_defaults_to_none(self) -> None:
        """TS-14-33: Without starting_variant, current_variant is None."""
        ladder = EscalationLadder(
            ModelTier.SIMPLE,
            ModelTier.ADVANCED,
            retries_before_escalation=1,
        )
        assert ladder.current_variant is None


# ---------------------------------------------------------------------------
# TS-14-P4: current_variant immutability across escalations
# Requirement: 14-REQ-8.2, 14-REQ-8.3
# ---------------------------------------------------------------------------


class TestCurrentVariantImmutabilityProperty:
    """Property: current_variant is unchanged after any number of escalations."""

    @pytest.mark.parametrize(
        "starting_variant",
        [None, "fast", "standard", "extended"],
    )
    def test_variant_unchanged_across_escalations(self, starting_variant: str | None) -> None:
        """TS-14-P4: current_variant remains the same before and after escalations.

        Using retries_before_escalation=0 so each failure triggers escalation.
        SIMPLE -> STANDARD -> ADVANCED (2 escalations), then exhaustion.
        """
        ladder = EscalationLadder(
            ModelTier.SIMPLE,
            ModelTier.ADVANCED,
            retries_before_escalation=0,
            starting_variant=starting_variant,
        )
        initial = ladder.current_variant

        # Record up to 3 failures (2 escalations + 1 exhaustion)
        for _ in range(3):
            if ladder.should_retry():
                ladder.record_failure()
            assert ladder.current_variant == initial, (
                f"current_variant changed from {initial!r} to {ladder.current_variant!r}"
            )


# ---------------------------------------------------------------------------
# TS-14-31: EscalationLadder passes variant to resolve_model on escalation
# Requirement: 14-REQ-8.4
# ---------------------------------------------------------------------------


class TestVariantPassedToResolveModelOnEscalation:
    """Verify variant='extended' is passed to resolve_model after escalation."""

    def test_variant_passed_after_standard_to_advanced(self) -> None:
        """TS-14-31: After escalation STANDARD->ADVANCED, resolve_model
        is called with variant='extended'.

        retries_before_escalation=0: first record_failure() escalates
        STANDARD -> ADVANCED.
        """
        ladder = EscalationLadder(
            ModelTier.STANDARD,
            ModelTier.ADVANCED,
            retries_before_escalation=0,
            starting_variant="extended",
        )
        # Escalate from STANDARD to ADVANCED
        ladder.record_failure()
        assert ladder.current_tier == ModelTier.ADVANCED

        with patch("agentfox.core.escalation.resolve_model") as mocked_rm:
            mocked_rm.return_value = "claude-opus-4-6[1m]"
            ladder.resolve_current_model()
            mocked_rm.assert_called_once()
            # Verify variant='extended' was passed as keyword argument
            assert mocked_rm.call_args.kwargs.get("variant") == "extended"


# ---------------------------------------------------------------------------
# TS-14-32: EscalationLadder passes variant unchanged when unavailable for tier
# Requirement: 14-REQ-8.5
# ---------------------------------------------------------------------------


class TestVariantPassedUnchangedWhenUnavailable:
    """Verify variant is passed unchanged even when unavailable for the tier."""

    def test_variant_passed_unchanged_at_simple(self) -> None:
        """TS-14-32: At SIMPLE with starting_variant='extended', resolve_model
        receives variant='extended' — EscalationLadder does not substitute.
        """
        ladder = EscalationLadder(
            ModelTier.SIMPLE,
            ModelTier.ADVANCED,
            retries_before_escalation=1,
            starting_variant="extended",
        )
        with patch("agentfox.core.escalation.resolve_model") as mocked_rm:
            mocked_rm.return_value = "claude-haiku-4-5"
            ladder.resolve_current_model()
            mocked_rm.assert_called_once()
            # Variant is passed through unchanged — no EscalationLadder fallback
            assert mocked_rm.call_args.kwargs.get("variant") == "extended"


# ---------------------------------------------------------------------------
# TS-14-E6: Fallback DEBUG log emitted by resolve_model, not EscalationLadder
# Requirement: 14-REQ-8.E1
# ---------------------------------------------------------------------------


class TestFallbackLogSourceIsResolveModel:
    """Verify fallback DEBUG log comes from resolve_model, not EscalationLadder."""

    def test_fallback_log_from_models_not_escalation(self, caplog: pytest.LogCaptureFixture) -> None:
        """TS-14-E6: When variant='fast' is unavailable for ADVANCED,
        the DEBUG log is emitted by the resolve_model module path
        (agentfox.core.models), not the escalation module.
        """
        ladder = EscalationLadder(
            ModelTier.ADVANCED,
            ModelTier.ADVANCED,
            retries_before_escalation=1,
            starting_variant="fast",
        )
        with caplog.at_level(logging.DEBUG):
            result = ladder.resolve_current_model()

        # resolve_model should return a valid model ID string
        assert isinstance(result, str) and len(result) > 0

        # All DEBUG logs about variant/fallback must come from the models
        # module, not the escalation module.
        debug_logs = [r for r in caplog.records if r.levelno == logging.DEBUG]
        escalation_debug_logs = [r for r in debug_logs if "escalation" in r.name]
        assert not escalation_debug_logs, (
            "No DEBUG logs should be emitted by the escalation module "
            "for variant fallback — resolve_model handles fallback logging"
        )
