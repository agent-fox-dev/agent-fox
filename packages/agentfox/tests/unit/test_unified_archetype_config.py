"""Unit tests for unified per-archetype configuration (issue #207).

Validates the new [archetypes.overrides.<name>] TOML table syntax and
resolution priority for model_tier, max_turns, thinking, and allowlist.

Requirements: 207-REQ-1, 207-REQ-2, 207-REQ-3
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import MagicMock

import pytest
from agentfox.core.config import (
    AgentFoxConfig,
    ArchetypesConfig,
    PerArchetypeConfig,
    load_config,
)
from agentfox.engine.sdk_params import resolve_max_turns, resolve_thinking
from agentfox.knowledge.db import KnowledgeDB
from pydantic import ValidationError

_MOCK_KB = MagicMock(spec=KnowledgeDB)


# ---------------------------------------------------------------------------
# PerArchetypeConfig — model validation
# ---------------------------------------------------------------------------


class TestPerArchetypeConfigParsing:
    """PerArchetypeConfig parses all fields correctly."""

    def test_all_fields_default_to_none(self) -> None:
        """Default PerArchetypeConfig has all fields as None."""
        cfg = PerArchetypeConfig()
        assert cfg.model_tier is None
        assert cfg.max_turns is None
        assert cfg.thinking_mode is None
        assert cfg.allowlist is None

    def test_max_turns_zero_allowed(self) -> None:
        """0 means unlimited — should be accepted."""
        cfg = PerArchetypeConfig(max_turns=0)
        assert cfg.max_turns == 0

    def test_negative_max_turns_rejected(self) -> None:
        with pytest.raises(ValidationError):
            PerArchetypeConfig(max_turns=-1)

    def test_invalid_thinking_mode_rejected(self) -> None:
        with pytest.raises(ValidationError):
            PerArchetypeConfig(thinking_mode="turbo")  # type: ignore[arg-type]


# ---------------------------------------------------------------------------
# TOML Parsing — [archetypes.overrides.<name>]
# ---------------------------------------------------------------------------


class TestOverridesTomlParsing:
    """[archetypes.overrides.<name>] parses correctly from TOML."""

    def test_single_override_parsed(self, tmp_path: Path) -> None:
        config_file = tmp_path / "config.toml"
        config_file.write_text('[archetypes.overrides.coder]\nmodel_tier = "ADVANCED"\nmax_turns = 200\n')
        config = load_config(path=config_file)
        assert "coder" in config.archetypes.overrides
        coder = config.archetypes.overrides["coder"]
        assert coder.model_tier == "ADVANCED"
        assert coder.max_turns == 200

    def test_multiple_overrides_parsed(self, tmp_path: Path) -> None:
        config_file = tmp_path / "config.toml"
        config_file.write_text(
            "[archetypes.overrides.coder]\n"
            'model_tier = "ADVANCED"\n'
            "max_turns = 200\n"
            "[archetypes.overrides.reviewer]\n"
            'model_tier = "STANDARD"\n'
            "max_turns = 50\n"
        )
        config = load_config(path=config_file)
        assert config.archetypes.overrides["coder"].model_tier == "ADVANCED"
        assert config.archetypes.overrides["reviewer"].model_tier == "STANDARD"
        assert config.archetypes.overrides["reviewer"].max_turns == 50

    def test_thinking_fields_parsed(self, tmp_path: Path) -> None:
        config_file = tmp_path / "config.toml"
        config_file.write_text('[archetypes.overrides.coder]\nthinking_mode = "adaptive"\n')
        config = load_config(path=config_file)
        coder = config.archetypes.overrides["coder"]
        assert coder.thinking_mode == "adaptive"

    def test_allowlist_field_parsed(self, tmp_path: Path) -> None:
        config_file = tmp_path / "config.toml"
        config_file.write_text('[archetypes.overrides.reviewer]\nallowlist = ["ls", "cat", "git"]\n')
        config = load_config(path=config_file)
        assert config.archetypes.overrides["reviewer"].allowlist == ["ls", "cat", "git"]

    def test_overrides_empty_by_default(self) -> None:
        config = AgentFoxConfig()
        assert config.archetypes.overrides == {}

    def test_coexists_with_boolean_enables(self, tmp_path: Path) -> None:
        """overrides and boolean enable flags work together."""
        config_file = tmp_path / "config.toml"
        config_file.write_text('[archetypes]\nreviewer = true\n[archetypes.overrides.coder]\nmodel_tier = "ADVANCED"\n')
        config = load_config(path=config_file)
        assert config.archetypes.overrides["coder"].model_tier == "ADVANCED"


# ---------------------------------------------------------------------------
# resolve_max_turns — priority: overrides > registry
# ---------------------------------------------------------------------------


class TestResolveMaxTurnsWithOverrides:
    """resolve_max_turns checks overrides first."""

    def test_override_takes_precedence_over_registry(self) -> None:
        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={"reviewer": PerArchetypeConfig(max_turns=40)},
            )
        )
        assert resolve_max_turns(config, "reviewer") == 40

    def test_override_zero_means_unlimited(self) -> None:
        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={"coder": PerArchetypeConfig(max_turns=0)},
            )
        )
        assert resolve_max_turns(config, "coder") is None

    def test_registry_default_used_when_no_override(self) -> None:
        """No override → registry default."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY

        config = AgentFoxConfig()
        expected = ARCHETYPE_REGISTRY["coder"].default_max_turns
        assert resolve_max_turns(config, "coder") == expected

    def test_override_none_max_turns_falls_through_to_registry(self) -> None:
        """PerArchetypeConfig with max_turns=None falls through to registry default."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY

        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={"coder": PerArchetypeConfig(max_turns=None)},
            )
        )
        expected = ARCHETYPE_REGISTRY["coder"].default_max_turns
        assert resolve_max_turns(config, "coder") == expected


# ---------------------------------------------------------------------------
# resolve_thinking — priority: overrides > registry
# ---------------------------------------------------------------------------


class TestResolveThinkingWithOverrides:
    """resolve_thinking checks overrides first."""

    def test_override_thinking_mode_enabled(self) -> None:
        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={"reviewer": PerArchetypeConfig(thinking_mode="adaptive")},
            )
        )
        result = resolve_thinking(config, "reviewer")
        assert result == {"type": "adaptive"}

    def test_override_thinking_mode_adaptive(self) -> None:
        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={"verifier": PerArchetypeConfig(thinking_mode="adaptive")},
            )
        )
        result = resolve_thinking(config, "verifier")
        assert result is not None
        assert result["type"] == "adaptive"
        assert "budget_tokens" not in result

    def test_override_thinking_mode_disabled(self) -> None:
        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={"coder": PerArchetypeConfig(thinking_mode="disabled")},
            )
        )
        result = resolve_thinking(config, "coder")
        assert result is None

    def test_override_none_thinking_mode_falls_through_to_registry(self) -> None:
        """PerArchetypeConfig with thinking_mode=None falls through to registry default."""
        from agentfox.archetypes import ARCHETYPE_REGISTRY

        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={"coder": PerArchetypeConfig(thinking_mode=None)},
            )
        )
        # thinking_mode is None in override → check registry
        result = resolve_thinking(config, "coder")
        coder_entry = ARCHETYPE_REGISTRY["coder"]
        if coder_entry.default_thinking_mode == "disabled":
            assert result is None
        else:
            assert result == {
                "type": coder_entry.default_thinking_mode,
            }


# ---------------------------------------------------------------------------
# _resolve_model_tier — priority: overrides > registry
# ---------------------------------------------------------------------------


class TestResolveModelTierWithOverrides:
    """NodeSessionRunner._resolve_model_tier checks overrides first."""

    def test_override_model_tier_takes_precedence(self) -> None:
        from agentfox.engine.session_lifecycle import NodeSessionRunner

        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={"coder": PerArchetypeConfig(model_tier="ADVANCED")},
            )
        )
        runner = NodeSessionRunner("spec:1", config, archetype="coder", knowledge_db=_MOCK_KB)
        # ADVANCED → claude-opus-4-6
        assert runner._resolved_model_id == "claude-opus-4-6"

    def test_override_standard_tier(self) -> None:
        from agentfox.engine.session_lifecycle import NodeSessionRunner

        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={"reviewer": PerArchetypeConfig(model_tier="STANDARD")},
            )
        )
        runner = NodeSessionRunner("spec:0", config, archetype="reviewer", knowledge_db=_MOCK_KB)
        # STANDARD → claude-sonnet-4-6
        assert runner._resolved_model_id == "claude-sonnet-4-6"

    def test_override_none_model_tier_falls_through_to_registry(self) -> None:
        from agentfox.engine.session_lifecycle import NodeSessionRunner

        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={"coder": PerArchetypeConfig(model_tier=None)},
            )
        )
        runner = NodeSessionRunner("spec:1", config, archetype="coder", knowledge_db=_MOCK_KB)
        # Falls through to registry default (STANDARD for coder)
        assert runner._resolved_model_id == "claude-sonnet-4-6"


# ---------------------------------------------------------------------------
# _resolve_security_config — priority: overrides > registry
# ---------------------------------------------------------------------------


class TestResolveSecurityConfigWithOverrides:
    """resolve_security_config checks overrides first."""

    def test_override_allowlist_takes_precedence(self) -> None:
        from agentfox.engine.sdk_params import resolve_security_config

        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={"coder": PerArchetypeConfig(allowlist=["git", "grep"])},
            )
        )
        sec = resolve_security_config(config, "coder")
        assert sec is not None
        assert sec.bash_allowlist == ["git", "grep"]

    def test_override_none_allowlist_falls_through_to_registry(self) -> None:
        from agentfox.engine.sdk_params import resolve_security_config

        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={"coder": PerArchetypeConfig(allowlist=None)},
            )
        )
        # Coder has no default allowlist in the registry → returns None
        sec = resolve_security_config(config, "coder")
        assert sec is None

    def test_override_empty_allowlist_not_treated_as_none(self) -> None:
        """An explicit empty list [] is a valid allowlist (no commands allowed)."""
        from agentfox.engine.sdk_params import resolve_security_config

        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={"coder": PerArchetypeConfig(allowlist=[])},
            )
        )
        sec = resolve_security_config(config, "coder")
        assert sec is not None
        assert sec.bash_allowlist == []


# ---------------------------------------------------------------------------
# End-to-end TOML loading and resolution
# ---------------------------------------------------------------------------


class TestEndToEndTomlResolution:
    """Full path: TOML file → load_config → resolve functions."""

    def test_model_tier_from_toml_overrides_table(self, tmp_path: Path) -> None:
        from agentfox.engine.session_lifecycle import NodeSessionRunner

        config_file = tmp_path / "config.toml"
        config_file.write_text('[archetypes.overrides.reviewer]\nmodel_tier = "STANDARD"\n')
        config = load_config(path=config_file)
        runner = NodeSessionRunner("spec:0", config, archetype="reviewer", knowledge_db=_MOCK_KB)
        # Registry default for reviewer is STANDARD, override also STANDARD → sonnet
        assert runner._resolved_model_id == "claude-sonnet-4-6"

    def test_max_turns_from_toml_overrides_table(self, tmp_path: Path) -> None:
        config_file = tmp_path / "config.toml"
        config_file.write_text("[archetypes.overrides.coder]\nmax_turns = 50\n")
        config = load_config(path=config_file)
        assert resolve_max_turns(config, "coder") == 50

    def test_thinking_from_toml_overrides_table(self, tmp_path: Path) -> None:
        config_file = tmp_path / "config.toml"
        config_file.write_text('[archetypes.overrides.verifier]\nthinking_mode = "adaptive"\n')
        config = load_config(path=config_file)
        result = resolve_thinking(config, "verifier")
        assert result == {"type": "adaptive"}

    def test_overrides_max_turns_from_toml(self, tmp_path: Path) -> None:
        """archetypes.overrides.coder.max_turns from TOML resolves correctly."""
        config_file = tmp_path / "config.toml"
        config_file.write_text("[archetypes.overrides.coder]\nmax_turns = 150\n")
        config = load_config(path=config_file)
        assert resolve_max_turns(config, "coder") == 150

    def test_overrides_thinking_from_toml(self, tmp_path: Path) -> None:
        """archetypes.overrides.coder thinking fields from TOML resolve correctly."""
        config_file = tmp_path / "config.toml"
        config_file.write_text('[archetypes.overrides.coder]\nthinking_mode = "adaptive"\n')
        config = load_config(path=config_file)
        result = resolve_thinking(config, "coder")
        assert result == {"type": "adaptive"}
