"""Remaining property tests for model variant support.

Test Spec: TS-14-P5, TS-14-P6, TS-14-P7
Requirements: 14-REQ-4.3, 14-REQ-13.1, 14-REQ-6.3, 14-REQ-6.E1,
              14-REQ-3.1, 14-REQ-3.3
"""

from __future__ import annotations

from unittest.mock import patch

import pytest
from agentfox.core.config import AgentFoxConfig, ArchetypesConfig

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
    def test_mode_variant_wins_over_archetype_variant(
        self, mode_variant: str, arch_variant: str
    ) -> None:
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
# TS-14-P6: For any archetype matched by the legacy config.archetypes.models
#           dict, resolve_model_variant returns None and never invokes
#           resolve_effective_config
# Requirement: 14-REQ-6.3, 14-REQ-6.E1
# ---------------------------------------------------------------------------


class TestLegacyDictShortCircuitProperty:
    """Property: legacy dict always short-circuits to None without resolve_effective_config."""

    @pytest.mark.parametrize("archetype_name", ["coder", "reviewer", "verifier"])
    def test_legacy_dict_returns_none_and_skips_resolve_effective_config(
        self, archetype_name: str
    ) -> None:
        """TS-14-P6: For each archetype in legacy dict, result is None and
        resolve_effective_config is never called.
        """
        from agentfox.engine.sdk_params import resolve_model_variant

        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                models={archetype_name: "ADVANCED"},
            )
        )
        with patch("agentfox.archetypes.resolve_effective_config") as mocked_rec:
            result = resolve_model_variant(config, archetype_name)
            assert result is None
            assert mocked_rec.call_count == 0

    @pytest.mark.parametrize("archetype_name", ["coder", "reviewer"])
    def test_legacy_dict_short_circuit_with_mode_specified(
        self, archetype_name: str
    ) -> None:
        """TS-14-P6 corollary: Legacy dict short-circuit applies even when mode is specified."""
        from agentfox.engine.sdk_params import resolve_model_variant

        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                models={archetype_name: "STANDARD"},
            )
        )
        with patch("agentfox.archetypes.resolve_effective_config") as mocked_rec:
            result = resolve_model_variant(config, archetype_name, mode="test-mode")
            assert result is None
            assert mocked_rec.call_count == 0


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
