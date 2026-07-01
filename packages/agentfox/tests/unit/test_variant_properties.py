"""Remaining property tests for model variant support.

Test Spec: TS-14-P5, TS-14-P7
Requirements: 14-REQ-4.3, 14-REQ-13.1,
              14-REQ-3.1, 14-REQ-3.3
"""

from __future__ import annotations

import pytest

# ---------------------------------------------------------------------------
# TS-14-P5: For any ModeConfig with non-None model_variant and any
#           ArchetypeEntry with non-None default_model_variant,
#           resolve_effective_config returns merged result where
#           default_model_variant equals the mode's model_variant
# Requirement: 14-REQ-4.3, 14-REQ-13.1
# ---------------------------------------------------------------------------


class TestModeBeatsArchetypeVariantProperty:
    """Property: mode variant always takes precedence over archetype variant."""

    @pytest.mark.parametrize("mode_variant", ["fast", "standard", "extended"])
    @pytest.mark.parametrize("arch_variant", ["fast", "standard", "extended"])
    def test_mode_variant_wins_over_archetype_variant(self, mode_variant: str, arch_variant: str) -> None:
        """TS-14-P5: For all (mode, archetype) variant combos, merged result
        has default_model_variant == mode_variant.
        """
        from agentfox.archetypes import ArchetypeEntry, ModeConfig, resolve_effective_config

        entry = ArchetypeEntry(
            name="test-archetype",
            default_model_variant=arch_variant,
            modes={
                "test-mode": ModeConfig(model_variant=mode_variant),
            },
        )
        result = resolve_effective_config(entry, "test-mode")
        assert result.default_model_variant == mode_variant


# ---------------------------------------------------------------------------
# TS-14-P7: VARIANT_ORDER contains an integer entry for every canonical
#           variant label and None is absent from VARIANT_ORDER
# Requirement: 14-REQ-3.1, 14-REQ-3.3
# ---------------------------------------------------------------------------


class TestVariantOrderCompletenessProperty:
    """Property: VARIANT_ORDER has all canonical labels as int values; None is absent."""

    def test_all_canonical_labels_present_with_int_values(self) -> None:
        """TS-14-P7: Each canonical label maps to an int in VARIANT_ORDER."""
        from agentfox.core.models import VARIANT_ORDER

        for label in ["fast", "standard", "extended"]:
            assert label in VARIANT_ORDER, f"Canonical label '{label}' missing from VARIANT_ORDER"
            assert isinstance(VARIANT_ORDER[label], int), (
                f"VARIANT_ORDER['{label}'] should be int, got {type(VARIANT_ORDER[label])}"
            )

    def test_none_absent_from_variant_order(self) -> None:
        """TS-14-P7: None is not a key in VARIANT_ORDER."""
        from agentfox.core.models import VARIANT_ORDER

        assert None not in VARIANT_ORDER

    def test_canonical_labels_are_strictly_ordered(self) -> None:
        """TS-14-P7 corollary: fast < standard < extended in ordinal values."""
        from agentfox.core.models import VARIANT_ORDER

        assert VARIANT_ORDER["fast"] < VARIANT_ORDER["standard"] < VARIANT_ORDER["extended"]
