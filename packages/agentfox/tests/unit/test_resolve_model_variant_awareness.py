"""resolve_model() variant awareness and fallback tests.

Test Spec: TS-14-24, TS-14-25, TS-14-26, TS-14-27, TS-14-34, TS-14-35,
           TS-14-36, TS-14-E4, TS-14-E5, TS-14-E7, TS-14-P1, TS-14-P2, TS-14-P3
Requirements: 14-REQ-7.1, 14-REQ-7.2, 14-REQ-7.3, 14-REQ-7.4,
              14-REQ-9.1, 14-REQ-9.2, 14-REQ-9.3,
              14-REQ-7.E1, 14-REQ-7.E2, 14-REQ-9.E1
"""

from __future__ import annotations

import logging

import pytest
from agentfox.core.models import MODEL_REGISTRY, TIER_DEFAULTS, resolve_model

# ---------------------------------------------------------------------------
# TS-14-24: resolve_model with variant=None returns TIER_DEFAULTS model ID
# Requirement: 14-REQ-7.1
# ---------------------------------------------------------------------------


class TestResolveModelVariantNone:
    """Verify that resolve_model with variant=None returns the TIER_DEFAULTS model ID."""

    @pytest.mark.parametrize("tier", ["SIMPLE", "STANDARD", "ADVANCED"])
    def test_variant_none_returns_tier_default(self, tier: str) -> None:
        """TS-14-24: resolve_model(tier, variant=None) == TIER_DEFAULTS[tier]."""
        result = resolve_model(tier, variant=None)
        assert result == TIER_DEFAULTS[tier]


# ---------------------------------------------------------------------------
# TS-14-25: resolve_model with matching (tier, variant) returns correct model_id
# Requirement: 14-REQ-7.2
# ---------------------------------------------------------------------------


class TestResolveModelVariantMatch:
    """Verify that resolve_model returns the matching model_id for a known (tier, variant)."""

    def test_advanced_extended_returns_opus_1m(self) -> None:
        """TS-14-25: resolve_model('ADVANCED', variant='extended') == 'claude-opus-4-6[1m]'."""
        result = resolve_model("ADVANCED", variant="extended")
        assert result == "claude-opus-4-6[1m]"


# ---------------------------------------------------------------------------
# TS-14-27 / TS-14-P1: TIER_DEFAULTS ADVANCED invariant
# Requirement: 14-REQ-7.4
# ---------------------------------------------------------------------------


class TestTierDefaultsAdvancedInvariant:
    """Verify TIER_DEFAULTS['ADVANCED'] equals the MODEL_REGISTRY standard variant entry."""

    def test_advanced_default_matches_standard_variant(self) -> None:
        """TS-14-27 / TS-14-P1: TIER_DEFAULTS['ADVANCED'] == MODEL_REGISTRY entry
        with tier=ADVANCED and variant='standard'.
        """
        standard_advanced = next(e for e in MODEL_REGISTRY.values() if e.tier == "ADVANCED" and e.variant == "standard")
        assert TIER_DEFAULTS["ADVANCED"] == standard_advanced.model_id


# ---------------------------------------------------------------------------
# TS-14-P2: Backward compatibility — variant=None returns TIER_DEFAULTS for all tiers
# Requirement: 14-REQ-11.1, 14-REQ-7.1
# ---------------------------------------------------------------------------


class TestVariantNoneBackwardCompat:
    """Property: resolve_model(tier, variant=None) == TIER_DEFAULTS[tier] for all tiers."""

    def test_all_tiers_variant_none_equals_tier_defaults(self) -> None:
        """TS-14-P2: For every tier, resolve_model(tier, variant=None) matches TIER_DEFAULTS."""
        for tier in ["SIMPLE", "STANDARD", "ADVANCED"]:
            assert resolve_model(tier, variant=None) == TIER_DEFAULTS[tier]


# ---------------------------------------------------------------------------
# TS-14-26 / TS-14-34 / TS-14-E4: Variant fallback for SIMPLE + extended
# Requirement: 14-REQ-7.3, 14-REQ-9.1, 14-REQ-7.E1
# ---------------------------------------------------------------------------


class TestResolveModelFallbackSimpleExtended:
    """Verify resolve_model falls back to TIER_DEFAULTS and emits DEBUG log."""

    def test_simple_extended_falls_back_to_haiku(self, caplog: pytest.LogCaptureFixture) -> None:
        """TS-14-26 / TS-14-34 / TS-14-E4: resolve_model('SIMPLE', variant='extended')
        returns 'claude-haiku-4-5' and emits a DEBUG log.
        """
        with caplog.at_level(logging.DEBUG, logger="agentfox.core.models"):
            result = resolve_model("SIMPLE", variant="extended")

        assert result == TIER_DEFAULTS["SIMPLE"]
        assert result == "claude-haiku-4-5"
        assert any(record.levelno == logging.DEBUG for record in caplog.records), (
            "Expected at least one DEBUG log to be emitted"
        )


