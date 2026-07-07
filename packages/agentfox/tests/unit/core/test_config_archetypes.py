"""Tests for ArchetypesConfig pydantic model.

Test Spec: TS-26-22 through TS-26-26, TS-26-E9
Requirements: 26-REQ-6.1 through 26-REQ-6.5, 26-REQ-6.E1
"""

from __future__ import annotations

import pytest

# ---------------------------------------------------------------------------
# TS-26-22: ArchetypesConfig has enable/disable toggles
# Requirement: 26-REQ-6.1
# ---------------------------------------------------------------------------


class TestArchetypeToggles:
    """Verify ArchetypesConfig has boolean toggles for each archetype."""

    def test_default_values(self) -> None:
        from agentfox.core.config import ArchetypesConfig

        cfg = ArchetypesConfig()
        assert cfg.reviewer is True
        assert cfg.verifier is True

    def test_disable_reviewer(self) -> None:
        from agentfox.core.config import ArchetypesConfig

        cfg = ArchetypesConfig(reviewer=False, verifier=True)
        assert cfg.reviewer is False
        assert cfg.verifier is True


# ---------------------------------------------------------------------------
# TS-26-23: Instance count configuration
# Requirement: 26-REQ-6.2
# ---------------------------------------------------------------------------


class TestInstanceCounts:
    """Verify archetypes.instances sub-section sets per-archetype counts."""

    def test_default_instances(self) -> None:
        from agentfox.core.config import ArchetypeInstancesConfig

        cfg = ArchetypeInstancesConfig()
        assert cfg.reviewer == 1
        assert cfg.verifier == 1  # 98-REQ-6.2: single instance

    def test_custom_instances(self) -> None:
        from agentfox.core.config import ArchetypeInstancesConfig

        cfg = ArchetypeInstancesConfig(reviewer=3, verifier=1)
        assert cfg.reviewer == 3
        assert cfg.verifier == 1

    def test_instance_clamped_to_5(self) -> None:
        from agentfox.core.config import ArchetypeInstancesConfig

        cfg = ArchetypeInstancesConfig(reviewer=10)
        assert cfg.reviewer == 5

    def test_instance_clamped_to_1(self) -> None:
        from agentfox.core.config import ArchetypeInstancesConfig

        cfg = ArchetypeInstancesConfig(reviewer=0)
        assert cfg.reviewer == 1


# ---------------------------------------------------------------------------
# TS-26-24: Model tier override per archetype
# Requirement: 26-REQ-6.3
# ---------------------------------------------------------------------------


class TestModelTierOverride:
    """Verify per-archetype model tier overrides in config via overrides table."""

    def test_model_override_stored(self) -> None:
        from agentfox.core.config import ArchetypesConfig, PerArchetypeConfig

        cfg = ArchetypesConfig(overrides={"reviewer": PerArchetypeConfig(model_tier="SIMPLE")})
        assert cfg.overrides["reviewer"].model_tier == "SIMPLE"

    def test_empty_overrides_default(self) -> None:
        from agentfox.core.config import ArchetypesConfig

        cfg = ArchetypesConfig()
        assert cfg.overrides == {}


# ---------------------------------------------------------------------------
# TS-26-25: Allowlist override per archetype
# Requirement: 26-REQ-6.4
# ---------------------------------------------------------------------------


class TestAllowlistOverride:
    """Verify per-archetype allowlist overrides in config via overrides table."""

    def test_allowlist_override_stored(self) -> None:
        from agentfox.core.config import ArchetypesConfig, PerArchetypeConfig

        cfg = ArchetypesConfig(overrides={"reviewer": PerArchetypeConfig(allowlist=["ls", "cat"])})
        assert cfg.overrides["reviewer"].allowlist == ["ls", "cat"]

    def test_empty_overrides_default_for_allowlists(self) -> None:
        from agentfox.core.config import ArchetypesConfig

        cfg = ArchetypesConfig()
        assert cfg.overrides == {}


# ---------------------------------------------------------------------------
# TS-26-E9: Missing archetypes config section
# Requirement: 26-REQ-6.E1
# ---------------------------------------------------------------------------


class TestMissingArchetypesSection:
    """Verify missing [archetypes] section uses all defaults."""

    def test_missing_section_uses_defaults(self) -> None:
        from agentfox.core.config import AgentFoxConfig

        # AgentFoxConfig without archetypes should use defaults
        cfg = AgentFoxConfig()
        assert cfg.archetypes.reviewer is True
        assert cfg.archetypes.instances.reviewer == 1

    def test_load_config_without_archetypes(
        self,
        tmp_path: pytest.TempPathFactory,
    ) -> None:
        from agentfox.core.config import load_config

        config_path = tmp_path / "config.toml"  # type: ignore[operator]
        config_path.write_text("[orchestrator]\nparallel = 2\n")

        cfg = load_config(config_path)
        assert cfg.archetypes.reviewer is True
        assert cfg.archetypes.instances.reviewer == 1
