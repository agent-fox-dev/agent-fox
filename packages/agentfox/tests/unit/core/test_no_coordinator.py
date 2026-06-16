"""Tests asserting coordinator removal from core modules and templates.

Test Spec: TS-62-3, TS-62-8, TS-62-E1
Requirements: 62-REQ-2.1, 62-REQ-6.1, 62-REQ-6.E1
"""

from __future__ import annotations

from pathlib import Path

# -------------------------------------------------------------------
# TS-62-3: Coordinator Template Deleted
# Requirement: 62-REQ-2.1
# -------------------------------------------------------------------


class TestCoordinatorTemplateDeleted:
    """TS-62-3: Verify coordinator is not a registered archetype."""

    def test_coordinator_not_in_registry(self) -> None:
        """coordinator must not appear in the archetype registry."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY

        assert "coordinator" not in ARCHETYPE_REGISTRY, (
            "coordinator should have been removed from the archetype registry"
        )


# -------------------------------------------------------------------
# TS-62-8: ModelConfig Removed (was: No Coordinator Field)
# Requirement: 62-REQ-6.1
# -------------------------------------------------------------------


class TestModelConfigRemoved:
    """TS-62-8: Verify ModelConfig class is no longer defined."""

    def test_model_config_absent(self) -> None:
        """ModelConfig class must not exist in config module."""
        import agentfox.core.config as config_mod

        assert not hasattr(config_mod, "ModelConfig"), (
            "ModelConfig should have been removed entirely"
        )


# -------------------------------------------------------------------
# TS-62-E1: Config With [models] Section Loads Successfully
# Requirement: 62-REQ-6.E1
# -------------------------------------------------------------------


class TestConfigWithModelsFieldLoadsOk:
    """TS-62-E1: TOML config with [models] section loads without error."""

    def test_config_with_models_section_loads_ok(self, tmp_path: Path) -> None:
        """A config file with [models] section loads successfully (silently ignored)."""
        from agentfox.core.config import load_config

        config_file = tmp_path / "config.toml"
        config_file.write_text('[models]\ncoordinator = "STANDARD"\n')

        # Must not raise — entire [models] section is silently ignored
        config = load_config(path=config_file)
        assert config is not None