# ---------------------------------------------------------------------------
# TS-14-35 / TS-14-E5: Unrecognized variant string (turbo) no exception
# Requirement: 14-REQ-9.2, 14-REQ-7.E2
# ---------------------------------------------------------------------------


class TestResolveModelUnrecognizedVariant:
    """Verify resolve_model never raises for an unrecognized variant string."""

    def test_turbo_variant_no_exception(self, caplog: pytest.LogCaptureFixture) -> None:
        """TS-14-35 / TS-14-E5: resolve_model('ADVANCED', variant='turbo')
        returns a non-empty string and emits a DEBUG log; no exception raised.
        """
        with caplog.at_level(logging.DEBUG, logger="agentfox.core.models"):
            result = resolve_model("ADVANCED", variant="turbo")

        assert isinstance(result, str) and len(result) > 0
        assert result == TIER_DEFAULTS["ADVANCED"]
        assert any(record.levelno == logging.DEBUG for record in caplog.records), "Expected a DEBUG-level fallback log"


# ---------------------------------------------------------------------------
# TS-14-36: Variant fallback log is DEBUG only — no WARNING or ERROR
# Requirement: 14-REQ-9.3
# ---------------------------------------------------------------------------


class TestResolveModelFallbackLogLevel:
    """Verify the fallback log is emitted at DEBUG only, not WARNING or ERROR."""

    def test_fallback_log_is_debug_only(self, caplog: pytest.LogCaptureFixture) -> None:
        """TS-14-36: Fallback log for unmatched variant is DEBUG;
        no WARNING or ERROR emitted.
        """
        with caplog.at_level(logging.DEBUG, logger="agentfox.core.models"):
            resolve_model("SIMPLE", variant="extended")

        # Any log referencing variant/fallback must be DEBUG level.
        fallback_logs = [r for r in caplog.records if "variant" in r.message.lower() or "fallback" in r.message.lower()]
        assert all(r.levelno == logging.DEBUG for r in fallback_logs), (
            "All variant/fallback log messages must be DEBUG level"
        )

        # No WARNING or ERROR logs from the models module.
        assert not any(r.levelno in (logging.WARNING, logging.ERROR) for r in caplog.records), (
            "No WARNING or ERROR logs should be emitted for variant fallback"
        )


# ---------------------------------------------------------------------------
# TS-14-E7: Valid canonical variant unavailable for SIMPLE tier
# Requirement: 14-REQ-9.E1
# ---------------------------------------------------------------------------


class TestResolveModelCanonicalVariantAvailable:
    """Verify that the 'standard' variant resolves directly for all tiers."""

    def test_simple_standard_variant_resolves_directly(self, caplog: pytest.LogCaptureFixture) -> None:
        """TS-14-E7 (updated): resolve_model('SIMPLE', variant='standard')
        resolves directly to claude-haiku-4-5 without a fallback log.
        """
        with caplog.at_level(logging.DEBUG, logger="agentfox.core.models"):
            result = resolve_model("SIMPLE", variant="standard")

        assert result == "claude-haiku-4-5"
        fallback_logs = [
            r for r in caplog.records
            if "fallback" in r.message.lower() or "falling back" in r.message.lower()
        ]
        assert not fallback_logs, "No fallback log expected for valid (SIMPLE, standard) match"


# ---------------------------------------------------------------------------
# TS-14-P3: No exception for any tier × variant combination
# Requirement: 14-REQ-9.2
# ---------------------------------------------------------------------------


class TestResolveModelNoExceptionProperty:
    """Property: resolve_model never raises for any tier and variant string."""

    @pytest.mark.parametrize("tier", ["SIMPLE", "STANDARD", "ADVANCED"])
    @pytest.mark.parametrize(
        "variant",
        ["fast", "standard", "extended", "turbo", "UNKNOWN_VARIANT", "x" * 100],
    )
    def test_no_exception_for_any_variant(self, tier: str, variant: str) -> None:
        """TS-14-P3: resolve_model(tier, variant=variant) never raises
        and always returns a non-empty string.
        """
        result = resolve_model(tier, variant=variant)
        assert isinstance(result, str) and len(result) > 0
