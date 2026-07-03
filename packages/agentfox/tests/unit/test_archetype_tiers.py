"""Unit tests for archetype model tier defaults.

Test Spec: TS-57-1 through TS-57-14, TS-57-E1 through TS-57-E3
Requirements: 57-REQ-1.1 through 57-REQ-3.E1, 57-REQ-4.1 through 57-REQ-4.3

Updated for spec 98 (reviewer consolidation):
- skeptic/oracle → reviewer (STANDARD base, fix-review mode = ADVANCED)
- verifier → STANDARD (was ADVANCED, per 98-REQ-6.1)
- auditor → reviewer:audit-review (STANDARD)
"""

from __future__ import annotations

from unittest.mock import MagicMock

import pytest
from agentfox.core.config import (
    AgentFoxConfig,
    ArchetypesConfig,
    PerArchetypeConfig,
)
from agentfox.core.errors import ConfigError
from agentfox.engine.session_lifecycle import NodeSessionRunner
from agentfox.knowledge.db import KnowledgeDB

_MOCK_KB = MagicMock(spec=KnowledgeDB)


# ---------------------------------------------------------------------------
# TS-57-1/2 (updated): Reviewer base defaults to STANDARD
# Requirement: 57-REQ-1.1, 57-REQ-1.2 (updated by 98-REQ-1.1)
# ---------------------------------------------------------------------------


class TestReviewerDefaultStandard:
    """Verify reviewer archetype base defaults to STANDARD (was skeptic/oracle ADVANCED)."""

    def test_reviewer_default_tier_is_standard(self) -> None:
        from agentfox.archetypes import ARCHETYPE_REGISTRY

        entry = ARCHETYPE_REGISTRY["reviewer"]
        assert entry.default_model_tier == "STANDARD"


# ---------------------------------------------------------------------------
# TS-57-3 (updated): Verifier Default Tier Is STANDARD
# Requirement: 57-REQ-1.3 (updated by 98-REQ-6.1)
# ---------------------------------------------------------------------------


class TestVerifierDefaultStandard:
    """TS-57-3 (updated): Verify Verifier defaults to STANDARD (was ADVANCED)."""

    def test_verifier_default_tier_is_standard(self) -> None:
        from agentfox.archetypes import ARCHETYPE_REGISTRY

        entry = ARCHETYPE_REGISTRY["verifier"]
        assert entry.default_model_tier == "STANDARD"


# ---------------------------------------------------------------------------
# TS-57-4: Coder Default Tier Is STANDARD
# Requirement: 57-REQ-1.4
# ---------------------------------------------------------------------------


class TestCoderDefaultStandard:
    """TS-57-4: Verify Coder archetype defaults to STANDARD.

    Spec 15 moved coder from ADVANCED to STANDARD as the registry default.
    """

    def test_coder_default_tier_is_standard(self) -> None:
        """ARCHETYPE_REGISTRY["coder"].default_model_tier must be STANDARD (spec 15)."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY

        entry = ARCHETYPE_REGISTRY["coder"]
        assert entry.default_model_tier == "STANDARD"


# ---------------------------------------------------------------------------
# TS-57-5 (updated): All base archetypes default to STANDARD
# Requirement: 57-REQ-1.5 (updated by 98-REQ-1.1, 98-REQ-6.1)
# ---------------------------------------------------------------------------


class TestRemainingArchetypesStandard:
    """reviewer, verifier, and coder default to STANDARD tier.

    Spec 15 moved coder from ADVANCED to STANDARD.
    """

    @pytest.mark.parametrize("name", ["reviewer", "curator", "verifier"])
    def test_archetype_defaults_to_standard(self, name: str) -> None:
        from agentfox.archetypes import ARCHETYPE_REGISTRY

        entry = ARCHETYPE_REGISTRY[name]
        assert entry.default_model_tier == "STANDARD", (
            f"{name} should default to STANDARD, got {entry.default_model_tier!r}"
        )


# ---------------------------------------------------------------------------
# TS-57-9: Config Override Takes Precedence
# Requirement: 57-REQ-3.1
# ---------------------------------------------------------------------------


class TestConfigOverridePrecedence:
    """TS-57-9: Config override for an archetype takes precedence over registry."""

    def test_config_override_takes_precedence(self) -> None:
        """archetypes.overrides.coder.model_tier = ADVANCED overrides STANDARD registry default."""
        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(overrides={"coder": PerArchetypeConfig(model_tier="ADVANCED")})
        )
        runner = NodeSessionRunner("spec:1", config, knowledge_db=_MOCK_KB)
        # With override "ADVANCED", model should be Opus
        assert runner._resolved_model_id == "claude-opus-4-6"


# ---------------------------------------------------------------------------
# TS-57-10: No Config Override Falls Back to Registry
# Requirement: 57-REQ-3.2
# ---------------------------------------------------------------------------


class TestNoOverrideUsesRegistry:
    """TS-57-10: Without config override, registry default is used."""

    def test_no_override_uses_registry_default(self) -> None:
        """Reviewer with no override should use registry default (STANDARD = Sonnet)."""
        config = AgentFoxConfig()
        runner = NodeSessionRunner("spec:0", config, archetype="reviewer", knowledge_db=_MOCK_KB)
        assert runner._resolved_model_id == "claude-sonnet-4-6"


# ---------------------------------------------------------------------------
# TS-57-11: Assessed Tier Overrides Everything
# Requirement: 57-REQ-3.3
# ---------------------------------------------------------------------------


# ---------------------------------------------------------------------------
# TS-57-E1: Unknown Archetype Falls Back to Coder
# Requirement: 57-REQ-1.E1
# ---------------------------------------------------------------------------


class TestUnknownArchetypeFallback:
    """TS-57-E1: Unknown archetype name falls back to Coder entry."""

    def test_unknown_archetype_returns_coder(self) -> None:
        from agentfox.archetypes import get_archetype

        entry = get_archetype("unknown_archetype_xyz")
        assert entry.name == "coder"
        # Coder registry default is STANDARD (spec 15)
        assert entry.default_model_tier == "STANDARD"


# ---------------------------------------------------------------------------
# TS-57-E3: Invalid Config Tier Raises ConfigError
# Requirement: 57-REQ-3.E1
# ---------------------------------------------------------------------------


class TestInvalidConfigTierRaises:
    """TS-57-E3: Invalid tier name in config raises ConfigError."""

    def test_invalid_config_tier_raises_config_error(self) -> None:
        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(overrides={"coder": PerArchetypeConfig(model_tier="INVALID_TIER")})
        )
        with pytest.raises(ConfigError):
            NodeSessionRunner("spec:1", config, archetype="coder", knowledge_db=_MOCK_KB)
