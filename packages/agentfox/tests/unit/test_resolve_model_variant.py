"""Tests for resolve_model_variant() 4-layer resolution in sdk_params.py.

Test Spec: TS-14-19, TS-14-20, TS-14-21, TS-14-22, TS-14-23, TS-14-E3
Requirements: 14-REQ-6.1, 14-REQ-6.2, 14-REQ-6.3, 14-REQ-6.4, 14-REQ-6.5, 14-REQ-6.E1
"""

from __future__ import annotations

from unittest.mock import patch

from agentfox.core.config import AgentFoxConfig, ArchetypesConfig, PerArchetypeConfig

# ---------------------------------------------------------------------------
# TS-14-19: Layer 1 — mode-level PerArchetypeConfig.model_variant (highest)
# Requirement: 14-REQ-6.1
# ---------------------------------------------------------------------------


class TestResolveModelVariantLayer1:
    """Verify resolve_model_variant returns mode-level model_variant immediately (Layer 1)."""

    def test_mode_level_model_variant_takes_precedence(self) -> None:
        """TS-14-19: Mode model_variant='extended' overrides archetype model_variant='standard'."""
        from agentfox.engine.sdk_params import resolve_model_variant

        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={
                    "coder": PerArchetypeConfig(
                        model_variant="standard",
                        modes={
                            "code": PerArchetypeConfig(model_variant="extended"),
                        },
                    ),
                }
            )
        )
        result = resolve_model_variant(config, "coder", mode="code")
        assert result == "extended"

    def test_mode_level_fast_variant_takes_precedence(self) -> None:
        """TS-14-19 corollary: Mode model_variant='fast' overrides archetype 'extended'."""
        from agentfox.engine.sdk_params import resolve_model_variant

        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={
                    "reviewer": PerArchetypeConfig(
                        model_variant="extended",
                        modes={
                            "pre-flight": PerArchetypeConfig(model_variant="fast"),
                        },
                    ),
                }
            )
        )
        result = resolve_model_variant(config, "reviewer", mode="pre-flight")
        assert result == "fast"


# ---------------------------------------------------------------------------
# TS-14-20: Layer 2 — archetype-level PerArchetypeConfig.model_variant
# Requirement: 14-REQ-6.2
# ---------------------------------------------------------------------------


class TestResolveModelVariantLayer2:
    """Verify resolve_model_variant returns archetype-level config override (Layer 2)."""

    def test_archetype_level_model_variant_when_mode_is_none(self) -> None:
        """TS-14-20: Archetype config model_variant='extended' returned when mode config is None."""
        from agentfox.engine.sdk_params import resolve_model_variant

        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={
                    "coder": PerArchetypeConfig(
                        model_variant="extended",
                        modes={
                            "code": PerArchetypeConfig(model_variant=None),
                        },
                    ),
                }
            )
        )
        result = resolve_model_variant(config, "coder", mode="code")
        assert result == "extended"

    def test_archetype_level_model_variant_when_no_mode_specified(self) -> None:
        """TS-14-20 corollary: Archetype-level variant returned when mode param is None."""
        from agentfox.engine.sdk_params import resolve_model_variant

        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={
                    "coder": PerArchetypeConfig(model_variant="extended"),
                }
            )
        )
        result = resolve_model_variant(config, "coder", mode=None)
        assert result == "extended"

    def test_archetype_level_model_variant_when_mode_not_in_modes(self) -> None:
        """TS-14-20 corollary: Falls back to archetype-level when mode not found in modes dict."""
        from agentfox.engine.sdk_params import resolve_model_variant

        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={
                    "coder": PerArchetypeConfig(
                        model_variant="extended",
                        modes={},
                    ),
                }
            )
        )
        result = resolve_model_variant(config, "coder", mode="nonexistent-mode")
        assert result == "extended"


# ---------------------------------------------------------------------------
# TS-14-22: Layer 3 — resolve_effective_config returns default_model_variant
# Requirement: 14-REQ-6.4
# ---------------------------------------------------------------------------


class TestResolveModelVariantLayer3:
    """Verify resolve_model_variant falls through to Layer 3 (resolve_effective_config)."""

    def test_layer3_returns_default_model_variant_from_registry(self) -> None:
        """TS-14-22: resolve_effective_config returns ArchetypeEntry with default_model_variant='extended'."""
        from agentfox.archetypes import ArchetypeEntry
        from agentfox.engine.sdk_params import resolve_model_variant

        # No overrides — falls through to Layer 3 (registry default).
        config = AgentFoxConfig()

        mock_entry = ArchetypeEntry(name="coder", default_model_variant="extended")
        with patch("agentfox.engine.sdk_params.get_archetype", return_value=mock_entry):
            result = resolve_model_variant(config, "coder", mode="code")
            assert result == "extended"

    def test_layer3_with_mode_resolved_variant(self) -> None:
        """TS-14-22 corollary: Layer 3 uses resolve_effective_config which applies mode overrides."""
        from agentfox.archetypes import ArchetypeEntry, ModeConfig
        from agentfox.engine.sdk_params import resolve_model_variant

        config = AgentFoxConfig()

        # Archetype has variant='standard' but mode overrides to 'extended'
        mock_entry = ArchetypeEntry(
            name="coder",
            default_model_variant="standard",
            modes={
                "code": ModeConfig(model_variant="extended"),
            },
        )
        with patch("agentfox.engine.sdk_params.get_archetype", return_value=mock_entry):
            result = resolve_model_variant(config, "coder", mode="code")
            assert result == "extended"


# ---------------------------------------------------------------------------
# TS-14-23: All three layers return None
# Requirement: 14-REQ-6.5
# ---------------------------------------------------------------------------


class TestResolveModelVariantAllLayersNone:
    """Verify resolve_model_variant returns None when all layers yield None."""

    def test_all_layers_return_none(self) -> None:
        """TS-14-23: No config overrides, no registry default variant → None."""
        from agentfox.archetypes import ArchetypeEntry
        from agentfox.engine.sdk_params import resolve_model_variant

        # No overrides at any layer
        config = AgentFoxConfig()

        # Archetype has no default_model_variant (defaults to None)
        mock_entry = ArchetypeEntry(name="coder")
        with patch("agentfox.engine.sdk_params.get_archetype", return_value=mock_entry):
            result = resolve_model_variant(config, "coder")
            assert result is None

    def test_all_layers_none_with_mode(self) -> None:
        """TS-14-23 corollary: All layers None even when mode is specified."""
        from agentfox.archetypes import ArchetypeEntry, ModeConfig
        from agentfox.engine.sdk_params import resolve_model_variant

        config = AgentFoxConfig()

        # Archetype has mode but no variant anywhere
        mock_entry = ArchetypeEntry(
            name="coder",
            modes={"code": ModeConfig()},  # no model_variant
        )
        with patch("agentfox.engine.sdk_params.get_archetype", return_value=mock_entry):
            result = resolve_model_variant(config, "coder", mode="code")
            assert result is None
