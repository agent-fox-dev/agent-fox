"""ArchetypeEntry and ModeConfig variant field tests, plus resolve_effective_config variant merging.

Test Spec: TS-14-11, TS-14-12, TS-14-13, TS-14-14, TS-14-45, TS-14-46, TS-14-47, TS-14-E2
Requirements: 14-REQ-4.1, 14-REQ-4.2, 14-REQ-4.3, 14-REQ-4.4, 14-REQ-4.E1,
              14-REQ-13.1, 14-REQ-13.2, 14-REQ-13.3
"""

from __future__ import annotations

from agentfox.archetypes import ArchetypeEntry, ModeConfig, resolve_effective_config
from agentfox.core.models import TIER_DEFAULTS

# ---------------------------------------------------------------------------
# TS-14-11: ArchetypeEntry exposes default_model_variant defaulting to None
# Requirement: 14-REQ-4.1
# ---------------------------------------------------------------------------


class TestArchetypeEntryDefaultModelVariant:
    """Verify that ArchetypeEntry exposes default_model_variant as an optional field defaulting to None."""

    def test_default_model_variant_defaults_to_none(self) -> None:
        """TS-14-11: ArchetypeEntry constructed without default_model_variant has it as None."""
        entry = ArchetypeEntry(name="test-archetype")
        assert entry.default_model_variant is None

    def test_default_model_variant_accepts_string(self) -> None:
        """TS-14-11 corollary: ArchetypeEntry accepts a string for default_model_variant."""
        entry = ArchetypeEntry(name="test-archetype", default_model_variant="extended")
        assert entry.default_model_variant == "extended"


# ---------------------------------------------------------------------------
# TS-14-12: ModeConfig exposes model_variant defaulting to None
# Requirement: 14-REQ-4.2
# ---------------------------------------------------------------------------


class TestModeConfigModelVariant:
    """Verify that ModeConfig exposes model_variant as an optional field defaulting to None."""

    def test_model_variant_defaults_to_none(self) -> None:
        """TS-14-12: ModeConfig constructed without model_variant has it as None."""
        mode = ModeConfig()
        assert mode.model_variant is None

    def test_model_variant_accepts_string(self) -> None:
        """TS-14-12 corollary: ModeConfig accepts a string for model_variant."""
        mode = ModeConfig(model_variant="extended")
        assert mode.model_variant == "extended"


# ---------------------------------------------------------------------------
# TS-14-E2: resolve_model uses TIER_DEFAULTS when both mode and archetype
#           variants are None (backward-compatible path)
# Requirement: 14-REQ-4.E1
# ---------------------------------------------------------------------------


class TestVariantNoneFallback:
    """Verify resolve_model uses TIER_DEFAULTS when both mode and archetype variant are None."""

    def test_resolve_model_advanced_with_variant_none(self) -> None:
        """TS-14-E2: resolve_model('ADVANCED', variant=None) returns TIER_DEFAULTS['ADVANCED']."""
        from agentfox.core.models import resolve_model

        result = resolve_model("ADVANCED", variant=None)
        assert result == TIER_DEFAULTS["ADVANCED"]
        assert result == "claude-opus-4-6"


# ---------------------------------------------------------------------------
# TS-14-13 / TS-14-45: resolve_effective_config returns mode-level model_variant
#                       when both mode and archetype specify non-None variants
# Requirement: 14-REQ-4.3, 14-REQ-13.1
# ---------------------------------------------------------------------------


class TestResolveEffectiveConfigModeBeatsArchetypeVariant:
    """Verify mode-level model_variant takes precedence over archetype-level default_model_variant."""

    def test_mode_variant_overrides_archetype_variant(self) -> None:
        """TS-14-13 / TS-14-45: Mode variant='extended' overrides archetype variant='standard'."""
        entry = ArchetypeEntry(
            name="test-archetype",
            default_model_variant="standard",
            modes={
                "test-mode": ModeConfig(model_variant="extended"),
            },
        )
        result = resolve_effective_config(entry, "test-mode")
        assert result.default_model_variant == "extended"


# ---------------------------------------------------------------------------
# TS-14-14 / TS-14-46: resolve_effective_config preserves archetype-level
#                       default_model_variant when mode has model_variant=None
# Requirement: 14-REQ-4.4, 14-REQ-13.2
# ---------------------------------------------------------------------------


class TestResolveEffectiveConfigPreservesArchetypeVariant:
    """Verify archetype-level default_model_variant is preserved when mode's model_variant is None."""

    def test_archetype_variant_preserved_when_mode_is_none(self) -> None:
        """TS-14-14 / TS-14-46: Archetype variant='extended' preserved when mode variant=None."""
        entry = ArchetypeEntry(
            name="test-archetype",
            default_model_variant="extended",
            modes={
                "test-mode": ModeConfig(model_variant=None),
            },
        )
        result = resolve_effective_config(entry, "test-mode")
        assert result.default_model_variant == "extended"


# ---------------------------------------------------------------------------
# TS-14-47: resolve_effective_config variant merging mirrors model_tier merging
# Requirement: 14-REQ-13.3
# ---------------------------------------------------------------------------


class TestResolveEffectiveConfigVariantTierSymmetry:
    """Verify model_variant merging mirrors model_tier merging (symmetric priority)."""

    def test_mode_overrides_both_tier_and_variant(self) -> None:
        """TS-14-47: Both default_model_tier and default_model_variant reflect mode-level values."""
        entry = ArchetypeEntry(
            name="test-archetype",
            default_model_tier="STANDARD",
            default_model_variant="standard",
            modes={
                "test-mode": ModeConfig(
                    model_tier="ADVANCED",
                    model_variant="extended",
                ),
            },
        )
        result = resolve_effective_config(entry, "test-mode")
        assert result.default_model_tier == "ADVANCED"
        assert result.default_model_variant == "extended"

    def test_mode_none_preserves_both_tier_and_variant(self) -> None:
        """TS-14-47 corollary: None mode values preserve both archetype tier and variant."""
        entry = ArchetypeEntry(
            name="test-archetype",
            default_model_tier="ADVANCED",
            default_model_variant="extended",
            modes={
                "test-mode": ModeConfig(
                    model_tier=None,
                    model_variant=None,
                ),
            },
        )
        result = resolve_effective_config(entry, "test-mode")
        assert result.default_model_tier == "ADVANCED"
        assert result.default_model_variant == "extended"

    def test_mode_overrides_variant_preserves_tier(self) -> None:
        """TS-14-47 corollary: Mode can override variant while preserving tier."""
        entry = ArchetypeEntry(
            name="test-archetype",
            default_model_tier="ADVANCED",
            default_model_variant="standard",
            modes={
                "test-mode": ModeConfig(
                    model_tier=None,
                    model_variant="extended",
                ),
            },
        )
        result = resolve_effective_config(entry, "test-mode")
        assert result.default_model_tier == "ADVANCED"
        assert result.default_model_variant == "extended"
