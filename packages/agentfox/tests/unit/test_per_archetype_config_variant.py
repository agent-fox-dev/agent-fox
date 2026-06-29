"""PerArchetypeConfig variant field and TOML mapping tests.

Test Spec: TS-14-15, TS-14-16, TS-14-17, TS-14-18
Requirements: 14-REQ-5.1, 14-REQ-5.2, 14-REQ-5.3, 14-REQ-5.4
"""

from __future__ import annotations

from pathlib import Path

from agentfox.core.config import AgentFoxConfig, PerArchetypeConfig, load_config

# ---------------------------------------------------------------------------
# TS-14-15: PerArchetypeConfig exposes model_variant defaulting to None
# Requirement: 14-REQ-5.1
# ---------------------------------------------------------------------------


class TestPerArchetypeConfigModelVariant:
    """Verify that PerArchetypeConfig exposes model_variant as an optional field defaulting to None."""

    def test_model_variant_defaults_to_none(self) -> None:
        """TS-14-15: PerArchetypeConfig() without model_variant has it as None."""
        config = PerArchetypeConfig()
        assert config.model_variant is None

    def test_model_variant_accepts_string(self) -> None:
        """TS-14-15 corollary: PerArchetypeConfig accepts a string for model_variant."""
        config = PerArchetypeConfig(model_variant="extended")
        assert config.model_variant == "extended"


# ---------------------------------------------------------------------------
# TS-14-16: Parsing model_variant under [archetypes.overrides.<name>]
# Requirement: 14-REQ-5.2
# ---------------------------------------------------------------------------


class TestTomlArchetypeOverrideModelVariant:
    """Verify that model_variant under [archetypes.overrides.<name>] populates correctly."""

    def test_archetype_level_model_variant_parsed(self, tmp_path: Path) -> None:
        """TS-14-16: model_variant under [archetypes.overrides.coder] is populated."""
        config_file = tmp_path / "config.toml"
        config_file.write_text('[archetypes.overrides.coder]\nmodel_variant = "extended"\n')
        config = load_config(path=config_file)
        assert "coder" in config.archetypes.overrides
        assert config.archetypes.overrides["coder"].model_variant == "extended"


# ---------------------------------------------------------------------------
# TS-14-17: Parsing model_variant under [archetypes.overrides.<name>.modes.<mode>]
# Requirement: 14-REQ-5.3
# ---------------------------------------------------------------------------


class TestTomlModeOverrideModelVariant:
    """Verify that model_variant under nested mode config populates correctly."""

    def test_mode_level_model_variant_parsed(self, tmp_path: Path) -> None:
        """TS-14-17: model_variant under [archetypes.overrides.reviewer.modes.fix-review] is populated."""
        config_file = tmp_path / "config.toml"
        config_file.write_text(
            '[archetypes.overrides.reviewer.modes.fix-review]\nmodel_variant = "standard"\n'
        )
        config = load_config(path=config_file)
        assert "reviewer" in config.archetypes.overrides
        reviewer_cfg = config.archetypes.overrides["reviewer"]
        assert "fix-review" in reviewer_cfg.modes
        assert reviewer_cfg.modes["fix-review"].model_variant == "standard"


# ---------------------------------------------------------------------------
# TS-14-18: model_variant outside archetypes.overrides is silently ignored
# Requirement: 14-REQ-5.4
# ---------------------------------------------------------------------------


class TestTomlModelVariantOutsideOverrides:
    """Verify that model_variant at top level or outside archetypes.overrides is silently ignored."""

    def test_top_level_model_variant_ignored(self, tmp_path: Path) -> None:
        """TS-14-18: model_variant at top-level is silently ignored via ConfigDict(extra='ignore')."""
        config_file = tmp_path / "config.toml"
        config_file.write_text('model_variant = "extended"\n')
        config = load_config(path=config_file)
        # Config should parse without error
        assert isinstance(config, AgentFoxConfig)
        # Top-level model_variant should not be accessible
        assert not hasattr(config, "model_variant") or getattr(config, "model_variant", None) is None

    def test_model_variant_under_orchestrator_ignored(self, tmp_path: Path) -> None:
        """TS-14-18 corollary: model_variant under [orchestrator] is silently ignored."""
        config_file = tmp_path / "config.toml"
        config_file.write_text('[orchestrator]\nmodel_variant = "extended"\n')
        config = load_config(path=config_file)
        # Config should parse without error
        assert isinstance(config, AgentFoxConfig)
